package agent

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
)

const compactionInstruction = `Create a detailed continuation summary for this conversation. The goal is that future turns can continue smoothly even after the original messages are removed.

First, use the <analysis> tag to audit coverage. Then put the final stored summary inside the <summary> tag only.

The final <summary> must be written in Chinese by default and use these seven numbered sections:

1. 用户的主要诉求与意图：Record every explicit user request, goal, constraint, and preference that still matters.
2. 关键概念与话题：Preserve the important topics, facts, viewpoints, and terms discussed.
3. 关键信息与素材：Preserve useful tool results, web evidence, URLs, uploaded file facts, document findings, code/file references, and why they matter.
4. 已解决的问题与进行中的事项：Separate completed work from active or partially completed work.
5. 待办事项：List clear unfinished tasks, bugs, decisions, or verification steps.
6. 当前进展：State the exact latest state of the conversation and what the assistant was doing most recently.
7. 可选的下一步：List only next actions directly relevant to the user's latest intent. Quote short original wording when it prevents ambiguity.

Rules:
- Do not call tools.
- Do not add text outside <analysis> and <summary>.
- Be dense and specific. Do not replace concrete details with vague phrases like "discussed the project".
- Preserve user decisions and corrections even if they contradicted an earlier plan.
- Preserve enough source identifiers, filenames, URLs, IDs, and offsets for future turns to recover evidence.
- Do not include transient frustration unless it changes a durable preference or decision.
- Do not claim work was completed unless the conversation clearly shows it was completed.`

const compactionPreamble = `This conversation continues from a compressed checkpoint. Use the summary below as context and continue naturally; do not restate the summary itself.`

// recentVerbatimCount 摘要后附带的最近原始消息条数，降低近期信息损失。
const recentVerbatimCount = 10

// compactionSummaryMaxChars 是压缩摘要落库前的硬上限。
//
// 压缩的目的就是降低后续上下文体积；如果模型生成的摘要过长，宁可保留前部结构化信息
// 并追加截断提示，也不能让“摘要”再次成为触发压缩的巨大消息。
const compactionSummaryMaxChars = 24000

const (
	contextSafetyMarginDivisor   = 20
	minContextSafetyMarginTokens = 2048
	maxContextSafetyMarginTokens = 16384
)

type compressionModelProfile struct {
	Model    einoModel.ToolCallingChatModel
	Provider string
	ModelID  string
}

func (a *EinoAgent) compressionContextThreshold() int {
	if a == nil {
		return 0
	}
	compressMaxTokens := a.compressMaxTokens
	if a.configRepo != nil {
		compressMaxTokens = a.configRepo.GetInt("compression_context_threshold", compressMaxTokens)
	}
	return compressMaxTokens
}

func (a *EinoAgent) compressionThresholdForRequest(req *ChatRequest) int {
	globalCeiling := a.compressionContextThreshold()
	if globalCeiling <= 0 || req == nil || req.ContextWindow <= 0 {
		return globalCeiling
	}

	outputReserve := req.MaxTokens
	if outputReserve <= 0 {
		outputReserve = req.ModelMaxOutput
	}
	safetyMargin := req.ContextWindow / contextSafetyMarginDivisor
	if safetyMargin < minContextSafetyMarginTokens {
		safetyMargin = minContextSafetyMarginTokens
	}
	if safetyMargin > maxContextSafetyMarginTokens {
		safetyMargin = maxContextSafetyMarginTokens
	}
	modelCeiling := req.ContextWindow - outputReserve - safetyMargin
	if modelCeiling < 1 {
		modelCeiling = 1
	}
	if modelCeiling < globalCeiling {
		return modelCeiling
	}
	return globalCeiling
}

func (a *EinoAgent) buildCompressionModel(ctx context.Context, fallback einoModel.ToolCallingChatModel, fallbackProvider, fallbackModelID string) compressionModelProfile {
	return compressionModelProfile{Model: fallback, Provider: fallbackProvider, ModelID: fallbackModelID}
}

func (a *EinoAgent) NeedsPreCompaction(ctx context.Context, req *ChatRequest) (bool, int, int, error) {
	threshold := a.compressionThresholdForRequest(req)
	if threshold <= 0 || req == nil {
		return false, 0, threshold, nil
	}
	visionCapable := req.Vision
	if !req.RuntimeResolved {
		visionCapable = modelbank.GetOrDefault(req.ModelID, req.Provider).Capabilities.Vision
	}
	history, err := convertToEinoMessages(req.Messages, visionCapable)
	if err != nil {
		return false, 0, threshold, err
	}
	plan := a.buildRuntimeContextPlan(req)
	instruction, err := buildInstruction(a.configRepo, req, plan.searchDecision, plan.mountedTools)
	if err != nil {
		return false, 0, threshold, err
	}
	if req.MemoryEnabled && a.memoryRepo != nil && req.SessionID > 0 {
		if req.RuntimeMemory != nil {
			instruction = appendMemoryInstruction(instruction, *req.RuntimeMemory)
		} else if mem, err := a.memoryRepo.Get(req.SessionID); err == nil {
			instruction = appendMemoryInstruction(instruction, mem)
		}
	}
	tokens := countContextTokens(history, estimateTextTokens(instruction)+plan.schemaTokenCost)
	return tokens >= threshold, tokens, threshold, nil
}

// countContextTokens 估算压缩判定用的上下文 token 数。
//
// 取两者较大值，避免上游 usage 缺失或本地估算偏低各自的盲区：
//   - usage 基线：最后一条 assistant 的 response_meta.usage.total_tokens（含当轮系统提示），
//     叠加其后新消息的本地估算增量——与 eino 默认计数器口径一致，但不依赖其内部实现。
//   - 本地估算：系统提示 + 全部会话消息逐条估算之和——上游完全不回传 usage 时的兜底，
//     确保"系统提示很大但历史文本不长"时也能反映真实上下文规模。
func countContextTokens(messages []*schema.Message, instructionTokens int) int {
	baseTokens, incrementStart := 0, 0
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m != nil && m.Role == schema.Assistant && m.ResponseMeta != nil &&
			m.ResponseMeta.Usage != nil && m.ResponseMeta.Usage.TotalTokens > 0 {
			baseTokens = m.ResponseMeta.Usage.TotalTokens
			incrementStart = i + 1
			break
		}
	}
	usageBased := baseTokens
	for _, m := range messages[incrementStart:] {
		usageBased += estimateMessageTokens(m)
	}

	localEstimate := instructionTokens
	for _, m := range messages {
		localEstimate += estimateMessageTokens(m)
	}

	if usageBased > localEstimate {
		return usageBased
	}
	return localEstimate
}

// estimateMessageTokens 估算单条消息的 token 数（含正文、多模态输入与工具调用）。
func estimateMessageTokens(m *schema.Message) int {
	if m == nil {
		return 0
	}
	chars := len(m.Content) + len(m.ReasoningContent)
	for _, tc := range m.ToolCalls {
		chars += len(tc.Function.Name) + len(tc.Function.Arguments)
	}
	return estimateCharTokens(chars) + estimateUserInputMultiContentTokens(m.UserInputMultiContent)
}

const (
	// multimodalInputBaseTokens 是多模态附件进入模型上下文时的保守固定估算。
	//
	// 不直接按 Base64Data 长度计数：图片在 API 侧通常按视觉 token / patch 计费，
	// 不是把 base64 字符串当普通文本；如果把 base64 原文全算进去，一张图就会被
	// 误判成数万 token，反而导致压缩过早触发。这里用固定成本承认“图片确实占上下文”，
	// 再把同一 part 的文本、URL、MIME、文件名等可见元数据按文本估算补进去。
	multimodalInputBaseTokens = 1024
)

func estimateUserInputMultiContentTokens(parts []schema.MessageInputPart) int {
	total := 0
	for _, part := range parts {
		total += estimateCharTokens(len(part.Text))
		if part.Image != nil {
			total += estimateCommonInputPartTokens(part.Image.MessagePartCommon)
		}
		if part.Audio != nil {
			total += estimateCommonInputPartTokens(part.Audio.MessagePartCommon)
		}
		if part.Video != nil {
			total += estimateCommonInputPartTokens(part.Video.MessagePartCommon)
		}
		if part.File != nil {
			total += multimodalInputBaseTokens
			total += estimateCharTokens(len(part.File.MIMEType) + len(part.File.Name))
			if part.File.URL != nil {
				total += estimateCharTokens(len(*part.File.URL))
			}
			// Base64 文件内容可能很大，不能把编码后的字符数当成文本 token。
			if part.File.Base64Data != nil {
				total += estimateCharTokens(min(len(*part.File.Base64Data), 512))
			}
		}
	}
	return total
}

func estimateCommonInputPartTokens(common schema.MessagePartCommon) int {
	total := multimodalInputBaseTokens + estimateCharTokens(len(common.MIMEType))
	if common.URL != nil {
		total += estimateCharTokens(len(*common.URL))
	}
	if common.Base64Data != nil {
		// 只采样极短前缀作为“有二进制输入”的元数据成本，避免 base64 字符串放大估算。
		total += estimateCharTokens(min(len(*common.Base64Data), 512))
	}
	return total
}

// estimateTextTokens 估算一段纯文本的 token 数。
func estimateTextTokens(s string) int { return estimateCharTokens(len(s)) }

// estimateCharTokens 按字节长度估算 token：偏保守地用 ~3 字节/token，
// 比 eino 默认的 4 字节/token 更贴近中英混排（中文 UTF-8 多为 3 字节/字、约 1 token/字）。
func estimateCharTokens(byteLen int) int {
	if byteLen <= 0 {
		return 0
	}
	return (byteLen + 2) / 3
}

// extractSummarySection 从模型输出中抽取 <summary>...</summary> 正文；
// 无标签时退回去掉 <analysis> 块后的全文，再退回原文（保证永不返回空）。
func extractSummarySection(s string) string {
	lower := strings.ToLower(s)
	if open := strings.Index(lower, "<summary>"); open >= 0 {
		rest := s[open+len("<summary>"):]
		restLower := strings.ToLower(rest)
		if c := strings.Index(restLower, "</summary>"); c >= 0 {
			if inner := strings.TrimSpace(rest[:c]); inner != "" {
				return inner
			}
		}
		if inner := strings.TrimSpace(rest); inner != "" {
			return inner
		}
	}
	if open := strings.Index(lower, "<analysis>"); open >= 0 {
		restLower := lower[open:]
		if c := strings.Index(restLower, "</analysis>"); c >= 0 {
			stripped := strings.TrimSpace(s[:open] + s[open+c+len("</analysis>"):])
			if stripped != "" {
				return stripped
			}
		}
	}
	// Some reasoning-capable gateways move the opening <analysis> portion into
	// their reasoning field but leak its closing tag and final draft tail into
	// ordinary content. Treat only the leading prefix before that orphan close
	// as reasoning; the remainder is still passed through the summary extractor
	// so a following provider envelope is normalized as usual.
	if close := strings.Index(lower, "</analysis>"); close >= 0 {
		if stripped := strings.TrimSpace(s[close+len("</analysis>"):]); stripped != "" {
			return extractSummarySection(stripped)
		}
	}
	return s
}

// buildCompactionSummaryMessage 把模型生成的摘要草稿转换为真正要持久化的检查点消息。
//
// 发送前 preflight 与手动压缩都调用同一条 checkpoint 摘要路径：
//  1. 丢弃 <analysis>，只保留 <summary>；
//  2. 加统一续接前言，告诉后续模型这是历史检查点；
//  3. 附最近原始消息，补偿摘要模型可能漏掉的短期状态；
//  4. 给消息打 eino summary 标记，便于 repository/前端识别检查点。
func buildCompactionSummaryMessage(rawSummary string, messages []*model.Message) *schema.Message {
	body := buildCompactionSummaryBody(rawSummary, messages)
	msg := schema.UserMessage(body)
	msg.Extra = map[string]any{
		"_eino_summarization_content_type": "summary",
	}
	return msg
}

func buildCompactionSummaryBody(rawSummary string, messages []*model.Message) string {
	summaryText := strings.TrimSpace(extractSummarySection(strings.TrimSpace(rawSummary)))
	if summaryText == "" {
		summaryText = "本次压缩未能生成有效摘要。请根据最近的原始对话继续。"
	}

	body := compactionPreamble + "\n\n" + summaryText
	if recent := renderRecentVerbatim(messages, recentVerbatimCount); recent != "" {
		body += "\n\n--- 最近的原始对话（保留原文以供参考）---\n\n" + recent
	}
	if len([]rune(body)) <= compactionSummaryMaxChars {
		return body
	}
	runes := []rune(body)
	truncated := string(runes[:compactionSummaryMaxChars])
	log.Printf("[eino] 压缩摘要超过上限，已裁剪: chars=%d limit=%d", len(runes), compactionSummaryMaxChars)
	return truncated + "\n\n[系统提示：压缩摘要因长度超过上限已截断。]"
}

// renderRecentVerbatim 把最近 n 条消息渲染成「角色: 正文」的纯文本块，附在摘要后。
// 仅取 user/assistant 的文本内容；空内容（如纯工具调用消息）跳过，避免噪声。
func renderRecentVerbatim(messages []*model.Message, n int) string {
	if len(messages) == 0 || n <= 0 {
		return ""
	}
	start := len(messages) - n
	if start < 0 {
		start = 0
	}
	var sb strings.Builder
	for _, msg := range messages[start:] {
		var data struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(msg.MessageData, &data); err != nil {
			continue
		}
		content := strings.TrimSpace(data.Content)
		if content == "" {
			continue
		}
		label := data.Role
		switch data.Role {
		case "user":
			label = "用户"
		case "assistant":
			label = "助手"
		case "tool":
			label = "工具结果"
		}
		sb.WriteString(label)
		sb.WriteString("：")
		sb.WriteString(content)
		sb.WriteString("\n\n")
	}
	return strings.TrimSpace(sb.String())
}
