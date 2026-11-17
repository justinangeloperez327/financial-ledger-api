# Architecture decisions

**State:** Phase 1 scaffold; no financial postings are implemented.

## Architectural boundary

- Go modular monolith, Chi for HTTP routing.
- Application services will own database transaction boundaries.
- Pure domain logic will validate accounting rules.
- Explicit PostgreSQL SQL through sqlc/pgx; PostgreSQL is the source of truth.
- Redis and foreign exchange are deferred until justified.

## Core financial invariants (to implement before ledger write endpoints)

1. A posted journal has two or more lines, at least one debit and one credit, and balances exactly.
2. Amounts are positive integral minor units, checked for overflow and currency precision.
3. Every journal entry is single-currency in the MVP; all accounts and journals belong to the same ledger.
4. Posting is atomic. All inserts, status changes, and idempotency results commit together.
5. Posted journal entries and lines are immutable. Corrections create reversing journals.
6. Journal-derived account balances are authoritative. No user-editable balance column.
7. Transfers reuse the journal posting engine; no separate ledger mutations.
8. Duplicate client requests cannot create duplicate postings.
9. Ledger-level authorization applies to every resource, including indirect references.
10. Account-level negative-balance policies, where configured, are protected by consistent row-locking.

Database-side enforcement of cross-row journal balancing will be designed before the posting migration; a simple CHECK constraint cannot enforce a sum across rows.

## Currency strategy

The initial currencies migration defines reference metadata and seeds AED, PHP, and USD. Financial accounts and journal schema are not yet present. The initial posting API will be single-currency per journal; FX rates, FX clearing, and gains/losses are future work.

## Consistency

- PostgreSQL READ COMMITTED with explicit row locks for checks that require serialization.
- Lock affected accounts in deterministic order.
- Durable, scoped idempotency keys and request fingerprints.
- Use reversal entries to correct posted financial history.
- Concurrency and fault-injection integration tests are release gates.

## Security

The health endpoints are unauthenticated. Financial endpoints will not be exposed without ledger-scoped authorization and verified JWT claims. Local development credentials must never be reused in deployment.

## Deferred scope

Redis caching, asynchronous messaging, event sourcing infrastructure, FX conversion, scheduled postings, SaaS billing, and microservices.

## Next work

Finalize ERD and API write contract, then implement accounts and journals with database enforcement and concurrency tests.
