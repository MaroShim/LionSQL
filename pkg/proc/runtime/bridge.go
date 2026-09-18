package runtime

import (
	"fmt"
	"strings"

	"github.com/dop251/goja"
	"sqltiger/pkg/storage"
)

// Bridge creates the JavaScript "db" and "console" APIs bound to an active storage transaction.
type Bridge struct {
	tx   storage.Transaction
	logs []string
}

// NewBridge creates a new bridge for the given transaction.
func NewBridge(tx storage.Transaction) *Bridge {
	return &Bridge{
		tx:   tx,
		logs: make([]string, 0),
	}
}

// Logs returns the collected console.log messages.
func (b *Bridge) Logs() []string {
	return b.logs
}

// Bind attaches db and console objects into the Goja runtime.
func (b *Bridge) Bind(vm *goja.Runtime) error {
	dbObj := vm.NewObject()

	// db.get(tableName, pk, [columnName])
	_ = dbObj.Set("get", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(vm.ToValue("db.get requires at least 2 arguments: (tableName, pk, [column])"))
		}

		tableName := call.Arguments[0].String()
		pkVal := toStorageValue(call.Arguments[1].Export())

		rec, err := b.tx.Get(tableName, pkVal)
		if err != nil {
			panic(vm.ToValue(fmt.Sprintf("db.get error on %s: %v", tableName, err)))
		}

		// If a specific column is requested
		if len(call.Arguments) >= 3 && !goja.IsNull(call.Arguments[2]) && !goja.IsUndefined(call.Arguments[2]) {
			colName := call.Arguments[2].String()
			val, exists := rec[colName]
			if !exists {
				return goja.Null()
			}
			return vm.ToValue(valToAny(val))
		}

		// Otherwise return entire row as JS object
		rowObj := vm.NewObject()
		for k, v := range rec {
			_ = rowObj.Set(k, valToAny(v))
		}
		return rowObj
	})

	// db.update(tableName, pk, updatesObject)
	_ = dbObj.Set("update", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			panic(vm.ToValue("db.update requires 3 arguments: (tableName, pk, updates)"))
		}

		tableName := call.Arguments[0].String()
		pkVal := toStorageValue(call.Arguments[1].Export())

		updatesExport, ok := call.Arguments[2].Export().(map[string]any)
		if !ok {
			panic(vm.ToValue("db.update third argument must be an object of column updates"))
		}

		rec := make(storage.Record)
		for k, v := range updatesExport {
			rec[k] = toStorageValue(v)
		}

		if err := b.tx.Update(tableName, pkVal, rec); err != nil {
			panic(vm.ToValue(fmt.Sprintf("db.update error on %s: %v", tableName, err)))
		}

		return vm.ToValue(true)
	})

	// db.insert(tableName, rowObject)
	_ = dbObj.Set("insert", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(vm.ToValue("db.insert requires 2 arguments: (tableName, row)"))
		}

		tableName := call.Arguments[0].String()
		rowExport, ok := call.Arguments[1].Export().(map[string]any)
		if !ok {
			panic(vm.ToValue("db.insert second argument must be a row object"))
		}

		rec := make(storage.Record)
		for k, v := range rowExport {
			rec[k] = toStorageValue(v)
		}

		if err := b.tx.Insert(tableName, rec); err != nil {
			panic(vm.ToValue(fmt.Sprintf("db.insert error on %s: %v", tableName, err)))
		}

		return vm.ToValue(true)
	})

	// db.delete(tableName, pk)
	_ = dbObj.Set("delete", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(vm.ToValue("db.delete requires 2 arguments: (tableName, pk)"))
		}

		tableName := call.Arguments[0].String()
		pkVal := toStorageValue(call.Arguments[1].Export())

		if err := b.tx.Delete(tableName, pkVal); err != nil {
			panic(vm.ToValue(fmt.Sprintf("db.delete error on %s: %v", tableName, err)))
		}

		return vm.ToValue(true)
	})

	// db.query(tableName)
	_ = dbObj.Set("query", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			panic(vm.ToValue("db.query requires 1 argument: (tableName)"))
		}

		tableName := call.Arguments[0].String()
		cursor, err := b.tx.Scan(tableName)
		if err != nil {
			panic(vm.ToValue(fmt.Sprintf("db.query error on %s: %v", tableName, err)))
		}
		defer cursor.Close()

		rows := make([]map[string]any, 0)
		for cursor.Next() {
			rec, err := cursor.Record()
			if err != nil {
				break
			}
			rowMap := make(map[string]any)
			for k, v := range rec {
				rowMap[k] = valToAny(v)
			}
			rows = append(rows, rowMap)
		}

		return vm.ToValue(rows)
	})

	if err := vm.Set("db", dbObj); err != nil {
		return err
	}

	// console.log(...)
	consoleObj := vm.NewObject()
	_ = consoleObj.Set("log", func(call goja.FunctionCall) goja.Value {
		parts := make([]string, len(call.Arguments))
		for i, arg := range call.Arguments {
			parts[i] = fmt.Sprintf("%v", arg.Export())
		}
		line := strings.Join(parts, " ")
		b.logs = append(b.logs, line)
		return goja.Undefined()
	})

	return vm.Set("console", consoleObj)
}

func valToAny(v storage.Value) any {
	if v.IsNull {
		return nil
	}
	switch v.Type {
	case storage.TypeInt:
		return v.IVal
	case storage.TypeVarChar:
		return v.SVal
	case storage.TypeFloat:
		return v.FVal
	case storage.TypeBool:
		return v.BVal
	case storage.TypeDateTime:
		return v.TVal
	default:
		return v.String()
	}
}

func toStorageValue(v any) storage.Value {
	if v == nil {
		return storage.NullValue(storage.TypeUnknown)
	}
	switch val := v.(type) {
	case int64:
		return storage.IntValue(val)
	case int:
		return storage.IntValue(int64(val))
	case float64:
		if val == float64(int64(val)) {
			return storage.IntValue(int64(val))
		}
		return storage.FloatValue(val)
	case string:
		return storage.StringValue(val)
	case bool:
		return storage.BoolValue(val)
	default:
		return storage.StringValue(fmt.Sprintf("%v", v))
	}
}
