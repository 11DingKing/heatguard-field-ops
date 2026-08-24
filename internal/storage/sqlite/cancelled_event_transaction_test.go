package sqlite

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/audit"
	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/operations"
)

func TestCancelledEventContextRollsBackTransaction(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := fixedTime().Add(2 * time.Hour)
	actor := insertTestUser(t, store, domain.RoleOrganizer, "cancel-actor")
	coach := insertTestUser(t, store, domain.RoleCoach, "cancel-coach")
	route, segments := insertTestRoute(t, store, "cancel")
	leader := insertTestLeader(t, store, coach)
	participant := insertTestParticipant(t, store, "010")
	wave := insertTestWave(t, store, route, leader, actor, "cancel")
	if err := store.InsertWaveParticipant(ctx, &domain.WaveParticipant{WaveID: wave.ID, ParticipantID: participant.ID, State: domain.ParticipantDeparted, Version: 1, EnrolledAt: fixedTime()}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateWaveState(ctx, wave.ID, wave.Version, domain.WaveActive, domain.RiskLow, now); err != nil {
		t.Fatal(err)
	}
	requestCtx, cancel := context.WithCancel(ctx)
	cancel()
	service := operations.New(store, audit.New(func() time.Time { return now }), func() time.Time { return now })
	_, err := service.RecordEvent(requestCtx, operations.RecordEventInput{WaveID: wave.ID, ParticipantID: participant.ID, SegmentID: segments[0].ID, ActorID: actor.ID, Type: domain.EventCheckpoint, OccurredAt: now, IdempotencyKey: "cancelled-event-010", Details: "client disconnected", RequestID: "req-cancel-010"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request returned %v, want context.Canceled", err)
	}
	member, err := store.GetWaveParticipant(ctx, wave.ID, participant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if member.Version != 1 || member.State != domain.ParticipantDeparted {
		t.Fatalf("participant changed after cancellation: %+v", member)
	}
	var eventCount, auditCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM field_events WHERE wave_id = ?`, wave.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE object_type = 'wave' AND object_id = ?`, strconv.FormatInt(wave.ID, 10)).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 || auditCount != 0 {
		t.Fatalf("cancelled transaction left event/audit rows: events=%d audits=%d", eventCount, auditCount)
	}
}
