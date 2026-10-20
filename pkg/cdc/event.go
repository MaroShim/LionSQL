package cdc

import (
	"encoding/json"
	"time"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// OpType represents the mutation operation in the CDC stream.
type OpType string

const (
	OpInsert OpType = "INSERT"
	OpUpdate OpType = "UPDATE"
	OpDelete OpType = "DELETE"
	OpDDL    OpType = "DDL"
)

// ChangeEvent represents a single committed row-level or schema mutation event.
type ChangeEvent struct {
	LSN       int64          `json:"lsn"`
	Timestamp int64          `json:"timestamp"`
	Table     string         `json:"table"`
	Op        OpType         `json:"op"`
	Key       string         `json:"key,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	BaseVer   int64          `json:"base_ver,omitempty"`
}

// ToJSON serializes the ChangeEvent into a JSON byte slice.
func (e ChangeEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

// RecordToMap converts a storage.Record (map[string]storage.Value) into a JSON-serializable map[string]any.
func RecordToMap(rec storage.Record) map[string]any {
	if rec == nil {
		return nil
	}
	m := make(map[string]any, len(rec))
	for k, v := range rec {
		if v.IsNull {
			m[k] = nil
			continue
		}
		switch v.Type {
		case storage.TypeInt:
			m[k] = v.IVal
		case storage.TypeVarChar, storage.TypeUUID, storage.TypeJSON:
			m[k] = v.SVal
		case storage.TypeFloat:
			m[k] = v.FVal
		case storage.TypeBool:
			m[k] = v.BVal
		case storage.TypeDateTime, storage.TypeDate, storage.TypeTime:
			m[k] = v.TVal.Format(time.RFC3339Nano)
		case storage.TypeDecimal:
			m[k] = v.DVal.String()
		case storage.TypeBinary:
			m[k] = v.BinVal
		default:
			m[k] = v.String()
		}
	}
	return m
}

// WALRecordToChangeEvents extracts individual ChangeEvents from a committed WALRecord.
func WALRecordToChangeEvents(rec memengine.WALRecord) []ChangeEvent {
	if len(rec.Ops) == 0 {
		return nil
	}
	events := make([]ChangeEvent, 0, len(rec.Ops))
	for _, op := range rec.Ops {
		var opType OpType
		switch op.Type {
		case memengine.OpInsert:
			opType = OpInsert
		case memengine.OpUpdate:
			opType = OpUpdate
		case memengine.OpDelete:
			opType = OpDelete
		case memengine.OpCreateTable, memengine.OpDropTable, memengine.OpCreateIndex, memengine.OpDropIndex, memengine.OpTruncateTable, memengine.OpDropPartition:
			opType = OpDDL
		default:
			opType = OpType(op.Type)
		}

		ev := ChangeEvent{
			LSN:       rec.LSN,
			Timestamp: rec.Timestamp,
			Table:     op.Table,
			Op:        opType,
			Key:       op.Key,
			BaseVer:   op.BaseVer,
		}
		if op.Record != nil {
			ev.Data = RecordToMap(op.Record)
		}
		events = append(events, ev)
	}
	return events
}
