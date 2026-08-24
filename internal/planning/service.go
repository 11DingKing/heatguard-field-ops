package planning

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/audit"
	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
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

type CreateRouteInput struct {
	Name           string
	Zone           string
	Kind           domain.ActivityKind
	DistanceMeters int
	Segments       []domain.RouteSegment
	ActorID        int64
	RequestID      string
}

func (s *Service) CreateRoute(ctx context.Context, input CreateRouteInput) (domain.Route, []domain.RouteSegment, error) {
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Zone) == "" {
		return domain.Route{}, nil, domain.Validation("route", "name and zone are required")
	}
	if !input.Kind.Valid() || input.DistanceMeters <= 0 {
		return domain.Route{}, nil, domain.Validation("route", "kind and distance must be valid")
	}
	if len(input.Segments) < 2 {
		return domain.Route{}, nil, domain.Validation("segments", "at least two segments are required")
	}
	for index := range input.Segments {
		input.Segments[index].Sequence = index + 1
		input.Segments[index].Version = 1
		if !input.Segments[index].Checkpoint.Valid() || strings.TrimSpace(input.Segments[index].Name) == "" {
			return domain.Route{}, nil, domain.Validation("segments", "each segment needs a name and valid checkpoint")
		}
	}
	now := s.now().UTC()
	route := domain.Route{Name: strings.TrimSpace(input.Name), Zone: strings.TrimSpace(input.Zone), ActivityKind: input.Kind, DistanceMeters: input.DistanceMeters, Active: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	err := s.store.WithinTx(ctx, func(tx repository.Tx) error {
		if err := tx.InsertRoute(ctx, &route, input.Segments); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, input.ActorID, "route.create", "route", route.ID, "success", input.RequestID, map[string]any{"segments": len(input.Segments)})
	})
	return route, input.Segments, err
}

func (s *Service) RegisterLeader(ctx context.Context, leader domain.Leader, actorID int64, requestID string) (domain.Leader, error) {
	if leader.UserID <= 0 || len(leader.Kinds) == 0 || !leader.QualifiedUntil.After(s.now()) {
		return domain.Leader{}, domain.Validation("leader", "user, qualifications and future expiry are required")
	}
	for _, kind := range leader.Kinds {
		if !kind.Valid() {
			return domain.Leader{}, domain.Validation("activity_kinds", "contains an unsupported kind")
		}
	}
	leader.Version = 1
	err := s.store.WithinTx(ctx, func(tx repository.Tx) error {
		user, err := tx.GetUser(ctx, leader.UserID)
		if err != nil {
			return err
		}
		if user.Role != domain.RoleCoach || !user.Active {
			return domain.ErrConflict
		}
		if err := tx.InsertLeader(ctx, &leader); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actorID, "leader.register", "leader", leader.ID, "success", requestID, map[string]any{"user_id": leader.UserID})
	})
	return leader, err
}

func (s *Service) RegisterParticipant(ctx context.Context, participant domain.Participant, actorID int64, requestID string) (domain.Participant, error) {
	if strings.TrimSpace(participant.Name) == "" || participant.BirthDate.IsZero() || strings.TrimSpace(participant.EmergencyPhone) == "" {
		return domain.Participant{}, domain.Validation("participant", "name, birth date and emergency contact are required")
	}
	participant.Active = true
	participant.Version = 1
	participant.CreatedAt = s.now().UTC()
	err := s.store.WithinTx(ctx, func(tx repository.Tx) error {
		if participant.GuardianUserID != nil {
			guardian, err := tx.GetUser(ctx, *participant.GuardianUserID)
			if err != nil {
				return err
			}
			if guardian.Role != domain.RoleGuardian || !guardian.Active {
				return domain.ErrConflict
			}
		}
		if err := tx.InsertParticipant(ctx, &participant); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actorID, "participant.register", "participant", participant.ID, "success", requestID, nil)
	})
	return participant, err
}

func (s *Service) AddRestriction(ctx context.Context, restriction domain.HealthRestriction, actorID int64, requestID string) (domain.HealthRestriction, error) {
	if restriction.ParticipantID <= 0 || restriction.EffectiveFrom.IsZero() || (restriction.EffectiveTo != nil && !restriction.EffectiveTo.After(restriction.EffectiveFrom)) {
		return domain.HealthRestriction{}, domain.Validation("restriction", "participant and valid effective window are required")
	}
	err := s.store.WithinTx(ctx, func(tx repository.Tx) error {
		participant, err := tx.GetParticipant(ctx, restriction.ParticipantID)
		if err != nil {
			return err
		}
		actor, err := tx.GetUser(ctx, actorID)
		if err != nil {
			return err
		}
		if actor.Role == domain.RoleGuardian && (participant.GuardianUserID == nil || *participant.GuardianUserID != actorID) {
			return domain.ErrForbidden
		}
		if actor.Role != domain.RoleGuardian && actor.Role != domain.RoleOrganizer {
			return domain.ErrForbidden
		}
		if err := tx.InsertRestriction(ctx, &restriction); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actorID, "restriction.create", "participant", restriction.ParticipantID, "success", requestID, map[string]any{"kind": restriction.Kind})
	})
	return restriction, err
}

func (s *Service) AddRiskRule(ctx context.Context, rule domain.RiskRule, actorID int64, requestID string) (domain.RiskRule, error) {
	if strings.TrimSpace(rule.Zone) == "" || !rule.ActivityKind.Valid() || rule.Level.Rank() < 0 || !rule.EffectiveTo.After(rule.EffectiveFrom) || strings.TrimSpace(rule.Action) == "" {
		return domain.RiskRule{}, domain.Validation("risk_rule", "scope, level, action and valid effective window are required")
	}
	rule.Version = 1
	rule.CreatedBy = actorID
	err := s.store.WithinTx(ctx, func(tx repository.Tx) error {
		if err := tx.InsertRiskRule(ctx, &rule); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actorID, "risk_rule.create", "risk_rule", rule.ID, "success", requestID, map[string]any{"zone": rule.Zone, "activity_kind": rule.ActivityKind})
	})
	return rule, err
}

type CreateWaveInput struct {
	RouteID, LeaderID, ActorID int64
	Name                       string
	Start, End                 time.Time
	Capacity                   int
	RequestID                  string
}

func (s *Service) CreateWave(ctx context.Context, input CreateWaveInput) (domain.ActivityWave, error) {
	if input.Capacity < 1 || !input.End.After(input.Start) || strings.TrimSpace(input.Name) == "" {
		return domain.ActivityWave{}, domain.Validation("wave", "name, positive capacity and ordered times are required")
	}
	var wave domain.ActivityWave
	err := s.store.WithinTx(ctx, func(tx repository.Tx) error {
		route, err := tx.GetRoute(ctx, input.RouteID)
		if err != nil {
			return err
		}
		leader, err := tx.GetLeader(ctx, input.LeaderID)
		if err != nil {
			return err
		}
		if !route.Active || !leader.QualifiedFor(route.ActivityKind, input.Start) {
			return domain.ErrConflict
		}
		now := s.now().UTC()
		wave = domain.ActivityWave{RouteID: input.RouteID, LeaderID: input.LeaderID, Name: strings.TrimSpace(input.Name), ScheduledStart: input.Start.UTC(), ExpectedEnd: input.End.UTC(), Capacity: input.Capacity, State: domain.WaveDraft, DepartureRisk: domain.RiskLow, Version: 1, CreatedBy: input.ActorID, CreatedAt: now, UpdatedAt: now}
		if err := tx.InsertWave(ctx, &wave); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, input.ActorID, "wave.create", "wave", wave.ID, "success", input.RequestID, map[string]any{"route_id": input.RouteID})
	})
	return wave, err
}

func (s *Service) Enroll(ctx context.Context, waveID, participantID, actorID int64, requestID string) (domain.WaveParticipant, error) {
	var enrollment domain.WaveParticipant
	err := s.store.WithinTx(ctx, func(tx repository.Tx) error {
		wave, err := tx.GetWave(ctx, waveID)
		if err != nil {
			return err
		}
		if wave.State != domain.WaveDraft && wave.State != domain.WaveReady {
			return domain.ErrInvalidState
		}
		participant, err := tx.GetParticipant(ctx, participantID)
		if err != nil {
			return err
		}
		if !participant.Active {
			return domain.ErrConflict
		}
		members, err := tx.ListWaveParticipants(ctx, waveID)
		if err != nil {
			return err
		}
		if len(members) >= wave.Capacity {
			return domain.ErrCapacity
		}
		restrictions, err := tx.ListRestrictions(ctx, participantID, wave.ScheduledStart)
		if err != nil {
			return err
		}
		for _, restriction := range restrictions {
			if restriction.Kind == domain.RestrictionNeedsGuardian && participant.GuardianUserID == nil {
				return domain.ErrConflict
			}
		}
		enrollment = domain.WaveParticipant{WaveID: waveID, ParticipantID: participantID, State: domain.ParticipantEnrolled, Version: 1, EnrolledAt: s.now().UTC()}
		if err := tx.InsertWaveParticipant(ctx, &enrollment); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actorID, "wave.enroll", "wave", waveID, "success", requestID, map[string]any{"participant_id": participantID})
	})
	return enrollment, err
}

func (s *Service) MarkReady(ctx context.Context, waveID, version, actorID int64, requestID string) error {
	return s.store.WithinTx(ctx, func(tx repository.Tx) error {
		wave, err := tx.GetWave(ctx, waveID)
		if err != nil {
			return err
		}
		if err := domain.ValidateWaveTransition(wave.State, domain.WaveReady); err != nil {
			return err
		}
		members, err := tx.ListWaveParticipants(ctx, waveID)
		if err != nil {
			return err
		}
		if len(members) == 0 {
			return domain.ErrConflict
		}
		if err := tx.UpdateWaveState(ctx, waveID, version, domain.WaveReady, wave.DepartureRisk, s.now().UTC()); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actorID, "wave.ready", "wave", waveID, "success", requestID, map[string]any{"participants": len(members)})
	})
}

func (s *Service) CloseSegment(ctx context.Context, segmentID, version, actorID int64, from, until *time.Time, requestID string) error {
	if from == nil || (until != nil && !until.After(*from)) {
		return domain.Validation("closure", "requires a valid time window")
	}
	return s.store.WithinTx(ctx, func(tx repository.Tx) error {
		if err := tx.UpdateSegmentClosure(ctx, segmentID, version, from, until); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actorID, "segment.close", "route_segment", segmentID, "success", requestID, map[string]any{"from": from, "until": until})
	})
}

func (s *Service) GetWave(ctx context.Context, id int64) (domain.ActivityWave, []domain.WaveParticipant, error) {
	wave, err := s.store.GetWave(ctx, id)
	if err != nil {
		return domain.ActivityWave{}, nil, fmt.Errorf("get wave: %w", err)
	}
	members, err := s.store.ListWaveParticipants(ctx, id)
	return wave, members, err
}
