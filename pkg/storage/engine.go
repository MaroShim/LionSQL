package storage

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
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
	ErrForeignKeyViolation  = errors.New("foreign key constraint violation")
	ErrDependentObjectsExist = errors.New("cannot drop table because other objects depend on it")
	ErrSerializationFailure = errors.New("serialization failure: concurrent update conflict")
	ErrInvalidJSON          = errors.New("invalid JSON syntax")
	ErrSavepointNotFound    = errors.New("savepoint not found")
	ErrPartitionNotFound    = errors.New("partition not found")
	ErrTableNotPartitioned  = errors.New("table is not partitioned")
	ErrReadOnlyTransaction  = errors.New("cannot execute mutation in read-only transaction (SQLSTATE 25006)")
	ErrReadOnlyReplica      = errors.New("cannot execute write operations on read-only replica (SQLSTATE 25006)")
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
	TypeJSON
	TypeDecimal
	TypeUUID
	TypeBinary
	TypeDate
	TypeTime
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
	case TypeJSON:
		return "JSON"
	case TypeDecimal:
		return "DECIMAL"
	case TypeUUID:
		return "UUID"
	case TypeBinary:
		return "VARBINARY"
	case TypeDate:
		return "DATE"
	case TypeTime:
		return "TIME"
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
	Identity   bool     `json:"identity,omitempty"`
	Precision  int      `json:"precision,omitempty"`
	Scale      int      `json:"scale,omitempty"`
}

// IndexSchema defines metadata for a secondary index.
type IndexSchema struct {
	Name      string   `json:"name"`
	TableName string   `json:"table_name"`
	Columns   []string `json:"columns"`
	Unique    bool     `json:"unique"`
}

// ForeignKeyAction defines the referential action when parent key is deleted or updated.
type ForeignKeyAction int

const (
	ActionNoAction ForeignKeyAction = iota
	ActionRestrict
	ActionCascade
	ActionSetNull
	ActionSetDefault
)

func (a ForeignKeyAction) String() string {
	switch a {
	case ActionCascade:
		return "CASCADE"
	case ActionSetNull:
		return "SET NULL"
	case ActionSetDefault:
		return "SET DEFAULT"
	case ActionRestrict:
		return "RESTRICT"
	default:
		return "NO ACTION"
	}
}

// ParseForeignKeyAction converts action string ("CASCADE", "SET NULL", "RESTRICT", "NO ACTION") to enum.
func ParseForeignKeyAction(s string) ForeignKeyAction {
	upper := strings.ToUpper(strings.TrimSpace(s))
	switch upper {
	case "CASCADE":
		return ActionCascade
	case "SET NULL":
		return ActionSetNull
	case "SET DEFAULT":
		return ActionSetDefault
	case "RESTRICT":
		return ActionRestrict
	default:
		return ActionNoAction
	}
}

// ForeignKeySchema defines a foreign key constraint linking child columns to parent table columns.
type ForeignKeySchema struct {
	Name          string           `json:"name"`
	ChildTable    string           `json:"child_table"`
	ChildColumns  []string         `json:"child_columns"`
	ParentTable   string           `json:"parent_table"`
	ParentColumns []string         `json:"parent_columns"`
	OnDelete      ForeignKeyAction `json:"on_delete"`
	OnUpdate      ForeignKeyAction `json:"on_update"`
}

// PartitionDef defines the partitioning strategy for a table (Alternative A: ClickHouse-style Auto Rolling Range Partitioning).
type PartitionDef struct {
	Expr   string `json:"expr"`   // e.g. "toYYYYMM(created_at)" or "MONTH(created_at)"
	Column string `json:"column"` // e.g. "created_at"
	Func   string `json:"func"`   // "TOYYYYMM", "MONTH", "TOYYYYMMDD", "DAY", "TOYYYY", "YEAR", "COLUMN"
}

// PartitionInfo describes partition metadata and row metrics.
type PartitionInfo struct {
	Name     string `json:"name"`
	RowCount int64  `json:"row_count"`
}

// TableSchema defines a table structure.
type TableSchema struct {
	Name        string             `json:"name"`
	Columns     []Column           `json:"columns"`
	Indexes     []IndexSchema      `json:"indexes,omitempty"`
	ForeignKeys []ForeignKeySchema `json:"foreign_keys,omitempty"`
	Partition   *PartitionDef      `json:"partition,omitempty"`
}

func (s *TableSchema) IsPartitioned() bool {
	return s != nil && s.Partition != nil && s.Partition.Column != ""
}

func (s *TableSchema) ComputePartitionKey(rec Record) (string, error) {
	if !s.IsPartitioned() {
		return "", nil
	}
	val, ok := rec[s.Partition.Column]
	if !ok || val.IsNull {
		return "null", nil
	}
	return s.ComputePartitionKeyFromValue(val)
}

func (s *TableSchema) ComputePartitionKeyFromRow(row Row) (string, error) {
	if !s.IsPartitioned() {
		return "", nil
	}
	_, idx, found := s.FindColumn(s.Partition.Column)
	if !found || idx < 0 || idx >= len(row.Values) {
		return "null", nil
	}
	val := row.Values[idx]
	if val.IsNull {
		return "null", nil
	}
	return s.ComputePartitionKeyFromValue(val)
}

func (s *TableSchema) ComputePartitionKeyFromValue(val Value) (string, error) {
	if !s.IsPartitioned() {
		return "", nil
	}
	if val.IsNull {
		return "null", nil
	}

	fn := strings.ToUpper(s.Partition.Func)
	switch fn {
	case "TOYYYYMM", "MONTH":
		t, err := parseTimeFromValue(val)
		if err != nil {
			return "", err
		}
		return t.Format("200601"), nil

	case "TOYYYYMMDD", "DAY":
		t, err := parseTimeFromValue(val)
		if err != nil {
			return "", err
		}
		return t.Format("20060102"), nil

	case "TOYYYY", "YEAR", "TOYEAR":
		t, err := parseTimeFromValue(val)
		if err != nil {
			return "", err
		}
		return t.Format("2006"), nil

	case "COLUMN":
		return val.String(), nil

	default:
		return val.String(), nil
	}
}

func parseTimeFromValue(val Value) (time.Time, error) {
	switch val.Type {
	case TypeDateTime:
		return val.TVal, nil
	case TypeDate:
		t, err := time.Parse("2006-01-02", val.SVal)
		if err != nil {
			return time.Time{}, err
		}
		return t, nil
	case TypeVarChar:
		str := strings.TrimSpace(val.SVal)
		layouts := []string{
			"2006-01-02 15:04:05",
			"2006-01-02 15:04:05.000000",
			"2006-01-02T15:04:05",
			"2006-01-02T15:04:05Z07:00",
			time.RFC3339,
			"2006-01-02",
			"2006/01/02",
			"20060102",
		}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, str); err == nil {
				return t, nil
			}
		}
		return time.Time{}, fmt.Errorf("cannot parse %q as date/time", str)
	case TypeInt:
		if val.IVal >= 10000000 && val.IVal <= 99999999 {
			if t, err := time.Parse("20060102", fmt.Sprintf("%d", val.IVal)); err == nil {
				return t, nil
			}
		}
		return time.Unix(val.IVal, 0), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported type %s for partition date calculation", val.Type)
	}
}

func (s *TableSchema) FindForeignKey(name string) (*ForeignKeySchema, int, bool) {
	for i := range s.ForeignKeys {
		if strings.EqualFold(s.ForeignKeys[i].Name, name) {
			return &s.ForeignKeys[i], i, true
		}
	}
	return nil, -1, false
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
			if strings.EqualFold(col, colName) {
				return &s.Indexes[i], true
			}
		}
	}
	return nil, false
}

// FindBestCompositeIndex finds the index that has the longest leading-column prefix
// fully covered by the provided equality-condition column set.
// It returns the chosen index and the prefix length (number of leading columns matched).
// For example, given index (dept, year, salary) and eqCols={dept, year},
// it returns the index and prefixLen=2.
func (s *TableSchema) FindBestCompositeIndex(eqCols map[string]bool) (*IndexSchema, int) {
	var best *IndexSchema
	bestPrefixLen := 0
	for i := range s.Indexes {
		idx := &s.Indexes[i]
		prefixLen := 0
		for _, col := range idx.Columns {
			if eqCols[strings.ToLower(col)] {
				prefixLen++
			} else {
				break
			}
		}
		if prefixLen > bestPrefixLen || (prefixLen == bestPrefixLen && prefixLen > 0 && (best == nil || len(idx.Columns) < len(best.Columns))) {
			best = idx
			bestPrefixLen = prefixLen
		}
	}
	return best, bestPrefixLen
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
	DVal   Decimal
	BinVal string
}

// Bytes returns the raw byte slice for TypeBinary values.
func (v Value) Bytes() []byte {
	return []byte(v.BinVal)
}

func IntValue(v int64) Value {
	return Value{Type: TypeInt, IVal: v, FVal: float64(v), DVal: NewDecimalFromInt(v, 0)}
}

func StringValue(v string) Value {
	return Value{Type: TypeVarChar, SVal: v}
}

func FloatValue(v float64) Value {
	return Value{Type: TypeFloat, FVal: v, IVal: int64(v), DVal: NewDecimalFromFloat(v, 4)}
}

func BoolValue(v bool) Value {
	return Value{Type: TypeBool, BVal: v}
}

func DateTimeValue(v time.Time) Value {
	return Value{Type: TypeDateTime, TVal: v}
}

func JSONValue(v string) Value {
	return Value{Type: TypeJSON, SVal: v}
}

func DecimalValue(s string) Value {
	dec, err := ParseDecimal(s)
	if err != nil {
		dec = ZeroDecimal(2)
	}
	return Value{
		Type: TypeDecimal,
		SVal: dec.String(),
		DVal: dec,
		FVal: dec.Float64(),
		IVal: dec.Int64(),
	}
}

func DecimalValueFromDecimal(d Decimal) Value {
	return Value{
		Type: TypeDecimal,
		SVal: d.String(),
		DVal: d,
		FVal: d.Float64(),
		IVal: d.Int64(),
	}
}

func NullValue(t DataType) Value {
	return Value{Type: t, IsNull: true}
}

// EncodeIndexKeyPart encodes a Value into an order-preserving string for composite index keys.
func EncodeIndexKeyPart(v Value) string {
	if v.IsNull {
		return "\x00NULL"
	}
	switch v.Type {
	case TypeInt:
		// Order-preserving 16-hex digit encoding for signed 64-bit int.
		// XORing with 1<<63 maps MinInt64..MaxInt64 monotonically to 0..MaxUint64.
		u := uint64(v.IVal) ^ 0x8000000000000000
		var b [16]byte
		const hexDigits = "0123456789abcdef"
		b[0] = hexDigits[u>>60&0xf]
		b[1] = hexDigits[u>>56&0xf]
		b[2] = hexDigits[u>>52&0xf]
		b[3] = hexDigits[u>>48&0xf]
		b[4] = hexDigits[u>>44&0xf]
		b[5] = hexDigits[u>>40&0xf]
		b[6] = hexDigits[u>>36&0xf]
		b[7] = hexDigits[u>>32&0xf]
		b[8] = hexDigits[u>>28&0xf]
		b[9] = hexDigits[u>>24&0xf]
		b[10] = hexDigits[u>>20&0xf]
		b[11] = hexDigits[u>>16&0xf]
		b[12] = hexDigits[u>>12&0xf]
		b[13] = hexDigits[u>>8&0xf]
		b[14] = hexDigits[u>>4&0xf]
		b[15] = hexDigits[u&0xf]
		return string(b[:])
	case TypeFloat:
		bits := math.Float64bits(v.FVal)
		if (bits & 0x8000000000000000) != 0 {
			bits = ^bits
		} else {
			bits ^= 0x8000000000000000
		}
		var b [16]byte
		const hexDigits = "0123456789abcdef"
		b[0] = hexDigits[bits>>60&0xf]
		b[1] = hexDigits[bits>>56&0xf]
		b[2] = hexDigits[bits>>52&0xf]
		b[3] = hexDigits[bits>>48&0xf]
		b[4] = hexDigits[bits>>44&0xf]
		b[5] = hexDigits[bits>>40&0xf]
		b[6] = hexDigits[bits>>36&0xf]
		b[7] = hexDigits[bits>>32&0xf]
		b[8] = hexDigits[bits>>28&0xf]
		b[9] = hexDigits[bits>>24&0xf]
		b[10] = hexDigits[bits>>20&0xf]
		b[11] = hexDigits[bits>>16&0xf]
		b[12] = hexDigits[bits>>12&0xf]
		b[13] = hexDigits[bits>>8&0xf]
		b[14] = hexDigits[bits>>4&0xf]
		b[15] = hexDigits[bits&0xf]
		return string(b[:])
	case TypeDateTime:
		return v.TVal.UTC().Format("2006-01-02T15:04:05.000000000Z")
	case TypeDate, TypeTime:
		return v.SVal
	default:
		return v.String()
	}
}

func (v Value) Decimal() Decimal {
	if v.Type == TypeDecimal {
		if v.DVal.value != nil {
			return v.DVal
		}
		if v.SVal != "" {
			d, err := ParseDecimal(v.SVal)
			if err == nil {
				return d
			}
		}
		return ZeroDecimal(2)
	}
	if v.Type == TypeInt {
		return NewDecimalFromInt(v.IVal, 0)
	}
	if v.Type == TypeFloat {
		return NewDecimalFromFloat(v.FVal, 4)
	}
	if v.SVal != "" {
		if d, err := ParseDecimal(v.SVal); err == nil {
			return d
		}
	}
	return ZeroDecimal(0)
}

func (v Value) String() string {
	if v.IsNull {
		return "NULL"
	}
	switch v.Type {
	case TypeInt:
		return fmt.Sprintf("%d", v.IVal)
	case TypeVarChar, TypeJSON:
		return v.SVal
	case TypeFloat:
		return fmt.Sprintf("%f", v.FVal)
	case TypeDecimal:
		if v.DVal.value != nil {
			return v.DVal.String()
		}
		if v.SVal != "" {
			return v.SVal
		}
		return "0"
	case TypeBool:
		return fmt.Sprintf("%t", v.BVal)
	case TypeDateTime:
		return v.TVal.Format(time.RFC3339)
	case TypeDate:
		return v.TVal.Format("2006-01-02")
	case TypeTime:
		return v.TVal.Format("15:04:05")
	case TypeUUID:
		return v.SVal
	case TypeBinary:
		if len(v.BinVal) > 0 {
			return "0x" + hex.EncodeToString([]byte(v.BinVal))
		}
		if v.SVal != "" {
			return v.SVal
		}
		return "0x"
	default:
		return "UNKNOWN"
	}
}

func (v Value) Equal(other Value) bool {
	if v.IsNull && other.IsNull {
		return true
	}
	if v.IsNull != other.IsNull {
		return false
	}
	if v.Type == TypeDecimal || other.Type == TypeDecimal {
		d1 := v.Decimal()
		d2 := other.Decimal()
		return d1.Equal(d2)
	}
	if v.Type != other.Type {
		return false
	}
	switch v.Type {
	case TypeInt:
		return v.IVal == other.IVal
	case TypeVarChar, TypeJSON:
		return v.SVal == other.SVal
	case TypeFloat:
		return fmt.Sprintf("%f", v.FVal) == fmt.Sprintf("%f", other.FVal)
	case TypeBool:
		return v.BVal == other.BVal
	case TypeDateTime:
		return v.TVal.Equal(other.TVal)
	case TypeDate:
		return v.TVal.Equal(other.TVal)
	case TypeTime:
		return v.IVal == other.IVal || v.TVal.Equal(other.TVal)
	case TypeUUID:
		return strings.EqualFold(v.SVal, other.SVal)
	case TypeBinary:
		return v.BinVal == other.BinVal
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
	if v.Type == TypeDecimal || other.Type == TypeDecimal {
		d1 := v.Decimal()
		d2 := other.Decimal()
		return d1.Cmp(d2)
	}
	if v.Type == TypeUUID || v.Type == TypeBinary || v.Type == TypeDate || v.Type == TypeTime {
		return CompareExtendedValues(v, other)
	}
	switch v.Type {
	case TypeInt:
		if v.IVal < other.IVal {
			return -1
		} else if v.IVal > other.IVal {
			return 1
		}
		return 0
	case TypeVarChar, TypeJSON:
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
	Row() (Row, error)
	Close() error
}

// TxOptions configures transaction behaviors.
type TxOptions struct {
	ReadOnly          bool
	SnapshotLSN       int64 // Optional snapshot visibility bound by LSN (0 for current engine LSN)
	SnapshotTimestamp int64 // Optional snapshot visibility bound by unix nano timestamp (0 for current time)
}

// Transaction represents an ACID transaction unit.
type Transaction interface {
	Insert(tableName string, record Record) error
	InsertRow(tableName string, row Row) error
	Update(tableName string, pk Value, record Record) error
	UpdateRow(tableName string, pk Value, row Row) error
	Delete(tableName string, pk Value) error
	Get(tableName string, pk Value) (Record, error)
	GetRow(tableName string, pk Value) (Row, error)
	Scan(tableName string) (Cursor, error)
	ScanPartitions(tableName string, partitions []string) (Cursor, error)
	IndexScan(tableName string, indexName string, low, high *Value) (Cursor, error)
	IndexOnlyScan(tableName string, indexName string, low, high *Value) (Cursor, error)
	IndexScanDesc(tableName string, indexName string, low, high *Value) (Cursor, error)
	Commit() error
	Rollback() error
	Savepoint(name string) error
	RollbackTo(name string) error
	ReleaseSavepoint(name string) error
	SnapshotLSN() int64
	SnapshotTimestamp() int64
	CommitLSN() int64
	CommitTimestamp() int64
}

// Session represents an active connection/session.
type Session interface {
	BeginTx(opts TxOptions) (Transaction, error)
	Close() error
}

// Engine is the top-level contract for the database storage layer.
type Engine interface {
	Type() string
	Open(dir string) error
	Close() error
	CurrentLSN() int64
	NewSession() (Session, error)
	CreateTable(schema *TableSchema) error
	DropTable(tableName string) error
	GetTable(tableName string) (*TableSchema, error)
	ListTables() ([]*TableSchema, error)
	CreateIndex(index *IndexSchema) error
	DropIndex(tableName, indexName string) error
	TruncateTable(tableName string) error
	DropPartition(tableName, partitionName string) error
	GetPartitions(tableName string) ([]PartitionInfo, error)
}

