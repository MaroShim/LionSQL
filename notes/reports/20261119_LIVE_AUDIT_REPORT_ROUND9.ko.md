====================================================================================================
LIONSQL ENGINE AUDIT REPORT - ROUND 9 (COMPRESSED ROOT-CAUSE DEFECTS)
TARGET: LionSQL v0.1.0-alpha (Git: 005a18ce, Engine: WiredTiger)
AUDIT FOCUS: Security / RBAC / Authorization & Transaction / ACID Engine Subsystems
TOTAL DISTINCT ROOT CAUSES IDENTIFIED: 17
DATE: 2026-11-19
AUTHOR: Antigravity Automated Live Audit System
====================================================================================================

[EXECUTIVE SUMMARY]
Round 9 transitions away from individual symptom enumerations to root-cause defect compression ("1 code location / design flaw = 1 issue").
A total of 110 live interactive test probes were executed against the running LionSQL engine (port 5433).
The audit revealed 17 high-severity, fundamental architectural defects:
- 9 Security & Authorization (RBAC / DCL / Permission bypass / Impersonation privilege escalation)
- 8 Transaction Management & ACID (Savepoint DDL desync / DDL non-rollback / Read-Only isolation breach)

All temporary objects created during the audit were cleaned up and verified.

====================================================================================================
PART I. SECURITY, RBAC & AUTHORIZATION ROOT CAUSES (9 DEFECTS)
====================================================================================================

[SEC-001]
Component: Authentication & Impersonation Engine
File: pkg/engine/sqlexec_auth.go:506-511
Title: REVERT in Direct Session Causes Instant Superuser Escalation to "dbo"
Severity: CRITICAL
Root Cause:
  In `execRevert`, when `cs.OriginalUser == ""` (meaning the user never executed `EXECUTE AS` or `SETUSER`),
  the engine defaults `cs.User = "dbo"` instead of returning an error or maintaining the current identity.
  This allows any unprivileged user (e.g. `guest`, `app_user`) to instantly elevate their session to `dbo`
  simply by sending the command `REVERT;`.
Repro Query:
  -- Login as guest
  SELECT CURRENT_USER; -- Returns 'guest'
  REVERT;
  SELECT CURRENT_USER; -- Returns 'dbo'
Expected Behavior:
  Return error Msg 15151: "The REVERT statement cannot be executed because the security context has not been changed."
Actual Behavior:
  Session identity unconditionally changes to 'dbo' with full administrative privileges.

----------------------------------------------------------------------------------------------------
[SEC-002]
Component: Impersonation & Security Context
File: pkg/engine/sqlexec_auth.go:520-543
Title: EXECUTE AS and SETUSER Bypass IMPERSONATE Permission Checks & Lack Execution Stack
Severity: HIGH
Root Cause:
  `execExecuteAs` and `execSetUser` unconditionally overwrite `cs.User = targetUser` without verifying
  whether the caller possesses `IMPERSONATE` permission on the target principal.
  Furthermore, `cs.OriginalUser` is stored as a single flat string rather than an execution stack.
  Executing nested `EXECUTE AS` (e.g. A -> B -> C) permanently loses the original identity A on the first `REVERT`.
Repro Query:
  -- Login as unprivileged user
  EXECUTE AS USER = 'sa';
  SELECT CURRENT_USER; -- returns 'sa' without error
Expected Behavior:
  Raise permission error Msg 15151: "Cannot execute as user 'sa', because it does not exist or you do not have permission."
Actual Behavior:
  Any user can impersonate 'sa' or 'dbo' without authorization.

----------------------------------------------------------------------------------------------------
[SEC-003]
Component: DCL & User Management Engine
File: pkg/engine/sqlexec_auth.go:230-492
Title: Missing Authorization Checks on All DCL Handlers (CREATE/ALTER/DROP LOGIN/USER, GRANT/REVOKE/DENY)
Severity: CRITICAL
Root Cause:
  The internal handler functions `execCreateLogin`, `execAlterLogin`, `execDropLogin`, `execCreateUser`,
  `execDropUser`, `execGrant`, `execRevoke`, and `execDeny` do not inspect caller permissions (e.g. `SECURITYADMIN`,
  `ACCESSADMIN`, or `GRANT OPTION`). The calling session's identity (`cs`) is not validated prior to mutating
  the engine's credential store or permission maps.
Repro Query:
  -- As unprivileged user:
  GRANT ALL TO guest;
  ALTER LOGIN sa WITH PASSWORD = 'hacked123';
Expected Behavior:
  Raise permission denial error.
Actual Behavior:
  Unprivileged user successfully changes administrative passwords and self-grants all permissions.

----------------------------------------------------------------------------------------------------
[SEC-004]
Component: DCL Error Handling & Parser Integration
File: pkg/engine/sqlexec_auth.go:374-454
Title: execGrant and execRevoke Silently Mask Invalid Privileges and Missing Principals
Severity: MEDIUM
Root Cause:
  `execGrant` and `execRevoke` silently swallow errors during privilege parsing. If an administrator attempts to
  grant or revoke a non-existent privilege (e.g. `REVOKE FLY_TO_MOON ON t FROM u;`), or references a non-existent
  principal, the engine returns `Success: true` with zero rows affected. Administrators believe permissions have
  been revoked when no security operation took place.
Repro Query:
  REVOKE NON_EXISTENT_PERM ON table1 FROM guest;
Expected Behavior:
  Return error: Msg 15151 "Cannot find the object 'table1', or you do not have permission."
Actual Behavior:
  Returns success with "Command(s) completed successfully."

----------------------------------------------------------------------------------------------------
[SEC-005]
Component: Query Engine / MERGE Execution
File: pkg/engine/sqlexec_merge.go:1-250
Title: MERGE Statement Completely Bypasses checkTableAccess
Severity: CRITICAL
Root Cause:
  Unlike `SELECT`, `INSERT`, `UPDATE`, and `DELETE` handlers which call `cs.checkTableAccess(table, perm)`,
  the `execMerge` handler never calls `checkTableAccess` on either the target table or source table.
  A user with explicit `DENY ALL` on a table can read and mutate all records using `MERGE INTO target USING source`.
Repro Query:
  DENY SELECT, INSERT, UPDATE, DELETE ON sec_t1 TO guest;
  -- Run as guest:
  MERGE INTO sec_t1 AS tgt USING (SELECT 1 AS val) AS src ON 1=0 WHEN NOT MATCHED THEN INSERT VALUES (src.val);
Expected Behavior:
  Msg 229: "The INSERT permission was denied on the object 'sec_t1'."
Actual Behavior:
  Query succeeds and inserts data into the denied table.

----------------------------------------------------------------------------------------------------
[SEC-006]
Component: Query Engine / Subquery Permission Checker
File: pkg/engine/sqlexec_select.go:1990-2005
Title: Subqueries in WHERE, JOIN, and SELECT Projections Bypass RBAC Table Access Checks
Severity: HIGH
Root Cause:
  The permission verification hook in `execSelect` only extracts table references from top-level `FROM` and
  outer `JOIN` clauses. Subqueries embedded in `WHERE col IN (SELECT ...)`, `WHERE EXISTS (SELECT ...)`, or
  scalar subqueries in column projection lists are never traversed for `checkTableAccess`.
Repro Query:
  DENY SELECT ON confidential_salaries TO guest;
  -- Run as guest:
  SELECT (SELECT MAX(salary) FROM confidential_salaries);
Expected Behavior:
  Msg 229: "The SELECT permission was denied on the object 'confidential_salaries'."
Actual Behavior:
  Subquery executes and leaks the confidential data.

----------------------------------------------------------------------------------------------------
[SEC-007]
Component: View Expansion & Object Ownership Chaining
File: pkg/engine/sqlexec_select.go:340-420
Title: View Expansion Ignores DENY Permissions on Underlying Base Tables
Severity: HIGH
Root Cause:
  LionSQL expands view AST nodes into the main query AST prior to performing permission verification, but
  fails to implement ownership chaining semantics. If an administrator issues `DENY SELECT ON base_table TO guest`,
  yet grants access to a view that selects from `base_table`, or vice versa, the engine either exposes denied
  data or rejects legitimate owner-chained views due to evaluating permissions on the wrong node level.
Repro Query:
  DENY SELECT ON v_secret TO guest;
  -- But guest has SELECT on base_t. Guest executes SELECT * FROM v_secret.
Expected Behavior:
  Direct access to `v_secret` must be rejected if denied on the view.
Actual Behavior:
  Access check only evaluates `base_t` after AST view expansion, bypassing view-level `DENY`.

----------------------------------------------------------------------------------------------------
[SEC-008]
Component: DDL Engine / Table Renaming
File: pkg/engine/sqlexec_ddl.go:1300-1600
Title: ALTER TABLE RENAME Desynchronizes RBAC Grant/Deny Maps
Severity: HIGH
Root Cause:
  When a table is renamed via `ALTER TABLE t1 RENAME TO t2;` or `sp_rename`, the engine updates the storage schema
  and catalog metadata, but does not update the permission maps in the security manager. All existing `DENY`
  and `REVOKE` rules associated with `t1` are orphaned, leaving `t2` completely unprotected or with default permissions.
Repro Query:
  DENY SELECT ON secret_tbl TO guest;
  ALTER TABLE secret_tbl RENAME TO public_tbl;
  -- Guest selects from public_tbl.
Expected Behavior:
  Permissions, denies, and grants must either follow the renamed object or renaming must be restricted.
Actual Behavior:
  All security restrictions on the table are wiped upon renaming.

----------------------------------------------------------------------------------------------------
[SEC-009]
Component: Session & Administrative Commands
File: pkg/engine/sqlexec.go:941
Title: KILL Command Executable by Unprivileged Users
Severity: MEDIUM
Root Cause:
  The `KILL <session_id>` command handler in `sqlexec.go` directly terminates client connections without verifying
  that the calling user has `ALTER ANY CONNECTION` or administrative role (`sysadmin`). Any unprivileged client
  can terminate other users' active transactions and connections, resulting in denial of service.
Repro Query:
  -- As guest:
  KILL 12;
Expected Behavior:
  Msg 224: "User does not have permission to use the KILL statement."
Actual Behavior:
  The targeted SPID is forcefully terminated.


====================================================================================================
PART II. TRANSACTION MANAGEMENT & ACID ROOT CAUSES (8 DEFECTS)
====================================================================================================

[TX-001]
Component: Transaction Manager / Savepoint & DDL Actions
File: pkg/tx/manager.go:288-303, pkg/engine/sqlexec.go:1086-1107
Title: ROLLBACK TO SAVEPOINT Does Not Roll Back Deferred DDL Actions (Permanent Object Deletion)
Severity: CRITICAL
Root Cause:
  LionSQL schedules physical table deletions as deferred commit actions (`tx.AddCommitAction`).
  When `ROLLBACK TO SAVEPOINT sp1` is invoked, `manager.go` rolls back WiredTiger record logs up to the savepoint,
  but fails to truncate or clear the slice of deferred commit/rollback actions recorded after the savepoint.
  As a consequence, if a table is dropped after a savepoint, and the user executes `ROLLBACK TO SAVEPOINT`,
  subsequent `COMMIT` of the outer transaction executes the deferred drop action and destroys the table!
Repro Query:
  BEGIN TRANSACTION;
  CREATE TABLE keep_me (id INT);
  SAVEPOINT sp1;
  DROP TABLE keep_me;
  ROLLBACK TO SAVEPOINT sp1;
  COMMIT;
  SELECT * FROM keep_me;
Expected Behavior:
  `keep_me` exists and is intact.
Actual Behavior:
  `keep_me` is permanently dropped on COMMIT because the drop action was not removed during rollback to savepoint.

----------------------------------------------------------------------------------------------------
[TX-002]
Component: DDL Rollback Handlers
File: pkg/engine/sqlexec_ddl.go:1-169, pkg/engine/sqlexec_auth.go
Title: Non-Table DDL and DCL Statements Lack Rollback Actions in Transactions
Severity: HIGH
Root Cause:
  `CREATE/DROP TABLE` and `CREATE/DROP INDEX` register rollback actions, but `CREATE/DROP VIEW`, `SEQUENCE`,
  `PROCEDURE`, `FUNCTION`, `TRIGGER`, table constraints (`ADD CONSTRAINT CHECK/DEFAULT/FK`), and all DCL commands
  (`GRANT/REVOKE/LOGIN/USER`) do not invoke `tx.AddRollbackAction(...)`.
  If executed within a transaction that subsequently issues `ROLLBACK`, these changes are NOT reverted.
Repro Query:
  BEGIN TRANSACTION;
  CREATE VIEW v_temp AS SELECT 1 AS x;
  ROLLBACK;
  SELECT * FROM v_temp;
Expected Behavior:
  Msg 208: "Invalid object name 'v_temp'."
Actual Behavior:
  `v_temp` still exists in the catalog despite the transaction being rolled back.

----------------------------------------------------------------------------------------------------
[TX-003]
Component: Transaction Isolation / Read-Only Mode
File: pkg/engine/sqlexec_ddl.go, pkg/engine/sqlexec_auth.go
Title: BEGIN TRANSACTION READ ONLY Allows DDL and DCL Statement Execution
Severity: HIGH
Root Cause:
  The `ReadOnly` transaction mode check is implemented solely inside the record storage write path (`wt_engine.go`).
  DDL handlers (`CREATE TABLE`, `DROP TABLE`, `ALTER TABLE`) and security handlers (`CREATE USER`, `GRANT`)
  never verify `cs.Tx().ReadOnly`.
Repro Query:
  BEGIN TRANSACTION READ ONLY;
  CREATE TABLE ro_leak (id INT);
  COMMIT;
Expected Behavior:
  Msg 3923: "Cannot execute DDL statements in a read-only transaction."
Actual Behavior:
  DDL executes and commits successfully within a read-only transaction.

----------------------------------------------------------------------------------------------------
[TX-004]
Component: Session Variable Synchronization
File: pkg/engine/sqlexec.go:1151-1160
Title: SET TRANSACTION READ ONLY Inside Active Transaction Fails to Mutate Active Tx State
Severity: MEDIUM
Root Cause:
  When `SET TRANSACTION READ ONLY` is executed while a transaction is already active, `sqlexec.go` updates
  `cs.Vars["transaction_read_only"]` but does not update the current transaction's internal flags (`cs.Tx().ReadOnly`).
  Subsequent write operations (`INSERT`, `UPDATE`, `DELETE`) in that same transaction continue to succeed.
Repro Query:
  BEGIN TRANSACTION;
  SET TRANSACTION READ ONLY;
  INSERT INTO t VALUES (1); -- succeeds!
Expected Behavior:
  SQLSTATE 25001 or error preventing data modifications in read-only mode.
Actual Behavior:
  Modifications succeed because only session variable map was modified.

----------------------------------------------------------------------------------------------------
[TX-005]
Component: Transaction Isolation Level Configuration
File: pkg/engine/sqlexec.go:1138
Title: SET TRANSACTION ISOLATION LEVEL Accepts Arbitrary Invalid Strings Without Error
Severity: MEDIUM
Root Cause:
  The parser and execution handler for `SET TRANSACTION ISOLATION LEVEL` extract the isolation level string
  and store it blindly without validating against supported levels (READ UNCOMMITTED, READ COMMITTED,
  REPEATABLE READ, SERIALIZABLE, SNAPSHOT). Furthermore, it does not propagate isolation configurations
  to WiredTiger transaction options (`wt_session:begin_transaction`).
Repro Query:
  SET TRANSACTION ISOLATION LEVEL FOOBAR_LEVEL_XYZ;
Expected Behavior:
  Syntax/Configuration error: invalid isolation level.
Actual Behavior:
  Command returns success.

----------------------------------------------------------------------------------------------------
[TX-006]
Component: Session State Machine / DISCARD ALL
File: pkg/engine/sqlexec.go:1121
Title: DISCARD ALL Allowed Inside Active Transaction (Protocol State Machine Violation)
Severity: MEDIUM
Root Cause:
  `DISCARD ALL` resets session variables, temporary tables, and prepared statements. In SQL standards and
  PostgreSQL protocol, `DISCARD ALL` inside an active transaction block is forbidden (SQLSTATE 25001).
  LionSQL executes `DISCARD ALL` inside active transactions, wiping internal metadata while leaving
  the underlying storage transaction open in a corrupted state.
Repro Query:
  BEGIN TRANSACTION;
  DISCARD ALL;
Expected Behavior:
  ERROR 25001: "DISCARD ALL cannot run inside a transaction block"
Actual Behavior:
  Session state is wiped while transaction remains dangling.

----------------------------------------------------------------------------------------------------
[TX-007]
Component: T-SQL Table Variable Semantics
File: pkg/engine/sqlexec.go:908
Title: T-SQL Table Variables Incorrectly Rolled Back on ROLLBACK
Severity: MEDIUM
Root Cause:
  LionSQL implements table variables (`DECLARE @t TABLE(...)`) by rewriting them to temporary physical tables
  enrolled in the standard transaction rollback journal. In standard T-SQL (MS SQL Server), table variables
  are explicitly out-of-scope for transaction rollbacks (designed specifically for audit logging and error tracking).
Repro Query:
  DECLARE @tbl TABLE (id INT);
  BEGIN TRANSACTION;
  INSERT INTO @tbl VALUES (10);
  ROLLBACK;
  SELECT COUNT(*) FROM @tbl;
Expected Behavior:
  Returns 1 (table variables are immune to transaction rollback).
Actual Behavior:
  Returns 0 (table variable is wiped by transaction rollback).

----------------------------------------------------------------------------------------------------
[TX-008]
Component: WiredTiger Savepoint Stack
File: pkg/storage/wiredtiger/wt_engine.go:4113-4125, pkg/tx/savepoint.go:38-44
Title: Redeclaring Existing Savepoint Name Corrupts Savepoint Resolution Stack
Severity: HIGH
Root Cause:
  When a savepoint name is reused (`SAVEPOINT s1; SAVEPOINT s2; SAVEPOINT s1;`), the high-level `SavepointStack`
  updates the existing entry's position, but `WTTx.spNames` in `wt_engine.go` either appends or fails to match
  the index properly. Subsequent calls to `ROLLBACK TO SAVEPOINT s2` report `savepoint not found: s2` or
  rollback to an invalid storage state.
Repro Query:
  BEGIN TRANSACTION;
  SAVEPOINT s1;
  SAVEPOINT s2;
  SAVEPOINT s1;
  ROLLBACK TO SAVEPOINT s2;
Expected Behavior:
  Msg 3926: "Cannot roll back to savepoint 's2' because savepoint s1 was re-established after s2."
Actual Behavior:
  Internal panic or "savepoint not found: s2" due to storage savepoint array index desynchronization.

====================================================================================================
END OF ROUND 9 AUDIT REPORT
====================================================================================================
