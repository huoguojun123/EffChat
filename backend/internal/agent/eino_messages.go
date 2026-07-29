package agent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/model"
)

func convertToEinoMessages(messages []*model.Message, visionCapable bool) ([]*schema.Message, error) {
	result := make([]*schema.Message, 0, len(messages))
	visionPlan := selectVisionImageParts(messages, visionCapable)
	latestImageMessage := latestImageMessageIndex(messages)
	for index, msg := range messages {
		m := &schema.Message{}
		if err := json.Unmarshal(msg.MessageData, m); err != nil {
			return nil, fmt.Errorf("failed to unmarshal message %d: %w", msg.ID, err)
		}
		// 历史 assistant 的 reasoning/thinking 不回传给模型：o系列/Anthropic/DeepSeek
		// 官方均说明历史推理内容不应回灌（省 token，且部分网关回灌会报错）。DB 与前端
		// 仍保留用于展示；当前轮的实时 thinking 走流式不经此路径，不受影响。
		if m.Role == schema.Assistant {
			m.ReasoningContent = ""
			if assistantMessageIsEmptyForModel(m) {
				continue
			}
		}
		// 图片附件：MessageService.prepareAttachmentsForAgent 已把图片引用记进 _image_parts。
		if m.Role == schema.User && index == latestImageMessage {
			if imgs := extractImageParts(msg.MessageData); len(imgs) > 0 {
				applyImageParts(m, imgs, visionCapable, visionPlan[index])
			}
		}
		result = append(result, m)
	}
	appendWebToolContextToLatestUser(result, messages)
	result = normalizeToolReplayHistory(result)
	return mergeConsecutiveUserMessages(result), nil
}

func assistantMessageIsEmptyForModel(m *schema.Message) bool {
	return strings.TrimSpace(m.Content) == "" &&
		len(m.ToolCalls) == 0 &&
		len(m.MultiContent) == 0 &&
		len(m.AssistantGenMultiContent) == 0
}

// normalizeToolReplayHistory 把数据库历史整理成模型 API 可接受的 tool 调用序列。
//
// OpenAI 兼容协议要求每条 role=tool 都必须紧跟在包含对应 tool_call_id 的
// assistant 消息之后。Eino/模型可以在一次 assistant 消息里并发发出多个 tool_calls，
// 但部分网关在历史回放时不接受 "assistant(多个 calls) -> 多条 tool" 的组合，
// 尤其当旧数据里 tool 结果顺序被打乱时会直接 400。
//
// 这里不修改数据库历史，只在喂回模型前做无损重排：
// assistant(call_a, call_b) + tool_b + tool_a 会变成
// assistant(call_a) + tool_a + assistant(call_b) + tool_b。
// 找不到匹配 assistant 的孤儿 tool 会被丢弃，避免旧脏数据继续污染后续对话。
func normalizeToolReplayHistory(messages []*schema.Message) []*schema.Message {
	if len(messages) == 0 {
		return messages
	}
	normalized := make([]*schema.Message, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role == schema.Tool {
			continue
		}
		if msg.Role != schema.Assistant || len(msg.ToolCalls) == 0 {
			normalized = append(normalized, msg)
			continue
		}

		next := i + 1
		tools := make([]*schema.Message, 0, len(msg.ToolCalls))
		for next < len(messages) && messages[next].Role == schema.Tool {
			tools = append(tools, messages[next])
			next++
		}
		pairs := pairToolResultsByCalls(msg.ToolCalls, tools)
		if len(pairs) == 0 {
			i = next - 1
			continue
		}

		for pairIndex, pair := range pairs {
			assistant := cloneAssistantWithToolCall(msg, pair.call)
			if pairIndex > 0 {
				assistant.Content = ""
				assistant.MultiContent = nil
				assistant.UserInputMultiContent = nil
				assistant.AssistantGenMultiContent = nil
			}
			normalized = append(normalized, assistant, pair.tool)
		}
		i = next - 1
	}
	return normalized
}

type toolReplayPair struct {
	call schema.ToolCall
	tool *schema.Message
}

func pairToolResultsByCalls(calls []schema.ToolCall, tools []*schema.Message) []toolReplayPair {
	if len(calls) == 0 || len(tools) == 0 {
		return nil
	}
	byID := make(map[string]*schema.Message, len(tools))
	for _, tool := range tools {
		if tool.ToolCallID == "" {
			continue
		}
		if _, exists := byID[tool.ToolCallID]; exists {
			continue
		}
		byID[tool.ToolCallID] = tool
	}

	pairs := make([]toolReplayPair, 0, len(calls))
	for _, call := range calls {
		if call.ID == "" {
			continue
		}
		tool, ok := byID[call.ID]
		if !ok {
			continue
		}
		pairs = append(pairs, toolReplayPair{call: call, tool: tool})
	}
	return pairs
}

func cloneAssistantWithToolCall(msg *schema.Message, call schema.ToolCall) *schema.Message {
	cloned := *msg
	call.Index = nil
	cloned.ToolCalls = []schema.ToolCall{call}
	return &cloned
}

// imagePart 是 message_data._image_parts 的一项，由 service 层注入。
type imagePart struct {
	FileID   int64  `json:"file_id"`
	FileType string `json:"file_type"`
	FilePath string `json:"file_path"`
	Filename string `json:"filename"`
	FileSize int64  `json:"file_size"`
}

func extractImageParts(raw []byte) []imagePart {
	var probe struct {
		ImageParts []imagePart `json:"_image_parts"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil
	}
	return probe.ImageParts
}

// applyImageParts 把图片读盘转 base64 塞进 m.UserInputMultiContent（vision 模型），
// 或在 content 末尾追加不支持提示（非 vision 模型 / 读盘失败 / 超限）。
func selectVisionImageParts(messages []*model.Message, visionCapable bool) map[int]map[int]bool {
	selected := make(map[int]map[int]bool)
	if !visionCapable {
		return selected
	}
	messageIndex := latestImageMessageIndex(messages)
	if messageIndex < 0 {
		return selected
	}
	remainingBytes := filepolicy.MaxVisionRequestBytes
	remainingImages := filepolicy.MaxVisionImages
	for imageIndex, image := range extractImageParts(messages[messageIndex].MessageData) {
		imageSize := visionImageSize(image)
		if imageSize <= 0 || imageSize > filepolicy.MaxVisionImageBytes || imageSize > remainingBytes {
			continue
		}
		if selected[messageIndex] == nil {
			selected[messageIndex] = make(map[int]bool)
		}
		selected[messageIndex][imageIndex] = true
		remainingImages--
		remainingBytes -= imageSize
		if remainingImages == 0 {
			break
		}
	}
	return selected
}

func latestImageMessageIndex(messages []*model.Message) int {
	for index := len(messages) - 1; index >= 0; index-- {
		var probe struct {
			Role string `json:"role"`
		}
		if json.Unmarshal(messages[index].MessageData, &probe) != nil || probe.Role != string(schema.User) {
			continue
		}
		if len(extractImageParts(messages[index].MessageData)) > 0 {
			return index
		}
		return -1
	}
	return -1
}

func visionImageSize(image imagePart) int64 {
	if image.FileSize > 0 {
		return image.FileSize
	}
	path, err := filepolicy.ExistingPath(image.FilePath)
	if err != nil {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func applyImageParts(m *schema.Message, imgs []imagePart, visionCapable bool, selected map[int]bool) {
	if !visionCapable {
		for _, img := range imgs {
			m.Content = appendNote(m.Content, fmt.Sprintf("[Image attachment %s: the current model cannot inspect images]", img.Filename))
		}
		return
	}
	parts := make([]schema.MessageInputPart, 0, len(imgs)+1)
	// 文本部分单独累积：进入 UserInputMultiContent 后 m.Content 必须清空。
	// Eino v0.9.6 已废弃 MultiContent；Gemini 新适配器只有 UserInputMultiContent
	// 能可靠携带 MIMEType 与 base64 原文。继续走旧字段会让 Google 侧拿到空 MIME，
	// 表现为请求成功但模型没有产出正式正文。
	// 失败/超限提示也并入这段文本，避免追加到 m.Content 后被丢弃。
	textPart := m.Content
	imageParts := make([]schema.MessageInputPart, 0, len(imgs))
	for index, img := range imgs {
		if !selected[index] {
			textPart = appendNote(textPart, fmt.Sprintf("[Image attachment %s was omitted from this request to keep the visual context within its limit]", img.Filename))
			continue
		}
		path, pathErr := filepolicy.ExistingPath(img.FilePath)
		if pathErr != nil {
			log.Printf("read image attachment %d blocked: %v", img.FileID, pathErr)
			textPart = appendNote(textPart, fmt.Sprintf("[Image %s could not be read and was not sent]", img.Filename))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("read image attachment %d failed: %v", img.FileID, err)
			textPart = appendNote(textPart, fmt.Sprintf("[Image %s could not be read and was not sent]", img.Filename))
			continue
		}
		if len(data) > int(filepolicy.MaxVisionImageBytes) {
			textPart = appendNote(textPart, fmt.Sprintf("[Image %s is too large and was not sent]", img.Filename))
			continue
		}
		b64 := base64.StdEncoding.EncodeToString(data)
		imageParts = append(imageParts, schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeImageURL,
			Image: &schema.MessageInputImage{
				MessagePartCommon: schema.MessagePartCommon{
					Base64Data: &b64,
					MIMEType:   img.FileType,
				},
				Detail: schema.ImageURLDetailAuto,
			},
		})
	}
	// 没有任何图片成功：保持纯文本 content（含失败提示），不触发多模态字段。
	if len(imageParts) == 0 {
		m.Content = textPart
		return
	}
	if strings.TrimSpace(textPart) != "" {
		parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: textPart})
	} else {
		parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: defaultImageOnlyPrompt(len(imageParts))})
	}
	parts = append(parts, imageParts...)
	m.Content = ""
	m.MultiContent = nil
	m.AssistantGenMultiContent = nil
	m.UserInputMultiContent = parts
}

func defaultImageOnlyPrompt(imageCount int) string {
	if imageCount > 1 {
		return "Analyze these images and answer in the conversation language. Default to Chinese."
	}
	return "Analyze this image and answer in the conversation language. Default to Chinese."
}

func appendNote(content, note string) string {
	if strings.TrimSpace(content) == "" {
		return note
	}
	return content + "\n" + note
}

// mergeConsecutiveUserMessages 合并相邻的纯 user 消息。
//
// 多数 OpenAI 兼容网关（deepseek 等）要求 user/assistant 角色严格交替；当用户连续发送
// 多条消息却没等到回复（如「你好」→「？」→「？」→「附件」），历史里就会出现连续 user 消息。
// 直接喂给模型会得到空补全（无报错），最终触发 "agent produced no assistant output"。
// 这里把连续 user 合并成一条，保证角色交替；带工具调用的消息及 system/assistant/tool
// 一律原样保留，不做合并。
//
// 多模态：连续 user 中任一带 UserInputMultiContent（图片）时，合并为单条多模态消息
// （文本部分与图片部分按顺序拼接），同样保证角色交替——否则连续 image-only turn 会
// 漏过合并、触发网关角色交替报错。
func mergeConsecutiveUserMessages(messages []*schema.Message) []*schema.Message {
	if len(messages) < 2 {
		return messages
	}
	merged := make([]*schema.Message, 0, len(messages))
	for _, m := range messages {
		if len(merged) > 0 && m.Role == schema.User && merged[len(merged)-1].Role == schema.User &&
			len(m.ToolCalls) == 0 && len(merged[len(merged)-1].ToolCalls) == 0 {
			prev := merged[len(merged)-1]
			// 纯文本路径：两条都无多模态字段，按空行拼接（保持原行为）。
			if !hasUserMultiContent(m) && !hasUserMultiContent(prev) {
				switch {
				case prev.Content == "":
					prev.Content = m.Content
				case m.Content != "":
					prev.Content = prev.Content + "\n\n" + m.Content
				}
				continue
			}
			// 多模态路径：任一带图片，合并为单条 UserInputMultiContent。
			// openai/gemini 适配器都不应同时收到 Content 与多模态字段，故 Content 必须清空。
			combined := append(userMessageParts(prev), userMessageParts(m)...)
			prev.UserInputMultiContent = combined
			prev.MultiContent = nil
			prev.Content = ""
			continue
		}
		merged = append(merged, m)
	}
	return merged
}

func hasUserMultiContent(m *schema.Message) bool {
	return len(m.UserInputMultiContent) > 0 || len(m.MultiContent) > 0
}

// userMessageParts 把一条 user 消息归一化为 UserInputMultiContent 片段：
// 新字段直接返回；旧 MultiContent 仅作为历史兼容输入转换；否则把非空 Content 包成文本片段。
func userMessageParts(m *schema.Message) []schema.MessageInputPart {
	if len(m.UserInputMultiContent) > 0 {
		return m.UserInputMultiContent
	}
	if len(m.MultiContent) > 0 {
		return legacyChatPartsToUserInputParts(m.MultiContent)
	}
	if strings.TrimSpace(m.Content) != "" {
		return []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeText, Text: m.Content}}
	}
	return nil
}

//lint:ignore SA1019 persisted pre-0.9.12 messages still require legacy MultiContent decoding
func legacyChatPartsToUserInputParts(parts []schema.ChatMessagePart) []schema.MessageInputPart {
	out := make([]schema.MessageInputPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case schema.ChatMessagePartTypeText:
			out = append(out, schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: part.Text})
		case schema.ChatMessagePartTypeImageURL:
			if part.ImageURL == nil {
				continue
			}
			inputImage := &schema.MessageInputImage{
				MessagePartCommon: schema.MessagePartCommon{MIMEType: part.ImageURL.MIMEType},
				Detail:            part.ImageURL.Detail,
			}
			switch {
			case part.ImageURL.URI != "":
				uri := part.ImageURL.URI
				inputImage.URL = &uri
			case part.ImageURL.URL != "":
				if b64, mimeType, ok := splitDataURL(part.ImageURL.URL); ok {
					inputImage.Base64Data = &b64
					if inputImage.MIMEType == "" {
						inputImage.MIMEType = mimeType
					}
				} else {
					url := part.ImageURL.URL
					inputImage.URL = &url
				}
			}
			out = append(out, schema.MessageInputPart{Type: schema.ChatMessagePartTypeImageURL, Image: inputImage})
		}
	}
	return out
}

func splitDataURL(value string) (string, string, bool) {
	if !strings.HasPrefix(value, "data:") {
		return "", "", false
	}
	comma := strings.Index(value, ",")
	if comma < 0 {
		return "", "", false
	}
	meta := strings.TrimPrefix(value[:comma], "data:")
	mimeType := strings.TrimSuffix(meta, ";base64")
	return value[comma+1:], mimeType, true
}

// messageToData 将 schema.Message 序列化为 message_data map。
// 经 JSON 往返，字段名与数据库生成列（has_tool_calls / has_reasoning）保持一致。
func messageToData(msg *schema.Message) map[string]interface{} {
	raw, err := json.Marshal(msg)
	if err != nil {
		return map[string]interface{}{"role": string(msg.Role), "content": msg.Content}
	}
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return map[string]interface{}{"role": string(msg.Role), "content": msg.Content}
	}
	sanitizeMessageData(data)
	return data
}

// sanitizeMessageData 清洗 message_data，确保不会破坏数据库生成列。
// tool_calls 只能是数组：模型回纯文本时可能给出 JSON null/对象/字符串，
// 而 v1 的 has_tool_calls 生成列会对非数组调用 jsonb_array_length 并报错，
// 导致整条 INSERT 失败、助手消息静默丢失。非数组一律删除该键（等价于无工具调用）。
func sanitizeMessageData(data map[string]interface{}) {
	if tc, ok := data["tool_calls"]; ok {
		if _, isArray := tc.([]interface{}); !isArray {
			delete(data, "tool_calls")
		}
	}
}

// canonicalizeProducedMessages 在本轮消息落库前生成同样合法的 tool 调用序列。
//
// 这一步和 normalizeToolReplayHistory 是一前一后两道防线：前者修正旧历史回放，
// 后者保证新落库的数据从源头就是合法顺序。工具仍然可以并发执行；这里只改变
// 持久化/回放形态，不把 tool runtime 强行串行化。
func canonicalizeProducedMessages(messages []map[string]interface{}) []map[string]interface{} {
	return canonicalizeProducedMessagesWithPartial(messages, false)
}

func canonicalizePartialProducedMessages(messages []map[string]interface{}) []map[string]interface{} {
	return canonicalizeProducedMessagesWithPartial(messages, true)
}

func canonicalizeProducedMessagesWithPartial(messages []map[string]interface{}, preservePartial bool) []map[string]interface{} {
	if len(messages) == 0 {
		return messages
	}
	normalized := make([]map[string]interface{}, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		role, _ := msg["role"].(string)
		if role == "tool" {
			continue
		}
		calls, ok := msg["tool_calls"].([]interface{})
		if role != "assistant" || !ok || len(calls) == 0 {
			normalized = append(normalized, msg)
			continue
		}

		next := i + 1
		tools := make([]map[string]interface{}, 0, len(calls))
		for next < len(messages) {
			nextRole, _ := messages[next]["role"].(string)
			if nextRole != "tool" {
				break
			}
			tools = append(tools, messages[next])
			next++
		}
		pairs := pairProducedToolsByCalls(calls, tools)
		if len(pairs) == 0 {
			if preservePartial {
				assistant := cloneProducedAssistantWithoutToolCalls(msg)
				if hasDisplayableAssistantOutput([]map[string]interface{}{assistant}) || assistantHasReasoning(assistant) {
					normalized = append(normalized, assistant)
				}
			}
			i = next - 1
			continue
		}

		for pairIndex, pair := range pairs {
			assistant := cloneProducedAssistantWithToolCall(msg, pair.call)
			if pairIndex > 0 {
				assistant["content"] = ""
				delete(assistant, "multi_content")
				delete(assistant, "user_input_multi_content")
				delete(assistant, "assistant_gen_multi_content")
			}
			normalized = append(normalized, assistant, pair.tool)
		}
		i = next - 1
	}
	return normalized
}

func cloneProducedAssistantWithoutToolCalls(message map[string]interface{}) map[string]interface{} {
	cloned := make(map[string]interface{}, len(message))
	for key, value := range message {
		cloned[key] = value
	}
	delete(cloned, "tool_calls")
	return cloned
}

type producedToolPair struct {
	call interface{}
	tool map[string]interface{}
}

func pairProducedToolsByCalls(calls []interface{}, tools []map[string]interface{}) []producedToolPair {
	if len(calls) == 0 || len(tools) == 0 {
		return nil
	}
	byID := make(map[string]map[string]interface{}, len(tools))
	for _, tool := range tools {
		id, _ := tool["tool_call_id"].(string)
		if id == "" {
			continue
		}
		if _, exists := byID[id]; exists {
			continue
		}
		byID[id] = tool
	}
	pairs := make([]producedToolPair, 0, len(calls))
	for _, call := range calls {
		callMap, ok := call.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := callMap["id"].(string)
		if id == "" {
			continue
		}
		tool, ok := byID[id]
		if !ok {
			continue
		}
		pairs = append(pairs, producedToolPair{call: call, tool: tool})
	}
	return pairs
}

func cloneProducedAssistantWithToolCall(msg map[string]interface{}, call interface{}) map[string]interface{} {
	cloned := make(map[string]interface{}, len(msg))
	for key, value := range msg {
		cloned[key] = value
	}
	cloned["tool_calls"] = []interface{}{call}
	return cloned
}
