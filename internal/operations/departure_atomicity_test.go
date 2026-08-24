package operations_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/audit"
	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/operations"
	storepkg "github.com/11DingKing/heatguard-field-ops/internal/storage/sqlite"
)

func TestDepartureFailurePreservesReadyGroup(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	type scenario struct {
		store       *storepkg.Store
		service     *operations.Service
		path        string
		wave        domain.ActivityWave
		participant domain.Participant
		actor       domain.User
	}
	newScenario := func(name string) scenario {
		path := filepath.Join(t.TempDir(), name+".db")
		store, err := storepkg.Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		actor := domain.User{Email: name + "-organizer@example.test", DisplayName: "Organizer", Role: domain.RoleOrganizer, PasswordHash: []byte("hash"), Active: true, CreatedAt: now, UpdatedAt: now}
		if err := store.InsertUser(ctx, &actor); err != nil {
			t.Fatal(err)
		}
		coach := domain.User{Email: name + "-coach@example.test", DisplayName: "Coach", Role: domain.RoleCoach, PasswordHash: []byte("hash"), Active: true, CreatedAt: now, UpdatedAt: now}
		if err := store.InsertUser(ctx, &coach); err != nil {
			t.Fatal(err)
		}
		route := domain.Route{Name: name + " route", Zone: "north", ActivityKind: domain.ActivityRun, DistanceMeters: 5000, Active: true, Version: 1, CreatedAt: now, UpdatedAt: now}
		segments := []domain.RouteSegment{{Sequence: 1, Name: "assembly", Checkpoint: domain.GeoPoint{Latitude: 30, Longitude: 120}, HydrationSite: true, Version: 1}}
		if err := store.InsertRoute(ctx, &route, segments); err != nil {
			t.Fatal(err)
		}
		leader := domain.Leader{UserID: coach.ID, Kinds: []domain.ActivityKind{domain.ActivityRun}, QualifiedUntil: now.AddDate(1, 0, 0), EmergencyTrained: true, Version: 1}
		if err := store.InsertLeader(ctx, &leader); err != nil {
			t.Fatal(err)
		}
		participant := domain.Participant{Name: name + " runner", BirthDate: now.AddDate(-20, 0, 0), EmergencyName: "Contact", EmergencyPhone: "13800000000", Active: true, Version: 1, CreatedAt: now}
		if err := store.InsertParticipant(ctx, &participant); err != nil {
			t.Fatal(err)
		}
		wave := domain.ActivityWave{RouteID: route.ID, LeaderID: leader.ID, Name: name + " wave", ScheduledStart: now.Add(time.Hour), ExpectedEnd: now.Add(3 * time.Hour), Capacity: 5, State: domain.WaveReady, DepartureRisk: domain.RiskLow, Version: 1, CreatedBy: actor.ID, CreatedAt: now, UpdatedAt: now}
		if err := store.InsertWave(ctx, &wave); err != nil {
			t.Fatal(err)
		}
		member := domain.WaveParticipant{WaveID: wave.ID, ParticipantID: participant.ID, State: domain.ParticipantEnrolled, Version: 1, EnrolledAt: now}
		if err := store.InsertWaveParticipant(ctx, &member); err != nil {
			t.Fatal(err)
		}
		clock := func() time.Time { return now }
		return scenario{store: store, service: operations.New(store, audit.New(clock), clock), path: path, wave: wave, participant: participant, actor: actor}
	}

	failing := newScenario("failing")
	raw, err := sql.Open("sqlite", failing.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.ExecContext(ctx, `CREATE TRIGGER reject_depart_audit BEFORE INSERT ON audit_events WHEN NEW.action = 'wave.depart' BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	_, err = failing.service.Depart(ctx, operations.DepartInput{WaveID: failing.wave.ID, Version: 1, ActorID: failing.actor.ID, Temperature: 29, HeatIndex: 31, IdempotencyKey: "depart-failing", RequestID: "request-failing"})
	if err == nil {
		t.Fatal("departure unexpectedly succeeded while audit storage rejected the record")
	}
	storedWave, err := failing.store.GetWave(ctx, failing.wave.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedMember, err := failing.store.GetWaveParticipant(ctx, failing.wave.ID, failing.participant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedWave.State != domain.WaveReady || storedWave.Version != 1 {
		t.Fatalf("failed departure changed wave: state=%s version=%d", storedWave.State, storedWave.Version)
	}
	if storedMember.State != domain.ParticipantEnrolled || storedMember.Version != 1 {
		t.Fatalf("failed departure changed participant: state=%s version=%d", storedMember.State, storedMember.Version)
	}

	healthy := newScenario("healthy")
	departed, err := healthy.service.Depart(ctx, operations.DepartInput{WaveID: healthy.wave.ID, Version: 1, ActorID: healthy.actor.ID, Temperature: 29, HeatIndex: 31, IdempotencyKey: "depart-healthy", RequestID: "request-healthy"})
	if err != nil {
		t.Fatal(err)
	}
	if departed.State != domain.WaveActive || departed.Version != 2 {
		t.Fatalf("healthy departure = state %s version %d", departed.State, departed.Version)
	}
}
