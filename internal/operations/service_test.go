package operations

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/audit"
	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
	"github.com/11DingKing/heatguard-field-ops/internal/storage/sqlite"
)

var auditFailure = errors.New("audit storage unavailable")

// auditRejectingStore wraps the real store so that audit writes inside a
// transaction fail, simulating the storage layer rejecting the final write.
type auditRejectingStore struct {
	repository.Store
}

type auditRejectingTx struct {
	repository.Tx
}

func (s *auditRejectingStore) WithinTx(ctx context.Context, fn func(repository.Tx) error) error {
	return s.Store.WithinTx(ctx, func(tx repository.Tx) error {
		return fn(&auditRejectingTx{Tx: tx})
	})
}

func (t *auditRejectingTx) InsertAudit(ctx context.Context, event *domain.AuditEvent) error {
	return auditFailure
}

func newClock() (time.Time, func() time.Time) {
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	return now, func() time.Time { return now }
}

func seedReadyWave(t *testing.T, store repository.Writer) (domain.ActivityWave, int64, []domain.WaveParticipant) {
	t.Helper()
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	actor := domain.User{Email: "organizer@example.test", DisplayName: "Organizer", Role: domain.RoleOrganizer, PasswordHash: []byte("hash"), Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertUser(context.Background(), &actor); err != nil {
		t.Fatal(err)
	}
	coach := domain.User{Email: "coach@example.test", DisplayName: "Coach", Role: domain.RoleCoach, PasswordHash: []byte("hash"), Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertUser(context.Background(), &coach); err != nil {
		t.Fatal(err)
	}
	route := domain.Route{Name: "River Loop", Zone: "river", ActivityKind: domain.ActivityRun, DistanceMeters: 5000, Active: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertRoute(context.Background(), &route, []domain.RouteSegment{
		{Sequence: 1, Name: "Start", Checkpoint: domain.GeoPoint{Latitude: 30.1, Longitude: 120.1}, HydrationSite: true, Version: 1},
		{Sequence: 2, Name: "Finish", Checkpoint: domain.GeoPoint{Latitude: 30.2, Longitude: 120.2}, Version: 1},
	}); err != nil {
		t.Fatal(err)
	}
	leader := domain.Leader{UserID: coach.ID, Kinds: []domain.ActivityKind{domain.ActivityRun}, QualifiedUntil: now.AddDate(1, 0, 0), EmergencyTrained: true, Version: 1}
	if err := store.InsertLeader(context.Background(), &leader); err != nil {
		t.Fatal(err)
	}
	wave := domain.ActivityWave{RouteID: route.ID, LeaderID: leader.ID, Name: "Wave One", ScheduledStart: now.Add(time.Hour), ExpectedEnd: now.Add(3 * time.Hour), Capacity: 4, State: domain.WaveDraft, DepartureRisk: domain.RiskLow, Version: 1, CreatedBy: actor.ID, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertWave(context.Background(), &wave); err != nil {
		t.Fatal(err)
	}
	var members []domain.WaveParticipant
	for index := 1; index <= 2; index++ {
		participant := domain.Participant{Name: fmt.Sprintf("Participant %d", index), BirthDate: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), EmergencyName: "Contact", EmergencyPhone: "13800000001", Active: true, Version: 1, CreatedAt: now}
		if err := store.InsertParticipant(context.Background(), &participant); err != nil {
			t.Fatal(err)
		}
		member := domain.WaveParticipant{WaveID: wave.ID, ParticipantID: participant.ID, State: domain.ParticipantEnrolled, Version: 1, EnrolledAt: now}
		if err := store.InsertWaveParticipant(context.Background(), &member); err != nil {
			t.Fatal(err)
		}
		members = append(members, member)
	}
	if err := store.UpdateWaveState(context.Background(), wave.ID, wave.Version, domain.WaveReady, domain.RiskLow, now); err != nil {
		t.Fatal(err)
	}
	wave.State = domain.WaveReady
	wave.Version++
	return wave, actor.ID, members
}

func departInput(wave domain.ActivityWave, actorID int64, key string) DepartInput {
	return DepartInput{WaveID: wave.ID, Version: wave.Version, ActorID: actorID, Temperature: 28, HeatIndex: 30, IdempotencyKey: key, RequestID: "req-1"}
}

func TestDepartRollsBackWhenAuditStorageRejects(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "ops.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	_, clock := newClock()
	rejecting := &auditRejectingStore{Store: store}
	svc := New(rejecting, audit.New(clock), clock)

	wave, actorID, members := seedReadyWave(t, store)

	_, err = svc.Depart(context.Background(), departInput(wave, actorID, "depart-failed"))
	if !errors.Is(err, auditFailure) {
		t.Fatalf("depart err = %v, want audit failure", err)
	}

	// Re-query: the batch must still be ready, not advanced to active.
	after, err := store.GetWave(context.Background(), wave.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != domain.WaveReady || after.Version != wave.Version {
		t.Fatalf("wave state = %s version %d, want ready/%d", after.State, after.Version, wave.Version)
	}
	for _, member := range members {
		got, err := store.GetWaveParticipant(context.Background(), wave.ID, member.ParticipantID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != domain.ParticipantEnrolled || got.Version != member.Version {
			t.Fatalf("participant %d state = %s version %d, want enrolled/%d", member.ParticipantID, got.State, got.Version, member.Version)
		}
	}
	// The idempotency record must not exist, so a retry is allowed.
	if _, ok, err := store.GetIdempotencyResult(context.Background(), "wave:"+fmt.Sprint(wave.ID), "depart", "depart-failed"); err != nil || ok {
		t.Fatalf("idempotency record leaked: ok=%v err=%v", ok, err)
	}
}

func TestDepartSucceedsAndRecordsIdempotency(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "ops.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	_, clock := newClock()
	svc := New(store, audit.New(clock), clock)

	wave, actorID, members := seedReadyWave(t, store)

	departed, err := svc.Depart(context.Background(), departInput(wave, actorID, "depart-ok"))
	if err != nil {
		t.Fatalf("depart err = %v", err)
	}
	if departed.State != domain.WaveActive || departed.Version != wave.Version+1 {
		t.Fatalf("departed = %+v", departed)
	}
	for _, member := range members {
		got, err := store.GetWaveParticipant(context.Background(), wave.ID, member.ParticipantID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != domain.ParticipantDeparted {
			t.Fatalf("participant %d state = %s, want departed", member.ParticipantID, got.State)
		}
	}
	// Idempotent retry returns the same wave without advancing further.
	retry, err := svc.Depart(context.Background(), departInput(wave, actorID, "depart-ok"))
	if err != nil {
		t.Fatalf("retry err = %v", err)
	}
	if retry.ID != departed.ID || retry.Version != departed.Version {
		t.Fatalf("retry departed version = %d, want %d", retry.Version, departed.Version)
	}
}
