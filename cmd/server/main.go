package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/audit"
	"github.com/11DingKing/heatguard-field-ops/internal/auth"
	"github.com/11DingKing/heatguard-field-ops/internal/config"
	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/httpapi"
	"github.com/11DingKing/heatguard-field-ops/internal/notification"
	"github.com/11DingKing/heatguard-field-ops/internal/operations"
	"github.com/11DingKing/heatguard-field-ops/internal/planning"
	"github.com/11DingKing/heatguard-field-ops/internal/storage/sqlite"
	"github.com/11DingKing/heatguard-field-ops/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := sqlite.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()
	now := time.Now
	auditService := audit.New(now)
	authService := auth.New(store, cfg.SessionTTL, now)
	if err := bootstrap(ctx, authService); err != nil {
		return err
	}
	planningService := planning.New(store, auditService, now)
	operationsService := operations.New(store, auditService, now)
	api := httpapi.New(authService, planningService, operationsService, store, logger)
	notifications := notification.New(store, logSender{logger: logger}, now)
	runner := worker.New(store, "server-1", cfg.WorkerPoll, cfg.WorkerLease, cfg.WorkerConcurrency, logger, now)
	runner.Register("checkpoint_watch", func(context.Context, domain.WorkerJob) error { return nil })
	runner.Register("notify_emergency_contact", func(ctx context.Context, job domain.WorkerJob) error {
		payload, err := worker.DecodeAlertPayload(job)
		if err != nil {
			return err
		}
		participant, err := store.GetParticipant(ctx, payload.ParticipantID)
		if err != nil {
			return err
		}
		return notifications.Notify(ctx, payload.AlertID, participant.EmergencyPhone, "sms")
	})
	go func() {
		if err := runner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("worker stopped", "error", err)
		}
	}()
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	errCh := make(chan error, 1)
	go func() { logger.Info("server listening", "addr", cfg.HTTPAddr); errCh <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
func bootstrap(ctx context.Context, service *auth.Service) error {
	_, err := service.BootstrapUser(ctx, env("BOOTSTRAP_EMAIL", "organizer@heatguard.local"), env("BOOTSTRAP_NAME", "HeatGuard Organizer"), env("BOOTSTRAP_PASSWORD", "change-this-password"), domain.RoleOrganizer)
	if errors.Is(err, domain.ErrConflict) {
		return nil
	}
	return err
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

type logSender struct{ logger *slog.Logger }

func (s logSender) Send(ctx context.Context, contact, message string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.logger.Info("notification accepted", "contact_length", len(contact), "message_length", len(message))
	return "accepted", nil
}
