//go:build cgo && wiredtiger

package wiredtiger

import (
	"errors"
	"strings"
	"testing"

	"lionsql/pkg/storage"
)

func TestWtOpError_RollbackMapsToSerializationFailure(t *testing.T) {
	err := wtOpError("cursor_update", wtRollback)
	if !errors.Is(err, storage.ErrSerializationFailure) {
		t.Fatalf("expected ErrSerializationFailure, got %v", err)
	}
}

func TestWtOpError_OtherCodesKeepGenericMessage(t *testing.T) {
	err := wtOpError("cursor_insert", -1)
	if errors.Is(err, storage.ErrSerializationFailure) {
		t.Fatalf("non-rollback code must not map to serialization failure: %v", err)
	}
	if !strings.Contains(err.Error(), "cursor_insert failed with code -1") {
		t.Fatalf("unexpected message: %v", err)
	}
}
