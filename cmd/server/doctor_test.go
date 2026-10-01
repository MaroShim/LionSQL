package main

import (
	"os"
	"path/filepath"
	"testing"

	"sqlion/pkg/config"
)

func TestDoctor_RunDiagnostics(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sqlion_doctor_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	cfg := config.DefaultConfig()
	cfg.Server.DataDir = filepath.Join(tmpDir, "data")
	cfg.Network.HTTPAddr = "127.0.0.1:28090"
	cfg.Network.PGAddr = "127.0.0.1:25433"
	cfg.Network.TDSAddr = "127.0.0.1:21433"

	doc := NewDoctor(cfg)
	passed, warnings, failures := doc.RunDiagnostics()

	if failures > 0 {
		t.Errorf("Expected 0 failures in standard environment, got %d failures", failures)
	}
	if passed == 0 {
		t.Errorf("Expected positive passed checks, got 0")
	}

	t.Logf("Doctor results: %d passed, %d warnings, %d failures", passed, warnings, failures)
	doc.PrintReport()
}

func TestDoctor_RunDoctorFunc(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sqlion_doctor_func_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	cfg := config.DefaultConfig()
	cfg.Server.DataDir = filepath.Join(tmpDir, "data")
	cfg.Network.HTTPAddr = "127.0.0.1:28091"
	cfg.Network.PGAddr = "127.0.0.1:25434"
	cfg.Network.TDSAddr = "127.0.0.1:21434"

	if err := RunDoctor(cfg); err != nil {
		t.Fatalf("RunDoctor failed: %v", err)
	}
}
