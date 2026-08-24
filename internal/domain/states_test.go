package domain

import (
	"errors"
	"testing"
	"time"
)

func TestWaveTransitions(t *testing.T) {
	tests := []struct {
		name string
		from WaveState
		to   WaveState
		ok   bool
	}{
		{"draft ready", WaveDraft, WaveReady, true},
		{"draft cancel", WaveDraft, WaveCancelled, true},
		{"ready active", WaveReady, WaveActive, true},
		{"ready cancel", WaveReady, WaveCancelled, true},
		{"active withdrawing", WaveActive, WaveWithdrawing, true},
		{"active split", WaveActive, WaveSplit, true},
		{"active close", WaveActive, WaveClosed, true},
		{"withdrawing split", WaveWithdrawing, WaveSplit, true},
		{"withdrawing close", WaveWithdrawing, WaveClosed, true},
		{"split withdrawing", WaveSplit, WaveWithdrawing, true},
		{"split close", WaveSplit, WaveClosed, true},
		{"same state", WaveActive, WaveActive, true},
		{"draft active", WaveDraft, WaveActive, false},
		{"ready closed", WaveReady, WaveClosed, false},
		{"closed active", WaveClosed, WaveActive, false},
		{"cancelled ready", WaveCancelled, WaveReady, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateWaveTransition(test.from, test.to)
			if test.ok && err != nil {
				t.Fatalf("expected transition to pass: %v", err)
			}
			if !test.ok && !errors.Is(err, ErrInvalidState) {
				t.Fatalf("expected invalid state, got %v", err)
			}
		})
	}
}

func TestParticipantTransitions(t *testing.T) {
	tests := []struct {
		from ParticipantState
		to   ParticipantState
		ok   bool
	}{
		{ParticipantEnrolled, ParticipantDeparted, true},
		{ParticipantEnrolled, ParticipantWithdrawn, true},
		{ParticipantDeparted, ParticipantCheckedIn, true},
		{ParticipantDeparted, ParticipantResting, true},
		{ParticipantDeparted, ParticipantMissing, true},
		{ParticipantDeparted, ParticipantCompleted, true},
		{ParticipantCheckedIn, ParticipantResting, true},
		{ParticipantResting, ParticipantCheckedIn, true},
		{ParticipantMissing, ParticipantCheckedIn, true},
		{ParticipantMissing, ParticipantWithdrawn, true},
		{ParticipantCompleted, ParticipantCheckedIn, false},
		{ParticipantWithdrawn, ParticipantDeparted, false},
		{ParticipantEnrolled, ParticipantCompleted, false},
		{ParticipantMissing, ParticipantCompleted, false},
	}
	for _, test := range tests {
		name := string(test.from) + "_to_" + string(test.to)
		t.Run(name, func(t *testing.T) {
			err := ValidateParticipantTransition(test.from, test.to)
			if test.ok && err != nil {
				t.Fatalf("expected transition to pass: %v", err)
			}
			if !test.ok && !errors.Is(err, ErrInvalidState) {
				t.Fatalf("expected invalid state, got %v", err)
			}
		})
	}
}

func TestStateForEvent(t *testing.T) {
	tests := []struct {
		name  string
		state ParticipantState
		event FieldEventType
		want  ParticipantState
	}{
		{"checkpoint", ParticipantDeparted, EventCheckpoint, ParticipantCheckedIn},
		{"hydration begins", ParticipantCheckedIn, EventHydrationStart, ParticipantResting},
		{"hydration ends", ParticipantResting, EventHydrationEnd, ParticipantCheckedIn},
		{"participant missing", ParticipantCheckedIn, EventMissing, ParticipantMissing},
		{"participant found", ParticipantMissing, EventFound, ParticipantCheckedIn},
		{"participant withdraws", ParticipantResting, EventWithdraw, ParticipantWithdrawn},
		{"participant completes", ParticipantCheckedIn, EventComplete, ParticipantCompleted},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := StateForEvent(test.state, test.event)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Fatalf("got %s, want %s", got, test.want)
			}
		})
	}
}

func TestStateForEventRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		state ParticipantState
		event FieldEventType
		want  error
	}{
		{"closure is wave scoped", ParticipantDeparted, EventClosure, ErrValidation},
		{"completed cannot check in", ParticipantCompleted, EventCheckpoint, ErrInvalidState},
		{"withdrawn cannot rest", ParticipantWithdrawn, EventHydrationStart, ErrInvalidState},
		{"enrolled cannot complete", ParticipantEnrolled, EventComplete, ErrInvalidState},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := StateForEvent(test.state, test.event)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestRiskRuleAppliesOnlyInsideScope(t *testing.T) {
	start := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	rule := RiskRule{
		Zone:           "north-park",
		ActivityKind:   ActivityRun,
		TemperatureMin: 34,
		HeatIndexMin:   40,
		EffectiveFrom:  start,
		EffectiveTo:    start.Add(2 * time.Hour),
	}
	tests := []struct {
		name        string
		zone        string
		kind        ActivityKind
		at          time.Time
		temperature float64
		heatIndex   float64
		want        bool
	}{
		{"matching observation", "north-park", ActivityRun, start.Add(time.Hour), 35, 42, true},
		{"before window", "north-park", ActivityRun, start.Add(-time.Second), 35, 42, false},
		{"at exclusive end", "north-park", ActivityRun, start.Add(2 * time.Hour), 35, 42, false},
		{"different zone", "south-park", ActivityRun, start.Add(time.Hour), 35, 42, false},
		{"different activity", "north-park", ActivityCycling, start.Add(time.Hour), 35, 42, false},
		{"temperature below minimum", "north-park", ActivityRun, start.Add(time.Hour), 33.9, 42, false},
		{"heat index below minimum", "north-park", ActivityRun, start.Add(time.Hour), 35, 39.9, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := rule.Applies(test.zone, test.kind, test.at, test.temperature, test.heatIndex)
			if got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestRouteSegmentClosureWindow(t *testing.T) {
	start := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	segment := RouteSegment{ClosedFrom: &start, ClosedUntil: &end}
	if segment.ClosedAt(start.Add(-time.Nanosecond)) {
		t.Fatal("segment closed before window")
	}
	if !segment.ClosedAt(start) {
		t.Fatal("segment should close at inclusive start")
	}
	if !segment.ClosedAt(end.Add(-time.Nanosecond)) {
		t.Fatal("segment should remain closed before end")
	}
	if segment.ClosedAt(end) {
		t.Fatal("segment should reopen at exclusive end")
	}
	segment.ClosedUntil = nil
	if !segment.ClosedAt(end.Add(24 * time.Hour)) {
		t.Fatal("open-ended closure should remain active")
	}
}

func TestParticipantAgeAt(t *testing.T) {
	participant := Participant{BirthDate: time.Date(2010, 8, 25, 0, 0, 0, 0, time.UTC)}
	before := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	on := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	if got := participant.AgeAt(before); got != 15 {
		t.Fatalf("before birthday got %d", got)
	}
	if got := participant.AgeAt(on); got != 16 {
		t.Fatalf("on birthday got %d", got)
	}
}

func TestHealthRestrictionWindow(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	restriction := HealthRestriction{EffectiveFrom: start, EffectiveTo: &end}
	if restriction.ActiveAt(start.Add(-time.Nanosecond)) {
		t.Fatal("restriction active before start")
	}
	if !restriction.ActiveAt(start) {
		t.Fatal("restriction not active at start")
	}
	if restriction.ActiveAt(end) {
		t.Fatal("restriction active at exclusive end")
	}
	restriction.EffectiveTo = nil
	if !restriction.ActiveAt(end.AddDate(1, 0, 0)) {
		t.Fatal("open restriction should remain active")
	}
}

func TestPublicErrorCodesPreserveWrappedErrors(t *testing.T) {
	tests := []struct {
		err  error
		code string
	}{
		{Validation("name", "required"), "validation_failed"},
		{Wrap("read", "wave", 42, ErrNotFound), "not_found"},
		{Wrap("update", "wave", 42, ErrVersionConflict), "version_conflict"},
		{ErrUnauthorized, "unauthorized"},
		{ErrForbidden, "forbidden"},
		{ErrCapacity, "capacity_exceeded"},
		{ErrRiskBlocked, "risk_blocked"},
		{ErrPendingSafety, "pending_safety_event"},
		{ErrUnavailable, "unavailable"},
		{errors.New("unknown"), "internal_error"},
	}
	for _, test := range tests {
		if got := PublicCode(test.err); got != test.code {
			t.Errorf("PublicCode(%v) = %q, want %q", test.err, got, test.code)
		}
	}
}

func TestRoleAndActivityKindValidation(t *testing.T) {
	for _, role := range []Role{RoleOrganizer, RoleCoach, RoleGuardian, RoleDuty} {
		if !role.Valid() {
			t.Errorf("role %q should be valid", role)
		}
	}
	if Role("superuser").Valid() {
		t.Fatal("unknown role should not be valid")
	}
	for _, kind := range []ActivityKind{ActivityRun, ActivityCycling, ActivityParkFit} {
		if !kind.Valid() {
			t.Errorf("kind %q should be valid", kind)
		}
	}
	if ActivityKind("swimming").Valid() {
		t.Fatal("unknown activity kind should not be valid")
	}
}

func TestRiskRanksAreOrdered(t *testing.T) {
	levels := []RiskLevel{RiskLow, RiskGuarded, RiskHigh, RiskExtreme}
	for index, level := range levels {
		if got := level.Rank(); got != index {
			t.Fatalf("rank %s = %d, want %d", level, got, index)
		}
	}
	if got := RiskLevel("unknown").Rank(); got != -1 {
		t.Fatalf("unknown rank = %d", got)
	}
}

func TestTerminalParticipantStates(t *testing.T) {
	for _, state := range []ParticipantState{ParticipantWithdrawn, ParticipantCompleted} {
		if !state.Terminal() {
			t.Errorf("%s should be terminal", state)
		}
	}
	for _, state := range []ParticipantState{ParticipantEnrolled, ParticipantDeparted, ParticipantCheckedIn, ParticipantResting, ParticipantMissing} {
		if state.Terminal() {
			t.Errorf("%s should not be terminal", state)
		}
	}
}
