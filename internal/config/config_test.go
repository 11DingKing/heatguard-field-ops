package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{"HTTP_ADDR", "DB_PATH", "SESSION_TTL", "WORKER_POLL_INTERVAL", "WORKER_LEASE_DURATION", "WORKER_CONCURRENCY", "SHUTDOWN_TIMEOUT", "LOG_LEVEL"} {
		t.Setenv(key, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.DBPath != "heatguard.db" {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.SessionTTL != 12*time.Hour || cfg.WorkerConcurrency != 2 {
		t.Fatalf("duration defaults = %+v", cfg)
	}
}

func TestLoadEnvironmentOverrides(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("DB_PATH", "/tmp/custom.db")
	t.Setenv("SESSION_TTL", "4h")
	t.Setenv("WORKER_POLL_INTERVAL", "250ms")
	t.Setenv("WORKER_LEASE_DURATION", "5s")
	t.Setenv("WORKER_CONCURRENCY", "7")
	t.Setenv("SHUTDOWN_TIMEOUT", "20s")
	t.Setenv("LOG_LEVEL", "debug")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":9090" || cfg.DBPath != "/tmp/custom.db" || cfg.SessionTTL != 4*time.Hour || cfg.WorkerConcurrency != 7 || cfg.LogLevel != "debug" {
		t.Fatalf("overrides = %+v", cfg)
	}
}

func TestLoadRejectsInvalidEnvironment(t *testing.T) {
	tests := []struct{ key, value string }{
		{"SESSION_TTL", "bad"},
		{"SESSION_TTL", "30s"},
		{"WORKER_POLL_INTERVAL", "0s"},
		{"WORKER_LEASE_DURATION", "100ms"},
		{"WORKER_CONCURRENCY", "0"},
		{"WORKER_CONCURRENCY", "33"},
		{"WORKER_CONCURRENCY", "abc"},
		{"SHUTDOWN_TIMEOUT", "0s"},
		{"LOG_LEVEL", "verbose"},
	}
	for _, test := range tests {
		t.Run(test.key+"="+test.value, func(t *testing.T) {
			for _, key := range []string{"SESSION_TTL", "WORKER_POLL_INTERVAL", "WORKER_LEASE_DURATION", "WORKER_CONCURRENCY", "SHUTDOWN_TIMEOUT", "LOG_LEVEL"} {
				t.Setenv(key, "")
			}
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateRejectsCrossFieldWorkerTiming(t *testing.T) {
	cfg := Config{HTTPAddr: ":8080", DBPath: "test.db", SessionTTL: time.Hour, WorkerPoll: time.Second, WorkerLease: time.Second, ShutdownTimeout: time.Second, LogLevel: "info"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("lease equal to poll should fail")
	}
	cfg.WorkerLease = 2 * time.Second
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config failed: %v", err)
	}
}
