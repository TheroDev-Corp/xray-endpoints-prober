package xraycore

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func init() {
	if len(os.Args) > 1 && os.Args[1] == "run" {
		// Mock Xray process: listen for termination signals
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		os.Exit(0)
	}
}

func copyTestBinary(t *testing.T, dest string) {
	t.Helper()
	src, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	dst, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
}

func testVersion(ver string) string {
	if runtime.GOOS == "windows" {
		return ver + ".exe"
	}
	return ver
}

func TestEnsureBinaryWithSymlink(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "xray-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	fakeBin := filepath.Join(tmpDir, "fake-xray")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\necho xray version 1.0.0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	pm := NewProcessManager(filepath.Join(tmpDir, "cache"), "v1.0.0")
	_ = os.MkdirAll(pm.cacheDir, 0755)
	if err := os.Symlink(fakeBin, pm.binPath); err != nil {
		t.Skipf("symlink not supported in current environment: %v", err)
	}

	err = pm.EnsureBinary(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(pm.binPath); err != nil {
		t.Fatalf("expected binary at %s, got error: %v", pm.binPath, err)
	}
}

func TestProcessManagerStartStop(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "xray-proc-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	ver := testVersion("v1.0.0")
	dest := filepath.Join(tmpDir, fmt.Sprintf("xray-%s", ver))
	copyTestBinary(t, dest)

	pm := NewProcessManager(tmpDir, ver)
	err = pm.Start("/dev/null")
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	if !pm.IsRunning() {
		t.Fatalf("expected pm.IsRunning() to be true")
	}

	if pm.cmd == nil || pm.cmd.Process == nil {
		t.Fatalf("expected running process")
	}

	err = pm.Stop()
	if err != nil {
		t.Fatalf("failed to stop: %v", err)
	}

	if pm.IsRunning() {
		t.Fatalf("expected pm.IsRunning() to be false after stop")
	}

	if pm.cmd != nil {
		t.Fatalf("expected cmd to be nil after stop")
	}
}
