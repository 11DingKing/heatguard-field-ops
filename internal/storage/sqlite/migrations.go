package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
)

type migration struct {
	version    int
	name       string
	statements []string
}

var migrations = []migration{
	{
		version: 1,
		name:    "identity_and_planning",
		statements: []string{
			`CREATE TABLE users (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				email TEXT NOT NULL COLLATE NOCASE UNIQUE,
				display_name TEXT NOT NULL,
				role TEXT NOT NULL CHECK (role IN ('organizer','coach','guardian','duty')),
				password_hash BLOB NOT NULL,
				active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			)`,
			`CREATE TABLE sessions (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				token_hash BLOB NOT NULL UNIQUE,
				expires_at TEXT NOT NULL,
				revoked_at TEXT,
				created_at TEXT NOT NULL,
				last_seen_at TEXT NOT NULL
			)`,
			`CREATE INDEX sessions_active_idx ON sessions(token_hash, expires_at) WHERE revoked_at IS NULL`,
			`CREATE TABLE routes (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL,
				zone TEXT NOT NULL,
				activity_kind TEXT NOT NULL CHECK (activity_kind IN ('run','cycling','park_conditioning')),
				distance_meters INTEGER NOT NULL CHECK (distance_meters > 0),
				active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
				version INTEGER NOT NULL DEFAULT 1,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				UNIQUE(zone, name)
			)`,
			`CREATE TABLE route_segments (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				route_id INTEGER NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
				sequence INTEGER NOT NULL CHECK (sequence > 0),
				name TEXT NOT NULL,
				latitude REAL NOT NULL CHECK (latitude BETWEEN -90 AND 90),
				longitude REAL NOT NULL CHECK (longitude BETWEEN -180 AND 180),
				hydration_site INTEGER NOT NULL DEFAULT 0 CHECK (hydration_site IN (0,1)),
				closed_from TEXT,
				closed_until TEXT,
				version INTEGER NOT NULL DEFAULT 1,
				UNIQUE(route_id, sequence),
				CHECK (closed_until IS NULL OR closed_from IS NOT NULL)
			)`,
			`CREATE INDEX route_segments_route_idx ON route_segments(route_id, sequence)`,
			`CREATE TABLE leaders (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				user_id INTEGER NOT NULL UNIQUE REFERENCES users(id),
				activity_kinds TEXT NOT NULL,
				qualified_until TEXT NOT NULL,
				emergency_trained INTEGER NOT NULL DEFAULT 0 CHECK (emergency_trained IN (0,1)),
				version INTEGER NOT NULL DEFAULT 1
			)`,
			`CREATE TABLE participants (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL,
				birth_date TEXT NOT NULL,
				guardian_user_id INTEGER REFERENCES users(id),
				emergency_name TEXT NOT NULL,
				emergency_phone TEXT NOT NULL,
				active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
				version INTEGER NOT NULL DEFAULT 1,
				created_at TEXT NOT NULL
			)`,
			`CREATE INDEX participants_guardian_idx ON participants(guardian_user_id)`,
			`CREATE TABLE health_restrictions (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				participant_id INTEGER NOT NULL REFERENCES participants(id) ON DELETE CASCADE,
				kind TEXT NOT NULL CHECK (kind IN ('no_extreme_heat','max_activity_minutes','guardian_presence','frequent_hydration')),
				value INTEGER NOT NULL DEFAULT 0,
				effective_from TEXT NOT NULL,
				effective_to TEXT,
				notes TEXT NOT NULL DEFAULT '',
				CHECK (effective_to IS NULL OR effective_to > effective_from)
			)`,
			`CREATE INDEX restrictions_active_idx ON health_restrictions(participant_id, effective_from, effective_to)`,
		},
	},
	{
		version: 2,
		name:    "operations_and_risk",
		statements: []string{
			`CREATE TABLE activity_waves (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				route_id INTEGER NOT NULL REFERENCES routes(id),
				leader_id INTEGER NOT NULL REFERENCES leaders(id),
				parent_wave_id INTEGER REFERENCES activity_waves(id),
				name TEXT NOT NULL,
				scheduled_start TEXT NOT NULL,
				expected_end TEXT NOT NULL,
				capacity INTEGER NOT NULL CHECK (capacity > 0),
				state TEXT NOT NULL CHECK (state IN ('draft','ready','active','withdrawing','split','closed','cancelled')),
				departure_risk TEXT NOT NULL DEFAULT 'low' CHECK (departure_risk IN ('low','guarded','high','extreme')),
				version INTEGER NOT NULL DEFAULT 1,
				created_by INTEGER NOT NULL REFERENCES users(id),
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				CHECK (expected_end > scheduled_start),
				UNIQUE(route_id, name, scheduled_start)
			)`,
			`CREATE INDEX waves_schedule_idx ON activity_waves(scheduled_start, state)`,
			`CREATE INDEX waves_leader_idx ON activity_waves(leader_id, scheduled_start)`,
			`CREATE TABLE wave_participants (
				wave_id INTEGER NOT NULL REFERENCES activity_waves(id) ON DELETE CASCADE,
				participant_id INTEGER NOT NULL REFERENCES participants(id),
				state TEXT NOT NULL CHECK (state IN ('enrolled','departed','checked_in','resting','missing','withdrawn','completed')),
				last_segment_id INTEGER REFERENCES route_segments(id),
				last_reported_at TEXT,
				disposition TEXT NOT NULL DEFAULT '',
				version INTEGER NOT NULL DEFAULT 1,
				enrolled_at TEXT NOT NULL,
				PRIMARY KEY(wave_id, participant_id)
			)`,
			`CREATE INDEX wave_participants_state_idx ON wave_participants(wave_id, state)`,
			`CREATE TABLE risk_rules (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				zone TEXT NOT NULL,
				activity_kind TEXT NOT NULL,
				level TEXT NOT NULL CHECK (level IN ('low','guarded','high','extreme')),
				temperature_min REAL NOT NULL,
				heat_index_min REAL NOT NULL,
				effective_from TEXT NOT NULL,
				effective_to TEXT NOT NULL,
				action TEXT NOT NULL,
				version INTEGER NOT NULL DEFAULT 1,
				created_by INTEGER NOT NULL REFERENCES users(id),
				CHECK (effective_to > effective_from)
			)`,
			`CREATE INDEX risk_rules_scope_idx ON risk_rules(zone, activity_kind, effective_from, effective_to)`,
			`CREATE TABLE field_events (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				wave_id INTEGER NOT NULL REFERENCES activity_waves(id),
				participant_id INTEGER REFERENCES participants(id),
				segment_id INTEGER REFERENCES route_segments(id),
				type TEXT NOT NULL,
				occurred_at TEXT NOT NULL,
				recorded_at TEXT NOT NULL,
				recorded_by INTEGER NOT NULL REFERENCES users(id),
				idempotency_key TEXT NOT NULL,
				details TEXT NOT NULL DEFAULT '',
				UNIQUE(wave_id, idempotency_key)
			)`,
			`CREATE INDEX field_events_timeline_idx ON field_events(wave_id, occurred_at, id)`,
			`CREATE TABLE alerts (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				wave_id INTEGER NOT NULL REFERENCES activity_waves(id),
				participant_id INTEGER REFERENCES participants(id),
				kind TEXT NOT NULL,
				severity TEXT NOT NULL CHECK (severity IN ('low','guarded','high','extreme')),
				status TEXT NOT NULL CHECK (status IN ('open','acknowledged','resolved')),
				dedupe_key TEXT NOT NULL UNIQUE,
				opened_at TEXT NOT NULL,
				acknowledged_at TEXT,
				resolved_at TEXT,
				version INTEGER NOT NULL DEFAULT 1
			)`,
			`CREATE INDEX alerts_open_idx ON alerts(wave_id, status, severity)`,
		},
	},
	{
		version: 3,
		name:    "delivery_jobs_and_audit",
		statements: []string{
			`CREATE TABLE notification_deliveries (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				alert_id INTEGER NOT NULL REFERENCES alerts(id),
				contact TEXT NOT NULL,
				channel TEXT NOT NULL,
				provider_key TEXT NOT NULL UNIQUE,
				status TEXT NOT NULL CHECK (status IN ('queued','sent','delivered','failed')),
				attempt_count INTEGER NOT NULL DEFAULT 1,
				last_receipt_at TEXT,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				UNIQUE(alert_id, contact, channel)
			)`,
			`CREATE INDEX notification_status_idx ON notification_deliveries(status, updated_at)`,
			`CREATE TABLE worker_jobs (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				kind TEXT NOT NULL,
				dedupe_key TEXT NOT NULL UNIQUE,
				payload TEXT NOT NULL,
				status TEXT NOT NULL CHECK (status IN ('pending','leased','succeeded','failed')),
				attempts INTEGER NOT NULL DEFAULT 0,
				max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
				available_at TEXT NOT NULL,
				lease_owner TEXT NOT NULL DEFAULT '',
				lease_until TEXT,
				last_error TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			)`,
			`CREATE INDEX worker_jobs_ready_idx ON worker_jobs(status, available_at, lease_until)`,
			`CREATE TABLE idempotency_records (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				scope TEXT NOT NULL,
				operation TEXT NOT NULL,
				key TEXT NOT NULL,
				result TEXT NOT NULL,
				created_at TEXT NOT NULL,
				UNIQUE(scope, operation, key)
			)`,
			`CREATE TABLE audit_events (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				actor_id INTEGER REFERENCES users(id),
				action TEXT NOT NULL,
				object_type TEXT NOT NULL,
				object_id TEXT NOT NULL,
				result TEXT NOT NULL,
				request_id TEXT NOT NULL,
				metadata TEXT NOT NULL DEFAULT '{}',
				created_at TEXT NOT NULL
			)`,
			`CREATE INDEX audit_object_idx ON audit_events(object_type, object_id, created_at DESC)`,
			`CREATE INDEX audit_request_idx ON audit_events(request_id)`,
		},
	},
}

func applyMigrations(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	for _, item := range migrations {
		if err := applyMigration(ctx, db, item); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, item migration) error {
	checksum := migrationChecksum(item)
	var existingName, existingChecksum string
	err := db.QueryRowContext(ctx, `SELECT name, checksum FROM schema_migrations WHERE version = ?`, item.version).Scan(&existingName, &existingChecksum)
	if err == nil {
		if existingName != item.name || existingChecksum != checksum {
			return fmt.Errorf("migration %d conflicts with applied history", item.version)
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("read migration %d: %w", item.version, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", item.version, err)
	}
	defer tx.Rollback()
	for index, statement := range item.statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migration %d statement %d: %w", item.version, index+1, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES(?,?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, item.version, item.name, checksum); err != nil {
		return fmt.Errorf("record migration %d: %w", item.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", item.version, err)
	}
	return nil
}

func migrationChecksum(item migration) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "%d\n%s\n", item.version, item.name)
	for _, statement := range item.statements {
		hash.Write([]byte(statement))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
