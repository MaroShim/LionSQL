package replication_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"lionsql/pkg/replication"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

type failingEngine struct {
	*memengine.Engine
}

func (f *failingEngine) CreateTable(s *storage.TableSchema) error {
	return fmt.Errorf("simulated engine failure on apply")
}

type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestP1_BatchM4_ReplicaWALApplyErrorPropagation tests Defect 5-3.
func TestP1_BatchM4_ReplicaWALApplyErrorPropagation(t *testing.T) {
	// A mock WAL stream emitting a record that will fail engine apply
	rec := memengine.WALRecord{
		LSN: 100,
		Ops: []memengine.WALOp{
			{
				Type:   memengine.OpCreateTable,
				Table:  "t_failing",
				Schema: &storage.TableSchema{Name: "t_failing"},
			},
		},
	}
	data, _ := json.Marshal(rec)
	data = append(data, '\n')

	rawEng := memengine.New()
	defer rawEng.Close()

	// Wrap in failingEngine (non-*memengine.Engine) so ApplyWALRecordToEngine delegates to CreateTable
	failEng := &failingEngine{Engine: rawEng}

	client := replication.NewReplicationClient("http://primary.mock", "replica-test-err", failEng, 50)

	// Mock HTTP client that streams data without network sockets
	mockHTTP := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(data)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	client.SetHTTPClient(mockHTTP)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client.Start(ctx)
	defer client.Stop()

	// Give a moment for client to process the record
	time.Sleep(100 * time.Millisecond)

	// In the unfixed code (Defect 5-3), ApplyWALRecordToEngine error was silently swallowed
	// and c.lastLSN was updated to 100!
	// With the fix, lastLSN must NOT advance past 50 when apply fails!
	if currentLSN := client.LastLSN(); currentLSN >= 100 {
		t.Fatalf("Defect 5-3 reproduced: client advanced lastLSN to %d despite WAL apply failure", currentLSN)
	}
}
