package sqlite

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/audit"
	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/operations"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
)

func TestFieldEventReplayPreservesOriginalRecord(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := fixedTime().Add(2 * time.Hour)
	actor := insertTestUser(t, store, domain.RoleOrganizer, "dedupe-actor")
	coach := insertTestUser(t, store, domain.RoleCoach, "dedupe-coach")
	route, segments := insertTestRoute(t, store, "dedupe")
	leader := insertTestLeader(t, store, coach)
	participant := insertTestParticipant(t, store, "009")
	wave := insertTestWave(t, store, route, leader, actor, "dedupe")
	if err := store.InsertWaveParticipant(ctx, &domain.WaveParticipant{
		WaveID: wave.ID, ParticipantID: participant.ID, State: domain.ParticipantDeparted,
		Version: 1, EnrolledAt: fixedTime(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateWaveState(ctx, wave.ID, wave.Version, domain.WaveActive, domain.RiskLow, now); err != nil {
		t.Fatal(err)
	}
	service := operations.New(store, audit.New(func() time.Time { return now }), func() time.Time { return now })
	input := operations.RecordEventInput{
		WaveID: wave.ID, ParticipantID: participant.ID, SegmentID: segments[0].ID,
		ActorID: actor.ID, Type: domain.EventCheckpoint, OccurredAt: now,
		IdempotencyKey: "mobile-replay-009", Details: "assembly checkpoint", RequestID: "req-009",
	}
	first, err := service.RecordEvent(ctx, input)
	if err != nil || first.ID == 0 {
		t.Fatalf("first record = %+v, %v", first, err)
	}
	memberAfterFirst, err := store.GetWaveParticipant(ctx, wave.ID, participant.ID)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.RecordEvent(ctx, input)
	if err != nil {
		t.Fatalf("idempotent replay returned error: %v", err)
	}
	if replayed.ID != first.ID || replayed.Details != first.Details {
		t.Fatalf("replay = %+v, want original %+v", replayed, first)
	}
	memberAfterReplay, err := store.GetWaveParticipant(ctx, wave.ID, participant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if memberAfterReplay.Version != memberAfterFirst.Version || memberAfterReplay.State != memberAfterFirst.State {
		t.Fatalf("replay changed participant state: first=%+v replay=%+v", memberAfterFirst, memberAfterReplay)
	}
	events, err := store.ListAuditEvents(ctx, "wave", strconv.FormatInt(wave.ID, 10), repository.Page{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Metadata == "" || events[0].Action != "field_event.record" {
		t.Fatalf("audit events = %+v", events)
	}
	if len(events) > 0 && strings.Contains(events[0].Metadata, `"event_id":0`) {
		t.Fatalf("audit metadata contains zero event id: %s", events[0].Metadata)
	}
}
