package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEngineManifest_FreshInitAndAutoAdoptOnRestart(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Fresh initialization with compact (memory) engine
	m1, err := ResolveOrWriteEngineManifest(tmpDir, "compact", "compact", true, false)
	if err != nil {
		t.Fatalf("expected fresh init to succeed, got: %v", err)
	}
	if m1.Engine != "memory" || m1.Disk != "compact" || m1.Memory != "compact" || m1.TupleCodecVersion != 2 {
		t.Fatalf("unexpected manifest fields: %+v", m1)
	}

	manifestPath := filepath.Join(tmpDir, EngineManifestFileName)
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected %s to exist on disk: %v", manifestPath, err)
	}

	// 2. Restart WITHOUT explicit engine flag (even if OS default is wiredtiger)
	m2, err := ResolveOrWriteEngineManifest(tmpDir, "wiredtiger", "compact", false, false)
	if err != nil {
		t.Fatalf("expected auto-adopt on restart without explicit engine flag to succeed, got: %v", err)
	}
	if m2.Engine != "memory" || m2.Disk != "compact" {
		t.Fatalf("expected auto-adopted manifest to retain memory/compact, got: %+v", m2)
	}

	// 3. Read-only inspection via ResolveExistingEngineManifest without explicit flag
	m3, err := ResolveExistingEngineManifest(tmpDir, "wiredtiger", false)
	if err != nil {
		t.Fatalf("ResolveExistingEngineManifest failed: %v", err)
	}
	if m3 == nil || m3.Disk != "compact" {
		t.Fatalf("expected ResolveExistingEngineManifest to return compact manifest, got: %+v", m3)
	}
}

func TestEngineManifest_FailClosedOnExplicitEngineMismatch(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize directory with wiredtiger
	_, err := ResolveOrWriteEngineManifest(tmpDir, "wiredtiger", "compact", true, false)
	if err != nil {
		t.Fatalf("initial wiredtiger manifest write failed: %v", err)
	}

	// Attempt to open with explicit -engine=memory / -disk=compact -> must fail closed with ErrEngineMismatch
	_, err = ResolveOrWriteEngineManifest(tmpDir, "compact", "compact", true, false)
	if !errors.Is(err, ErrEngineMismatch) {
		t.Fatalf("expected ErrEngineMismatch when explicitly requesting compact on wiredtiger dir, got: %v", err)
	}

	// Also verify ResolveExistingEngineManifest fails closed when explicitEngineSet is true
	_, err = ResolveExistingEngineManifest(tmpDir, "compact", true)
	if !errors.Is(err, ErrEngineMismatch) {
		t.Fatalf("expected ResolveExistingEngineManifest to return ErrEngineMismatch, got: %v", err)
	}

	// Explicit matching engine ("wiredtiger") must succeed
	mOK, err := ResolveOrWriteEngineManifest(tmpDir, "wiredtiger", "compact", true, false)
	if err != nil {
		t.Fatalf("expected matching explicit wiredtiger to succeed, got: %v", err)
	}
	if mOK.Disk != "wiredtiger" {
		t.Fatalf("expected disk=wiredtiger, got %q", mOK.Disk)
	}
}

func TestEngineManifest_LegacyDirectoryBackfill(t *testing.T) {
	t.Run("LegacyWiredTigerDirectory", func(t *testing.T) {
		wtDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(wtDir, "WiredTiger.wt"), []byte("wt"), 0644); err != nil {
			t.Fatal(err)
		}

		// Calling without explicit flag auto-detects WiredTiger and backfills lionsql.engine.json
		m, err := ResolveOrWriteEngineManifest(wtDir, "compact", "compact", false, false)
		if err != nil {
			t.Fatalf("expected legacy WiredTiger detection to succeed, got: %v", err)
		}
		if m.Engine != "wiredtiger" || m.Disk != "wiredtiger" {
			t.Fatalf("expected wiredtiger manifest, got: %+v", m)
		}
		if _, err := os.Stat(filepath.Join(wtDir, EngineManifestFileName)); err != nil {
			t.Fatalf("expected backfilled manifest file on disk, got: %v", err)
		}

		// Calling with conflicting explicit -disk=compact on legacy WT dir must fail with ErrEngineMismatch
		wtDir2 := t.TempDir()
		if err := os.WriteFile(filepath.Join(wtDir2, "WiredTiger.turtle"), []byte("wt"), 0644); err != nil {
			t.Fatal(err)
		}
		_, err = ResolveOrWriteEngineManifest(wtDir2, "compact", "compact", true, false)
		if !errors.Is(err, ErrEngineMismatch) {
			t.Fatalf("expected ErrEngineMismatch on legacy WT dir with explicit compact, got: %v", err)
		}
	})

	t.Run("LegacyMemoryDirectory", func(t *testing.T) {
		memDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(memDir, "wal.aof"), []byte("aof"), 0644); err != nil {
			t.Fatal(err)
		}

		// Calling without explicit flag auto-detects memory/compact even if default requested is wiredtiger
		m, err := ResolveOrWriteEngineManifest(memDir, "wiredtiger", "compact", false, false)
		if err != nil {
			t.Fatalf("expected legacy memory detection to succeed, got: %v", err)
		}
		if m.Engine != "memory" || m.Disk != "compact" {
			t.Fatalf("expected memory/compact manifest, got: %+v", m)
		}
		if _, err := os.Stat(filepath.Join(memDir, EngineManifestFileName)); err != nil {
			t.Fatalf("expected backfilled manifest file on disk, got: %v", err)
		}
	})
}
