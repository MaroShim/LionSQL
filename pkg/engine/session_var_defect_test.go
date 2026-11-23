package engine_test

import (
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestDEFECT_SessionVar_CoerceFailureReturnsError(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer func() {
		coord.Close()
		eng.Close()
	}()

	sessID := "sess_var_coerce_defect"
	_, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	defer coord.CloseSession(sessID)

	// Declare integer variable
	if _, err := coord.ExecuteSessionSQL(sessID, "DECLARE @i INT;"); err != nil {
		t.Fatalf("DECLARE @i INT failed: %v", err)
	}

	// Setting non-numeric string to @i must return a conversion error, NOT silently succeed!
	res, err := coord.ExecuteSessionSQL(sessID, "SET @i = 'not_a_number';")
	if err == nil && res != nil && res.Success {
		t.Fatalf("[DEFECT-SESSION-VAR-COERCE-SILENT-1] Expected error when setting non-numeric string to INT variable, but operation succeeded silently: %+v", res)
	}

	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "conversion") && !strings.Contains(strings.ToLower(err.Error()), "cannot convert") && !strings.Contains(strings.ToLower(err.Error()), "type") {
		t.Logf("Received expected error: %v", err)
	}
}
