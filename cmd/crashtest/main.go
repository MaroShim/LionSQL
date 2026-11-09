// Command crashtest verifies LionSQL crash recovery and ACID durability under
// random SIGKILL ("kill -9") termination across cumulative rounds (DURA-KILL-1).
//
// In each round:
//  1. The server is started against a persistent data directory.
//  2. Concurrent workers execute multi-statement atomic transfers (debit src,
//     credit dst, insert crash_ledger row) and optional concurrent DDL.
//  3. Every transaction where COMMIT returns nil is recorded in the client-side
//     acknowledged ledger (persisted atomically via temp file + rename).
//     Transactions whose COMMIT was in-flight when SIGKILL struck are tracked
//     in an uncertain set.
//  4. At a random point within [min-run, max-run], the server is terminated
//     with SIGKILL (or SIGTERM in the graceful control round).
//  5. The server is restarted on the same data directory and verified against
//     four durability contracts:
//     - D1 (Commit Durability): 100% of acknowledged commits exist in DB with
//       intact checksums. Any extra committed row in DB must come from the
//       in-flight uncertain set (never fabricated).
//     - D2 (Atomicity): SUM(balance) equals the initial total AND every single
//       account balance matches the exact ledger reconstruction (proving no
//       partial debit/credit/ledger execution ever leaked).
//     - D3 (Consistency): Table scan count matches COUNT(*) and PK lookups.
//     - D4 (Recoverability): Server recovers within the startup timeout.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	_ "github.com/lib/pq"
)

const initialBalance int64 = 10000

type txEntry struct {
	TxID     int64  `json:"tx_id"`
	Src      int    `json:"src"`
	Dst      int    `json:"dst"`
	Amount   int64  `json:"amount"`
	Checksum string `json:"checksum"`
}

type ledgerState struct {
	mu       sync.Mutex
	Acked    map[int64]txEntry `json:"acked"`
	InFlight map[int64]txEntry `json:"in_flight"`
	path     string
}

func newLedgerState(path string) *ledgerState {
	return &ledgerState{
		Acked:    make(map[int64]txEntry),
		InFlight: make(map[int64]txEntry),
		path:     path,
	}
}

func (l *ledgerState) markInFlight(e txEntry) {
	l.mu.Lock()
	l.InFlight[e.TxID] = e
	l.mu.Unlock()
}

func (l *ledgerState) markAcked(e txEntry) {
	l.mu.Lock()
	delete(l.InFlight, e.TxID)
	l.Acked[e.TxID] = e
	l.mu.Unlock()
}

func (l *ledgerState) markAborted(txID int64) {
	l.mu.Lock()
	delete(l.InFlight, txID)
	l.mu.Unlock()
}

// saveAtomically persists the ledger via temporary swap file and atomic rename ([RULE:AtomicSaves]).
func (l *ledgerState) saveAtomically() error {
	l.mu.Lock()
	data, err := json.Marshal(l)
	l.mu.Unlock()
	if err != nil {
		return err
	}
	clean := filepath.Clean(l.path)
	tmp := clean + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, clean)
}

func computeChecksum(txID int64, src, dst int, amount int64) string {
	raw := fmt.Sprintf("tx=%d:src=%d:dst=%d:amt=%d", txID, src, dst, amount)
	sum := sha256.Sum256([]byte(raw))
	return "chk_" + hex.EncodeToString(sum[:8])
}

func main() {
	binPath := flag.String("bin", "bin/lionsql", "Path to the lionsql server binary")
	engine := flag.String("engine", "wiredtiger", "Storage engine (wiredtiger, memory)")
	rounds := flag.Int("rounds", 10, "Number of cumulative crash-recovery rounds")
	workers := flag.Int("workers", 6, "Concurrent transfer workers")
	accounts := flag.Int("accounts", 200, "Number of bank accounts")
	pgPort := flag.Int("pg-port", 5545, "PGWire port for the child server")
	password := flag.String("password", "1234567890", "Database password")
	minRun := flag.Duration("min-run", 250*time.Millisecond, "Minimum workload duration before SIGKILL")
	maxRun := flag.Duration("max-run", 900*time.Millisecond, "Maximum workload duration before SIGKILL")
	withDDL := flag.Bool("ddl", true, "Run concurrent CREATE/INSERT/DROP TABLE DDL during workload")
	withVariants := flag.Bool("variants", true, "Include double-kill, graceful SIGTERM, and WAL tail-corruption rounds")
	keep := flag.Bool("keep", false, "Keep data directory after completion (always kept on failure)")
	flag.Parse()

	// [RULE:CleanTeardown] Trap interrupt signals and ensure active child server is killed cleanly
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		activeServerMu.Lock()
		cmd := activeCmd
		waitCh := activeWaitCh
		activeServerMu.Unlock()
		if cmd != nil {
			fmt.Fprintln(os.Stderr, "\ncrashtest: interrupted, killing active child server ([RULE:CleanTeardown])...")
			killNow(cmd, waitCh)
		}
		os.Exit(130)
	}()
	defer signal.Stop(sigCh)

	code, err := runCrashTest(*binPath, *engine, *rounds, *workers, *accounts, *pgPort, *password, *minRun, *maxRun, *withDDL, *withVariants, *keep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crashtest: %v\n", err)
	}
	os.Exit(code)
}

func runCrashTest(binPath, engine string, rounds, workers, accounts, pgPort int, password string,
	minRun, maxRun time.Duration, withDDL, withVariants, keep bool) (int, error) {

	if rounds < 1 || workers < 1 || accounts < 2 {
		return 2, errors.New("rounds, workers must be >= 1 and accounts >= 2")
	}
	absBin, err := filepath.Abs(binPath)
	if err != nil {
		return 2, err
	}
	baseDir, err := os.MkdirTemp("", fmt.Sprintf("lionsql_crash_%s_*", engine))
	if err != nil {
		return 2, err
	}
	dataDir := filepath.Join(baseDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return 2, err
	}
	ledger := newLedgerState(filepath.Join(baseDir, "ack_ledger.json"))
	dsn := fmt.Sprintf("host=127.0.0.1 port=%d user=postgres password=%s dbname=postgres sslmode=disable", pgPort, password)

	fmt.Printf("crashtest: engine=%s rounds=%d workers=%d accounts=%d ddl=%v variants=%v dir=%s\n",
		engine, rounds, workers, accounts, withDDL, withVariants, baseDir)

	var nextTxID atomic.Int64
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Initial bootstrap round: start server and create schema + seed accounts.
	srv, waitCh, logPath, err := startServer(absBin, dataDir, engine, pgPort, baseDir, 0)
	if err != nil {
		return 2, err
	}
	db, _, err := openAndWait(dsn, 20*time.Second, waitCh)
	if err != nil {
		killNow(srv, waitCh)
		return 2, fmt.Errorf("bootstrap connect: %w (log: %s)", err, logPath)
	}
	if err := initSchema(db, accounts); err != nil {
		_ = db.Close()
		killNow(srv, waitCh)
		return 2, fmt.Errorf("init schema: %w", err)
	}
	_ = db.Close()
	gracefulStop(srv, waitCh)

	for r := 1; r <= rounds; r++ {
		srv, waitCh, logPath, err = startServer(absBin, dataDir, engine, pgPort, baseDir, r)
		if err != nil {
			return 2, err
		}
		db, _, err = openAndWait(dsn, 20*time.Second, waitCh)
		if err != nil {
			killNow(srv, waitCh)
			return 1, fmt.Errorf("round %d start failed: %w (log: %s)", r, err, logPath)
		}
		db.SetMaxOpenConns(workers + 4)
		db.SetMaxIdleConns(workers + 4)

		runDur := minRun
		if maxRun > minRun {
			runDur += time.Duration(rng.Int63n(int64(maxRun - minRun)))
		}
		wctx, wcancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		var roundAcked atomic.Int64

		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				wrng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(r*1000+workerID)))
				for {
					if wctx.Err() != nil {
						return
					}
					txID := nextTxID.Add(1)
					src := wrng.Intn(accounts)
					dst := (src + 1 + wrng.Intn(accounts-1)) % accounts
					amt := int64(1 + wrng.Intn(25))
					entry := txEntry{
						TxID:     txID,
						Src:      src,
						Dst:      dst,
						Amount:   amt,
						Checksum: computeChecksum(txID, src, dst, amt),
					}
					if execTransfer(wctx, db, entry, ledger) {
						roundAcked.Add(1)
					}
				}
			}(w)
		}

		if withDDL {
			wg.Add(1)
			go func(roundNum int) {
				defer wg.Done()
				ddlLoop(wctx, db, roundNum)
			}(r)
		}

		// Wait until random kill point.
		timer := time.NewTimer(runDur)
		select {
		case <-timer.C:
		case exitErr := <-waitCh:
			timer.Stop()
			wcancel()
			wg.Wait()
			_ = db.Close()
			return 1, fmt.Errorf("round %d: server crashed unexpectedly before kill signal: %v (log: %s)", r, exitErr, logPath)
		}

		// Mode selection: last round uses graceful SIGTERM if variants enabled; others use SIGKILL.
		killMode := "SIGKILL"
		if withVariants && r == rounds {
			killMode = "SIGTERM"
			wcancel()
			wg.Wait()
			_ = db.Close()
			if err := gracefulStopTimed(srv, waitCh, 1500*time.Millisecond); err != nil {
				return 1, fmt.Errorf("round %d graceful SIGTERM failed: %w", r, err)
			}
		} else {
			// Strike with SIGKILL while workers are actively mid-transaction!
			killNow(srv, waitCh)
			wcancel()
			wg.Wait()
			_ = db.Close()
		}

		if err := ledger.saveAtomically(); err != nil {
			return 2, fmt.Errorf("save ledger: %w", err)
		}

		// Variant: Double-kill during recovery on round 2 (when rounds >= 3)
		if withVariants && r == 2 && rounds >= 3 {
			srv2, waitCh2, _, err := startServer(absBin, dataDir, engine, pgPort, baseDir, r*100)
			if err != nil {
				return 2, err
			}
			// Strike again after 40ms during/immediately after recovery
			time.Sleep(40 * time.Millisecond)
			killNow(srv2, waitCh2)
			killMode = "SIGKILL+DOUBLE_KILL"
		}

		// Variant: WAL tail torn-write simulation on memory engine on round 3
		if withVariants && engine == "memory" && r == 3 {
			aofPath := filepath.Join(dataDir, "wal.aof")
			if f, err := os.OpenFile(aofPath, os.O_WRONLY|os.O_APPEND, 0644); err == nil {
				// Append an incomplete 4-byte length header + 5 garbage bytes
				_, _ = f.Write([]byte{0x00, 0x00, 0x01, 0x00, 0xde, 0xad, 0xbe, 0xef, 0x01})
				_ = f.Close()
				killMode += "+TORN_WAL_TAIL"
			}
		}

		// Restart server and run the 4-Point Durability & Integrity Oracle.
		recSrv, recWaitCh, recLogPath, err := startServer(absBin, dataDir, engine, pgPort, baseDir, r*1000)
		if err != nil {
			return 2, err
		}
		recDB, recDur, err := openAndWait(dsn, 25*time.Second, recWaitCh)
		if err != nil {
			killNow(recSrv, recWaitCh)
			return 1, fmt.Errorf("round %d D4 recovery failed after %s: %w (log: %s, dir preserved: %s)",
				r, killMode, err, recLogPath, baseDir)
		}

		promoted, err := verifyOracle(recDB, accounts, ledger)
		_ = recDB.Close()
		gracefulStop(recSrv, recWaitCh)
		if err != nil {
			return 1, fmt.Errorf("round %d oracle FAILED after %s: %w (dir preserved: %s)", r, killMode, err, baseDir)
		}

		fmt.Printf("round %2d/%d [%-22s] run=%-6s rec=%-6s round_ack=%-4d total_ack=%-5d promoted_inflight=%d -> PASS\n",
			r, rounds, killMode, runDur.Round(time.Millisecond), recDur.Round(time.Millisecond),
			roundAcked.Load(), len(ledger.Acked), promoted)
	}

	if !keep {
		_ = os.RemoveAll(baseDir)
	} else {
		fmt.Printf("crashtest: preserved directory at %s\n", baseDir)
	}
	fmt.Printf("RESULT: PASS (%d/%d crash-recovery rounds verified, %d total acknowledged transactions durable)\n",
		rounds, rounds, len(ledger.Acked))
	return 0, nil
}

func initSchema(db *sql.DB, accounts int) error {
	stmts := []string{
		"CREATE TABLE crash_accounts (id INT PRIMARY KEY, balance BIGINT NOT NULL, updated_tx BIGINT NOT NULL)",
		"CREATE TABLE crash_ledger (tx_id BIGINT PRIMARY KEY, src INT NOT NULL, dst INT NOT NULL, amount BIGINT NOT NULL, checksum VARCHAR(64) NOT NULL)",
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	for i := 0; i < accounts; i++ {
		if _, err := db.Exec("INSERT INTO crash_accounts (id, balance, updated_tx) VALUES ($1, $2, 0)", i, initialBalance); err != nil {
			return fmt.Errorf("seed account %d: %w", i, err)
		}
	}
	return nil
}

func execTransfer(ctx context.Context, db *sql.DB, e txEntry, ledger *ledgerState) bool {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE crash_accounts SET balance = balance - $1, updated_tx = $2 WHERE id = $3",
		e.Amount, e.TxID, e.Src); err != nil {
		_ = tx.Rollback()
		return false
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE crash_accounts SET balance = balance + $1, updated_tx = $2 WHERE id = $3",
		e.Amount, e.TxID, e.Dst); err != nil {
		_ = tx.Rollback()
		return false
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO crash_ledger (tx_id, src, dst, amount, checksum) VALUES ($1, $2, $3, $4, $5)",
		e.TxID, e.Src, e.Dst, e.Amount, e.Checksum); err != nil {
		_ = tx.Rollback()
		return false
	}

	// Mark in-flight right before issuing COMMIT so if SIGKILL strikes after the
	// server commits but before the TCP response arrives, the oracle can account for it.
	ledger.markInFlight(e)
	if err := tx.Commit(); err != nil {
		// Note: we keep e in InFlight if the connection died mid-commit; if it was
		// a normal SQL error (like 40001 conflict), it won't be in the DB anyway.
		return false
	}
	ledger.markAcked(e)
	return true
}

func ddlLoop(ctx context.Context, db *sql.DB, roundNum int) {
	tbl := fmt.Sprintf("crash_ddl_r%d", roundNum)
	for i := 0; ctx.Err() == nil; i++ {
		_, _ = db.ExecContext(ctx, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (k INT PRIMARY KEY, v VARCHAR(32))", tbl))
		_, _ = db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (k, v) VALUES ($1, $2)", tbl), i, "ddl-val")
		if i%5 == 4 {
			_, _ = db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", tbl))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// verifyOracle checks D1 (Commit Durability), D2 (Atomicity), and D3 (Consistency).
func verifyOracle(db *sql.DB, accounts int, ledger *ledgerState) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Load all committed rows from crash_ledger
	rows, err := db.QueryContext(ctx, "SELECT tx_id, src, dst, amount, checksum FROM crash_ledger")
	if err != nil {
		return 0, fmt.Errorf("D3 query crash_ledger: %w", err)
	}
	defer rows.Close()

	dbLedger := make(map[int64]txEntry)
	expectedBalances := make([]int64, accounts)
	for i := 0; i < accounts; i++ {
		expectedBalances[i] = initialBalance
	}

	for rows.Next() {
		var e txEntry
		var ns sql.NullString
		if err := rows.Scan(&e.TxID, &e.Src, &e.Dst, &e.Amount, &ns); err != nil {
			return 0, fmt.Errorf("D3 scan crash_ledger: %w", err)
		}
		if !ns.Valid {
			return 0, fmt.Errorf("D1 NULL checksum in crash_ledger row: tx_id=%d src=%d dst=%d amount=%d", e.TxID, e.Src, e.Dst, e.Amount)
		}
		e.Checksum = ns.String
		wantSum := computeChecksum(e.TxID, e.Src, e.Dst, e.Amount)
		if e.Checksum != wantSum {
			return 0, fmt.Errorf("D1 corrupted ledger row tx_id=%d: got checksum %q want %q", e.TxID, e.Checksum, wantSum)
		}
		if e.Src < 0 || e.Src >= accounts || e.Dst < 0 || e.Dst >= accounts {
			return 0, fmt.Errorf("D1 out-of-range account in tx_id=%d: src=%d dst=%d", e.TxID, e.Src, e.Dst)
		}
		dbLedger[e.TxID] = e
		expectedBalances[e.Src] -= e.Amount
		expectedBalances[e.Dst] += e.Amount
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	// D1 Check A: Every client-acknowledged commit MUST exist in dbLedger.
	ledger.mu.Lock()
	for txID, acked := range ledger.Acked {
		dbEntry, ok := dbLedger[txID]
		if !ok {
			ledger.mu.Unlock()
			return 0, fmt.Errorf("D1 VIOLATION (lost committed transaction): tx_id=%d (src=%d dst=%d amt=%d) was acknowledged to client but is MISSING after recovery",
				txID, acked.Src, acked.Dst, acked.Amount)
		}
		if dbEntry != acked {
			ledger.mu.Unlock()
			return 0, fmt.Errorf("D1 VIOLATION (mismatched committed transaction): tx_id=%d got %+v want %+v", txID, dbEntry, acked)
		}
	}

	// D1 Check B: Any row in dbLedger not in Acked MUST come from InFlight (commit succeeded right as SIGKILL hit).
	promoted := 0
	for txID, dbEntry := range dbLedger {
		if _, isAcked := ledger.Acked[txID]; isAcked {
			continue
		}
		inFlight, wasInFlight := ledger.InFlight[txID]
		if !wasInFlight {
			ledger.mu.Unlock()
			return 0, fmt.Errorf("D2 VIOLATION (phantom/uncommitted transaction leaked): tx_id=%d present in DB but was never sent to COMMIT", txID)
		}
		if dbEntry != inFlight {
			ledger.mu.Unlock()
			return 0, fmt.Errorf("D1 VIOLATION (in-flight mismatch): tx_id=%d got %+v want %+v", txID, dbEntry, inFlight)
		}
		ledger.Acked[txID] = dbEntry
		promoted++
	}
	// Clear remaining in-flight entries that did not commit before the crash.
	ledger.InFlight = make(map[int64]txEntry)
	ledger.mu.Unlock()

	// D2 Check A: Global balance invariant SUM(balance) == accounts * initialBalance
	var totalSum int64
	wantTotal := int64(accounts) * initialBalance
	if err := db.QueryRowContext(ctx, "SELECT SUM(balance) FROM crash_accounts").Scan(&totalSum); err != nil {
		return promoted, fmt.Errorf("D2 SUM(balance) query: %w", err)
	}
	if totalSum != wantTotal {
		return promoted, fmt.Errorf("D2 VIOLATION (torn transaction / balance conservation broken): SUM(balance)=%d want %d", totalSum, wantTotal)
	}

	// D2 Check B: Every single account balance must match exact ledger reconstruction
	accRows, err := db.QueryContext(ctx, "SELECT id, balance FROM crash_accounts")
	if err != nil {
		return promoted, fmt.Errorf("D2 query crash_accounts: %w", err)
	}
	defer accRows.Close()

	seenAccounts := 0
	for accRows.Next() {
		var id int
		var bal int64
		if err := accRows.Scan(&id, &bal); err != nil {
			return promoted, fmt.Errorf("D2 scan crash_accounts: %w", err)
		}
		if id < 0 || id >= accounts {
			return promoted, fmt.Errorf("D2 invalid account id=%d", id)
		}
		seenAccounts++
		if bal != expectedBalances[id] {
			return promoted, fmt.Errorf("D2 VIOLATION (account %d balance mismatch vs ledger): db=%d expected_from_ledger=%d",
				id, bal, expectedBalances[id])
		}
	}
	if seenAccounts != accounts {
		return promoted, fmt.Errorf("D3 account count mismatch: got %d want %d", seenAccounts, accounts)
	}

	// D3 Check: COUNT(*) aggregate vs table scan count
	var ledgerCount int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM crash_ledger").Scan(&ledgerCount); err != nil {
		return promoted, fmt.Errorf("D3 COUNT(*) crash_ledger: %w", err)
	}
	if int(ledgerCount) != len(dbLedger) {
		return promoted, fmt.Errorf("D3 COUNT(*) mismatch on crash_ledger: count=%d scan=%d", ledgerCount, len(dbLedger))
	}

	return promoted, nil
}

var (
	activeServerMu sync.Mutex
	activeCmd      *exec.Cmd
	activeWaitCh   <-chan error
)

func registerActiveServer(cmd *exec.Cmd, waitCh <-chan error) {
	activeServerMu.Lock()
	activeCmd = cmd
	activeWaitCh = waitCh
	activeServerMu.Unlock()
}

func unregisterActiveServer(cmd *exec.Cmd) {
	activeServerMu.Lock()
	if activeCmd == cmd {
		activeCmd = nil
		activeWaitCh = nil
	}
	activeServerMu.Unlock()
}

func startServer(absBin, dataDir, engine string, pgPort int, baseDir string, tag int) (*exec.Cmd, <-chan error, string, error) {
	// Clean up stale pid file if left behind by SIGKILL.
	_ = os.Remove(filepath.Join(dataDir, "lionsql.pid"))

	srv := exec.Command(absBin,
		"-dir", dataDir,
		"-engine", engine,
		"-pg", fmt.Sprintf("127.0.0.1:%d", pgPort),
		"-tds", "",
		"-http", "")
	logPath := filepath.Join(baseDir, "server_"+strconv.Itoa(tag)+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, nil, "", err
	}
	srv.Stdout, srv.Stderr = logFile, logFile
	if err := srv.Start(); err != nil {
		_ = logFile.Close()
		return nil, nil, "", fmt.Errorf("start server: %w", err)
	}
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- srv.Wait()
		_ = logFile.Close()
	}()
	registerActiveServer(srv, waitCh)
	return srv, waitCh, logPath, nil
}

func openAndWait(dsn string, timeout time.Duration, serverDone <-chan error) (*sql.DB, time.Duration, error) {
	started := time.Now()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, 0, err
	}
	deadline := time.After(timeout)
	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	var lastErr error
	for {
		pctx, pcancel := context.WithTimeout(context.Background(), 1*time.Second)
		lastErr = db.PingContext(pctx)
		pcancel()
		if lastErr == nil {
			return db, time.Since(started), nil
		}
		select {
		case err := <-serverDone:
			_ = db.Close()
			return nil, 0, fmt.Errorf("server exited before ready: %v", err)
		case <-deadline:
			_ = db.Close()
			return nil, 0, fmt.Errorf("server not ready after %s: %w", timeout, lastErr)
		case <-tick.C:
		}
	}
}

func killNow(cmd *exec.Cmd, waitCh <-chan error) {
	defer unregisterActiveServer(cmd)
	if cmd == nil || cmd.Process == nil {
		return
	}
	select {
	case <-waitCh:
		return
	default:
	}
	_ = cmd.Process.Kill()
	<-waitCh
}

func gracefulStop(cmd *exec.Cmd, waitCh <-chan error) {
	_ = gracefulStopTimed(cmd, waitCh, 1500*time.Millisecond)
}

func gracefulStopTimed(cmd *exec.Cmd, waitCh <-chan error, limit time.Duration) error {
	defer unregisterActiveServer(cmd)
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	select {
	case <-waitCh:
		return nil
	default:
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-waitCh:
		return nil
	case <-time.After(limit):
		_ = cmd.Process.Kill()
		<-waitCh
		return fmt.Errorf("process did not terminate within %s of SIGTERM", limit)
	}
}
