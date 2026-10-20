package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CronJob defines a scheduled recurring job executed by the internal CronScheduler.
type CronJob struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Schedule       string    `json:"schedule"`     // Standard 5-field cron: "min hour dom month dow"
	CommandType    string    `json:"command_type"` // "sql" or "procedure"
	Command        string    `json:"command"`      // SQL statement or Stored Procedure name
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
	LastRun        time.Time `json:"last_run,omitempty"`
	LastStatus     string    `json:"last_status,omitempty"` // "success" or "error"
	LastDurationMs int64     `json:"last_duration_ms,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
}

// CronExecutor is a callback function invoked by the scheduler when a job triggers.
type CronExecutor func(ctx context.Context, commandType, command string) error

// CronScheduler manages and evaluates recurring cron jobs in LionSQL.
type CronScheduler struct {
	mu           sync.RWMutex
	jobs         map[string]*CronJob
	executor     CronExecutor
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	lastRanKey   map[string]string // jobID -> "YYYY-MM-DD HH:MM" to prevent duplicate runs within same minute
	tickInterval time.Duration
}

// NewCronScheduler creates an initialized CronScheduler.
func NewCronScheduler(executor CronExecutor) *CronScheduler {
	return &CronScheduler{
		jobs:         make(map[string]*CronJob),
		executor:     executor,
		lastRanKey:   make(map[string]string),
		tickInterval: 1 * time.Second,
	}
}

// SetTickInterval overrides the default 1s ticker (useful for deterministic fast tests).
func (cs *CronScheduler) SetTickInterval(d time.Duration) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.tickInterval = d
}

// Start launches the background scheduler loop.
func (cs *CronScheduler) Start(parentCtx context.Context) {
	cs.mu.Lock()
	if cs.cancel != nil {
		cs.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parentCtx)
	cs.cancel = cancel
	cs.wg.Add(1)
	interval := cs.tickInterval
	cs.mu.Unlock()

	go func() {
		defer cs.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				cs.evaluate(ctx, t)
			}
		}
	}()
}

// Stop gracefully shuts down the background scheduler.
func (cs *CronScheduler) Stop() {
	cs.mu.Lock()
	cancel := cs.cancel
	cs.cancel = nil
	cs.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	cs.wg.Wait()
}

// evaluate checks all registered jobs against current time t.
func (cs *CronScheduler) evaluate(ctx context.Context, t time.Time) {
	minuteKey := t.Format("2006-01-02 15:04")

	cs.mu.RLock()
	var dueJobs []*CronJob
	for _, job := range cs.jobs {
		if !job.Enabled {
			continue
		}
		if cs.lastRanKey[job.ID] == minuteKey {
			continue
		}
		if matchCron(job.Schedule, t) {
			dueJobs = append(dueJobs, job)
		}
	}
	cs.mu.RUnlock()

	for _, job := range dueJobs {
		cs.mu.Lock()
		cs.lastRanKey[job.ID] = minuteKey
		cs.mu.Unlock()

		go cs.executeJob(ctx, job)
	}
}

func (cs *CronScheduler) executeJob(ctx context.Context, job *CronJob) {
	start := time.Now()
	var err error
	if cs.executor != nil {
		execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = cs.executor(execCtx, job.CommandType, job.Command)
		cancel()
	}

	duration := time.Since(start).Milliseconds()

	cs.mu.Lock()
	defer cs.mu.Unlock()
	job.LastRun = start
	job.LastDurationMs = duration
	if err != nil {
		job.LastStatus = "error"
		job.LastError = err.Error()
	} else {
		job.LastStatus = "success"
		job.LastError = ""
	}
}

// RunJobNow triggers an immediate manual execution of a specific cron job.
func (cs *CronScheduler) RunJobNow(ctx context.Context, id string) error {
	cs.mu.RLock()
	job, exists := cs.jobs[id]
	cs.mu.RUnlock()
	if !exists {
		return fmt.Errorf("cron job %q not found", id)
	}

	cs.executeJob(ctx, job)
	return nil
}

// AddJob registers a new cron job after validating its schedule.
func (cs *CronScheduler) AddJob(job *CronJob) error {
	if job == nil {
		return fmt.Errorf("job cannot be nil")
	}
	if strings.TrimSpace(job.Name) == "" {
		return fmt.Errorf("job name is required")
	}
	if err := validateCronExpression(job.Schedule); err != nil {
		return fmt.Errorf("invalid cron schedule: %w", err)
	}
	cmdType := strings.ToLower(strings.TrimSpace(job.CommandType))
	if cmdType != "sql" && cmdType != "procedure" {
		return fmt.Errorf("command_type must be 'sql' or 'procedure'")
	}
	if strings.TrimSpace(job.Command) == "" {
		return fmt.Errorf("command is required")
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	if job.ID == "" {
		job.ID = generateCronID()
	}
	job.CommandType = cmdType
	job.CreatedAt = time.Now()

	cs.jobs[job.ID] = job
	return nil
}

// UpdateJob updates an existing cron job.
func (cs *CronScheduler) UpdateJob(job *CronJob) error {
	if job == nil || job.ID == "" {
		return fmt.Errorf("job ID required")
	}
	if err := validateCronExpression(job.Schedule); err != nil {
		return fmt.Errorf("invalid cron schedule: %w", err)
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	existing, exists := cs.jobs[job.ID]
	if !exists {
		return fmt.Errorf("cron job %q not found", job.ID)
	}

	existing.Name = job.Name
	existing.Schedule = job.Schedule
	existing.CommandType = strings.ToLower(job.CommandType)
	existing.Command = job.Command
	existing.Enabled = job.Enabled
	return nil
}

// DeleteJob removes a cron job by ID.
func (cs *CronScheduler) DeleteJob(id string) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if _, exists := cs.jobs[id]; !exists {
		return fmt.Errorf("cron job %q not found", id)
	}
	delete(cs.jobs, id)
	delete(cs.lastRanKey, id)
	return nil
}

// GetJob returns a copy of a job by ID.
func (cs *CronScheduler) GetJob(id string) (*CronJob, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	j, exists := cs.jobs[id]
	if !exists {
		return nil, false
	}
	cpy := *j
	return &cpy, true
}

// ListJobs returns all registered cron jobs.
func (cs *CronScheduler) ListJobs() []*CronJob {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	res := make([]*CronJob, 0, len(cs.jobs))
	for _, j := range cs.jobs {
		cpy := *j
		res = append(res, &cpy)
	}
	return res
}

func generateCronID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "cron-" + hex.EncodeToString(b)
}

// -------------------------------------------------------------------------
// Cron Expression Matcher & Validator (Standard 5-field: min hour dom month dow)
// -------------------------------------------------------------------------

func validateCronExpression(expr string) error {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return fmt.Errorf("expected 5 fields, got %d", len(fields))
	}
	if _, err := parseCronField(fields[0], 0, 59); err != nil {
		return fmt.Errorf("invalid minute field: %w", err)
	}
	if _, err := parseCronField(fields[1], 0, 23); err != nil {
		return fmt.Errorf("invalid hour field: %w", err)
	}
	if _, err := parseCronField(fields[2], 1, 31); err != nil {
		return fmt.Errorf("invalid day-of-month field: %w", err)
	}
	if _, err := parseCronField(fields[3], 1, 12); err != nil {
		return fmt.Errorf("invalid month field: %w", err)
	}
	if _, err := parseCronField(fields[4], 0, 7); err != nil {
		return fmt.Errorf("invalid day-of-week field: %w", err)
	}
	return nil
}

func matchCron(expr string, t time.Time) bool {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return false
	}

	minuteMatches := matchField(fields[0], t.Minute(), 0, 59)
	hourMatches := matchField(fields[1], t.Hour(), 0, 23)
	domMatches := matchField(fields[2], t.Day(), 1, 31)
	monthMatches := matchField(fields[3], int(t.Month()), 1, 12)

	weekday := int(t.Weekday()) // 0=Sunday
	dowMatches := matchField(fields[4], weekday, 0, 7)
	if !dowMatches && fields[4] == "7" && weekday == 0 {
		dowMatches = true
	}

	return minuteMatches && hourMatches && domMatches && monthMatches && dowMatches
}

func matchField(field string, value, min, max int) bool {
	if field == "*" {
		return true
	}

	parts := strings.Split(field, ",")
	for _, part := range parts {
		if strings.HasPrefix(part, "*/") {
			step, err := strconv.Atoi(part[2:])
			if err == nil && step > 0 {
				if (value-min)%step == 0 {
					return true
				}
			}
			continue
		}

		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) == 2 {
				start, err1 := strconv.Atoi(rangeParts[0])
				end, err2 := strconv.Atoi(rangeParts[1])
				if err1 == nil && err2 == nil {
					if value >= start && value <= end {
						return true
					}
				}
			}
			continue
		}

		num, err := strconv.Atoi(part)
		if err == nil && num == value {
			return true
		}
	}

	return false
}

func parseCronField(field string, min, max int) (bool, error) {
	if field == "*" {
		return true, nil
	}
	parts := strings.Split(field, ",")
	for _, part := range parts {
		if strings.HasPrefix(part, "*/") {
			step, err := strconv.Atoi(part[2:])
			if err != nil || step <= 0 {
				return false, fmt.Errorf("invalid step: %s", part)
			}
			continue
		}
		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 {
				return false, fmt.Errorf("invalid range: %s", part)
			}
			start, err1 := strconv.Atoi(rangeParts[0])
			end, err2 := strconv.Atoi(rangeParts[1])
			if err1 != nil || err2 != nil || start < min || end > max || start > end {
				return false, fmt.Errorf("invalid range bounds: %s", part)
			}
			continue
		}
		num, err := strconv.Atoi(part)
		if err != nil || num < min || num > max {
			return false, fmt.Errorf("value out of range [%d, %d]: %s", min, max, part)
		}
	}
	return true, nil
}
