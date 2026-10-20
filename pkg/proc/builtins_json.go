package proc

import (
	"encoding/json"
	"fmt"
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



func isJSONBuiltin(name string) bool {
	switch name {
	case "ISJSON", "JSON_VALUE", "JSON_QUERY", "JSON_MODIFY":
		return true
	default:
		return false
	}
}

func evalJSONBuiltin(upper string, args []storage.Value) storage.Value {
	switch upper {
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
