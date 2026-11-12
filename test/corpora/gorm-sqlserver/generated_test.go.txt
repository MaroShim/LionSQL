package sqlserver

import (
	"testing"

	"gorm.io/gorm/schema"
)

func TestDataTypeOfGeneratedColumn(t *testing.T) {
	dialector := Dialector{Config: &Config{}}
	tests := []struct {
		name  string
		field *schema.Field
		want  string
	}{
		{
			// SQL Server infers a computed column's type from the expression,
			// so the column type is omitted and the value is PERSISTED (stored).
			name:  "computed column renders a PERSISTED computed column",
			field: &schema.Field{DataType: schema.Float, TagSettings: map[string]string{"GENERATED": "price * quantity"}},
			want:  "AS (price * quantity) PERSISTED",
		},
		{
			name:  "computed expression keeps commas",
			field: &schema.Field{DataType: schema.String, TagSettings: map[string]string{"GENERATED": "concat(first_name, last_name)"}},
			want:  "AS (concat(first_name, last_name)) PERSISTED",
		},
		{
			// `identity` is reserved for identity columns, which SQL Server
			// renders through its native IDENTITY rather than a computed column.
			name:  "identity keyword is not treated as a computed column",
			field: &schema.Field{DataType: schema.Int, Size: 64, AutoIncrement: true, TagSettings: map[string]string{"GENERATED": "identity"}},
			want:  "bigint IDENTITY(1,1)",
		},
		{
			name:  "identity with an explicit mode is also reserved",
			field: &schema.Field{DataType: schema.Int, Size: 64, AutoIncrement: true, TagSettings: map[string]string{"GENERATED": "identity always"}},
			want:  "bigint IDENTITY(1,1)",
		},
		{
			name:  "a bare generated tag is ignored",
			field: &schema.Field{DataType: schema.Float, TagSettings: map[string]string{"GENERATED": "GENERATED"}},
			want:  "float",
		},
		{
			name:  "a lowercase generated expression is not mistaken for a bare tag",
			field: &schema.Field{DataType: schema.Float, TagSettings: map[string]string{"GENERATED": "generated"}},
			want:  "AS (generated) PERSISTED",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dialector.DataTypeOf(tt.field); got != tt.want {
				t.Errorf("DataTypeOf() = %q, want %q", got, tt.want)
			}
		})
	}
}
