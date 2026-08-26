package planning

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/audit"
	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
	"github.com/11DingKing/heatguard-field-ops/internal/storage/sqlite"
)

func TestStaleClosureCannotOverwriteNewerWindow(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "closure.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	actor := domain.User{Email: "duty@closure.test", DisplayName: "Duty", Role: domain.RoleDuty, PasswordHash: []byte("hash"), Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.InsertUser(ctx, &actor); err != nil {
		t.Fatal(err)
	}
	service := New(store, audit.New(func() time.Time { return now }), func() time.Time { return now })
	_, segments, err := service.CreateRoute(ctx, CreateRouteInput{
		Name: "Riverside Run", Zone: "east-park", Kind: domain.ActivityRun, DistanceMeters: 4200,
		ActorID: actor.ID, RequestID: "route-create",
		Segments: []domain.RouteSegment{
			{Name: "Assembly", Checkpoint: domain.GeoPoint{Latitude: 30.1, Longitude: 120.1}},
			{Name: "Riverside", Checkpoint: domain.GeoPoint{Latitude: 30.2, Longitude: 120.2}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	freshFrom := now.Add(2 * time.Hour)
	freshUntil := freshFrom.Add(90 * time.Minute)
	if err := service.CloseSegment(ctx, segments[1].ID, 1, actor.ID, &freshFrom, &freshUntil, "closure-fresh"); err != nil {
		t.Fatal(err)
	}
	staleFrom := now.Add(30 * time.Minute)
	staleUntil := staleFrom.Add(20 * time.Minute)
	if err := service.CloseSegment(ctx, segments[1].ID, 1, actor.ID, &staleFrom, &staleUntil, "closure-stale"); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale closure error = %v, want ErrVersionConflict", err)
	}

	got, err := store.GetRouteSegment(ctx, segments[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 || got.ClosedFrom == nil || !got.ClosedFrom.Equal(freshFrom) || got.ClosedUntil == nil || !got.ClosedUntil.Equal(freshUntil) {
		t.Fatalf("closure after stale write = %+v, want version 2 and original window", got)
	}
	events, err := store.ListAuditEvents(ctx, "route_segment", strconv.FormatInt(segments[1].ID, 10), repository.Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].RequestID != "closure-fresh" {
		t.Fatalf("closure audits = %+v, want only the accepted update", events)
	}
}
