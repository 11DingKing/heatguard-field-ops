package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/audit"
	"github.com/11DingKing/heatguard-field-ops/internal/auth"
	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/operations"
	"github.com/11DingKing/heatguard-field-ops/internal/planning"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
	"github.com/11DingKing/heatguard-field-ops/internal/storage/sqlite"
)

type apiFixture struct {
	handler http.Handler
	auth    *auth.Service
	store   *sqlite.Store
	now     time.Time
}

func newAPIFixture(t *testing.T) apiFixture {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	auditService := audit.New(clock)
	authService := auth.New(store, 2*time.Hour, clock)
	planningService := planning.New(store, auditService, clock)
	operationsService := operations.New(store, auditService, clock)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := New(authService, planningService, operationsService, store, logger).Handler()
	return apiFixture{handler: handler, auth: authService, store: store, now: now}
}

func performRequest(handler http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		payload, _ := json.Marshal(body)
		reader = bytes.NewReader(payload)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func loginToken(t *testing.T, fixture apiFixture, email, password string, role domain.Role) string {
	t.Helper()
	if _, err := fixture.auth.BootstrapUser(context.Background(), email, "Test User", password, role); err != nil {
		t.Fatal(err)
	}
	recorder := performRequest(fixture.handler, http.MethodPost, "/api/v1/login", "", map[string]any{"email": email, "password": password})
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Token
}

func TestHealthAndReadiness(t *testing.T) {
	fixture := newAPIFixture(t)
	for _, path := range []string{"/healthz", "/readyz"} {
		recorder := performRequest(fixture.handler, http.MethodGet, path, "", nil)
		if recorder.Code != http.StatusOK {
			t.Errorf("%s status %d: %s", path, recorder.Code, recorder.Body.String())
		}
		if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
			t.Errorf("%s content type %q", path, contentType)
		}
		if recorder.Header().Get("X-Request-ID") == "" {
			t.Errorf("%s lacks request ID", path)
		}
	}
}

func TestRequestIDIsPreserved(t *testing.T) {
	fixture := newAPIFixture(t)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("X-Request-ID", "caller-request-42")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if got := recorder.Header().Get("X-Request-ID"); got != "caller-request-42" {
		t.Fatalf("request id = %q", got)
	}
}

func TestLoginContractAndLogoutRevocation(t *testing.T) {
	fixture := newAPIFixture(t)
	password := "secure-password"
	token := loginToken(t, fixture, "coach-api@example.test", password, domain.RoleCoach)
	response := performRequest(fixture.handler, http.MethodPost, "/api/v1/logout", token, nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("logout status %d: %s", response.Code, response.Body.String())
	}
	response = performRequest(fixture.handler, http.MethodGet, "/api/v1/waves/1", token, nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status %d: %s", response.Code, response.Body.String())
	}
}

func TestLoginRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	fixture := newAPIFixture(t)
	tests := []string{
		`{"email":"a@example.test","password":"password123","extra":true}`,
		`{"email":"a@example.test","password":"password123"} {}`,
		`{"email":`,
	}
	for _, body := range tests {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("body %q status %d", body, recorder.Code)
		}
		assertAPIError(t, recorder, "validation_failed")
	}
}

func TestAuthenticationErrorsHaveStableShape(t *testing.T) {
	fixture := newAPIFixture(t)
	for _, token := range []string{"", "unknown-token"} {
		recorder := performRequest(fixture.handler, http.MethodGet, "/api/v1/waves/1", token, nil)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("token %q status %d", token, recorder.Code)
		}
		assertAPIError(t, recorder, "unauthorized")
	}
}

func TestRoleAuthorizationRejectsCoachCreatingRoute(t *testing.T) {
	fixture := newAPIFixture(t)
	token := loginToken(t, fixture, "coach-role@example.test", "secure-password", domain.RoleCoach)
	body := map[string]any{
		"name":            "River",
		"zone":            "north",
		"activity_kind":   "run",
		"distance_meters": 5000,
		"segments": []map[string]any{
			{"name": "Start", "checkpoint": map[string]any{"latitude": 30, "longitude": 120}},
			{"name": "Finish", "checkpoint": map[string]any{"latitude": 31, "longitude": 121}},
		},
	}
	recorder := performRequest(fixture.handler, http.MethodPost, "/api/v1/routes", token, body)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	assertAPIError(t, recorder, "forbidden")
}

func TestOrganizerCreatesRouteThroughHTTP(t *testing.T) {
	fixture := newAPIFixture(t)
	token := loginToken(t, fixture, "organizer-route@example.test", "secure-password", domain.RoleOrganizer)
	body := map[string]any{
		"name":            "River Loop",
		"zone":            "north",
		"activity_kind":   "run",
		"distance_meters": 5000,
		"segments": []map[string]any{
			{"name": "Start", "checkpoint": map[string]any{"latitude": 30, "longitude": 120}, "hydration_site": true},
			{"name": "Finish", "checkpoint": map[string]any{"latitude": 31, "longitude": 121}, "hydration_site": true},
		},
	}
	recorder := performRequest(fixture.handler, http.MethodPost, "/api/v1/routes", token, body)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Route    domain.Route          `json:"route"`
		Segments []domain.RouteSegment `json:"segments"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Route.ID == 0 || len(response.Segments) != 2 || response.Segments[0].RouteID != response.Route.ID {
		t.Fatalf("response = %+v", response)
	}
	audits, err := fixture.store.ListAuditEvents(context.Background(), "route", strconv.FormatInt(response.Route.ID, 10), repository.Page{})
	if err != nil || len(audits) != 1 || audits[0].Action != "route.create" {
		t.Fatalf("audit = %+v, %v", audits, err)
	}
}

func TestRouteValidationReturnsRequestID(t *testing.T) {
	fixture := newAPIFixture(t)
	token := loginToken(t, fixture, "organizer-invalid@example.test", "secure-password", domain.RoleOrganizer)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/routes", strings.NewReader(`{"name":"","zone":"","activity_kind":"run","distance_meters":0,"segments":[]}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Request-ID", "invalid-route-request")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var body errorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.RequestID != "invalid-route-request" || body.Error.Code != "validation_failed" {
		t.Fatalf("error = %+v", body)
	}
}

func TestUnknownResourceMapsToNotFound(t *testing.T) {
	fixture := newAPIFixture(t)
	token := loginToken(t, fixture, "duty-notfound@example.test", "secure-password", domain.RoleDuty)
	recorder := performRequest(fixture.handler, http.MethodGet, "/api/v1/waves/999", token, nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	assertAPIError(t, recorder, "not_found")
}

func TestInvalidPathIDMapsToValidation(t *testing.T) {
	fixture := newAPIFixture(t)
	token := loginToken(t, fixture, "duty-path@example.test", "secure-password", domain.RoleDuty)
	for _, path := range []string{"/api/v1/waves/abc", "/api/v1/waves/0", "/api/v1/waves/-1"} {
		recorder := performRequest(fixture.handler, http.MethodGet, path, token, nil)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("path %s status %d", path, recorder.Code)
		}
		assertAPIError(t, recorder, "validation_failed")
	}
}

func assertAPIError(t *testing.T, recorder *httptest.ResponseRecorder, code string) {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v; body=%s", err, recorder.Body.String())
	}
	if body.Error.Code != code {
		t.Errorf("code = %q, want %q", body.Error.Code, code)
	}
	if body.Error.Message == "" || body.Error.RequestID == "" {
		t.Errorf("incomplete error: %+v", body.Error)
	}
}

type failingReadiness struct{ err error }

func (f failingReadiness) Ping(context.Context) error { return f.err }

func TestReadinessFailureIsUnavailable(t *testing.T) {
	fixture := newAPIFixture(t)
	server := New(fixture.auth, planning.New(fixture.store, audit.New(time.Now), time.Now), operations.New(fixture.store, audit.New(time.Now), time.Now), failingReadiness{err: errors.New("database down")}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	recorder := performRequest(server.Handler(), http.MethodGet, "/readyz", "", nil)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	assertAPIError(t, recorder, "unavailable")
}
