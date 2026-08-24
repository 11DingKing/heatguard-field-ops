# HeatGuard Field Ops

HeatGuard Field Ops is an operational safety backend for regional summer running, cycling, and park conditioning activities. Organizers plan routes and departure waves, coaches record checkpoints and hydration rests, guardians maintain participant safety contacts, and duty staff handle heat escalation, local closures, missing participants, early withdrawal, and whole-group closeout.

## Runtime

- Go 1.23
- SQLite through the pure-Go `modernc.org/sqlite` driver
- Versioned, checksummed migrations applied transactionally at startup
- Opaque, revocable server-side sessions with bcrypt password hashes
- Persisted worker jobs with leases, retry backoff, permanent failure, and restart recovery
- JSON HTTP API with request IDs, role checks, structured errors, panic recovery, and graceful shutdown
- Checksum-pinned Go dependencies with BuildKit module and build caches

Copy `.env.example` values into the runtime environment as needed. The default bootstrap account is `organizer@heatguard.local` with password `change-this-password`; production deployments must override `BOOTSTRAP_EMAIL`, `BOOTSTRAP_NAME`, and `BOOTSTRAP_PASSWORD` before first startup.

```sh
go run ./cmd/server
```

The service listens on `:8080` by default. `GET /healthz` checks process liveness and `GET /readyz` verifies database access.

## Main API

All business endpoints use `Authorization: Bearer <token>` after `POST /api/v1/login`.

- `POST /api/v1/routes` creates a route and its ordered checkpoints atomically.
- `POST /api/v1/leaders` registers a qualified coach as a leader.
- `POST /api/v1/participants` registers a participant and emergency contact.
- `POST /api/v1/participants/{id}/restrictions` records a time-scoped health restriction.
- `POST /api/v1/risk-rules` publishes a zone, activity, and time-scoped risk rule.
- `POST /api/v1/waves` creates a departure wave.
- `POST /api/v1/waves/{id}/enrollments` enrolls an eligible participant with capacity enforcement.
- `POST /api/v1/waves/{id}/ready` freezes readiness after enrollment.
- `POST /api/v1/waves/{id}/depart` evaluates route closures, leader qualification, participant restrictions, and current risk rules in one transaction.
- `POST /api/v1/waves/{id}/events` records checkpoints, hydration, missing, found, withdrawal, and completion events idempotently.
- `POST /api/v1/alerts/{id}/acknowledge` acknowledges an alert with optimistic concurrency.
- `POST /api/v1/waves/{id}/close` closes only after every participant has a terminal disposition and no open safety alert remains.

## Persistence

The embedded migration ledger currently creates 16 related business tables plus `schema_migrations`: identity and sessions; routes and segments; leaders, participants, and restrictions; waves and enrollments; time-scoped risk rules; field events and alerts; notification deliveries; worker jobs; idempotency records; and durable audit events. Foreign keys are enabled for every connection. Applied migration names and checksums are verified on every startup, and a conflict stops startup without rewriting historical data.

Transactions cover route creation, wave planning and enrollment, departure, field events with derived alerts/jobs/audit, participant split/withdrawal, and closeout. Version columns and conditional updates prevent stale wave, segment, alert, and participant changes. Unique constraints protect idempotency keys, delivery identity, and worker deduplication.

## Verification

```sh
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
```

Docker uses the real `./cmd/server` entry and persists SQLite under `/data`:

```sh
docker build --platform linux/amd64 -t heatguard-field-ops:amd64 .
docker build --platform linux/arm64 -t heatguard-field-ops:arm64 .
docker run --rm -p 8080:8080 heatguard-field-ops:amd64
```
