UPDATE tool_configs
SET timeout_seconds = 30,
    updated_at = NOW()
WHERE tool_key = 'web_extract'
  AND timeout_seconds = 20;
