package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
)

type Handler func(context.Context, domain.WorkerJob) error

type Runner struct {
	store       repository.Store
	owner       string
	poll        time.Duration
	lease       time.Duration
	concurrency int
	logger      *slog.Logger
	now         func() time.Time
	mu          sync.RWMutex
	handlers    map[string]Handler
}

func New(store repository.Store, owner string, poll, lease time.Duration, concurrency int, logger *slog.Logger, now func() time.Time) *Runner {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	if concurrency < 1 {
		concurrency = 1
	}
	return &Runner{store: store, owner: owner, poll: poll, lease: lease, concurrency: concurrency, logger: logger, now: now, handlers: map[string]Handler{}}
}

func (r *Runner) Register(kind string, handler Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[kind] = handler
}

func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.runBatch(ctx); err != nil && !errors.Is(err, context.Canceled) {
				r.logger.Error("worker batch failed", "error", err)
			}
		}
	}
}

func (r *Runner) runBatch(ctx context.Context) error {
	jobs, err := r.store.LeaseJobs(ctx, repository.JobLease{Owner: r.owner, Now: r.now().UTC(), Duration: r.lease, Limit: r.concurrency})
	if err != nil {
		return err
	}
	var group sync.WaitGroup
	errs := make(chan error, len(jobs))
	for _, job := range jobs {
		job := job
		group.Add(1)
		go func() {
			defer group.Done()
			if err := r.execute(ctx, job); err != nil {
				errs <- err
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		r.logger.Warn("worker job failed", "error", err)
	}
	return nil
}

func (r *Runner) execute(parent context.Context, job domain.WorkerJob) error {
	r.mu.RLock()
	handler := r.handlers[job.Kind]
	r.mu.RUnlock()
	if handler == nil {
		handler = func(context.Context, domain.WorkerJob) error { return fmt.Errorf("unsupported job kind %q", job.Kind) }
	}
	ctx, cancel := context.WithTimeout(parent, r.lease/2)
	defer cancel()
	err := handler(ctx, job)
	now := r.now().UTC()
	if err == nil {
		return r.store.CompleteJob(parent, job.ID, r.owner, now)
	}
	delay := time.Duration(math.Min(math.Pow(2, float64(job.Attempts)), 300)) * time.Second
	if persistErr := r.store.FailJob(parent, job.ID, r.owner, now, now.Add(delay), err); persistErr != nil {
		return fmt.Errorf("job %d failed: %v; persist: %w", job.ID, err, persistErr)
	}
	return fmt.Errorf("job %d: %w", job.ID, err)
}

type AlertPayload struct {
	AlertID       int64 `json:"alert_id"`
	ParticipantID int64 `json:"participant_id"`
}

func DecodeAlertPayload(job domain.WorkerJob) (AlertPayload, error) {
	var payload AlertPayload
	if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
		return AlertPayload{}, fmt.Errorf("decode job payload: %w", err)
	}
	if payload.AlertID <= 0 {
		return AlertPayload{}, errors.New("job payload lacks alert_id")
	}
	return payload, nil
}
