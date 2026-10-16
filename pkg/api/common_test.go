package api_test

import (
	"testing"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

// setupMemServer creates a lightweight in-memory test api.Server with automatic cleanup.
func setupMemServer(t *testing.T) *api.Server {
	t.Helper()
	eng := memengine.New()
	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)
	t.Cleanup(func() {
		_ = eng.Close()
	})
	return server
}

// execStatements runs multiple SQL statements sequentially, failing the test immediately on any error.
func execStatements(t *testing.T, server *api.Server, stmts ...string) {
	t.Helper()
	for _, stmt := range stmts {
		res, err := server.ExecuteSQL(stmt)
		if err != nil || (res != nil && !res.Success) {
			t.Fatalf("execStatements failed on %q: %v (res: %+v)", stmt, err, res)
		}
	}
}
