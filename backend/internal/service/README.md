# Service 层边界

`internal/service` 承接业务编排，handler 只负责 HTTP/SSE 参数、认证上下文和响应格式。复杂逻辑优先放在 service，避免 handler 变成无法测试的流程脚本。

## 当前职责

- `auth_service.go`：注册、登录、JWT、首用户 admin 口径。
- `session_service.go` / `session_folder_service.go`：会话和文件夹权限边界。
- `message_service.go`：消息落库、Agent 历史裁剪、附件提示、压缩检查点。
- `run_hub.go`：运行中 SSE 事件记录、断连重放、停止生成。
- `title_service.go`：标题生成输入裁剪和落库。
- `model_service.go`：模型 CRUD、管理员覆盖和模型目录同步。
- `skill_*.go`：结构化 Skill 的导入、更新、权限、包文件持久化。
- `usage` 包不在本目录下：模型用量统计刻意放在 `internal/usage`，通过 ChatModel wrapper 覆盖所有上游模型调用。

## 后续收敛方向

- `stream_handler.go` 中的运行生命周期应逐步下沉为 `ChatRunService`，handler 只保留 SSE 适配。
- `file_handler.go` 中的上传、解析和删除编排应逐步拆到文件 service。
- 新增业务时先找现有 service 边界，不要在 handler 或 repository 里直接堆流程。
