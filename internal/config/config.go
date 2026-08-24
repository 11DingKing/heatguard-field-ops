package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr          string
	DBPath            string
	SessionTTL        time.Duration
	WorkerPoll        time.Duration
	WorkerLease       time.Duration
	WorkerConcurrency int
	ShutdownTimeout   time.Duration
	LogLevel          string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:          env("HTTP_ADDR", ":8080"),
		DBPath:            env("DB_PATH", "heatguard.db"),
		SessionTTL:        12 * time.Hour,
		WorkerPoll:        500 * time.Millisecond,
		WorkerLease:       30 * time.Second,
		WorkerConcurrency: 2,
		ShutdownTimeout:   10 * time.Second,
		LogLevel:          env("LOG_LEVEL", "info"),
	}
	var err error
	if cfg.SessionTTL, err = duration("SESSION_TTL", cfg.SessionTTL); err != nil {
		return Config{}, err
	}
	if cfg.WorkerPoll, err = duration("WORKER_POLL_INTERVAL", cfg.WorkerPoll); err != nil {
		return Config{}, err
	}
	if cfg.WorkerLease, err = duration("WORKER_LEASE_DURATION", cfg.WorkerLease); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = duration("SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout); err != nil {
		return Config{}, err
	}
	if raw := strings.TrimSpace(os.Getenv("WORKER_CONCURRENCY")); raw != "" {
		cfg.WorkerConcurrency, err = strconv.Atoi(raw)
		if err != nil || cfg.WorkerConcurrency < 1 || cfg.WorkerConcurrency > 32 {
			return Config{}, fmt.Errorf("WORKER_CONCURRENCY must be between 1 and 32")
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.HTTPAddr) == "" {
		return errors.New("HTTP_ADDR is required")
	}
	if strings.TrimSpace(c.DBPath) == "" {
		return errors.New("DB_PATH is required")
	}
	if c.SessionTTL < time.Minute {
		return errors.New("SESSION_TTL must be at least one minute")
	}
	if c.WorkerPoll <= 0 || c.WorkerLease <= c.WorkerPoll {
		return errors.New("worker lease must exceed the positive poll interval")
	}
	if c.ShutdownTimeout <= 0 {
		return errors.New("SHUTDOWN_TIMEOUT must be positive")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("unsupported LOG_LEVEL %q", c.LogLevel)
	}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func duration(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}
