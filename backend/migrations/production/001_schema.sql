-- Production migration baseline.
-- Keep this file schema-only. Runtime defaults live in Go code; development
-- seed data must not be added to production migrations.
\ir ../init.sql
