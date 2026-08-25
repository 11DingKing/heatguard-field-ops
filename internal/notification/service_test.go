package notification

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/storage/sqlite"
)

func fixedTime() time.Time { return time.Date(2026, 8, 24, 8, 30, 0, 0, time.UTC) }

// alertFixture persists the dependency chain (user, route, leader, wave, alert)
// so notification_deliveries.alert_id satisfies its foreign key.
func alertFixture(t *testing.T, store *sqlite.Store) domain.Alert {
	t.Helper()
	now := fixedTime()
	user := domain.User{Email: "notify@example.test", DisplayName: "Notifier", Role: domain.RoleOrganizer, PasswordHash: []byte("hash"), Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertUser(context.Background(), &user); err != nil {
		t.Fatal(err)
	}
	route := domain.Route{Name: "Notify Route", Zone: "notify", ActivityKind: domain.ActivityRun, DistanceMeters: 5000, Active: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertRoute(context.Background(), &route, []domain.RouteSegment{
		{Sequence: 1, Name: "Start", Checkpoint: domain.GeoPoint{Latitude: 30.1, Longitude: 120.1}, Version: 1},
	}); err != nil {
		t.Fatal(err)
	}
	leader := domain.Leader{UserID: user.ID, Kinds: []domain.ActivityKind{domain.ActivityRun}, QualifiedUntil: now.AddDate(1, 0, 0), EmergencyTrained: true, Version: 1}
	if err := store.InsertLeader(context.Background(), &leader); err != nil {
		t.Fatal(err)
	}
	wave := domain.ActivityWave{RouteID: route.ID, LeaderID: leader.ID, Name: "Notify Wave", ScheduledStart: now.Add(time.Hour), ExpectedEnd: now.Add(3 * time.Hour), Capacity: 4, State: domain.WaveDraft, DepartureRisk: domain.RiskLow, Version: 1, CreatedBy: user.ID, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertWave(context.Background(), &wave); err != nil {
		t.Fatal(err)
	}
	alert := domain.Alert{WaveID: wave.ID, Kind: "heat", Severity: domain.RiskHigh, Status: domain.AlertOpen, DedupeKey: "notify-heat", OpenedAt: now, Version: 1}
	if _, err := store.InsertAlert(context.Background(), &alert); err != nil {
		t.Fatal(err)
	}
	return alert
}

type stubSender struct {
	sent  int
	msgs  []string
	errOn int
}

func (s *stubSender) Send(ctx context.Context, contact, message string) (string, error) {
	if s.errOn == s.sent+1 {
		return "", errors.New("provider unavailable")
	}
	s.sent++
	s.msgs = append(s.msgs, message)
	return "accepted", nil
}

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "notify.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func deliveryStatus(t *testing.T, store *sqlite.Store, alertID int64, contact, channel string) (string, int) {
	t.Helper()
	var status string
	var attempts int
	err := store.DB().QueryRow(`SELECT status, attempt_count FROM notification_deliveries WHERE alert_id = ? AND contact = ? AND channel = ?`, alertID, contact, channel).Scan(&status, &attempts)
	if err != nil {
		t.Fatal(err)
	}
	return status, attempts
}

// TestNotifySendsOnceOnReplay reproduces the worker restart recovery path:
// after the first delivery reaches "sent" and the worker dies before marking
// the job complete, replaying Notify must not send again and must leave the
// delivery terminal.
func TestNotifySendsOnceOnReplay(t *testing.T) {
	store := openStore(t)
	alert := alertFixture(t, store)
	now := fixedTime()
	sender := &stubSender{}
	svc := New(store, sender, func() time.Time { return now })

	contact, channel := "13800001234", "sms"
	if err := svc.Notify(context.Background(), alert.ID, contact, channel); err != nil {
		t.Fatalf("first notify: %v", err)
	}
	if sender.sent != 1 {
		t.Fatalf("after first notify, sender.sent = %d, want 1", sender.sent)
	}
	status, attempts := deliveryStatus(t, store, alert.ID, contact, channel)
	if status != "sent" || attempts != 1 {
		t.Fatalf("after first notify, status=%s attempts=%d, want sent/1", status, attempts)
	}

	// Worker restart: the same notification key is processed again.
	if err := svc.Notify(context.Background(), alert.ID, contact, channel); err != nil {
		t.Fatalf("replay notify: %v", err)
	}
	if sender.sent != 1 {
		t.Fatalf("after replay, sender.sent = %d, want 1 (no duplicate send)", sender.sent)
	}
	status, attempts = deliveryStatus(t, store, alert.ID, contact, channel)
	if status != "sent" {
		t.Fatalf("after replay, status = %q, want sent (terminal preserved)", status)
	}
	if attempts != 1 {
		t.Fatalf("after replay, attempt_count = %d, want 1 (not bumped on terminal replay)", attempts)
	}
}

// TestNotifyRetriesWhileQueued confirms that a non-terminal delivery (e.g.
// queued, or a prior send failure) is still retried and attempt_count bumps.
func TestNotifyRetriesWhileQueued(t *testing.T) {
	store := openStore(t)
	alert := alertFixture(t, store)
	now := fixedTime()
	sender := &stubSender{errOn: 1} // first send fails
	svc := New(store, sender, func() time.Time { return now })

	contact, channel := "13800005555", "sms"
	if err := svc.Notify(context.Background(), alert.ID, contact, channel); err == nil {
		t.Fatal("first notify wanted send error")
	}
	status, attempts := deliveryStatus(t, store, alert.ID, contact, channel)
	if status != "failed" {
		t.Fatalf("after failed send, status = %q, want failed", status)
	}
	if attempts != 1 {
		t.Fatalf("after failed send, attempt_count = %d, want 1", attempts)
	}

	// Retry: the receipt merge keeps a queued/failed delivery eligible, and
	// attempt_count should bump on the next attempt.
	sender.errOn = 0
	if err := svc.Notify(context.Background(), alert.ID, contact, channel); err != nil {
		t.Fatalf("retry notify: %v", err)
	}
	if sender.sent != 1 {
		t.Fatalf("after retry, sender.sent = %d, want 1", sender.sent)
	}
	status, attempts = deliveryStatus(t, store, alert.ID, contact, channel)
	if status != "sent" {
		t.Fatalf("after retry, status = %q, want sent", status)
	}
	if attempts != 2 {
		t.Fatalf("after retry, attempt_count = %d, want 2", attempts)
	}
}
