package replication

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"sqlion/pkg/backup"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

// ReplicationClient streams and replays WAL records from the primary node.
type ReplicationClient struct {
	primaryURL     string
	replicaID      string
	eng            storage.Engine
	lastLSN        atomic.Int64
	lastHeartbeat  atomic.Int64
	stopCh         chan struct{}
	stopped        atomic.Bool
	wg             sync.WaitGroup
	httpClient     *http.Client
	onLSNProgress  func(lsn int64)
	onTermReceived func(term int64)
	cancelFn       context.CancelFunc
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
	c.lastHeartbeat.Store(time.Now().UnixNano())
	return c
}

// SetOnLSNProgress registers a callback triggered whenever a WAL record is successfully applied.
func (c *ReplicationClient) SetOnLSNProgress(fn func(lsn int64)) {
	c.onLSNProgress = fn
}

// SetOnTermReceived registers a callback triggered whenever the primary's term is received.
func (c *ReplicationClient) SetOnTermReceived(fn func(term int64)) {
	c.onTermReceived = fn
}

// LastLSN returns the latest LSN applied by this client.
func (c *ReplicationClient) LastLSN() int64 {
	return c.lastLSN.Load()
}

// LastHeartbeatTime returns the timestamp of the last received heartbeat or WAL frame.
func (c *ReplicationClient) LastHeartbeatTime() time.Time {
	nanos := c.lastHeartbeat.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

// ResetHeartbeat resets the last heartbeat timestamp to now.
func (c *ReplicationClient) ResetHeartbeat() {
	c.lastHeartbeat.Store(time.Now().UnixNano())
}

// Start launches the background replication loop.
func (c *ReplicationClient) Start(ctx context.Context) {
	loopCtx, cancel := context.WithCancel(ctx)
	c.cancelFn = cancel
	c.wg.Add(1)
	go c.runLoop(loopCtx)
}

// Stop gracefully stops the replication client and instantly aborts active streams.
func (c *ReplicationClient) Stop() {
	if c.stopped.CompareAndSwap(false, true) {
		if c.cancelFn != nil {
			c.cancelFn()
		}
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

	c.lastHeartbeat.Store(time.Now().UnixNano())
	if termStr := resp.Header.Get("X-Replication-Term"); termStr != "" {
		if term, err := strconv.ParseInt(termStr, 10, 64); err == nil && c.onTermReceived != nil {
			c.onTermReceived(term)
		}
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

		c.lastHeartbeat.Store(time.Now().UnixNano())

		// Handle heartbeat ping line (e.g. ":heartbeat" or ":heartbeat term=2")
		if bytes.HasPrefix(line, []byte(":heartbeat")) {
			if idx := bytes.Index(line, []byte("term=")); idx != -1 {
				termStr := string(bytes.TrimSpace(line[idx+5:]))
				if term, err := strconv.ParseInt(termStr, 10, 64); err == nil && c.onTermReceived != nil {
					c.onTermReceived(term)
				}
			}
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
