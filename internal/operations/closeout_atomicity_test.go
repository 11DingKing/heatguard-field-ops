package operations

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/audit"
	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
	"github.com/11DingKing/heatguard-field-ops/internal/storage/sqlite"
)

func TestCloseoutAuditFailureKeepsWaveActive(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "closeout.db")
	store, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 8, 24, 13, 30, 0, 0, time.UTC)
	actor := domain.User{Email: "organizer-closeout@example.test", DisplayName: "Closeout Organizer", Role: domain.RoleOrganizer, PasswordHash: []byte("hash"), Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertUser(ctx, &actor); err != nil {
		t.Fatalf("insert actor: %v", err)
	}
	coach := domain.User{Email: "coach-closeout@example.test", DisplayName: "Closeout Coach", Role: domain.RoleCoach, PasswordHash: []byte("hash"), Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertUser(ctx, &coach); err != nil {
		t.Fatalf("insert coach: %v", err)
	}
	route := domain.Route{Name: "Closeout Route", Zone: "south-park", ActivityKind: domain.ActivityRun, DistanceMeters: 4800, Active: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	segments := []domain.RouteSegment{
		{Sequence: 1, Name: "Assembly", Checkpoint: domain.GeoPoint{Latitude: 30.1, Longitude: 120.1}, HydrationSite: true, Version: 1},
		{Sequence: 2, Name: "Finish", Checkpoint: domain.GeoPoint{Latitude: 30.2, Longitude: 120.2}, HydrationSite: true, Version: 1},
	}
	if err := store.InsertRoute(ctx, &route, segments); err != nil {
		t.Fatalf("insert route: %v", err)
	}
	leader := domain.Leader{UserID: coach.ID, Kinds: []domain.ActivityKind{domain.ActivityRun}, QualifiedUntil: now.AddDate(1, 0, 0), EmergencyTrained: true, Version: 1}
	if err := store.InsertLeader(ctx, &leader); err != nil {
		t.Fatalf("insert leader: %v", err)
	}

	failedParticipant := domain.Participant{Name: "Failed Closeout Participant", BirthDate: now.AddDate(-22, 0, 0), EmergencyName: "Emergency Contact", EmergencyPhone: "13800000001", Active: true, Version: 1, CreatedAt: now}
	if err := store.InsertParticipant(ctx, &failedParticipant); err != nil {
		t.Fatalf("insert failed participant: %v", err)
	}
	failedWave := domain.ActivityWave{RouteID: route.ID, LeaderID: leader.ID, Name: "Failed Closeout Wave", ScheduledStart: now.Add(-2 * time.Hour), ExpectedEnd: now.Add(-time.Hour), Capacity: 4, State: domain.WaveActive, DepartureRisk: domain.RiskGuarded, Version: 1, CreatedBy: actor.ID, CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)}
	if err := store.InsertWave(ctx, &failedWave); err != nil {
		t.Fatalf("insert failed wave: %v", err)
	}
	failedMembership := domain.WaveParticipant{WaveID: failedWave.ID, ParticipantID: failedParticipant.ID, State: domain.ParticipantCompleted, Disposition: string(domain.ParticipantCompleted), Version: 1, EnrolledAt: now.Add(-3 * time.Hour)}
	if err := store.InsertWaveParticipant(ctx, &failedMembership); err != nil {
		t.Fatalf("insert failed membership: %v", err)
	}

	adminDB, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open trigger connection: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	_, err = adminDB.ExecContext(ctx, `CREATE TRIGGER reject_wave_close_audit
		BEFORE INSERT ON audit_events
		WHEN NEW.action = 'wave.close'
		BEGIN
			SELECT RAISE(ABORT, 'audit storage unavailable');
		END`)
	if err != nil {
		t.Fatalf("create audit failure trigger: %v", err)
	}

	service := New(store, audit.New(func() time.Time { return now }), func() time.Time { return now })
	if err := service.CloseWave(ctx, failedWave.ID, failedWave.Version, actor.ID, "request-close-failed"); err == nil {
		t.Error("close wave succeeded while audit storage rejected the record")
	}
	reloadedFailedWave, err := store.GetWave(ctx, failedWave.ID)
	if err != nil {
		t.Fatalf("reload failed wave: %v", err)
	}
	if reloadedFailedWave.State != domain.WaveActive || reloadedFailedWave.Version != 1 {
		t.Errorf("failed closeout persisted state %s at version %d; want active at version 1", reloadedFailedWave.State, reloadedFailedWave.Version)
	}

	if _, err := adminDB.ExecContext(ctx, `DROP TRIGGER reject_wave_close_audit`); err != nil {
		t.Fatalf("drop audit failure trigger: %v", err)
	}
	healthyParticipant := domain.Participant{Name: "Healthy Closeout Participant", BirthDate: now.AddDate(-24, 0, 0), EmergencyName: "Healthy Contact", EmergencyPhone: "13800000002", Active: true, Version: 1, CreatedAt: now}
	if err := store.InsertParticipant(ctx, &healthyParticipant); err != nil {
		t.Fatalf("insert healthy participant: %v", err)
	}
	healthyWave := domain.ActivityWave{RouteID: route.ID, LeaderID: leader.ID, Name: "Healthy Closeout Wave", ScheduledStart: now.Add(-2 * time.Hour), ExpectedEnd: now.Add(-time.Hour), Capacity: 4, State: domain.WaveActive, DepartureRisk: domain.RiskLow, Version: 1, CreatedBy: actor.ID, CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)}
	if err := store.InsertWave(ctx, &healthyWave); err != nil {
		t.Fatalf("insert healthy wave: %v", err)
	}
	healthyMembership := domain.WaveParticipant{WaveID: healthyWave.ID, ParticipantID: healthyParticipant.ID, State: domain.ParticipantWithdrawn, Disposition: string(domain.ParticipantWithdrawn), Version: 1, EnrolledAt: now.Add(-3 * time.Hour)}
	if err := store.InsertWaveParticipant(ctx, &healthyMembership); err != nil {
		t.Fatalf("insert healthy membership: %v", err)
	}
	if err := service.CloseWave(ctx, healthyWave.ID, healthyWave.Version, actor.ID, "request-close-healthy"); err != nil {
		t.Fatalf("healthy closeout: %v", err)
	}
	reloadedHealthyWave, err := store.GetWave(ctx, healthyWave.ID)
	if err != nil {
		t.Fatalf("reload healthy wave: %v", err)
	}
	if reloadedHealthyWave.State != domain.WaveClosed || reloadedHealthyWave.Version != 2 {
		t.Fatalf("healthy closeout state = %s version %d; want closed version 2", reloadedHealthyWave.State, reloadedHealthyWave.Version)
	}
	auditEvents, err := store.ListAuditEvents(ctx, "wave", strconv.FormatInt(healthyWave.ID, 10), repository.Page{Limit: 10})
	if err != nil {
		t.Fatalf("list healthy audit events: %v", err)
	}
	if len(auditEvents) != 1 || auditEvents[0].Action != "wave.close" {
		t.Fatalf("healthy closeout audit events = %+v", auditEvents)
	}
}
