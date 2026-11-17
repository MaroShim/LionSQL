package proc

import (
	"testing"

	"lionsql/pkg/storage"
)

func TestParser_CreateTable_FullAST(t *testing.T) {
	sql := `
	CREATE TEMPORARY TABLE IF NOT EXISTS [dbo].[orders] (
		id SERIAL PRIMARY KEY,
		amount DECIMAL(12, 4) NOT NULL DEFAULT ((0.0000)) CHECK (amount >= 0),
		price MONEY DEFAULT 100,
		ratio FLOAT(16) DEFAULT 0.5,
		big_ratio FLOAT(53),
		note VARCHAR(255) DEFAULT 'item, (with, parens)',
		rv ROWVERSION,
		user_id INT REFERENCES users(id) ON DELETE CASCADE ON UPDATE SET NULL,
		org_id INT NOT NULL,
		code VARCHAR(32) NOT NULL,
		CONSTRAINT uq_org_code UNIQUE (org_id, code),
		CONSTRAINT fk_orders_org FOREIGN KEY (org_id) REFERENCES organizations(org_id) ON DELETE SET DEFAULT,
		CONSTRAINT chk_total CHECK (amount > -100)
	) WITH (MEMORY_OPTIMIZED = ON, DURABILITY = SCHEMA_ONLY) PARTITION BY YEAR(created_at);
	`

	p := NewParser(NewLexer(sql))
	stmt, err := p.ParseCreateTable()
	if err != nil {
		t.Fatalf("ParseCreateTable failed: %v", err)
	}

	if stmt.Name != "orders" || !stmt.IfNotExists || !stmt.Temporary {
		t.Errorf("unexpected table header: name=%q ifNotExists=%v temp=%v", stmt.Name, stmt.IfNotExists, stmt.Temporary)
	}
	if !stmt.MemoryOptimized || stmt.Durability != storage.DurabilitySchemaOnly {
		t.Errorf("unexpected WITH options: memOpt=%v durability=%q", stmt.MemoryOptimized, stmt.Durability)
	}
	if stmt.Partition == nil || stmt.Partition.Func != "YEAR" || stmt.Partition.Column != "created_at" {
		t.Errorf("unexpected PARTITION BY: %+v", stmt.Partition)
	}
	if len(stmt.Columns) != 10 {
		t.Fatalf("expected 10 columns, got %d", len(stmt.Columns))
	}

	// id SERIAL PRIMARY KEY
	if !stmt.Columns[0].PrimaryKey || !stmt.Columns[0].Identity || stmt.Columns[0].Nullable {
		t.Errorf("unexpected id column flags: %+v", stmt.Columns[0])
	}
	// amount DECIMAL(12, 4) DEFAULT ((0.0000)) CHECK (amount >= 0)
	if stmt.Columns[1].Type != storage.TypeDecimal || stmt.Columns[1].Precision != 12 || stmt.Columns[1].Scale != 4 || stmt.Columns[1].Default != "0.0000" || stmt.Columns[1].CheckExpr != "amount >= 0" {
		t.Errorf("unexpected amount column: %+v", stmt.Columns[1])
	}
	// price MONEY (19, 4)
	if stmt.Columns[2].Type != storage.TypeDecimal || stmt.Columns[2].Precision != 19 || stmt.Columns[2].Scale != 4 {
		t.Errorf("unexpected price MONEY column: %+v", stmt.Columns[2])
	}
	// ratio FLOAT(16) -> TypeReal(24)
	if stmt.Columns[3].Type != storage.TypeReal || stmt.Columns[3].Precision != 24 {
		t.Errorf("unexpected ratio FLOAT(16) column: %+v", stmt.Columns[3])
	}
	// big_ratio FLOAT(53) -> TypeFloat(53)
	if stmt.Columns[4].Type != storage.TypeFloat || stmt.Columns[4].Precision != 53 {
		t.Errorf("unexpected big_ratio FLOAT(53) column: %+v", stmt.Columns[4])
	}
	// note VARCHAR(255) DEFAULT 'item, (with, parens)'
	if stmt.Columns[5].Default != "'item, (with, parens)'" {
		t.Errorf("unexpected note default with commas/parens: %q", stmt.Columns[5].Default)
	}
	// rv ROWVERSION
	if !stmt.Columns[6].IsRowVersion || stmt.Columns[6].Type != storage.TypeBinary || stmt.Columns[6].Precision != 8 {
		t.Errorf("unexpected rv ROWVERSION column: %+v", stmt.Columns[6])
	}

	if len(stmt.ForeignKeys) != 2 {
		t.Fatalf("expected 2 foreign keys, got %d", len(stmt.ForeignKeys))
	}
	if stmt.ForeignKeys[0].ParentTable != "users" || stmt.ForeignKeys[0].OnDelete != storage.ActionCascade || stmt.ForeignKeys[0].OnUpdate != storage.ActionSetNull {
		t.Errorf("unexpected inline FK: %+v", stmt.ForeignKeys[0])
	}
	if stmt.ForeignKeys[1].Name != "fk_orders_org" || stmt.ForeignKeys[1].OnDelete != storage.ActionSetDefault {
		t.Errorf("unexpected table-level FK: %+v", stmt.ForeignKeys[1])
	}

	if len(stmt.UniqueIndexes) != 1 || stmt.UniqueIndexes[0].Name != "uq_org_code" || len(stmt.UniqueIndexes[0].Columns) != 2 {
		t.Errorf("unexpected unique indexes: %+v", stmt.UniqueIndexes)
	}
	if len(stmt.CheckConstraints) != 1 || stmt.CheckConstraints[0] != "amount > - 100" {
		t.Errorf("unexpected check constraints: %+v", stmt.CheckConstraints)
	}
}

func TestParser_AlterTable_CompoundAndAllActions(t *testing.T) {
	t.Run("compound alter column type and not null", func(t *testing.T) {
		sql := `ALTER TABLE IF EXISTS attachments ALTER COLUMN file_size SET DATA TYPE BIGINT, ALTER COLUMN file_size SET NOT NULL;`
		p := NewParser(NewLexer(sql))
		stmt, err := p.ParseAlterTable()
		if err != nil {
			t.Fatalf("ParseAlterTable failed: %v", err)
		}
		if stmt.Table != "attachments" || !stmt.IfExists || len(stmt.Actions) != 2 {
			t.Fatalf("unexpected stmt: %+v", stmt)
		}
		if stmt.Actions[0].Type != AlterColumnType || stmt.Actions[0].Column.Type != storage.TypeInt {
			t.Errorf("unexpected action 0: %+v", stmt.Actions[0])
		}
		if stmt.Actions[1].Type != AlterColumnSetNotNull || stmt.Actions[1].ColumnName != "file_size" {
			t.Errorf("unexpected action 1: %+v", stmt.Actions[1])
		}
	})

	t.Run("add multiple columns and constraints", func(t *testing.T) {
		sql := `ALTER TABLE users ADD COLUMN IF NOT EXISTS age INT DEFAULT 18, bio VARCHAR(100) NOT NULL`
		p := NewParser(NewLexer(sql))
		stmt, err := p.ParseAlterTable()
		if err != nil {
			t.Fatalf("ParseAlterTable failed: %v", err)
		}
		if len(stmt.Actions) != 2 {
			t.Fatalf("expected 2 ADD_COLUMN actions, got %d", len(stmt.Actions))
		}
		if stmt.Actions[0].Column.Name != "age" || !stmt.Actions[0].IfNotExists || stmt.Actions[0].Column.Default != "18" {
			t.Errorf("unexpected first column: %+v", stmt.Actions[0])
		}
		if stmt.Actions[1].Column.Name != "bio" || stmt.Actions[1].Column.Nullable {
			t.Errorf("unexpected second column: %+v", stmt.Actions[1])
		}
	})

	t.Run("drop columns, rename, and constraints", func(t *testing.T) {
		sql := `ALTER TABLE users DROP COLUMN IF EXISTS c1, c2`
		p := NewParser(NewLexer(sql))
		stmt, err := p.ParseAlterTable()
		if err != nil {
			t.Fatalf("ParseAlterTable failed: %v", err)
		}
		if len(stmt.Actions) != 1 || stmt.Actions[0].Type != AlterDropColumn || !stmt.Actions[0].IfExists || len(stmt.Actions[0].DropColumns) != 2 {
			t.Errorf("unexpected drop columns action: %+v", stmt.Actions)
		}
	})
}

func TestParser_DropTruncateAndStatisticsDDL(t *testing.T) {
	truncP := NewParser(NewLexer(`TRUNCATE TABLE ONLY "orders" *, "order_items" CONTINUE IDENTITY CASCADE;`))
	truncStmt, err := truncP.ParseTruncate()
	if err != nil {
		t.Fatalf("ParseTruncate failed: %v", err)
	}
	if len(truncStmt.Tables) != 2 || truncStmt.Tables[0] != "orders" || truncStmt.Tables[1] != "order_items" || !truncStmt.Cascade || truncStmt.RestartIdentity {
		t.Errorf("unexpected TruncateStmt: %+v", truncStmt)
	}

	statP := NewParser(NewLexer(`CREATE STATISTICS stat_cust_region ON customers (region, tier);`))
	statStmt, err := statP.ParseCreateStatistics()
	if err != nil {
		t.Fatalf("ParseCreateStatistics failed: %v", err)
	}
	if statStmt.Name != "stat_cust_region" || statStmt.Table != "customers" || len(statStmt.Columns) != 2 {
		t.Errorf("unexpected CreateStatisticsStmt: %+v", statStmt)
	}

	dropStatP := NewParser(NewLexer(`DROP STATISTICS customers.stat_cust_region;`))
	dropStatStmt, err := dropStatP.ParseDropStatistics()
	if err != nil {
		t.Fatalf("ParseDropStatistics failed: %v", err)
	}
	if dropStatStmt.Table != "customers" || dropStatStmt.Name != "stat_cust_region" {
		t.Errorf("unexpected DropStatisticsStmt: %+v", dropStatStmt)
	}
}
