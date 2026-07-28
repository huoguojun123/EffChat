-- Switch MinerU OCR from the anonymous lightweight Agent API to token-based precise parsing.

UPDATE external_services
SET
    service_key = 'mineru',
    display_name = 'MinerU 精准解析',
    base_url = COALESCE(NULLIF(base_url, ''), 'https://mineru.net'),
    updated_at = NOW()
WHERE service_key = 'mineru_light'
  AND NOT EXISTS (SELECT 1 FROM external_services WHERE service_key = 'mineru');

INSERT INTO external_services (service_key, display_name, kind, base_url, enabled, sort_order, max_concurrency, deleted_at)
VALUES ('mineru', 'MinerU 精准解析', 'ocr', 'https://mineru.net', false, 60, 2, NULL)
ON CONFLICT (service_key) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    kind = EXCLUDED.kind,
    base_url = COALESCE(NULLIF(external_services.base_url, ''), EXCLUDED.base_url),
    sort_order = EXCLUDED.sort_order,
    max_concurrency = CASE
        WHEN external_services.max_concurrency <= 0 THEN EXCLUDED.max_concurrency
        ELSE external_services.max_concurrency
    END,
    deleted_at = NULL,
    updated_at = NOW();

UPDATE files
SET ocr_provider = 'mineru'
WHERE ocr_provider = 'mineru_light';

UPDATE external_services
SET deleted_at = NOW(), enabled = false, updated_at = NOW()
WHERE service_key = 'mineru_light';
