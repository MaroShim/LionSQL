package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// ServerConfig defines server storage and process parameters.
type ServerConfig struct {
	DataDir   string `json:"data_dir"`
	Engine    string `json:"engine"`
	Ephemeral bool   `json:"ephemeral"`
	AutoSeed  bool   `json:"auto_seed"`
	PIDFile   string `json:"pid_file,omitempty"`
	LogFile   string `json:"log_file,omitempty"`
}

// TLSConfig defines encryption parameters for wire protocols.
type TLSConfig struct {
	Enabled    bool   `json:"enabled"`
	RequireSSL bool   `json:"require_ssl"`
	CertFile   string `json:"cert_file,omitempty"`
	KeyFile    string `json:"key_file,omitempty"`
}

// IsEnabled returns true if TLS is explicitly enabled, required, or certificates are configured.
func (t TLSConfig) IsEnabled() bool {
	return t.Enabled || t.RequireSSL || strings.TrimSpace(t.CertFile) != ""
}

// NetworkConfig defines listener endpoints and protocols.
type NetworkConfig struct {
	HTTPAddr string    `json:"http_addr"`
	PGAddr   string    `json:"pg_addr"`
	TDSAddr  string    `json:"tds_addr"`
	TLS      TLSConfig `json:"tls"`
}

// IsHTTPEnabled returns true if HTTPAddr is configured and non-empty.
func (n NetworkConfig) IsHTTPEnabled() bool {
	return strings.TrimSpace(n.HTTPAddr) != ""
}

// IsPGEnabled returns true if PGAddr is configured and non-empty.
func (n NetworkConfig) IsPGEnabled() bool {
	return strings.TrimSpace(n.PGAddr) != ""
}

// IsTDSEnabled returns true if TDSAddr is configured and non-empty.
func (n NetworkConfig) IsTDSEnabled() bool {
	return strings.TrimSpace(n.TDSAddr) != ""
}

// AuthConfig defines administrative authentication and credential settings.
type AuthConfig struct {
	Enabled    bool   `json:"enabled"`
	AdminEmail string `json:"admin_email"`
	AdminPass  string `json:"admin_pass"`
}

// ResourcesConfig defines concurrency and memory limits.
type ResourcesConfig struct {
	MaxWorkers      int    `json:"max_workers"`
	MaxIdleSessions int    `json:"max_idle_sessions"`
	WorkMem         string `json:"work_mem"`
	MaxMemory       string `json:"max_memory"`
}

// SlowQueryLogConfig defines criteria and destination for slow queries.
type SlowQueryLogConfig struct {
	File        string  `json:"file,omitempty"`
	ThresholdMS float64 `json:"threshold_ms"`
}

// IsEnabled returns true if a non-empty log file is configured.
func (s SlowQueryLogConfig) IsEnabled() bool {
	return strings.TrimSpace(s.File) != ""
}

// GeneralQueryLogConfig defines full query auditing destination.
type GeneralQueryLogConfig struct {
	File string `json:"file,omitempty"`
}

// IsEnabled returns true if a non-empty log file is configured.
func (g GeneralQueryLogConfig) IsEnabled() bool {
	return strings.TrimSpace(g.File) != ""
}

// LoggingConfig aggregates diagnostic and query log policies.
type LoggingConfig struct {
	SlowQuery    SlowQueryLogConfig    `json:"slow_query"`
	GeneralQuery GeneralQueryLogConfig `json:"general_query"`
	DevMode      bool                  `json:"dev_mode,omitempty"`
}

// ReplicationConfig defines 1-Writer Multi-Reader HA availability group parameters.
type ReplicationConfig struct {
	Enabled            bool     `json:"enabled"`
	Role               string   `json:"role"`                            // "PRIMARY" or "REPLICA"
	PrimaryURL         string   `json:"primary_url,omitempty"`           // e.g. "http://127.0.0.1:8090"
	SelfURL            string   `json:"self_url,omitempty"`              // e.g. "http://127.0.0.1:8091"
	NodeID             string   `json:"node_id,omitempty"`
	HistoryCap         int      `json:"history_cap,omitempty"`
	AutoFailover       bool     `json:"auto_failover,omitempty"`
	Peers              []string `json:"peers,omitempty"`                 // Peer HTTP URLs
	HeartbeatTimeoutMS float64  `json:"heartbeat_timeout_ms,omitempty"`  // default: 3000ms
	ElectionTimeoutMS  float64  `json:"election_timeout_ms,omitempty"`   // default: 1500ms
	Quorum             int      `json:"quorum,omitempty"`
}

// OutboxConfig defines settings for Transactional Outbox broker forwarding.
type OutboxConfig struct {
	Enabled        bool     `json:"enabled"`
	TableFilter    string   `json:"table_filter,omitempty"`
	TopicPrefix    string   `json:"topic_prefix,omitempty"`
	DedicatedTable bool     `json:"dedicated_table,omitempty"`
	WebhookURLs    []string `json:"webhook_urls,omitempty"`
	KafkaEndpoint  string   `json:"kafka_endpoint,omitempty"`
	RabbitMQURL    string   `json:"rabbitmq_url,omitempty"`
}

// Config represents the unified, hierarchical configuration for LionSQL.
type Config struct {
	Server      ServerConfig      `json:"server"`
	Network     NetworkConfig     `json:"network"`
	Auth        AuthConfig        `json:"auth"`
	Resources   ResourcesConfig   `json:"resources"`
	Logging     LoggingConfig     `json:"logging"`
	Replication ReplicationConfig `json:"replication"`
	Outbox      OutboxConfig      `json:"outbox,omitempty"`
}

// DefaultConfig returns the production-ready zero-config baseline.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			DataDir:  "data",
			Engine:   "wiredtiger",
			AutoSeed: false,
		},
		Outbox: OutboxConfig{
			TopicPrefix: "lionsql.",
		},
		Replication: ReplicationConfig{
			Enabled:    true,
			Role:       "PRIMARY",
			HistoryCap: 10000,
		},
		Network: NetworkConfig{
			HTTPAddr: "127.0.0.1:8090",
			PGAddr:   "127.0.0.1:5433",
			TDSAddr:  "127.0.0.1:1433",
			TLS: TLSConfig{
				Enabled:    false,
				RequireSSL: false,
			},
		},
		Auth: AuthConfig{
			Enabled:    true,
			AdminEmail: "admin@lionsql.local",
			AdminPass:  "1234567890",
		},
		Resources: ResourcesConfig{
			MaxWorkers:      0, // NumCPU * 4
			MaxIdleSessions: 64,
			WorkMem:         "64MB",
		},
		Logging: LoggingConfig{
			SlowQuery: SlowQueryLogConfig{
				File:        "",
				ThresholdMS: 100.0,
			},
			GeneralQuery: GeneralQueryLogConfig{
				File: "",
			},
		},
	}
}

// LoadFromFile loads and parses a JSON configuration file, overlaying upon default settings.
func LoadFromFile(filePath string) (*Config, error) {
	cleanPath := filepath.Clean(filePath)
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", cleanPath, err)
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse JSON config file %q: %w", cleanPath, err)
	}
	return cfg, nil
}

// AutoDiscoverConfig attempts to locate an existing config file or falls back to defaults.
// Precedence: explicitPath > ./lionsql.json > <dataDir>/lionsql.json > Built-in Defaults.
// Applies environment variable overrides as per Golden Rule 37.
func AutoDiscoverConfig(explicitPath string, dataDir string) (*Config, string, error) {
	var resCfg *Config
	var resPath string

	if explicitPath != "" {
		cfg, err := LoadFromFile(explicitPath)
		if err != nil {
			return nil, "", err
		}
		resCfg = cfg
		resPath = explicitPath
	} else if fi, err := os.Stat("lionsql.json"); err == nil && !fi.IsDir() {
		cfg, err := LoadFromFile("lionsql.json")
		if err == nil {
			resCfg = cfg
			resPath = "lionsql.json"
		}
	} else if dataDir != "" {
		dataConfig := filepath.Join(dataDir, "lionsql.json")
		if fi, err := os.Stat(dataConfig); err == nil && !fi.IsDir() {
			cfg, err := LoadFromFile(dataConfig)
			if err == nil {
				resCfg = cfg
				resPath = dataConfig
			}
		}
	}

	if resCfg == nil {
		resCfg = DefaultConfig()
	}
	ApplyEnvOverrides(resCfg)
	return resCfg, resPath, nil
}

// ApplyEnvOverrides overrides configuration settings with environment variables.
// Follows Golden Rule 37: CLI Arguments > Environment Variables > Config File > Built-in Defaults.
func ApplyEnvOverrides(cfg *Config) {
	if cfg == nil {
		return
	}
	if v := os.Getenv("LIONSQL_WORK_MEM"); v != "" {
		cfg.Resources.WorkMem = v
	}
	if v := os.Getenv("LIONSQL_MAX_MEMORY"); v != "" {
		cfg.Resources.MaxMemory = v
	}
	if v := os.Getenv("LIONSQL_ADMIN_PASS"); v != "" {
		cfg.Auth.AdminPass = v
	}
	if v := os.Getenv("LIONSQL_ADMIN_EMAIL"); v != "" {
		cfg.Auth.AdminEmail = v
	}
}

// IsLoopbackHost checks whether the specified address or host is bound strictly to a loopback interface.
// If addr is empty, it returns true (indicating a disabled or inactive service).
func IsLoopbackHost(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return true
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.TrimSpace(host)
	if host == "" || host == "0.0.0.0" || host == "::" {
		return false
	}
	if strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// NonLoopbackServiceAddresses returns a list of active network services (e.g. "HTTP (0.0.0.0:8090)")
// that are bound to non-loopback (public or external) interfaces.
func (c *Config) NonLoopbackServiceAddresses() []string {
	var addrs []string
	if c.Network.IsHTTPEnabled() && !IsLoopbackHost(c.Network.HTTPAddr) {
		addrs = append(addrs, fmt.Sprintf("HTTP (%s)", c.Network.HTTPAddr))
	}
	if c.Network.IsPGEnabled() && !IsLoopbackHost(c.Network.PGAddr) {
		addrs = append(addrs, fmt.Sprintf("PostgreSQL Wire (%s)", c.Network.PGAddr))
	}
	if c.Network.IsTDSEnabled() && !IsLoopbackHost(c.Network.TDSAddr) {
		addrs = append(addrs, fmt.Sprintf("TDS/MSSQL Wire (%s)", c.Network.TDSAddr))
	}
	return addrs
}

// ValidateSecurityGuard enforces production safety rules (PROD-SEC-1).
// The default superuser password ("1234567890") is strictly permitted ONLY
// when all active network listeners are bound exclusively to loopback addresses (127.0.0.1, localhost).
// If any service binds to an external interface (e.g. 0.0.0.0) with default password, it returns an error.
func (c *Config) ValidateSecurityGuard() error {
	if c.Auth.AdminPass != "1234567890" && strings.TrimSpace(c.Auth.AdminPass) != "" {
		return nil
	}
	externalServices := c.NonLoopbackServiceAddresses()
	if len(externalServices) > 0 {
		return fmt.Errorf("insecure default administrator password ('1234567890') is prohibited when binding to external/non-loopback interfaces: %s", strings.Join(externalServices, ", "))
	}
	return nil
}

// WriteToFileAtomic writes configuration to disk via a temporary swap file and atomic rename.
func WriteToFileAtomic(cfg *Config, targetPath string) error {
	cleanPath := filepath.Clean(targetPath)
	dir := filepath.Dir(cleanPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", dir, err)
		}
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize config JSON: %w", err)
	}
	data = append(data, '\n')

	tmpPath := cleanPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temporary config file: %w", err)
	}

	if err := os.Rename(tmpPath, cleanPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to atomically rename config file: %w", err)
	}

	return nil
}
