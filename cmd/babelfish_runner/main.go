package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

var reGo = regexp.MustCompile(`(?mi)^\s*go\s*$`)

// reExpectedError matches T-SQL comments that indicate this batch is expected to fail.
var reExpectedError = regexp.MustCompile(`(?mi)--\s*(?:should\s+(?:error|fail|throw)|expected\s+(?:error|exception|to\s+fail)|expect\s+error|must\s+fail|throws?\s+error|syntax\s+error|invalid\b|error\s*$)`)

func isExpectedErrorBatch(rawBatch string) bool {
	return reExpectedError.MatchString(rawBatch)
}

func cleanBatch(b string) string {
	lines := strings.Split(b, "\n")
	var cleaned []string
	for _, l := range lines {
		tr := strings.TrimSpace(l)
		if strings.HasPrefix(tr, "--") || (strings.HasPrefix(tr, "/*") && strings.HasSuffix(tr, "*/")) {
			continue
		}
		if tr != "" {
			cleaned = append(cleaned, l)
		}
	}
	return strings.Join(cleaned, "\n")
}

func main() {
	dirFlag := flag.String("dir", "test/babelfish/raw", "Directory of Babelfish SQL test files")
	parallelFlag := flag.Int("parallel", 16, "Concurrency level")
	limitFlag := flag.Int("limit", 0, "Limit number of files to run (0 = all)")
	timeoutFlag := flag.Duration("timeout", 3*time.Second, "Per-batch timeout")
	flag.Parse()

	var files []string
	err := filepath.Walk(*dirFlag, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".sql") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to walk %s: %v\n", *dirFlag, err)
		os.Exit(1)
	}

	if *limitFlag > 0 && len(files) > *limitFlag {
		files = files[:*limitFlag]
	}

	totalFiles := len(files)
	fmt.Printf("================================================================================\n")
	fmt.Printf("  LionSQL Babelfish T-SQL Full 2,900-Suite Runner (Timeout Protected: %s)\n", *timeoutFlag)
	fmt.Printf("  Target Directory: %s\n", *dirFlag)
	fmt.Printf("  Total SQL Files:  %d\n", totalFiles)
	fmt.Printf("  Parallel Workers: %d\n", *parallelFlag)
	fmt.Printf("================================================================================\n")

	start := time.Now()
	var (
		passFiles        atomic.Int64
		failFiles        atomic.Int64
		timeoutFiles     atomic.Int64
		totalBatches     atomic.Int64
		passBatches      atomic.Int64
		failBatches      atomic.Int64
		expectedErrMatch atomic.Int64
	)

	workChan := make(chan string, 100)
	var wg sync.WaitGroup

	for i := 0; i < *parallelFlag; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			eng := memengine.New()
			cat := catalog.New(eng)
			coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())

			for file := range workChan {
				content, err := os.ReadFile(file)
				if err != nil {
					failFiles.Add(1)
					continue
				}

				rawBatches := reGo.Split(string(content), -1)
				fileFailed := false

				for _, rb := range rawBatches {
					cb := cleanBatch(rb)
					if cb == "" {
						continue
					}
					totalBatches.Add(1)

					// Execute in a goroutine with strict channel timeout guard to guarantee completion
					type execResult struct {
						res *engine.QueryResult
						err error
					}
					ch := make(chan execResult, 1)

					ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
					go func(queryText string) {
						r, e := coord.ExecuteSessionSQLContext(ctx, "default", queryText)
						ch <- execResult{res: r, err: e}
					}(cb)

					var res *engine.QueryResult
					var execErr error
					select {
					case r := <-ch:
						res = r.res
						execErr = r.err
					case <-time.After(*timeoutFlag + 100*time.Millisecond):
						execErr = context.DeadlineExceeded
					}
					cancel()

					isErrExpected := isExpectedErrorBatch(rb)
					batchFailed := execErr != nil || (res != nil && !res.Success)

					if isErrExpected {
						if batchFailed {
							passBatches.Add(1)
							expectedErrMatch.Add(1)
						} else {
							fileFailed = true
							failBatches.Add(1)
						}
					} else {
						if batchFailed {
							fileFailed = true
							failBatches.Add(1)
							if execErr == context.DeadlineExceeded {
								timeoutFiles.Add(1)
							}
						} else {
							passBatches.Add(1)
						}
					}
				}

				if fileFailed {
					failFiles.Add(1)
				} else {
					passFiles.Add(1)
				}
			}
		}(i)
	}

	// Feed files
	go func() {
		for _, f := range files {
			workChan <- f
		}
		close(workChan)
	}()

	// Progress reporter
	doneChan := make(chan struct{})
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pf := passFiles.Load()
				ff := failFiles.Load()
				pb := passBatches.Load()
				fb := failBatches.Load()
				processed := pf + ff
				pct := float64(processed) / float64(totalFiles) * 100

				fmt.Printf("  [Progress] Files: %d/%d (%.1f%%) | Batches: %d PASS, %d FAIL\n",
					processed, totalFiles, pct, pb, fb)
			case <-doneChan:
				return
			}
		}
	}()

	wg.Wait()
	close(doneChan)
	elapsed := time.Since(start)

	pf := passFiles.Load()
	ff := failFiles.Load()
	tb := totalBatches.Load()
	pb := passBatches.Load()
	fb := failBatches.Load()
	to := timeoutFiles.Load()
	em := expectedErrMatch.Load()

	fmt.Printf("--------------------------------------------------------------------------------\n")
	fmt.Printf("  Babelfish 2,900 Suite Execution Complete in %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("  Files:    Total: %d | PASS: %d | FAIL: %d | Timeouts: %d\n", totalFiles, pf, ff, to)
	fmt.Printf("  Batches:  Total: %d | PASS: %d (Expected Errors Rejection: %d) | FAIL: %d (%.2f%% Batch Pass Rate)\n",
		tb, pb, em, fb, float64(pb)/float64(tb)*100)
	fmt.Printf("================================================================================\n")
}
