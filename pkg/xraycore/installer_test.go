package xraycore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

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
		t.Fatal(err)
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

	fakeBin := filepath.Join(tmpDir, "xray-v1.0.0")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nsleep 10\n"), 0755); err != nil {
		t.Fatal(err)
	}

	pm := NewProcessManager(tmpDir, "v1.0.0")
	err = pm.Start("/dev/null")
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	if pm.cmd == nil || pm.cmd.Process == nil {
		t.Fatalf("expected running process")
	}

	err = pm.Stop()
	if err != nil {
		t.Fatalf("failed to stop: %v", err)
	}

	if pm.cmd != nil {
		t.Fatalf("expected cmd to be nil after stop")
	}
}
