package runtime

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dop251/goja"
	"sqlion/pkg/storage"
)

// TriggerHandler is a callback invoked when DML operations occur.
type TriggerHandler func(tableName string, event string, inserted []map[string]any, deleted []map[string]any) error

// Bridge creates the JavaScript "db", "console", and "SQLion" APIs bound to an active storage transaction.
type Bridge struct {
	tx              storage.Transaction
	logs            []string
	executor        *Executor
	transientTables map[string][]map[string]any
	triggerHandler  TriggerHandler
}

// NewBridge creates a new bridge for the given transaction.
func NewBridge(tx storage.Transaction) *Bridge {
	return &Bridge{
		tx:              tx,
		logs:            make([]string, 0),
		transientTables: make(map[string][]map[string]any),
	}
}

// SetExecutor links an Executor for nested procedure invocation.
func (b *Bridge) SetExecutor(exec *Executor) {
	b.executor = exec
}

// SetTriggerHandler links a trigger handler callback.
func (b *Bridge) SetTriggerHandler(h TriggerHandler) {
	b.triggerHandler = h
}

// Logs returns the collected console.log messages.
func (b *Bridge) Logs() []string {
	return b.logs
}

// Bind attaches db, console, and SQLion objects into the Goja runtime.
func (b *Bridge) Bind(vm *goja.Runtime) error {
	dbObj := vm.NewObject()

	// db._setTransientTable(name, rows)
	_ = dbObj.Set("_setTransientTable", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return goja.Undefined()
		}
		name := strings.ToLower(call.Arguments[0].String())
		val := call.Arguments[1].Export()
		var rows []map[string]any
		if arr, ok := val.([]any); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
					rows = append(rows, m)
				}
			}
		} else if arr, ok := val.([]map[string]any); ok {
			rows = arr
		}
		b.transientTables[name] = rows
		return goja.Undefined()
	})

	// db._clearTransientTables()
	_ = dbObj.Set("_clearTransientTables", func(call goja.FunctionCall) goja.Value {
		b.transientTables = make(map[string][]map[string]any)
		return goja.Undefined()
	})

	// db.get(tableName, pk, [columnName])
	_ = dbObj.Set("get", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(vm.ToValue("db.get requires at least 2 arguments: (tableName, pk, [column])"))
		}

		tableName := call.Arguments[0].String()
		lowerName := strings.ToLower(tableName)

		if rows, isTransient := b.transientTables[lowerName]; isTransient {
			pkVal := call.Arguments[1].Export()
			var found map[string]any
			for _, r := range rows {
				if idVal, hasID := r["id"]; hasID {
					if fmt.Sprintf("%v", idVal) == fmt.Sprintf("%v", pkVal) {
						found = r
						break
					}
				} else {
					found = r
					break
				}
			}
			if found == nil && len(rows) > 0 {
				found = rows[0]
			}
			if found == nil {
				return goja.Null()
			}
			if len(call.Arguments) >= 3 && !goja.IsNull(call.Arguments[2]) && !goja.IsUndefined(call.Arguments[2]) {
				colName := call.Arguments[2].String()
				val, exists := found[colName]
				if !exists {
					return goja.Null()
				}
				return vm.ToValue(val)
			}
			rowObj := vm.NewObject()
			for k, v := range found {
				_ = rowObj.Set(k, v)
			}
			return rowObj
		}

		pkVal := toStorageValue(call.Arguments[1].Export())

		rec, err := b.tx.Get(tableName, pkVal)
		if err != nil {
			panic(vm.ToValue(fmt.Sprintf("db.get error on %s: %v", tableName, err)))
		}

		if len(call.Arguments) >= 3 && !goja.IsNull(call.Arguments[2]) && !goja.IsUndefined(call.Arguments[2]) {
			colName := call.Arguments[2].String()
			val, exists := rec[colName]
			if !exists {
				return goja.Null()
			}
			return vm.ToValue(valToAny(val))
		}

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

		var oldRec storage.Record
		if b.triggerHandler != nil && !isTransientTable(tableName) {
			oldRec, _ = b.tx.Get(tableName, pkVal)
		}

		rec := make(storage.Record)
		for k, v := range updatesExport {
			rec[k] = toStorageValue(v)
		}

		if err := b.tx.Update(tableName, pkVal, rec); err != nil {
			panic(vm.ToValue(fmt.Sprintf("db.update error on %s: %v", tableName, err)))
		}

		if b.triggerHandler != nil && !isTransientTable(tableName) && oldRec != nil {
			oldRow := make(map[string]any)
			newRow := make(map[string]any)
			for k, v := range oldRec {
				oldRow[k] = valToAny(v)
				newRow[k] = valToAny(v)
			}
			for k, v := range updatesExport {
				newRow[k] = v
			}
			if err := b.triggerHandler(tableName, "UPDATE", []map[string]any{newRow}, []map[string]any{oldRow}); err != nil {
				panic(vm.ToValue(fmt.Sprintf("trigger error on %s UPDATE: %v", tableName, err)))
			}
		}

		return vm.ToValue(true)
	})

	// db.updateWhere(tableName, filterFn, updatesObjectOrFn)
	_ = dbObj.Set("updateWhere", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			panic(vm.ToValue("db.updateWhere requires (tableName, filterFn, updates)"))
		}

		tableName := call.Arguments[0].String()
		filterFn, ok := goja.AssertFunction(call.Arguments[1])
		if !ok {
			panic(vm.ToValue("db.updateWhere second argument must be a filter function"))
		}

		cursor, err := b.tx.Scan(tableName)
		if err != nil {
			panic(vm.ToValue(fmt.Sprintf("db.updateWhere scan error on %s: %v", tableName, err)))
		}
		defer cursor.Close()

		type updateItem struct {
			pk      storage.Value
			updates storage.Record
			oldRow  map[string]any
			newRow  map[string]any
		}
		toUpdate := make([]updateItem, 0)

		for cursor.Next() {
			rec, err := cursor.Record()
			if err != nil {
				break
			}
			rowObj := vm.NewObject()
			oldRowMap := make(map[string]any)
			for k, v := range rec {
				anyVal := valToAny(v)
				_ = rowObj.Set(k, anyVal)
				oldRowMap[k] = anyVal
			}

			condVal, err := filterFn(goja.Null(), rowObj)
			if err != nil {
				panic(vm.ToValue(fmt.Sprintf("filter error in updateWhere: %v", err)))
			}

			if condVal.ToBoolean() {
				pkVal, hasPk := rec["id"]
				if !hasPk {
					for _, v := range rec {
						pkVal = v
						hasPk = true
						break
					}
				}

				recUpdates := make(storage.Record)
				newRowMap := make(map[string]any)
				for k, v := range oldRowMap {
					newRowMap[k] = v
				}

				if updateFn, isFn := goja.AssertFunction(call.Arguments[2]); isFn {
					res, err := updateFn(goja.Null(), rowObj)
					if err != nil {
						panic(vm.ToValue(fmt.Sprintf("update fn error: %v", err)))
					}
					if m, ok := res.Export().(map[string]any); ok {
						for k, v := range m {
							recUpdates[k] = toStorageValue(v)
							newRowMap[k] = v
						}
					}
				} else if m, ok := call.Arguments[2].Export().(map[string]any); ok {
					for k, v := range m {
						recUpdates[k] = toStorageValue(v)
						newRowMap[k] = v
					}
				}

				if hasPk {
					toUpdate = append(toUpdate, updateItem{
						pk:      pkVal,
						updates: recUpdates,
						oldRow:  oldRowMap,
						newRow:  newRowMap,
					})
				}
			}
		}

		updatedCount := int64(0)
		var oldRows []map[string]any
		var newRows []map[string]any
		for _, item := range toUpdate {
			if err := b.tx.Update(tableName, item.pk, item.updates); err == nil {
				updatedCount++
				oldRows = append(oldRows, item.oldRow)
				newRows = append(newRows, item.newRow)
			}
		}

		if b.triggerHandler != nil && !isTransientTable(tableName) && len(newRows) > 0 {
			if err := b.triggerHandler(tableName, "UPDATE", newRows, oldRows); err != nil {
				panic(vm.ToValue(fmt.Sprintf("trigger error on %s UPDATE: %v", tableName, err)))
			}
		}

		return vm.ToValue(updatedCount)
	})

	// db.deleteWhere(tableName, filterFn)
	_ = dbObj.Set("deleteWhere", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(vm.ToValue("db.deleteWhere requires (tableName, filterFn)"))
		}

		tableName := call.Arguments[0].String()
		filterFn, ok := goja.AssertFunction(call.Arguments[1])
		if !ok {
			panic(vm.ToValue("db.deleteWhere second argument must be a filter function"))
		}

		cursor, err := b.tx.Scan(tableName)
		if err != nil {
			panic(vm.ToValue(fmt.Sprintf("db.deleteWhere scan error on %s: %v", tableName, err)))
		}
		defer cursor.Close()

		type delItem struct {
			pk  storage.Value
			row map[string]any
		}
		toDelete := make([]delItem, 0)
		for cursor.Next() {
			rec, err := cursor.Record()
			if err != nil {
				break
			}
			rowObj := vm.NewObject()
			rowMap := make(map[string]any)
			for k, v := range rec {
				anyVal := valToAny(v)
				_ = rowObj.Set(k, anyVal)
				rowMap[k] = anyVal
			}

			condVal, err := filterFn(goja.Null(), rowObj)
			if err != nil {
				panic(vm.ToValue(fmt.Sprintf("filter error in deleteWhere: %v", err)))
			}

			if condVal.ToBoolean() {
				pkVal, hasPk := rec["id"]
				if !hasPk {
					for _, v := range rec {
						pkVal = v
						hasPk = true
						break
					}
				}
				if hasPk {
					toDelete = append(toDelete, delItem{pk: pkVal, row: rowMap})
				}
			}
		}

		deletedCount := int64(0)
		var deletedRows []map[string]any
		for _, item := range toDelete {
			if err := b.tx.Delete(tableName, item.pk); err == nil {
				deletedCount++
				deletedRows = append(deletedRows, item.row)
			}
		}

		if b.triggerHandler != nil && !isTransientTable(tableName) && len(deletedRows) > 0 {
			if err := b.triggerHandler(tableName, "DELETE", nil, deletedRows); err != nil {
				panic(vm.ToValue(fmt.Sprintf("trigger error on %s DELETE: %v", tableName, err)))
			}
		}

		return vm.ToValue(deletedCount)
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

		if b.triggerHandler != nil && !isTransientTable(tableName) {
			rowCopy := make(map[string]any)
			for k, v := range rowExport {
				rowCopy[k] = v
			}
			if err := b.triggerHandler(tableName, "INSERT", []map[string]any{rowCopy}, nil); err != nil {
				panic(vm.ToValue(fmt.Sprintf("trigger error on %s INSERT: %v", tableName, err)))
			}
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

		var oldRec storage.Record
		if b.triggerHandler != nil && !isTransientTable(tableName) {
			oldRec, _ = b.tx.Get(tableName, pkVal)
		}

		if err := b.tx.Delete(tableName, pkVal); err != nil {
			panic(vm.ToValue(fmt.Sprintf("db.delete error on %s: %v", tableName, err)))
		}

		if b.triggerHandler != nil && !isTransientTable(tableName) && oldRec != nil {
			oldRow := make(map[string]any)
			for k, v := range oldRec {
				oldRow[k] = valToAny(v)
			}
			if err := b.triggerHandler(tableName, "DELETE", nil, []map[string]any{oldRow}); err != nil {
				panic(vm.ToValue(fmt.Sprintf("trigger error on %s DELETE: %v", tableName, err)))
			}
		}

		return vm.ToValue(true)
	})

	// db.query(tableName, [filterFn])
	_ = dbObj.Set("query", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			panic(vm.ToValue("db.query requires 1 argument: (tableName)"))
		}

		tableName := call.Arguments[0].String()
		lowerName := strings.ToLower(tableName)

		var filterFn goja.Callable
		if len(call.Arguments) >= 2 && !goja.IsNull(call.Arguments[1]) && !goja.IsUndefined(call.Arguments[1]) {
			filterFn, _ = goja.AssertFunction(call.Arguments[1])
		}

		if rows, isTransient := b.transientTables[lowerName]; isTransient {
			resRows := make([]map[string]any, 0, len(rows))
			for _, r := range rows {
				if filterFn != nil {
					rowObj := vm.NewObject()
					for k, v := range r {
						_ = rowObj.Set(k, v)
					}
					res, err := filterFn(goja.Null(), rowObj)
					if err != nil || !res.ToBoolean() {
						continue
					}
				}
				resRows = append(resRows, r)
			}
			return vm.ToValue(resRows)
		}

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

			if filterFn != nil {
				rowObj := vm.NewObject()
				for k, v := range rowMap {
					_ = rowObj.Set(k, v)
				}
				res, err := filterFn(goja.Null(), rowObj)
				if err != nil || !res.ToBoolean() {
					continue
				}
			}

			rows = append(rows, rowMap)
		}

		return vm.ToValue(rows)
	})

	// db.count(tableName, [filterFn])
	_ = dbObj.Set("count", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			panic(vm.ToValue("db.count requires (tableName)"))
		}
		tableName := call.Arguments[0].String()
		lowerName := strings.ToLower(tableName)

		var filterFn goja.Callable
		if len(call.Arguments) >= 2 && !goja.IsNull(call.Arguments[1]) && !goja.IsUndefined(call.Arguments[1]) {
			filterFn, _ = goja.AssertFunction(call.Arguments[1])
		}

		if rows, isTransient := b.transientTables[lowerName]; isTransient {
			cnt := int64(0)
			for _, r := range rows {
				if filterFn != nil {
					rowObj := vm.NewObject()
					for k, v := range r {
						_ = rowObj.Set(k, v)
					}
					res, err := filterFn(goja.Null(), rowObj)
					if err == nil && res.ToBoolean() {
						cnt++
					}
				} else {
					cnt++
				}
			}
			return vm.ToValue(cnt)
		}

		cursor, err := b.tx.Scan(tableName)
		if err != nil {
			panic(vm.ToValue(fmt.Sprintf("db.count error on %s: %v", tableName, err)))
		}
		defer cursor.Close()

		cnt := int64(0)
		for cursor.Next() {
			rec, err := cursor.Record()
			if err != nil {
				break
			}
			if filterFn != nil {
				rowObj := vm.NewObject()
				for k, v := range rec {
					_ = rowObj.Set(k, valToAny(v))
				}
				res, err := filterFn(goja.Null(), rowObj)
				if err == nil && res.ToBoolean() {
					cnt++
				}
			} else {
				cnt++
			}
		}
		return vm.ToValue(cnt)
	})

	if err := vm.Set("db", dbObj); err != nil {
		return err
	}

	// SQLion helper utilities
	sqlionObj := vm.NewObject()
	_ = sqlionObj.Set("like", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return vm.ToValue(false)
		}
		str := call.Arguments[0].String()
		pattern := call.Arguments[1].String()
		return vm.ToValue(matchLike(str, pattern))
	})
	_ = sqlionObj.Set("in", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return vm.ToValue(false)
		}
		val := call.Arguments[0].Export()
		listExport, ok := call.Arguments[1].Export().([]any)
		if !ok {
			return vm.ToValue(false)
		}
		for _, item := range listExport {
			if fmt.Sprintf("%v", item) == fmt.Sprintf("%v", val) {
				return vm.ToValue(true)
			}
		}
		return vm.ToValue(false)
	})
	_ = sqlionObj.Set("between", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			return vm.ToValue(false)
		}
		v := call.Arguments[0].ToFloat()
		low := call.Arguments[1].ToFloat()
		high := call.Arguments[2].ToFloat()
		return vm.ToValue(v >= low && v <= high)
	})

	if err := vm.Set("SQLion", sqlionObj); err != nil {
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

func matchLike(str, pattern string) bool {
	var b strings.Builder
	b.WriteString("(?i)^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteString(".")
		case '.', '+', '?', '^', '$', '(', ')', '[', ']', '{', '}', '|', '\\':
			b.WriteString("\\")
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return strings.EqualFold(str, pattern)
	}
	return re.MatchString(str)
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
			return storage.Value{Type: storage.TypeInt, IVal: int64(val), FVal: val}
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

func isTransientTable(name string) bool {
	lower := strings.ToLower(name)
	return lower == "inserted" || lower == "deleted"
}

