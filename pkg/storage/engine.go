package storage

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrTableNotFound         = errors.New("table not found")
	ErrTableExists           = errors.New("table already exists")
	ErrKeyNotFound           = errors.New("key not found")
	ErrKeyExists             = errors.New("key already exists (primary key constraint)")
	ErrTxClosed              = errors.New("transaction is already closed")
	ErrColumnNotFound        = errors.New("column not found")
	ErrInvalidDataType       = errors.New("invalid data type for column")
	ErrNotNullViolation      = errors.New("not null constraint violation")
	ErrSerializationFailure  = errors.New("serialization failure: concurrent update conflict")
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

// TableSchema defines a table structure.
type TableSchema struct {
	Name    string   `json:"name"`
	Columns []Column `json:"columns"`
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
	return Value{Type: TypeInt, IVal: v}
}

func StringValue(v string) Value {
	return Value{Type: TypeVarChar, SVal: v}
}

func FloatValue(v float64) Value {
	return Value{Type: TypeFloat, FVal: v}
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
}
