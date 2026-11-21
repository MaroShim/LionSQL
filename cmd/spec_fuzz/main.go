package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lionsql/pkg/speckit/runner"
)

func main() {
	durationFlag := flag.Duration("duration", 5*time.Second, "Discovery run duration (e.g. 5s, 30s, 1m)")
	workersFlag := flag.Int("workers", 4, "Number of concurrent fuzzer workers")
	depthFlag := flag.Int("depth", 3, "Maximum predicate AST recursion depth")
	oracleFlag := flag.String("oracle", "norec", "Oracle type: norec, dualengine, or all")
	outputFlag := flag.String("output", "test/spec_defects", "Output directory for generated TDD Red test files")
	verboseFlag := flag.Bool("v", true, "Verbose logging of discovery events")
	flag.Parse()

	fmt.Println("================================================================================")
	fmt.Println("  LionSQL Spec-Driven Query Discovery & TDD Red Engine (cmd/spec_fuzz)")
	fmt.Println("================================================================================")
	fmt.Printf(" [Duration]  %v\n", *durationFlag)
	fmt.Printf(" [Workers]   %d concurrent workers\n", *workersFlag)
	fmt.Printf(" [Max Depth] %d\n", *depthFlag)
	fmt.Printf(" [Oracle]    %s\n", *oracleFlag)
	fmt.Printf(" [Output]    %s\n", *outputFlag)
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println("Starting autonomous specification exploration...")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := runner.Config{
		Duration:   *durationFlag,
		Workers:    *workersFlag,
		MaxDepth:   *depthFlag,
		OracleType: *oracleFlag,
		OutputDir:  *outputFlag,
		Verbose:    *verboseFlag,
	}

	res, err := runner.Run(ctx, cfg)
	if err != nil {
		fmt.Printf("Discovery run failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println("  Exploration Summary")
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Printf("  Elapsed Time:       %v\n", res.ElapsedDuration.Round(time.Millisecond))
	fmt.Printf("  Tables Fuzzed:      %d\n", res.TotalTables)
	fmt.Printf("  Queries Evaluated:  %d (%.1f QPS)\n", res.TotalQueries, float64(res.TotalQueries)/res.ElapsedDuration.Seconds())
	fmt.Printf("  Discrepancies:      %d detected\n", len(res.Discrepancies))
	fmt.Printf("  TDD Test Files:     %d generated in %s\n", len(res.TDDTestFiles), *outputFlag)

	if len(res.Discrepancies) > 0 {
		fmt.Println("================================================================================")
		fmt.Println("  Detected Discrepancies Sample:")
		fmt.Println("================================================================================")
		limit := 3
		if len(res.Discrepancies) < limit {
			limit = len(res.Discrepancies)
		}
		for i := 0; i < limit; i++ {
			d := res.Discrepancies[i]
			fmt.Printf("  [%d] %s\n", i+1, d.Details)
			fmt.Printf("      Query: %s\n", d.Query)
			fmt.Printf("      Expected: %s vs Actual: %s\n", d.Expected, d.Actual)
		}
		if len(res.TDDTestFiles) > 0 {
			fmt.Printf("\nGenerated TDD test file: %s\n", res.TDDTestFiles[0])
		}
	}
	fmt.Println("================================================================================")
}
