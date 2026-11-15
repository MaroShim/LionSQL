package tds

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"lionsql/pkg/storage"
)

// withOutputParams wraps extraTokens so that RETURNVALUE (0xAC) tokens for every
// OUTPUT-flagged RPC parameter are appended after any tokens the caller already emits.
// finalVars holds the callee's final variable state keyed by parameter name.
// It returns extraTokens unchanged when the call has no OUTPUT parameters.
func withOutputParams(extraTokens func(*bytes.Buffer), params []RPCParam, procParamNames []string, finalVars map[string]any) func(*bytes.Buffer) {
	hasOutput := false
	for _, p := range params {
		if p.IsOutput {
			hasOutput = true
			break
		}
	}
	if !hasOutput {
		return extraTokens
	}
	return func(buf *bytes.Buffer) {
		if extraTokens != nil {
			extraTokens(buf)
		}
		for i, p := range params {
			if !p.IsOutput {
				continue
			}
			name := p.Name
			if name == "" && i < len(procParamNames) {
				name = procParamNames[i]
			}
			val := unwrapTdsOutputValue(lookupFinalVar(finalVars, name))
			col := outputColumnInfo(p, val)
			if col.Type == TypeNVarChar && val != nil {
				val = outputStringValue(val)
			}
			WriteReturnValue(buf, uint16(i), name, col, val)
		}
	}
}

func unwrapTdsOutputValue(v any) any {
	if isNilTdsValue(v) {
		return nil
	}
	switch sv := v.(type) {
	case storage.Value:
		if sv.Type == storage.TypeBinary {
			return sv.Bytes()
		}
		return sv.Interface()
	case *storage.Value:
		if sv == nil {
			return nil
		}
		if sv.Type == storage.TypeBinary {
			return sv.Bytes()
		}
		return sv.Interface()
	default:
		return v
	}
}

// rpcHasNamedParam reports whether any RPC parameter carries an explicit name.
func rpcHasNamedParam(params []RPCParam) bool {
	for _, p := range params {
		if p.Name != "" {
			return true
		}
	}
	return false
}

// lookupFinalVar finds a parameter's final value by name, tolerating the '@' prefix and case.
func lookupFinalVar(vars map[string]any, name string) any {
	clean := strings.TrimPrefix(name, "@")
	if v, ok := vars[clean]; ok {
		return v
	}
	if v, ok := vars["@"+clean]; ok {
		return v
	}
	for k, v := range vars {
		if strings.EqualFold(strings.TrimPrefix(k, "@"), clean) {
			return v
		}
	}
	return nil
}

// outputColumnInfo chooses the TDS type used to return an OUTPUT parameter.
// The integer width follows the type the client declared so the driver can bind it back unchanged.
func outputColumnInfo(p RPCParam, val any) ColumnInfo {
	col := ColumnInfo{Name: p.Name, Nullable: true}
	intSize := declaredIntSize(p)
	switch v := val.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		col.Type, col.Size = TypeIntN, intSize
	case float32, float64:
		col.Type, col.Size = TypeFltN, 8
	case bool:
		col.Type, col.Size = TypeBitN, 1
	case []byte:
		col.Type, col.Size = TypeBigVarBin, len(v)
		if len(v) > 8000 {
			col.IsPLP, col.Size = true, -1
		}
	case time.Time:
		col.Type, col.Size = TypeDateTimeN, 8
	case nil:
		switch p.Type {
		case TypeInt1, TypeInt2, TypeInt4, TypeInt8, TypeIntN:
			col.Type, col.Size = TypeIntN, intSize
		case TypeBit, TypeBitN:
			col.Type, col.Size = TypeBitN, 1
		case TypeFlt4, TypeFlt8, TypeFltN:
			col.Type, col.Size = TypeFltN, 8
		default:
			col.Type, col.Size = TypeNVarChar, 4000
		}
	default:
		s := fmt.Sprint(v)
		col.Type, col.Size = TypeNVarChar, 4000
		if len([]rune(s)) > 4000 {
			col.IsPLP, col.Size = true, -1
		}
	}
	return col
}

// declaredIntSize returns the byte width of the integer type the client declared for p (default 8).
func declaredIntSize(p RPCParam) int {
	switch p.Type {
	case TypeInt1:
		return 1
	case TypeInt2:
		return 2
	case TypeInt4:
		return 4
	case TypeInt8:
		return 8
	}
	switch p.Value.(type) {
	case int8, uint8:
		return 1
	case int16, uint16:
		return 2
	case int32, uint32:
		return 4
	}
	return 8
}

// outputStringValue renders values without a native TDS mapping (for example DECIMAL) as text.
func outputStringValue(val any) string {
	if s, ok := val.(string); ok {
		return s
	}
	return fmt.Sprint(val)
}
