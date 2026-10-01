package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"sqlion/pkg/config"
)

func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg.Server.DataDir != "data" {
		t.Errorf("expected data_dir 'data', got %q", cfg.Server.DataDir)
	}
	if cfg.Server.Engine != "wiredtiger" {
		t.Errorf("expected engine 'wiredtiger', got %q", cfg.Server.Engine)
	}
	if !cfg.Server.AutoSeed {
		t.Errorf("expected auto_seed true")
	}
	if cfg.Network.HTTPAddr != "127.0.0.1:8090" {
		t.Errorf("expected http_addr '127.0.0.1:8090', got %q", cfg.Network.HTTPAddr)
	}
	if cfg.Network.PGAddr != "127.0.0.1:5433" {
		t.Errorf("expected pg_addr '127.0.0.1:5433', got %q", cfg.Network.PGAddr)
	}
	if cfg.Network.TDSAddr != "127.0.0.1:1433" {
		t.Errorf("expected tds_addr '127.0.0.1:1433', got %q", cfg.Network.TDSAddr)
	}
	if cfg.Logging.SlowQuery.ThresholdMS != 50.0 {
		t.Errorf("expected threshold_ms 50.0, got %v", cfg.Logging.SlowQuery.ThresholdMS)
	}
}

func TestLoadFromFile_PartialJSON(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sqlion_config_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "custom.json")
	// Only override HTTP and DataDir; other fields should maintain defaults (Rule 82)
	jsonContent := `{
		"server": {
			"data_dir": "custom_data"
		},
		"network": {
			"http_addr": "0.0.0.0:9999"
		}
	}`
	if err := os.WriteFile(configPath, []byte(jsonContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	if cfg.Server.DataDir != "custom_data" {
		t.Errorf("expected data_dir 'custom_data', got %q", cfg.Server.DataDir)
	}
	if cfg.Network.HTTPAddr != "0.0.0.0:9999" {
		t.Errorf("expected http_addr '0.0.0.0:9999', got %q", cfg.Network.HTTPAddr)
	}
	// Defaults preserved
	if cfg.Server.Engine != "wiredtiger" {
		t.Errorf("expected default engine 'wiredtiger', got %q", cfg.Server.Engine)
	}
	if cfg.Network.PGAddr != "127.0.0.1:5433" {
		t.Errorf("expected default pg_addr '127.0.0.1:5433', got %q", cfg.Network.PGAddr)
	}
}

func TestWriteToFileAtomic_AndReload(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sqlion_config_atomic_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	targetPath := filepath.Join(tmpDir, "sub", "sqlion.json")
	cfg := config.DefaultConfig()
	cfg.Server.DataDir = "atomic_dir"
	cfg.Network.HTTPAddr = "127.0.0.1:8888"

	if err := config.WriteToFileAtomic(cfg, targetPath); err != nil {
		t.Fatalf("WriteToFileAtomic failed: %v", err)
	}

	// Verify temp file does not remain
	if _, err := os.Stat(targetPath + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temporary swap file should be cleaned up")
	}

	// Reload
	reloaded, err := config.LoadFromFile(targetPath)
	if err != nil {
		t.Fatalf("failed to reload atomic config: %v", err)
	}
	if reloaded.Server.DataDir != "atomic_dir" || reloaded.Network.HTTPAddr != "127.0.0.1:8888" {
		t.Errorf("reloaded config mismatch: %+v", reloaded)
	}
}

func TestAutoDiscoverConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sqlion_autodiscover_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 1. When no file exists, returns default and empty path
	cfg, path, err := config.AutoDiscoverConfig("", filepath.Join(tmpDir, "nonexistent"))
	if err != nil || path != "" || cfg == nil {
		t.Fatalf("expected default config fallback, got path=%q, err=%v", path, err)
	}

	// 2. When dataDir has sqlion.json
	dataConfig := filepath.Join(tmpDir, "sqlion.json")
	customJSON := `{"server":{"engine":"memory"}}`
	if err := os.WriteFile(dataConfig, []byte(customJSON), 0644); err != nil {
		t.Fatalf("write dataConfig failed: %v", err)
	}

	cfg, path, err = config.AutoDiscoverConfig("", tmpDir)
	if err != nil || path != dataConfig || cfg.Server.Engine != "memory" {
		t.Errorf("auto-discovery from dataDir failed: path=%s, engine=%s, err=%v", path, cfg.Server.Engine, err)
	}
}

func TestNetworkConfig_OptOut(t *testing.T) {
	// 1. Default config: all 3 enabled
	cfg := config.DefaultConfig()
	if !cfg.Network.IsHTTPEnabled() || !cfg.Network.IsPGEnabled() || !cfg.Network.IsTDSEnabled() {
		t.Fatalf("expected all listeners enabled by default")
	}

	// 2. Opt-out via empty address string (e.g. -pg="" or "pg_addr": "")
	cfg.Network.PGAddr = ""
	if cfg.Network.IsPGEnabled() {
		t.Errorf("expected PG to be disabled when pg_addr is empty")
	}
	if !cfg.Network.IsHTTPEnabled() || !cfg.Network.IsTDSEnabled() {
		t.Errorf("expected other listeners to remain enabled")
	}

	// 3. Opt-out via empty addresses in JSON file
	tmpDir, err := os.MkdirTemp("", "sqlion_optout_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	jsonPath := filepath.Join(tmpDir, "sqlion.json")
	customJSON := `{
		"network": {
			"http_addr": "",
			"pg_addr": "",
			"tds_addr": ""
		}
	}`
	if err := os.WriteFile(jsonPath, []byte(customJSON), 0644); err != nil {
		t.Fatalf("write customJSON failed: %v", err)
	}

	loaded, err := config.LoadFromFile(jsonPath)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}
	if loaded.Network.IsHTTPEnabled() {
		t.Errorf("expected HTTP to be disabled when http_addr is empty")
	}
	if loaded.Network.IsPGEnabled() {
		t.Errorf("expected PG to be disabled when pg_addr is empty")
	}
	if loaded.Network.IsTDSEnabled() {
		t.Errorf("expected TDS to be disabled when tds_addr is empty")
	}
}

