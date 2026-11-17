# Financial Ledger API

A Go REST API for double-entry accounting, immutable journal postings, account balances, and internal transfers.

> **Status: Foundation (Phase 1).** This repository currently contains the service scaffold and database tooling. Financial posting, account management, and transfers are planned but **not implemented**.

## Technology

- **Go 1.27**, **Chi** (HTTP routing), **pgx** (PostgreSQL connection pool)
- **PostgreSQL 17**, **Goose** migrations, **sqlc** typed SQL generation
- **Docker Compose**, **GitHub Actions**, **OpenAPI**
- Redis and foreign-exchange posting are intentionally deferred.

## Quick start

Requirements: Docker with Compose, or Go 1.27 and PostgreSQL 17.

```bash
docker compose up --build
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

The first Docker build resolves the Go dependencies. The development database is created automatically; **schema migrations must be applied explicitly**.

To use local Go and a local PostgreSQL database:

```bash
cp .env.example .env
# Export variables from .env using your preferred shell.
go mod tidy
go run ./cmd/api
```

For migrations, install Goose:

```bash
go install github.com/pressly/goose/v3/cmd/goose@latest
```

Then apply them using the database URL configured in your environment:

```bash
goose -dir db/migrations postgres "$DATABASE_URL" up
```

The development database is published on **localhost:5433** when using Docker Compose. Credentials in the example and Compose files are **local development only**.

## Endpoints

| Method | Path | Description |
| --- | --- | --- |
| GET | `/healthz` | Process liveness (200) |
| GET | `/readyz` | PostgreSQL readiness (200 or 503) |

No ledger-writing endpoints exist yet.

## Commands

```bash
make test           # Go unit tests
make race           # Race detector
make fmt            # Format Go sources
make run            # Run API (requires DATABASE_URL)
make sqlc           # Generate typed queries (requires sqlc CLI)
make migrate-up     # Apply migrations (requires goose and DATABASE_URL)
make migrate-down   # Roll back most recent migration
```

The initial scaffold does not include a committed `go.sum` because external module download was unavailable in the initialization environment. Run `go mod tidy` from a network-connected development environment before treating dependency resolution as locked; commit the generated `go.sum` in the next change.

## Accounting invariants (planned)

1. Each posted journal balances (debits equal credits).
2. All postings are atomic; no partial ledger changes.
3. Posted journal records are immutable; corrections use reversals.
4. Every financial operation is idempotent across safe retries.
5. Currency amounts use exact minor units; no floating-point money.
6. PostgreSQL is the financial source of truth.
7. Transfers reuse the journal-posting engine; FX is post-MVP.

See [Architecture](docs/architecture.md) and [API contract](api/openapi.yaml).

## Planned milestones

- **v0.1:** Ledger/account model, postings, balances, reversals, same-currency transfers, idempotency
- **v0.2:** Scoped authentication/authorization, audit logs, statements, trial balance
- **v1.0:** Integration and concurrency validation, documented demonstration environment and operational hardening

This is a portfolio engineering project, **not a production accounting service** in its current state.
