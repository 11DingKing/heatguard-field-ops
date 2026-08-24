package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/repository"
	_ "modernc.org/sqlite"
)

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Store struct {
	db   *sql.DB
	exec sqlExecutor
}

func Open(ctx context.Context, path string) (*Store, error) {
	dsn, err := dataSource(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := applyMigrations(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, exec: db}, nil
}

func dataSource(path string) (string, error) {
	if path == ":memory:" {
		return "file:heatguard-memory?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("database path: %w", err)
	}
	value := url.URL{Scheme: "file", Path: absolute}
	query := value.Query()
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "busy_timeout(5000)")
	value.RawQuery = query.Encode()
	return value.String(), nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("database unavailable: %w", err)
	}
	var value int
	if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(&value); err != nil || value != 1 {
		return fmt.Errorf("database readiness check: %w", err)
	}
	return nil
}

func (s *Store) WithinTx(ctx context.Context, fn func(repository.Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	child := &Store{db: s.db, exec: tx}
	if err := fn(child); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return fmt.Errorf("transaction failed: %v; rollback: %w", err, rollbackErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "constraint failed")
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse persisted time %q: %w", value, err)
	}
	return parsed, nil
}

func optionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

var _ repository.Store = (*Store)(nil)
