package xraycore

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
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
	pm := NewProcessManager("/tmp/cache", "v26.0.0")
	if pm.BinaryPath() != "/tmp/cache/xray-v26.0.0" {
		t.Errorf("unexpected binary path: %s", pm.BinaryPath())
	}
}

func TestProcessManager_Restart(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "xray-restart-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	fakeBin := filepath.Join(tmpDir, "xray-v26.0.0")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nsleep 10\n"), 0755); err != nil {
		t.Fatal(err)
	}

	pm := NewProcessManager(tmpDir, "v26.0.0")
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
