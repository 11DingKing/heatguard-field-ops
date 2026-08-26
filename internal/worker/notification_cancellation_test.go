package worker

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/notification"
	"github.com/11DingKing/heatguard-field-ops/internal/storage/sqlite"
)

type cancellationSender struct {
	started chan struct{}
	release chan struct{}
	stopped chan struct{}
}

func (s *cancellationSender) Send(ctx context.Context, _, _ string) (string, error) {
	close(s.started)
	select {
	case <-ctx.Done():
		close(s.stopped)
		return "", ctx.Err()
	case <-s.release:
		return "provider-id", nil
	}
}

func TestCancelledWorkerStopsEmergencyNotification(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	organizer := domain.User{Email: "organizer@example.test", DisplayName: "Organizer", Role: domain.RoleOrganizer, PasswordHash: []byte("hash"), Active: true, CreatedAt: now, UpdatedAt: now}
	coach := domain.User{Email: "coach@example.test", DisplayName: "Coach", Role: domain.RoleCoach, PasswordHash: []byte("hash"), Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertUser(ctx, &organizer); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertUser(ctx, &coach); err != nil {
		t.Fatal(err)
	}
	route := domain.Route{Name: "Cancellation Route", Zone: "north", ActivityKind: domain.ActivityRun, DistanceMeters: 3000, Active: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertRoute(ctx, &route, nil); err != nil {
		t.Fatal(err)
	}
	leader := domain.Leader{UserID: coach.ID, Kinds: []domain.ActivityKind{domain.ActivityRun}, QualifiedUntil: now.AddDate(1, 0, 0), Version: 1}
	if err := store.InsertLeader(ctx, &leader); err != nil {
		t.Fatal(err)
	}
	wave := domain.ActivityWave{RouteID: route.ID, LeaderID: leader.ID, Name: "Cancellation Wave", ScheduledStart: now, ExpectedEnd: now.Add(time.Hour), Capacity: 3, State: domain.WaveActive, DepartureRisk: domain.RiskLow, Version: 1, CreatedBy: organizer.ID, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertWave(ctx, &wave); err != nil {
		t.Fatal(err)
	}
	alert := domain.Alert{WaveID: wave.ID, Kind: "participant_missing", Severity: domain.RiskExtreme, Status: domain.AlertOpen, DedupeKey: "missing-cancel", OpenedAt: now, Version: 1}
	if _, err := store.InsertAlert(ctx, &alert); err != nil {
		t.Fatal(err)
	}
	job := domain.WorkerJob{Kind: "notify", DedupeKey: "notify-cancel", Payload: `{}`, Status: domain.JobPending, MaxAttempts: 3, AvailableAt: now, CreatedAt: now, UpdatedAt: now}
	if _, err := store.InsertJob(ctx, &job); err != nil {
		t.Fatal(err)
	}

	sender := &cancellationSender{started: make(chan struct{}), release: make(chan struct{}), stopped: make(chan struct{})}
	notifier := notification.New(store, sender, func() time.Time { return now })
	runner := New(store, "cancel-worker", time.Millisecond, 10*time.Second, 1, slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return now })
	runner.Register("notify", func(handlerCtx context.Context, _ domain.WorkerJob) error {
		return notifier.Notify(handlerCtx, alert.ID, "13800000000", "sms")
	})
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- runner.runBatch(workerCtx) }()
	<-sender.started
	cancel()
	select {
	case <-sender.stopped:
	case <-time.After(200 * time.Millisecond):
		close(sender.release)
		<-done
		t.Fatal("notification sender remained active after worker cancellation")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
