-- Remove the historical built-in skill seeds.
-- User-created, Git-imported, and Zip-imported skills are intentionally untouched.

DELETE FROM skills
WHERE is_builtin = true
  AND source_type = 'builtin'
  AND created_by IS NULL
  AND id IN (
    'code-review',
    'implementation-planner',
    'frontend-polish',
    'release-operator',
    'research-summarizer'
  );
