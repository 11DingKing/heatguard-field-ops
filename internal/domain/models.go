package domain

import "time"

type Role string

const (
	RoleOrganizer Role = "organizer"
	RoleCoach     Role = "coach"
	RoleGuardian  Role = "guardian"
	RoleDuty      Role = "duty"
)

func (r Role) Valid() bool {
	switch r {
	case RoleOrganizer, RoleCoach, RoleGuardian, RoleDuty:
		return true
	default:
		return false
	}
}

type ActivityKind string

const (
	ActivityRun     ActivityKind = "run"
	ActivityCycling ActivityKind = "cycling"
	ActivityParkFit ActivityKind = "park_conditioning"
)

func (k ActivityKind) Valid() bool {
	return k == ActivityRun || k == ActivityCycling || k == ActivityParkFit
}

type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	DisplayName  string    `json:"display_name"`
	Role         Role      `json:"role"`
	PasswordHash []byte    `json:"-"`
	Active       bool      `json:"active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Session struct {
	ID        int64      `json:"id"`
	UserID    int64      `json:"user_id"`
	TokenHash []byte     `json:"-"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	LastSeen  time.Time  `json:"last_seen_at"`
}

type GeoPoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

func (p GeoPoint) Valid() bool {
	return p.Latitude >= -90 && p.Latitude <= 90 && p.Longitude >= -180 && p.Longitude <= 180
}

type Route struct {
	ID             int64        `json:"id"`
	Name           string       `json:"name"`
	Zone           string       `json:"zone"`
	ActivityKind   ActivityKind `json:"activity_kind"`
	DistanceMeters int          `json:"distance_meters"`
	Active         bool         `json:"active"`
	Version        int64        `json:"version"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

type RouteSegment struct {
	ID            int64      `json:"id"`
	RouteID       int64      `json:"route_id"`
	Sequence      int        `json:"sequence"`
	Name          string     `json:"name"`
	Checkpoint    GeoPoint   `json:"checkpoint"`
	HydrationSite bool       `json:"hydration_site"`
	ClosedFrom    *time.Time `json:"closed_from,omitempty"`
	ClosedUntil   *time.Time `json:"closed_until,omitempty"`
	Version       int64      `json:"version"`
}

func (s RouteSegment) ClosedAt(at time.Time) bool {
	if s.ClosedFrom == nil {
		return false
	}
	if at.Before(*s.ClosedFrom) {
		return false
	}
	return s.ClosedUntil == nil || at.Before(*s.ClosedUntil)
}

type Leader struct {
	ID               int64          `json:"id"`
	UserID           int64          `json:"user_id"`
	Kinds            []ActivityKind `json:"activity_kinds"`
	QualifiedUntil   time.Time      `json:"qualified_until"`
	EmergencyTrained bool           `json:"emergency_trained"`
	Version          int64          `json:"version"`
}

func (l Leader) QualifiedFor(kind ActivityKind, at time.Time) bool {
	if !l.QualifiedUntil.After(at) {
		return false
	}
	for _, allowed := range l.Kinds {
		if allowed == kind {
			return true
		}
	}
	return false
}

type Participant struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	BirthDate      time.Time `json:"birth_date"`
	GuardianUserID *int64    `json:"guardian_user_id,omitempty"`
	EmergencyName  string    `json:"emergency_name"`
	EmergencyPhone string    `json:"emergency_phone"`
	Active         bool      `json:"active"`
	Version        int64     `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
}

func (p Participant) AgeAt(at time.Time) int {
	years := at.Year() - p.BirthDate.Year()
	birthday := time.Date(at.Year(), p.BirthDate.Month(), p.BirthDate.Day(), 0, 0, 0, 0, at.Location())
	if at.Before(birthday) {
		years--
	}
	return years
}

type RestrictionKind string

const (
	RestrictionNoExtremeHeat RestrictionKind = "no_extreme_heat"
	RestrictionMaxMinutes    RestrictionKind = "max_activity_minutes"
	RestrictionNeedsGuardian RestrictionKind = "guardian_presence"
	RestrictionNeedsWater    RestrictionKind = "frequent_hydration"
)

type HealthRestriction struct {
	ID            int64           `json:"id"`
	ParticipantID int64           `json:"participant_id"`
	Kind          RestrictionKind `json:"kind"`
	Value         int             `json:"value"`
	EffectiveFrom time.Time       `json:"effective_from"`
	EffectiveTo   *time.Time      `json:"effective_to,omitempty"`
	Notes         string          `json:"notes"`
}

func (r HealthRestriction) ActiveAt(at time.Time) bool {
	return !at.Before(r.EffectiveFrom) && (r.EffectiveTo == nil || at.Before(*r.EffectiveTo))
}

type WaveState string

const (
	WaveDraft       WaveState = "draft"
	WaveReady       WaveState = "ready"
	WaveActive      WaveState = "active"
	WaveWithdrawing WaveState = "withdrawing"
	WaveSplit       WaveState = "split"
	WaveClosed      WaveState = "closed"
	WaveCancelled   WaveState = "cancelled"
)

type ActivityWave struct {
	ID             int64     `json:"id"`
	RouteID        int64     `json:"route_id"`
	LeaderID       int64     `json:"leader_id"`
	ParentWaveID   *int64    `json:"parent_wave_id,omitempty"`
	Name           string    `json:"name"`
	ScheduledStart time.Time `json:"scheduled_start"`
	ExpectedEnd    time.Time `json:"expected_end"`
	Capacity       int       `json:"capacity"`
	State          WaveState `json:"state"`
	DepartureRisk  RiskLevel `json:"departure_risk"`
	Version        int64     `json:"version"`
	CreatedBy      int64     `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ParticipantState string

const (
	ParticipantEnrolled  ParticipantState = "enrolled"
	ParticipantDeparted  ParticipantState = "departed"
	ParticipantCheckedIn ParticipantState = "checked_in"
	ParticipantResting   ParticipantState = "resting"
	ParticipantMissing   ParticipantState = "missing"
	ParticipantWithdrawn ParticipantState = "withdrawn"
	ParticipantCompleted ParticipantState = "completed"
)

func (s ParticipantState) Terminal() bool {
	return s == ParticipantWithdrawn || s == ParticipantCompleted
}

type WaveParticipant struct {
	WaveID         int64            `json:"wave_id"`
	ParticipantID  int64            `json:"participant_id"`
	State          ParticipantState `json:"state"`
	LastSegmentID  *int64           `json:"last_segment_id,omitempty"`
	LastReportedAt *time.Time       `json:"last_reported_at,omitempty"`
	Disposition    string           `json:"disposition"`
	Version        int64            `json:"version"`
	EnrolledAt     time.Time        `json:"enrolled_at"`
}

type RiskLevel string

const (
	RiskLow     RiskLevel = "low"
	RiskGuarded RiskLevel = "guarded"
	RiskHigh    RiskLevel = "high"
	RiskExtreme RiskLevel = "extreme"
)

func (r RiskLevel) Rank() int {
	switch r {
	case RiskLow:
		return 0
	case RiskGuarded:
		return 1
	case RiskHigh:
		return 2
	case RiskExtreme:
		return 3
	default:
		return -1
	}
}

type RiskRule struct {
	ID             int64        `json:"id"`
	Zone           string       `json:"zone"`
	ActivityKind   ActivityKind `json:"activity_kind"`
	Level          RiskLevel    `json:"level"`
	TemperatureMin float64      `json:"temperature_min"`
	HeatIndexMin   float64      `json:"heat_index_min"`
	EffectiveFrom  time.Time    `json:"effective_from"`
	EffectiveTo    time.Time    `json:"effective_to"`
	Action         string       `json:"action"`
	Version        int64        `json:"version"`
	CreatedBy      int64        `json:"created_by"`
}

func (r RiskRule) Applies(zone string, kind ActivityKind, at time.Time, temperature, heatIndex float64) bool {
	return r.Zone == zone && r.ActivityKind == kind && !at.Before(r.EffectiveFrom) && at.Before(r.EffectiveTo) && temperature >= r.TemperatureMin && heatIndex >= r.HeatIndexMin
}

type FieldEventType string

const (
	EventCheckpoint     FieldEventType = "checkpoint"
	EventHydrationStart FieldEventType = "hydration_start"
	EventHydrationEnd   FieldEventType = "hydration_end"
	EventMissing        FieldEventType = "missing"
	EventFound          FieldEventType = "found"
	EventWithdraw       FieldEventType = "withdraw"
	EventComplete       FieldEventType = "complete"
	EventClosure        FieldEventType = "segment_closure"
)

type FieldEvent struct {
	ID             int64          `json:"id"`
	WaveID         int64          `json:"wave_id"`
	ParticipantID  *int64         `json:"participant_id,omitempty"`
	SegmentID      *int64         `json:"segment_id,omitempty"`
	Type           FieldEventType `json:"type"`
	OccurredAt     time.Time      `json:"occurred_at"`
	RecordedAt     time.Time      `json:"recorded_at"`
	RecordedBy     int64          `json:"recorded_by"`
	IdempotencyKey string         `json:"idempotency_key"`
	Details        string         `json:"details"`
}

type AlertStatus string

const (
	AlertOpen         AlertStatus = "open"
	AlertAcknowledged AlertStatus = "acknowledged"
	AlertResolved     AlertStatus = "resolved"
)

type Alert struct {
	ID             int64       `json:"id"`
	WaveID         int64       `json:"wave_id"`
	ParticipantID  *int64      `json:"participant_id,omitempty"`
	Kind           string      `json:"kind"`
	Severity       RiskLevel   `json:"severity"`
	Status         AlertStatus `json:"status"`
	DedupeKey      string      `json:"dedupe_key"`
	OpenedAt       time.Time   `json:"opened_at"`
	AcknowledgedAt *time.Time  `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time  `json:"resolved_at,omitempty"`
	Version        int64       `json:"version"`
}

type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobLeased    JobStatus = "leased"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
)

type WorkerJob struct {
	ID          int64      `json:"id"`
	Kind        string     `json:"kind"`
	DedupeKey   string     `json:"dedupe_key"`
	Payload     string     `json:"payload"`
	Status      JobStatus  `json:"status"`
	Attempts    int        `json:"attempts"`
	MaxAttempts int        `json:"max_attempts"`
	AvailableAt time.Time  `json:"available_at"`
	LeaseOwner  string     `json:"lease_owner,omitempty"`
	LeaseUntil  *time.Time `json:"lease_until,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type AuditEvent struct {
	ID         int64     `json:"id"`
	ActorID    *int64    `json:"actor_id,omitempty"`
	Action     string    `json:"action"`
	ObjectType string    `json:"object_type"`
	ObjectID   string    `json:"object_id"`
	Result     string    `json:"result"`
	RequestID  string    `json:"request_id"`
	Metadata   string    `json:"metadata"`
	CreatedAt  time.Time `json:"created_at"`
}
