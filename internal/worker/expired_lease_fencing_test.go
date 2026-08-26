package worker

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
	"github.com/11DingKing/heatguard-field-ops/internal/storage/sqlite"
)

func TestExpiredLeaseCannotBeCompletedByOldWorker(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "lease.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	base := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	job := domain.WorkerJob{Kind: "lease-check", DedupeKey: "lease-fence-1", Payload: "{}", Status: domain.JobPending, MaxAttempts: 4, AvailableAt: base, CreatedAt: base, UpdatedAt: base}
	if _, err := store.InsertJob(ctx, &job); err != nil {
		t.Fatal(err)
	}

	aStarted := make(chan struct{})
	aRelease := make(chan struct{})
	runnerA := New(store, "worker-a", time.Hour, time.Minute, 1, nil, func() time.Time { return base })
	runnerA.Register("lease-check", func(context.Context, domain.WorkerJob) error {
		close(aStarted)
		<-aRelease
		return nil
	})
	aDone := make(chan error, 1)
	go func() { aDone <- runnerA.runBatch(ctx) }()
	<-aStarted

	bStarted := make(chan struct{})
	bRelease := make(chan struct{})
	bComplete := make(chan error, 1)
	runnerB := New(store, "worker-b", time.Hour, time.Minute, 1, nil, func() time.Time { return base.Add(2 * time.Minute) })
	runnerB.Register("lease-check", func(handlerCtx context.Context, got domain.WorkerJob) error {
		close(bStarted)
		<-bRelease
		err := store.CompleteJob(handlerCtx, got.ID, "worker-b", base.Add(2*time.Minute))
		bComplete <- err
		return err
	})
	bDone := make(chan error, 1)
	go func() { bDone <- runnerB.runBatch(ctx) }()
	<-bStarted

	close(aRelease)
	if err := <-aDone; err != nil {
		t.Fatalf("old worker batch = %v", err)
	}
	close(bRelease)
	if err := <-bComplete; err != nil {
		if errors.Is(err, domain.ErrVersionConflict) {
			t.Fatalf("new lease owner lost completion after old worker returned: %v", err)
		}
		t.Fatal(err)
	}
	if err := <-bDone; err != nil {
		t.Fatalf("new worker batch = %v", err)
	}

	jobs, err := store.LeaseJobs(ctx, repository.JobLease{Owner: "observer", Now: base.Add(2 * time.Minute), Duration: time.Minute, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("completed job was leased again: %+v", jobs)
	}
}
