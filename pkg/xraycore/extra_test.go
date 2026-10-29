package xraycore

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func createFakeXrayZip() ([]byte, error) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// Add 'xray' binary to zip
	w, err := zw.Create("xray")
	if err != nil {
		return nil, err
	}
	_, err = w.Write([]byte("#!/bin/sh\necho xray v26.0.0\n"))
	if err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func TestProcessManager_BinaryPath(t *testing.T) {
	pm := NewProcessManager(filepath.Join("tmp", "cache"), "v26.0.0")
	expected := filepath.Join("tmp", "cache", "xray-v26.0.0")
	if pm.BinaryPath() != expected {
		t.Errorf("unexpected binary path: got %s, expected %s", pm.BinaryPath(), expected)
	}
}

func TestProcessManager_Restart(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "xray-restart-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	ver := testVersion("v26.0.0")
	dest := filepath.Join(tmpDir, fmt.Sprintf("xray-%s", ver))
	copyTestBinary(t, dest)

	pm := NewProcessManager(tmpDir, ver)
	if err := pm.Start("/dev/null"); err != nil {
		t.Fatalf("first start failed: %v", err)
	}

	firstPID := pm.cmd.Process.Pid

	if err := pm.Restart("/dev/null"); err != nil {
		t.Fatalf("restart failed: %v", err)
	}

	secondPID := pm.cmd.Process.Pid
	if firstPID == secondPID {
		t.Errorf("expected different PID after restart")
	}

	_ = pm.Stop()
}

func TestProcessManager_AutoRecovery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "xray-autorecovery-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	ver := testVersion("v26.0.0")
	dest := filepath.Join(tmpDir, fmt.Sprintf("xray-%s", ver))
	copyTestBinary(t, dest)

	var restartedCount int32
	pm := NewProcessManager(tmpDir, ver)
	pm.SetOnRestart(func() {
		atomic.AddInt32(&restartedCount, 1)
	})

	if err := pm.Start("/dev/null"); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	if !pm.IsRunning() {
		t.Fatalf("expected running process")
	}

	firstPID := pm.cmd.Process.Pid

	// Simulate sudden crash by killing process without Stop()
	_ = pm.cmd.Process.Kill()

	// Wait for supervisor to auto-restart (backoff is ~1s)
	deadline := time.Now().Add(4 * time.Second)
	recovered := false
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if pm.IsRunning() && pm.cmd != nil && pm.cmd.Process != nil && pm.cmd.Process.Pid != firstPID {
			recovered = true
			break
		}
	}

	if !recovered {
		t.Fatalf("process was not auto-recovered within deadline")
	}

	if atomic.LoadInt32(&restartedCount) < 1 {
		t.Errorf("expected onRestart callback to be invoked at least once, got %d", atomic.LoadInt32(&restartedCount))
	}

	_ = pm.Stop()
	if pm.IsRunning() {
		t.Errorf("expected process to be stopped")
	}
}

func TestProcessManager_ExtractZipLogic(t *testing.T) {
	zipBytes, err := createFakeXrayZip()
	if err != nil {
		t.Fatalf("failed to create zip: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "xray-extract-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tmpZip := filepath.Join(tmpDir, "xray-v26.0.0.zip.tmp")
	if err := os.WriteFile(tmpZip, zipBytes, 0644); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(tmpZip)
	if err != nil {
		t.Fatalf("zip.OpenReader failed: %v", err)
	}
	defer zr.Close()

	destBin := filepath.Join(tmpDir, "xray-v26.0.0")
	var found bool
	for _, file := range zr.File {
		if file.Name == "xray" {
			found = true
			rc, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()

			f, err := os.OpenFile(destBin, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			break
		}
	}

	if !found {
		t.Fatalf("xray not found in fake zip")
	}
}
