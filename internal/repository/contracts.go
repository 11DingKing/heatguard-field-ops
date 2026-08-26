package repository

import (
	"context"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
)

type Page struct {
	Limit  int
	Offset int
}

func (p Page) Normalize() Page {
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Limit > 200 {
		p.Limit = 200
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	return p
}

type WaveFilter struct {
	States    []domain.WaveState
	RouteID   int64
	LeaderID  int64
	StartFrom *time.Time
	StartTo   *time.Time
	Page      Page
}

type JobLease struct {
	Owner    string
	Now      time.Time
	Duration time.Duration
	Limit    int
}

type Reader interface {
	FindUserByEmail(context.Context, string) (domain.User, error)
	GetUser(context.Context, int64) (domain.User, error)
	GetSessionByHash(context.Context, []byte, time.Time) (domain.Session, domain.User, error)
	GetRoute(context.Context, int64) (domain.Route, error)
	GetRouteSegment(context.Context, int64) (domain.RouteSegment, error)
	ListRouteSegments(context.Context, int64) ([]domain.RouteSegment, error)
	GetLeader(context.Context, int64) (domain.Leader, error)
	GetParticipant(context.Context, int64) (domain.Participant, error)
	ListRestrictions(context.Context, int64, time.Time) ([]domain.HealthRestriction, error)
	GetWave(context.Context, int64) (domain.ActivityWave, error)
	ListWaveParticipants(context.Context, int64) ([]domain.WaveParticipant, error)
	GetWaveParticipant(context.Context, int64, int64) (domain.WaveParticipant, error)
	ListWaves(context.Context, WaveFilter) ([]domain.ActivityWave, int, error)
	ListRiskRules(context.Context, string, domain.ActivityKind, time.Time) ([]domain.RiskRule, error)
	GetFieldEventByKey(context.Context, int64, string) (domain.FieldEvent, error)
	ListOpenAlerts(context.Context, int64) ([]domain.Alert, error)
	GetAlert(context.Context, int64) (domain.Alert, error)
	ListAuditEvents(context.Context, string, string, Page) ([]domain.AuditEvent, error)
}

type Writer interface {
	InsertUser(context.Context, *domain.User) error
	InsertSession(context.Context, *domain.Session) error
	TouchSession(context.Context, int64, time.Time) error
	RevokeSession(context.Context, []byte, time.Time) (bool, error)
	RevokeExpiredSessions(context.Context, time.Time) (int64, error)
	InsertRoute(context.Context, *domain.Route, []domain.RouteSegment) error
	UpdateSegmentClosure(context.Context, int64, int64, *time.Time, *time.Time) error
	InsertLeader(context.Context, *domain.Leader) error
	InsertParticipant(context.Context, *domain.Participant) error
	InsertRestriction(context.Context, *domain.HealthRestriction) error
	InsertRiskRule(context.Context, *domain.RiskRule) error
	InsertWave(context.Context, *domain.ActivityWave) error
	InsertWaveParticipant(context.Context, *domain.WaveParticipant) error
	UpdateWaveState(context.Context, int64, int64, domain.WaveState, domain.RiskLevel, time.Time) error
	UpdateParticipantState(context.Context, int64, int64, int64, domain.ParticipantState, *int64, time.Time, string) error
	InsertFieldEvent(context.Context, *domain.FieldEvent) error
	InsertAlert(context.Context, *domain.Alert) (bool, error)
	UpdateAlertStatus(context.Context, int64, int64, domain.AlertStatus, time.Time) error
	InsertJob(context.Context, *domain.WorkerJob) (bool, error)
	LeaseJobs(context.Context, JobLease) ([]domain.WorkerJob, error)
	CompleteJob(context.Context, int64, string, time.Time) error
	FailJob(context.Context, int64, string, time.Time, time.Time, error) error
	InsertNotificationAttempt(context.Context, int64, string, string, string, time.Time) (int64, bool, error)
	MergeNotificationReceipt(context.Context, string, string, time.Time) error
	InsertIdempotencyResult(context.Context, string, string, string, string, time.Time) (bool, error)
	GetIdempotencyResult(context.Context, string, string, string) (string, bool, error)
	InsertAudit(context.Context, *domain.AuditEvent) error
}

type Tx interface {
	Reader
	Writer
}

type Store interface {
	Tx
	WithinTx(context.Context, func(Tx) error) error
	Ping(context.Context) error
	Close() error
}
