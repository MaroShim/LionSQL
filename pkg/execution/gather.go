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

						if hasSelection {
							if row, dErr := storage.DecodeTupleSelectedWithArena(g.schema, rawData, g.requiredCols, g.arena); dErr == nil {
								chunk.Tuples = append(chunk.Tuples, FormatRowWithSelectedCols(row, g.schema, g.alias, g.colIndexMap, g.requiredCols, g.arena))
								continue
							}
						}

						if lazy, lErr := storage.NewLazyTuple(g.schema, rawData); lErr == nil {
							var vals []any
							if g.arena != nil {
								vals = g.arena.AllocAnySlice(len(g.schema.Columns))
							} else {
								vals = make([]any, len(g.schema.Columns))
							}
							chunk.Tuples = append(chunk.Tuples, Tuple{
								Values: vals,
								Index:  g.colIndexMap,
								Lazy:   lazy,
							})
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
						chunk.Tuples = append(chunk.Tuples, t)
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
	return g.colIndexMap
}
