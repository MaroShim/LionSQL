package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"lionsql/pkg/config"
)

func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg.Server.DataDir != "data" {
		t.Errorf("expected data_dir 'data', got %q", cfg.Server.DataDir)
	}
	if cfg.Server.Engine != "wiredtiger" {
		t.Errorf("expected engine 'wiredtiger', got %q", cfg.Server.Engine)
	}
	if cfg.Server.AutoSeed {
		t.Errorf("expected auto_seed false")
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
	if cfg.Logging.SlowQuery.ThresholdMS != 100.0 {
		t.Errorf("expected threshold_ms 100.0, got %v", cfg.Logging.SlowQuery.ThresholdMS)
	}
	if !cfg.Auth.Enabled {
		t.Errorf("expected auth enabled by default")
	}
	if cfg.Auth.AdminEmail != "admin@lionsql.local" {
		t.Errorf("expected admin email 'admin@lionsql.local', got %q", cfg.Auth.AdminEmail)
	}
}

func TestLoadFromFile_PartialJSON(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_config_test_*")
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
	tmpDir, err := os.MkdirTemp("", "lionsql_config_atomic_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	targetPath := filepath.Join(tmpDir, "sub", "lionsql.json")
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
	tmpDir, err := os.MkdirTemp("", "lionsql_autodiscover_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 1. When no file exists, returns default and empty path
	cfg, path, err := config.AutoDiscoverConfig("", filepath.Join(tmpDir, "nonexistent"))
	if err != nil || path != "" || cfg == nil {
		t.Fatalf("expected default config fallback, got path=%q, err=%v", path, err)
	}

	// 2. When dataDir has lionsql.json
	dataConfig := filepath.Join(tmpDir, "lionsql.json")
	customJSON := `{"server":{"engine":"memory"}}`
	if err := os.WriteFile(dataConfig, []byte(customJSON), 0644); err != nil {
		t.Fatalf("write dataConfig failed: %v", err)
	}

	cfg, path, err = config.AutoDiscoverConfig("", tmpDir)
	if err != nil || path != dataConfig || cfg.Server.Engine != "memory" {
		t.Errorf("auto-discovery from dataDir failed: path=%s, engine=%s, err=%v", path, cfg.Server.Engine, err)
	}
}

func TestAutoDiscoverConfig_StrictLegacyIgnore(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_legacy_ignore_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// When legacy other_config.json exists in dataDir, it must NOT be loaded (security guard against config poisoning)
	legacyConfig := filepath.Join(tmpDir, "other_config.json")
	customJSON := `{"server":{"engine":"memory"}}`
	if err := os.WriteFile(legacyConfig, []byte(customJSON), 0644); err != nil {
		t.Fatalf("write legacyConfig failed: %v", err)
	}

	cfg, path, err := config.AutoDiscoverConfig("", tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path for legacy other_config.json, got %q", path)
	}
	if cfg.Server.Engine != "wiredtiger" {
		t.Errorf("expected default engine 'wiredtiger', got %q", cfg.Server.Engine)
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
	tmpDir, err := os.MkdirTemp("", "lionsql_optout_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	jsonPath := filepath.Join(tmpDir, "lionsql.json")
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

func TestTLSAndLogging_IsEnabled(t *testing.T) {
	// TLSConfig
	tls1 := config.TLSConfig{}
	if tls1.IsEnabled() {
		t.Errorf("expected empty TLSConfig to be disabled")
	}
	tls2 := config.TLSConfig{CertFile: "server.crt"}
	if !tls2.IsEnabled() {
		t.Errorf("expected TLSConfig with CertFile to be enabled")
	}
	tls3 := config.TLSConfig{RequireSSL: true}
	if !tls3.IsEnabled() {
		t.Errorf("expected TLSConfig with RequireSSL to be enabled")
	}

	// SlowQueryLogConfig
	slow1 := config.SlowQueryLogConfig{}
	if slow1.IsEnabled() {
		t.Errorf("expected empty SlowQueryLogConfig to be disabled")
	}
	slow2 := config.SlowQueryLogConfig{File: "slow.log"}
	if !slow2.IsEnabled() {
		t.Errorf("expected SlowQueryLogConfig with File to be enabled")
	}

	// GeneralQueryLogConfig
	gen1 := config.GeneralQueryLogConfig{}
	if gen1.IsEnabled() {
		t.Errorf("expected empty GeneralQueryLogConfig to be disabled")
	}
	gen2 := config.GeneralQueryLogConfig{File: "general.log"}
	if !gen2.IsEnabled() {
		t.Errorf("expected GeneralQueryLogConfig with File to be enabled")
	}
}

func TestIsLoopbackHost(t *testing.T) {
	testCases := []struct {
		addr     string
		expected bool
	}{
		{"127.0.0.1:8090", true},
		{"127.0.0.1", true},
		{"localhost:8090", true},
		{"localhost", true},
		{"127.0.0.2:5433", true},
		{"[::1]:1433", true},
		{"::1", true},
		{"", true}, // disabled service
		{"   ", true},
		{"0.0.0.0:8090", false},
		{"0.0.0.0", false},
		{":8090", false},
		{"[::]:8090", false},
		{"::", false},
		{"192.168.1.100:8090", false},
		{"10.0.0.1:5433", false},
		{"example.com:1433", false},
	}

	for _, tc := range testCases {
		res := config.IsLoopbackHost(tc.addr)
		if res != tc.expected {
			t.Errorf("IsLoopbackHost(%q) = %v; expected %v", tc.addr, res, tc.expected)
		}
	}
}

func TestValidateSecurityGuard(t *testing.T) {
	// 1. Default config (127.0.0.1 loopback) with default password (1234567890) -> Allowed
	cfg := config.DefaultConfig()
	if err := cfg.ValidateSecurityGuard(); err != nil {
		t.Errorf("expected default loopback config to be allowed, got error: %v", err)
	}

	// 2. HTTP listening on 0.0.0.0 with default password -> Prohibited
	cfgHTTP0000 := config.DefaultConfig()
	cfgHTTP0000.Network.HTTPAddr = "0.0.0.0:8090"
	if err := cfgHTTP0000.ValidateSecurityGuard(); err == nil {
		t.Errorf("expected error when HTTP is bound to 0.0.0.0 with default password, got nil")
	}

	// 3. PG listening on :5433 with default password -> Prohibited
	cfgPGWildcard := config.DefaultConfig()
	cfgPGWildcard.Network.PGAddr = ":5433"
	if err := cfgPGWildcard.ValidateSecurityGuard(); err == nil {
		t.Errorf("expected error when PG is bound to :5433 with default password, got nil")
	}

	// 4. TDS listening on 192.168.1.5:1433 with default password -> Prohibited
	cfgTDSExternal := config.DefaultConfig()
	cfgTDSExternal.Network.TDSAddr = "192.168.1.5:1433"
	if err := cfgTDSExternal.ValidateSecurityGuard(); err == nil {
		t.Errorf("expected error when TDS is bound to external IP with default password, got nil")
	}

	// 5. External interface with custom password -> Allowed
	cfgCustomPass := config.DefaultConfig()
	cfgCustomPass.Network.HTTPAddr = "0.0.0.0:8090"
	cfgCustomPass.Network.PGAddr = "0.0.0.0:5433"
	cfgCustomPass.Network.TDSAddr = "0.0.0.0:1433"
	cfgCustomPass.Auth.AdminPass = "MySecureEnterpriseSecret123!"
	if err := cfgCustomPass.ValidateSecurityGuard(); err != nil {
		t.Errorf("expected external binding with custom password to be allowed, got error: %v", err)
	}

	// 6. External interface with disabled HTTP/PG/TDS -> Allowed
	cfgDisabled := config.DefaultConfig()
	cfgDisabled.Network.HTTPAddr = ""
	cfgDisabled.Network.PGAddr = ""
	cfgDisabled.Network.TDSAddr = ""
	if err := cfgDisabled.ValidateSecurityGuard(); err != nil {
		t.Errorf("expected disabled network services to be allowed, got error: %v", err)
	}
}

func TestApplyEnvOverrides_AdminCredentials(t *testing.T) {
	// LIONSQL_ADMIN_PASS
	os.Setenv("LIONSQL_ADMIN_PASS", "EnvPass999")
	defer os.Unsetenv("LIONSQL_ADMIN_PASS")

	cfg := config.DefaultConfig()
	config.ApplyEnvOverrides(cfg)
	if cfg.Auth.AdminPass != "EnvPass999" {
		t.Errorf("expected AdminPass 'EnvPass999', got %q", cfg.Auth.AdminPass)
	}


}


