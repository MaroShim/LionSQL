package engine

import (
	"errors"
	"fmt"
	"testing"

	"lionsql/pkg/storage"
)

func TestIsWriteConflictDetection(t *testing.T) {
	// 1. Direct ErrSerializationFailure
	if !storage.IsWriteConflict(storage.ErrSerializationFailure) {
		t.Errorf("expected ErrSerializationFailure to be detected as write conflict")
	}

	// 2. Wrapped ErrSerializationFailure
	wrapped := fmt.Errorf("transaction commit failed: %w", storage.ErrSerializationFailure)
	if !storage.IsWriteConflict(wrapped) {
		t.Errorf("expected wrapped ErrSerializationFailure to be detected")
	}

	// 3. WiredTiger WT_ROLLBACK (-31800) error text
	wtErr := errors.New("cursor_update failed with code -31800 (WT_ROLLBACK)")
	if !storage.IsWriteConflict(wtErr) {
		t.Errorf("expected WT_ROLLBACK string to be detected")
	}

	// 4. Non-conflict errors
	if storage.IsWriteConflict(nil) {
		t.Errorf("nil error must not be write conflict")
	}
	if storage.IsWriteConflict(storage.ErrTableNotFound) {
		t.Errorf("ErrTableNotFound must not be write conflict")
	}
	if storage.IsWriteConflict(storage.ErrUniqueViolation) {
		t.Errorf("ErrUniqueViolation must not be write conflict")
	}
}
