package storage_test

import (
	"testing"

	"lionsql/pkg/storage"
)

func TestToastPointer_EncodeDecode(t *testing.T) {
	tp := storage.ToastPointer{
		ChunkID:    12345678901234,
		TotalSize:  1048576, // 1MB
		ChunkCount: 263,
		Flags:      0x01,
	}

	encoded := tp.Encode()
	if len(encoded) != 16 {
		t.Fatalf("expected 16 bytes encoded ToastPointer, got %d", len(encoded))
	}

	decoded, err := storage.DecodeToastPointer(encoded[:])
	if err != nil {
		t.Fatalf("DecodeToastPointer failed: %v", err)
	}

	if decoded.ChunkID != tp.ChunkID {
		t.Errorf("ChunkID mismatch: got %d, expected %d", decoded.ChunkID, tp.ChunkID)
	}
	if decoded.TotalSize != tp.TotalSize {
		t.Errorf("TotalSize mismatch: got %d, expected %d", decoded.TotalSize, tp.TotalSize)
	}
	if decoded.ChunkCount != tp.ChunkCount {
		t.Errorf("ChunkCount mismatch: got %d, expected %d", decoded.ChunkCount, tp.ChunkCount)
	}
	if decoded.Flags != tp.Flags {
		t.Errorf("Flags mismatch: got %d, expected %d", decoded.Flags, tp.Flags)
	}
}

func TestToastPointer_TooShort(t *testing.T) {
	_, err := storage.DecodeToastPointer([]byte{1, 2, 3})
	if err == nil {
		t.Fatalf("expected error for truncated ToastPointer buffer, got nil")
	}
}
