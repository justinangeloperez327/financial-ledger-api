# Integration tests

Planned: PostgreSQL-backed tests for atomic journal posting, idempotency, locking,
reversals, currency precision, account policies, and ledger-level authorization.

The foundation currently includes unit tests for configuration and health handlers.
Database integration tests must be introduced before financial write endpoints ship.
