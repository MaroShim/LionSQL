package cdc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// OutboxMessage represents a normalized message ready for external broker transmission.
type OutboxMessage struct {
	ID        string            `json:"id"`
	LSN       int64             `json:"lsn"`
	Topic     string            `json:"topic"`
	Key       string            `json:"key,omitempty"`
	Payload   []byte            `json:"payload"`
	Headers   map[string]string `json:"headers,omitempty"`
	Timestamp int64             `json:"timestamp"`
	Table     string            `json:"table,omitempty"`
	Op        OpType            `json:"op,omitempty"`
}

// BrokerSink represents a pluggable destination broker sink (Kafka, RabbitMQ, Webhook, Memory).
type BrokerSink interface {
	Name() string
	Publish(ctx context.Context, msg *OutboxMessage) error
	Close() error
}

// DispatcherConfig configures the transactional outbox dispatcher.
type DispatcherConfig struct {
	DataDir        string        `json:"data_dir"`        // Checkpoint directory (empty = in-memory)
	BatchSize      int           `json:"batch_size"`      // Subscriber buffer size (default 1024)
	MaxRetries     int           `json:"max_retries"`     // Retries per failed sink publish (default 5)
	InitialBackoff time.Duration `json:"initial_backoff"` // Initial retry backoff (default 50ms)
	MaxBackoff     time.Duration `json:"max_backoff"`     // Maximum retry backoff (default 2s)
	TableFilter    string        `json:"table_filter"`    // Optional table filter (e.g. "outbox")
	TopicPrefix    string        `json:"topic_prefix"`    // Prefix for auto-generated topics (default "sqlion.")
	DedicatedTable bool          `json:"dedicated_table"` // If true, extracts payload/topic from dedicated outbox schema
}

// DefaultDispatcherConfig returns standard production defaults.
func DefaultDispatcherConfig() DispatcherConfig {
	return DispatcherConfig{
		BatchSize:      1024,
		MaxRetries:     5,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     2 * time.Second,
		TopicPrefix:    "sqlion.",
	}
}

// OutboxCheckpoint represents persisted checkpoint state for at-least-once delivery.
type OutboxCheckpoint struct {
	LastAckLSN int64     `json:"last_ack_lsn"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// DispatcherStats captures operational metrics for observability.
type DispatcherStats struct {
	DispatchedCount int64     `json:"dispatched_count"`
	FailedCount     int64     `json:"failed_count"`
	RetriedCount    int64     `json:"retried_count"`
	LastAckLSN      int64     `json:"last_ack_lsn"`
	LastDispatchAt  time.Time `json:"last_dispatch_at"`
	SinkCount       int       `json:"sink_count"`
	Active          bool      `json:"active"`
}

// OutboxDispatcher manages CDC event consumption, payload formatting, retries, and broker dispatching.
type OutboxDispatcher struct {
	mu             sync.RWMutex
	streamer       *Streamer
	config         DispatcherConfig
	sinks          []BrokerSink
	lastAckLSN     atomic.Int64
	checkpointFile string

	dispatchedCount atomic.Int64
	failedCount     atomic.Int64
	retriedCount    atomic.Int64
	lastDispatchAt  time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	started atomic.Bool
	closed  atomic.Bool
	stopOnce sync.Once
}

// NewOutboxDispatcher constructs an OutboxDispatcher bound to a CDC streamer.
func NewOutboxDispatcher(streamer *Streamer, cfg DispatcherConfig) *OutboxDispatcher {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 1024
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 5
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 50 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 2 * time.Second
	}
	if cfg.TopicPrefix == "" {
		cfg.TopicPrefix = "sqlion."
	}

	d := &OutboxDispatcher{
		streamer: streamer,
		config:   cfg,
		sinks:    make([]BrokerSink, 0),
	}

	if cfg.DataDir != "" {
		cleanDir := filepath.Clean(cfg.DataDir)
		d.checkpointFile = filepath.Join(cleanDir, "outbox_checkpoint.json")
		d.loadCheckpoint()
	}

	return d
}

// RegisterSink registers a message broker sink. Safe to call before Start.
func (d *OutboxDispatcher) RegisterSink(sink BrokerSink) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sinks = append(d.sinks, sink)
}

// Sinks returns the registered sink list.
func (d *OutboxDispatcher) Sinks() []BrokerSink {
	d.mu.RLock()
	defer d.mu.RUnlock()
	copied := make([]BrokerSink, len(d.sinks))
	copy(copied, d.sinks)
	return copied
}

// LastAckLSN returns the latest acknowledged LSN safely persisted.
func (d *OutboxDispatcher) LastAckLSN() int64 {
	return d.lastAckLSN.Load()
}

// SetLastAckLSN manually sets the acknowledged LSN (useful for testing or manual repositioning).
func (d *OutboxDispatcher) SetLastAckLSN(lsn int64) {
	d.lastAckLSN.Store(lsn)
	d.saveCheckpoint(lsn)
}

// Stats returns a snapshot of runtime metrics.
func (d *OutboxDispatcher) Stats() DispatcherStats {
	d.mu.RLock()
	sinkCount := len(d.sinks)
	lastTime := d.lastDispatchAt
	d.mu.RUnlock()

	return DispatcherStats{
		DispatchedCount: d.dispatchedCount.Load(),
		FailedCount:     d.failedCount.Load(),
		RetriedCount:    d.retriedCount.Load(),
		LastAckLSN:      d.lastAckLSN.Load(),
		LastDispatchAt:  lastTime,
		SinkCount:       sinkCount,
		Active:          d.started.Load() && !d.closed.Load(),
	}
}

// Start initiates background CDC subscription and broker dispatching.
func (d *OutboxDispatcher) Start(parentCtx context.Context) error {
	if d.started.Swap(true) {
		return fmt.Errorf("outbox dispatcher already started")
	}
	if d.streamer == nil {
		return fmt.Errorf("cdc streamer is nil")
	}

	if parentCtx == nil {
		parentCtx = context.Background()
	}
	d.ctx, d.cancel = context.WithCancel(parentCtx)

	fromLSN := d.lastAckLSN.Load()
	ch, cancelSub, err := d.streamer.Subscribe(d.ctx, fromLSN, d.config.TableFilter, d.config.BatchSize)
	if err != nil {
		d.cancel()
		return fmt.Errorf("failed to subscribe to cdc streamer: %w", err)
	}

	d.wg.Add(1)
	go d.dispatchLoop(ch, cancelSub)

	return nil
}

// dispatchLoop continuously consumes ChangeEvents, converts them to OutboxMessages, and forwards to sinks.
func (d *OutboxDispatcher) dispatchLoop(ch <-chan ChangeEvent, cancelSub func()) {
	defer d.wg.Done()
	defer cancelSub()

	for {
		select {
		case <-d.ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			msg, err := d.EventToMessage(ev)
			if err != nil {
				d.failedCount.Add(1)
				continue
			}

			if err := d.publishToAllSinks(msg); err != nil {
				d.failedCount.Add(1)
				// Note: On failure, we do NOT advance lastAckLSN, guaranteeing at-least-once delivery
			} else {
				d.lastAckLSN.Store(ev.LSN)
				d.dispatchedCount.Add(1)
				d.mu.Lock()
				d.lastDispatchAt = time.Now()
				d.mu.Unlock()
				d.saveCheckpoint(ev.LSN)
			}
		}
	}
}

// EventToMessage converts a CDC ChangeEvent into an OutboxMessage.
func (d *OutboxDispatcher) EventToMessage(ev ChangeEvent) (*OutboxMessage, error) {
	isDedicated := d.config.DedicatedTable || strings.EqualFold(ev.Table, "outbox") || strings.EqualFold(ev.Table, "_outbox")

	if isDedicated && ev.Data != nil {
		return d.extractDedicatedOutboxMessage(ev)
	}

	// Standard general CDC event dispatching
	payload, err := ev.ToJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to serialize cdc event to json: %w", err)
	}

	topic := d.config.TopicPrefix + strings.ToLower(ev.Table)
	id := fmt.Sprintf("%d_%s", ev.LSN, ev.Key)
	if ev.Key == "" {
		id = fmt.Sprintf("%d_%d", ev.LSN, ev.Timestamp)
	}

	headers := map[string]string{
		"x-sqlion-table": ev.Table,
		"x-sqlion-op":    string(ev.Op),
		"x-sqlion-lsn":   strconv.FormatInt(ev.LSN, 10),
		"x-sqlion-ts":    strconv.FormatInt(ev.Timestamp, 10),
	}

	return &OutboxMessage{
		ID:        id,
		LSN:       ev.LSN,
		Topic:     topic,
		Key:       ev.Key,
		Payload:   payload,
		Headers:   headers,
		Timestamp: ev.Timestamp,
		Table:     ev.Table,
		Op:        ev.Op,
	}, nil
}

func (d *OutboxDispatcher) extractDedicatedOutboxMessage(ev ChangeEvent) (*OutboxMessage, error) {
	var id string
	var topic string
	var key string
	var payloadBytes []byte
	headers := make(map[string]string)

	for k, v := range ev.Data {
		kLower := strings.ToLower(k)
		switch kLower {
		case "id":
			id = fmt.Sprintf("%v", v)
		case "topic", "destination":
			topic = fmt.Sprintf("%v", v)
		case "key", "routing_key":
			key = fmt.Sprintf("%v", v)
		case "payload", "message", "body":
			switch pv := v.(type) {
			case string:
				payloadBytes = []byte(pv)
			case []byte:
				payloadBytes = pv
			default:
				if b, err := json.Marshal(pv); err == nil {
					payloadBytes = b
				}
			}
		case "headers":
			if strVal, ok := v.(string); ok && strVal != "" {
				var parsed map[string]string
				if err := json.Unmarshal([]byte(strVal), &parsed); err == nil {
					for hk, hv := range parsed {
						headers[hk] = hv
					}
				}
			}
		}
	}

	if topic == "" {
		topic = d.config.TopicPrefix + strings.ToLower(ev.Table)
	}
	if id == "" {
		id = fmt.Sprintf("%d_%s", ev.LSN, ev.Key)
	}
	if key == "" {
		key = ev.Key
	}
	if len(payloadBytes) == 0 {
		// Fallback to complete data object serialization
		if b, err := json.Marshal(ev.Data); err == nil {
			payloadBytes = b
		}
	}

	headers["x-sqlion-table"] = ev.Table
	headers["x-sqlion-op"] = string(ev.Op)
	headers["x-sqlion-lsn"] = strconv.FormatInt(ev.LSN, 10)

	return &OutboxMessage{
		ID:        id,
		LSN:       ev.LSN,
		Topic:     topic,
		Key:       key,
		Payload:   payloadBytes,
		Headers:   headers,
		Timestamp: ev.Timestamp,
		Table:     ev.Table,
		Op:        ev.Op,
	}, nil
}

// publishToAllSinks transmits the message across all sinks with exponential backoff retry.
func (d *OutboxDispatcher) publishToAllSinks(msg *OutboxMessage) error {
	d.mu.RLock()
	sinks := d.sinks
	d.mu.RUnlock()

	if len(sinks) == 0 {
		return nil
	}

	for _, sink := range sinks {
		if err := d.publishWithRetry(sink, msg); err != nil {
			return err
		}
	}
	return nil
}

func (d *OutboxDispatcher) publishWithRetry(sink BrokerSink, msg *OutboxMessage) error {
	var lastErr error
	backoff := d.config.InitialBackoff

	for attempt := 0; attempt <= d.config.MaxRetries; attempt++ {
		if attempt > 0 {
			d.retriedCount.Add(1)
			select {
			case <-d.ctx.Done():
				return d.ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > d.config.MaxBackoff {
				backoff = d.config.MaxBackoff
			}
		}

		if err := sink.Publish(d.ctx, msg); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}

	return fmt.Errorf("sink %s failed after %d retries: %w", sink.Name(), d.config.MaxRetries, lastErr)
}

// loadCheckpoint loads persisted LastAckLSN from disk.
func (d *OutboxDispatcher) loadCheckpoint() {
	if d.checkpointFile == "" {
		return
	}
	data, err := os.ReadFile(d.checkpointFile)
	if err != nil {
		return
	}
	var cp OutboxCheckpoint
	if err := json.Unmarshal(data, &cp); err == nil && cp.LastAckLSN > 0 {
		d.lastAckLSN.Store(cp.LastAckLSN)
	}
}

// saveCheckpoint saves LastAckLSN atomically using swap file (Golden Rule 1 & 5).
func (d *OutboxDispatcher) saveCheckpoint(lsn int64) {
	if d.checkpointFile == "" {
		return
	}

	cp := OutboxCheckpoint{
		LastAckLSN: lsn,
		UpdatedAt:  time.Now(),
	}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')

	cleanPath := filepath.Clean(d.checkpointFile)
	tmpPath := fmt.Sprintf("%s.tmp.%d", cleanPath, time.Now().UnixNano())

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return
	}
	if err := os.Rename(tmpPath, cleanPath); err != nil {
		_ = os.Remove(tmpPath)
	}
}

// Stop terminates the dispatcher, waits for current batch to flush, and cleans up sinks.
func (d *OutboxDispatcher) Stop() {
	d.stopOnce.Do(func() {
		d.closed.Store(true)
		if d.cancel != nil {
			d.cancel()
		}
		d.wg.Wait()

		// Save final checkpoint
		d.saveCheckpoint(d.lastAckLSN.Load())

		// Close all sinks cleanly (Golden Rule 12 & 20)
		d.mu.Lock()
		defer d.mu.Unlock()
		for _, sink := range d.sinks {
			_ = sink.Close()
		}
	})
}
