package storage

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"lionsql/pkg/sqlutil"
)

// MaxColumnsPerTable is the standard SQL Server parity maximum column limit per table (1,024).
const MaxColumnsPerTable = 1024

// MaxInlineRowSize is the standard SQL Server parity maximum in-row inline byte limit (8,060 bytes).
const MaxInlineRowSize = 8060

// MaxRowSize is the absolute upper bound for a single logical tuple including LOB/overflow columns (16 MB).
const MaxRowSize = 16 * 1024 * 1024

// MaxIdentifierLength is the maximum length in UTF-8 characters (runes) for SQL identifiers (128).
const MaxIdentifierLength = 128

// MaxIndexColumns is the maximum number of key columns allowed in a single index (32).
const MaxIndexColumns = 32

// MaxIndexKeyBytes is the maximum byte size of an index key in a B-Tree index (1,700 bytes).
const MaxIndexKeyBytes = 1700

// MaxIndexesPerTable is the maximum number of non-clustered indexes allowed per table (999).
const MaxIndexesPerTable = 999

var (
	ErrTableNotFound         = errors.New("table not found")
	ErrTableExists           = errors.New("table already exists")
	ErrIndexNotFound         = errors.New("index not found")
	ErrIndexExists           = errors.New("index already exists")
	ErrIndexDisabled         = errors.New("index is disabled")
	ErrUniqueViolation       = errors.New("unique constraint violation on index")
	ErrKeyNotFound           = errors.New("key not found")
	ErrKeyExists             = errors.New("key already exists (primary key constraint)")
	ErrTxClosed              = errors.New("transaction is already closed")
	ErrColumnNotFound        = errors.New("column not found")
	ErrDuplicateColumn       = errors.New("duplicate column name")
	ErrInvalidDataType       = errors.New("invalid data type for column")
	ErrNotNullViolation      = errors.New("not null constraint violation")
	ErrCheckViolation        = errors.New("check constraint violation")
	ErrForeignKeyViolation   = errors.New("foreign key constraint violation")
	ErrDependentObjectsExist = errors.New("cannot drop table because other objects depend on it")
	ErrSerializationFailure  = errors.New("serialization failure: concurrent update conflict")
	ErrInvalidJSON           = errors.New("invalid JSON syntax")
	ErrSavepointNotFound     = errors.New("savepoint not found")
	ErrPartitionNotFound     = errors.New("partition not found")
	ErrTableNotPartitioned   = errors.New("table is not partitioned")
	ErrReadOnlyTransaction   = errors.New("cannot execute mutation in read-only transaction (SQLSTATE 25006)")
	ErrReadOnlyReplica       = errors.New("cannot execute write operations on read-only replica (SQLSTATE 25006)")
	ErrTableFull             = errors.New("table data limit exceeded (SQLSTATE 53200)")
	ErrEngineTainted         = errors.New("storage: engine is tainted due to unrecoverable I/O or fsync failure")
	ErrTooManyColumns        = errors.New("table exceeds maximum column limit (1024 columns)")
	ErrInlineRowTooLarge     = errors.New("table exceeds maximum in-row inline size (8060 bytes)")
	ErrRowTooLarge           = errors.New("row size exceeds maximum limit (16MB)")
	ErrIdentifierTooLong     = errors.New("identifier exceeds maximum length limit (128 characters, SQLSTATE 42622, Msg 103)")
	ErrTooManyIndexColumns   = errors.New("index exceeds maximum column limit (32 columns, SQLSTATE 54011, Msg 1904)")
	ErrIndexKeyTooLarge      = errors.New("index key exceeds maximum size limit (1700 bytes, SQLSTATE 54000, Msg 1946)")
	ErrTooManyIndexes        = errors.New("table exceeds maximum index limit (999 indexes, SQLSTATE 54000, Msg 1905)")
)

// ValidateIdentifier verifies that an identifier does not exceed MaxIdentifierLength (128 characters).
func ValidateIdentifier(name string) error {
	trimmed := strings.Trim(strings.TrimSpace(name), "[]\"'`")
	if trimmed == "" {
		return nil
	}
	// Strip internal temporary table prefix (__temp_<sess>_<userTable>) before checking user identifier length
	if strings.HasPrefix(trimmed, "__temp_") {
		rest := strings.TrimPrefix(trimmed, "__temp_")
		if idx := strings.LastIndex(rest, "_"); idx != -1 && idx+1 < len(rest) {
			trimmed = rest[idx+1:]
		}
	} else if strings.HasPrefix(trimmed, "__db_") {
		// 3-tier composite key (__db_<db>_<schema>_<table>): validate each segment
		rest := strings.TrimPrefix(trimmed, "__db_")
		parts := strings.SplitN(rest, "_", 3)
		for _, p := range parts {
			if rc := utf8.RuneCountInString(p); rc > MaxIdentifierLength {
				return fmt.Errorf("%w: identifier %q has %d characters (max %d)", ErrIdentifierTooLong, p, rc, MaxIdentifierLength)
			}
		}
		return nil
	}
	// If dotted (schema.table), check each segment
	if strings.Contains(trimmed, ".") {
		for _, part := range strings.Split(trimmed, ".") {
			cleanPart := strings.Trim(strings.TrimSpace(part), "[]\"'`")
			if rc := utf8.RuneCountInString(cleanPart); rc > MaxIdentifierLength {
				return fmt.Errorf("%w: identifier %q has %d characters (max %d)", ErrIdentifierTooLong, cleanPart, rc, MaxIdentifierLength)
			}
		}
		return nil
	}
	if rc := utf8.RuneCountInString(trimmed); rc > MaxIdentifierLength {
		return fmt.Errorf("%w: identifier %q has %d characters (max %d)", ErrIdentifierTooLong, trimmed, rc, MaxIdentifierLength)
	}
	return nil
}

// IsWriteConflict checks whether an error is a concurrency write-write conflict
// or serialization failure (e.g., OCC version mismatch, WiredTiger WT_ROLLBACK / -31800).
func IsWriteConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrSerializationFailure) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "WT_ROLLBACK") ||
		strings.Contains(msg, "-31800") ||
		strings.Contains(msg, "concurrent update conflict") ||
		strings.Contains(msg, "serialization failure")
}

// FastRandN returns a non-negative pseudo-random integer in [0, n) using UnixNano.
func FastRandN(n int) int {
	if n <= 0 {
		return 0
	}
	return int(uint64(time.Now().UnixNano()) % uint64(n))
}

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
	TypeReal
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
	case TypeReal:
		return "REAL"
	default:
		return "UNKNOWN"
	}
}

// Column defines metadata for a single column.
type Column struct {
	Name         string   `json:"name" msgpack:"name"`
	Type         DataType `json:"type" msgpack:"type"`
	RawType      string   `json:"raw_type,omitempty" msgpack:"raw_type,omitempty"`
	IsFixed      bool     `json:"is_fixed,omitempty" msgpack:"is_fixed,omitempty"`
	PrimaryKey   bool     `json:"primary_key" msgpack:"primary_key"`
	Nullable     bool     `json:"nullable" msgpack:"nullable"`
	Identity          bool     `json:"identity,omitempty" msgpack:"identity,omitempty"`
	IdentitySeed      int64    `json:"identity_seed,omitempty" msgpack:"identity_seed,omitempty"`
	IdentityIncrement int64    `json:"identity_increment,omitempty" msgpack:"identity_increment,omitempty"`
	IsRowVersion      bool     `json:"is_rowversion,omitempty" msgpack:"is_rowversion,omitempty"`
	Hidden       bool     `json:"hidden,omitempty" msgpack:"hidden,omitempty"`
	Precision    int      `json:"precision,omitempty" msgpack:"precision,omitempty"`
	Scale        int      `json:"scale,omitempty" msgpack:"scale,omitempty"`
	Default      string   `json:"default,omitempty" msgpack:"default,omitempty"`
	CheckExpr    string   `json:"check_expr,omitempty" msgpack:"check_expr,omitempty"`
	ComputedExpr string   `json:"computed_expr,omitempty" msgpack:"computed_expr,omitempty"`
	IsComputed   bool     `json:"is_computed,omitempty" msgpack:"is_computed,omitempty"`
}

// HasComputedExpr reports whether the column is a computed (virtual/generated) column.
func (c Column) HasComputedExpr() bool {
	return (c.IsComputed && c.ComputedExpr != "") || (c.ComputedExpr != "")
}

// GetIdentitySeed returns the seed value for an identity column, defaulting to 1 if unset.
func (c Column) GetIdentitySeed() int64 {
	if c.IdentitySeed != 0 {
		return c.IdentitySeed
	}
	return 1
}

// GetIdentityIncrement returns the increment/step value for an identity column, defaulting to 1 if unset.
func (c Column) GetIdentityIncrement() int64 {
	if c.IdentityIncrement != 0 {
		return c.IdentityIncrement
	}
	return 1
}

// IndexSchema defines metadata for a secondary index.
type IndexSchema struct {
	Name        string   `json:"name"`
	TableName   string   `json:"table_name"`
	Columns     []string `json:"columns"`
	IncludeCols []string `json:"include_cols,omitempty"`
	Unique      bool     `json:"unique"`
	Expression  string   `json:"expression,omitempty" msgpack:"expression,omitempty"`
	ExprColumn  string   `json:"expr_column,omitempty" msgpack:"expr_column,omitempty"`
	ExprPath    string   `json:"expr_path,omitempty" msgpack:"expr_path,omitempty"`
	Disabled    bool     `json:"disabled,omitempty" msgpack:"disabled,omitempty"`
}

// IsExpression reports whether the index is a functional expression index on a JSON document or column expression.
func (s *IndexSchema) IsExpression() bool {
	return s != nil && (s.Expression != "" || s.ExprColumn != "")
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

// IsTemporaryTable returns true if tableName represents a session-isolated temporary table
// or table variable that should not be persisted across database restarts or WAL logs.
func IsTemporaryTable(name string) bool {
	if isTempPrefix(name) {
		return true
	}
	if strings.ContainsAny(name, ".[]\"'` ") {
		clean := strings.Trim(strings.TrimSpace(name), "[]\"'`")
		if idx := strings.LastIndex(clean, "."); idx != -1 {
			clean = strings.Trim(clean[idx+1:], "[]\"'`")
		}
		return isTempPrefix(clean)
	}
	return false
}

func isTempPrefix(name string) bool {
	if len(name) == 0 {
		return false
	}
	if name[0] == '#' {
		return true
	}
	if name[0] != '_' {
		return false
	}
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "__temp_") ||
		strings.HasPrefix(lower, "__tv_") ||
		strings.HasPrefix(lower, "__var_")
}

// IsTableVariable returns true if tableName represents a T-SQL table variable (@var, __tv_*, or __var_*).
func IsTableVariable(name string) bool {
	if len(name) == 0 {
		return false
	}
	clean := strings.Trim(strings.TrimSpace(name), "[]\"'`")
	if idx := strings.LastIndex(clean, "."); idx != -1 {
		clean = strings.Trim(clean[idx+1:], "[]\"'`")
	}
	if len(clean) == 0 {
		return false
	}
	if clean[0] == '@' {
		return true
	}
	lower := strings.ToLower(clean)
	return strings.HasPrefix(lower, "__tv_") || strings.HasPrefix(lower, "__var_")
}

// Durability options for tables (Microsoft SQL Server / In-Memory OLTP parity)
const (
	DurabilitySchemaAndData = "SCHEMA_AND_DATA"
	DurabilitySchemaOnly    = "SCHEMA_ONLY"
)

// TableSchema defines a table structure.
type TableSchema struct {
	Name             string             `json:"name"`
	PhysicalName     string             `json:"physical_name,omitempty"`
	Columns          []Column           `json:"columns"`
	Indexes          []IndexSchema      `json:"indexes,omitempty"`
	ForeignKeys      []ForeignKeySchema `json:"foreign_keys,omitempty"`
	CheckConstraints []string           `json:"check_constraints,omitempty"`
	Partition        *PartitionDef      `json:"partition,omitempty"`
	Durability       string             `json:"durability,omitempty"`       // "SCHEMA_AND_DATA" (default) or "SCHEMA_ONLY"
	MemoryOptimized  bool               `json:"memory_optimized,omitempty"` // true if WITH (MEMORY_OPTIMIZED = ON)
}

// Clone returns a deep copy of TableSchema so callers can mutate trial copies safely.
func (s TableSchema) Clone() TableSchema {
	out := s
	if s.Columns != nil {
		out.Columns = append([]Column(nil), s.Columns...)
	}
	if s.CheckConstraints != nil {
		out.CheckConstraints = append([]string(nil), s.CheckConstraints...)
	}
	if s.Indexes != nil {
		out.Indexes = make([]IndexSchema, len(s.Indexes))
		for i, idx := range s.Indexes {
			out.Indexes[i] = idx
			if idx.Columns != nil {
				out.Indexes[i].Columns = append([]string(nil), idx.Columns...)
			}
			if idx.IncludeCols != nil {
				out.Indexes[i].IncludeCols = append([]string(nil), idx.IncludeCols...)
			}
		}
	}
	if s.ForeignKeys != nil {
		out.ForeignKeys = make([]ForeignKeySchema, len(s.ForeignKeys))
		for i, fk := range s.ForeignKeys {
			out.ForeignKeys[i] = fk
			if fk.ChildColumns != nil {
				out.ForeignKeys[i].ChildColumns = append([]string(nil), fk.ChildColumns...)
			}
			if fk.ParentColumns != nil {
				out.ForeignKeys[i].ParentColumns = append([]string(nil), fk.ParentColumns...)
			}
		}
	}
	if s.Partition != nil {
		pCopy := *s.Partition
		out.Partition = &pCopy
	}
	return out
}

// HasCheckConstraints reports whether the table or any of its columns define a CHECK constraint.
func (s *TableSchema) HasCheckConstraints() bool {
	if s == nil {
		return false
	}
	if len(s.CheckConstraints) > 0 {
		return true
	}
	for i := range s.Columns {
		if s.Columns[i].CheckExpr != "" {
			return true
		}
	}
	return false
}

// FixedRowSize computes the minimal in-row footprint of all fixed-size columns and header metadata.
func (s *TableSchema) FixedRowSize() int {
	if s == nil {
		return 0
	}
	numCols := len(s.Columns)
	nullBitmapLen := (numCols + 7) / 8
	size := 4 + nullBitmapLen // 4-byte V2 header + NullBitmap
	for _, col := range s.Columns {
		switch col.Type {
		case TypeInt:
			size += 8
		case TypeFloat:
			size += 8
		case TypeReal:
			size += 4
		case TypeBool:
			size += 1
		case TypeDateTime, TypeDate, TypeTime:
			size += 8
		case TypeUUID:
			size += 16
		case TypeDecimal:
			size += 16
		case TypeVarChar, TypeBinary, TypeJSON:
			if col.IsFixed || strings.EqualFold(col.RawType, "CHAR") || strings.EqualFold(col.RawType, "NCHAR") || strings.EqualFold(col.RawType, "BINARY") {
				n := col.Precision
				if n <= 0 {
					n = 1
				}
				if strings.EqualFold(col.RawType, "NCHAR") {
					n *= 2
				}
				size += n
			} else if col.Precision < 0 {
				// 24-byte in-row LOB pointer footprint for VARCHAR(MAX)/NVARCHAR(MAX)/VARBINARY(MAX)
				size += 24
			} else {
				// 2-byte variable column offset/length entry
				size += 2
			}
		}
	}
	return size
}

// ValidateRowSize validates that the table schema adheres to identifier length, column uniqueness,
// SQL Server in-row inline byte limit (8,060 bytes), and index key/column constraints.
func (s *TableSchema) ValidateRowSize() error {
	if s == nil {
		return errors.New("nil table schema")
	}
	if err := ValidateIdentifier(s.Name); err != nil {
		return err
	}
	seenCols := make(map[string]bool, len(s.Columns))
	for _, col := range s.Columns {
		if err := ValidateIdentifier(col.Name); err != nil {
			return err
		}
		lower := strings.ToLower(strings.TrimSpace(col.Name))
		if lower != "" {
			if seenCols[lower] {
				return fmt.Errorf("%w: column %q specified more than once in table %q", ErrDuplicateColumn, col.Name, s.Name)
			}
			seenCols[lower] = true
		}
	}
	fixedSize := s.FixedRowSize()
	if fixedSize > MaxInlineRowSize {
		return fmt.Errorf("%w: fixed column footprint %d bytes exceeds maximum in-row inline limit (%d bytes)", ErrInlineRowTooLarge, fixedSize, MaxInlineRowSize)
	}
	if len(s.Indexes) > MaxIndexesPerTable {
		return fmt.Errorf("%w: table %q has %d indexes (max %d)", ErrTooManyIndexes, s.Name, len(s.Indexes), MaxIndexesPerTable)
	}
	for i := range s.Indexes {
		if err := s.Indexes[i].Validate(s); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks that the index adheres to identifier length (128), column count (32),
// column existence/uniqueness, and maximum index key size (1,700 bytes) limits.
func (s *IndexSchema) Validate(tableSchema *TableSchema) error {
	if s == nil {
		return errors.New("nil index schema")
	}
	if err := ValidateIdentifier(s.Name); err != nil {
		return err
	}
	if len(s.Columns) > MaxIndexColumns {
		return fmt.Errorf("%w: index %q has %d columns (max %d)", ErrTooManyIndexColumns, s.Name, len(s.Columns), MaxIndexColumns)
	}
	if !s.IsExpression() {
		seenCols := make(map[string]bool, len(s.Columns))
		keyBytes := 0
		for _, colName := range s.Columns {
			lower := strings.ToLower(strings.TrimSpace(colName))
			if lower != "" {
				if seenCols[lower] {
					return fmt.Errorf("%w: column %q specified more than once in index %q", ErrDuplicateColumn, colName, s.Name)
				}
				seenCols[lower] = true
			}
			if tableSchema != nil {
				col, _, found := tableSchema.FindColumn(colName)
				if !found || col == nil {
					return fmt.Errorf("%w: %s on table %s", ErrColumnNotFound, colName, tableSchema.Name)
				}
				switch col.Type {
				case TypeInt, TypeFloat, TypeDateTime, TypeDate, TypeTime:
					keyBytes += 8
				case TypeReal:
					keyBytes += 4
				case TypeBool:
					keyBytes += 1
				case TypeUUID, TypeDecimal:
					keyBytes += 16
				case TypeVarChar, TypeBinary, TypeJSON:
					if col.Precision < 0 {
						return fmt.Errorf("%w: column %q in index %q is of a LOB/MAX type invalid for index key", ErrIndexKeyTooLarge, col.Name, s.Name)
					}
					if col.Precision > 0 {
						n := col.Precision
						if strings.EqualFold(col.RawType, "NCHAR") || strings.EqualFold(col.RawType, "NVARCHAR") {
							n *= 2
						}
						keyBytes += n
					}
				}
			}
		}
		if keyBytes > MaxIndexKeyBytes {
			return fmt.Errorf("%w: index %q key size %d bytes exceeds maximum limit (%d bytes)", ErrIndexKeyTooLarge, s.Name, keyBytes, MaxIndexKeyBytes)
		}
	}
	if tableSchema != nil {
		for _, colName := range s.IncludeCols {
			if _, _, found := tableSchema.FindColumn(colName); !found {
				return fmt.Errorf("%w: %s on table %s", ErrColumnNotFound, colName, tableSchema.Name)
			}
		}
	}
	return nil
}


// TableURI returns the WiredTiger table URI ("table:<physical_name>").
func (s *TableSchema) TableURI() string {
	if s == nil {
		return ""
	}
	if s.PhysicalName != "" {
		return "table:" + s.PhysicalName
	}
	return "table:" + s.Name
}

// IndexFileURI returns the WiredTiger URI of the single multiplexed secondary index file
// shared by every index of this table ("table:_idx_<physical_name>"). Individual indexes are
// separated inside that file by IndexSchema.KeyPrefix (ARCH-WT-FILE-LAYOUT-1).
func (s *TableSchema) IndexFileURI() string {
	if s == nil {
		return ""
	}
	return "table:_idx_" + s.physicalBase()
}

// LegacyIndexURI returns the pre-multiplexing per-index file URI
// ("table:_idx_<physical_name>_<index_name>"). It exists only for startup migration.
func (s *TableSchema) LegacyIndexURI(indexName string) string {
	if s == nil {
		return ""
	}
	return fmt.Sprintf("table:_idx_%s_%s", s.physicalBase(), indexName)
}

func (s *TableSchema) physicalBase() string {
	if s.PhysicalName != "" {
		return s.PhysicalName
	}
	return s.Name
}

// KeyPrefix returns the namespace prefix ("<lower(index name)>\x00") under which all entries of
// this index are stored inside the table's multiplexed secondary index file.
func (s *IndexSchema) KeyPrefix() string {
	return strings.ToLower(s.Name) + "\x00"
}

// ParsePKValue parses a string representation into a Value of the specified DataType.
func ParsePKValue(s string, dt DataType) Value {
	switch dt {
	case TypeInt:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return Value{Type: TypeInt, IVal: n, FVal: float64(n)}
		}
		return Value{Type: TypeInt}
	case TypeFloat:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return Value{Type: TypeFloat, FVal: f, IVal: int64(f)}
		}
		return Value{Type: TypeFloat}
	case TypeReal:
		if f, err := strconv.ParseFloat(s, 32); err == nil {
			return RealValue(float32(f))
		}
		return Value{Type: TypeReal}
	case TypeDecimal:
		return DecimalValue(s)
	case TypeBool:
		return BoolValue(strings.EqualFold(s, "true") || s == "1")
	case TypeDateTime:
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return DateTimeValue(t)
		}
		return StringValue(s)
	case TypeDate:
		return DateValueFromString(s)
	case TypeTime:
		return TimeValueFromString(s)
	case TypeUUID:
		return UUIDValue(s)
	case TypeBinary:
		return BinaryValueFromString(s)
	default:
		return StringValue(s)
	}
}

func (s *TableSchema) IsTemporary() bool {
	return s != nil && IsTemporaryTable(s.Name)
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
		if idx := strings.Index(str, " m="); idx != -1 {
			str = strings.TrimSpace(str[:idx])
		}
		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02 15:04:05.999999999Z07:00",
			"2006-01-02 15:04:05.999999999Z",
			"2006-01-02 15:04:05.999999999 -0700 MST",
			"2006-01-02 15:04:05.999999999 -0700",
			"2006-01-02 15:04:05.999999999-07:00",
			"2006-01-02 15:04:05.999999999 -07:00",
			"2006-01-02 15:04:05.999999999-07",
			"2006-01-02 15:04:05.999999999",
			"2006-01-02 15:04:05.999999 -0700 MST",
			"2006-01-02 15:04:05.999999 -0700",
			"2006-01-02 15:04:05.999999-07:00",
			"2006-01-02 15:04:05.999999 -07:00",
			"2006-01-02 15:04:05.999999-07",
			"2006-01-02 15:04:05.999999",
			"2006-01-02 15:04:05Z07:00",
			"2006-01-02 15:04:05Z",
			"2006-01-02 15:04:05 -0700 MST",
			"2006-01-02 15:04:05 -0700",
			"2006-01-02 15:04:05-07:00",
			"2006-01-02 15:04:05 -07:00",
			"2006-01-02 15:04:05-07",
			"2006-01-02 15:04:05",
			"2006-01-02 15:04:05.000000",
			"2006-01-02 15:04:05.000",
			"2006-01-02T15:04:05",
			"2006-01-02T15:04:05Z07:00",
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

func (s *TableSchema) HasPrimaryKey() bool {
	if s == nil {
		return false
	}
	for i := range s.Columns {
		if s.Columns[i].PrimaryKey {
			return true
		}
	}
	return false
}

func (s *TableSchema) EnsurePrimaryKey(syntheticColName string) {
	if s == nil || s.HasPrimaryKey() {
		return
	}
	if syntheticColName == "" {
		syntheticColName = "_row_id"
	}
	s.Columns = append(s.Columns, Column{
		Name:       syntheticColName,
		Type:       TypeInt,
		PrimaryKey: true,
		Nullable:   false,
		Identity:   true,
		Hidden:     true,
	})
}

func (s *TableSchema) IsCompositePK() bool {
	if s == nil {
		return false
	}
	count := 0
	for i := range s.Columns {
		if s.Columns[i].PrimaryKey {
			count++
			if count > 1 {
				return true
			}
		}
	}
	return false
}

func (s *TableSchema) ResolveRecordPK(rec Record) (Value, bool) {
	if s == nil || rec == nil {
		return Value{}, false
	}
	if s.IsCompositePK() {
		var parts []string
		for i := range s.Columns {
			if s.Columns[i].PrimaryKey {
				v, ok := rec.Get(s.Columns[i].Name)
				if !ok || v.IsNull {
					return Value{}, false
				}
				parts = append(parts, v.String())
			}
		}
		if len(parts) == 0 {
			return Value{}, false
		}
		return StringValue(strings.Join(parts, "\x1f")), true
	}
	pkCol, err := s.PrimaryKeyColumn()
	if err != nil || pkCol == nil {
		return Value{}, false
	}
	return rec.Get(pkCol.Name)
}

// ResolveRowPK extracts a single or composite primary key Value from a schema-aligned Row.
func (s *TableSchema) ResolveRowPK(row Row) (Value, bool) {
	if s == nil {
		return Value{}, false
	}
	if s.IsCompositePK() {
		var parts []string
		for i := range s.Columns {
			if s.Columns[i].PrimaryKey {
				if i >= len(row.Values) || row.Values[i].IsNull {
					return Value{}, false
				}
				parts = append(parts, row.Values[i].String())
			}
		}
		if len(parts) == 0 {
			return Value{}, false
		}
		return StringValue(strings.Join(parts, "\x1f")), true
	}
	pkIdx := s.PrimaryKeyIndex()
	if pkIdx < 0 || pkIdx >= len(row.Values) {
		return Value{}, false
	}
	return row.Values[pkIdx], true
}

func (s *TableSchema) PrimaryKeyIndex() int {
	for i := range s.Columns {
		if s.Columns[i].PrimaryKey {
			return i
		}
	}
	return -1
}

func (s *TableSchema) PrimaryKeyColumn() (*Column, error) {
	for i := range s.Columns {
		if s.Columns[i].PrimaryKey {
			return &s.Columns[i], nil
		}
	}
	return nil, fmt.Errorf("no primary key defined on table %q", s.Name)
}

// PrimaryKeyColumns returns all primary key columns defined on the table schema.
func (s *TableSchema) PrimaryKeyColumns() []*Column {
	if s == nil {
		return nil
	}
	var cols []*Column
	for i := range s.Columns {
		if s.Columns[i].PrimaryKey {
			cols = append(cols, &s.Columns[i])
		}
	}
	return cols
}

// RowVersionColumn returns the ROWVERSION column if defined on the table.
func (s *TableSchema) RowVersionColumn() (*Column, int, bool) {
	if s == nil {
		return nil, -1, false
	}
	for i := range s.Columns {
		if s.Columns[i].IsRowVersion {
			return &s.Columns[i], i, true
		}
	}
	return nil, -1, false
}

// IdentityColumn returns the IDENTITY column if defined on the table.
func (s *TableSchema) IdentityColumn() (*Column, bool) {
	if s == nil {
		return nil, false
	}
	for i := range s.Columns {
		if s.Columns[i].Identity {
			return &s.Columns[i], true
		}
	}
	return nil, false
}

func (s *TableSchema) FindColumn(name string) (*Column, int, bool) {
	for i := range s.Columns {
		if sqlutil.EqualFoldIdent(s.Columns[i].Name, name) {
			return &s.Columns[i], i, true
		}
	}
	return nil, -1, false
}

func (s *TableSchema) FindIndex(name string) (*IndexSchema, int, bool) {
	for i := range s.Indexes {
		if sqlutil.EqualFoldIdent(s.Indexes[i].Name, name) {
			return &s.Indexes[i], i, true
		}
	}
	return nil, -1, false
}

// FindActiveIndex finds a non-disabled index by name.
func (s *TableSchema) FindActiveIndex(name string) (*IndexSchema, int, bool) {
	for i := range s.Indexes {
		if s.Indexes[i].Disabled {
			continue
		}
		if sqlutil.EqualFoldIdent(s.Indexes[i].Name, name) {
			return &s.Indexes[i], i, true
		}
	}
	return nil, -1, false
}

// FindIndexByColumn finds an index whose leading indexed column matches colName.
func (s *TableSchema) FindIndexByColumn(colName string) (*IndexSchema, bool) {
	for i := range s.Indexes {
		if s.Indexes[i].Disabled {
			continue
		}
		if len(s.Indexes[i].Columns) > 0 && strings.EqualFold(s.Indexes[i].Columns[0], colName) {
			return &s.Indexes[i], true
		}
	}
	return nil, false
}

// FindExpressionIndex finds a functional expression index on the specified column and JSON path.
func (s *TableSchema) FindExpressionIndex(colName, jsonPath string) (*IndexSchema, bool) {
	normPath := strings.TrimSpace(jsonPath)
	if !strings.HasPrefix(normPath, "$") {
		if strings.HasPrefix(normPath, "[") {
			normPath = "$" + normPath
		} else {
			normPath = "$." + normPath
		}
	}
	for i := range s.Indexes {
		idx := &s.Indexes[i]
		if idx.Disabled || !idx.IsExpression() {
			continue
		}
		targetCol := idx.ExprColumn
		if targetCol == "" && len(idx.Columns) > 0 {
			targetCol = idx.Columns[0]
		}
		if !strings.EqualFold(targetCol, colName) {
			continue
		}
		idxNormPath := strings.TrimSpace(idx.ExprPath)
		if !strings.HasPrefix(idxNormPath, "$") {
			if strings.HasPrefix(idxNormPath, "[") {
				idxNormPath = "$" + idxNormPath
			} else {
				idxNormPath = "$." + idxNormPath
			}
		}
		if strings.EqualFold(idxNormPath, normPath) {
			return idx, true
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
		if idx.Disabled {
			continue
		}
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
	Type      DataType
	IsNull    bool
	IVal      int64
	SVal      string
	FVal      float64
	BVal      bool
	TVal      time.Time
	DVal      Decimal
	BinVal    string
	Precision int
}

// Bytes returns the raw byte slice for TypeBinary values.
func (v Value) Bytes() []byte {
	return []byte(v.BinVal)
}

// Interface converts a storage.Value into a standard Go any value.
func (v Value) Interface() any {
	if v.IsNull {
		return nil
	}
	switch v.Type {
	case TypeInt:
		return v.IVal
	case TypeFloat:
		return v.FVal
	case TypeReal:
		return float32(v.FVal)
	case TypeBool:
		return v.BVal
	case TypeDateTime, TypeDate:
		return v.TVal
	case TypeDecimal:
		return v.DVal
	case TypeBinary:
		return v.BinVal
	default:
		return v.SVal
	}
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

func RealValue(v float32) Value {
	return Value{Type: TypeReal, FVal: float64(v), IVal: int64(v)}
}

func BoolValue(v bool) Value {
	return Value{Type: TypeBool, BVal: v}
}

// BoolValueFromAny converts an arbitrary Value to a TypeBool Value.
func BoolValueFromAny(v Value) Value {
	if v.IsNull {
		return NullValue(TypeBool)
	}
	switch v.Type {
	case TypeBool:
		return v
	case TypeInt:
		return BoolValue(v.IVal != 0)
	case TypeFloat, TypeReal:
		return BoolValue(v.FVal != 0)
	case TypeDecimal:
		return BoolValue(!v.Decimal().IsZero())
	case TypeVarChar, TypeJSON:
		s := strings.Trim(strings.TrimSpace(v.SVal), "'\"")
		return BoolValue(strings.EqualFold(s, "true") || s == "1" || strings.EqualFold(s, "t"))
	default:
		return BoolValue(false)
	}
}

func DateTimeValue(v time.Time) Value {
	return Value{
		Type: TypeDateTime,
		TVal: v,
		SVal: v.Format("2006-01-02 15:04:05"),
		IVal: v.Unix(),
	}
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

// ContainsNull reports whether the value is NULL or, for composite index keys, contains any NULL component.
func (v Value) ContainsNull() bool {
	if v.IsNull {
		return true
	}
	if v.Type == TypeVarChar && (strings.Contains(v.SVal, "\x00NULL") || v.SVal == "NULL") {
		return true
	}
	return false
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
	case TypeReal:
		bits := math.Float32bits(float32(v.FVal))
		if (bits & 0x80000000) != 0 {
			bits = ^bits
		} else {
			bits ^= 0x80000000
		}
		var b [8]byte
		const hexDigits = "0123456789abcdef"
		b[0] = hexDigits[bits>>28&0xf]
		b[1] = hexDigits[bits>>24&0xf]
		b[2] = hexDigits[bits>>20&0xf]
		b[3] = hexDigits[bits>>16&0xf]
		b[4] = hexDigits[bits>>12&0xf]
		b[5] = hexDigits[bits>>8&0xf]
		b[6] = hexDigits[bits>>4&0xf]
		b[7] = hexDigits[bits&0xf]
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
	if v.Type == TypeFloat || v.Type == TypeReal {
		return NewDecimalFromFloat(v.FVal, 4)
	}
	if v.SVal != "" {
		if d, err := ParseDecimal(v.SVal); err == nil {
			return d
		}
	}
	return ZeroDecimal(0)
}

var smallIntStrs [100001]string

func init() {
	for i := 0; i <= 100000; i++ {
		smallIntStrs[i] = strconv.Itoa(i)
	}
}

func (v Value) String() string {
	if v.IsNull {
		return "NULL"
	}
	switch v.Type {
	case TypeInt:
		if v.IVal >= 0 && v.IVal <= 100000 {
			return smallIntStrs[v.IVal]
		}
		return strconv.FormatInt(v.IVal, 10)
	case TypeVarChar, TypeJSON:
		return v.SVal
	case TypeFloat:
		return fmt.Sprintf("%f", v.FVal)
	case TypeReal:
		return strconv.FormatFloat(v.FVal, 'g', -1, 32)
	case TypeDecimal:
		if v.DVal.value != nil {
			return v.DVal.String()
		}
		if v.SVal != "" {
			return v.SVal
		}
		return "0"
	case TypeBool:
		return strconv.FormatBool(v.BVal)
	case TypeDateTime:
		return v.TVal.Format(time.RFC3339)
	case TypeDate:
		return v.TVal.Format("2006-01-02")
	case TypeTime:
		if v.SVal != "" {
			return v.SVal
		}
		if v.TVal.Nanosecond() > 0 {
			return v.TVal.Format("15:04:05.999999999")
		}
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
	// Fast-path: non-null integer equality (highest frequency in point lookups)
	if v.Type == TypeInt && other.Type == TypeInt && !v.IsNull && !other.IsNull {
		return v.IVal == other.IVal
	}

	if v.IsNull && other.IsNull {
		return true
	}
	if v.IsNull != other.IsNull {
		return false
	}

	// Heterogeneous type coercion via Type Promotion Lattice
	if v.Type != other.Type {
		if c1, c2, ok := CoerceBinaryValues(v, other); ok {
			v, other = c1, c2
		} else {
			return false
		}
	}

	switch v.Type {
	case TypeInt:
		return v.IVal == other.IVal
	case TypeVarChar:
		return strings.TrimRight(v.SVal, " ") == strings.TrimRight(other.SVal, " ")
	case TypeJSON:
		return v.SVal == other.SVal
	case TypeFloat, TypeReal:
		return v.FVal == other.FVal || math.Abs(v.FVal-other.FVal) < 1e-9
	case TypeDecimal:
		return v.Decimal().Equal(other.Decimal())
	case TypeBool:
		return v.BVal == other.BVal
	case TypeDateTime, TypeDate:
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
	// Fast-path: non-null integer comparison (highest frequency in OLTP indexes)
	if v.Type == TypeInt && other.Type == TypeInt && !v.IsNull && !other.IsNull {
		if v.IVal < other.IVal {
			return -1
		} else if v.IVal > other.IVal {
			return 1
		}
		return 0
	}

	if v.IsNull && other.IsNull {
		return 0
	}
	if v.IsNull {
		return -1
	}
	if other.IsNull {
		return 1
	}
	// Heterogeneous type coercion via Type Promotion Lattice
	if v.Type != other.Type {
		if c1, c2, ok := CoerceBinaryValues(v, other); ok {
			v, other = c1, c2
		}
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
	case TypeVarChar:
		s1 := strings.TrimRight(v.SVal, " ")
		s2 := strings.TrimRight(other.SVal, " ")
		if s1 < s2 {
			return -1
		} else if s1 > s2 {
			return 1
		}
		return 0
	case TypeJSON:
		if v.SVal < other.SVal {
			return -1
		} else if v.SVal > other.SVal {
			return 1
		}
		return 0
	case TypeFloat, TypeReal:
		return CompareFloatEpsilon(v.FVal, other.FVal)
	case TypeDecimal:
		return v.Decimal().Cmp(other.Decimal())
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
	Err() error
}

// RawTupleCursor is an optional interface implemented by cursors that support zero-copy binary tuple access.
type RawTupleCursor interface {
	Cursor
	RawTuple() ([]byte, bool)
}

// ArenaRowCursor is an optional interface implemented by cursors that can decode
// rows into a caller-managed Arena without altering Cursor.Row() ownership semantics.
type ArenaRowCursor interface {
	Cursor
	RowWithArena(arena *Arena) (Row, error)
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
	ListTables() ([]*TableSchema, error)
	Validate() error
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
	TruncateTables(tableNames []string, cascade bool) error
	DropPartition(tableName, partitionName string) error
	GetPartitions(tableName string) ([]PartitionInfo, error)
	AddColumn(tableName string, col *Column) error
	RenameTable(oldName, newName string) error
	AlterTable(tableName string, mutator func(schema *TableSchema) error) error
}

// Syncer is an optional interface implemented by storage engines that support
// explicit disk synchronization of pending write-ahead log buffers ([RULE:ExplicitError], Scope-129).
type Syncer interface {
	Sync() error
}

