package storage

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrTableNotFound        = errors.New("table not found")
	ErrTableExists          = errors.New("table already exists")
	ErrIndexNotFound        = errors.New("index not found")
	ErrIndexExists          = errors.New("index already exists")
	ErrUniqueViolation      = errors.New("unique constraint violation on index")
	ErrKeyNotFound          = errors.New("key not found")
	ErrKeyExists            = errors.New("key already exists (primary key constraint)")
	ErrTxClosed             = errors.New("transaction is already closed")
	ErrColumnNotFound       = errors.New("column not found")
	ErrInvalidDataType      = errors.New("invalid data type for column")
	ErrNotNullViolation     = errors.New("not null constraint violation")
	ErrSerializationFailure = errors.New("serialization failure: concurrent update conflict")
)

// DataType represents the column data type.
type DataType int

const (
	TypeUnknown DataType = iota
	TypeInt
	TypeVarChar
	TypeFloat
	TypeBool
	TypeDateTime
)

func (t DataType) String() string {
	switch t {
	case TypeInt:
		return "INT"
	case TypeVarChar:
		return "VARCHAR"
	case TypeFloat:
		return "FLOAT"
	case TypeBool:
		return "BOOL"
	case TypeDateTime:
		return "DATETIME"
	default:
		return "UNKNOWN"
	}
}

// Column defines metadata for a single column.
type Column struct {
	Name       string   `json:"name"`
	Type       DataType `json:"type"`
	PrimaryKey bool     `json:"primary_key"`
	Nullable   bool     `json:"nullable"`
}

// IndexSchema defines metadata for a secondary index.
type IndexSchema struct {
	Name      string   `json:"name"`
	TableName string   `json:"table_name"`
	Columns   []string `json:"columns"`
	Unique    bool     `json:"unique"`
}

// TableSchema defines a table structure.
type TableSchema struct {
	Name    string        `json:"name"`
	Columns []Column      `json:"columns"`
	Indexes []IndexSchema `json:"indexes,omitempty"`
}

func (s *TableSchema) PrimaryKeyColumn() (*Column, error) {
	for i := range s.Columns {
		if s.Columns[i].PrimaryKey {
			return &s.Columns[i], nil
		}
	}
	return nil, fmt.Errorf("no primary key defined on table %q", s.Name)
}

func (s *TableSchema) FindColumn(name string) (*Column, int, bool) {
	for i := range s.Columns {
		if s.Columns[i].Name == name {
			return &s.Columns[i], i, true
		}
	}
	return nil, -1, false
}

func (s *TableSchema) FindIndex(name string) (*IndexSchema, int, bool) {
	for i := range s.Indexes {
		if s.Indexes[i].Name == name {
			return &s.Indexes[i], i, true
		}
	}
	return nil, -1, false
}

func (s *TableSchema) FindIndexByColumn(colName string) (*IndexSchema, bool) {
	for i := range s.Indexes {
		for _, col := range s.Indexes[i].Columns {
			if col == colName {
				return &s.Indexes[i], true
			}
		}
	}
	return nil, false
}

// Value represents a typed field value.
type Value struct {
	Type   DataType
	IsNull bool
	IVal   int64
	SVal   string
	FVal   float64
	BVal   bool
	TVal   time.Time
}

func IntValue(v int64) Value {
	return Value{Type: TypeInt, IVal: v, FVal: float64(v)}
}

func StringValue(v string) Value {
	return Value{Type: TypeVarChar, SVal: v}
}

func FloatValue(v float64) Value {
	return Value{Type: TypeFloat, FVal: v, IVal: int64(v)}
}

func BoolValue(v bool) Value {
	return Value{Type: TypeBool, BVal: v}
}

func DateTimeValue(v time.Time) Value {
	return Value{Type: TypeDateTime, TVal: v}
}

func NullValue(t DataType) Value {
	return Value{Type: t, IsNull: true}
}

func (v Value) String() string {
	if v.IsNull {
		return "NULL"
	}
	switch v.Type {
	case TypeInt:
		return fmt.Sprintf("%d", v.IVal)
	case TypeVarChar:
		return v.SVal
	case TypeFloat:
		return fmt.Sprintf("%f", v.FVal)
	case TypeBool:
		return fmt.Sprintf("%t", v.BVal)
	case TypeDateTime:
		return v.TVal.Format(time.RFC3339)
	default:
		return "UNKNOWN"
	}
}

func (v Value) Equal(other Value) bool {
	if v.IsNull && other.IsNull {
		return true
	}
	if v.IsNull != other.IsNull || v.Type != other.Type {
		return false
	}
	switch v.Type {
	case TypeInt:
		return v.IVal == other.IVal
	case TypeVarChar:
		return v.SVal == other.SVal
	case TypeFloat:
		return v.FVal == other.FVal
	case TypeBool:
		return v.BVal == other.BVal
	case TypeDateTime:
		return v.TVal.Equal(other.TVal)
	default:
		return false
	}
}

// Compare returns -1 if v < other, 0 if v == other, 1 if v > other.
// NULL is considered smaller than any non-NULL value.
func (v Value) Compare(other Value) int {
	if v.IsNull && other.IsNull {
		return 0
	}
	if v.IsNull {
		return -1
	}
	if other.IsNull {
		return 1
	}
	switch v.Type {
	case TypeInt:
		if v.IVal < other.IVal {
			return -1
		} else if v.IVal > other.IVal {
			return 1
		}
		return 0
	case TypeVarChar:
		if v.SVal < other.SVal {
			return -1
		} else if v.SVal > other.SVal {
			return 1
		}
		return 0
	case TypeFloat:
		if v.FVal < other.FVal {
			return -1
		} else if v.FVal > other.FVal {
			return 1
		}
		return 0
	case TypeBool:
		if !v.BVal && other.BVal {
			return -1
		} else if v.BVal && !other.BVal {
			return 1
		}
		return 0
	case TypeDateTime:
		if v.TVal.Before(other.TVal) {
			return -1
		} else if v.TVal.After(other.TVal) {
			return 1
		}
		return 0
	default:
		return 0
	}
}

// Record is a mapping from column name to Value.
type Record map[string]Value

// Cursor allows iterating over table rows.
type Cursor interface {
	Next() bool
	Record() (Record, error)
	Close() error
}

// TxOptions configures transaction behaviors.
type TxOptions struct {
	ReadOnly bool
}

// Transaction represents an ACID transaction unit.
type Transaction interface {
	Insert(tableName string, record Record) error
	Update(tableName string, pk Value, record Record) error
	Delete(tableName string, pk Value) error
	Get(tableName string, pk Value) (Record, error)
	Scan(tableName string) (Cursor, error)
	IndexScan(tableName string, indexName string, low, high *Value) (Cursor, error)
	Commit() error
	Rollback() error
}

// Session represents an active connection/session.
type Session interface {
	BeginTx(opts TxOptions) (Transaction, error)
	Close() error
}

// Engine is the top-level contract for the database storage layer.
type Engine interface {
	Open(dir string) error
	Close() error
	NewSession() (Session, error)
	CreateTable(schema *TableSchema) error
	DropTable(tableName string) error
	GetTable(tableName string) (*TableSchema, error)
	ListTables() ([]*TableSchema, error)
	CreateIndex(index *IndexSchema) error
	DropIndex(tableName, indexName string) error
}
