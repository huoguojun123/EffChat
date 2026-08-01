-- Deliberately fails after both DDL and DML so the migration runner test can
-- prove that the migration and its schema_migrations row share one rollback.
CREATE TABLE migration_atomicity_probe (
    id INTEGER PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO migration_atomicity_probe (id, value)
VALUES (1, 'invalid');

ALTER TABLE migration_atomicity_probe
    ADD CONSTRAINT migration_atomicity_probe_value_check
    CHECK (value = 'valid');
