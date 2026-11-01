package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/slt"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func main() {
	dirFlag := flag.String("dir", "test/slt", "Directory containing .slt test files")
	fileFlag := flag.String("file", "", "Run specific SLT test file")
	verboseFlag := flag.Bool("v", false, "Verbose execution logging")
	engineFlag := flag.String("engine", "memory", "Storage engine backend (memory)")
	flag.Parse()

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

	fmt.Println("================================================================================")
	fmt.Println("  LionSQL SQLLogicTest (SLT) Compliance & Blackbox Oracle Suite")
	fmt.Println("================================================================================")
	fmt.Printf("  [Storage Engine] %s\n", *engineFlag)
	fmt.Printf("  [Discovered]     %d test files\n", len(testFiles))
	fmt.Println("--------------------------------------------------------------------------------")

	totalFiles := len(testFiles)
	passedFiles := 0
	failedFiles := 0
	totalStatements := 0
	totalQueries := 0
	suiteStart := time.Now()

	for _, file := range testFiles {
		records, err := slt.ParseFile(file)
		if err != nil {
			fmt.Printf("FAIL  %s (parse error: %v)\n", file, err)
			failedFiles++
			continue
		}

		// Initialize a fresh isolated coordinator per test file
		var eng storage.Engine = memengine.New()
		cat := catalog.New(eng)
		coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
		exec := slt.NewCoordinatorExecutor(coord)

		fileStart := time.Now()
		fileFailed := false
		var fileErr error
		stmtsCount := 0
		queryCount := 0

		for _, rec := range records {
			if rec.Type == slt.CmdStatementOk || rec.Type == slt.CmdStatementError {
				stmtsCount++
			} else if rec.Type == slt.CmdQuery {
				queryCount++
			}

			if *verboseFlag && rec.SQL != "" {
				fmt.Printf("[%s:%d] %s\n", rec.FilePath, rec.LineNumber, strings.ReplaceAll(rec.SQL, "\n", " "))
			}

			if err := slt.RunRecord(exec, rec); err != nil {
				fileFailed = true
				fileErr = err
				break
			}
		}

		totalStatements += stmtsCount
		totalQueries += queryCount
		fileDur := time.Since(fileStart)

		if fileFailed {
			failedFiles++
			fmt.Printf("FAIL  %s (%v) - %d stmts, %d queries\n", file, fileDur.Round(time.Millisecond), stmtsCount, queryCount)
			fmt.Fprintf(os.Stderr, "      Error: %v\n", fileErr)
		} else {
			passedFiles++
			fmt.Printf("PASS  %s (%v) - %d stmts, %d queries\n", file, fileDur.Round(time.Millisecond), stmtsCount, queryCount)
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
