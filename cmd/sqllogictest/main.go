package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/slt"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/storage/wiredtiger"
	"lionsql/pkg/wire"
)

type fileResult struct {
	file       string
	failed     bool
	err        error
	dur        time.Duration
	stmtsCount int
	queryCount int
}

func runSingleFile(file string, engineType string, mode string, verbose bool, opts slt.ParseOptions) fileResult {
	records, err := slt.ParseFileWithOptions(file, opts)
	if err != nil {
		return fileResult{
			file:   file,
			failed: true,
			err:    fmt.Errorf("parse error: %v", err),
		}
	}

	var eng storage.Engine
	var cleanupDir string
	switch strings.ToLower(engineType) {
	case "wiredtiger", "wt":
		tmpDir, err := os.MkdirTemp("", "lionsql-slt-wt-*")
		if err != nil {
			return fileResult{
				file:   file,
				failed: true,
				err:    fmt.Errorf("failed to create temp dir for wiredtiger: %v", err),
			}
		}
		cleanupDir = tmpDir
		eng = wiredtiger.New()
		if err := eng.Open(tmpDir); err != nil {
			_ = os.RemoveAll(tmpDir)
			return fileResult{
				file:   file,
				failed: true,
				err:    fmt.Errorf("failed to open wiredtiger engine: %v", err),
			}
		}
	case "memory", "mem":
		eng = memengine.New()
		_ = eng.Open("")
	default:
		return fileResult{
			file:   file,
			failed: true,
			err:    fmt.Errorf("unknown storage engine %q (expected memory or wiredtiger)", engineType),
		}
	}

	defer func() {
		_ = eng.Close()
		if cleanupDir != "" {
			_ = os.RemoveAll(cleanupDir)
		}
	}()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())

	var exec slt.Executor
	var wireSrv *wire.Server
	var pgExec *slt.PGWireExecutor

	switch strings.ToLower(mode) {
	case "inproc", "direct":
		exec = slt.NewCoordinatorExecutor(coord)
	case "pgwire", "wire", "tcp":
		wireSrv = wire.NewServer("127.0.0.1:0", cat, eng)
		wireSrv.SetCoordinator(coord)
		if err := wireSrv.Start(); err != nil {
			return fileResult{
				file:   file,
				failed: true,
				err:    fmt.Errorf("failed to start pgwire server: %v", err),
			}
		}
		defer func() {
			_ = wireSrv.Close()
		}()

		pgExec, err = slt.NewPGWireExecutor(wireSrv.Addr().String())
		if err != nil {
			return fileResult{
				file:   file,
				failed: true,
				err:    fmt.Errorf("failed to connect pgwire executor: %v", err),
			}
		}
		defer func() {
			_ = pgExec.Close()
		}()
		exec = pgExec
	default:
		return fileResult{
			file:   file,
			failed: true,
			err:    fmt.Errorf("unknown execution mode %q (expected inproc or pgwire)", mode),
		}
	}

	fileStart := time.Now()
	var fileErr error
	fileFailed := false
	stmtsCount := 0
	queryCount := 0

	for _, rec := range records {
		if rec.Type == slt.CmdStatementOk || rec.Type == slt.CmdStatementError {
			stmtsCount++
		} else if rec.Type == slt.CmdQuery {
			queryCount++
		}

		if verbose && rec.SQL != "" {
			fmt.Printf("[%s:%d] %s\n", rec.FilePath, rec.LineNumber, strings.ReplaceAll(rec.SQL, "\n", " "))
		}

		if err := slt.RunRecord(exec, rec); err != nil {
			fileFailed = true
			fileErr = err
			break
		}
	}

	return fileResult{
		file:       file,
		failed:     fileFailed,
		err:        fileErr,
		dur:        time.Since(fileStart),
		stmtsCount: stmtsCount,
		queryCount: queryCount,
	}
}

func main() {
	dirFlag := flag.String("dir", "test/slt", "Directory containing .slt test files")
	fileFlag := flag.String("file", "", "Run specific SLT test file")
	verboseFlag := flag.Bool("v", false, "Verbose execution logging")
	engineFlag := flag.String("engine", "memory", "Storage engine backend (memory, wiredtiger)")
	modeFlag := flag.String("mode", "inproc", "Execution mode (inproc, pgwire)")
	parallelFlag := flag.Int("parallel", 1, "Number of concurrent SLT file workers")
	targetFlag := flag.String("target", "sqlite,lionsql", "Comma-separated target database dialect tags for onlyif/skipif directives")
	flag.Parse()

	var targetDialects []string
	for _, t := range strings.Split(*targetFlag, ",") {
		if trimmed := strings.TrimSpace(t); trimmed != "" {
			targetDialects = append(targetDialects, trimmed)
		}
	}
	parseOpts := slt.ParseOptions{TargetDialects: targetDialects}

	var testFiles []string
	if *fileFlag != "" {
		testFiles = []string{*fileFlag}
	} else {
		err := filepath.Walk(*dirFlag, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && (strings.HasSuffix(path, ".slt") || strings.HasSuffix(path, ".test")) {
				testFiles = append(testFiles, path)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error discovering test files in %s: %v\n", *dirFlag, err)
			os.Exit(1)
		}
	}

	sort.Strings(testFiles)
	if len(testFiles) == 0 {
		fmt.Printf("No .slt or .test files found in %s\n", *dirFlag)
		os.Exit(0)
	}

	workers := *parallelFlag
	if workers < 1 {
		workers = 1
	}
	if workers > len(testFiles) {
		workers = len(testFiles)
	}

	fmt.Println("================================================================================")
	fmt.Println("  LionSQL SQLLogicTest (SLT) Compliance & Blackbox Oracle Suite")
	fmt.Println("================================================================================")
	fmt.Printf("  [Storage Engine] %s\n", *engineFlag)
	fmt.Printf("  [Execution Mode] %s\n", *modeFlag)
	fmt.Printf("  [Parallelism]    %d worker(s)\n", workers)
	fmt.Printf("  [Target Tags]    %s\n", strings.Join(targetDialects, ", "))
	fmt.Printf("  [Discovered]     %d test files\n", len(testFiles))
	fmt.Println("--------------------------------------------------------------------------------")

	suiteStart := time.Now()
	results := make([]fileResult, len(testFiles))

	if workers == 1 {
		for i, file := range testFiles {
			res := runSingleFile(file, *engineFlag, *modeFlag, *verboseFlag, parseOpts)
			results[i] = res
			if res.failed {
				fmt.Printf("FAIL  %s (%v) - %d stmts, %d queries\n", res.file, res.dur.Round(time.Millisecond), res.stmtsCount, res.queryCount)
				fmt.Fprintf(os.Stderr, "      Error: %v\n", res.err)
			} else {
				fmt.Printf("PASS  %s (%v) - %d stmts, %d queries\n", res.file, res.dur.Round(time.Millisecond), res.stmtsCount, res.queryCount)
			}
		}
	} else {
		type job struct {
			idx  int
			file string
		}
		jobs := make(chan job, len(testFiles))
		for i, f := range testFiles {
			jobs <- job{idx: i, file: f}
		}
		close(jobs)

		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobs {
					results[j.idx] = runSingleFile(j.file, *engineFlag, *modeFlag, false, parseOpts)
				}
			}()
		}
		wg.Wait()

		for _, res := range results {
			if res.failed {
				fmt.Printf("FAIL  %s (%v) - %d stmts, %d queries\n", res.file, res.dur.Round(time.Millisecond), res.stmtsCount, res.queryCount)
				fmt.Fprintf(os.Stderr, "      Error: %v\n", res.err)
			} else {
				fmt.Printf("PASS  %s (%v) - %d stmts, %d queries\n", res.file, res.dur.Round(time.Millisecond), res.stmtsCount, res.queryCount)
			}
		}
	}

	totalFiles := len(testFiles)
	passedFiles := 0
	failedFiles := 0
	totalStatements := 0
	totalQueries := 0
	for _, res := range results {
		totalStatements += res.stmtsCount
		totalQueries += res.queryCount
		if res.failed {
			failedFiles++
		} else {
			passedFiles++
		}
	}

	totalDur := time.Since(suiteStart)
	fmt.Println("================================================================================")
	fmt.Println("                             SLT SUITE RESULTS                                  ")
	fmt.Println("================================================================================")
	fmt.Printf(" Files:       %d total, %d passed, %d failed\n", totalFiles, passedFiles, failedFiles)
	fmt.Printf(" Statements:  %d executed\n", totalStatements)
	fmt.Printf(" Queries:     %d executed\n", totalQueries)
	fmt.Printf(" Total Time:  %v\n", totalDur.Round(time.Millisecond))
	fmt.Println("--------------------------------------------------------------------------------")

	if failedFiles > 0 {
		fmt.Printf(" Verdict:     FAILED (%d files failed)\n", failedFiles)
		fmt.Println("================================================================================")
		os.Exit(1)
	}

	fmt.Println(" Verdict:     100% PASS (Zero conformance mismatches across all SLT test cases)")
	fmt.Println("================================================================================")
}
