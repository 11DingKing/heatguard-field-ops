package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/storage/sqlite"
)

func authFixture(t *testing.T) (*Service, *sqlite.Store, *time.Time) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	service := New(store, 2*time.Hour, func() time.Time { return now })
	return service, store, &now
}

func TestBootstrapUserValidatesFields(t *testing.T) {
	service, _, _ := authFixture(t)
	tests := []struct {
		name     string
		email    string
		display  string
		password string
		role     domain.Role
		field    string
	}{
		{"bad email", "not-an-email", "Name", "long-password", domain.RoleOrganizer, "email"},
		{"empty name", "user@example.test", " ", "long-password", domain.RoleOrganizer, "display_name"},
		{"short password", "user@example.test", "Name", "short", domain.RoleOrganizer, "password"},
		{"bad role", "user@example.test", "Name", "long-password", domain.Role("admin"), "role"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.BootstrapUser(context.Background(), test.email, test.display, test.password, test.role)
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("got %v", err)
			}
			var fieldErr domain.FieldError
			if !errors.As(err, &fieldErr) || fieldErr.Field != test.field {
				t.Fatalf("field error = %+v", fieldErr)
			}
		})
	}
}

func TestBootstrapLoginAuthenticateLogoutLifecycle(t *testing.T) {
	service, _, now := authFixture(t)
	ctx := context.Background()
	created, err := service.BootstrapUser(ctx, "  COACH@example.test ", "Field Coach", "secure-password", domain.RoleCoach)
	if err != nil {
		t.Fatal(err)
	}
	if created.Email != "coach@example.test" || created.Role != domain.RoleCoach || len(created.PasswordHash) == 0 {
		t.Fatalf("created user = %+v", created)
	}
	result, err := service.Login(ctx, "coach@example.test", "secure-password")
	if err != nil {
		t.Fatal(err)
	}
	if result.Token == "" || result.User.ID != created.ID {
		t.Fatalf("login result = %+v", result)
	}
	if want := now.Add(2 * time.Hour); !result.ExpiresAt.Equal(want) {
		t.Fatalf("expires at %s, want %s", result.ExpiresAt, want)
	}
	authenticated, err := service.Authenticate(ctx, result.Token)
	if err != nil || authenticated.ID != created.ID {
		t.Fatalf("authenticate = %+v, %v", authenticated, err)
	}
	if err := service.Logout(ctx, result.Token); err != nil {
		t.Fatal(err)
	}
	_, err = service.Authenticate(ctx, result.Token)
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revoked token got %v", err)
	}
}

func TestLoginRejectsUnknownUserAndWrongPassword(t *testing.T) {
	service, _, _ := authFixture(t)
	ctx := context.Background()
	if _, err := service.BootstrapUser(ctx, "duty@example.test", "Duty", "secure-password", domain.RoleDuty); err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct{ email, password string }{
		{"missing@example.test", "secure-password"},
		{"duty@example.test", "wrong-password"},
		{"", ""},
	} {
		if _, err := service.Login(ctx, input.email, input.password); !errors.Is(err, domain.ErrUnauthorized) {
			t.Errorf("Login(%q) got %v", input.email, err)
		}
	}
}

func TestSessionExpiresAtBoundary(t *testing.T) {
	service, _, now := authFixture(t)
	ctx := context.Background()
	if _, err := service.BootstrapUser(ctx, "expiry@example.test", "Expiry", "secure-password", domain.RoleDuty); err != nil {
		t.Fatal(err)
	}
	result, err := service.Login(ctx, "expiry@example.test", "secure-password")
	if err != nil {
		t.Fatal(err)
	}
	*now = result.ExpiresAt.Add(-time.Nanosecond)
	if _, err := service.Authenticate(ctx, result.Token); err != nil {
		t.Fatalf("token expired early: %v", err)
	}
	*now = result.ExpiresAt
	if _, err := service.Authenticate(ctx, result.Token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("token valid at exclusive expiry: %v", err)
	}
}

func TestExpireSessionsRevokesOnlyExpiredRecords(t *testing.T) {
	service, _, now := authFixture(t)
	ctx := context.Background()
	if _, err := service.BootstrapUser(ctx, "cleanup@example.test", "Cleanup", "secure-password", domain.RoleDuty); err != nil {
		t.Fatal(err)
	}
	first, err := service.Login(ctx, "cleanup@example.test", "secure-password")
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	second, err := service.Login(ctx, "cleanup@example.test", "secure-password")
	if err != nil {
		t.Fatal(err)
	}
	*now = first.ExpiresAt
	count, err := service.ExpireSessions(ctx)
	if err != nil || count != 1 {
		t.Fatalf("ExpireSessions = %d, %v", count, err)
	}
	if _, err := service.Authenticate(ctx, first.Token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Errorf("first token got %v", err)
	}
	if _, err := service.Authenticate(ctx, second.Token); err != nil {
		t.Errorf("second token got %v", err)
	}
}

func TestDuplicateEmailIsConflict(t *testing.T) {
	service, _, _ := authFixture(t)
	ctx := context.Background()
	if _, err := service.BootstrapUser(ctx, "same@example.test", "First", "secure-password", domain.RoleOrganizer); err != nil {
		t.Fatal(err)
	}
	_, err := service.BootstrapUser(ctx, "SAME@example.test", "Second", "secure-password", domain.RoleCoach)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate got %v", err)
	}
}

func TestRequireRole(t *testing.T) {
	organizer := domain.User{Role: domain.RoleOrganizer}
	coach := domain.User{Role: domain.RoleCoach}
	if err := RequireRole(organizer, domain.RoleOrganizer); err != nil {
		t.Fatal(err)
	}
	if err := RequireRole(coach, domain.RoleOrganizer, domain.RoleDuty); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("coach role got %v", err)
	}
	if err := RequireRole(coach, domain.RoleGuardian, domain.RoleCoach); err != nil {
		t.Fatal(err)
	}
}

func TestTokensAreOpaqueAndUnique(t *testing.T) {
	service, _, _ := authFixture(t)
	ctx := context.Background()
	if _, err := service.BootstrapUser(ctx, "tokens@example.test", "Tokens", "secure-password", domain.RoleDuty); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for index := 0; index < 8; index++ {
		result, err := service.Login(ctx, "tokens@example.test", "secure-password")
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Token) < 40 {
			t.Errorf("token too short: %q", result.Token)
		}
		if seen[result.Token] {
			t.Fatalf("duplicate token at index %d", index)
		}
		seen[result.Token] = true
	}
}

func TestCancelledContextPropagatesToDatabase(t *testing.T) {
	service, _, _ := authFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.BootstrapUser(ctx, "cancel@example.test", "Cancel", "secure-password", domain.RoleDuty)
	if err == nil {
		t.Fatal("expected cancelled database operation")
	}
}
