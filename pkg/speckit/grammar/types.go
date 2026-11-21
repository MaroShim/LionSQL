package grammar

import (
	"fmt"
	"math/rand"
	"strings"
)

// SQLType represents a SQL data type for fuzzing and generative testing.
type SQLType string

const (
	TypeInt      SQLType = "INT"
	TypeBigInt   SQLType = "BIGINT"
	TypeSmallInt SQLType = "SMALLINT"
	TypeTinyInt  SQLType = "TINYINT"
	TypeFloat    SQLType = "FLOAT"
	TypeDecimal  SQLType = "DECIMAL(10,2)"
	TypeVarChar  SQLType = "VARCHAR(50)"
	TypeBit      SQLType = "BIT"
	TypeDateTime SQLType = "DATETIME"
)

// AllSQLTypes lists all supported types for spec fuzzing.
var AllSQLTypes = []SQLType{
	TypeInt,
	TypeBigInt,
	TypeSmallInt,
	TypeTinyInt,
	TypeFloat,
	TypeDecimal,
	TypeVarChar,
	TypeBit,
	TypeDateTime,
}

// ColumnDef defines a column in a generated table.
type ColumnDef struct {
	Name     string
	Type     SQLType
	IsPK     bool
	Nullable bool
}

// TableSchema defines a generated table schema.
type TableSchema struct {
	Name    string
	Columns []ColumnDef
}

// PKColumn returns the primary key column.
func (t TableSchema) PKColumn() ColumnDef {
	for _, c := range t.Columns {
		if c.IsPK {
			return c
		}
	}
	return t.Columns[0]
}

// NonPKColumns returns all columns except primary key.
func (t TableSchema) NonPKColumns() []ColumnDef {
	var res []ColumnDef
	for _, c := range t.Columns {
		if !c.IsPK {
			res = append(res, c)
		}
	}
	return res
}

// GenerateTableSchema generates a randomized TableSchema.
func GenerateTableSchema(rng *rand.Rand, tableName string, numCols int) TableSchema {
	if numCols < 2 {
		numCols = 2
	}
	ts := TableSchema{
		Name: tableName,
		Columns: []ColumnDef{
			{Name: "id", Type: TypeInt, IsPK: true, Nullable: false},
		},
	}

	for i := 1; i < numCols; i++ {
		t := AllSQLTypes[rng.Intn(len(AllSQLTypes))]
		ts.Columns = append(ts.Columns, ColumnDef{
			Name:     fmt.Sprintf("c%d", i),
			Type:     t,
			IsPK:     false,
			Nullable: rng.Float64() < 0.7, // 70% nullable to stress 3VL
		})
	}
	return ts
}

// GenerateCreateTableDDL generates the CREATE TABLE DDL string.
func (t TableSchema) GenerateCreateTableDDL() string {
	var cols []string
	for _, c := range t.Columns {
		s := fmt.Sprintf("%s %s", c.Name, c.Type)
		if c.IsPK {
			s += " PRIMARY KEY"
		} else if !c.Nullable {
			s += " NOT NULL"
		}
		cols = append(cols, s)
	}
	return fmt.Sprintf("CREATE TABLE %s (%s);", t.Name, strings.Join(cols, ", "))
}

// SampleValues returns a slice of boundary and representative values for a given type.
func SampleValues(t SQLType, rng *rand.Rand, nullable bool) string {
	if nullable && rng.Float64() < 0.25 {
		return "NULL"
	}
	switch t {
	case TypeInt:
		vals := []string{"0", "1", "-1", "42", "2147483647", "-2147483648", "100"}
		return vals[rng.Intn(len(vals))]
	case TypeBigInt:
		vals := []string{"0", "1", "-1", "9223372036854775807", "-9223372036854775808", "1000000"}
		return vals[rng.Intn(len(vals))]
	case TypeSmallInt:
		vals := []string{"0", "1", "-1", "32767", "-32768", "255"}
		return vals[rng.Intn(len(vals))]
	case TypeTinyInt:
		vals := []string{"0", "1", "42", "255", "128"}
		return vals[rng.Intn(len(vals))]
	case TypeFloat:
		vals := []string{"0.0", "1.0", "-1.0", "3.14159", "0.00001", "-999.99"}
		return vals[rng.Intn(len(vals))]
	case TypeDecimal:
		vals := []string{"0.00", "1.00", "-1.00", "99.99", "0.01", "-0.01", "1234.56"}
		return vals[rng.Intn(len(vals))]
	case TypeVarChar:
		vals := []string{"''", "'a'", "'A'", "'null'", "'NULL'", "'foo'", "'123'", "'hello world'"}
		return vals[rng.Intn(len(vals))]
	case TypeBit:
		vals := []string{"0", "1"}
		return vals[rng.Intn(len(vals))]
	case TypeDateTime:
		vals := []string{
			"'2026-01-01 00:00:00'",
			"'2020-02-29 12:00:00'", // Leap year
			"'1900-01-01 00:00:00'",
			"'2030-12-31 23:59:59'",
		}
		return vals[rng.Intn(len(vals))]
	default:
		return "NULL"
	}
}

// GenerateInsertDML generates batch INSERT statements for a table.
func (t TableSchema) GenerateInsertDML(rng *rand.Rand, numRows int) []string {
	var stmts []string
	for i := 1; i <= numRows; i++ {
		var vals []string
		for _, col := range t.Columns {
			if col.IsPK {
				vals = append(vals, fmt.Sprintf("%d", i))
			} else {
				vals = append(vals, SampleValues(col.Type, rng, col.Nullable))
			}
		}
		stmt := fmt.Sprintf("INSERT INTO %s VALUES (%s);", t.Name, strings.Join(vals, ", "))
		stmts = append(stmts, stmt)
	}
	return stmts
}
