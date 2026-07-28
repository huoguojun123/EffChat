-- Add OCR service configuration for MinerU precise parsing.

ALTER TABLE external_services
    ADD COLUMN IF NOT EXISTS max_concurrency INTEGER NOT NULL DEFAULT 0 CHECK (max_concurrency >= 0);

ALTER TABLE external_services
    DROP CONSTRAINT IF EXISTS external_services_kind_check;

ALTER TABLE external_services
    ADD CONSTRAINT external_services_kind_check CHECK (kind IN ('search', 'crawler', 'ocr'));

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
