-- Holds the runner lock long enough for a second test runner to contend, then
-- leaves one deterministic schema object and one migration ledger row.
SELECT pg_sleep(2);

CREATE TABLE migration_concurrency_probe (
    id INTEGER PRIMARY KEY
);
