package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestCasdoor_CompositePrimaryKeyAndHexBinary(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("failed to open memengine: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	ddl := `CREATE TABLE [provider] (
		[owner] VARCHAR(100) NOT NULL,
		[name] VARCHAR(100) NOT NULL,
		[category] VARCHAR(100) NULL,
		[addresses] VARBINARY(50) NULL,
		PRIMARY KEY (owner, name)
	);`
	if _, err := coord.ExecuteSQL(ddl); err != nil {
		t.Fatalf("CREATE TABLE with composite PK failed: %v", err)
	}

	// Insert multiple rows sharing the same leading PK column ('admin')
	ins1 := `INSERT INTO [provider] ([owner], [name], [category], [addresses]) VALUES ('admin', 'provider_captcha_default', 'Captcha', 0x6e756c6c);`
	if _, err := coord.ExecuteSQL(ins1); err != nil {
		t.Fatalf("INSERT 1 failed: %v", err)
	}

	ins2 := `INSERT INTO [provider] ([owner], [name], [category], [addresses]) VALUES ('admin', 'provider_balance', 'Payment', 0x5b5d);`
	if _, err := coord.ExecuteSQL(ins2); err != nil {
		t.Fatalf("INSERT 2 with same owner but different name failed: %v", err)
	}

	// Duplicate (owner, name) must fail
	if _, err := coord.ExecuteSQL(ins2); err == nil {
		t.Fatalf("expected duplicate composite PK error, got nil")
	}

	// Point query on second PK column ('name') must not falsely trigger single-column PK bypass
	sel, err := coord.ExecuteSQL(`SELECT [owner], [name], [category], [addresses] FROM [provider] WHERE [name] = 'provider_balance'`)
	if err != nil {
		t.Fatalf("SELECT by second PK column failed: %v", err)
	}
	if len(sel.Rows) != 1 {
		t.Fatalf("expected 1 row for name='provider_balance', got %d", len(sel.Rows))
	}
	if sel.Rows[0]["category"] != "Payment" {
		t.Errorf("expected category 'Payment', got %v", sel.Rows[0]["category"])
	}
	if sel.Rows[0]["addresses"] != "0x5b5d" {
		t.Errorf("expected addresses '0x5b5d', got %v", sel.Rows[0]["addresses"])
	}

	// Update single row by composite WHERE condition
	upd := `UPDATE [provider] SET [category] = 'UpdatedPayment' WHERE [owner] = 'admin' AND [name] = 'provider_balance'`
	if _, err := coord.ExecuteSQL(upd); err != nil {
		t.Fatalf("UPDATE by composite key failed: %v", err)
	}

	selAll, err := coord.ExecuteSQL(`SELECT [owner], [name], [category] FROM [provider] WHERE [owner] = 'admin' ORDER BY [name] ASC`)
	if err != nil {
		t.Fatalf("SELECT all for owner='admin' failed: %v", err)
	}
	if len(selAll.Rows) != 2 {
		t.Fatalf("expected 2 rows for owner='admin', got %d", len(selAll.Rows))
	}
	if selAll.Rows[0]["category"] != "UpdatedPayment" {
		t.Errorf("expected updated category 'UpdatedPayment', got %v", selAll.Rows[0]["category"])
	}

	// Delete single row by composite WHERE condition
	del := `DELETE FROM [provider] WHERE [owner] = 'admin' AND [name] = 'provider_captcha_default'`
	if _, err := coord.ExecuteSQL(del); err != nil {
		t.Fatalf("DELETE by composite key failed: %v", err)
	}

	selRem, err := coord.ExecuteSQL(`SELECT COUNT(*) AS cnt FROM [provider]`)
	if err != nil {
		t.Fatalf("SELECT COUNT(*) failed: %v", err)
	}
	if len(selRem.Rows) != 1 || selRem.Rows[0]["cnt"] != int64(1) {
		t.Errorf("expected 1 remaining row, got %+v", selRem.Rows)
	}
}
