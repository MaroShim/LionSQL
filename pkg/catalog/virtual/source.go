package virtual

import (
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
)

// Source provides schema and catalog metadata to virtual catalog join interceptors.
type Source interface {
	ListTables() ([]*storage.TableSchema, error)
	GetTable(name string) (*storage.TableSchema, error)
	ListProcedures() []*catalog.Procedure
	ListFunctions() []*catalog.Function
	ListViews() []*catalog.View
	GetView(name string) (*catalog.View, error)
	GetSortedTables() []*storage.TableSchema
	GetSortedViews() []*catalog.View
	GetSortedProcedures() []*catalog.Procedure
}

// Result represents the tabular result of an intercepted virtual catalog query.
type Result struct {
	Columns []string
	Rows    []map[string]any
}
