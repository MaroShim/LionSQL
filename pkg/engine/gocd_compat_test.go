package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestGoCD_LiquibaseAndHibernateCompatibility(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. PostgreSQL session parameters inspection
	res, err := coord.ExecuteSQL(`SELECT version()`)
	if err != nil {
		t.Fatalf("SELECT version() failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 version row, got %d", len(res.Rows))
	}

	// 1.5. Liquibase DatabaseMetaData.getSQLKeywords query
	kwRes, err := coord.ExecuteSQL(`select string_agg(word, ',') from pg_catalog.pg_get_keywords() where word <> ALL ('{ALL,AND,ANY}')`)
	if err != nil {
		t.Fatalf("Liquibase getSQLKeywords failed: %v", err)
	}
	if len(kwRes.Rows) != 1 {
		t.Fatalf("expected 1 row from string_agg, got %d", len(kwRes.Rows))
	}
	aggVal, ok := kwRes.Rows[0]["string_agg"].(string)
	if !ok || aggVal == "" {
		t.Fatalf("expected non-empty string_agg result, got %v", kwRes.Rows[0])
	}

	// 2. Liquibase Lock table creation & lock acquisition
	_, err = coord.ExecuteSQL(`
		CREATE TABLE DATABASECHANGELOGLOCK (
			ID INT NOT NULL PRIMARY KEY,
			LOCKED INT NOT NULL,
			LOCKGRANTED DATETIME,
			LOCKEDBY VARCHAR(255)
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE DATABASECHANGELOGLOCK failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO DATABASECHANGELOGLOCK (ID, LOCKED, LOCKEDBY) VALUES (1, 0, NULL);`)
	if err != nil {
		t.Fatalf("INSERT into DATABASECHANGELOGLOCK failed: %v", err)
	}

	// Lock acquisition
	_, err = coord.ExecuteSQL(`UPDATE DATABASECHANGELOGLOCK SET LOCKED = 1, LOCKEDBY = 'gocd-server-01' WHERE ID = 1 AND LOCKED = 0;`)
	if err != nil {
		t.Fatalf("Lock acquire failed: %v", err)
	}

	res, err = coord.ExecuteSQL(`SELECT LOCKED, LOCKEDBY FROM DATABASECHANGELOGLOCK WHERE ID = 1;`)
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("Query lock failed: %v", err)
	}
	if res.Rows[0]["LOCKED"] != int64(1) || res.Rows[0]["LOCKEDBY"] != "gocd-server-01" {
		t.Errorf("expected locked=1, lockedby=gocd-server-01, got %v", res.Rows[0])
	}

	// 3. Liquibase Changelog tracking table
	_, err = coord.ExecuteSQL(`
		CREATE TABLE DATABASECHANGELOG (
			ID VARCHAR(255) NOT NULL,
			AUTHOR VARCHAR(255) NOT NULL,
			FILENAME VARCHAR(255) NOT NULL,
			DATEEXECUTED DATETIME NOT NULL,
			ORDEREXECUTED INT NOT NULL,
			EXECTYPE VARCHAR(10) NOT NULL,
			MD5SUM VARCHAR(35),
			DESCRIPTION VARCHAR(255)
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE DATABASECHANGELOG failed: %v", err)
	}

	// 4. Core GoCD Schema Creation (ACCESSTOKEN, AGENTS, PIPELINES, STAGES)
	_, err = coord.ExecuteSQL(`
		CREATE TABLE ACCESSTOKEN (
			ID BIGINT IDENTITY(1,1) PRIMARY KEY,
			VALUE VARCHAR(255) NOT NULL,
			USERNAME VARCHAR(255) NOT NULL,
			REVOKED INT DEFAULT 0
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE ACCESSTOKEN failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`
		CREATE TABLE AGENTS (
			ID BIGINT IDENTITY(1,1) PRIMARY KEY,
			UUID VARCHAR(255) NOT NULL,
			HOSTNAME VARCHAR(255) NOT NULL,
			IPADDRESS VARCHAR(45) NOT NULL,
			ISDISABLED INT DEFAULT 0
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE AGENTS failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`
		CREATE TABLE PIPELINES (
			ID BIGINT IDENTITY(1,1) PRIMARY KEY,
			NAME VARCHAR(255) NOT NULL,
			COUNTER INT DEFAULT 1
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE PIPELINES failed: %v", err)
	}

	// Record changelog entry
	_, err = coord.ExecuteSQL(`
		INSERT INTO DATABASECHANGELOG (ID, AUTHOR, FILENAME, DATEEXECUTED, ORDEREXECUTED, EXECTYPE, DESCRIPTION)
		VALUES ('1', 'gocd', 'db-migration-scripts/initial/create-schema.xml', '2026-11-16 12:00:00', 1, 'EXECUTED', 'initial schema');
	`)
	if err != nil {
		t.Fatalf("INSERT into DATABASECHANGELOG failed: %v", err)
	}

	// Release lock
	_, err = coord.ExecuteSQL(`UPDATE DATABASECHANGELOGLOCK SET LOCKED = 0, LOCKEDBY = NULL WHERE ID = 1;`)
	if err != nil {
		t.Fatalf("Release lock failed: %v", err)
	}

	// 5. Test GoCD Agent registration & Pipeline trigger DML
	_, err = coord.ExecuteSQL(`INSERT INTO AGENTS (UUID, HOSTNAME, IPADDRESS, ISDISABLED) VALUES ('agent-uuid-101', 'build-worker-1', '10.0.1.20', 0);`)
	if err != nil {
		t.Fatalf("Register agent failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO PIPELINES (NAME, COUNTER) VALUES ('prod-deploy-pipeline', 1);`)
	if err != nil {
		t.Fatalf("Trigger pipeline failed: %v", err)
	}

	// 6. Verification Queries
	res, err = coord.ExecuteSQL(`SELECT a.HOSTNAME, p.NAME, p.COUNTER FROM AGENTS a, PIPELINES p WHERE a.UUID = 'agent-uuid-101';`)
	if err != nil {
		t.Fatalf("Pipeline query failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0]["HOSTNAME"] != "build-worker-1" || res.Rows[0]["NAME"] != "prod-deploy-pipeline" {
		t.Errorf("unexpected query result: %v", res.Rows[0])
	}

	// 7. PostgreSQL Trigger Function & EXECUTE PROCEDURE DDL (GoCD Liquibase migration syntax)
	_, err = coord.ExecuteSQL(`
		CREATE OR REPLACE FUNCTION update_stage_status_fn() RETURNS TRIGGER AS $$
		BEGIN
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;
	`)
	if err != nil {
		t.Fatalf("CREATE OR REPLACE FUNCTION ... RETURNS TRIGGER failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`
		CREATE TRIGGER trg_pipeline_status AFTER INSERT ON PIPELINES
		FOR EACH ROW EXECUTE PROCEDURE update_stage_status_fn();
	`)
	if err != nil {
		t.Fatalf("CREATE TRIGGER ... EXECUTE PROCEDURE failed: %v", err)
	}

	// 8. PostgreSQL JDBC DatabaseMetaData.getColumns() required fields (nspname, atttypmod, attnotnull, attlen)
	colMetaRes, err := coord.ExecuteSQL(`
		SELECT n.nspname, c.relname, a.attname, a.atttypid, a.attnotnull, a.atttypmod, a.attlen, a.attnum
		FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON (c.relnamespace = n.oid)
		JOIN pg_catalog.pg_attribute a ON (c.oid = a.attrelid)
		WHERE c.relname = 'PIPELINES'
	`)
	if err != nil {
		t.Fatalf("JDBC getColumns metadata query failed: %v", err)
	}
	if len(colMetaRes.Rows) == 0 {
		t.Fatalf("expected column metadata rows for PIPELINES, got 0")
	}
	if colMetaRes.Rows[0]["nspname"] == nil || colMetaRes.Rows[0]["atttypmod"] == nil {
		t.Errorf("expected non-nil nspname and atttypmod in pg_attribute join, got %v", colMetaRes.Rows[0])
	}

	// 9. GoCD _builds View creation & wildcard + alias projection
	_, err = coord.ExecuteSQL(`
		CREATE TABLE BUILDS (
			ID BIGINT IDENTITY(1,1) PRIMARY KEY,
			NAME VARCHAR(255) NOT NULL,
			STATE VARCHAR(50) NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE BUILDS failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO BUILDS (NAME, STATE) VALUES ('compile-job', 'Scheduled');`)
	if err != nil {
		t.Fatalf("INSERT INTO BUILDS failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`CREATE VIEW _builds AS SELECT ID, NAME, STATE FROM BUILDS;`)
	if err != nil {
		t.Fatalf("CREATE VIEW _builds failed: %v", err)
	}
	viewRes, err := coord.ExecuteSQL(`SELECT *, id AS buildId FROM _builds WHERE state != 'Completed';`)
	if err != nil {
		t.Fatalf("SELECT from _builds view failed: %v", err)
	}
	if len(viewRes.Rows) != 1 || viewRes.Rows[0]["buildId"] == nil {
		t.Errorf("expected 1 row with buildId from _builds view, got %v", viewRes.Rows)
	}
}
