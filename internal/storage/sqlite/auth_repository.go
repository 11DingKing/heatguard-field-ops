package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
)

func (s *Store) InsertUser(ctx context.Context, user *domain.User) error {
	result, err := s.exec.ExecContext(ctx, `INSERT INTO users(email, display_name, role, password_hash, active, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?)`, user.Email, user.DisplayName, user.Role, user.PasswordHash, user.Active, formatTime(user.CreatedAt), formatTime(user.UpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Wrap("insert", "user", 0, domain.ErrConflict)
		}
		return domain.Wrap("insert", "user", 0, err)
	}
	user.ID, err = result.LastInsertId()
	return domain.Wrap("read id", "user", 0, err)
}

func (s *Store) FindUserByEmail(ctx context.Context, email string) (domain.User, error) {
	row := s.exec.QueryRowContext(ctx, `SELECT id, email, display_name, role, password_hash, active, created_at, updated_at FROM users WHERE email = ?`, email)
	return scanUser(row)
}

func (s *Store) GetUser(ctx context.Context, id int64) (domain.User, error) {
	row := s.exec.QueryRowContext(ctx, `SELECT id, email, display_name, role, password_hash, active, created_at, updated_at FROM users WHERE id = ?`, id)
	return scanUser(row)
}

type rowScanner interface {
	Scan(...any) error
}

func scanUser(row rowScanner) (domain.User, error) {
	var user domain.User
	var role, created, updated string
	if err := row.Scan(&user.ID, &user.Email, &user.DisplayName, &role, &user.PasswordHash, &user.Active, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.User{}, domain.ErrNotFound
		}
		return domain.User{}, fmt.Errorf("scan user: %w", err)
	}
	user.Role = domain.Role(role)
	var err error
	if user.CreatedAt, err = parseTime(created); err != nil {
		return domain.User{}, err
	}
	if user.UpdatedAt, err = parseTime(updated); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (s *Store) InsertSession(ctx context.Context, session *domain.Session) error {
	result, err := s.exec.ExecContext(ctx, `INSERT INTO sessions(user_id, token_hash, expires_at, revoked_at, created_at, last_seen_at)
		VALUES(?,?,?,?,?,?)`, session.UserID, session.TokenHash, formatTime(session.ExpiresAt), nil, formatTime(session.CreatedAt), formatTime(session.LastSeen))
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrConflict
		}
		return fmt.Errorf("insert session: %w", err)
	}
	session.ID, err = result.LastInsertId()
	return err
}

func (s *Store) GetSessionByHash(ctx context.Context, tokenHash []byte, now time.Time) (domain.Session, domain.User, error) {
	row := s.exec.QueryRowContext(ctx, `SELECT s.id, s.user_id, s.token_hash, s.expires_at, s.revoked_at, s.created_at, s.last_seen_at,
		u.id, u.email, u.display_name, u.role, u.password_hash, u.active, u.created_at, u.updated_at
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ?`, tokenHash)
	var session domain.Session
	var user domain.User
	var expires, revoked, sessionCreated, lastSeen sql.NullString
	var role, userCreated, userUpdated string
	if err := row.Scan(&session.ID, &session.UserID, &session.TokenHash, &expires, &revoked, &sessionCreated, &lastSeen,
		&user.ID, &user.Email, &user.DisplayName, &role, &user.PasswordHash, &user.Active, &userCreated, &userUpdated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Session{}, domain.User{}, domain.ErrUnauthorized
		}
		return domain.Session{}, domain.User{}, fmt.Errorf("scan session: %w", err)
	}
	var err error
	if session.ExpiresAt, err = parseTime(expires.String); err != nil {
		return domain.Session{}, domain.User{}, err
	}
	if session.RevokedAt, err = optionalTime(revoked); err != nil {
		return domain.Session{}, domain.User{}, err
	}
	if session.CreatedAt, err = parseTime(sessionCreated.String); err != nil {
		return domain.Session{}, domain.User{}, err
	}
	if session.LastSeen, err = parseTime(lastSeen.String); err != nil {
		return domain.Session{}, domain.User{}, err
	}
	user.Role = domain.Role(role)
	if user.CreatedAt, err = parseTime(userCreated); err != nil {
		return domain.Session{}, domain.User{}, err
	}
	if user.UpdatedAt, err = parseTime(userUpdated); err != nil {
		return domain.Session{}, domain.User{}, err
	}
	if session.RevokedAt != nil || !session.ExpiresAt.After(now) || !user.Active {
		return domain.Session{}, domain.User{}, domain.ErrUnauthorized
	}
	return session, user, nil
}

func (s *Store) TouchSession(ctx context.Context, id int64, at time.Time) error {
	result, err := s.exec.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id = ? AND revoked_at IS NULL AND expires_at > ?`, formatTime(at), id, formatTime(at))
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return domain.ErrUnauthorized
	}
	return nil
}

func (s *Store) RevokeSession(ctx context.Context, tokenHash []byte, at time.Time) (bool, error) {
	result, err := s.exec.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`, formatTime(at), tokenHash)
	if err != nil {
		return false, fmt.Errorf("revoke session: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *Store) RevokeExpiredSessions(ctx context.Context, at time.Time) (int64, error) {
	result, err := s.exec.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE revoked_at IS NULL AND expires_at <= ?`, formatTime(at), formatTime(at))
	if err != nil {
		return 0, fmt.Errorf("revoke expired sessions: %w", err)
	}
	return result.RowsAffected()
}
