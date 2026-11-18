# Security and operations

## Access

Use a cryptographically random `JWT_SECRET` of at least 32 bytes. Keep it outside the repository and frontend applications. The example file intentionally has no secret. Token lifetime is limited to one hour; issuer and audience must match the API configuration. Tokens carry:

```json
{
  "sub": "operator-or-user-id",
  "iss": "financial-ledger-api",
  "aud": "financial-ledger-api",
  "iat": 1800000000,
  "nbf": 1800000000,
  "exp": 1800003600,
  "ledgers": {"LEDGER_UUID": "writer"}
}
```

`admin: true` grants global operator access. Only a trusted offline operator should issue such tokens. Never expose the signing secret or token command through an HTTP endpoint.

| Role | Permissions |
| --- | --- |
| Reader | Ledger metadata, accounts, journal history, balances, statements, trial balance |
| Writer | Reader permissions plus account creation, journals, transfers, reversals |
| Ledger admin | Writer permissions plus account closure and audit history |
| Global operator | All ledgers, plus ledger creation |

Currency metadata requires any valid token. Health checks are public. Failed authentication returns 401, denied ledger access 403, absent scoped resources 404, invalid input 400, and state/idempotency/lock conflicts 409. Unknown failures return 500 with a request ID; database details stay in server logs. Check constraints can also surface as 409.

To invalidate every token, replace `JWT_SECRET` and restart all API instances. Individual revocation and gradual signing-key rotation require an external identity integration, which is outside the demonstration. Keep system clocks synchronized.

## Database roles

Compose uses a single database owner only for local development. For deployment, run migrations using a separate owner role. The API role must not own tables, create schema objects, disable triggers, truncate tables, or change session replication behavior.

An operator may adapt these grants after provisioning login credentials separately:

```sql
GRANT CONNECT ON DATABASE ledger TO ledger_app;
GRANT USAGE ON SCHEMA public TO ledger_app;
GRANT SELECT ON currencies, ledgers, accounts, journals, journal_lines,
  idempotency_keys, audit_events TO ledger_app;
GRANT INSERT ON ledgers, accounts, journals, journal_lines,
  idempotency_keys, audit_events TO ledger_app;
GRANT UPDATE (closed) ON accounts TO ledger_app;
GRANT USAGE, SELECT ON SEQUENCE audit_events_id_seq TO ledger_app;
```

Review default public schema privileges and organization-wide database policies before adopting the example. The runtime role does not require migration-table access or DELETE grants. Database administrators remain a trust boundary.

## Start, migrate, and stop

`docker compose up --build -d` applies embedded migrations in the `migrate` service before API startup. `docker compose logs migrate api` shows failures. Migrations use a session advisory lock to serialize migration commands. Run only reviewed migrations; never roll back a populated ledger as a routine deployment strategy.

A local deployment can run `DATABASE_URL=... go run ./cmd/migrate`, then `go run ./cmd/api`. Send SIGTERM for graceful HTTP shutdown (ten-second deadline). Put TLS and ingress limits in front of the API; do not expose PostgreSQL publicly. This repository does not auto-deploy.

## Recovery and observation

- Configure encrypted PostgreSQL backups and point-in-time recovery appropriate to the required RPO/RTO. Test restores in an isolated environment.
- Restore journals, lines, accounts, idempotency keys, audit history, and migration state together. Partial restoration breaks retry and reporting guarantees.
- After restore, verify trial balance per currency, journal constraint checks, account policy checks, and a known idempotent request before accepting traffic.
- Probe `/healthz` for process liveness and `/readyz` for PostgreSQL connectivity. Readiness does not certify schema version, backup health, or accounting correctness.
- Collect JSON error logs and request IDs. Never log bearer tokens or signing secrets. Track latency, error rate, pool contention, lock timeouts, and database/backup capacity in the deployment platform.

Audit events cover successful ledger/account creation, account closure, and financial postings. Denied requests and authentication attempts are not persisted as financial audit events. Ingress/security logging should cover those separately. Trial balance and reports operate on all committed entries; there is no period close or backdate support.

## Testing

Use a dedicated, disposable `TEST_DATABASE_URL`. Integration tests apply migrations, create test ledgers, and briefly install a fault-injection trigger. The tests require an owner-level database role and must never target a real ledger database. They do not delete existing records. CI creates a fresh PostgreSQL service for each run.
