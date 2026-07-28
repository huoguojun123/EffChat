-- Basic is a built-in crawler fallback, never an administrator-managed provider.
UPDATE external_services
SET deleted_at = NOW(), enabled = false, updated_at = NOW()
WHERE service_key = 'basic' AND deleted_at IS NULL;

-- Replace the temporary Tavily search key with the final explicit key.
-- A current configuration wins when both keys already exist.
UPDATE external_services
SET deleted_at = NOW(), enabled = false, updated_at = NOW()
WHERE service_key = 'tavily'
  AND deleted_at IS NULL
  AND EXISTS (
      SELECT 1
      FROM external_services current
      WHERE current.service_key = 'tavily_search'
        AND current.deleted_at IS NULL
  );

UPDATE external_services current
SET
    display_name = 'Tavily',
    kind = legacy.kind,
    base_url = legacy.base_url,
    api_key = legacy.api_key,
    enabled = legacy.enabled,
    sort_order = legacy.sort_order,
    max_concurrency = legacy.max_concurrency,
    deleted_at = NULL,
    updated_at = NOW()
FROM external_services legacy
WHERE current.service_key = 'tavily_search'
  AND current.deleted_at IS NOT NULL
  AND legacy.service_key = 'tavily'
  AND legacy.deleted_at IS NULL;

UPDATE external_services
SET deleted_at = NOW(), enabled = false, updated_at = NOW()
WHERE service_key = 'tavily'
  AND deleted_at IS NULL
  AND EXISTS (
      SELECT 1
      FROM external_services current
      WHERE current.service_key = 'tavily_search'
        AND current.deleted_at IS NULL
  );

UPDATE external_services
SET service_key = 'tavily_search', display_name = 'Tavily', updated_at = NOW()
WHERE service_key = 'tavily'
  AND deleted_at IS NULL;
