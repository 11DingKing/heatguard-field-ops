package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "heatguard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

func fixedTime() time.Time {
	return time.Date(2026, 8, 24, 8, 30, 0, 0, time.UTC)
}

func insertTestUser(t *testing.T, store repository.Writer, role domain.Role, suffix string) domain.User {
	t.Helper()
	now := fixedTime()
	user := domain.User{
		Email:        fmt.Sprintf("%s@example.test", suffix),
		DisplayName:  suffix,
		Role:         role,
		PasswordHash: []byte("hash"),
		Active:       true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := store.InsertUser(context.Background(), &user); err != nil {
		t.Fatalf("InsertUser: %v", err)
	}
	return user
}

func insertTestRoute(t *testing.T, store repository.Writer, suffix string) (domain.Route, []domain.RouteSegment) {
	t.Helper()
	now := fixedTime()
	route := domain.Route{
		Name:           "River Route " + suffix,
		Zone:           "river-" + suffix,
		ActivityKind:   domain.ActivityRun,
		DistanceMeters: 5000,
		Active:         true,
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	segments := []domain.RouteSegment{
		{Sequence: 1, Name: "Assembly", Checkpoint: domain.GeoPoint{Latitude: 30.1, Longitude: 120.1}, HydrationSite: true, Version: 1},
		{Sequence: 2, Name: "Bridge", Checkpoint: domain.GeoPoint{Latitude: 30.2, Longitude: 120.2}, Version: 1},
		{Sequence: 3, Name: "Finish", Checkpoint: domain.GeoPoint{Latitude: 30.3, Longitude: 120.3}, HydrationSite: true, Version: 1},
	}
	if err := store.InsertRoute(context.Background(), &route, segments); err != nil {
		t.Fatalf("InsertRoute: %v", err)
	}
	return route, segments
}

func insertTestLeader(t *testing.T, store repository.Writer, user domain.User) domain.Leader {
	t.Helper()
	leader := domain.Leader{
		UserID:           user.ID,
		Kinds:            []domain.ActivityKind{domain.ActivityRun, domain.ActivityParkFit},
		QualifiedUntil:   fixedTime().AddDate(1, 0, 0),
		EmergencyTrained: true,
		Version:          1,
	}
	if err := store.InsertLeader(context.Background(), &leader); err != nil {
		t.Fatalf("InsertLeader: %v", err)
	}
	return leader
}

func insertTestParticipant(t *testing.T, store repository.Writer, suffix string) domain.Participant {
	t.Helper()
	participant := domain.Participant{
		Name:           "Participant " + suffix,
		BirthDate:      time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		EmergencyName:  "Contact " + suffix,
		EmergencyPhone: "1380000" + suffix,
		Active:         true,
		Version:        1,
		CreatedAt:      fixedTime(),
	}
	if err := store.InsertParticipant(context.Background(), &participant); err != nil {
		t.Fatalf("InsertParticipant: %v", err)
	}
	return participant
}

func insertTestWave(t *testing.T, store repository.Writer, route domain.Route, leader domain.Leader, actor domain.User, suffix string) domain.ActivityWave {
	t.Helper()
	now := fixedTime()
	wave := domain.ActivityWave{
		RouteID:        route.ID,
		LeaderID:       leader.ID,
		Name:           "Wave " + suffix,
		ScheduledStart: now.Add(time.Hour),
		ExpectedEnd:    now.Add(3 * time.Hour),
		Capacity:       4,
		State:          domain.WaveDraft,
		DepartureRisk:  domain.RiskLow,
		Version:        1,
		CreatedBy:      actor.ID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := store.InsertWave(context.Background(), &wave); err != nil {
		t.Fatalf("InsertWave: %v", err)
	}
	return wave
}

func TestMigrationsCreateExpectedSchema(t *testing.T) {
	store := openTestStore(t)
	expected := []string{
		"users", "sessions", "routes", "route_segments", "leaders",
		"participants", "health_restrictions", "activity_waves",
		"wave_participants", "risk_rules", "field_events", "alerts",
		"notification_deliveries", "worker_jobs", "idempotency_records",
		"audit_events", "schema_migrations",
	}
	for _, table := range expected {
		var count int
		err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
		if err != nil {
			t.Fatalf("query table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s count = %d", table, count)
		}
	}
	var migrationCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != len(migrations) {
		t.Fatalf("migration count = %d, want %d", migrationCount, len(migrations))
	}
}

func TestMigrationRestartPreservesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	user := insertTestUser(t, store, domain.RoleOrganizer, "restart")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetUser(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != user.Email || got.Role != user.Role {
		t.Fatalf("reopened user = %+v", got)
	}
	var migrationCount int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != len(migrations) {
		t.Fatalf("migration count changed to %d", migrationCount)
	}
}

func TestMigrationChecksumConflictStopsStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conflict.db")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE schema_migrations SET checksum='corrupt' WHERE version=1`); err != nil {
		t.Fatal(err)
	}
	store.Close()
	_, err = Open(context.Background(), path)
	if err == nil {
		t.Fatal("expected migration conflict")
	}
}

func TestWithinTxCommitsCrossEntityWrites(t *testing.T) {
	store := openTestStore(t)
	err := store.WithinTx(context.Background(), func(tx repository.Tx) error {
		user := insertTestUser(t, tx, domain.RoleCoach, "tx-commit")
		route, segments := insertTestRoute(t, tx, "tx-commit")
		leader := insertTestLeader(t, tx, user)
		if user.ID == 0 || route.ID == 0 || leader.ID == 0 || segments[0].ID == 0 {
			t.Fatal("transaction did not assign identifiers")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"users", "routes", "route_segments", "leaders"} {
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Errorf("table %s did not commit", table)
		}
	}
}

func TestWithinTxRollsBackAllEntities(t *testing.T) {
	store := openTestStore(t)
	sentinel := errors.New("audit storage unavailable")
	err := store.WithinTx(context.Background(), func(tx repository.Tx) error {
		user := insertTestUser(t, tx, domain.RoleCoach, "tx-rollback")
		insertTestRoute(t, tx, "tx-rollback")
		if user.ID == 0 {
			t.Fatal("missing user id")
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("got %v, want sentinel", err)
	}
	for _, table := range []string{"users", "routes", "route_segments"} {
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("table %s leaked %d rows", table, count)
		}
	}
}

func TestUserAndSessionLifecycle(t *testing.T) {
	store := openTestStore(t)
	user := insertTestUser(t, store, domain.RoleDuty, "session")
	now := fixedTime()
	session := domain.Session{
		UserID:    user.ID,
		TokenHash: []byte("unique-token-hash"),
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
		LastSeen:  now,
	}
	if err := store.InsertSession(context.Background(), &session); err != nil {
		t.Fatal(err)
	}
	gotSession, gotUser, err := store.GetSessionByHash(context.Background(), session.TokenHash, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if gotSession.ID != session.ID || gotUser.ID != user.ID {
		t.Fatalf("session/user mismatch: %+v %+v", gotSession, gotUser)
	}
	touched := now.Add(2 * time.Minute)
	if err := store.TouchSession(context.Background(), session.ID, touched); err != nil {
		t.Fatal(err)
	}
	revoked, err := store.RevokeSession(context.Background(), session.TokenHash, touched)
	if err != nil || !revoked {
		t.Fatalf("revoke = %v, %v", revoked, err)
	}
	_, _, err = store.GetSessionByHash(context.Background(), session.TokenHash, touched)
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revoked session got %v", err)
	}
}

func TestExpiredSessionsAreRevokedInBulk(t *testing.T) {
	store := openTestStore(t)
	user := insertTestUser(t, store, domain.RoleDuty, "expiry")
	now := fixedTime()
	for index, expiry := range []time.Time{now.Add(-time.Hour), now, now.Add(time.Hour)} {
		session := domain.Session{UserID: user.ID, TokenHash: []byte(fmt.Sprintf("token-%d", index)), ExpiresAt: expiry, CreatedAt: now.Add(-2 * time.Hour), LastSeen: now.Add(-2 * time.Hour)}
		if err := store.InsertSession(context.Background(), &session); err != nil {
			t.Fatal(err)
		}
	}
	count, err := store.RevokeExpiredSessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("revoked %d, want 2", count)
	}
}

func TestRouteSegmentsRoundTripAndClosureVersion(t *testing.T) {
	store := openTestStore(t)
	route, segments := insertTestRoute(t, store, "segments")
	got, err := store.ListRouteSegments(context.Background(), route.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[1].Name != "Bridge" || !got[0].HydrationSite {
		t.Fatalf("segments = %+v", got)
	}
	from := fixedTime().Add(time.Hour)
	until := from.Add(30 * time.Minute)
	if err := store.UpdateSegmentClosure(context.Background(), segments[1].ID, 1, &from, &until); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSegmentClosure(context.Background(), segments[1].ID, 1, &from, &until); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale closure got %v", err)
	}
}

func TestWaveParticipantAndOptimisticUpdates(t *testing.T) {
	store := openTestStore(t)
	actor := insertTestUser(t, store, domain.RoleOrganizer, "wave-actor")
	coach := insertTestUser(t, store, domain.RoleCoach, "wave-coach")
	route, _ := insertTestRoute(t, store, "wave")
	leader := insertTestLeader(t, store, coach)
	participant := insertTestParticipant(t, store, "001")
	wave := insertTestWave(t, store, route, leader, actor, "one")
	member := domain.WaveParticipant{WaveID: wave.ID, ParticipantID: participant.ID, State: domain.ParticipantEnrolled, Version: 1, EnrolledAt: fixedTime()}
	if err := store.InsertWaveParticipant(context.Background(), &member); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateWaveState(context.Background(), wave.ID, 1, domain.WaveReady, domain.RiskGuarded, fixedTime()); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateWaveState(context.Background(), wave.ID, 1, domain.WaveActive, domain.RiskLow, fixedTime()); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale wave update got %v", err)
	}
	if err := store.UpdateParticipantState(context.Background(), wave.ID, participant.ID, 1, domain.ParticipantDeparted, nil, fixedTime(), ""); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateParticipantState(context.Background(), wave.ID, participant.ID, 1, domain.ParticipantMissing, nil, fixedTime(), ""); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale participant update got %v", err)
	}
}

func TestConcurrentVersionUpdatesHaveSingleWinner(t *testing.T) {
	store := openTestStore(t)
	actor := insertTestUser(t, store, domain.RoleOrganizer, "race-actor")
	coach := insertTestUser(t, store, domain.RoleCoach, "race-coach")
	route, _ := insertTestRoute(t, store, "race")
	leader := insertTestLeader(t, store, coach)
	wave := insertTestWave(t, store, route, leader, actor, "race")
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, state := range []domain.WaveState{domain.WaveReady, domain.WaveCancelled} {
		state := state
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			results <- store.UpdateWaveState(context.Background(), wave.ID, 1, state, domain.RiskLow, fixedTime())
		}()
	}
	close(start)
	group.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected update error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestFieldEventIdempotency(t *testing.T) {
	store := openTestStore(t)
	actor := insertTestUser(t, store, domain.RoleOrganizer, "event-actor")
	coach := insertTestUser(t, store, domain.RoleCoach, "event-coach")
	route, _ := insertTestRoute(t, store, "event")
	leader := insertTestLeader(t, store, coach)
	wave := insertTestWave(t, store, route, leader, actor, "event")
	participant := insertTestParticipant(t, store, "002")
	event := domain.FieldEvent{WaveID: wave.ID, ParticipantID: &participant.ID, Type: domain.EventMissing, OccurredAt: fixedTime(), RecordedAt: fixedTime(), RecordedBy: actor.ID, IdempotencyKey: "mobile-001", Details: "checkpoint overdue"}
	if err := store.InsertFieldEvent(context.Background(), &event); err != nil {
		t.Fatal(err)
	}
	duplicate := event
	duplicate.ID = 0
	if err := store.InsertFieldEvent(context.Background(), &duplicate); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate event got %v", err)
	}
	got, err := store.GetFieldEventByKey(context.Background(), wave.ID, event.IdempotencyKey)
	if err != nil || got.ID != event.ID || got.Details != event.Details {
		t.Fatalf("event round trip = %+v, %v", got, err)
	}
}

func TestAlertDeduplicationAndStatusVersion(t *testing.T) {
	store := openTestStore(t)
	actor := insertTestUser(t, store, domain.RoleOrganizer, "alert-actor")
	coach := insertTestUser(t, store, domain.RoleCoach, "alert-coach")
	route, _ := insertTestRoute(t, store, "alert")
	leader := insertTestLeader(t, store, coach)
	wave := insertTestWave(t, store, route, leader, actor, "alert")
	alert := domain.Alert{WaveID: wave.ID, Kind: "heat", Severity: domain.RiskHigh, Status: domain.AlertOpen, DedupeKey: "wave-heat", OpenedAt: fixedTime(), Version: 1}
	created, err := store.InsertAlert(context.Background(), &alert)
	if err != nil || !created || alert.ID == 0 {
		t.Fatalf("insert alert = %v, %v, %+v", created, err, alert)
	}
	duplicate := domain.Alert{WaveID: wave.ID, Kind: "heat", Severity: domain.RiskHigh, Status: domain.AlertOpen, DedupeKey: "wave-heat", OpenedAt: fixedTime(), Version: 1}
	created, err = store.InsertAlert(context.Background(), &duplicate)
	if err != nil || created || duplicate.ID != alert.ID {
		t.Fatalf("duplicate alert = %v, %v, %+v", created, err, duplicate)
	}
	if err := store.UpdateAlertStatus(context.Background(), alert.ID, 1, domain.AlertAcknowledged, fixedTime()); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAlertStatus(context.Background(), alert.ID, 1, domain.AlertResolved, fixedTime()); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale alert got %v", err)
	}
}

func TestJobsLeaseRetryAndRecovery(t *testing.T) {
	store := openTestStore(t)
	now := fixedTime()
	job := domain.WorkerJob{Kind: "escalate", DedupeKey: "alert-1", Payload: `{}`, Status: domain.JobPending, MaxAttempts: 3, AvailableAt: now, CreatedAt: now, UpdatedAt: now}
	created, err := store.InsertJob(context.Background(), &job)
	if err != nil || !created {
		t.Fatalf("insert job = %v, %v", created, err)
	}
	created, err = store.InsertJob(context.Background(), &job)
	if err != nil || created {
		t.Fatalf("duplicate job = %v, %v", created, err)
	}
	leased, err := store.LeaseJobs(context.Background(), repository.JobLease{Owner: "worker-a", Now: now, Duration: time.Minute, Limit: 10})
	if err != nil || len(leased) != 1 || leased[0].Attempts != 1 {
		t.Fatalf("lease = %+v, %v", leased, err)
	}
	other, err := store.LeaseJobs(context.Background(), repository.JobLease{Owner: "worker-b", Now: now.Add(30 * time.Second), Duration: time.Minute, Limit: 10})
	if err != nil || len(other) != 0 {
		t.Fatalf("active lease was stolen: %+v, %v", other, err)
	}
	recovered, err := store.LeaseJobs(context.Background(), repository.JobLease{Owner: "worker-b", Now: now.Add(2 * time.Minute), Duration: time.Minute, Limit: 10})
	if err != nil || len(recovered) != 1 || recovered[0].Attempts != 2 {
		t.Fatalf("expired lease not recovered: %+v, %v", recovered, err)
	}
	if err := store.FailJob(context.Background(), recovered[0].ID, "worker-b", now.Add(2*time.Minute), now.Add(3*time.Minute), errors.New("temporary")); err != nil {
		t.Fatal(err)
	}
	leased, err = store.LeaseJobs(context.Background(), repository.JobLease{Owner: "worker-c", Now: now.Add(3 * time.Minute), Duration: time.Minute, Limit: 10})
	if err != nil || len(leased) != 1 || leased[0].Attempts != 3 {
		t.Fatalf("retry lease = %+v, %v", leased, err)
	}
	if err := store.CompleteJob(context.Background(), leased[0].ID, "worker-c", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestIdempotencyScopeIncludesOperation(t *testing.T) {
	store := openTestStore(t)
	now := fixedTime()
	created, err := store.InsertIdempotencyResult(context.Background(), "wave:1", "depart", "key", "1", now)
	if err != nil || !created {
		t.Fatalf("first result = %v, %v", created, err)
	}
	created, err = store.InsertIdempotencyResult(context.Background(), "wave:1", "depart", "key", "other", now)
	if err != nil || created {
		t.Fatalf("duplicate result = %v, %v", created, err)
	}
	created, err = store.InsertIdempotencyResult(context.Background(), "wave:1", "close", "key", "1", now)
	if err != nil || !created {
		t.Fatalf("different operation collided = %v, %v", created, err)
	}
	value, found, err := store.GetIdempotencyResult(context.Background(), "wave:1", "depart", "key")
	if err != nil || !found || value != "1" {
		t.Fatalf("lookup = %q, %v, %v", value, found, err)
	}
}

func TestAuditPaginationAndObjectIsolation(t *testing.T) {
	store := openTestStore(t)
	actor := insertTestUser(t, store, domain.RoleDuty, "auditor")
	for index := 0; index < 5; index++ {
		event := domain.AuditEvent{ActorID: &actor.ID, Action: "wave.action", ObjectType: "wave", ObjectID: "42", Result: "success", RequestID: fmt.Sprintf("req-%d", index), Metadata: `{}`, CreatedAt: fixedTime().Add(time.Duration(index) * time.Second)}
		if err := store.InsertAudit(context.Background(), &event); err != nil {
			t.Fatal(err)
		}
	}
	other := domain.AuditEvent{ActorID: &actor.ID, Action: "route.action", ObjectType: "route", ObjectID: "42", Result: "success", RequestID: "other", Metadata: `{}`, CreatedAt: fixedTime()}
	if err := store.InsertAudit(context.Background(), &other); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListAuditEvents(context.Background(), "wave", "42", repository.Page{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].RequestID != "req-3" || page[1].RequestID != "req-2" {
		t.Fatalf("page = %+v", page)
	}
}

func TestWaveFilteringUsesSamePredicateForCountAndRows(t *testing.T) {
	store := openTestStore(t)
	actor := insertTestUser(t, store, domain.RoleOrganizer, "filter-actor")
	coach := insertTestUser(t, store, domain.RoleCoach, "filter-coach")
	routeA, _ := insertTestRoute(t, store, "filter-a")
	routeB, _ := insertTestRoute(t, store, "filter-b")
	leader := insertTestLeader(t, store, coach)
	for index := 0; index < 4; index++ {
		route := routeA
		if index == 3 {
			route = routeB
		}
		insertTestWave(t, store, route, leader, actor, fmt.Sprintf("filter-%d", index))
	}
	waves, total, err := store.ListWaves(context.Background(), repository.WaveFilter{RouteID: routeA.ID, States: []domain.WaveState{domain.WaveDraft}, Page: repository.Page{Limit: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if len(waves) != 2 || total != 3 {
		t.Fatalf("rows=%d total=%d", len(waves), total)
	}
}

func TestNotificationReceiptDoesNotRegressConfirmedDelivery(t *testing.T) {
	store := openTestStore(t)
	actor := insertTestUser(t, store, domain.RoleOrganizer, "receipt-actor")
	coach := insertTestUser(t, store, domain.RoleCoach, "receipt-coach")
	route, _ := insertTestRoute(t, store, "receipt")
	leader := insertTestLeader(t, store, coach)
	wave := insertTestWave(t, store, route, leader, actor, "receipt")
	alert := domain.Alert{WaveID: wave.ID, Kind: "heat", Severity: domain.RiskHigh, Status: domain.AlertOpen, DedupeKey: "receipt-heat", OpenedAt: fixedTime(), Version: 1}
	if _, err := store.InsertAlert(context.Background(), &alert); err != nil {
		t.Fatal(err)
	}

	base := fixedTime()
	deliveredAt := base.Add(2 * time.Minute)
	failedAt := base.Add(1 * time.Minute) // earlier event time, arrives late after partition
	providerKey := "alert-receipt-sms"
	if _, _, err := store.InsertNotificationAttempt(context.Background(), alert.ID, "138", "sms", providerKey, base); err != nil {
		t.Fatal(err)
	}

	// The supplier first confirms delivery; this is the confirmed terminal state.
	if err := store.MergeNotificationReceipt(context.Background(), providerKey, "delivered", deliveredAt); err != nil {
		t.Fatal(err)
	}

	// After the network recovers, the supplier replays a failed receipt whose
	// event time predates the confirmed delivery. It must not regress state or
	// backdate the recorded event time.
	if err := store.MergeNotificationReceipt(context.Background(), providerKey, "failed", failedAt); err != nil {
		t.Fatalf("late failed receipt: %v", err)
	}

	var status, lastReceipt, updatedAt string
	if err := store.db.QueryRow(`SELECT status, last_receipt_at, updated_at FROM notification_deliveries WHERE provider_key = ?`, providerKey).Scan(&status, &lastReceipt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if status != "delivered" {
		t.Fatalf("status regressed to %q, want delivered", status)
	}
	gotReceipt, err := parseTime(lastReceipt)
	if err != nil {
		t.Fatal(err)
	}
	if !gotReceipt.Equal(deliveredAt) {
		t.Fatalf("last_receipt_at = %v, want %v", gotReceipt, deliveredAt)
	}
	gotUpdated, err := parseTime(updatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !gotUpdated.Equal(deliveredAt) {
		t.Fatalf("updated_at = %v, want %v", gotUpdated, deliveredAt)
	}
}

func TestNotificationReceiptAdvancesForwardAcrossEvents(t *testing.T) {
	store := openTestStore(t)
	actor := insertTestUser(t, store, domain.RoleOrganizer, "advance-actor")
	coach := insertTestUser(t, store, domain.RoleCoach, "advance-coach")
	route, _ := insertTestRoute(t, store, "advance")
	leader := insertTestLeader(t, store, coach)
	wave := insertTestWave(t, store, route, leader, actor, "advance")
	alert := domain.Alert{WaveID: wave.ID, Kind: "heat", Severity: domain.RiskHigh, Status: domain.AlertOpen, DedupeKey: "advance-heat", OpenedAt: fixedTime(), Version: 1}
	if _, err := store.InsertAlert(context.Background(), &alert); err != nil {
		t.Fatal(err)
	}

	base := fixedTime()
	providerKey := "alert-advance-sms"
	if _, _, err := store.InsertNotificationAttempt(context.Background(), alert.ID, "138", "sms", providerKey, base); err != nil {
		t.Fatal(err)
	}
	if err := store.MergeNotificationReceipt(context.Background(), providerKey, "failed", base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// A later delivery supersedes the earlier failure and records the newest event.
	if err := store.MergeNotificationReceipt(context.Background(), providerKey, "delivered", base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := store.db.QueryRow(`SELECT status FROM notification_deliveries WHERE provider_key = ?`, providerKey).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "delivered" {
		t.Fatalf("status = %q, want delivered", status)
	}
}

func TestNotificationReceiptUnknownProviderIsNotFound(t *testing.T) {
	store := openTestStore(t)
	err := store.MergeNotificationReceipt(context.Background(), "unknown-provider", "failed", fixedTime())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
