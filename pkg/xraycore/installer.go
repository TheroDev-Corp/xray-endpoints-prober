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
	"sync/atomic"
	"time"
)

type ProcessManager struct {
	cacheDir       string
	version        string
	binPath        string
	cmd            *exec.Cmd
	mu             sync.Mutex
	stopCh         chan struct{}
	waitDone       chan struct{}
	xrayMemLimitMB int

	// Supervision & recovery state
	configPath string
	isRunning  atomic.Bool
	isStopping bool
	onRestart  func()
	onFatal    func(error)
	crashTimes []time.Time
}

func NewProcessManager(cacheDir, version string, memLimitMB ...int) *ProcessManager {
	limit := 50
	if len(memLimitMB) > 0 && memLimitMB[0] > 0 {
		limit = memLimitMB[0]
	}
	return &ProcessManager{
		cacheDir:       cacheDir,
		version:        version,
		binPath:        filepath.Join(cacheDir, fmt.Sprintf("xray-%s", version)),
		xrayMemLimitMB: limit,
	}
}

func (pm *ProcessManager) BinaryPath() string {
	return pm.binPath
}

func (pm *ProcessManager) IsRunning() bool {
	return pm.isRunning.Load()
}

func (pm *ProcessManager) SetOnRestart(fn func()) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.onRestart = fn
}

func (pm *ProcessManager) SetOnFatal(fn func(error)) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.onFatal = fn
}

func (pm *ProcessManager) SetMemoryLimitMB(limitMB int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if limitMB > 0 {
		pm.xrayMemLimitMB = limitMB
	}
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
		_ = pm.stopLocked()
	}

	pm.crashTimes = nil
	return pm.startLocked(configPath)
}

func (pm *ProcessManager) startLocked(configPath string) error {
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
		cmd.Env = append(cmd.Env, fmt.Sprintf("GOMEMLIMIT=%dMiB", pm.xrayMemLimitMB))
	}

	if err := cmd.Start(); err != nil {
		pm.isRunning.Store(false)
		return fmt.Errorf("failed to start xray process: %w", err)
	}

	pm.cmd = cmd
	pm.configPath = configPath
	pm.isStopping = false
	pm.isRunning.Store(true)
	pm.stopCh = make(chan struct{})
	pm.waitDone = make(chan struct{})

	stopCh := pm.stopCh
	waitDone := pm.waitDone

	go func(c *exec.Cmd, sCh, wDone chan struct{}, cfg string) {
		err := c.Wait()
		close(wDone)

		select {
		case <-sCh:
			// intentional stop
			return
		default:
			// unexpected exit!
		}

		pm.mu.Lock()
		if pm.isStopping {
			pm.mu.Unlock()
			return
		}

		pm.cmd = nil
		pm.isRunning.Store(false)

		log.Printf("[xraycore] ERROR: xray process exited unexpectedly: %v", err)

		now := time.Now()
		pm.crashTimes = append(pm.crashTimes, now)

		// Prune crashes older than 60 seconds
		var recent []time.Time
		cutoff := now.Add(-60 * time.Second)
		for _, t := range pm.crashTimes {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		pm.crashTimes = recent

		if len(recent) >= 5 {
			log.Printf("[xraycore] FATAL: xray process crash loop detected (%d crashes in 60s), aborting auto-restart", len(recent))
			fatalErr := fmt.Errorf("xray process crash loop detected: %d crashes in 60s (last err: %w)", len(recent), err)
			fatalCb := pm.onFatal
			pm.mu.Unlock()
			if fatalCb != nil {
				fatalCb(fatalErr)
			}
			return
		}

		restartCb := pm.onRestart
		pm.mu.Unlock()

		// Brief backoff before auto-restart
		time.Sleep(1 * time.Second)

		pm.mu.Lock()
		if pm.isStopping || pm.configPath == "" {
			pm.mu.Unlock()
			return
		}
		log.Printf("[xraycore] Initiating auto-recovery of Xray process with config %s...", cfg)
		restartErr := pm.startLocked(cfg)
		pm.mu.Unlock()

		if restartErr != nil {
			log.Printf("[xraycore] Failed to auto-recover xray process: %v", restartErr)
		} else {
			log.Printf("[xraycore] Xray process successfully recovered and restarted")
			if restartCb != nil {
				restartCb()
			}
		}
	}(cmd, stopCh, waitDone, configPath)

	return nil
}

func (pm *ProcessManager) Stop() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.stopLocked()
}

func (pm *ProcessManager) stopLocked() error {
	pm.isStopping = true
	pm.isRunning.Store(false)
	if pm.cmd == nil || pm.cmd.Process == nil {
		return nil
	}
	close(pm.stopCh)

	// Send SIGTERM/Interrupt on Unix, Kill on Windows
	if runtime.GOOS == "windows" {
		_ = pm.cmd.Process.Kill()
	} else {
		_ = pm.cmd.Process.Signal(os.Interrupt)
	}

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

