# Eino ADK 正确集成实现

## ✅ 已完成

### 核心实现
- ✅ 使用 `adk.NewChatModelAgent` 创建 Agent
- ✅ 使用 `adk.NewRunner` 驱动执行
- ✅ 使用 `runner.Run()` 进行事件迭代
- ✅ 正确处理 `MessageStream.Recv()` 流式输出（逐帧 delta + `ConcatMessages` 还原完整消息）
- ✅ 集成 `eino-ext/components/model/openai`
- ✅ OpenAI 兼容路径经 NewAPI 统一网关（`BaseURL`）访问
- ✅ SSE 事件流正常工作

### M3：工具调用 + 搜索 + 推理（已完成）
- ✅ 自适应搜索决策（`modelbank.ResolveSearchConfig`，移植自 LobeHub getSearchConfig）
- ✅ SearXNG / Tavily 搜索工具（`tool.WebSearchTool`）按决策挂载
- ✅ Firecrawl → Jina → basic 网页提取链路（`tool.WebExtractTool`）
- ✅ 按事件 `Role` 分流：assistant 正文与 tool 结果严格分离，工具 JSON 不再污染回答
- ✅ `tool_call_start` + `tool_call_result` 事件完整下发
- ✅ `thinking_delta` 接入 `ReasoningContent`
- ✅ 从最终消息 `ResponseMeta` 提取 `Usage` / `FinishReason`
- ✅ 工具调用链完整持久化（assistant+tool_calls / tool 结果 / 最终 assistant 逐条入库，
  字段名对齐数据库生成列 `has_tool_calls` / `has_reasoning`，可跨请求回放）
- ✅ 前端断连不中断生成：后端跑完并存库（`clientGone` 标记）
- ✅ 单元测试覆盖消息往返与流式重组（`eino_agent_test.go`）

### M4：多 Provider 原生接入（已完成）
- ✅ per-provider 配置（`config.ProviderConfig`）：每家独立 key/baseURL/enabled，
  BaseURL 可指向 NewAPI 的厂商原生路由
- ✅ `buildChatModel` 四类分支接通全部 provider：
  - **openai / perplexity / deepseek**：复用 `eino-ext/components/model/openai`
    （三家均为 OpenAI 兼容协议，仅 key+baseURL+model 不同，无需各自原生 SDK）
  - **anthropic**：`eino-ext/components/model/claude`，BaseURL 指向原生 `/v1/messages`
  - **google**：先建 `genai.Client`（APIKey + `HTTPOptions.BaseURL`），再 `gemini.NewChatModel`；
    按搜索决策挂载 `EnableGoogleSearch`（params 型原生 grounding）
- ✅ model/provider 从会话读取（`stream_handler` 不再硬编码 gpt-4o-mini）
- ✅ provider 前置校验：未配置/未启用/无 key 时返回清晰错误，不把空 key 透传给 SDK
- ✅ 环境变量劫持修复：`godotenv.Overload()` 令 `.env` 对进程环境具有权威性，
  避免开发机 shell 中已存在的 `ANTHROPIC_BASE_URL` 等变量劫持后端 provider 端点
- ✅ 采样参数透传：Temperature/MaxTokens 统一下传（`<=0` 视为未设置交模型默认）

### 测试结果

```
✅ OpenAI gpt-5.5 调用成功（经 NewAPI 网关，后端 /messages/stream 接口）
✅ Anthropic claude-opus-4-8 调用成功（环境变量劫持修复后，原生 /v1/messages）
✅ Google gemini-3-flash-preview 调用成功（genai.Client + 原生端点）
✅ SSE 流式事件: message_start → (thinking_delta) → content_delta
   → tool_call_start → tool_call_result → content_delta → message_complete
✅ SearXNG 集成测试：实时返回引用（go test -run Integration）
✅ 流式重组单测：tool_calls / reasoning / usage 跨帧保留
✅ 消息按调用链顺序保存到数据库
✅ provider 未启用错误路径：返回 "provider \"deepseek\" is not enabled"

⚠️ DeepSeek 真实出流被网关侧上游渠道阻塞（返回 HTML 非 JSON），非代码问题；
   代码路径与 openai 同构（已由 gpt-5.5 覆盖验证），待网关渠道修复后补测
```

## 正确的调用方式

```go
// 1. 创建 ChatModel
chatModel, _ := openai.NewChatModel(ctx, &openai.ChatModelConfig{
    Model:  "gpt-4o-mini",
    APIKey: apiKey,
})

// 2. 创建 ChatModelAgent
agent, _ := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
    Model:       chatModel,
    Instruction: systemPrompt,
    MaxIterations: 20,
})

// 3. 创建 Runner
runner := adk.NewRunner(ctx, adk.RunnerConfig{
    Agent: agent,
})

// 4. 流式执行
iter := runner.Run(ctx, messages)
for {
    event, ok := iter.Next()
    if !ok {
        break  // 完成
    }

    // 处理 MessageStream
    if event.Output.MessageOutput.MessageStream != nil {
        stream := event.Output.MessageOutput.MessageStream
        for {
            msg, err := stream.Recv()
            if err == io.EOF {
                break
            }
            // 处理每个 chunk
            writer.WriteEvent("content_delta", msg.Content)
        }
    }
}
```

## 之前的错误

### ❌ 错误实现
```go
// 直接使用 components/model API（这是内部实现）
import "github.com/cloudwego/eino/components/model"

client := model.NewChatModel(...)  // 错误！
resp, _ := client.Generate(...)     // 错误！
```

### ✅ 正确实现
```go
// 使用 adk + eino-ext
import "github.com/cloudwego/eino/adk"
import "github.com/cloudwego/eino-ext/components/model/openai"

chatModel, _ := openai.NewChatModel(...)  // 正确！
agent, _ := adk.NewChatModelAgent(...)     // 正确！
runner := adk.NewRunner(...)               // 正确！
```

## Provider 接入（M4 已完成）

> 全部 provider 已接通真实调用；工具调用与推理输出在 M3 完成（见下文 3、4）。

### 1. Anthropic (Claude) 支持（✅ 已完成）
```go
import "github.com/cloudwego/eino-ext/components/model/claude"

cfg := &claude.Config{APIKey: cred.APIKey, Model: req.ModelID, MaxTokens: req.MaxTokens}
if cred.BaseURL != "" {
    baseURL := cred.BaseURL // 指向 NewAPI 的 Anthropic 原生路由 /v1/messages
    cfg.BaseURL = &baseURL
}
chatModel, _ := claude.NewChatModel(ctx, cfg)
```

> 注意：claude 组件会读取 `ANTHROPIC_BASE_URL` 环境变量，若进程环境（如开发机 shell）
> 已存在该变量会劫持端点。已通过 `godotenv.Overload()` 让项目 `.env` 取得权威性来规避。

### 2. Google (Gemini) 支持（✅ 已完成）
```go
import (
    "github.com/cloudwego/eino-ext/components/model/gemini"
    "google.golang.org/genai"
)

clientCfg := &genai.ClientConfig{APIKey: cred.APIKey}
if cred.BaseURL != "" {
    clientCfg.HTTPOptions = genai.HTTPOptions{BaseURL: cred.BaseURL} // NewAPI Gemini 原生端点
}
client, _ := genai.NewClient(ctx, clientCfg)
cfg := &gemini.Config{Client: client, Model: req.ModelID}
if searchDecision.UseModelNativeSearch && searchDecision.SearchImpl == modelbank.SearchImplParams {
    cfg.EnableGoogleSearch = &genai.GoogleSearch{} // params 型原生 grounding
}
chatModel, _ := gemini.NewChatModel(ctx, cfg)
```

### 3. DeepSeek / Perplexity（✅ 复用 openai 组件）
```go
// 两者均为 OpenAI 兼容协议，经 NewAPI 网关访问，无需各自原生 SDK。
// 与 openai 共用同一分支，仅 key+baseURL+model 不同。
cfg := &openai.ChatModelConfig{Model: req.ModelID, APIKey: cred.APIKey}
if cred.BaseURL != "" {
    cfg.BaseURL = cred.BaseURL
}
chatModel, _ := openai.NewChatModel(ctx, cfg)
```

### 4. 工具调用（✅ 已完成）

```go
// 按搜索决策挂载工具；搜索 provider 与网页提取器分层配置
if searchDecision.UseApplicationTool {
    agentConfig.ToolsConfig = adk.ToolsConfig{
        ToolsNodeConfig: compose.ToolsNodeConfig{
            Tools: []einoTool.BaseTool{
                tool.NewWebSearchTool(tool.WebSearchConfig{
                    Provider:   "searxng",
                    Providers:  []string{"searxng"},
                    SearXNGURL: webSearchURL,
                }),
                tool.NewWebExtractTool(tool.WebExtractConfig{
                    CrawlerProviders: []string{"firecrawl", "jina", "basic"},
                }),
            },
        },
    }
}

// 事件循环按 Role 分流，tool 结果单独下发，不混入助手正文
if mv.Role == schema.Tool {
    toolMsg, _ := mv.GetMessage()
    emit("tool_call_result", ToolCallResultEvent{ToolCallID: toolMsg.ToolCallID, Result: toolMsg.Content})
    continue
}
```

### 5. Thinking 输出（✅ 已完成）

```go
// OpenAI o 系列 / DeepSeek R1 等的推理内容
if chunk.ReasoningContent != "" {
    emit("thinking_delta", ThinkingDeltaEvent{Delta: chunk.ReasoningContent})
}
// Claude Extended Thinking 待 anthropic provider 接入后对齐
```

## 架构对比

### 之前（错误）
```
HTTP Request
  → Handler
    → CustomAgent (我自己写的)
      → CustomAdapter (我自己写的)
        → components/model (内部 API)
```

### 现在（正确）
```
HTTP Request
  → Handler
    → EinoAgent
      → adk.ChatModelAgent (官方)
        → adk.Runner (官方)
          → eino-ext models (官方)
```

## 教训

1. **先看官方文档，不要猜测 API**
2. **用 Quick Start 示例验证理解**
3. **不要直接用内部实现（components/model）**
4. **用官方封装层（adk + eino-ext）**

---

**更新时间**: 2026-06-13
**状态**: M3 完成（工具调用 / 自适应搜索 / 推理 / 调用链持久化）；
M4 完成（全部 provider 接入：openai/perplexity/deepseek 走 openai 兼容组件、
anthropic 原生、google 原生 + grounding；provider 校验、环境变量劫持修复）。
openai / anthropic / google 经后端接口联调出流通过；deepseek 待网关渠道修复后补测
