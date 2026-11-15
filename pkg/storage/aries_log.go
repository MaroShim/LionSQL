package storage

import "sync"

// IndexImage는 인덱스 B-Tree 엔트리의 완전한 Key-Value 물리적 스냅샷입니다.
type IndexImage struct {
	URI   string // WiredTiger Table URI (예: "table:_idx_users_email")
	Key   string // B-Tree Index Key (Order-preserving 인코딩 완료된 물리적 키)
	Value string // B-Tree Index Value (Unique의 경우 PK, 비고유의 경우 빈 문자열)
}

// PhysiologicalLogKind는 ARIES 오퍼레이션 종류를 정의합니다.
type PhysiologicalLogKind uint8

const (
	OpInsert PhysiologicalLogKind = iota + 1
	OpDelete
	OpUpdate
)

// PhysiologicalRecord는 트랜잭션의 원자적 변경 상태를 Undo/Redo 양방향으로 완벽히 표현합니다.
type PhysiologicalRecord struct {
	Kind     PhysiologicalLogKind
	Table    string
	PK       Value
	OldTuple []byte       // Before-Image (Undo용: 이전 본문 튜플 바이트)
	NewTuple []byte       // After-Image  (Redo용: 신규 본문 튜플 바이트)
	OldIdx   []IndexImage // Before-Image (Undo용: 변경 전 인덱스 키 목록)
	NewIdx   []IndexImage // After-Image  (Redo용: 변경 후 신규 인덱스 키 목록)
}

var physRecordPool = sync.Pool{
	New: func() any {
		return &PhysiologicalRecord{
			OldIdx: make([]IndexImage, 0, 4),
			NewIdx: make([]IndexImage, 0, 4),
		}
	},
}

// AcquirePhysRecord는 풀에서 PhysiologicalRecord를 획득합니다.
func AcquirePhysRecord(kind PhysiologicalLogKind, table string, pk Value, oldTuple, newTuple []byte) *PhysiologicalRecord {
	r := physRecordPool.Get().(*PhysiologicalRecord)
	r.Kind = kind
	r.Table = table
	r.PK = pk
	r.OldTuple = oldTuple
	r.NewTuple = newTuple
	r.OldIdx = r.OldIdx[:0]
	r.NewIdx = r.NewIdx[:0]
	return r
}

// ReleasePhysRecord는 PhysiologicalRecord를 풀로 반납합니다.
func ReleasePhysRecord(r *PhysiologicalRecord) {
	if r == nil {
		return
	}
	r.OldTuple = nil
	r.NewTuple = nil
	r.OldIdx = r.OldIdx[:0]
	r.NewIdx = r.NewIdx[:0]
	physRecordPool.Put(r)
}
