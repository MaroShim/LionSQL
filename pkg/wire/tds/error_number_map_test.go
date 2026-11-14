package tds

import (
	"errors"
	"fmt"
	"testing"

	"lionsql/pkg/storage"
)

type customTdsError struct {
	number   int32
	severity byte
	state    byte
	msg      string
}

func (e *customTdsError) Error() string {
	return e.msg
}

func (e *customTdsError) TdsErrorInfo() (int32, byte, byte) {
	return e.number, e.severity, e.state
}

func TestExtractTdsErrorInfo(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantNumber    int32
		wantState     byte
		wantSeverity  byte
		wantMsgSubstr string
	}{
		{
			name:         "nil error",
			err:          nil,
			wantNumber:   0,
			wantState:    0,
			wantSeverity: 0,
		},
		{
			name: "custom tdsErrorDescriber",
			err: &customTdsError{
				number:   55555,
				severity: 18,
				state:    4,
				msg:      "custom failure",
			},
			wantNumber:    55555,
			wantState:     4,
			wantSeverity:  18,
			wantMsgSubstr: "custom failure",
		},
		{
			name:          "storage.ErrKeyExists (Primary Key)",
			err:           fmt.Errorf("duplicate key value: %w", storage.ErrKeyExists),
			wantNumber:    2627,
			wantState:     1,
			wantSeverity:  14,
			wantMsgSubstr: "duplicate key value",
		},
		{
			name:          "storage.ErrUniqueViolation (Unique Index)",
			err:           fmt.Errorf("unique index violation on idx_email: %w", storage.ErrUniqueViolation),
			wantNumber:    2601,
			wantState:     1,
			wantSeverity:  14,
			wantMsgSubstr: "idx_email",
		},
		{
			name:          "storage.ErrForeignKeyViolation",
			err:           fmt.Errorf("fk conflict on orders: %w", storage.ErrForeignKeyViolation),
			wantNumber:    547,
			wantState:     0,
			wantSeverity:  16,
			wantMsgSubstr: "orders",
		},
		{
			name:          "storage.ErrCheckViolation",
			err:           fmt.Errorf("check constraint failed on age: %w", storage.ErrCheckViolation),
			wantNumber:    547,
			wantState:     0,
			wantSeverity:  16,
			wantMsgSubstr: "age",
		},
		{
			name:          "storage.ErrNotNullViolation",
			err:           fmt.Errorf("null not allowed on col name: %w", storage.ErrNotNullViolation),
			wantNumber:    515,
			wantState:     2,
			wantSeverity:  16,
			wantMsgSubstr: "name",
		},
		{
			name:          "storage.ErrTableNotFound",
			err:           fmt.Errorf("cannot find object: %w", storage.ErrTableNotFound),
			wantNumber:    208,
			wantState:     1,
			wantSeverity:  16,
			wantMsgSubstr: "cannot find object",
		},
		{
			name:          "storage.ErrColumnNotFound",
			err:           fmt.Errorf("invalid column: %w", storage.ErrColumnNotFound),
			wantNumber:    207,
			wantState:     1,
			wantSeverity:  16,
			wantMsgSubstr: "invalid column",
		},
		{
			name:          "storage.ErrTableExists",
			err:           fmt.Errorf("table already created: %w", storage.ErrTableExists),
			wantNumber:    2714,
			wantState:     6,
			wantSeverity:  16,
			wantMsgSubstr: "table already created",
		},
		{
			name:          "storage.ErrIndexExists",
			err:           fmt.Errorf("index already exists: %w", storage.ErrIndexExists),
			wantNumber:    1913,
			wantState:     1,
			wantSeverity:  16,
			wantMsgSubstr: "index already exists",
		},
		{
			name:          "storage.ErrDependentObjectsExist",
			err:           fmt.Errorf("cannot drop table with dependencies: %w", storage.ErrDependentObjectsExist),
			wantNumber:    3726,
			wantState:     1,
			wantSeverity:  16,
			wantMsgSubstr: "dependencies",
		},
		{
			name:          "storage.ErrSerializationFailure (Deadlock / Conflict)",
			err:           fmt.Errorf("concurrent update conflict: %w", storage.ErrSerializationFailure),
			wantNumber:    1205,
			wantState:     51,
			wantSeverity:  13,
			wantMsgSubstr: "concurrent update conflict",
		},
		{
			name:          "storage.ErrReadOnlyTransaction",
			err:           fmt.Errorf("cannot mutate: %w", storage.ErrReadOnlyTransaction),
			wantNumber:    3906,
			wantState:     1,
			wantSeverity:  16,
			wantMsgSubstr: "cannot mutate",
		},
		{
			name:          "fallback string: divide by zero",
			err:           errors.New("divide by zero error encountered in expression"),
			wantNumber:    8134,
			wantState:     1,
			wantSeverity:  16,
			wantMsgSubstr: "divide by zero",
		},
		{
			name:          "fallback string: arithmetic overflow",
			err:           errors.New("arithmetic overflow converting float to int"),
			wantNumber:    8115,
			wantState:     1,
			wantSeverity:  16,
			wantMsgSubstr: "arithmetic overflow",
		},
		{
			name:          "fallback string: syntax error",
			err:           errors.New("syntax error near SELECT"),
			wantNumber:    102,
			wantState:     1,
			wantSeverity:  15,
			wantMsgSubstr: "syntax error",
		},
		{
			name:          "fallback string: proc not found",
			err:           errors.New("could not find stored procedure 'dbo.calc'"),
			wantNumber:    2812,
			wantState:     62,
			wantSeverity:  16,
			wantMsgSubstr: "could not find stored procedure",
		},
		{
			name:          "unknown generic error fallback",
			err:           errors.New("some unexpected internal error"),
			wantNumber:    50000,
			wantState:     1,
			wantSeverity:  16,
			wantMsgSubstr: "some unexpected internal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			num, state, sev, msg := extractTdsErrorInfo(tt.err)
			if num != tt.wantNumber {
				t.Errorf("Number = %d, want %d", num, tt.wantNumber)
			}
			if state != tt.wantState {
				t.Errorf("State = %d, want %d", state, tt.wantState)
			}
			if sev != tt.wantSeverity {
				t.Errorf("Severity = %d, want %d", sev, tt.wantSeverity)
			}
			if tt.wantMsgSubstr != "" && !errors.Is(tt.err, nil) {
				if len(msg) == 0 {
					t.Errorf("expected non-empty message containing %q", tt.wantMsgSubstr)
				}
			}
		})
	}
}
