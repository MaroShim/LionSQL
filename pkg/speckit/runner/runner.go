package runner

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/speckit/grammar"
	"lionsql/pkg/speckit/minimizer"
	"lionsql/pkg/speckit/oracle"
	"lionsql/pkg/storage/memengine"
)

// Config configures the Spec Discovery Runner.
type Config struct {
	Duration   time.Duration
	Workers    int
	MaxDepth   int
	OracleType string // "norec", "dualengine", "all"
	OutputDir  string // Directory to write TDD Red test files (e.g. "test/spec_defects")
	Verbose    bool
}

// Result summarizes the discovery run.
type Result struct {
	TotalQueries    int64
	TotalTables     int64
	Discrepancies   []*oracle.Discrepancy
	TDDTestFiles    []string
	ElapsedDuration time.Duration
	OracleExecuted  oracle.DiscrepancyType
}

// Run executes the autonomous discovery loop.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 3
	}
	if cfg.Duration <= 0 {
		cfg.Duration = 10 * time.Second
	}
	if cfg.OutputDir != "" {
		_ = os.MkdirAll(cfg.OutputDir, 0755)
	}

	res := &Result{}
	var queryCount atomic.Int64
	var tableCount atomic.Int64
	var discMu sync.Mutex

	ctx, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()

	var wg sync.WaitGroup
	start := time.Now()

	for workerID := 0; workerID < cfg.Workers; workerID++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(wid*1000)))

			eng := memengine.New()
			_ = eng.Open("")
			defer eng.Close()
			cat := catalog.New(eng)
			coord := engine.NewCoordinator(cat, eng, engine.Config{})

			sessID := fmt.Sprintf("spec_runner_worker_%d", wid)
			_, _ = coord.GetOrCreateSession(sessID)
			defer coord.CloseSession(sessID)

			// Setup second coordinator for dualengine verification if requested
			var coord2 *engine.Coordinator
			var sessID2 string
			if cfg.OracleType == "dualengine" || cfg.OracleType == "all" {
				eng2 := memengine.New()
				_ = eng2.Open("")
				defer eng2.Close()
				cat2 := catalog.New(eng2)
				coord2 = engine.NewCoordinator(cat2, eng2, engine.Config{})
				sessID2 = fmt.Sprintf("spec_runner_worker2_%d", wid)
				_, _ = coord2.GetOrCreateSession(sessID2)
				defer coord2.CloseSession(sessID2)
			}

			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				// 1. Generate Table Schema and Seed Data
				tblName := fmt.Sprintf("fuzz_w%d_%d", wid, rng.Intn(100000))
				numCols := rng.Intn(4) + 3 // 3 to 6 columns
				table := grammar.GenerateTableSchema(rng, tblName, numCols)
				tableDDL := table.GenerateCreateTableDDL()
				insertDMLs := table.GenerateInsertDML(rng, 10)
				tableCount.Add(1)

				// Create Table
				if _, err := coord.ExecuteSessionSQL(sessID, tableDDL); err != nil {
					continue
				}
				for _, dml := range insertDMLs {
					_, _ = coord.ExecuteSessionSQL(sessID, dml)
				}
				if coord2 != nil {
					if _, err := coord2.ExecuteSessionSQL(sessID2, tableDDL); err == nil {
						for _, dml := range insertDMLs {
							_, _ = coord2.ExecuteSessionSQL(sessID2, dml)
						}
					}
				}

				// 2. Generate and Evaluate Queries
				for qIdx := 0; qIdx < 15; qIdx++ {
					select {
					case <-ctx.Done():
						_, _ = coord.ExecuteSessionSQL(sessID, fmt.Sprintf("DROP TABLE IF EXISTS %s;", tblName))
						if coord2 != nil {
							_, _ = coord2.ExecuteSessionSQL(sessID2, fmt.Sprintf("DROP TABLE IF EXISTS %s;", tblName))
						}
						return
					default:
					}

					var disc *oracle.Discrepancy
					var err error

					mode := cfg.OracleType
					if mode == "all" {
						modes := []string{"norec", "tlp", "pqs", "dualengine"}
						mode = modes[rng.Intn(len(modes))]
					}

					switch mode {
					case "dualengine":
						res.OracleExecuted = oracle.DiscrepancyDualEngine
						q := grammar.GenerateSelectQuery(rng, table, cfg.MaxDepth)
						queryCount.Add(2)
						if coord2 != nil {
							disc, err = oracle.CheckDualEngine(coord, sessID, "OCC_1", coord2, sessID2, "OCC_2", q, tableDDL, insertDMLs)
						}
					case "tlp":
						res.OracleExecuted = oracle.DiscrepancyTLP
						pair := grammar.GenerateTLPPair(rng, table, cfg.MaxDepth)
						queryCount.Add(4)
						disc, err = oracle.CheckTLP(coord, sessID, pair, tableDDL, insertDMLs)
					case "pqs":
						res.OracleExecuted = oracle.DiscrepancyPQS
						pivotID := int64(rng.Intn(len(insertDMLs)) + 1)
						pair := grammar.GeneratePQSPair(rng, table, pivotID)
						queryCount.Add(1)
						disc, err = oracle.CheckPQS(coord, sessID, pair, tableDDL, insertDMLs)
					case "norec":
						fallthrough
					default:
						res.OracleExecuted = oracle.DiscrepancyNoREC
						pair := grammar.GenerateNoRECPair(rng, table, cfg.MaxDepth)
						queryCount.Add(2)
						disc, err = oracle.CheckNoREC(coord, sessID, pair, tableDDL, insertDMLs)
					}

					if err == nil && disc != nil {
						// Discrepancy Found!
						minDisc := minimizer.Minimize(coord, sessID, disc)

						discMu.Lock()
						defectNum := len(res.Discrepancies) + 1
						res.Discrepancies = append(res.Discrepancies, minDisc)

						if cfg.OutputDir != "" {
							testName := fmt.Sprintf("TestSpecDefect_%s_%d", minDisc.Type, defectNum)
							defectID := fmt.Sprintf("DEFECT-ENGINE-%s-%d", minDisc.Type, defectNum)
							content := minimizer.GenerateTDDTestFile("spec_defects", testName, defectID, minDisc)
							filePath := filepath.Join(cfg.OutputDir, fmt.Sprintf("spec_defect_%s_%d_test.go", minDisc.Type, defectNum))
							_ = os.WriteFile(filePath, []byte(content), 0644)
							res.TDDTestFiles = append(res.TDDTestFiles, filePath)
						}
						discMu.Unlock()

						if cfg.Verbose {
							fmt.Printf("[DISCOVERY] Worker %d found discrepancy #%d (%s): %s\n", wid, defectNum, disc.Type, disc.Details)
						}
					}
				}

				// Cleanup Table
				_, _ = coord.ExecuteSessionSQL(sessID, fmt.Sprintf("DROP TABLE IF EXISTS %s;", tblName))
			}
		}(workerID)
	}

	wg.Wait()
	res.ElapsedDuration = time.Since(start)
	res.TotalQueries = queryCount.Load()
	res.TotalTables = tableCount.Load()

	return res, nil
}
