//go:build !cgo || !wiredtiger

package wiredtiger

import (
	"fmt"
	"strings"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// WiredTigerOpenConfig defines the canonical, verified configuration passed to wiredtiger_open.
const WiredTigerOpenConfig = "create,cache_size=500M,session_max=2000,eviction=(threads_min=4,threads_max=8),checkpoint=(wait=60),log=(enabled=true)"

// BuildWiredTigerOpenConfig dynamically constructs a verified configuration passed to wiredtiger_open.
func BuildWiredTigerOpenConfig(cacheSize string, sessionMax int) string {
	if cacheSize == "" {
		cacheSize = "500M"
	}
	if sessionMax <= 0 {
		sessionMax = 2000
	}
	cfg := fmt.Sprintf("create,cache_size=%s,session_max=%d,eviction=(threads_min=4,threads_max=8),checkpoint=(wait=60),log=(enabled=true)", cacheSize, sessionMax)
	if strings.Contains(cfg, "transaction_sync") {
		panic("ILLEGAL WIREDTIGER CONFIG: 'transaction_sync' is strictly prohibited in LionSQL")
	}
	return cfg
}

// Engine is a stub implementation when WiredTiger is not compiled in.
type Engine struct{}

// SetCacheSize sets the WiredTiger heap cache allocation before Open().
func (e *Engine) SetCacheSize(sz string) {}

// SetSessionMax sets the maximum concurrent internal sessions allocated by WiredTiger.
func (e *Engine) SetSessionMax(max int) {}

// New creates a new WiredTiger engine stub.
func New() *Engine {
	return &Engine{}
}

func (e *Engine) Type() string {
	return "WiredTiger"
}

func (e *Engine) Open(dir string) error {
	return fmt.Errorf("wiredtiger storage engine not enabled in this build; build with '-tags wiredtiger'")
}

func (e *Engine) Close() error {
	return nil
}

func (e *Engine) CurrentLSN() int64 {
	return 0
}

func (e *Engine) SetWALHook(fn func(rec memengine.WALRecord)) {}
func (e *Engine) AddWALHook(fn func(rec memengine.WALRecord)) {}

func (e *Engine) SetFsyncPolicy(policy memengine.FsyncPolicy) {}

func (e *Engine) FsyncPolicy() memengine.FsyncPolicy { return memengine.FsyncEverySec }

func (e *Engine) SyncCount() int64 { return 0 }

func (e *Engine) Sync() error { return nil }

func (e *Engine) NewSession() (storage.Session, error) {
	return nil, fmt.Errorf("wiredtiger storage engine not enabled")
}

func (e *Engine) CreateTable(schema *storage.TableSchema) error {
	return fmt.Errorf("wiredtiger storage engine not enabled")
}

func (e *Engine) DropTable(tableName string) error {
	return fmt.Errorf("wiredtiger storage engine not enabled")
}

func (e *Engine) GetTable(tableName string) (*storage.TableSchema, error) {
	return nil, fmt.Errorf("wiredtiger storage engine not enabled")
}

func (e *Engine) ListTables() ([]*storage.TableSchema, error) {
	return nil, fmt.Errorf("wiredtiger storage engine not enabled")
}

func (e *Engine) CreateIndex(index *storage.IndexSchema) error {
	return fmt.Errorf("wiredtiger storage engine not enabled")
}

func (e *Engine) DropIndex(tableName, indexName string) error {
	return fmt.Errorf("wiredtiger storage engine not enabled")
}

func (e *Engine) TruncateTable(tableName string) error {
	return fmt.Errorf("wiredtiger storage engine not enabled")
}

func (e *Engine) TruncateTables(tableNames []string, cascade bool) error {
	return fmt.Errorf("wiredtiger storage engine not enabled")
}

func (e *Engine) DropPartition(tableName, partitionName string) error {
	return fmt.Errorf("wiredtiger storage engine not enabled")
}

func (e *Engine) GetPartitions(tableName string) ([]storage.PartitionInfo, error) {
	return nil, fmt.Errorf("wiredtiger storage engine not enabled")
}

func (e *Engine) AddColumn(tableName string, col *storage.Column) error {
	return fmt.Errorf("wiredtiger storage engine not enabled")
}

func (e *Engine) RenameTable(oldName, newName string) error {
	return fmt.Errorf("wiredtiger storage engine not enabled")
}

func (e *Engine) AlterTable(tableName string, mutator func(schema *storage.TableSchema) error) error {
	return fmt.Errorf("wiredtiger storage engine not enabled")
}

func (e *Engine) TableRowCount(tableName string) (int64, error) {
	return 0, fmt.Errorf("wiredtiger storage engine not enabled")
}

