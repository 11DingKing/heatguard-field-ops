package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
	"github.com/11DingKing/heatguard-field-ops/internal/storage/sqlite"
)

func workerFixture(t *testing.T) (*Runner, *sqlite.Store, *time.Time) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "worker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runner := New(store, "worker-test", time.Millisecond, time.Second, 2, logger, func() time.Time { return now })
	return runner, store, &now
}

func insertWorkerJob(t *testing.T, store *sqlite.Store, kind, key string, maxAttempts int, at time.Time) domain.WorkerJob {
	t.Helper()
	job := domain.WorkerJob{Kind: kind, DedupeKey: key, Payload: `{"alert_id":42,"participant_id":7}`, Status: domain.JobPending, MaxAttempts: maxAttempts, AvailableAt: at, CreatedAt: at, UpdatedAt: at}
	created, err := store.InsertJob(context.Background(), &job)
	if err != nil || !created {
		t.Fatalf("InsertJob = %v, %v", created, err)
	}
	return job
}

func TestRunBatchCompletesHandledJob(t *testing.T) {
	runner, store, now := workerFixture(t)
	job := insertWorkerJob(t, store, "notify", "notify-1", 3, *now)
	called := make(chan domain.WorkerJob, 1)
	runner.Register("notify", func(ctx context.Context, got domain.WorkerJob) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			called <- got
			return nil
		}
	})
	if err := runner.runBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-called:
		if got.ID != job.ID || got.Attempts != 1 {
			t.Fatalf("handled job = %+v", got)
		}
	default:
		t.Fatal("handler was not called")
	}
	leased, err := store.LeaseJobs(context.Background(), repository.JobLease{Owner: "other", Now: now.Add(time.Hour), Duration: time.Minute, Limit: 10})
	if err != nil || len(leased) != 0 {
		t.Fatalf("completed job leased again: %+v, %v", leased, err)
	}
}

func TestRunBatchPersistsRetry(t *testing.T) {
	runner, store, now := workerFixture(t)
	insertWorkerJob(t, store, "temporary", "temporary-1", 3, *now)
	sentinel := errors.New("provider unavailable")
	runner.Register("temporary", func(context.Context, domain.WorkerJob) error { return sentinel })
	if err := runner.runBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(3 * time.Second)
	leased, err := store.LeaseJobs(context.Background(), repository.JobLease{Owner: "retry", Now: *now, Duration: time.Minute, Limit: 10})
	if err != nil || len(leased) != 1 || leased[0].Attempts != 2 {
		t.Fatalf("retry lease = %+v, %v", leased, err)
	}
}

func TestRunReturnsOnCancellation(t *testing.T) {
	runner, _, _ := workerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not stop after cancellation")
	}
}

func TestDecodeAlertPayload(t *testing.T) {
	tests := []struct {
		name, payload string
		wantID        int64
		bad           bool
	}{
		{"valid", `{"alert_id":42,"participant_id":7}`, 42, false},
		{"missing alert", `{"participant_id":7}`, 0, true},
		{"invalid json", `{`, 0, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := DecodeAlertPayload(domain.WorkerJob{Payload: test.payload})
			if test.bad && err == nil {
				t.Fatal("expected error")
			}
			if !test.bad && (err != nil || payload.AlertID != test.wantID) {
				t.Fatalf("payload = %+v, %v", payload, err)
			}
		})
	}
}
