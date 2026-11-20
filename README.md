# Financial Ledger API

A Go REST API for double-entry accounting, immutable journals, exact balances, and same-currency transfers. PostgreSQL enforces the financial invariants; the application coordinates authorization, locking, retries, and audit history.

## Features

- Ledgers and asset, liability, equity, income, and expense accounts.
- Atomic balanced postings and exact reversal entries; no editing posted history.
- Same-currency transfers between asset accounts through the journal engine.
- Durable idempotency with request fingerprints and safe concurrent retries.
- Optional nonnegative account balances protected by ordered row locks.
- Ledger-scoped reader, writer, and admin roles with verified HS256 JWTs.
- Account balances, paginated statements, trial balance, and immutable audit events.
- Embedded, locked Goose migrations, OpenAPI contract, Docker Compose, and PostgreSQL CI tests.

**Stack:** Go 1.27, Chi, pgx, PostgreSQL 17, Goose, sqlc, Docker, GitHub Actions. Redis and FX posting are deferred.

## Run with Docker

```bash
cp .env.example .env
# Paste the output of this command into JWT_SECRET in .env:
openssl rand -hex 32
docker compose up --build -d
curl --fail http://localhost:8080/healthz
curl --fail http://localhost:8080/readyz
```

Compose starts PostgreSQL, applies migrations using the migration binary, then starts the API. The database is exposed only on `127.0.0.1:5433`; the API on `127.0.0.1:8080`. Database credentials in Compose are local development values.

Generate a one-hour bootstrap operator token (keep it private):

```bash
export TOKEN=$(docker compose exec -T api /token -admin -subject local-operator)
curl --fail http://localhost:8080/v1/ledgers \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"Personal ledger"}'
```

For ordinary access, use the returned ledger ID to issue a scoped token:

```bash
docker compose exec -T api /token -subject demo-user -ledger LEDGER_UUID -role writer
```

The token command is an **offline operator tool**. There is no public token issuance endpoint. Only trusted operators hold the signing secret. See [security and operations](docs/operations.md) for roles, secrets, database grants, and backups.

Run the complete posting → retry → transfer → reversal demonstration (requires Bash, curl, and jq):

```bash
TOKEN="$TOKEN" bash scripts/demo.sh
```

## Run with local Go

Requirements: Go 1.27.4 and PostgreSQL 17. Create an empty database, then:

```bash
export DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/ledger?sslmode=disable'
export JWT_SECRET=$(openssl rand -hex 32)
go mod download
go run ./cmd/migrate
go run ./cmd/api
# Another terminal, with the same JWT_SECRET:
go run ./cmd/token -admin -subject local-operator
```

`APP_PORT` defaults to 8080. `JWT_ISSUER` and `JWT_AUDIENCE` both default to `financial-ledger-api`. The application requires a secret of at least 32 bytes and does not load `.env` automatically outside Compose.

## API

All financial endpoints require `Authorization: Bearer TOKEN`. Paths below are relative to `/v1/ledgers/{ledgerID}` except where specified.

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/v1/ledgers` | Create ledger (global operator) |
| GET | `/v1/currencies` | Currency metadata (authenticated) |
| GET | `/` | Ledger metadata |
| POST / GET | `/accounts` | Create / list accounts |
| POST | `/accounts/{accountID}/close` | Close account (admin) |
| GET | `/accounts/{accountID}/balance` | Natural balance, debit and credit totals |
| GET | `/accounts/{accountID}/statement` | Paginated journal lines |
| POST / GET | `/journals` | Post / list journals |
| GET | `/journals/{journalID}` | Journal and lines |
| POST | `/journals/{journalID}/reverse` | Exact correcting journal |
| POST | `/transfers` | Move between asset accounts |
| GET | `/trial-balance` | Per-account debit/credit totals, separated by currency |
| GET | `/audit-events` | Paginated audit history (admin) |

`GET /healthz` and `GET /readyz` are public process and database checks.

### Money and posting semantics

Amounts are **JSON decimal strings of positive integer minor units**: `"10000"` means AED 100.00. Floating-point values are rejected. Individual amounts and journal side totals fit signed 64-bit integers. Account totals use PostgreSQL arbitrary-precision sums and are returned as strings.

A journal needs 2–100 lines, unique accounts, matching currencies and ledgers, and equal nonzero debit and credit totals. Initial supported currencies: AED, PHP, USD. No exchange-rate conversion occurs.

```json
{
  "currency": "AED",
  "description": "Opening capital",
  "lines": [
    {"account_id": "CASH_ACCOUNT_UUID", "side": "debit", "amount": "10000"},
    {"account_id": "EQUITY_ACCOUNT_UUID", "side": "credit", "amount": "10000"}
  ]
}
```

Every posting, transfer, and reversal requires `Idempotency-Key` (1–128 bytes). A first success returns 201; a matching retry returns the original journal with 200 and `Idempotency-Replayed: true`. Reusing the key with a changed operation or payload returns 409. Keys are scoped to the ledger and retained indefinitely. JSON object field order is normalized; line array order is part of the fingerprint. Failed transactions do not consume keys. Ledger/account creation uses unique account codes where applicable, rather than posting idempotency.

Asset and expense balances are debits minus credits; liability, equity, and income balances are credits minus debits. `allow_negative` defaults to true for general ledger flexibility; set it to false when creating cash or bank accounts that cannot overdraw. Transfers require two open asset accounts. Account closure requires an exact zero net balance; attempts to close a funded or overdrawn account return 409. Drain or correct the balance before closure. Closed accounts remain reportable but reject new postings, including reversals. A journal can be reversed once, and a reversal cannot itself be reversed.

Names, account codes, and descriptions use Unicode character limits matching the API contract and PostgreSQL. Idempotency keys retain their explicit 128-byte limit.

List endpoints use `limit` (default 50, maximum 200) and `offset` (default 0). Trial balance returns all accounts for a ledger to preserve a complete report; it does not combine currencies. Journal lists contain headers; the detail endpoint supplies lines. Reports include committed history at query time; statements have no editable dates or backdating.

## Verification

```bash
make test
make race
make vet
make build
# Disposable PostgreSQL database: migrations and test records are added.
export TEST_DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/ledger_test?sslmode=disable'
make integration
```

Integration tests exercise real PostgreSQL constraints, constraint-timing changes, closure/posting races, Unicode round trips, concurrent retries, competing withdrawals, opposing transfers, transaction fault injection, and HTTP workflows. Migration upgrade and rollback tests preserve existing financial history. Without `TEST_DATABASE_URL`, integration tests explicitly skip. CI supplies PostgreSQL and runs all tests with the race detector, generated-query checks, and container build.

`make sqlc` regenerates reference queries with sqlc **v1.30.0**. Lock-sensitive financial operations use explicit pgx transactions. `make migrate-up` runs the embedded Goose migrations. Rollback is an operator action using Goose CLI (`make migrate-down`); migration 2 rollback deletes ledger data and must only be used with disposable databases.

See [architecture](docs/architecture.md), [OpenAPI](api/openapi.yaml), and [operations](docs/operations.md).

This is a completed portfolio MVP. Live financial use requires deployment-specific security review, backup/restore testing, monitoring, and accounting review. FX, backdated periods, scheduled postings, user registration, and external payment settlement are outside this release.
