package runtime

// TriggerHandler is a callback invoked when DML operations occur.
type TriggerHandler func(tableName string, event string, inserted []map[string]any, deleted []map[string]any) error
