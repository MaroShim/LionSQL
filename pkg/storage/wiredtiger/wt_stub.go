//go:build !cgo || !wiredtiger

package wiredtiger

import (
	"fmt"

	"sqlion/pkg/storage"
)

// Engine is a stub implementation when WiredTiger is not compiled in.
type Engine struct{}

// New creates a new WiredTiger engine stub.
func New() *Engine {
	return &Engine{}
}

func (e *Engine) Open(dir string) error {
	return fmt.Errorf("wiredtiger storage engine not enabled in this build; build with '-tags wiredtiger'")
}

func (e *Engine) Close() error {
	return nil
}

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
