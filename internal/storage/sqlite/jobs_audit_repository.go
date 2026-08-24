package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
)

func (s *Store) InsertJob(ctx context.Context, job *domain.WorkerJob) (bool, error) {
	result, err := s.exec.ExecContext(ctx, `INSERT INTO worker_jobs(kind, dedupe_key, payload, status, attempts, max_attempts, available_at, lease_owner, lease_until, last_error, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(dedupe_key) DO NOTHING`, job.Kind, job.DedupeKey, job.Payload, job.Status, job.Attempts,
		job.MaxAttempts, formatTime(job.AvailableAt), job.LeaseOwner, timeArg(job.LeaseUntil), job.LastError, formatTime(job.CreatedAt), formatTime(job.UpdatedAt))
	if err != nil {
		return false, fmt.Errorf("insert worker job: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count == 1 {
		job.ID, err = result.LastInsertId()
		return true, err
	}
	return false, nil
}

func (s *Store) LeaseJobs(ctx context.Context, lease repository.JobLease) ([]domain.WorkerJob, error) {
	if lease.Limit <= 0 || lease.Limit > 100 {
		lease.Limit = 10
	}
	leaseUntil := lease.Now.Add(lease.Duration)
	rows, err := s.exec.QueryContext(ctx, `UPDATE worker_jobs SET status = 'leased', lease_owner = ?, lease_until = ?, attempts = attempts + 1, updated_at = ?
		WHERE id IN (SELECT id FROM worker_jobs WHERE available_at <= ? AND attempts < max_attempts AND
		(status = 'pending' OR (status = 'leased' AND lease_until <= ?)) ORDER BY available_at, id LIMIT ?)
		RETURNING id, kind, dedupe_key, payload, status, attempts, max_attempts, available_at, lease_owner, lease_until, last_error, created_at, updated_at`,
		lease.Owner, formatTime(leaseUntil), formatTime(lease.Now), formatTime(lease.Now), formatTime(lease.Now), lease.Limit)
	if err != nil {
		return nil, fmt.Errorf("lease jobs: %w", err)
	}
	defer rows.Close()
	jobs := make([]domain.WorkerJob, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func scanJob(row rowScanner) (domain.WorkerJob, error) {
	var job domain.WorkerJob
	var status, available, created, updated string
	var leaseUntil sql.NullString
	if err := row.Scan(&job.ID, &job.Kind, &job.DedupeKey, &job.Payload, &status, &job.Attempts, &job.MaxAttempts, &available,
		&job.LeaseOwner, &leaseUntil, &job.LastError, &created, &updated); err != nil {
		return domain.WorkerJob{}, fmt.Errorf("scan job: %w", err)
	}
	job.Status = domain.JobStatus(status)
	var err error
	if job.AvailableAt, err = parseTime(available); err != nil {
		return domain.WorkerJob{}, err
	}
	if job.LeaseUntil, err = optionalTime(leaseUntil); err != nil {
		return domain.WorkerJob{}, err
	}
	if job.CreatedAt, err = parseTime(created); err != nil {
		return domain.WorkerJob{}, err
	}
	job.UpdatedAt, err = parseTime(updated)
	return job, err
}

func (s *Store) CompleteJob(ctx context.Context, id int64, owner string, at time.Time) error {
	result, err := s.exec.ExecContext(ctx, `UPDATE worker_jobs SET status = 'succeeded', lease_owner = '', lease_until = NULL, updated_at = ? WHERE id = ? AND status = 'leased' AND lease_owner = ?`, formatTime(at), id, owner)
	if err != nil {
		return fmt.Errorf("complete job: %w", err)
	}
	return requireVersion(result)
}

func (s *Store) FailJob(ctx context.Context, id int64, owner string, at, retryAt time.Time, cause error) error {
	message := cause.Error()
	result, err := s.exec.ExecContext(ctx, `UPDATE worker_jobs SET
		status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'pending' END,
		available_at = CASE WHEN attempts >= max_attempts THEN available_at ELSE ? END,
		lease_owner = '', lease_until = NULL, last_error = ?, updated_at = ?
		WHERE id = ? AND status = 'leased' AND lease_owner = ?`, formatTime(retryAt), message, formatTime(at), id, owner)
	if err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	return requireVersion(result)
}

func (s *Store) InsertNotificationAttempt(ctx context.Context, alertID int64, contact, channel, providerKey string, at time.Time) (int64, bool, error) {
	result, err := s.exec.ExecContext(ctx, `INSERT INTO notification_deliveries(alert_id, contact, channel, provider_key, status, attempt_count, created_at, updated_at)
		VALUES(?,?,?,?, 'queued', 1, ?, ?) ON CONFLICT(alert_id, contact, channel) DO UPDATE SET attempt_count = attempt_count + 1, updated_at = excluded.updated_at`,
		alertID, contact, channel, providerKey, formatTime(at), formatTime(at))
	if err != nil {
		if isUniqueViolation(err) {
			return 0, false, domain.ErrConflict
		}
		return 0, false, fmt.Errorf("insert notification attempt: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	var attempts int
	if err := s.exec.QueryRowContext(ctx, `SELECT id, attempt_count FROM notification_deliveries WHERE alert_id = ? AND contact = ? AND channel = ?`, alertID, contact, channel).Scan(&id, &attempts); err != nil {
		return 0, false, err
	}
	return id, attempts == 1, nil
}

func (s *Store) MergeNotificationReceipt(ctx context.Context, providerKey, status string, at time.Time) error {
	result, err := s.exec.ExecContext(ctx, `UPDATE notification_deliveries SET
		status = ?,
		last_receipt_at = ?,
		updated_at = ? WHERE provider_key = ?`, status, formatTime(at), formatTime(at), providerKey)
	if err != nil {
		return fmt.Errorf("merge notification receipt: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) InsertIdempotencyResult(ctx context.Context, scope, operation, key, value string, at time.Time) (bool, error) {
	result, err := s.exec.ExecContext(ctx, `INSERT INTO idempotency_records(scope, operation, key, result, created_at) VALUES(?,?,?,?,?) ON CONFLICT(scope, operation, key) DO NOTHING`,
		scope, operation, key, value, formatTime(at))
	if err != nil {
		return false, fmt.Errorf("insert idempotency result: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *Store) GetIdempotencyResult(ctx context.Context, scope, operation, key string) (string, bool, error) {
	var value string
	err := s.exec.QueryRowContext(ctx, `SELECT result FROM idempotency_records WHERE scope = ? AND operation = ? AND key = ?`, scope, operation, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get idempotency result: %w", err)
	}
	return value, true, nil
}

func (s *Store) InsertAudit(ctx context.Context, event *domain.AuditEvent) error {
	result, err := s.exec.ExecContext(ctx, `INSERT INTO audit_events(actor_id, action, object_type, object_id, result, request_id, metadata, created_at) VALUES(?,?,?,?,?,?,?,?)`,
		event.ActorID, event.Action, event.ObjectType, event.ObjectID, event.Result, event.RequestID, event.Metadata, formatTime(event.CreatedAt))
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	event.ID, err = result.LastInsertId()
	return err
}

func (s *Store) ListAuditEvents(ctx context.Context, objectType, objectID string, page repository.Page) ([]domain.AuditEvent, error) {
	page = page.Normalize()
	rows, err := s.exec.QueryContext(ctx, `SELECT id, actor_id, action, object_type, object_id, result, request_id, metadata, created_at FROM audit_events
		WHERE object_type = ? AND object_id = ? ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, objectType, objectID, page.Limit, page.Offset)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	result := make([]domain.AuditEvent, 0)
	for rows.Next() {
		var event domain.AuditEvent
		var actor sql.NullInt64
		var created string
		if err := rows.Scan(&event.ID, &actor, &event.Action, &event.ObjectType, &event.ObjectID, &event.Result, &event.RequestID, &event.Metadata, &created); err != nil {
			return nil, err
		}
		if actor.Valid {
			event.ActorID = &actor.Int64
		}
		if event.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}
