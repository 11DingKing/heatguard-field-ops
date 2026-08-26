package sqlite

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/notification"
)

type countingSender struct{ calls int }

func (s *countingSender) Send(context.Context, string, string) (string, error) {
	s.calls++
	return "provider-replay", nil
}

func TestNotificationReplayDoesNotSendAgain(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "notify.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 8, 25, 11, 0, 0, 0, time.UTC)
	actor := domain.User{Email: "duty@replay.test", DisplayName: "Duty", Role: domain.RoleDuty, PasswordHash: []byte("hash"), Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertUser(ctx, &actor); err != nil {
		t.Fatal(err)
	}
	route := domain.Route{Name: "Replay Route", Zone: "south-park", ActivityKind: domain.ActivityKind("run"), DistanceMeters: 3000, Active: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertRoute(ctx, &route, []domain.RouteSegment{{Sequence: 1, Name: "Start", Checkpoint: domain.GeoPoint{Latitude: 30, Longitude: 120}, Version: 1}, {Sequence: 2, Name: "Finish", Checkpoint: domain.GeoPoint{Latitude: 30.1, Longitude: 120.1}, Version: 1}}); err != nil {
		t.Fatal(err)
	}
	leader := domain.Leader{UserID: actor.ID, Kinds: []domain.ActivityKind{domain.ActivityRun}, QualifiedUntil: now.Add(time.Hour), EmergencyTrained: true, Version: 1}
	if err := store.InsertLeader(ctx, &leader); err != nil {
		t.Fatal(err)
	}
	wave := domain.ActivityWave{RouteID: route.ID, LeaderID: leader.ID, Name: "Replay Wave", ScheduledStart: now, ExpectedEnd: now.Add(time.Hour), Capacity: 2, State: domain.WaveActive, DepartureRisk: domain.RiskLow, Version: 1, CreatedBy: actor.ID, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertWave(ctx, &wave); err != nil {
		t.Fatal(err)
	}
	alert := domain.Alert{WaveID: wave.ID, Kind: "participant_missing", Severity: domain.RiskExtreme, Status: domain.AlertOpen, DedupeKey: "replay:alert", OpenedAt: now, Version: 1}
	if _, err := store.InsertAlert(ctx, &alert); err != nil {
		t.Fatal(err)
	}
	sender := &countingSender{}
	service := notification.New(store, sender, func() time.Time { return now })
	if err := service.Notify(ctx, alert.ID, "13800000000", "sms"); err != nil {
		t.Fatal(err)
	}
	providerKey := "alert-" + formatInt(alert.ID) + "-sms-13800000000"
	if err := service.Receipt(ctx, providerKey, "delivered", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := service.Notify(ctx, alert.ID, "13800000000", "sms"); err != nil {
		t.Fatal(err)
	}
	if sender.calls != 1 {
		t.Fatalf("sender calls = %d, want one initial delivery", sender.calls)
	}
	var status string
	var attempts int
	if err := store.db.QueryRowContext(ctx, `SELECT status, attempt_count FROM notification_deliveries WHERE provider_key = ?`, providerKey).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "delivered" || attempts != 1 {
		t.Fatalf("replayed delivery = status %q attempts %d, want delivered/1", status, attempts)
	}
}

func formatInt(value int64) string {
	return strconv.FormatInt(value, 10)
}
