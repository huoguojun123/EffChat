-- 模型思考参数格式：provider 是调用通道，thinking_format 才是请求参数形状。
ALTER TABLE models
ADD COLUMN IF NOT EXISTS thinking_format VARCHAR(64) NOT NULL DEFAULT 'auto';

COMMENT ON COLUMN models.thinking_format IS '思考参数格式：auto=按 model_id 自动匹配，none=不下发，其它值=管理员指定官方参数格式';
