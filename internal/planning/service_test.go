package planning

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/11DingKing/heatguard-field-ops/internal/audit"
	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
	"github.com/11DingKing/heatguard-field-ops/internal/storage/sqlite"
)

func planningFixture(t *testing.T) (*Service, *sqlite.Store, time.Time) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "planning.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	service := New(store, audit.New(func() time.Time { return now }), func() time.Time { return now })
	return service, store, now
}

func planningUser(t *testing.T, store *sqlite.Store, email string, role domain.Role, now time.Time) domain.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("secure-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	user := domain.User{Email: email, DisplayName: email, Role: role, PasswordHash: hash, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertUser(context.Background(), &user); err != nil {
		t.Fatal(err)
	}
	return user
}

func TestGuardianCanRestrictOnlyOwnParticipant(t *testing.T) {
	service, store, now := planningFixture(t)
	guardian := planningUser(t, store, "guardian@example.test", domain.RoleGuardian, now)
	otherGuardian := planningUser(t, store, "other@example.test", domain.RoleGuardian, now)
	participant, err := service.RegisterParticipant(context.Background(), domain.Participant{Name: "Youth Runner", BirthDate: now.AddDate(-15, 0, 0), GuardianUserID: &guardian.ID, EmergencyName: "Guardian", EmergencyPhone: "13800000000"}, guardian.ID, "register-request")
	if err != nil {
		t.Fatal(err)
	}
	restriction := domain.HealthRestriction{ParticipantID: participant.ID, Kind: domain.RestrictionNeedsWater, EffectiveFrom: now, Notes: "hydration every checkpoint"}
	created, err := service.AddRestriction(context.Background(), restriction, guardian.ID, "own-request")
	if err != nil || created.ID == 0 {
		t.Fatalf("own restriction = %+v, %v", created, err)
	}
	restriction.ID = 0
	_, err = service.AddRestriction(context.Background(), restriction, otherGuardian.ID, "other-request")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("other guardian got %v", err)
	}
}

func TestOrganizerCanManageParticipantRestriction(t *testing.T) {
	service, store, now := planningFixture(t)
	organizer := planningUser(t, store, "organizer@example.test", domain.RoleOrganizer, now)
	participant, err := service.RegisterParticipant(context.Background(), domain.Participant{Name: "Adult Rider", BirthDate: now.AddDate(-30, 0, 0), EmergencyName: "Contact", EmergencyPhone: "13900000000"}, organizer.ID, "register-request")
	if err != nil {
		t.Fatal(err)
	}
	restriction := domain.HealthRestriction{ParticipantID: participant.ID, Kind: domain.RestrictionMaxMinutes, Value: 60, EffectiveFrom: now}
	if _, err := service.AddRestriction(context.Background(), restriction, organizer.ID, "restriction-request"); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListRestrictions(context.Background(), participant.ID, now.Add(time.Minute))
	if err != nil || len(got) != 1 || got[0].Value != 60 {
		t.Fatalf("restrictions = %+v, %v", got, err)
	}
}

func TestRegisterLeaderRequiresCoach(t *testing.T) {
	service, store, now := planningFixture(t)
	organizer := planningUser(t, store, "organizer-leader@example.test", domain.RoleOrganizer, now)
	leader := domain.Leader{UserID: organizer.ID, Kinds: []domain.ActivityKind{domain.ActivityRun}, QualifiedUntil: now.AddDate(1, 0, 0), EmergencyTrained: true}
	_, err := service.RegisterLeader(context.Background(), leader, organizer.ID, "leader-request")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("organizer leader got %v", err)
	}
	coach := planningUser(t, store, "coach@example.test", domain.RoleCoach, now)
	leader.UserID = coach.ID
	created, err := service.RegisterLeader(context.Background(), leader, organizer.ID, "leader-request-2")
	if err != nil || created.ID == 0 || created.Version != 1 {
		t.Fatalf("coach leader = %+v, %v", created, err)
	}
}

func TestCreateRouteRejectsInvalidSegmentBeforePersistence(t *testing.T) {
	service, store, now := planningFixture(t)
	organizer := planningUser(t, store, "organizer-route@example.test", domain.RoleOrganizer, now)
	_, _, err := service.CreateRoute(context.Background(), CreateRouteInput{Name: "Invalid Route", Zone: "north", Kind: domain.ActivityRun, DistanceMeters: 5000, ActorID: organizer.ID, RequestID: "route-request", Segments: []domain.RouteSegment{{Name: "Start", Checkpoint: domain.GeoPoint{Latitude: 30, Longitude: 120}}, {Name: "Bad", Checkpoint: domain.GeoPoint{Latitude: 100, Longitude: 120}}}})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("route got %v", err)
	}
}

func TestCloseSegmentRejectsStaleVersion(t *testing.T) {
	service, store, now := planningFixture(t)
	organizer := planningUser(t, store, "organizer-segment@example.test", domain.RoleOrganizer, now)
	_, segments, err := service.CreateRoute(context.Background(), CreateRouteInput{
		Name: "Closure Route", Zone: "north", Kind: domain.ActivityRun, DistanceMeters: 5000,
		ActorID: organizer.ID, RequestID: "route-request",
		Segments: []domain.RouteSegment{
			{Name: "Start", Checkpoint: domain.GeoPoint{Latitude: 30, Longitude: 120}},
			{Name: "Bridge", Checkpoint: domain.GeoPoint{Latitude: 30.01, Longitude: 120.01}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	segment := segments[1]
	// Tablet A retains version 1 while offline; tablet B extends the closure window first.
	staleFrom := now.Add(time.Hour)
	staleUntil := staleFrom.Add(30 * time.Minute)
	if err := service.CloseSegment(context.Background(), segment.ID, 1, organizer.ID, &staleFrom, &staleUntil, "close-a"); err != nil {
		t.Fatal(err)
	}
	// Another terminal extends the window using the refreshed version.
	current, err := store.GetRouteSegment(context.Background(), segment.ID)
	if err != nil {
		t.Fatal(err)
	}
	extendedFrom := now.Add(time.Hour)
	extendedUntil := extendedFrom.Add(2 * time.Hour)
	if err := service.CloseSegment(context.Background(), segment.ID, current.Version, organizer.ID, &extendedFrom, &extendedUntil, "close-b"); err != nil {
		t.Fatal(err)
	}
	// Stale tablet A syncs its shorter window carrying the old version; it must be rejected.
	err = service.CloseSegment(context.Background(), segment.ID, 1, organizer.ID, &staleFrom, &staleUntil, "close-stale")
	if !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale closure got %v, want version conflict", err)
	}
	preserved, err := store.GetRouteSegment(context.Background(), segment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preserved.ClosedUntil == nil || !preserved.ClosedUntil.Equal(extendedUntil) {
		t.Fatalf("newer closure not preserved: %+v", preserved)
	}
	events, err := store.ListAuditEvents(context.Background(), "route_segment", strconv.FormatInt(segment.ID, 10), repository.Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2 (no audit for rejected stale write)", len(events))
	}
}
