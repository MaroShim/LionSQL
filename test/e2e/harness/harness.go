package harness

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/storage/wiredtiger"
	"lionsql/pkg/wire"
	"lionsql/pkg/wire/tds"
)

// StorageType specifies the underlying storage engine variant.
type StorageType string

const (
	StorageMemEngine  StorageType = "MemEngine"
	StorageWiredTiger StorageType = "WiredTiger"
)

// DualServerInstance wraps active PGWire and TDS servers and their shared catalog and storage engine.
type DualServerInstance struct {
	StorageType StorageType
	Engine      storage.Engine
	Catalog     *catalog.Catalog
	PGPort      int
	TDSPort     int
	Cleanup     func()

	pgServer  *wire.Server
	tdsServer *tds.Server
	tempDir   string
	closeOnce sync.Once
}

// StartDualServer launches both PGWire and TDS listeners on dynamic ports (:0) sharing one storage instance.
func StartDualServer(t *testing.T, sType StorageType) *DualServerInstance {
	t.Helper()

	var eng storage.Engine
	var tempDir string

	switch sType {
	case StorageMemEngine:
		m := memengine.New()
		if err := m.Open(""); err != nil {
			t.Fatalf("[%s] memengine.Open failed: %v", sType, err)
		}
		eng = m

	case StorageWiredTiger:
		d, err := os.MkdirTemp("", "e2e_wt_*")
		if err != nil {
			t.Fatalf("[%s] MkdirTemp failed: %v", sType, err)
		}
		tempDir = d
		w := wiredtiger.New()
		if err := w.Open(tempDir); err != nil {
			_ = os.RemoveAll(tempDir)
			t.Fatalf("[%s] wiredtiger.Open failed: %v", sType, err)
		}
		eng = w

	default:
		t.Fatalf("unsupported storage type: %s", sType)
	}

	cat := catalog.New(eng)

	// PGWire listener
	pgSrv := wire.NewServer("127.0.0.1:0", cat, eng)
	if err := pgSrv.Start(); err != nil {
		_ = eng.Close()
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
		t.Fatalf("[%s] PGWire Start failed: %v", sType, err)
	}
	pgPort := pgSrv.Addr().(*net.TCPAddr).Port

	// TDS listener
	tdsSrv := tds.NewServer("127.0.0.1:0", cat, eng)
	if err := tdsSrv.Start(); err != nil {
		pgSrv.Close()
		_ = eng.Close()
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
		t.Fatalf("[%s] TDS Start failed: %v", sType, err)
	}
	tdsPort := tdsSrv.Addr().(*net.TCPAddr).Port

	inst := &DualServerInstance{
		StorageType: sType,
		Engine:      eng,
		Catalog:     cat,
		PGPort:      pgPort,
		TDSPort:     tdsPort,
		pgServer:    pgSrv,
		tdsServer:   tdsSrv,
		tempDir:     tempDir,
	}

	inst.Cleanup = func() {
		inst.closeOnce.Do(func() {
			if inst.tdsServer != nil {
				inst.tdsServer.Close()
			}
			if inst.pgServer != nil {
				inst.pgServer.Close()
			}
			if inst.Engine != nil {
				_ = inst.Engine.Close()
			}
			if inst.tempDir != "" {
				_ = os.RemoveAll(inst.tempDir)
			}
		})
	}

	return inst
}

// OpenClients creates *sql.DB connections for PGWire and TDS and ensures they are healthy.
func (d *DualServerInstance) OpenClients(t *testing.T) (*sql.DB, *sql.DB) {
	t.Helper()

	pgDSN := fmt.Sprintf("postgres://postgres:secret@127.0.0.1:%d/master?sslmode=disable&connect_timeout=3", d.PGPort)
	pgDB, err := sql.Open("postgres", pgDSN)
	if err != nil {
		t.Fatalf("[%s] sql.Open postgres failed: %v", d.StorageType, err)
	}

	tdsDSN := fmt.Sprintf("server=127.0.0.1;port=%d;database=master;user id=sa;password=secret;encrypt=disable;connection timeout=3", d.TDSPort)
	tdsDB, err := sql.Open("sqlserver", tdsDSN)
	if err != nil {
		pgDB.Close()
		t.Fatalf("[%s] sql.Open sqlserver failed: %v", d.StorageType, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pgDB.PingContext(ctx); err != nil {
		pgDB.Close()
		tdsDB.Close()
		t.Fatalf("[%s] PGWire ping failed: %v", d.StorageType, err)
	}
	if err := tdsDB.PingContext(ctx); err != nil {
		pgDB.Close()
		tdsDB.Close()
		t.Fatalf("[%s] TDS ping failed: %v", d.StorageType, err)
	}

	return pgDB, tdsDB
}
