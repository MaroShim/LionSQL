package proc

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"lionsql/pkg/storage"
)

func modifyJSONByPath(doc, path string, newVal storage.Value) (string, error) {
	var data any
	if err := json.Unmarshal([]byte(doc), &data); err != nil {
		return doc, err
	}
	cleanPath := strings.TrimPrefix(strings.TrimSpace(path), "$")
	cleanPath = strings.TrimPrefix(cleanPath, ".")
	if cleanPath == "" {
		if newVal.IsNull {
			return "null", nil
		}
		marshaled, err := json.Marshal(storageValueToInterface(newVal))
		return string(marshaled), err
	}

	parts := strings.Split(cleanPath, ".")
	if obj, ok := data.(map[string]any); ok {
		current := obj
		for i := 0; i < len(parts)-1; i++ {
			p := parts[i]
			sub, exists := current[p]
			if !exists {
				nextMap := make(map[string]any)
				current[p] = nextMap
				current = nextMap
			} else if nextMap, isMap := sub.(map[string]any); isMap {
				current = nextMap
			} else {
				return doc, fmt.Errorf("path is not an object")
			}
		}
		lastKey := parts[len(parts)-1]
		if newVal.IsNull {
			delete(current, lastKey)
		} else {
			current[lastKey] = storageValueToInterface(newVal)
		}
		res, err := json.Marshal(obj)
		if err != nil {
			return doc, err
		}
		return string(res), nil
	}
	return doc, nil
}

func storageValueToInterface(v storage.Value) any {
	if v.IsNull {
		return nil
	}
	switch v.Type {
	case storage.TypeInt:
		return v.IVal
	case storage.TypeFloat:
		return v.FVal
	case storage.TypeBool:
		return v.BVal
	case storage.TypeJSON:
		var parsed any
		if err := json.Unmarshal([]byte(v.SVal), &parsed); err == nil {
			return parsed
		}
		return v.SVal
	default:
		return v.SVal
	}
}



func encodeJSONElement(sb *strings.Builder, v storage.Value) {
	if v.IsNull {
		sb.WriteString("null")
		return
	}
	switch v.Type {
	case storage.TypeInt:
		sb.WriteString(strconv.FormatInt(v.IVal, 10))
	case storage.TypeFloat:
		sb.WriteString(strconv.FormatFloat(v.FVal, 'f', -1, 64))
	case storage.TypeBool:
		if v.BVal {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case storage.TypeDecimal:
		sb.WriteString(valToString(v))
	case storage.TypeJSON:
		trimmed := strings.TrimSpace(v.SVal)
		if trimmed == "" {
			sb.WriteString("null")
		} else {
			sb.WriteString(trimmed)
		}
	default:
		// Strings, Date, Time, UUID, etc.
		str := valToString(v)
		b, _ := json.Marshal(str)
		sb.Write(b)
	}
}

func isJSONBuiltin(name string) bool {
	switch strings.ToUpper(name) {
	case "ISJSON", "JSON_VALUE", "JSON_QUERY", "JSON_MODIFY", "JSON_OBJECT", "JSON_ARRAY", "JSON_PATH_EXISTS", "JSON_OBJECTAGG", "JSON_ARRAYAGG":
		return true
	default:
		return false
	}
}

func evalJSONBuiltin(upper string, args []storage.Value) storage.Value {
	switch upper {
	case "JSON_OBJECT":
		nullOption := "ABSENT" // default ABSENT ON NULL
		effectiveArgs := args
		if len(args) > 0 {
			last := args[len(args)-1]
			if !last.IsNull && strings.HasPrefix(last.SVal, "__OPT:") {
				nullOption = strings.TrimPrefix(last.SVal, "__OPT:")
				effectiveArgs = args[:len(args)-1]
			}
		}

		if len(effectiveArgs) == 0 {
			return storage.JSONValue("{}")
		}

		var sb strings.Builder
		sb.WriteString("{")
		first := true

		for i := 0; i+1 < len(effectiveArgs); i += 2 {
			kVal := effectiveArgs[i]
			vVal := effectiveArgs[i+1]

			if vVal.IsNull && nullOption == "ABSENT" {
				continue
			}

			if !first {
				sb.WriteString(",")
			}
			first = false

			keyStr := valToString(kVal)
			keyBytes, _ := json.Marshal(keyStr)
			sb.Write(keyBytes)
			sb.WriteString(":")

			encodeJSONElement(&sb, vVal)
		}
		sb.WriteString("}")
		return storage.JSONValue(sb.String())

	case "JSON_ARRAY":
		nullOption := "ABSENT" // default ABSENT ON NULL
		effectiveArgs := args
		if len(args) > 0 {
			last := args[len(args)-1]
			if !last.IsNull && strings.HasPrefix(last.SVal, "__OPT:") {
				nullOption = strings.TrimPrefix(last.SVal, "__OPT:")
				effectiveArgs = args[:len(args)-1]
			}
		}

		var sb strings.Builder
		sb.WriteString("[")
		first := true

		for _, vVal := range effectiveArgs {
			if vVal.IsNull && nullOption == "ABSENT" {
				continue
			}

			if !first {
				sb.WriteString(",")
			}
			first = false

			encodeJSONElement(&sb, vVal)
		}
		sb.WriteString("]")
		return storage.JSONValue(sb.String())

	case "JSON_OBJECTAGG":
		return storage.StringValue("JSON_OBJECTAGG(name:column_id)")

	case "JSON_ARRAYAGG":
		return storage.StringValue("JSON_ARRAYAGG")

	case "JSON_PATH_EXISTS":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		doc := valToString(args[0])
		path := valToString(args[1])
		exists, valid := storage.JSONPathExists(doc, path)
		if !valid {
			return storage.IntValue(0)
		}
		if exists {
			return storage.IntValue(1)
		}
		return storage.IntValue(0)
	// 5. JSON Functions
	// ----------------------------------------------------
	case "JSON_VALUE":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		doc := valToString(args[0])
		path := valToString(args[1])
		val, ok := storage.JSONValueByPath(doc, path)
		if !ok || val == nil {
			return storage.NullValue(storage.TypeVarChar)
		}
		switch v := val.(type) {
		case string:
			return storage.StringValue(v)
		case int64:
			return storage.IntValue(v)
		case float64:
			return storage.FloatValue(v)
		case bool:
			return storage.BoolValue(v)
		default:
			return storage.StringValue(fmt.Sprintf("%v", v))
		}

	case "JSON_QUERY":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeJSON)
		}
		doc := valToString(args[0])
		path := valToString(args[1])
		val, ok := storage.JSONQueryByPath(doc, path)
		if !ok || val == "" {
			return storage.NullValue(storage.TypeJSON)
		}
		return storage.JSONValue(val)

	case "ISJSON":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		str := strings.TrimSpace(valToString(args[0]))
		if str == "" {
			return storage.IntValue(0)
		}
		var parsed any
		if err := json.Unmarshal([]byte(str), &parsed); err != nil {
			return storage.IntValue(0)
		}
		if len(args) >= 2 && !args[1].IsNull {
			opt := strings.ToUpper(strings.TrimSpace(valToString(args[1])))
			switch opt {
			case "ARRAY":
				if _, ok := parsed.([]any); !ok {
					return storage.IntValue(0)
				}
			case "OBJECT":
				if _, ok := parsed.(map[string]any); !ok {
					return storage.IntValue(0)
				}
			case "SCALAR":
				switch parsed.(type) {
				case map[string]any, []any:
					return storage.IntValue(0)
				}
			}
		}
		return storage.IntValue(1)

	case "JSON_MODIFY":
		if len(args) < 3 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		doc := valToString(args[0])
		path := valToString(args[1])
		newVal := args[2]
		res, err := modifyJSONByPath(doc, path, newVal)
		if err != nil {
			return storage.StringValue(doc)
		}
		return storage.StringValue(res)

	// ----------------------------------------------------
	default:
		return storage.NullValue(storage.TypeUnknown)
	}
}
