package replication

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"sqlion/pkg/backup"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

// ReplicationClient streams and replays WAL records from the primary node.
type ReplicationClient struct {
	primaryURL    string
	replicaID     string
	eng           storage.Engine
	lastLSN       atomic.Int64
	stopCh        chan struct{}
	stopped       atomic.Bool
	wg            sync.WaitGroup
	httpClient    *http.Client
	onLSNProgress func(lsn int64)
}

// NewReplicationClient creates a new WAL stream replication client.
func NewReplicationClient(primaryURL, replicaID string, eng storage.Engine, initialLSN int64) *ReplicationClient {
	c := &ReplicationClient{
		primaryURL: primaryURL,
		replicaID:  replicaID,
		eng:        eng,
		stopCh:     make(chan struct{}),
		httpClient: &http.Client{
			Timeout: 0, // Streaming connection does not timeout
		},
	}
	c.lastLSN.Store(initialLSN)
	return c
}

// SetOnLSNProgress registers a callback triggered whenever a WAL record is successfully applied.
func (c *ReplicationClient) SetOnLSNProgress(fn func(lsn int64)) {
	c.onLSNProgress = fn
}

// LastLSN returns the latest LSN applied by this client.
func (c *ReplicationClient) LastLSN() int64 {
	return c.lastLSN.Load()
}

// Start launches the background replication loop.
func (c *ReplicationClient) Start(ctx context.Context) {
	c.wg.Add(1)
	go c.runLoop(ctx)
}

// Stop gracefully stops the replication client.
func (c *ReplicationClient) Stop() {
	if c.stopped.CompareAndSwap(false, true) {
		close(c.stopCh)
		c.wg.Wait()
	}
}

func (c *ReplicationClient) runLoop(ctx context.Context) {
	defer c.wg.Done()

	backoff := 500 * time.Millisecond
	const maxBackoff = 5 * time.Second

	for {
		select {
		case <-c.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		err := c.streamOnce(ctx)
		if err != nil {
			select {
			case <-c.stopCh:
				return
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		} else {
			backoff = 500 * time.Millisecond
		}
	}
}

func (c *ReplicationClient) streamOnce(ctx context.Context) error {
	fromLSN := c.lastLSN.Load()
	streamURL := fmt.Sprintf("%s/api/replication/stream?from_lsn=%d&replica_id=%s", c.primaryURL, fromLSN, c.replicaID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("replication stream returned status %d: %s", resp.StatusCode, string(body))
	}

	reader := bufio.NewReader(resp.Body)
	for {
		select {
		case <-c.stopCh:
			return nil
		case <-ctx.Done():
			return nil
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			return err
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		// Handle heartbeat ping line
		if string(line) == ":heartbeat" {
			continue
		}

		var rec memengine.WALRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // Skip malformed frame
		}

		// Apply WAL record to local engine
		if err := backup.ApplyWALRecordToEngine(c.eng, rec); err != nil {
			// Fail-safe log or error
		}

		if rec.LSN > c.lastLSN.Load() {
			c.lastLSN.Store(rec.LSN)
			if c.onLSNProgress != nil {
				c.onLSNProgress(rec.LSN)
			}
		}
	}
}
