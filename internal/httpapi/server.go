package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/auth"
	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/operations"
	"github.com/11DingKing/heatguard-field-ops/internal/planning"
	"github.com/11DingKing/heatguard-field-ops/internal/requestid"
)

type readiness interface{ Ping(context.Context) error }
type Server struct {
	auth       *auth.Service
	planning   *planning.Service
	operations *operations.Service
	ready      readiness
	logger     *slog.Logger
	mux        *http.ServeMux
}

func New(a *auth.Service, p *planning.Service, o *operations.Service, ready readiness, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{auth: a, planning: p, operations: o, ready: ready, logger: logger, mux: http.NewServeMux()}
	s.routes()
	return s
}
func (s *Server) Handler() http.Handler { return s.recover(s.requestID(s.log(s.mux))) }
func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /readyz", s.readiness)
	s.mux.HandleFunc("POST /api/v1/login", s.login)
	s.mux.Handle("POST /api/v1/logout", s.withAuth(http.HandlerFunc(s.logout)))
	s.mux.Handle("POST /api/v1/routes", s.withRoles(http.HandlerFunc(s.createRoute), domain.RoleOrganizer))
	s.mux.Handle("POST /api/v1/leaders", s.withRoles(http.HandlerFunc(s.createLeader), domain.RoleOrganizer))
	s.mux.Handle("POST /api/v1/participants", s.withRoles(http.HandlerFunc(s.createParticipant), domain.RoleOrganizer))
	s.mux.Handle("POST /api/v1/participants/{id}/restrictions", s.withRoles(http.HandlerFunc(s.createRestriction), domain.RoleOrganizer, domain.RoleGuardian))
	s.mux.Handle("POST /api/v1/risk-rules", s.withRoles(http.HandlerFunc(s.createRiskRule), domain.RoleOrganizer, domain.RoleDuty))
	s.mux.Handle("POST /api/v1/waves", s.withRoles(http.HandlerFunc(s.createWave), domain.RoleOrganizer))
	s.mux.Handle("GET /api/v1/waves/{id}", s.withRoles(http.HandlerFunc(s.getWave), domain.RoleOrganizer, domain.RoleCoach, domain.RoleDuty))
	s.mux.Handle("POST /api/v1/waves/{id}/enrollments", s.withRoles(http.HandlerFunc(s.enroll), domain.RoleOrganizer))
	s.mux.Handle("POST /api/v1/waves/{id}/ready", s.withRoles(http.HandlerFunc(s.readyWave), domain.RoleOrganizer))
	s.mux.Handle("POST /api/v1/waves/{id}/depart", s.withRoles(http.HandlerFunc(s.depart), domain.RoleOrganizer, domain.RoleCoach))
	s.mux.Handle("POST /api/v1/waves/{id}/events", s.withRoles(http.HandlerFunc(s.recordEvent), domain.RoleCoach, domain.RoleDuty))
	s.mux.Handle("POST /api/v1/waves/{id}/close", s.withRoles(http.HandlerFunc(s.closeWave), domain.RoleOrganizer, domain.RoleDuty))
	s.mux.Handle("POST /api/v1/alerts/{id}/acknowledge", s.withRoles(http.HandlerFunc(s.ackAlert), domain.RoleOrganizer, domain.RoleDuty))
}

type principalKey struct{}

func principal(ctx context.Context) domain.User {
	u, _ := ctx.Value(principalKey{}).(domain.User)
	return u
}
func bearer(r *http.Request) string {
	parts := strings.SplitN(strings.TrimSpace(r.Header.Get("Authorization")), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.auth.Authenticate(r.Context(), bearer(r))
		if err != nil {
			writeError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, u)))
	})
}
func (s *Server) withRoles(next http.Handler, roles ...domain.Role) http.Handler {
	return s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := auth.RequireRole(principal(r.Context()), roles...); err != nil {
			writeError(w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	}))
}
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if id == "" || len(id) > 128 {
			id = requestid.New()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(requestid.With(r.Context(), id)))
	})
}
func (s *Server) log(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("http request", "method", r.Method, "path", r.URL.Path, "request_id", requestid.From(r.Context()), "duration_ms", time.Since(start).Milliseconds())
	})
}
func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.logger.Error("http panic", "panic", v, "stack", string(debug.Stack()), "request_id", requestid.From(r.Context()))
				writeError(w, r, domain.ErrUnavailable)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
