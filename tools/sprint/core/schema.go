package core

import "strings"

// SchemaDDL is the deterministic SQL DDL for sprint_tasks table and index.
const SchemaDDL = `
CREATE TABLE IF NOT EXISTS sprint_tasks (
    id VARCHAR(64) PRIMARY KEY,
    domain VARCHAR(50) DEFAULT 'Engine',
    title VARCHAR(255),
    priority INT DEFAULT 2,
    status VARCHAR(20) DEFAULT 'PENDING',
    plan_doc VARCHAR(255),
    metrics TEXT,
    target_files TEXT,
    phases TEXT,
    description TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sprint_tasks_status_prio ON sprint_tasks (status, priority);`

// EscapeSQL sanitizes string literals for SQL embedding.
func EscapeSQL(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
