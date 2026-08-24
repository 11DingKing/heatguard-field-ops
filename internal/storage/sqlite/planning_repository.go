package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
)

func (s *Store) InsertRoute(ctx context.Context, route *domain.Route, segments []domain.RouteSegment) error {
	result, err := s.exec.ExecContext(ctx, `INSERT INTO routes(name, zone, activity_kind, distance_meters, active, version, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		route.Name, route.Zone, route.ActivityKind, route.DistanceMeters, route.Active, route.Version, formatTime(route.CreatedAt), formatTime(route.UpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrConflict
		}
		return fmt.Errorf("insert route: %w", err)
	}
	route.ID, err = result.LastInsertId()
	if err != nil {
		return err
	}
	for index := range segments {
		segment := &segments[index]
		segment.RouteID = route.ID
		result, err := s.exec.ExecContext(ctx, `INSERT INTO route_segments(route_id, sequence, name, latitude, longitude, hydration_site, closed_from, closed_until, version) VALUES(?,?,?,?,?,?,?,?,?)`,
			segment.RouteID, segment.Sequence, segment.Name, segment.Checkpoint.Latitude, segment.Checkpoint.Longitude, segment.HydrationSite,
			timeArg(segment.ClosedFrom), timeArg(segment.ClosedUntil), segment.Version)
		if err != nil {
			return fmt.Errorf("insert route segment %d: %w", segment.Sequence, err)
		}
		segment.ID, err = result.LastInsertId()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetRoute(ctx context.Context, id int64) (domain.Route, error) {
	var route domain.Route
	var kind, created, updated string
	err := s.exec.QueryRowContext(ctx, `SELECT id, name, zone, activity_kind, distance_meters, active, version, created_at, updated_at FROM routes WHERE id = ?`, id).
		Scan(&route.ID, &route.Name, &route.Zone, &kind, &route.DistanceMeters, &route.Active, &route.Version, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Route{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Route{}, fmt.Errorf("get route: %w", err)
	}
	route.ActivityKind = domain.ActivityKind(kind)
	if route.CreatedAt, err = parseTime(created); err != nil {
		return domain.Route{}, err
	}
	if route.UpdatedAt, err = parseTime(updated); err != nil {
		return domain.Route{}, err
	}
	return route, nil
}

func (s *Store) GetRouteSegment(ctx context.Context, id int64) (domain.RouteSegment, error) {
	var segment domain.RouteSegment
	var closedFrom, closedUntil sql.NullString
	err := s.exec.QueryRowContext(ctx, `SELECT id, route_id, sequence, name, latitude, longitude, hydration_site, closed_from, closed_until, version FROM route_segments WHERE id = ?`, id).
		Scan(&segment.ID, &segment.RouteID, &segment.Sequence, &segment.Name, &segment.Checkpoint.Latitude, &segment.Checkpoint.Longitude,
			&segment.HydrationSite, &closedFrom, &closedUntil, &segment.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RouteSegment{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.RouteSegment{}, fmt.Errorf("get route segment: %w", err)
	}
	if segment.ClosedFrom, err = optionalTime(closedFrom); err != nil {
		return domain.RouteSegment{}, err
	}
	segment.ClosedUntil, err = optionalTime(closedUntil)
	return segment, err
}

func (s *Store) ListRouteSegments(ctx context.Context, routeID int64) ([]domain.RouteSegment, error) {
	rows, err := s.exec.QueryContext(ctx, `SELECT id, route_id, sequence, name, latitude, longitude, hydration_site, closed_from, closed_until, version FROM route_segments WHERE route_id = ? ORDER BY sequence`, routeID)
	if err != nil {
		return nil, fmt.Errorf("list route segments: %w", err)
	}
	defer rows.Close()
	segments := make([]domain.RouteSegment, 0)
	for rows.Next() {
		var segment domain.RouteSegment
		var closedFrom, closedUntil sql.NullString
		if err := rows.Scan(&segment.ID, &segment.RouteID, &segment.Sequence, &segment.Name, &segment.Checkpoint.Latitude, &segment.Checkpoint.Longitude,
			&segment.HydrationSite, &closedFrom, &closedUntil, &segment.Version); err != nil {
			return nil, fmt.Errorf("scan route segment: %w", err)
		}
		if segment.ClosedFrom, err = optionalTime(closedFrom); err != nil {
			return nil, err
		}
		if segment.ClosedUntil, err = optionalTime(closedUntil); err != nil {
			return nil, err
		}
		segments = append(segments, segment)
	}
	return segments, rows.Err()
}

func (s *Store) UpdateSegmentClosure(ctx context.Context, id, version int64, from, until *time.Time) error {
	result, err := s.exec.ExecContext(ctx, `UPDATE route_segments SET closed_from = ?, closed_until = ?, version = version + 1 WHERE id = ? AND version = ?`, timeArg(from), timeArg(until), id, version)
	if err != nil {
		return fmt.Errorf("update segment closure: %w", err)
	}
	return requireVersion(result)
}

func (s *Store) InsertLeader(ctx context.Context, leader *domain.Leader) error {
	kinds := make([]string, 0, len(leader.Kinds))
	for _, kind := range leader.Kinds {
		kinds = append(kinds, string(kind))
	}
	result, err := s.exec.ExecContext(ctx, `INSERT INTO leaders(user_id, activity_kinds, qualified_until, emergency_trained, version) VALUES(?,?,?,?,?)`,
		leader.UserID, strings.Join(kinds, ","), formatTime(leader.QualifiedUntil), leader.EmergencyTrained, leader.Version)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrConflict
		}
		return fmt.Errorf("insert leader: %w", err)
	}
	leader.ID, err = result.LastInsertId()
	return err
}

func (s *Store) GetLeader(ctx context.Context, id int64) (domain.Leader, error) {
	var leader domain.Leader
	var kinds, qualified string
	err := s.exec.QueryRowContext(ctx, `SELECT id, user_id, activity_kinds, qualified_until, emergency_trained, version FROM leaders WHERE id = ?`, id).
		Scan(&leader.ID, &leader.UserID, &kinds, &qualified, &leader.EmergencyTrained, &leader.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Leader{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Leader{}, fmt.Errorf("get leader: %w", err)
	}
	for _, value := range strings.Split(kinds, ",") {
		leader.Kinds = append(leader.Kinds, domain.ActivityKind(value))
	}
	leader.QualifiedUntil, err = parseTime(qualified)
	return leader, err
}

func (s *Store) InsertParticipant(ctx context.Context, participant *domain.Participant) error {
	result, err := s.exec.ExecContext(ctx, `INSERT INTO participants(name, birth_date, guardian_user_id, emergency_name, emergency_phone, active, version, created_at) VALUES(?,?,?,?,?,?,?,?)`,
		participant.Name, formatTime(participant.BirthDate), participant.GuardianUserID, participant.EmergencyName, participant.EmergencyPhone, participant.Active, participant.Version, formatTime(participant.CreatedAt))
	if err != nil {
		return fmt.Errorf("insert participant: %w", err)
	}
	participant.ID, err = result.LastInsertId()
	return err
}

func (s *Store) GetParticipant(ctx context.Context, id int64) (domain.Participant, error) {
	var participant domain.Participant
	var birthDate, created string
	var guardian sql.NullInt64
	err := s.exec.QueryRowContext(ctx, `SELECT id, name, birth_date, guardian_user_id, emergency_name, emergency_phone, active, version, created_at FROM participants WHERE id = ?`, id).
		Scan(&participant.ID, &participant.Name, &birthDate, &guardian, &participant.EmergencyName, &participant.EmergencyPhone, &participant.Active, &participant.Version, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Participant{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Participant{}, fmt.Errorf("get participant: %w", err)
	}
	if guardian.Valid {
		participant.GuardianUserID = &guardian.Int64
	}
	if participant.BirthDate, err = parseTime(birthDate); err != nil {
		return domain.Participant{}, err
	}
	participant.CreatedAt, err = parseTime(created)
	return participant, err
}

func (s *Store) InsertRestriction(ctx context.Context, restriction *domain.HealthRestriction) error {
	result, err := s.exec.ExecContext(ctx, `INSERT INTO health_restrictions(participant_id, kind, value, effective_from, effective_to, notes) VALUES(?,?,?,?,?,?)`,
		restriction.ParticipantID, restriction.Kind, restriction.Value, formatTime(restriction.EffectiveFrom), timeArg(restriction.EffectiveTo), restriction.Notes)
	if err != nil {
		return fmt.Errorf("insert health restriction: %w", err)
	}
	restriction.ID, err = result.LastInsertId()
	return err
}

func (s *Store) ListRestrictions(ctx context.Context, participantID int64, at time.Time) ([]domain.HealthRestriction, error) {
	rows, err := s.exec.QueryContext(ctx, `SELECT id, participant_id, kind, value, effective_from, effective_to, notes FROM health_restrictions
		WHERE participant_id = ? AND effective_from <= ? AND (effective_to IS NULL OR effective_to > ?) ORDER BY effective_from`, participantID, formatTime(at), formatTime(at))
	if err != nil {
		return nil, fmt.Errorf("list health restrictions: %w", err)
	}
	defer rows.Close()
	restrictions := make([]domain.HealthRestriction, 0)
	for rows.Next() {
		var restriction domain.HealthRestriction
		var kind, from string
		var until sql.NullString
		if err := rows.Scan(&restriction.ID, &restriction.ParticipantID, &kind, &restriction.Value, &from, &until, &restriction.Notes); err != nil {
			return nil, err
		}
		restriction.Kind = domain.RestrictionKind(kind)
		if restriction.EffectiveFrom, err = parseTime(from); err != nil {
			return nil, err
		}
		if restriction.EffectiveTo, err = optionalTime(until); err != nil {
			return nil, err
		}
		restrictions = append(restrictions, restriction)
	}
	return restrictions, rows.Err()
}

func (s *Store) InsertRiskRule(ctx context.Context, rule *domain.RiskRule) error {
	result, err := s.exec.ExecContext(ctx, `INSERT INTO risk_rules(zone, activity_kind, level, temperature_min, heat_index_min, effective_from, effective_to, action, version, created_by) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		rule.Zone, rule.ActivityKind, rule.Level, rule.TemperatureMin, rule.HeatIndexMin, formatTime(rule.EffectiveFrom), formatTime(rule.EffectiveTo), rule.Action, rule.Version, rule.CreatedBy)
	if err != nil {
		return fmt.Errorf("insert risk rule: %w", err)
	}
	rule.ID, err = result.LastInsertId()
	return err
}

func (s *Store) ListRiskRules(ctx context.Context, zone string, kind domain.ActivityKind, at time.Time) ([]domain.RiskRule, error) {
	rows, err := s.exec.QueryContext(ctx, `SELECT id, zone, activity_kind, level, temperature_min, heat_index_min, effective_from, effective_to, action, version, created_by
		FROM risk_rules WHERE zone = ? AND activity_kind = ? AND effective_from <= ? AND effective_to > ? ORDER BY effective_from DESC`, zone, kind, formatTime(at), formatTime(at))
	if err != nil {
		return nil, fmt.Errorf("list risk rules: %w", err)
	}
	defer rows.Close()
	result := make([]domain.RiskRule, 0)
	for rows.Next() {
		var rule domain.RiskRule
		var kindValue, level, from, until string
		if err := rows.Scan(&rule.ID, &rule.Zone, &kindValue, &level, &rule.TemperatureMin, &rule.HeatIndexMin, &from, &until, &rule.Action, &rule.Version, &rule.CreatedBy); err != nil {
			return nil, err
		}
		rule.ActivityKind = domain.ActivityKind(kindValue)
		rule.Level = domain.RiskLevel(level)
		if rule.EffectiveFrom, err = parseTime(from); err != nil {
			return nil, err
		}
		if rule.EffectiveTo, err = parseTime(until); err != nil {
			return nil, err
		}
		result = append(result, rule)
	}
	return result, rows.Err()
}

func timeArg(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func requireVersion(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return domain.ErrVersionConflict
	}
	return nil
}
