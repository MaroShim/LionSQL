// Command soaktest runs a simple long-duration soak test against a LionSQL
// server over the PostgreSQL wire protocol (DURA-SOAK-1, phase 1).
//
// It launches the server as a child process, applies a steady mixed workload
// (money transfers that preserve a global invariant, point reads, periodic
// full-table sums, and connection churn), samples server resource usage at a
// fixed interval, and finally judges the run:
//
//   - the server RSS and open file descriptor counts must not keep growing
//     after warm-up,
//   - the p99 latency must not degrade by more than a configured ratio,
//   - no invariant violation may be observed during or after the run,
//   - the number of rows must equal the number of acknowledged commits.
//
// Exit codes: 0 = pass, 1 = soak verdict failed, 2 = setup or runtime error.
package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lib/pq"
)

const initialBalance = 1000

type sample struct {
	elapsed   time.Duration
	rssKB     int64
	fds       int
	dirBytes  int64
	ops       int64
	errs      int64
	p50, p99  time.Duration
	opsPerSec float64
}

type window struct {
	mu   sync.Mutex
	lats []time.Duration
}

func (w *window) add(d time.Duration) {
	w.mu.Lock()
	w.lats = append(w.lats, d)
	w.mu.Unlock()
}

func (w *window) drain() []time.Duration {
	w.mu.Lock()
	out := w.lats
	w.lats = nil
	w.mu.Unlock()
	return out
}

// errLog keeps the first few distinct error messages so failures are visible.
type errLog struct {
	mu   sync.Mutex
	seen map[string]int
}

func (e *errLog) record(err error) {
	msg := err.Error()
	if len(msg) > 160 {
		msg = msg[:160]
	}
	e.mu.Lock()
	if e.seen == nil {
		e.seen = map[string]int{}
	}
	e.seen[msg]++
	e.mu.Unlock()
}

func (e *errLog) report() {
	e.mu.Lock()
	defer e.mu.Unlock()
	type kv struct {
		k string
		v int
	}
	var all []kv
	for k, v := range e.seen {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v })
	for i, x := range all {
		if i >= 5 {
			break
		}
		fmt.Printf("  error x%d: %s\n", x.v, x.k)
	}
}

func main() {
	binPath := flag.String("bin", "bin/lionsql", "Path to the lionsql server binary")
	engine := flag.String("engine", "wiredtiger", "Storage engine (wiredtiger, memory)")
	duration := flag.Duration("duration", time.Minute, "Total soak duration")
	sampleEvery := flag.Duration("sample", 5*time.Second, "Resource sampling interval")
	workers := flag.Int("workers", 8, "Concurrent workload workers")
	rate := flag.Int("rate", 200, "Target total operations per second")
	accounts := flag.Int("accounts", 1000, "Number of accounts")
	pgPort := flag.Int("pg-port", 5543, "PGWire port for the child server")
	password := flag.String("password", "1234567890", "Database password")
	maxGrowth := flag.Float64("max-resource-growth", 0.30, "Allowed relative RSS/FD growth after warm-up")
	maxP99Ratio := flag.Float64("max-p99-ratio", 2.0, "Allowed p99 latency ratio (late/early)")
	outCSV := flag.String("out", "", "Optional CSV output path for samples")
	keep := flag.Bool("keep", false, "Keep the temporary data directory")
	flag.Parse()

	code, err := run(*binPath, *engine, *duration, *sampleEvery, *workers, *rate, *accounts,
		*pgPort, *password, *maxGrowth, *maxP99Ratio, *outCSV, *keep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "soaktest: %v\n", err)
	}
	os.Exit(code)
}

func run(binPath, engine string, duration, sampleEvery time.Duration, workers, rate, accounts, pgPort int,
	password string, maxGrowth, maxP99Ratio float64, outCSV string, keep bool) (int, error) {

	if workers < 1 || rate < 1 || accounts < 2 {
		return 2, errors.New("workers, rate must be >= 1 and accounts >= 2")
	}
	absBin, err := filepath.Abs(binPath)
	if err != nil {
		return 2, err
	}
	dataDir, err := os.MkdirTemp("", "lionsql_soak_*")
	if err != nil {
		return 2, err
	}
	if !keep {
		defer func() {
			if rmErr := os.RemoveAll(dataDir); rmErr != nil {
				fmt.Fprintf(os.Stderr, "soaktest: cleanup %s: %v\n", dataDir, rmErr)
			}
		}()
	}

	// Argument vector only; no shell involved.
	srv := exec.Command(absBin,
		"-dir", dataDir, "-engine", engine,
		"-pg", fmt.Sprintf("127.0.0.1:%d", pgPort), "-tds", "", "-http", "")
	logFile, err := os.Create(filepath.Join(dataDir, "..", filepath.Base(dataDir)+".server.log"))
	if err != nil {
		return 2, err
	}
	defer logFile.Close()
	srv.Stdout, srv.Stderr = logFile, logFile
	if err := srv.Start(); err != nil {
		return 2, fmt.Errorf("start server: %w", err)
	}
	pid := srv.Process.Pid
	waitCh := make(chan error, 1)
	go func() { waitCh <- srv.Wait() }()
	defer stopServer(srv, waitCh)

	dsn := fmt.Sprintf("host=127.0.0.1 port=%d user=postgres password=%s dbname=postgres sslmode=disable", pgPort, password)
	db, err := openAndWait(dsn, 30*time.Second, waitCh)
	if err != nil {
		return 2, err
	}
	defer db.Close()
	db.SetMaxOpenConns(workers + 4)
	db.SetMaxIdleConns(workers + 4)

	if err := setup(db, accounts); err != nil {
		return 2, fmt.Errorf("setup: %w", err)
	}
	fmt.Printf("soaktest: engine=%s duration=%s workers=%d rate=%d/s accounts=%d pid=%d dir=%s\n",
		engine, duration, workers, rate, accounts, pid, dataDir)

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var ops, errs, conflicts, committed, violations atomic.Int64
	var win window
	var elog errLog
	var wg sync.WaitGroup
	expectedTotal := int64(accounts) * initialBalance

	interval := time.Duration(float64(time.Second) * float64(workers) / float64(rate))
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))
			tick := time.NewTicker(interval)
			defer tick.Stop()
			var seq int64
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
				start := time.Now()
				var opErr error
				switch n := rng.Intn(100); {
				case n < 55:
					seq++
					opErr = transfer(ctx, db, rng, accounts, int64(id)*1_000_000_000+seq)
					if opErr == nil {
						committed.Add(1)
					}
				case n < 98:
					var bal int64
					opErr = db.QueryRowContext(ctx, "SELECT balance FROM soak_accounts WHERE id = $1", rng.Intn(accounts)).Scan(&bal)
				default:
					var sum int64
					opErr = db.QueryRowContext(ctx, "SELECT SUM(balance) FROM soak_accounts").Scan(&sum)
					if opErr == nil && sum != expectedTotal {
						violations.Add(1)
						elog.record(fmt.Errorf("invariant violation: SUM(balance)=%d expected %d", sum, expectedTotal))
					}
				}
				if ctx.Err() != nil {
					return
				}
				win.add(time.Since(start))
				ops.Add(1)
				if opErr != nil {
					if isSerializationFailure(opErr) {
						// Retryable conflict (SQLSTATE 40001): expected under contention.
						conflicts.Add(1)
					} else {
						errs.Add(1)
						elog.record(opErr)
					}
				}
			}
		}(w)
	}

	// Connection churn: open, query, close a fresh connection periodically.
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			c, openErr := sql.Open("postgres", dsn)
			if openErr == nil {
				openErr = c.PingContext(ctx)
				if closeErr := c.Close(); openErr == nil {
					openErr = closeErr
				}
			}
			if openErr != nil && ctx.Err() == nil {
				errs.Add(1)
				elog.record(fmt.Errorf("churn: %w", openErr))
			}
		}
	}()

	// Sampler.
	var samples []sample
	started := time.Now()
	var lastOps int64
	var lastT = started
	st := time.NewTicker(sampleEvery)
	defer st.Stop()
sampling:
	for {
		select {
		case <-ctx.Done():
			break sampling
		case err := <-waitCh:
			cancel()
			wg.Wait()
			return 1, fmt.Errorf("server exited during soak: %v (see %s)", err, logFile.Name())
		case <-st.C:
			now := time.Now()
			lats := win.drain()
			cur := ops.Load()
			s := sample{elapsed: now.Sub(started), ops: cur, errs: errs.Load()}
			s.opsPerSec = float64(cur-lastOps) / now.Sub(lastT).Seconds()
			lastOps, lastT = cur, now
			s.p50, s.p99 = percentiles(lats)
			s.rssKB, err = rssKB(pid)
			if err != nil {
				fmt.Fprintf(os.Stderr, "soaktest: rss sample: %v\n", err)
			}
			s.fds, err = fdCount(pid)
			if err != nil {
				fmt.Fprintf(os.Stderr, "soaktest: fd sample: %v\n", err)
			}
			s.dirBytes = dirSize(dataDir)
			samples = append(samples, s)
			fmt.Printf("t=%-8s rss=%6dMB fds=%-4d disk=%6dMB ops/s=%-7.1f p50=%-9s p99=%-9s errs=%d\n",
				s.elapsed.Round(time.Second), s.rssKB/1024, s.fds, s.dirBytes>>20, s.opsPerSec, s.p50.Round(time.Microsecond), s.p99.Round(time.Microsecond), s.errs)
		}
	}
	wg.Wait()

	if outCSV != "" {
		if err := writeCSV(outCSV, samples); err != nil {
			fmt.Fprintf(os.Stderr, "soaktest: write csv: %v\n", err)
		}
	}

	// Final integrity check.
	failed := false
	fmt.Println("---- final verdict ----")
	var sum, rows int64
	vctx, vcancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer vcancel()
	if err := db.QueryRowContext(vctx, "SELECT SUM(balance) FROM soak_accounts").Scan(&sum); err != nil {
		fmt.Printf("FAIL final SUM query: %v\n", err)
		failed = true
	} else if sum != expectedTotal {
		fmt.Printf("FAIL final invariant: SUM(balance)=%d expected %d\n", sum, expectedTotal)
		failed = true
	} else {
		fmt.Printf("PASS final invariant: SUM(balance)=%d\n", sum)
	}
	if err := db.QueryRowContext(vctx, "SELECT COUNT(*) FROM soak_log").Scan(&rows); err != nil {
		fmt.Printf("FAIL final COUNT query: %v\n", err)
		failed = true
	} else if rows != committed.Load() {
		fmt.Printf("FAIL acknowledged commits=%d but soak_log rows=%d\n", committed.Load(), rows)
		failed = true
	} else {
		fmt.Printf("PASS acknowledged commits == rows (%d)\n", rows)
	}
	if v := violations.Load(); v > 0 {
		fmt.Printf("FAIL %d invariant violations observed during run\n", v)
		failed = true
	}
	fmt.Printf("INFO %d retryable serialization conflicts (SQLSTATE 40001)\n", conflicts.Load())
	if e := errs.Load(); e > 0 {
		fmt.Printf("INFO %d operation errors out of %d ops\n", e, ops.Load())
		elog.report()
	}
	if trendFailed(samples, maxGrowth, maxP99Ratio) {
		failed = true
	}
	if failed {
		return 1, nil
	}
	fmt.Println("RESULT: PASS")
	return 0, nil
}

func setup(db *sql.DB, accounts int) error {
	stmts := []string{
		"CREATE TABLE soak_accounts (id INT PRIMARY KEY, balance BIGINT NOT NULL)",
		"CREATE TABLE soak_log (id BIGINT PRIMARY KEY, src INT NOT NULL, dst INT NOT NULL, payload VARCHAR(64))",
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	for i := 0; i < accounts; i++ {
		if _, err := db.Exec("INSERT INTO soak_accounts (id, balance) VALUES ($1, $2)", i, initialBalance); err != nil {
			return fmt.Errorf("seed account %d: %w", i, err)
		}
	}
	return nil
}

func transfer(ctx context.Context, db *sql.DB, rng *rand.Rand, accounts int, logID int64) error {
	src := rng.Intn(accounts)
	dst := (src + 1 + rng.Intn(accounts-1)) % accounts
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE soak_accounts SET balance = balance - 1 WHERE id = $1", src); err != nil {
		return rollback(tx, err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE soak_accounts SET balance = balance + 1 WHERE id = $1", dst); err != nil {
		return rollback(tx, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO soak_log (id, src, dst, payload) VALUES ($1, $2, $3, $4)",
		logID, src, dst, "soak-"+strconv.FormatInt(logID, 10)); err != nil {
		return rollback(tx, err)
	}
	return tx.Commit()
}

func isSerializationFailure(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "40001"
}

func rollback(tx *sql.Tx, cause error) error {
	if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
		return fmt.Errorf("%w (rollback also failed: %v)", cause, rbErr)
	}
	return cause
}

func openAndWait(dsn string, timeout time.Duration, serverDone <-chan error) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	deadline := time.After(timeout)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	var lastErr error
	for {
		pctx, pcancel := context.WithTimeout(context.Background(), 2*time.Second)
		lastErr = db.PingContext(pctx)
		pcancel()
		if lastErr == nil {
			return db, nil
		}
		select {
		case err := <-serverDone:
			db.Close()
			return nil, fmt.Errorf("server exited before ready: %v", err)
		case <-deadline:
			db.Close()
			return nil, fmt.Errorf("server not ready after %s: %w", timeout, lastErr)
		case <-tick.C:
		}
	}
}

// stopServer escalates from SIGTERM to SIGKILL within 1.5 seconds of waiting.
func stopServer(cmd *exec.Cmd, waitCh <-chan error) {
	if cmd.Process == nil {
		return
	}
	select {
	case <-waitCh:
		return
	default:
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		fmt.Fprintf(os.Stderr, "soaktest: SIGTERM: %v\n", err)
	}
	select {
	case <-waitCh:
	case <-time.After(1500 * time.Millisecond):
		if err := cmd.Process.Kill(); err != nil {
			fmt.Fprintf(os.Stderr, "soaktest: SIGKILL: %v\n", err)
		}
		<-waitCh
	}
}

func percentiles(l []time.Duration) (p50, p99 time.Duration) {
	if len(l) == 0 {
		return 0, 0
	}
	sort.Slice(l, func(i, j int) bool { return l[i] < l[j] })
	idx := func(p float64) int {
		i := int(float64(len(l)-1) * p)
		if i < 0 {
			i = 0
		}
		if i >= len(l) {
			i = len(l) - 1
		}
		return i
	}
	return l[idx(0.50)], l[idx(0.99)]
}

func rssKB(pid int) (int64, error) {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
}

func fdCount(pid int) (int, error) {
	if runtime.GOOS == "linux" {
		ents, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
		return len(ents), err
	}
	out, err := exec.Command("lsof", "-n", "-P", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	n := strings.Count(string(out), "\n") - 1 // minus header
	if n < 0 {
		n = 0
	}
	return n, nil
}

func dirSize(root string) int64 {
	var total int64
	walkErr := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // files may vanish while the server rotates logs
		}
		if d.Type().IsRegular() {
			if info, ierr := d.Info(); ierr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	if walkErr != nil {
		fmt.Fprintf(os.Stderr, "soaktest: dir size: %v\n", walkErr)
	}
	return total
}

func writeCSV(path string, samples []sample) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	_ = w.Write([]string{"elapsed_s", "rss_kb", "fds", "disk_bytes", "ops", "errors", "ops_per_s", "p50_us", "p99_us"})
	for _, s := range samples {
		_ = w.Write([]string{
			strconv.Itoa(int(s.elapsed.Seconds())), strconv.FormatInt(s.rssKB, 10), strconv.Itoa(s.fds),
			strconv.FormatInt(s.dirBytes, 10), strconv.FormatInt(s.ops, 10), strconv.FormatInt(s.errs, 10),
			strconv.FormatFloat(s.opsPerSec, 'f', 1, 64),
			strconv.FormatInt(s.p50.Microseconds(), 10), strconv.FormatInt(s.p99.Microseconds(), 10),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	return c[len(c)/2]
}

// trendFailed compares an early post-warm-up window (20%-30% of samples) with
// the final window (last 10%). Returns true when a trend limit is exceeded.
func trendFailed(samples []sample, maxGrowth, maxP99Ratio float64) bool {
	n := len(samples)
	if n < 10 {
		fmt.Printf("INFO only %d samples; trend checks skipped (need >= 10, increase -duration)\n", n)
		return false
	}
	early := samples[n*2/10 : max(n*3/10, n*2/10+1)]
	late := samples[n-max(n/10, 1):]
	pick := func(ss []sample, f func(sample) float64) float64 {
		v := make([]float64, 0, len(ss))
		for _, s := range ss {
			v = append(v, f(s))
		}
		return median(v)
	}
	failed := false
	check := func(name string, e, l, limit float64, ratio bool) {
		if e <= 0 {
			fmt.Printf("INFO %s: no baseline\n", name)
			return
		}
		r := l / e
		bad := r > 1+limit
		if ratio {
			bad = r > limit
		}
		status := "PASS"
		if bad {
			status = "FAIL"
			failed = true
		}
		fmt.Printf("%s %s: early=%.1f late=%.1f ratio=%.2f\n", status, name, e, l, r)
	}
	check("rss_kb", pick(early, func(s sample) float64 { return float64(s.rssKB) }), pick(late, func(s sample) float64 { return float64(s.rssKB) }), maxGrowth, false)
	check("open_fds", pick(early, func(s sample) float64 { return float64(s.fds) }), pick(late, func(s sample) float64 { return float64(s.fds) }), maxGrowth, false)
	check("p99_latency_us", pick(early, func(s sample) float64 { return float64(s.p99.Microseconds()) }), pick(late, func(s sample) float64 { return float64(s.p99.Microseconds()) }), maxP99Ratio, true)
	return failed
}
