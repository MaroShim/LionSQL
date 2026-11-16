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
}
