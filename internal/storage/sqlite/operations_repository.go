package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
)

func (s *Store) InsertWave(ctx context.Context, wave *domain.ActivityWave) error {
	result, err := s.exec.ExecContext(ctx, `INSERT INTO activity_waves(route_id, leader_id, parent_wave_id, name, scheduled_start, expected_end, capacity, state, departure_risk, version, created_by, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, wave.RouteID, wave.LeaderID, wave.ParentWaveID, wave.Name, formatTime(wave.ScheduledStart), formatTime(wave.ExpectedEnd),
		wave.Capacity, wave.State, wave.DepartureRisk, wave.Version, wave.CreatedBy, formatTime(wave.CreatedAt), formatTime(wave.UpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrConflict
		}
		return fmt.Errorf("insert wave: %w", err)
	}
	wave.ID, err = result.LastInsertId()
	return err
}

func (s *Store) GetWave(ctx context.Context, id int64) (domain.ActivityWave, error) {
	row := s.exec.QueryRowContext(ctx, `SELECT id, route_id, leader_id, parent_wave_id, name, scheduled_start, expected_end, capacity, state, departure_risk, version, created_by, created_at, updated_at
		FROM activity_waves WHERE id = ?`, id)
	return scanWave(row)
}

func scanWave(row rowScanner) (domain.ActivityWave, error) {
	var wave domain.ActivityWave
	var parent sql.NullInt64
	var start, end, state, risk, created, updated string
	if err := row.Scan(&wave.ID, &wave.RouteID, &wave.LeaderID, &parent, &wave.Name, &start, &end, &wave.Capacity, &state, &risk, &wave.Version, &wave.CreatedBy, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ActivityWave{}, domain.ErrNotFound
		}
		return domain.ActivityWave{}, fmt.Errorf("scan wave: %w", err)
	}
	if parent.Valid {
		wave.ParentWaveID = &parent.Int64
	}
	wave.State = domain.WaveState(state)
	wave.DepartureRisk = domain.RiskLevel(risk)
	var err error
	if wave.ScheduledStart, err = parseTime(start); err != nil {
		return domain.ActivityWave{}, err
	}
	if wave.ExpectedEnd, err = parseTime(end); err != nil {
		return domain.ActivityWave{}, err
	}
	if wave.CreatedAt, err = parseTime(created); err != nil {
		return domain.ActivityWave{}, err
	}
	wave.UpdatedAt, err = parseTime(updated)
	return wave, err
}

func (s *Store) ListWaves(ctx context.Context, filter repository.WaveFilter) ([]domain.ActivityWave, int, error) {
	page := filter.Page.Normalize()
	where := []string{"1=1"}
	args := make([]any, 0)
	if filter.RouteID > 0 {
		where = append(where, "route_id = ?")
		args = append(args, filter.RouteID)
	}
	if filter.LeaderID > 0 {
		where = append(where, "leader_id = ?")
		args = append(args, filter.LeaderID)
	}
	if filter.StartFrom != nil {
		where = append(where, "scheduled_start >= ?")
		args = append(args, formatTime(*filter.StartFrom))
	}
	if filter.StartTo != nil {
		where = append(where, "scheduled_start < ?")
		args = append(args, formatTime(*filter.StartTo))
	}
	if len(filter.States) > 0 {
		placeholders := make([]string, len(filter.States))
		for index, state := range filter.States {
			placeholders[index] = "?"
			args = append(args, state)
		}
		where = append(where, "state IN ("+strings.Join(placeholders, ",")+")")
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.exec.QueryRowContext(ctx, "SELECT COUNT(*) FROM activity_waves WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count waves: %w", err)
	}
	queryArgs := append(append([]any{}, args...), page.Limit, page.Offset)
	rows, err := s.exec.QueryContext(ctx, `SELECT id, route_id, leader_id, parent_wave_id, name, scheduled_start, expected_end, capacity, state, departure_risk, version, created_by, created_at, updated_at
		FROM activity_waves WHERE `+clause+` ORDER BY scheduled_start, id LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list waves: %w", err)
	}
	defer rows.Close()
	waves := make([]domain.ActivityWave, 0)
	for rows.Next() {
		wave, err := scanWave(rows)
		if err != nil {
			return nil, 0, err
		}
		waves = append(waves, wave)
	}
	return waves, total, rows.Err()
}

func (s *Store) InsertWaveParticipant(ctx context.Context, participant *domain.WaveParticipant) error {
	_, err := s.exec.ExecContext(ctx, `INSERT INTO wave_participants(wave_id, participant_id, state, last_segment_id, last_reported_at, disposition, version, enrolled_at)
		VALUES(?,?,?,?,?,?,?,?)`, participant.WaveID, participant.ParticipantID, participant.State, participant.LastSegmentID, timeArg(participant.LastReportedAt),
		participant.Disposition, participant.Version, formatTime(participant.EnrolledAt))
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrConflict
		}
		return fmt.Errorf("insert wave participant: %w", err)
	}
	return nil
}

func (s *Store) GetWaveParticipant(ctx context.Context, waveID, participantID int64) (domain.WaveParticipant, error) {
	row := s.exec.QueryRowContext(ctx, `SELECT wave_id, participant_id, state, last_segment_id, last_reported_at, disposition, version, enrolled_at
		FROM wave_participants WHERE wave_id = ? AND participant_id = ?`, waveID, participantID)
	return scanWaveParticipant(row)
}

func scanWaveParticipant(row rowScanner) (domain.WaveParticipant, error) {
	var participant domain.WaveParticipant
	var state, enrolled string
	var segment sql.NullInt64
	var reported sql.NullString
	if err := row.Scan(&participant.WaveID, &participant.ParticipantID, &state, &segment, &reported, &participant.Disposition, &participant.Version, &enrolled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.WaveParticipant{}, domain.ErrNotFound
		}
		return domain.WaveParticipant{}, fmt.Errorf("scan wave participant: %w", err)
	}
	participant.State = domain.ParticipantState(state)
	if segment.Valid {
		participant.LastSegmentID = &segment.Int64
	}
	var err error
	if participant.LastReportedAt, err = optionalTime(reported); err != nil {
		return domain.WaveParticipant{}, err
	}
	participant.EnrolledAt, err = parseTime(enrolled)
	return participant, err
}

func (s *Store) ListWaveParticipants(ctx context.Context, waveID int64) ([]domain.WaveParticipant, error) {
	rows, err := s.exec.QueryContext(ctx, `SELECT wave_id, participant_id, state, last_segment_id, last_reported_at, disposition, version, enrolled_at
		FROM wave_participants WHERE wave_id = ? ORDER BY participant_id`, waveID)
	if err != nil {
		return nil, fmt.Errorf("list wave participants: %w", err)
	}
	defer rows.Close()
	result := make([]domain.WaveParticipant, 0)
	for rows.Next() {
		participant, err := scanWaveParticipant(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, participant)
	}
	return result, rows.Err()
}

func (s *Store) UpdateWaveState(ctx context.Context, id, version int64, state domain.WaveState, risk domain.RiskLevel, at time.Time) error {
	result, err := s.exec.ExecContext(ctx, `UPDATE activity_waves SET state = ?, departure_risk = ?, version = version + 1, updated_at = ? WHERE id = ? AND version = ?`,
		state, risk, formatTime(at), id, version)
	if err != nil {
		return fmt.Errorf("update wave state: %w", err)
	}
	return requireVersion(result)
}

func (s *Store) UpdateParticipantState(ctx context.Context, waveID, participantID, version int64, state domain.ParticipantState, segmentID *int64, at time.Time, disposition string) error {
	result, err := s.exec.ExecContext(ctx, `UPDATE wave_participants SET state = ?, last_segment_id = COALESCE(?, last_segment_id), last_reported_at = ?, disposition = ?, version = version + 1
		WHERE wave_id = ? AND participant_id = ? AND version = ?`, state, segmentID, formatTime(at), disposition, waveID, participantID, version)
	if err != nil {
		return fmt.Errorf("update wave participant: %w", err)
	}
	return requireVersion(result)
}

func (s *Store) InsertFieldEvent(ctx context.Context, event *domain.FieldEvent) error {
	result, err := s.exec.ExecContext(ctx, `INSERT INTO field_events(wave_id, participant_id, segment_id, type, occurred_at, recorded_at, recorded_by, idempotency_key, details)
		VALUES(?,?,?,?,?,?,?,?,?)`, event.WaveID, event.ParticipantID, event.SegmentID, event.Type, formatTime(event.OccurredAt), formatTime(event.RecordedAt), event.RecordedBy, event.IdempotencyKey, event.Details)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrConflict
		}
		return fmt.Errorf("insert field event: %w", err)
	}
	event.ID, err = result.LastInsertId()
	return err
}

func (s *Store) GetFieldEventByKey(ctx context.Context, waveID int64, key string) (domain.FieldEvent, error) {
	var event domain.FieldEvent
	var participant, segment sql.NullInt64
	var kind, occurred, recorded string
	err := s.exec.QueryRowContext(ctx, `SELECT id, wave_id, participant_id, segment_id, type, occurred_at, recorded_at, recorded_by, idempotency_key, details
		FROM field_events WHERE wave_id = ? AND idempotency_key = ?`, waveID, key).
		Scan(&event.ID, &event.WaveID, &participant, &segment, &kind, &occurred, &recorded, &event.RecordedBy, &event.IdempotencyKey, &event.Details)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.FieldEvent{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.FieldEvent{}, fmt.Errorf("get field event: %w", err)
	}
	if participant.Valid {
		event.ParticipantID = &participant.Int64
	}
	if segment.Valid {
		event.SegmentID = &segment.Int64
	}
	event.Type = domain.FieldEventType(kind)
	if event.OccurredAt, err = parseTime(occurred); err != nil {
		return domain.FieldEvent{}, err
	}
	event.RecordedAt, err = parseTime(recorded)
	return event, err
}

func (s *Store) InsertAlert(ctx context.Context, alert *domain.Alert) (bool, error) {
	result, err := s.exec.ExecContext(ctx, `INSERT INTO alerts(wave_id, participant_id, kind, severity, status, dedupe_key, opened_at, acknowledged_at, resolved_at, version)
		VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(dedupe_key) DO NOTHING`, alert.WaveID, alert.ParticipantID, alert.Kind, alert.Severity, alert.Status,
		alert.DedupeKey, formatTime(alert.OpenedAt), timeArg(alert.AcknowledgedAt), timeArg(alert.ResolvedAt), alert.Version)
	if err != nil {
		return false, fmt.Errorf("insert alert: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count == 1 {
		alert.ID, err = result.LastInsertId()
		return true, err
	}
	existing, err := s.getAlertByDedupeKey(ctx, alert.DedupeKey)
	if err != nil {
		return false, err
	}
	alert.ID = existing.ID
	alert.Status = existing.Status
	alert.Version = existing.Version
	return false, nil
}

func (s *Store) getAlertByDedupeKey(ctx context.Context, key string) (domain.Alert, error) {
	return scanAlert(s.exec.QueryRowContext(ctx, `SELECT id, wave_id, participant_id, kind, severity, status, dedupe_key, opened_at, acknowledged_at, resolved_at, version FROM alerts WHERE dedupe_key = ?`, key))
}

func (s *Store) GetAlert(ctx context.Context, id int64) (domain.Alert, error) {
	return scanAlert(s.exec.QueryRowContext(ctx, `SELECT id, wave_id, participant_id, kind, severity, status, dedupe_key, opened_at, acknowledged_at, resolved_at, version FROM alerts WHERE id = ?`, id))
}

func scanAlert(row rowScanner) (domain.Alert, error) {
	var alert domain.Alert
	var participant sql.NullInt64
	var severity, status, opened string
	var acknowledged, resolved sql.NullString
	if err := row.Scan(&alert.ID, &alert.WaveID, &participant, &alert.Kind, &severity, &status, &alert.DedupeKey, &opened, &acknowledged, &resolved, &alert.Version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Alert{}, domain.ErrNotFound
		}
		return domain.Alert{}, fmt.Errorf("scan alert: %w", err)
	}
	if participant.Valid {
		alert.ParticipantID = &participant.Int64
	}
	alert.Severity = domain.RiskLevel(severity)
	alert.Status = domain.AlertStatus(status)
	var err error
	if alert.OpenedAt, err = parseTime(opened); err != nil {
		return domain.Alert{}, err
	}
	if alert.AcknowledgedAt, err = optionalTime(acknowledged); err != nil {
		return domain.Alert{}, err
	}
	alert.ResolvedAt, err = optionalTime(resolved)
	return alert, err
}

func (s *Store) ListOpenAlerts(ctx context.Context, waveID int64) ([]domain.Alert, error) {
	rows, err := s.exec.QueryContext(ctx, `SELECT id, wave_id, participant_id, kind, severity, status, dedupe_key, opened_at, acknowledged_at, resolved_at, version
		FROM alerts WHERE wave_id = ? AND status != 'resolved' ORDER BY opened_at, id`, waveID)
	if err != nil {
		return nil, fmt.Errorf("list open alerts: %w", err)
	}
	defer rows.Close()
	alerts := make([]domain.Alert, 0)
	for rows.Next() {
		alert, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, alert)
	}
	return alerts, rows.Err()
}

func (s *Store) UpdateAlertStatus(ctx context.Context, id, version int64, status domain.AlertStatus, at time.Time) error {
	acknowledged := any(nil)
	resolved := any(nil)
	if status == domain.AlertAcknowledged {
		acknowledged = formatTime(at)
	}
	if status == domain.AlertResolved {
		resolved = formatTime(at)
	}
	result, err := s.exec.ExecContext(ctx, `UPDATE alerts SET status = ?, acknowledged_at = COALESCE(?, acknowledged_at), resolved_at = COALESCE(?, resolved_at), version = version + 1 WHERE id = ? AND version = ?`,
		status, acknowledged, resolved, id, version)
	if err != nil {
		return fmt.Errorf("update alert: %w", err)
	}
	return requireVersion(result)
}
