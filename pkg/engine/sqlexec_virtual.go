package engine

import (
	"sort"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/catalog/virtual"
	"lionsql/pkg/storage"
)

var _ virtual.Source = (*Coordinator)(nil)

// ListTables satisfies virtual.Source.
func (s *Coordinator) ListTables() ([]*storage.TableSchema, error) {
	if s.engine == nil {
		return nil, nil
	}
	return s.engine.ListTables()
}

// GetTable satisfies virtual.Source.
func (s *Coordinator) GetTable(name string) (*storage.TableSchema, error) {
	if s.engine == nil {
		return nil, storage.ErrTableNotFound
	}
	return s.engine.GetTable(name)
}

// ListProcedures satisfies virtual.Source.
func (s *Coordinator) ListProcedures() []*catalog.Procedure {
	if s.catalog == nil {
		return nil
	}
	return s.catalog.ListProcedures()
}

// ListFunctions satisfies virtual.Source.
func (s *Coordinator) ListFunctions() []*catalog.Function {
	if s.catalog == nil {
		return nil
	}
	return s.catalog.ListFunctions()
}

// ListViews satisfies virtual.Source.
func (s *Coordinator) ListViews() []*catalog.View {
	if s.catalog == nil {
		return nil
	}
	return s.catalog.ListViews()
}

// GetView satisfies virtual.Source.
func (s *Coordinator) GetView(name string) (*catalog.View, error) {
	if s.catalog == nil {
		return nil, catalog.ErrViewNotFound
	}
	return s.catalog.GetView(name)
}

// GetSortedTables satisfies virtual.Source.
func (s *Coordinator) GetSortedTables() []*storage.TableSchema {
	if s.engine == nil {
		return nil
	}
	tables, _ := s.engine.ListTables()
	sort.Slice(tables, func(i, j int) bool {
		return tables[i].Name < tables[j].Name
	})
	return tables
}

// GetSortedViews satisfies virtual.Source.
func (s *Coordinator) GetSortedViews() []*catalog.View {
	if s.catalog == nil {
		return nil
	}
	views := s.catalog.ListViews()
	sort.Slice(views, func(i, j int) bool {
		return views[i].Name < views[j].Name
	})
	return views
}

// GetSortedProcedures satisfies virtual.Source.
func (s *Coordinator) GetSortedProcedures() []*catalog.Procedure {
	if s.catalog == nil {
		return nil
	}
	procs := s.catalog.ListProcedures()
	sort.Slice(procs, func(i, j int) bool {
		return procs[i].Name < procs[j].Name
	})
	return procs
}

func (s *Coordinator) getSortedTables() []*storage.TableSchema {
	return s.GetSortedTables()
}

func (s *Coordinator) getSortedViews() []*catalog.View {
	return s.GetSortedViews()
}

func (s *Coordinator) getSortedProcedures() []*catalog.Procedure {
	return s.GetSortedProcedures()
}

// GetTableRowCount returns row count for a table, consulting stats first then counting.
func (s *Coordinator) GetTableRowCount(name string) int64 {
	if s.catalog != nil {
		if st, ok := s.catalog.GetTableStats(name); ok && st != nil && st.RowCount > 0 {
			return st.RowCount
		}
	}
	if s.engine != nil {
		sess, err := s.engine.NewSession()
		if err == nil && sess != nil {
			defer sess.Close()
			tx, err := sess.BeginTx(storage.TxOptions{ReadOnly: true})
			if err == nil {
				cursor, err := tx.Scan(name)
				if err == nil {
					var cnt int64
					for cursor.Next() {
						cnt++
					}
					cursor.Close()
					return cnt
				}
			}
		}
	}
	return 0
}

func (s *Coordinator) execVirtualCatalogJoinSelect(sql string, start time.Time) (*QueryResult, error) {
	res, err := virtual.Execute(s, sql)
	if err != nil {
		return nil, err
	}
	return &QueryResult{
		Success:      true,
		Command:      "SELECT",
		Columns:      res.Columns,
		Rows:         res.Rows,
		RowsAffected: int64(len(res.Rows)),
		DurationUS:   time.Since(start).Microseconds(),
	}, nil
}
