package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/audit"
	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
	"github.com/11DingKing/heatguard-field-ops/internal/risk"
)

type Service struct {
	store repository.Store
	audit *audit.Service
	now   func() time.Time
}

func New(store repository.Store, auditService *audit.Service, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, audit: auditService, now: now}
}

type DepartInput struct {
	WaveID, Version, ActorID  int64
	Temperature, HeatIndex    float64
	IdempotencyKey, RequestID string
}

func (s *Service) Depart(ctx context.Context, input DepartInput) (domain.ActivityWave, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return domain.ActivityWave{}, domain.Validation("idempotency_key", "is required")
	}
	scope := "wave:" + strconv.FormatInt(input.WaveID, 10)
	if value, ok, err := s.store.GetIdempotencyResult(ctx, scope, "depart", input.IdempotencyKey); err != nil {
		return domain.ActivityWave{}, err
	} else if ok {
		id, _ := strconv.ParseInt(value, 10, 64)
		return s.store.GetWave(ctx, id)
	}
	var departed domain.ActivityWave
	err := s.store.WithinTx(ctx, func(tx repository.Tx) error {
		wave, err := tx.GetWave(ctx, input.WaveID)
		if err != nil {
			return err
		}
		if wave.State != domain.WaveReady {
			return domain.ErrInvalidState
		}
		route, err := tx.GetRoute(ctx, wave.RouteID)
		if err != nil {
			return err
		}
		leader, err := tx.GetLeader(ctx, wave.LeaderID)
		if err != nil {
			return err
		}
		if !leader.QualifiedFor(route.ActivityKind, wave.ScheduledStart) {
			return domain.ErrConflict
		}
		segments, err := tx.ListRouteSegments(ctx, route.ID)
		if err != nil {
			return err
		}
		for _, segment := range segments {
			if segment.ClosedAt(wave.ScheduledStart) {
				return domain.ErrRiskBlocked
			}
		}
		members, err := tx.ListWaveParticipants(ctx, wave.ID)
		if err != nil {
			return err
		}
		if len(members) == 0 {
			return domain.ErrConflict
		}
		heatRestricted := false
		for _, member := range members {
			restrictions, err := tx.ListRestrictions(ctx, member.ParticipantID, wave.ScheduledStart)
			if err != nil {
				return err
			}
			for _, restriction := range restrictions {
				if restriction.Kind == domain.RestrictionNoExtremeHeat {
					heatRestricted = true
				}
			}
		}
		rules, err := tx.ListRiskRules(ctx, route.Zone, route.ActivityKind, wave.ScheduledStart)
		if err != nil {
			return err
		}
		decision := risk.Evaluate(rules, risk.Observation{Zone: route.Zone, Kind: route.ActivityKind, At: wave.ScheduledStart, Temperature: input.Temperature, HeatIndex: input.HeatIndex})
		if err := risk.DepartureAllowed(decision, heatRestricted); err != nil {
			return err
		}
		now := s.now().UTC()
		if err := tx.UpdateWaveState(ctx, wave.ID, input.Version, domain.WaveActive, decision.Level, now); err != nil {
			return err
		}
		for _, member := range members {
			if err := tx.UpdateParticipantState(ctx, wave.ID, member.ParticipantID, member.Version, domain.ParticipantDeparted, nil, now, ""); err != nil {
				return err
			}
		}
		job := domain.WorkerJob{Kind: "checkpoint_watch", DedupeKey: fmt.Sprintf("wave:%d:checkpoint-watch", wave.ID), Payload: fmt.Sprintf(`{"wave_id":%d}`, wave.ID), Status: domain.JobPending, MaxAttempts: 5, AvailableAt: now.Add(15 * time.Minute), CreatedAt: now, UpdatedAt: now}
		if _, err := tx.InsertJob(ctx, &job); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, input.ActorID, "wave.depart", "wave", wave.ID, "success", input.RequestID, map[string]any{"risk": decision.Level, "rules": decision.MatchedRules}); err != nil {
			return err
		}
		if _, err := tx.InsertIdempotencyResult(ctx, scope, "depart", input.IdempotencyKey, strconv.FormatInt(wave.ID, 10), now); err != nil {
			return err
		}
		departed = wave
		departed.State = domain.WaveActive
		departed.DepartureRisk = decision.Level
		departed.Version++
		return nil
	})
	return departed, err
}

type RecordEventInput struct {
	WaveID, ParticipantID, SegmentID, ActorID int64
	Type                                      domain.FieldEventType
	OccurredAt                                time.Time
	IdempotencyKey, Details, RequestID        string
}

func (s *Service) RecordEvent(ctx context.Context, input RecordEventInput) (domain.FieldEvent, error) {
	if input.ParticipantID <= 0 || strings.TrimSpace(input.IdempotencyKey) == "" {
		return domain.FieldEvent{}, domain.Validation("event", "participant and idempotency key are required")
	}
	if existing, err := s.store.GetFieldEventByKey(ctx, input.WaveID, input.IdempotencyKey); err == nil {
		return existing, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.FieldEvent{}, err
	}
	var event domain.FieldEvent
	err := s.store.WithinTx(ctx, func(tx repository.Tx) error {
		wave, err := tx.GetWave(ctx, input.WaveID)
		if err != nil {
			return err
		}
		if wave.State != domain.WaveActive && wave.State != domain.WaveWithdrawing && wave.State != domain.WaveSplit {
			return domain.ErrInvalidState
		}
		member, err := tx.GetWaveParticipant(ctx, input.WaveID, input.ParticipantID)
		if err != nil {
			return err
		}
		next, err := domain.StateForEvent(member.State, input.Type)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		occurred := input.OccurredAt.UTC()
		if occurred.IsZero() {
			occurred = now
		}
		if occurred.After(now.Add(5 * time.Minute)) {
			return domain.Validation("occurred_at", "cannot be in the future")
		}
		var segment *int64
		if input.SegmentID > 0 {
			segment = &input.SegmentID
		}
		disposition := ""
		if next.Terminal() {
			disposition = string(next)
		}
		if err := tx.UpdateParticipantState(ctx, input.WaveID, input.ParticipantID, member.Version, next, segment, occurred, disposition); err != nil {
			return err
		}
		event = domain.FieldEvent{WaveID: input.WaveID, ParticipantID: &input.ParticipantID, SegmentID: segment, Type: input.Type, OccurredAt: occurred, RecordedAt: now, RecordedBy: input.ActorID, IdempotencyKey: input.IdempotencyKey, Details: input.Details}
		if err := tx.InsertFieldEvent(ctx, &event); err != nil {
			return err
		}
		if input.Type == domain.EventMissing {
			alert := domain.Alert{WaveID: input.WaveID, ParticipantID: &input.ParticipantID, Kind: "participant_missing", Severity: domain.RiskExtreme, Status: domain.AlertOpen, DedupeKey: fmt.Sprintf("missing:%d:%d", input.WaveID, input.ParticipantID), OpenedAt: now, Version: 1}
			if _, err := tx.InsertAlert(ctx, &alert); err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]any{"alert_id": alert.ID, "participant_id": input.ParticipantID})
			job := domain.WorkerJob{Kind: "notify_emergency_contact", DedupeKey: fmt.Sprintf("alert:%d:notify", alert.ID), Payload: string(payload), Status: domain.JobPending, MaxAttempts: 5, AvailableAt: now, CreatedAt: now, UpdatedAt: now}
			if _, err := tx.InsertJob(ctx, &job); err != nil {
				return err
			}
		}
		return s.audit.Record(ctx, tx, input.ActorID, "field_event.record", "wave", input.WaveID, "success", input.RequestID, map[string]any{"event_id": event.ID, "type": event.Type, "participant_id": input.ParticipantID})
	})
	return event, err
}

func (s *Service) AcknowledgeAlert(ctx context.Context, alertID, version, actorID int64, requestID string) error {
	return s.store.WithinTx(ctx, func(tx repository.Tx) error {
		alert, err := tx.GetAlert(ctx, alertID)
		if err != nil {
			return err
		}
		if alert.Status != domain.AlertOpen {
			return domain.ErrInvalidState
		}
		if err := tx.UpdateAlertStatus(ctx, alertID, version, domain.AlertAcknowledged, s.now().UTC()); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actorID, "alert.acknowledge", "alert", alertID, "success", requestID, nil)
	})
}

func (s *Service) CloseWave(ctx context.Context, waveID, version, actorID int64, requestID string) error {
	return s.store.WithinTx(ctx, func(tx repository.Tx) error {
		wave, err := tx.GetWave(ctx, waveID)
		if err != nil {
			return err
		}
		if err := domain.ValidateWaveTransition(wave.State, domain.WaveClosed); err != nil {
			return err
		}
		members, err := tx.ListWaveParticipants(ctx, waveID)
		if err != nil {
			return err
		}
		for _, member := range members {
			if !member.State.Terminal() {
				return domain.ErrPendingSafety
			}
		}
		alerts, err := tx.ListOpenAlerts(ctx, waveID)
		if err != nil {
			return err
		}
		for _, alert := range alerts {
			if alert.Status == domain.AlertOpen {
				return domain.ErrPendingSafety
			}
		}
		if err := tx.UpdateWaveState(ctx, waveID, version, domain.WaveClosed, wave.DepartureRisk, s.now().UTC()); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actorID, "wave.close", "wave", waveID, "success", requestID, map[string]any{"participants": len(members)})
	})
}

func (s *Service) SplitWave(ctx context.Context, waveID, version, actorID int64, participantIDs []int64, name, requestID string) (domain.ActivityWave, error) {
	if len(participantIDs) == 0 {
		return domain.ActivityWave{}, domain.Validation("participants", "at least one participant is required")
	}
	var child domain.ActivityWave
	err := s.store.WithinTx(ctx, func(tx repository.Tx) error {
		parent, err := tx.GetWave(ctx, waveID)
		if err != nil {
			return err
		}
		if parent.State != domain.WaveActive && parent.State != domain.WaveWithdrawing {
			return domain.ErrInvalidState
		}
		members, err := tx.ListWaveParticipants(ctx, waveID)
		if err != nil {
			return err
		}
		selected := map[int64]bool{}
		for _, id := range participantIDs {
			selected[id] = true
		}
		if len(selected) >= len(members) {
			return domain.ErrConflict
		}
		now := s.now().UTC()
		child = parent
		child.ID = 0
		child.ParentWaveID = &parent.ID
		child.Name = name
		child.Capacity = len(selected)
		child.State = domain.WaveActive
		child.Version = 1
		child.CreatedAt = now
		child.UpdatedAt = now
		if err := tx.InsertWave(ctx, &child); err != nil {
			return err
		}
		for _, member := range members {
			if selected[member.ParticipantID] {
				member.WaveID = child.ID
				member.Version = 1
				member.EnrolledAt = now
				if err := tx.InsertWaveParticipant(ctx, &member); err != nil {
					return err
				}
				if err := tx.UpdateParticipantState(ctx, parent.ID, member.ParticipantID, member.Version, domain.ParticipantWithdrawn, member.LastSegmentID, now, "split_to:"+strconv.FormatInt(child.ID, 10)); err != nil {
					return err
				}
			}
		}
		if err := tx.UpdateWaveState(ctx, parent.ID, version, domain.WaveSplit, parent.DepartureRisk, now); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actorID, "wave.split", "wave", parent.ID, "success", requestID, map[string]any{"child_wave_id": child.ID, "participants": participantIDs})
	})
	return child, err
}
