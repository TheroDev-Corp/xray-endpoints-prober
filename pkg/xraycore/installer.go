package xraycore

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type ProcessManager struct {
	cacheDir string
	version  string
	binPath  string
	cmd      *exec.Cmd
	mu       sync.Mutex
	stopCh   chan struct{}
	waitDone chan struct{}
}

func NewProcessManager(cacheDir, version string) *ProcessManager {
	return &ProcessManager{
		cacheDir: cacheDir,
		version:  version,
		binPath:  filepath.Join(cacheDir, fmt.Sprintf("xray-%s", version)),
	}
}

func (pm *ProcessManager) BinaryPath() string {
	return pm.binPath
}

func (pm *ProcessManager) EnsureBinary(ctx context.Context) error {
	if err := os.MkdirAll(pm.cacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache dir: %w", err)
	}

	// Check if custom or system xray already exists
	if _, err := os.Stat(pm.binPath); err == nil {
		log.Printf("[xraycore] binary found in local cache: %s", pm.binPath)
		return nil
	}
	log.Printf("[xraycore] binary not found in cache (%s), proceeding to fetch...", pm.binPath)

	// Check if system 'xray' is available in PATH
	if path, err := exec.LookPath("xray"); err == nil {
		log.Printf("Found system xray binary at %s, creating symlink to %s", path, pm.binPath)
		_ = os.Remove(pm.binPath)
		if err := os.Symlink(path, pm.binPath); err == nil {
			return nil
		}
	}

	// Download from GitHub releases
	return pm.downloadRelease(ctx)
}

func (pm *ProcessManager) downloadRelease(ctx context.Context) error {
	arch := runtime.GOARCH
	var zipArch string
	switch arch {
	case "amd64":
		zipArch = "64"
	case "arm64":
		zipArch = "arm64-v8a"
	case "arm":
		zipArch = "arm32-v7a"
	case "386":
		zipArch = "32"
	default:
		return fmt.Errorf("unsupported architecture: %s", arch)
	}

	v := strings.TrimPrefix(pm.version, "v")
	fileName := fmt.Sprintf("Xray-linux-%s.zip", zipArch)
	url := fmt.Sprintf("https://github.com/XTLS/Xray-core/releases/download/v%s/%s", v, fileName)

	log.Printf("Downloading Xray %s from %s ...", pm.version, url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "xray-endpoints-prober")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download xray: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status downloading xray: %s", resp.Status)
	}

	tmpZipFile := filepath.Join(pm.cacheDir, fmt.Sprintf("xray-%s.zip.tmp", pm.version))
	f, err := os.OpenFile(tmpZipFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create tmp zip file: %w", err)
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmpZipFile)
		return fmt.Errorf("failed to save zip file: %w", err)
	}
	f.Close()
	defer os.Remove(tmpZipFile)

	// Unzip xray binary
	zr, err := zip.OpenReader(tmpZipFile)
	if err != nil {
		return fmt.Errorf("failed to open zip reader: %w", err)
	}
	defer zr.Close()

	var foundXray bool
	for _, file := range zr.File {
		if file.Name == "xray" || file.Name == "xray.exe" {
			foundXray = true
			rc, err := file.Open()
			if err != nil {
				return fmt.Errorf("failed to open zipped file %s: %w", file.Name, err)
			}
			tmpBin := pm.binPath + ".tmp"
			outF, err := os.OpenFile(tmpBin, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				rc.Close()
				return fmt.Errorf("failed to create target binary %s: %w", tmpBin, err)
			}
			if _, err := io.Copy(outF, rc); err != nil {
				outF.Close()
				rc.Close()
				os.Remove(tmpBin)
				return fmt.Errorf("failed to write binary: %w", err)
			}
			outF.Close()
			rc.Close()

			if err := os.Rename(tmpBin, pm.binPath); err != nil {
				return fmt.Errorf("failed to finalize binary: %w", err)
			}
			break
		}
	}

	if !foundXray {
		return fmt.Errorf("xray binary not found inside downloaded archive")
	}

	log.Printf("Successfully cached Xray binary at %s", pm.binPath)
	return nil
}

func (pm *ProcessManager) Start(configPath string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.cmd != nil && pm.cmd.Process != nil {
		// Stop existing
		_ = pm.stopLocked()
	}

	cmd := exec.Command(pm.binPath, "run", "-config", configPath)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	// Guard against memory leaks in child Xray process: set GOMEMLIMIT if not explicitly configured
	var hasMemLimit bool
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "GOMEMLIMIT=") {
			hasMemLimit = true
			break
		}
	}
	if !hasMemLimit {
		cmd.Env = append(cmd.Env, "GOMEMLIMIT=150MiB")
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start xray process: %w", err)
	}

	pm.cmd = cmd
	pm.stopCh = make(chan struct{})
	pm.waitDone = make(chan struct{})

	go func(c *exec.Cmd, stopCh, waitDone chan struct{}) {
		defer close(waitDone)
		err := c.Wait()
		select {
		case <-stopCh:
			// intentional stop
		default:
			log.Printf("xray process exited unexpectedly: %v", err)
		}
	}(cmd, pm.stopCh, pm.waitDone)

	return nil
}

func (pm *ProcessManager) Stop() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.stopLocked()
}

func (pm *ProcessManager) stopLocked() error {
	if pm.cmd == nil || pm.cmd.Process == nil {
		return nil
	}
	close(pm.stopCh)

	// Send SIGTERM
	_ = pm.cmd.Process.Signal(os.Interrupt)

	select {
	case <-pm.waitDone:
		// Process cleanly exited
	case <-time.After(3 * time.Second):
		_ = pm.cmd.Process.Kill()
		<-pm.waitDone
	}

	pm.cmd = nil
	pm.waitDone = nil
	return nil
}

func (pm *ProcessManager) Restart(configPath string) error {
	return pm.Start(configPath)
}

// FindBinaryInPath searches for binary in PATH
func FindBinaryInPath(name string) (string, error) {
	return exec.LookPath(name)
}
