package execution

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

type scanJob struct {
	rawBytes [][]byte
	rows     []storage.Row
	hasRaw   bool
}

func (j *scanJob) Reset() {
	if cap(j.rawBytes) > 0 {
		clear(j.rawBytes[:cap(j.rawBytes)])
		j.rawBytes = j.rawBytes[:0]
	}
	if cap(j.rows) > 0 {
		clear(j.rows[:cap(j.rows)])
		j.rows = j.rows[:0]
	}
	j.hasRaw = false
}

var scanJobPool = sync.Pool{
	New: func() any {
		return &scanJob{
			rawBytes: make([][]byte, 0, 1024),
			rows:     make([]storage.Row, 0, 1024),
		}
	},
}

// GatherIterator is a parallel execution operator (Intra-Query Multi-Core Parallelism)
// that coordinates multiple worker goroutines scanning, filtering, and decoding table chunks
// in parallel and gathers the resulting tuple chunks for parent Volcano operators.
type GatherIterator struct {
	tx           storage.Transaction
	tableName    string
	alias        string
	schema       *storage.TableSchema
	colIndexMap  *ColumnIndexMap
	requiredCols []int
	filterCond   string
	filterExpr   proc.Expr
	arena        *storage.Arena
	dop          int
	parallelJoin *ParallelJoinConfig

	cancel    context.CancelFunc
	wg        sync.WaitGroup
	chunkCh   chan *Chunk
	errCh     chan error
	curChunk  *Chunk
	curIdx    int
	eof       bool
	closeOnce sync.Once
}

// NewGatherIterator creates a new parallel GatherIterator.
func NewGatherIterator(tx storage.Transaction, tableName, alias string, schema *storage.TableSchema) *GatherIterator {
	if alias == "" {
		alias = tableName
	}
	return &GatherIterator{
		tx:          tx,
		tableName:   tableName,
		alias:       alias,
		schema:      schema,
		colIndexMap: NewColumnIndexMap(schema, alias),
	}
}

// SetDOP sets the Degree of Parallelism (number of parallel workers).
func (g *GatherIterator) SetDOP(dop int) {
	g.dop = dop
}

// SetArena configures an optional memory arena for zero-GC tuple allocation.
func (g *GatherIterator) SetArena(a *storage.Arena) {
	g.arena = a
}

// ParallelJoinConfig holds read-only build table and metadata for multi-worker parallel hash join.
type ParallelJoinConfig struct {
	IsFlatIntTable   bool
	FlatIntTable     []flatHashSlot
	FlatIntMask      int
	IntHashTable     map[int64][]hashMatch
	ProbeKeyIdx      int
	ProbeKey         string
	JoinColMap       *ColumnIndexMap
	JoinType         string // "INNER" or "LEFT"
	BuildRight       bool   // true if left is probe and right is build
	BuildColsCount   int
	ParsedJoinFilter proc.Expr
}

// SetParallelJoin configures parallel hash join probe pushdown for worker goroutines.
func (g *GatherIterator) SetParallelJoin(cfg *ParallelJoinConfig) {
	g.parallelJoin = cfg
}

// ParallelJoin returns the active parallel join configuration, if any.
func (g *GatherIterator) ParallelJoin() *ParallelJoinConfig {
	return g.parallelJoin
}

// SetRequiredCols configures selective column decoding for projection pushdown.
func (g *GatherIterator) SetRequiredCols(cols []int) {
	g.requiredCols = cols
}

// SetFilterCond configures a pushdown filter predicate for parallel evaluation across workers.
func (g *GatherIterator) SetFilterCond(cond string) {
	g.filterCond = strings.TrimSpace(cond)
	if g.filterCond != "" {
		p := proc.NewParser(proc.NewLexer(g.filterCond))
		if expr := p.ParseExpr(); expr != nil && len(p.Errors()) == 0 {
			g.filterExpr = proc.FoldConstants(expr)
		}
	}
}

// EffectiveDOP calculates the actual worker count honoring MAXDOP, NumCPU/2 bounds, and minimum 1.
func (g *GatherIterator) EffectiveDOP() int {
	w := g.dop
	if w <= 0 {
		w = runtime.NumCPU() / 2
		if w < 1 {
			w = 1
		}
		if w > 8 {
			w = 8
		}
	}
	return w
}

// Open initializes the parallel worker pool and begins asynchronous chunk scanning.
func (g *GatherIterator) Open(ctx context.Context) error {
	if g.tx == nil {
		return fmt.Errorf("gather scan: transaction is nil")
	}
	g.colIndexMap = NewColumnIndexMap(g.schema, g.alias)
	g.curIdx = 0
	g.eof = false

	workers := g.EffectiveDOP()
	workerCtx, cancel := context.WithCancel(ctx)
	g.cancel = cancel

	g.chunkCh = make(chan *Chunk, workers*2)
	g.errCh = make(chan error, workers+2)

	cursor, err := g.tx.Scan(g.tableName)
	if err != nil {
		cancel()
		return fmt.Errorf("gather scan on %q: %w", g.tableName, err)
	}

	jobCh := make(chan *scanJob, workers*2)

	// 1. Feeder Goroutine: Reads raw byte records or storage rows without expensive decoding
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		defer close(jobCh)
		defer cursor.Close()

		rawCur, hasRaw := cursor.(storage.RawTupleCursor)
		bufPtr := storage.GetTupleBuffer()
		defer storage.PutTupleBuffer(bufPtr)

		const batchSize = 1024

		for {
			select {
			case <-workerCtx.Done():
				return
			default:
			}

			job := scanJobPool.Get().(*scanJob)
			job.Reset()
			job.hasRaw = hasRaw

			var batchCount int
			var eof bool

			for batchCount < batchSize {
				if !cursor.Next() {
					eof = true
					if cErr := cursor.Err(); cErr != nil && cErr != io.EOF {
						select {
						case g.errCh <- cErr:
						default:
						}
					}
					break
				}

				if hasRaw {
					if rawCur.RawTupleBuffer(bufPtr) {
						b := make([]byte, len(*bufPtr))
						copy(b, *bufPtr)
						job.rawBytes = append(job.rawBytes, b)
						batchCount++
					} else if rawData, ok := rawCur.RawTuple(); ok {
						b := make([]byte, len(rawData))
						copy(b, rawData)
						job.rawBytes = append(job.rawBytes, b)
						batchCount++
					}
				} else {
					if row, rErr := cursor.Row(); rErr == nil {
						job.rows = append(job.rows, row)
						batchCount++
					}
				}
			}

			if batchCount > 0 {
				select {
				case jobCh <- job:
				case <-workerCtx.Done():
					scanJobPool.Put(job)
					return
				}
			} else {
				scanJobPool.Put(job)
			}

			if eof {
				break
			}
		}
	}()

	// 2. Worker Pool: Multiple parallel workers evaluating filter expressions and decoding columns
	hasSelection := len(g.requiredCols) > 0 && g.schema != nil && len(g.requiredCols) < len(g.schema.Columns)

	for w := 0; w < workers; w++ {
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()

			for job := range jobCh {
				chunk := GetChunk()

				if job.hasRaw {
					for _, rawData := range job.rawBytes {
						if g.filterExpr != nil {
							rawTuple := Tuple{
								Raw:    rawData,
								Schema: g.schema,
								Index:  g.colIndexMap,
							}
							matched, _ := EvalTupleBoolExprWithContextErr(rawTuple, g.filterExpr, nil, nil)
							if !matched {
								continue
							}
						}

						var t Tuple
						if hasSelection {
							if row, dErr := storage.DecodeTupleSelectedWithArena(g.schema, rawData, g.requiredCols, g.arena); dErr == nil {
								t = FormatRowWithSelectedCols(row, g.schema, g.alias, g.colIndexMap, g.requiredCols, g.arena)
							}
						}
						if t.IsNil() {
							if lazy, lErr := storage.NewLazyTuple(g.schema, rawData); lErr == nil {
								var vals []any
								if g.arena != nil {
									vals = g.arena.AllocAnySlice(len(g.schema.Columns))
								} else {
									vals = make([]any, len(g.schema.Columns))
								}
								t = Tuple{
									Values: vals,
									Index:  g.colIndexMap,
									Lazy:   lazy,
								}
							}
						}

						if !t.IsNil() {
							if g.parallelJoin != nil {
								g.probeAndAppendJoinTuple(chunk, t)
							} else {
								chunk.Tuples = append(chunk.Tuples, t)
							}
							if chunk.Len() >= DefaultChunkSize {
								select {
								case g.chunkCh <- chunk:
									chunk = GetChunk()
								case <-workerCtx.Done():
									PutChunk(chunk)
									return
								}
							}
						}
					}
				} else {
					for _, row := range job.rows {
						var t Tuple
						if hasSelection {
							t = FormatRowWithSelectedCols(row, g.schema, g.alias, g.colIndexMap, g.requiredCols, g.arena)
						} else {
							t = FormatRowWithArena(row, g.schema, g.alias, g.colIndexMap, g.arena)
						}
						if g.filterExpr != nil {
							matched, _ := EvalTupleBoolExprWithContextErr(t, g.filterExpr, nil, nil)
							if !matched {
								continue
							}
						}
						if g.parallelJoin != nil {
							g.probeAndAppendJoinTuple(chunk, t)
						} else {
							chunk.Tuples = append(chunk.Tuples, t)
						}
						if chunk.Len() >= DefaultChunkSize {
							select {
							case g.chunkCh <- chunk:
								chunk = GetChunk()
							case <-workerCtx.Done():
								PutChunk(chunk)
								return
							}
						}
					}
				}

				scanJobPool.Put(job)

				if chunk.Len() > 0 {
					select {
					case g.chunkCh <- chunk:
					case <-workerCtx.Done():
						PutChunk(chunk)
						return
					}
				} else {
					PutChunk(chunk)
				}
			}
		}()
	}

	// 3. Collector Sentinel: Closes chunkCh when all workers finish ([RULE:ZeroOrphan])
	go func() {
		g.wg.Wait()
		close(g.chunkCh)
	}()

	return nil
}

// NextChunk pulls the next parallel chunk of tuples from worker channels into dest.
func (g *GatherIterator) NextChunk(ctx context.Context, dest *Chunk) error {
	if dest == nil {
		return fmt.Errorf("gather next chunk: nil destination")
	}
	dest.Reset()

	if g.eof {
		return io.EOF
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case chunk, ok := <-g.chunkCh:
		if !ok {
			g.eof = true
			select {
			case err := <-g.errCh:
				if err != nil && err != io.EOF {
					return err
				}
			default:
			}
			return io.EOF
		}

		dest.Tuples = append(dest.Tuples[:0], chunk.Tuples...)
		PutChunk(chunk)
		return nil
	}
}

// Next pulls a single tuple, streaming from the active parallel chunk.
func (g *GatherIterator) Next(ctx context.Context) (Tuple, error) {
	if g.curChunk == nil {
		g.curChunk = GetChunk()
		g.curIdx = 0
	}

	if g.curIdx >= g.curChunk.Len() {
		err := g.NextChunk(ctx, g.curChunk)
		if err != nil {
			return Tuple{}, err
		}
		g.curIdx = 0
	}

	if g.curIdx < g.curChunk.Len() {
		t := g.curChunk.Tuples[g.curIdx]
		g.curIdx++
		return t, nil
	}

	return Tuple{}, io.EOF
}

// Close terminates all background workers and releases channel and pool resources.
func (g *GatherIterator) Close() error {
	g.closeOnce.Do(func() {
		if g.cancel != nil {
			g.cancel()
		}
		g.wg.Wait()
		if g.curChunk != nil {
			PutChunk(g.curChunk)
			g.curChunk = nil
		}
		// Drain any remaining chunks in chunkCh
		if g.chunkCh != nil {
			for c := range g.chunkCh {
				PutChunk(c)
			}
		}
	})
	return nil
}

// Schema returns the output column names for this operator.
func (g *GatherIterator) Schema() []string {
	if g.parallelJoin != nil && g.parallelJoin.JoinColMap != nil {
		return g.parallelJoin.JoinColMap.Columns()
	}
	if g.colIndexMap != nil {
		return g.colIndexMap.cols
	}
	if g.schema != nil {
		cols := make([]string, len(g.schema.Columns))
		for i, c := range g.schema.Columns {
			cols[i] = c.Name
		}
		return cols
	}
	return nil
}

// ColIndexMap returns the column index map for O(1) resolution and join composition.
func (g *GatherIterator) ColIndexMap() *ColumnIndexMap {
	if g.parallelJoin != nil && g.parallelJoin.JoinColMap != nil {
		return g.parallelJoin.JoinColMap
	}
	return g.colIndexMap
}

func (g *GatherIterator) probeAndAppendJoinTuple(chunk *Chunk, t Tuple) {
	cfg := g.parallelJoin
	if cfg == nil {
		chunk.Tuples = append(chunk.Tuples, t)
		return
	}
	val, ok := t.GetIdx(cfg.ProbeKeyIdx)
	if !ok || val == nil {
		if cfg.JoinType == "LEFT" && cfg.BuildRight {
			merged := mergeTuplesWithNullsAndArena(t, cfg.BuildColsCount, cfg.JoinColMap, g.arena)
			chunk.Tuples = append(chunk.Tuples, merged)
		}
		return
	}
	iKey, isInt := toInt64Fast(val)
	if !isInt {
		if cfg.JoinType == "LEFT" && cfg.BuildRight {
			merged := mergeTuplesWithNullsAndArena(t, cfg.BuildColsCount, cfg.JoinColMap, g.arena)
			chunk.Tuples = append(chunk.Tuples, merged)
		}
		return
	}

	found := false
	if cfg.IsFlatIntTable {
		slot := int(splitmix64(uint64(iKey)) & uint64(cfg.FlatIntMask))
		for cfg.FlatIntTable[slot].used {
			if cfg.FlatIntTable[slot].key == iKey {
				found = true
				buildTuple := cfg.FlatIntTable[slot].tuple
				merged := mergeTuplesDirect(t, buildTuple, cfg.BuildRight, cfg.JoinColMap, g.arena)
				if cfg.ParsedJoinFilter == nil || g.evalJoinFilter(merged, cfg.ParsedJoinFilter) {
					chunk.Tuples = append(chunk.Tuples, merged)
				}
			}
			slot = (slot + 1) & cfg.FlatIntMask
		}
	} else if len(cfg.IntHashTable) > 0 {
		if matches, exists := cfg.IntHashTable[iKey]; exists {
			for _, m := range matches {
				found = true
				merged := mergeTuplesDirect(t, m.tuple, cfg.BuildRight, cfg.JoinColMap, g.arena)
				if cfg.ParsedJoinFilter == nil || g.evalJoinFilter(merged, cfg.ParsedJoinFilter) {
					chunk.Tuples = append(chunk.Tuples, merged)
				}
			}
		}
	}

	if !found && cfg.JoinType == "LEFT" && cfg.BuildRight {
		merged := mergeTuplesWithNullsAndArena(t, cfg.BuildColsCount, cfg.JoinColMap, g.arena)
		chunk.Tuples = append(chunk.Tuples, merged)
	}
}

func (g *GatherIterator) evalJoinFilter(t Tuple, filter proc.Expr) bool {
	matched, _ := EvalTupleBoolExprWithContextErr(t, filter, nil, nil)
	return matched
}

func mergeTuplesDirect(probeTuple, buildTuple Tuple, buildRight bool, colMap *ColumnIndexMap, arena *storage.Arena) Tuple {
	if probeTuple.Lazy != nil {
		probeTuple.Materialize()
	}
	if buildTuple.Lazy != nil {
		buildTuple.Materialize()
	}
	var leftVals, rightVals []any
	if buildRight {
		leftVals = probeTuple.Values
		rightVals = buildTuple.Values
	} else {
		leftVals = buildTuple.Values
		rightVals = probeTuple.Values
	}
	totalLen := len(leftVals) + len(rightVals)
	var vals []any
	if arena != nil {
		vals = arena.AllocAnySlice(totalLen)
	} else {
		vals = make([]any, totalLen)
	}
	copy(vals, leftVals)
	copy(vals[len(leftVals):], rightVals)
	return Tuple{
		Values: vals,
		Index:  colMap,
	}
}
