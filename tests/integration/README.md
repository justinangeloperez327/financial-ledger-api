# PostgreSQL integration tests

Set `TEST_DATABASE_URL` to a disposable PostgreSQL 17 database and run `make integration`.

Tests apply embedded Goose migrations, create independent ledgers, and exercise HTTP posting, durable idempotency, exact reversal, simultaneous retries, overdraft races, opposing transfers, closed/cross-ledger/currency rejection, immutable database history, deferred balancing, and failure after journal-line insertion. The fault-injection test briefly creates a database trigger and requires migration/owner privileges. Never use a live database.

Without the environment variable, tests explicitly skip. CI always supplies a fresh PostgreSQL database and runs with `-race -count=1`.
