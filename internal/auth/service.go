package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
)

type Clock func() time.Time

type Service struct {
	store repository.Store
	ttl   time.Duration
	now   Clock
}

type LoginResult struct {
	Token     string      `json:"token"`
	ExpiresAt time.Time   `json:"expires_at"`
	User      domain.User `json:"user"`
}

func New(store repository.Store, ttl time.Duration, now Clock) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, ttl: ttl, now: now}
}

func (s *Service) BootstrapUser(ctx context.Context, email, name, password string, role domain.Role) (domain.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") {
		return domain.User{}, domain.Validation("email", "must be a valid email address")
	}
	if strings.TrimSpace(name) == "" {
		return domain.User{}, domain.Validation("display_name", "is required")
	}
	if len(password) < 10 {
		return domain.User{}, domain.Validation("password", "must have at least 10 characters")
	}
	if !role.Valid() {
		return domain.User{}, domain.Validation("role", "is unsupported")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return domain.User{}, fmt.Errorf("hash password: %w", err)
	}
	now := s.now().UTC()
	user := domain.User{Email: email, DisplayName: strings.TrimSpace(name), PasswordHash: hash, Role: role, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := s.store.InsertUser(ctx, &user); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (LoginResult, error) {
	user, err := s.store.FindUserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return LoginResult{}, domain.ErrUnauthorized
		}
		return LoginResult{}, err
	}
	if !user.Active || bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(password)) != nil {
		return LoginResult{}, domain.ErrUnauthorized
	}
	token, hash, err := newToken()
	if err != nil {
		return LoginResult{}, err
	}
	now := s.now().UTC()
	session := domain.Session{UserID: user.ID, TokenHash: hash, ExpiresAt: now.Add(s.ttl), CreatedAt: now, LastSeen: now}
	if err := s.store.InsertSession(ctx, &session); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: token, ExpiresAt: session.ExpiresAt, User: user}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (domain.User, error) {
	if strings.TrimSpace(token) == "" {
		return domain.User{}, domain.ErrUnauthorized
	}
	hash := sha256.Sum256([]byte(token))
	session, user, err := s.store.GetSessionByHash(ctx, hash[:], s.now().UTC())
	if err != nil {
		return domain.User{}, err
	}
	if err := s.store.TouchSession(ctx, session.ID, s.now().UTC()); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	hash := sha256.Sum256([]byte(token))
	_, err := s.store.RevokeSession(ctx, hash[:], s.now().UTC())
	return err
}

func (s *Service) ExpireSessions(ctx context.Context) (int64, error) {
	return s.store.RevokeExpiredSessions(ctx, s.now().UTC())
}

func RequireRole(user domain.User, allowed ...domain.Role) error {
	for _, role := range allowed {
		if user.Role == role {
			return nil
		}
	}
	return domain.ErrForbidden
}

func newToken() (string, []byte, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", nil, fmt.Errorf("create session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buffer)
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}
