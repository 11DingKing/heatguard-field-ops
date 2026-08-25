package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/notification"
)

func TestDelayedReceiptCannotDowngradeDeliveredNotification(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "receipt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	actor := domain.User{Email: "duty@receipt.test", DisplayName: "Duty", Role: domain.RoleDuty, PasswordHash: []byte("hash"), Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertUser(ctx, &actor); err != nil {
		t.Fatal(err)
	}
	route := domain.Route{Name: "Receipt Route", Zone: "north-park", ActivityKind: domain.ActivityRun, DistanceMeters: 3000, Active: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	segments := []domain.RouteSegment{
		{Sequence: 1, Name: "Assembly", Checkpoint: domain.GeoPoint{Latitude: 30.1, Longitude: 120.1}, HydrationSite: true, Version: 1},
		{Sequence: 2, Name: "Finish", Checkpoint: domain.GeoPoint{Latitude: 30.2, Longitude: 120.2}, Version: 1},
	}
	if err := store.InsertRoute(ctx, &route, segments); err != nil {
		t.Fatal(err)
	}
	leader := domain.Leader{UserID: actor.ID, Kinds: []domain.ActivityKind{domain.ActivityRun}, QualifiedUntil: now.AddDate(1, 0, 0), EmergencyTrained: true, Version: 1}
	if err := store.InsertLeader(ctx, &leader); err != nil {
		t.Fatal(err)
	}
	wave := domain.ActivityWave{RouteID: route.ID, LeaderID: leader.ID, Name: "Morning Run", ScheduledStart: now.Add(time.Hour), ExpectedEnd: now.Add(2 * time.Hour), Capacity: 8, State: domain.WaveActive, DepartureRisk: domain.RiskHigh, Version: 1, CreatedBy: actor.ID, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertWave(ctx, &wave); err != nil {
		t.Fatal(err)
	}
	alert := domain.Alert{WaveID: wave.ID, Kind: "participant_missing", Severity: domain.RiskExtreme, Status: domain.AlertOpen, DedupeKey: "missing:receipt", OpenedAt: now, Version: 1}
	if _, err := store.InsertAlert(ctx, &alert); err != nil {
		t.Fatal(err)
	}
	providerKey := "provider-receipt-42"
	if _, fresh, err := store.InsertNotificationAttempt(ctx, alert.ID, "13800000000", "sms", providerKey, now); err != nil || !fresh {
		t.Fatalf("InsertNotificationAttempt fresh=%v err=%v", fresh, err)
	}

	service := notification.New(store, nil, func() time.Time { return now.Add(30 * time.Minute) })
	deliveredAt := now.Add(20 * time.Minute)
	if err := service.Receipt(ctx, providerKey, "delivered", deliveredAt); err != nil {
		t.Fatal(err)
	}
	if err := service.Receipt(ctx, providerKey, "failed", now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	var status, receiptAt string
	if err := store.db.QueryRowContext(ctx, `SELECT status, last_receipt_at FROM notification_deliveries WHERE provider_key = ?`, providerKey).Scan(&status, &receiptAt); err != nil {
		t.Fatal(err)
	}
	if status != "delivered" {
		t.Fatalf("late failure downgraded delivered notification to %q", status)
	}
	parsed, err := parseTime(receiptAt)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Equal(deliveredAt) {
		t.Fatalf("receipt event time = %s, want %s", parsed, deliveredAt)
	}
}
