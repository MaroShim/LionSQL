package cdc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MemorySink provides an in-memory, thread-safe broker sink primarily for testing and embedded streaming.
type MemorySink struct {
	mu           sync.RWMutex
	name         string
	messages     []*OutboxMessage
	notifyCh     chan struct{}
	failUntil    int
	injectedErr  error
	publishCount int
}

// NewMemorySink creates a new MemorySink.
func NewMemorySink(name string) *MemorySink {
	if name == "" {
		name = "memory_sink"
	}
	return &MemorySink{
		name:     name,
		messages: make([]*OutboxMessage, 0),
		notifyCh: make(chan struct{}, 1),
	}
}

func (s *MemorySink) Name() string {
	return s.name
}

// Publish stores the message in memory or returns an injected failure.
func (s *MemorySink) Publish(ctx context.Context, msg *OutboxMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.publishCount++
	if s.publishCount <= s.failUntil && s.injectedErr != nil {
		return s.injectedErr
	}

	copied := *msg
	s.messages = append(s.messages, &copied)

	// Non-blocking notification for waiting tests (Golden Rule 29)
	select {
	case s.notifyCh <- struct{}{}:
	default:
	}

	return nil
}

// Close is a no-op for MemorySink.
func (s *MemorySink) Close() error {
	return nil
}

// Messages returns a copy of all published messages.
func (s *MemorySink) Messages() []*OutboxMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]*OutboxMessage, len(s.messages))
	copy(res, s.messages)
	return res
}

// Count returns the number of published messages.
func (s *MemorySink) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.messages)
}

// SetFailUntil configures simulated transient failure for the first N publish attempts.
func (s *MemorySink) SetFailUntil(attempts int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failUntil = attempts
	s.injectedErr = err
	s.publishCount = 0
}

// WaitForMessages waits until at least count messages are received or timeout expires (Golden Rule 17).
func (s *MemorySink) WaitForMessages(count int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if s.Count() >= count {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return s.Count() >= count
		}
		select {
		case <-s.notifyCh:
		case <-time.After(remaining):
			return s.Count() >= count
		}
	}
}

// WebhookSink forwards outbox messages to an external HTTP webhook endpoint.
type WebhookSink struct {
	name       string
	targetURL  string
	headers    map[string]string
	authToken  string
	httpClient *http.Client
	closed     atomic.Bool
}

// WebhookConfig specifies settings for WebhookSink.
type WebhookConfig struct {
	Name       string
	TargetURL  string
	Headers    map[string]string
	AuthToken  string
	Timeout    time.Duration
	HTTPClient *http.Client
}

// NewWebhookSink creates a new WebhookSink.
func NewWebhookSink(cfg WebhookConfig) (*WebhookSink, error) {
	if strings.TrimSpace(cfg.TargetURL) == "" {
		return nil, fmt.Errorf("webhook target url cannot be empty")
	}
	name := cfg.Name
	if name == "" {
		name = fmt.Sprintf("webhook_%s", cfg.TargetURL)
	}
	client := cfg.HTTPClient
	if client == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}

	return &WebhookSink{
		name:       name,
		targetURL:  cfg.TargetURL,
		headers:    cfg.Headers,
		authToken:  cfg.AuthToken,
		httpClient: client,
	}, nil
}

func (w *WebhookSink) Name() string {
	return w.name
}

// Publish POSTs the OutboxMessage payload as JSON to the webhook URL.
func (w *WebhookSink) Publish(ctx context.Context, msg *OutboxMessage) error {
	if w.closed.Load() {
		return fmt.Errorf("webhook sink is closed")
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal outbox message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.targetURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to construct webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "SQLion-Outbox-Dispatcher/1.0")

	if w.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+w.authToken)
	}
	for k, v := range w.headers {
		req.Header.Set(k, v)
	}
	for k, v := range msg.Headers {
		req.Header.Set("X-SQLion-"+k, v)
	}

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook http dispatch error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("webhook returned non-2xx status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (w *WebhookSink) Close() error {
	w.closed.Store(true)
	return nil
}

// KafkaRESTSink publishes messages to a Kafka REST Proxy (e.g., Confluent REST, Strimzi, Upstash Kafka).
type KafkaRESTSink struct {
	name          string
	endpoint      string
	topicOverride string
	apiKey        string
	httpClient    *http.Client
	closed        atomic.Bool
}

// KafkaRESTConfig specifies connection settings for Kafka REST proxy.
type KafkaRESTConfig struct {
	Name          string
	Endpoint      string // e.g. "http://localhost:8082"
	TopicOverride string // optional default topic
	APIKey        string // optional Bearer token / Basic auth
	Timeout       time.Duration
	HTTPClient    *http.Client
}

// NewKafkaRESTSink creates a KafkaRESTSink.
func NewKafkaRESTSink(cfg KafkaRESTConfig) (*KafkaRESTSink, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("kafka rest endpoint cannot be empty")
	}
	name := cfg.Name
	if name == "" {
		name = fmt.Sprintf("kafka_%s", cfg.Endpoint)
	}
	endpoint := strings.TrimRight(cfg.Endpoint, "/")

	client := cfg.HTTPClient
	if client == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}

	return &KafkaRESTSink{
		name:          name,
		endpoint:      endpoint,
		topicOverride: cfg.TopicOverride,
		apiKey:        cfg.APIKey,
		httpClient:    client,
	}, nil
}

func (k *KafkaRESTSink) Name() string {
	return k.name
}

// Publish sends message to Kafka REST endpoint POST /topics/{topic}.
func (k *KafkaRESTSink) Publish(ctx context.Context, msg *OutboxMessage) error {
	if k.closed.Load() {
		return fmt.Errorf("kafka rest sink is closed")
	}

	targetTopic := msg.Topic
	if k.topicOverride != "" {
		targetTopic = k.topicOverride
	}

	url := fmt.Sprintf("%s/topics/%s", k.endpoint, targetTopic)

	// Confluent Kafka REST v2 schema format
	type recordItem struct {
		Key   string `json:"key,omitempty"`
		Value any    `json:"value"`
	}
	type kafkaPayload struct {
		Records []recordItem `json:"records"`
	}

	var rawValue any
	if err := json.Unmarshal(msg.Payload, &rawValue); err != nil {
		rawValue = string(msg.Payload)
	}

	payload := kafkaPayload{
		Records: []recordItem{
			{
				Key:   msg.Key,
				Value: rawValue,
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal kafka rest payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create kafka rest request: %w", err)
	}

	req.Header.Set("Content-Type", "application/vnd.kafka.json.v2+json")
	req.Header.Set("Accept", "application/vnd.kafka.v2+json, application/vnd.kafka+json, application/json")

	if k.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+k.apiKey)
	}

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("kafka rest request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("kafka rest returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (k *KafkaRESTSink) Close() error {
	k.closed.Store(true)
	return nil
}

// RabbitMQHTTPSink publishes messages to RabbitMQ Management HTTP API.
type RabbitMQHTTPSink struct {
	name       string
	endpoint   string
	vhost      string
	exchange   string
	username   string
	password   string
	httpClient *http.Client
	closed     atomic.Bool
}

// RabbitMQHTTPConfig specifies connection settings for RabbitMQ HTTP API.
type RabbitMQHTTPConfig struct {
	Name       string
	Endpoint   string // e.g. "http://localhost:15672"
	VHost      string // default: "%2f" (/)
	Exchange   string // default: "amq.topic"
	Username   string // default: "guest"
	Password   string // default: "guest"
	Timeout    time.Duration
	HTTPClient *http.Client
}

// NewRabbitMQHTTPSink creates a RabbitMQHTTPSink.
func NewRabbitMQHTTPSink(cfg RabbitMQHTTPConfig) (*RabbitMQHTTPSink, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("rabbitmq endpoint cannot be empty")
	}
	name := cfg.Name
	if name == "" {
		name = fmt.Sprintf("rabbitmq_%s", cfg.Endpoint)
	}
	endpoint := strings.TrimRight(cfg.Endpoint, "/")

	vhost := strings.Trim(cfg.VHost, "/")
	if vhost == "" || strings.EqualFold(vhost, "%2f") {
		vhost = "%2f"
	}
	exchange := cfg.Exchange
	if exchange == "" {
		exchange = "amq.topic"
	}
	username := cfg.Username
	if username == "" {
		username = "guest"
	}
	password := cfg.Password
	if password == "" {
		password = "guest"
	}

	client := cfg.HTTPClient
	if client == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}

	return &RabbitMQHTTPSink{
		name:       name,
		endpoint:   endpoint,
		vhost:      vhost,
		exchange:   exchange,
		username:   username,
		password:   password,
		httpClient: client,
	}, nil
}

func (r *RabbitMQHTTPSink) Name() string {
	return r.name
}

// Publish sends message to RabbitMQ Management API POST /api/exchanges/{vhost}/{exchange}/publish.
func (r *RabbitMQHTTPSink) Publish(ctx context.Context, msg *OutboxMessage) error {
	if r.closed.Load() {
		return fmt.Errorf("rabbitmq http sink is closed")
	}

	url := fmt.Sprintf("%s/api/exchanges/%s/%s/publish", r.endpoint, r.vhost, r.exchange)

	type rabbitPayload struct {
		Properties      map[string]any `json:"properties"`
		RoutingKey      string         `json:"routing_key"`
		Payload         string         `json:"payload"`
		PayloadEncoding string         `json:"payload_encoding"`
	}

	routingKey := msg.Topic
	if msg.Key != "" {
		routingKey = fmt.Sprintf("%s.%s", msg.Topic, msg.Key)
	}

	headers := make(map[string]any, len(msg.Headers))
	for k, v := range msg.Headers {
		headers[k] = v
	}

	payload := rabbitPayload{
		Properties: map[string]any{
			"content_type": "application/json",
			"headers":      headers,
			"timestamp":    msg.Timestamp,
			"message_id":   msg.ID,
		},
		RoutingKey:      routingKey,
		Payload:         string(msg.Payload),
		PayloadEncoding: "string",
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal rabbitmq payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create rabbitmq request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	auth := base64.StdEncoding.EncodeToString([]byte(r.username + ":" + r.password))
	req.Header.Set("Authorization", "Basic "+auth)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("rabbitmq request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("rabbitmq returned status %d: %s", resp.StatusCode, string(respBody))
	}

	// RabbitMQ publish API returns {"routed": true/false}
	type rabbitPublishResponse struct {
		Routed bool `json:"routed"`
	}
	var res rabbitPublishResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err == nil {
		// Even if routed is false (no queue bound to topic), message was accepted by exchange
		_ = res
	}

	return nil
}

func (r *RabbitMQHTTPSink) Close() error {
	r.closed.Store(true)
	return nil
}
