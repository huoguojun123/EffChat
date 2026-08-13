package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/repository"
)

var (
	ErrInvalidMessageInput      = errors.New("invalid message input")
	ErrMessageTooLarge          = errors.New("message is too large")
	ErrTooManyAttachments       = errors.New("too many attachments")
	ErrRetryTargetStale         = repository.ErrRetryTargetStale
	ErrMessageAlreadyAnswered   = repository.ErrMessageAlreadyAnswered
	ErrMessageUnchanged         = repository.ErrMessageUnchanged
	ErrChatRunActive            = repository.ErrChatRunActive
	ErrConversationTurnNotFound = errors.New("conversation turn not found")
	ErrCompactionNotFound       = repository.ErrCompactionNotFound
	ErrCompactionUndoDenied     = repository.ErrCompactionUndoDenied
	ErrCompactionUndoStale      = repository.ErrCompactionUndoStale
)

const (
	MaxMessageRequestBytes = 1 << 20
	MaxMessageContentRunes = 250_000
	MaxClientRunIDBytes    = 128
	MaxMessageAttachments  = 10
)

type MessageService struct {
	messageRepo       *repository.MessageRepository
	sessionRepo       *repository.SessionRepository
	fileRepo          *repository.FileRepository
	answerAttemptRepo *repository.AnswerAttemptRepository
}

func NewMessageService(messageRepo *repository.MessageRepository, sessionRepo *repository.SessionRepository, fileRepo *repository.FileRepository, answerAttemptRepo *repository.AnswerAttemptRepository) *MessageService {
	return &MessageService{
		messageRepo:       messageRepo,
		sessionRepo:       sessionRepo,
		fileRepo:          fileRepo,
		answerAttemptRepo: answerAttemptRepo,
	}
}

func normalizeMessageThinkingEffort(effort string) string {
	normalized := modelbank.NormalizeThinkingEffort(effort)
	if normalized == string(modelbank.ThinkingEffortAuto) || !modelbank.IsValidThinkingEffort(normalized) {
		return ""
	}
	return normalized
}

type SendMessageRequest struct {
	Content        string  `json:"content"`        // 可为空：允许仅附件消息（图片/文件），由 CreateUserMessage 校验
	SchemaVersion  string  `json:"schema_version"` // v1 or v2, 默认使用会话的 message_format
	ClientRunID    string  `json:"client_run_id"`
	Attachments    []int64 `json:"attachments"`     // 文件 id 列表，文本正文由 file_read 工具按需读取
	ThinkingEffort string  `json:"thinking_effort"` // 本轮 thinking 强度；具体含义由模型 thinking_format 决定
}

type MessageResponse struct {
	ID               int64                    `json:"id"`
	SessionID        int64                    `json:"session_id"`
	SchemaVersion    string                   `json:"schema_version"`
	Role             string                   `json:"role"`
	MessageData      map[string]interface{}   `json:"message_data"`
	HasToolCalls     bool                     `json:"has_tool_calls"`
	HasReasoning     bool                     `json:"has_reasoning"`
	AnswerAttemptID  *int64                   `json:"answer_attempt_id,omitempty"`
	AnswerNavigation *AnswerAttemptNavigation `json:"answer_navigation,omitempty"`
	CreatedAt        string                   `json:"created_at"`
}

type ConversationTurnResponse struct {
	ID            int64  `json:"id"`
	Sequence      int64  `json:"sequence"`
	UserMessageID int64  `json:"user_message_id"`
	UserPreview   string `json:"user_preview"`
	CreatedAt     string `json:"created_at"`
}

type ConversationTurnPage struct {
	Turns            []ConversationTurnResponse `json:"turns"`
	Total            int64                      `json:"total"`
	HasMore          bool                       `json:"has_more"`
	NextBeforeTurnID *int64                     `json:"next_before_turn_id"`
}

type MessageWindowResponse struct {
	Messages    []*MessageResponse `json:"messages"`
	FirstTurnID int64              `json:"first_turn_id"`
	LastTurnID  int64              `json:"last_turn_id"`
	HasOlder    bool               `json:"has_older"`
	HasNewer    bool               `json:"has_newer"`
}

type AnswerAttemptNavigation struct {
	AttemptID         int64  `json:"attempt_id"`
	AttemptNumber     int    `json:"attempt_number"`
	AttemptCount      int    `json:"attempt_count"`
	PreviousAttemptID *int64 `json:"previous_attempt_id,omitempty"`
	NextAttemptID     *int64 `json:"next_attempt_id,omitempty"`
	CanSwitch         bool   `json:"can_switch"`
}

// CreateUserMessage 创建用户消息
func (s *MessageService) CreateUserMessage(sessionID, userID int64, req *SendMessageRequest) (*model.Message, error) {
	return s.CreateUserMessageContext(context.Background(), sessionID, userID, req)
}

func (s *MessageService) CreateUserMessageContext(ctx context.Context, sessionID, userID int64, req *SendMessageRequest) (*model.Message, error) {
	// 验证会话权限
	session, err := s.sessionRepo.GetByIDContext(ctx, sessionID, userID)
	if err != nil {
		return nil, fmt.Errorf("session not found or access denied")
	}
	message, err := s.buildUserMessage(ctx, session, userID, req)
	if err != nil {
		return nil, err
	}

	if err := s.messageRepo.CreateForActiveSession(ctx, sessionID, userID, message); err != nil {
		clientRunID := strings.TrimSpace(req.ClientRunID)
		if clientRunID != "" && repository.IsUniqueViolation(err) {
			existing, findErr := s.messageRepo.FindByRunIDContext(ctx, sessionID, clientRunID, []string{"user"})
			if findErr != nil {
				return nil, findErr
			}
			if len(existing) > 0 {
				return existing[0], nil
			}
		}
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	return message, nil
}

func (s *MessageService) BuildUserMessagePreview(sessionID, userID int64, req *SendMessageRequest) (*model.Message, error) {
	return s.BuildUserMessagePreviewContext(context.Background(), sessionID, userID, req)
}

func (s *MessageService) BuildUserMessagePreviewContext(ctx context.Context, sessionID, userID int64, req *SendMessageRequest) (*model.Message, error) {
	session, err := s.sessionRepo.GetByIDContext(ctx, sessionID, userID)
	if err != nil {
		return nil, sessionLookupError(err)
	}
	return s.buildUserMessage(ctx, session, userID, req)
}

func (s *MessageService) buildUserMessage(ctx context.Context, session *model.Session, userID int64, req *SendMessageRequest) (*model.Message, error) {
	if err := validateSendMessageInput(req); err != nil {
		return nil, err
	}
	// 确定消息格式版本
	schemaVersion := req.SchemaVersion
	if schemaVersion == "" {
		schemaVersion = session.MessageFormat
	}

	// 验证格式版本
	if schemaVersion != "v1" && schemaVersion != "v2" {
		return nil, fmt.Errorf("%w: schema_version must be v1 or v2", ErrInvalidMessageInput)
	}

	clientRunID := strings.TrimSpace(req.ClientRunID)
	// 构造 message_data (Eino Message v1 格式)
	messageData := map[string]interface{}{
		"role":    "user",
		"content": req.Content,
	}
	metadata := map[string]interface{}{}
	if clientRunID != "" {
		metadata["run_id"] = clientRunID
	}
	if effort := normalizeMessageThinkingEffort(req.ThinkingEffort); effort != "" {
		metadata["thinking_effort"] = effort
	}
	if len(metadata) > 0 {
		attachMessageMetadata(messageData, metadata)
	}

	// 附件：把元数据(file_id/filename/type/size/token)写进独立的 attachments 数组。
	// 不把 extracted_text 写进消息体，也不在这里拼进 content；大文件如果每轮都全文
	// 进入上下文，会很快触发压缩阈值，并且让模型在不需要文件时也承担无意义 token。
	// 后续 ListForAgent 只会追加一段短清单，真正正文由 agent 调用 file_read 工具读取。
	// 这里同时统计「真正进入上下文的有效附件数」，用于下方归一化层的契约校验。
	validAttachments := 0
	if len(req.Attachments) > 0 && s.fileRepo != nil {
		files, ferr := s.fileRepo.GetStagedFilesForSessionContext(ctx, userID, session.ID, req.Attachments)
		if ferr != nil {
			return nil, fmt.Errorf("failed to load attachments: %w", ferr)
		}
		atts := make([]map[string]interface{}, 0, len(req.Attachments))
		seen := make(map[int64]struct{}, len(req.Attachments))
		var imageBytes int64
		imageCount := 0
		for _, fid := range req.Attachments {
			if fid <= 0 {
				return nil, fmt.Errorf("%w: attachment is invalid", ErrInvalidMessageInput)
			}
			if _, duplicate := seen[fid]; duplicate {
				continue
			}
			seen[fid] = struct{}{}
			f, ok := files[fid]
			if !ok {
				return nil, fmt.Errorf("%w: attachment is not available in this conversation", ErrInvalidMessageInput)
			}
			if !strings.HasPrefix(f.FileType, "image/") && strings.TrimSpace(f.ExtractStatus) != "ready" {
				return nil, fmt.Errorf("%w: 文件“%s”仍在解析或解析失败，请等待完成后发送，或重试解析", ErrInvalidMessageInput, f.FileName)
			}
			if strings.HasPrefix(f.FileType, "image/") {
				imageCount++
				imageBytes += f.FileSize
				if f.FileSize > filepolicy.MaxVisionImageBytes || imageCount > filepolicy.MaxVisionImages || imageBytes > filepolicy.MaxVisionRequestBytes {
					return nil, fmt.Errorf("%w: 图片总量超过视觉输入限制，请压缩或分批发送", ErrMessageTooLarge)
				}
			}
			atts = append(atts, map[string]interface{}{
				"file_id":        f.ID,
				"filename":       f.FileName,
				"file_type":      f.FileType,
				"size":           f.FileSize,
				"token_estimate": f.TokenEstimate,
			})
		}
		validAttachments = len(atts)
		if len(atts) > 0 {
			messageData["attachments"] = atts
		}
	}

	// 契约校验（归一化结果层）：放开 content 的 required binding 后，在此兜底——
	// 文字与「有效附件」至少有其一。注意要看解析后真正有效的附件数，而非请求里的 id 数：
	// 全是越权/不存在 id 时会被全部跳过，仍是空 turn，必须挡住。
	if strings.TrimSpace(req.Content) == "" && validAttachments == 0 {
		return nil, fmt.Errorf("%w: message must have content or attachments", ErrInvalidMessageInput)
	}

	messageDataBytes, err := json.Marshal(messageData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal message data: %w", err)
	}

	message := &model.Message{
		SessionID:     session.ID,
		SchemaVersion: schemaVersion,
		MessageData:   messageDataBytes,
	}
	return message, nil
}

func validateSendMessageInput(req *SendMessageRequest) error {
	if req == nil {
		return fmt.Errorf("%w: request is required", ErrInvalidMessageInput)
	}
	if len([]rune(req.Content)) > MaxMessageContentRunes {
		return fmt.Errorf("%w: content exceeds %d characters", ErrMessageTooLarge, MaxMessageContentRunes)
	}
	if len(req.ClientRunID) > MaxClientRunIDBytes {
		return fmt.Errorf("%w: client_run_id is too long", ErrInvalidMessageInput)
	}
	if len(req.Attachments) > MaxMessageAttachments {
		return fmt.Errorf("%w: maximum is %d", ErrTooManyAttachments, MaxMessageAttachments)
	}
	return nil
}

// CreateAssistantMessage 创建 AI 回复消息
func (s *MessageService) CreateAssistantMessage(sessionID, userID int64, messageData map[string]interface{}, schemaVersion string) (*model.Message, error) {
	return s.CreateAssistantMessageContext(context.Background(), sessionID, userID, messageData, schemaVersion)
}

func (s *MessageService) CreateAssistantMessageContext(ctx context.Context, sessionID, userID int64, messageData map[string]interface{}, schemaVersion string) (*model.Message, error) {
	// 验证会话权限
	_, err := s.sessionRepo.GetByIDContext(ctx, sessionID, userID)
	if err != nil {
		return nil, fmt.Errorf("session not found or access denied")
	}

	messageDataBytes, err := json.Marshal(messageData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal message data: %w", err)
	}

	message := &model.Message{
		SessionID:     sessionID,
		SchemaVersion: schemaVersion,
		MessageData:   messageDataBytes,
	}

	if err := s.messageRepo.CreateForActiveSession(ctx, sessionID, userID, message); err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	return message, nil
}

// CreateErrorAssistantMessage 持久化失败提示消息。
// 这类消息用于前端展示，但不会回灌到下一次模型上下文里。
func (s *MessageService) CreateErrorAssistantMessage(sessionID, userID int64, content, schemaVersion string) (*model.Message, error) {
	return s.CreateErrorAssistantMessageContext(context.Background(), sessionID, userID, content, schemaVersion)
}

func (s *MessageService) CreateErrorAssistantMessageContext(ctx context.Context, sessionID, userID int64, content, schemaVersion string) (*model.Message, error) {
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("error content is required")
	}

	return s.CreateAssistantMessageContext(ctx, sessionID, userID, map[string]interface{}{
		"role":    "assistant",
		"content": content,
		"metadata": map[string]interface{}{
			"ephemeral_error": true,
		},
	}, schemaVersion)
}

// PersistAgentMessages 按顺序持久化 Agent 本轮产生的全部消息
// （assistant 可能带 tool_calls、tool 结果、最终 assistant）。
// 权限只校验一次，role/has_tool_calls 等由数据库生成列从 message_data 派生。
// 整批在单事务内写入：任一条失败整体回滚，不留半截对话。
func (s *MessageService) PersistAgentMessages(sessionID, userID int64, messages []map[string]interface{}, schemaVersion, runID string) ([]*model.Message, error) {
	return s.PersistAgentMessagesContext(context.Background(), sessionID, userID, messages, schemaVersion, runID)
}

func (s *MessageService) PersistAgentMessagesContext(ctx context.Context, sessionID, userID int64, messages []map[string]interface{}, schemaVersion, runID string) ([]*model.Message, error) {
	if _, err := s.sessionRepo.GetByIDContext(ctx, sessionID, userID); err != nil {
		return nil, fmt.Errorf("session not found or access denied")
	}
	runID = strings.TrimSpace(runID)
	if runID != "" {
		existing, err := s.messageRepo.FindByRunIDContext(ctx, sessionID, runID, []string{"assistant", "tool"})
		if err != nil {
			return nil, err
		}
		if len(existing) > 0 {
			return existing, nil
		}
	}
	saved, err := buildAgentMessages(sessionID, messages, schemaVersion, runID)
	if err != nil {
		return nil, err
	}
	if err := s.messageRepo.CreateBatchForActiveRun(ctx, sessionID, userID, runID, saved); err != nil {
		if runID != "" && repository.IsUniqueViolation(err) {
			existing, findErr := s.messageRepo.FindByRunIDContext(ctx, sessionID, runID, []string{"assistant", "tool"})
			if findErr != nil {
				return nil, findErr
			}
			if len(existing) > 0 {
				return existing, nil
			}
		}
		return nil, err
	}
	return saved, nil
}

func (s *MessageService) PersistAgentMessagesAndTransitionContext(ctx context.Context, sessionID, userID int64, messages []map[string]interface{}, schemaVersion, runID string, input repository.ChatRunTransitionInput) ([]*model.Message, repository.ChatRunRecord, bool, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, repository.ChatRunRecord{}, false, fmt.Errorf("run id is required")
	}
	saved, err := buildAgentMessages(sessionID, messages, schemaVersion, runID)
	if err != nil {
		return nil, repository.ChatRunRecord{}, false, err
	}
	record, transitioned, err := s.messageRepo.CreateBatchAndTransitionActiveRun(ctx, sessionID, userID, runID, saved, input)
	if err != nil {
		return nil, repository.ChatRunRecord{}, false, err
	}
	return saved, record, transitioned, nil
}

func buildAgentMessages(sessionID int64, messages []map[string]interface{}, schemaVersion, runID string) ([]*model.Message, error) {
	saved := make([]*model.Message, 0, len(messages))
	for i, data := range messages {
		if runID != "" {
			attachMessageMetadata(data, map[string]interface{}{
				"run_id":        runID,
				"run_sequence":  i,
				"produced_role": data["role"],
			})
		}
		dataBytes, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal message %d: %w", i, err)
		}
		saved = append(saved, &model.Message{
			SessionID:     sessionID,
			SchemaVersion: schemaVersion,
			MessageData:   dataBytes,
		})
	}
	return saved, nil
}

func attachMessageMetadata(data map[string]interface{}, patch map[string]interface{}) {
	meta, ok := data["metadata"].(map[string]interface{})
	if !ok {
		meta = map[string]interface{}{}
		if raw, ok := data["metadata"].(map[string]string); ok {
			for key, value := range raw {
				meta[key] = value
			}
		}
	}
	for key, value := range patch {
		meta[key] = value
	}
	data["metadata"] = meta
}

// ListBySessionPaged 游标分页获取展示用消息，返回 (本页消息, 是否还有更早的消息)。
// beforeID<=0 取最新一页；否则取更早一页。消息按时间升序返回。
func (s *MessageService) ListBySessionPaged(sessionID, userID int64, limit int, beforeID int64) ([]*MessageResponse, bool, error) {
	if _, err := s.sessionRepo.GetByID(sessionID, userID); err != nil {
		return nil, false, sessionLookupError(err)
	}

	messages, hasMore, err := s.messageRepo.ListBySessionPaged(sessionID, limit, beforeID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to list messages: %w", err)
	}

	result, err := s.messageResponses(context.Background(), messages)
	if err != nil {
		return nil, false, err
	}
	return result, hasMore, nil
}

func (s *MessageService) ListConversationTurns(sessionID, userID int64, limit int, beforeTurnID int64) (*ConversationTurnPage, error) {
	if _, err := s.sessionRepo.GetByID(sessionID, userID); err != nil {
		return nil, sessionLookupError(err)
	}
	turns, total, hasMore, err := s.messageRepo.ListConversationTurns(sessionID, limit, beforeTurnID)
	if err != nil {
		return nil, err
	}
	page := &ConversationTurnPage{
		Turns:   make([]ConversationTurnResponse, 0, len(turns)),
		Total:   total,
		HasMore: hasMore,
	}
	for _, turn := range turns {
		page.Turns = append(page.Turns, ConversationTurnResponse{
			ID:            turn.ID,
			Sequence:      turn.Sequence,
			UserMessageID: turn.ID,
			UserPreview:   conversationTurnPreview(turn.Content),
			CreatedAt:     turn.CreatedAt.Format(time.RFC3339),
		})
	}
	if hasMore && len(page.Turns) > 0 {
		next := page.Turns[0].ID
		page.NextBeforeTurnID = &next
	}
	return page, nil
}

func (s *MessageService) ListMessageWindow(sessionID, userID int64, mode repository.MessageWindowMode, targetTurnID int64, turnLimit int) (*MessageWindowResponse, error) {
	if _, err := s.sessionRepo.GetByID(sessionID, userID); err != nil {
		return nil, sessionLookupError(err)
	}
	window, err := s.messageRepo.ListMessageWindow(sessionID, mode, targetTurnID, turnLimit)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrConversationTurnNotFound
		}
		return nil, err
	}
	messages, err := s.messageResponses(context.Background(), window.Messages)
	if err != nil {
		return nil, err
	}
	return &MessageWindowResponse{
		Messages:    messages,
		FirstTurnID: window.FirstTurnID,
		LastTurnID:  window.LastTurnID,
		HasOlder:    window.HasOlder,
		HasNewer:    window.HasNewer,
	}, nil
}

var (
	turnCodeFencePattern = regexp.MustCompile("(?s)```.*?```")
	turnImagePattern     = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	turnLinkPattern      = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	turnMarkupPattern    = regexp.MustCompile(`[*_~` + "`" + `>#-]+`)
)

func conversationTurnPreview(content string) string {
	content = turnCodeFencePattern.ReplaceAllString(content, " [代码] ")
	content = turnImagePattern.ReplaceAllString(content, " [图片] ")
	content = turnLinkPattern.ReplaceAllString(content, "$1")
	content = turnMarkupPattern.ReplaceAllString(content, " ")
	content = strings.Join(strings.Fields(content), " ")
	if content == "" {
		return "空消息"
	}
	runes := []rune(content)
	if len(runes) > 110 {
		runes = runes[:110]
	}
	for len([]byte(string(runes))) > 140 {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}

func (s *MessageService) SelectAnswerAttempt(ctx context.Context, sessionID, userID, attemptID int64) (*repository.AnswerAttempt, error) {
	if s.answerAttemptRepo == nil {
		return nil, fmt.Errorf("answer attempt selection is unavailable")
	}
	return s.answerAttemptRepo.SelectForActiveSession(ctx, sessionID, userID, attemptID)
}

func (s *MessageService) DeleteAnswerAttempt(ctx context.Context, sessionID, userID, attemptID int64) (*repository.AnswerAttemptDeletion, error) {
	if s.answerAttemptRepo == nil {
		return nil, fmt.Errorf("answer attempt deletion is unavailable")
	}
	return s.answerAttemptRepo.DeleteForActiveSession(ctx, sessionID, userID, attemptID)
}

func (s *MessageService) messageResponses(ctx context.Context, messages []*model.Message) ([]*MessageResponse, error) {
	result := make([]*MessageResponse, len(messages))
	lastAssistantMessageByAttempt := make(map[int64]int64)
	attemptIDs := make([]int64, 0)
	seenAttemptIDs := make(map[int64]struct{})

	for i, msg := range messages {
		var messageData map[string]interface{}
		if err := json.Unmarshal(msg.MessageData, &messageData); err != nil {
			return nil, fmt.Errorf("failed to unmarshal message data: %w", err)
		}
		result[i] = &MessageResponse{
			ID:              msg.ID,
			SessionID:       msg.SessionID,
			SchemaVersion:   msg.SchemaVersion,
			Role:            msg.Role,
			MessageData:     messageData,
			HasToolCalls:    msg.HasToolCalls,
			HasReasoning:    msg.HasReasoning,
			AnswerAttemptID: msg.AnswerAttemptID,
			CreatedAt:       msg.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
		if msg.Role != "assistant" || msg.AnswerAttemptID == nil {
			continue
		}
		attemptID := *msg.AnswerAttemptID
		lastAssistantMessageByAttempt[attemptID] = msg.ID
		if _, seen := seenAttemptIDs[attemptID]; !seen {
			seenAttemptIDs[attemptID] = struct{}{}
			attemptIDs = append(attemptIDs, attemptID)
		}
	}

	if s.answerAttemptRepo == nil || len(attemptIDs) == 0 {
		return result, nil
	}
	navigation, err := s.answerAttemptRepo.NavigationForAttemptIDs(ctx, attemptIDs)
	if err != nil {
		return nil, err
	}
	for _, response := range result {
		if response.Role != "assistant" || response.AnswerAttemptID == nil {
			continue
		}
		attemptID := *response.AnswerAttemptID
		if lastAssistantMessageByAttempt[attemptID] != response.ID {
			continue
		}
		nav, ok := navigation[attemptID]
		if !ok || nav.AttemptCount < 2 || !nav.CanSwitch {
			continue
		}
		response.AnswerNavigation = &AnswerAttemptNavigation{
			AttemptID:         nav.ID,
			AttemptNumber:     nav.AttemptNumber,
			AttemptCount:      nav.AttemptCount,
			PreviousAttemptID: nav.PreviousID,
			NextAttemptID:     nav.NextID,
			CanSwitch:         nav.CanSwitch,
		}
	}
	return result, nil
}

// ListForAgent 获取 agent 上下文消息（检查点式：摘要 + 未压缩消息）
func (s *MessageService) ListForAgent(sessionID, userID int64) ([]*model.Message, error) {
	return s.ListForAgentContext(context.Background(), sessionID, userID)
}

func (s *MessageService) ListForAgentContext(ctx context.Context, sessionID, userID int64) ([]*model.Message, error) {
	return s.listForAgentContext(ctx, sessionID, userID, 0, nil)
}

func (s *MessageService) listForAgentContext(ctx context.Context, sessionID, userID, preserveUserMessageID int64, replacement *model.Message) ([]*model.Message, error) {
	_, err := s.sessionRepo.GetByIDContext(ctx, sessionID, userID)
	if err != nil {
		return nil, sessionLookupError(err)
	}

	messages, err := s.messageRepo.ListBySessionContext(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	filtered := make([]*model.Message, 0, len(messages))
	for _, msg := range messages {
		if isEphemeralErrorMessage(msg) {
			if size := len(filtered); size > 0 && filtered[size-1].Role == "user" && filtered[size-1].ID != preserveUserMessageID {
				filtered = filtered[:size-1]
			}
			continue
		}
		if replacement != nil && msg.ID == replacement.ID {
			cloned := *replacement
			cloned.MessageData = append([]byte(nil), replacement.MessageData...)
			filtered = append(filtered, &cloned)
			continue
		}
		filtered = append(filtered, msg)
	}

	// 准备附件上下文：文本附件只追加短清单，引导模型在需要时调用 file_read；
	// 图片附件仍写入 _image_parts，供 agent 层按 vision 能力转成多模态输入。
	s.prepareAttachmentsForAgent(ctx, userID, sessionID, filtered)

	return filtered, nil
}

func (s *MessageService) ListForRetryAgentContext(ctx context.Context, sessionID, userID, targetMessageID int64) ([]*model.Message, error) {
	_, messages, err := s.RetryAgentContext(ctx, sessionID, userID, targetMessageID)
	return messages, err
}

func (s *MessageService) RetryAgentContext(ctx context.Context, sessionID, userID, targetMessageID int64) (*model.Message, []*model.Message, error) {
	retryUser, err := s.PrepareRetryContext(ctx, sessionID, userID, targetMessageID)
	if err != nil {
		return nil, nil, err
	}
	messages, err := s.listForAgentContext(ctx, sessionID, userID, retryUser.ID, nil)
	if err != nil {
		return nil, nil, err
	}
	messages, err = messagesThroughID(messages, retryUser.ID)
	if err != nil {
		return nil, nil, err
	}
	return retryUser, messages, nil
}

func (s *MessageService) EditRetryAgentContext(ctx context.Context, sessionID, userID, targetMessageID int64, content, clientRunID string) (*model.Message, []*model.Message, error) {
	source, err := s.messageRepo.PrepareEditRetryForActiveSession(ctx, sessionID, userID, targetMessageID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, nil, ErrSessionNotFound
		}
		return nil, nil, err
	}
	replacement, err := buildEditedRetryMessage(source, content, clientRunID)
	if err != nil {
		return nil, nil, err
	}
	messages, err := s.listForAgentContext(ctx, sessionID, userID, source.ID, replacement)
	if err != nil {
		return nil, nil, err
	}
	messages, err = messagesThroughID(messages, source.ID)
	if err != nil {
		return nil, nil, err
	}
	return replacement, messages, nil
}

func buildEditedRetryMessage(source *model.Message, content, clientRunID string) (*model.Message, error) {
	if source == nil || source.ID <= 0 || source.Role != "user" {
		return nil, ErrRetryTargetStale
	}
	if len([]rune(content)) > MaxMessageContentRunes {
		return nil, fmt.Errorf("%w: content exceeds %d characters", ErrMessageTooLarge, MaxMessageContentRunes)
	}
	clientRunID = strings.TrimSpace(clientRunID)
	if clientRunID == "" || len(clientRunID) > MaxClientRunIDBytes {
		return nil, fmt.Errorf("%w: client_run_id is invalid", ErrInvalidMessageInput)
	}
	data, err := repository.ParseMessageData(source.MessageData)
	if err != nil {
		return nil, err
	}
	originalContent, _ := data["content"].(string)
	if content == originalContent {
		return nil, ErrMessageUnchanged
	}
	attachments, err := attachmentIDsFromMessagePayload(data)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(content) == "" && len(attachments) == 0 {
		return nil, fmt.Errorf("%w: message must have content or attachments", ErrInvalidMessageInput)
	}
	data["content"] = content
	metadata, _ := data["metadata"].(map[string]interface{})
	if metadata == nil {
		metadata = map[string]interface{}{}
		data["metadata"] = metadata
	}
	metadata["run_id"] = clientRunID
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal edited retry message: %w", err)
	}
	replacement := *source
	replacement.MessageData = raw
	replacement.CreatedAt = time.Time{}
	replacement.UpdatedAt = time.Time{}
	replacement.DeletedAt = nil
	return &replacement, nil
}

func attachmentIDsFromMessagePayload(data map[string]interface{}) ([]int64, error) {
	rawAttachments, ok := data["attachments"].([]interface{})
	if !ok || len(rawAttachments) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(rawAttachments))
	seen := make(map[int64]struct{}, len(rawAttachments))
	for _, rawAttachment := range rawAttachments {
		attachment, ok := rawAttachment.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("%w: attachment is invalid", ErrInvalidMessageInput)
		}
		id, ok := toInt64(attachment["file_id"])
		if !ok {
			return nil, fmt.Errorf("%w: attachment is invalid", ErrInvalidMessageInput)
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *MessageService) ListForAgentThroughMessageContext(ctx context.Context, sessionID, userID, messageID int64) ([]*model.Message, error) {
	messages, err := s.ListForAgentContext(ctx, sessionID, userID)
	if err != nil {
		return nil, err
	}
	return messagesThroughID(messages, messageID)
}

func (s *MessageService) ListForCompactionBeforeMessageContext(ctx context.Context, sessionID, userID, preserveMessageID int64) ([]*model.Message, error) {
	if preserveMessageID <= 0 {
		return nil, ErrRetryTargetStale
	}
	messages, err := s.ListForAgentContext(ctx, sessionID, userID)
	if err != nil {
		return nil, err
	}
	var latestUserID int64
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message == nil || message.Role != "user" || isCompactionSummaryMessage(message) {
			continue
		}
		latestUserID = message.ID
		break
	}
	if latestUserID != preserveMessageID {
		return nil, ErrRetryTargetStale
	}
	compactionMessages := make([]*model.Message, 0, len(messages))
	for _, message := range messages {
		if message != nil && message.ID < preserveMessageID {
			compactionMessages = append(compactionMessages, message)
		}
	}
	return compactionMessages, nil
}

func messagesThroughID(messages []*model.Message, messageID int64) ([]*model.Message, error) {
	result := make([]*model.Message, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		result = append(result, message)
		if message.ID == messageID {
			return result, nil
		}
	}
	return nil, ErrRetryTargetStale
}

func (s *MessageService) ListForAgentWithDraft(sessionID, userID int64, req *SendMessageRequest) ([]*model.Message, error) {
	return s.ListForAgentWithDraftContext(context.Background(), sessionID, userID, req)
}

func (s *MessageService) ListForAgentWithDraftContext(ctx context.Context, sessionID, userID int64, req *SendMessageRequest) ([]*model.Message, error) {
	messages, err := s.ListForAgentContext(ctx, sessionID, userID)
	if err != nil {
		return nil, err
	}
	draft, err := s.BuildUserMessagePreviewContext(ctx, sessionID, userID, req)
	if err != nil {
		return nil, err
	}
	s.prepareStagedAttachmentsForPreflight(ctx, userID, sessionID, []*model.Message{draft})
	messages = append(messages, draft)
	return messages, nil
}

func (s *MessageService) CountUserMessagesSince(sessionID, userID int64, since time.Time) (int, error) {
	if _, err := s.sessionRepo.GetByID(sessionID, userID); err != nil {
		return 0, fmt.Errorf("session not found or access denied")
	}
	messages, err := s.messageRepo.ListAllBySession(sessionID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, msg := range messages {
		if msg == nil || msg.Role != "user" || isCompactionSummaryMessage(msg) || isEphemeralErrorMessage(msg) {
			continue
		}
		if since.IsZero() || msg.CreatedAt.After(since) {
			count++
		}
	}
	return count, nil
}

func (s *MessageService) RecentConversationTextForMemory(sessionID, userID int64, userLimit int) (string, error) {
	return s.RecentConversationTextForMemoryContext(context.Background(), sessionID, userID, userLimit)
}

func (s *MessageService) RecentConversationTextForMemoryContext(ctx context.Context, sessionID, userID int64, userLimit int) (string, error) {
	if _, err := s.sessionRepo.GetByIDContext(ctx, sessionID, userID); err != nil {
		return "", fmt.Errorf("session not found or access denied")
	}
	if userLimit <= 0 {
		userLimit = 5
	}
	messages, err := s.messageRepo.ListAllBySessionContext(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return RecentConversationTextForMemoryMessages(messages, userLimit), nil
}

func RecentConversationTextForMemoryMessages(messages []*model.Message, userLimit int) string {
	start := 0
	seenUsers := 0
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg == nil || msg.Role != "user" || isCompactionSummaryMessage(msg) || isEphemeralErrorMessage(msg) {
			continue
		}
		seenUsers++
		start = i
		if seenUsers >= userLimit {
			break
		}
	}
	var b strings.Builder
	for _, msg := range messages[start:] {
		if msg == nil || isCompactionSummaryMessage(msg) || isEphemeralErrorMessage(msg) {
			continue
		}
		data, err := repository.ParseMessageData(msg.MessageData)
		if err != nil {
			continue
		}
		role, _ := data["role"].(string)
		if role == "" {
			role = msg.Role
		}
		if role != "user" && role != "assistant" {
			continue
		}
		content, _ := data["content"].(string)
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(role)
		b.WriteString(" @ ")
		b.WriteString(msg.CreatedAt.Format(time.RFC3339))
		b.WriteString(":\n")
		b.WriteString(truncateMemoryContext(content, 1600))
	}
	return b.String()
}

// prepareAttachmentsForAgent 就地改写“发给模型的消息副本”：
//   - 文本/PDF/Office 等已提取正文的附件，不再把 extracted_text 全文塞进 content。
//     这里只追加一个很短的附件清单，让模型知道有哪些 file_id 可用；当它确实需要
//     文件正文时，再通过 file_read 工具按需读取片段。
//   - 图片附件继续写入 _image_parts，由 agent 层根据模型 vision 能力转成 UserInputMultiContent，
//     或给非 vision 模型追加“不支持读图”的降级说明。
//
// 这段逻辑只改内存里的消息副本，不改数据库原始 message_data。失败时静默跳过：
// 附件属于上下文增强能力，不应该因为某个文件元数据异常而阻断正常对话。
func (s *MessageService) prepareAttachmentsForAgent(ctx context.Context, userID, sessionID int64, messages []*model.Message) {
	s.prepareAttachments(ctx, userID, sessionID, messages, false)
}

func (s *MessageService) prepareStagedAttachmentsForPreflight(ctx context.Context, userID, sessionID int64, messages []*model.Message) {
	s.prepareAttachments(ctx, userID, sessionID, messages, true)
}

func (s *MessageService) prepareAttachments(ctx context.Context, userID, sessionID int64, messages []*model.Message, staged bool) {
	if s.fileRepo == nil {
		return
	}
	// 收集所有引用到的 file_id
	type ref struct {
		msgIdx      int
		fileID      int64
		filename    string
		fileType    string
		unavailable bool
	}
	var refs []ref
	idSet := map[int64]struct{}{}
	parsed := make([]map[string]interface{}, len(messages))
	for i, msg := range messages {
		var data map[string]interface{}
		if err := json.Unmarshal(msg.MessageData, &data); err != nil {
			continue
		}
		parsed[i] = data
		atts, ok := data["attachments"].([]interface{})
		if !ok || len(atts) == 0 {
			continue
		}
		for _, a := range atts {
			am, ok := a.(map[string]interface{})
			if !ok {
				continue
			}
			fid, ok := toInt64(am["file_id"])
			if !ok {
				continue
			}
			filename, _ := am["filename"].(string)
			fileType, _ := am["file_type"].(string)
			unavailable, _ := am["unavailable"].(bool)
			refs = append(refs, ref{msgIdx: i, fileID: fid, filename: filename, fileType: fileType, unavailable: unavailable})
			idSet[fid] = struct{}{}
		}
	}
	if len(idSet) == 0 {
		return
	}
	ids := make([]int64, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	var files map[int64]*model.File
	var err error
	if staged {
		files, err = s.fileRepo.GetStagedFilesForSessionContext(ctx, userID, sessionID, ids)
	} else {
		files, err = s.fileRepo.GetFormalFilesForSessionContext(ctx, userID, sessionID, ids)
	}
	if err != nil {
		return
	}

	// 按消息聚合：文本附件只生成短清单；图片附件不进 content，单独记到 _image_parts。
	notesByMsg := map[int][]string{}
	imagesByMsg := map[int][]map[string]interface{}{}
	unavailableByMsg := map[int][]string{}
	for _, r := range refs {
		f, ok := files[r.fileID]
		if r.unavailable || !ok {
			unavailableByMsg[r.msgIdx] = append(unavailableByMsg[r.msgIdx], formatUnavailableAttachmentListItem(r.fileID, r.filename, r.fileType))
			continue
		}
		if strings.HasPrefix(f.FileType, "image/") {
			imagesByMsg[r.msgIdx] = append(imagesByMsg[r.msgIdx], map[string]interface{}{
				"file_id":   f.ID,
				"file_type": f.FileType,
				"file_path": f.FilePath,
				"filename":  f.FileName,
				"file_size": f.FileSize,
			})
			continue
		}
		notesByMsg[r.msgIdx] = append(notesByMsg[r.msgIdx], formatAttachmentListItem(f))
	}
	for idx := range parsed {
		data := parsed[idx]
		if data == nil {
			continue
		}
		notes := notesByMsg[idx]
		images := imagesByMsg[idx]
		unavailable := unavailableByMsg[idx]
		if len(notes) == 0 && len(images) == 0 && len(unavailable) == 0 {
			continue
		}
		if len(notes) > 0 || len(unavailable) > 0 {
			content, _ := data["content"].(string)
			data["content"] = appendAttachmentListNote(content, append(notes, unavailable...))
		}
		if len(images) > 0 {
			data["_image_parts"] = images
		}
		if b, err := json.Marshal(data); err == nil {
			messages[idx].MessageData = b
		}
	}
}

func formatAttachmentListItem(f *model.File) string {
	status := strings.TrimSpace(f.ExtractStatus)
	if status == "" {
		status = "unknown"
	}
	tokenPart := ""
	if f.TokenEstimate > 0 {
		tokenPart = fmt.Sprintf(" tokens≈%d", f.TokenEstimate)
	}
	errorPart := ""
	if f.ExtractError != nil && strings.TrimSpace(*f.ExtractError) != "" {
		errorPart = fmt.Sprintf(" error=%q", strings.TrimSpace(*f.ExtractError))
	}
	return fmt.Sprintf("- file_id=%d filename=%q type=%s size=%d%s status=%s%s",
		f.ID, f.FileName, f.FileType, f.FileSize, tokenPart, status, errorPart)
}

func formatUnavailableAttachmentListItem(fileID int64, filename, fileType string) string {
	if strings.TrimSpace(filename) == "" {
		filename = "unknown"
	}
	if strings.TrimSpace(fileType) == "" {
		fileType = "unknown"
	}
	return fmt.Sprintf("- file_id=%d filename=%q type=%s status=unavailable (the original attachment was deleted; do not infer its contents)", fileID, filename, fileType)
}

func appendAttachmentListNote(content string, items []string) string {
	note := "[Attachment list]\n" +
		strings.Join(items, "\n") +
		"\nWhen the answer depends on attachment text that is not already visible, use file_search with focused entity/section/claim/table terms, then use file_read(file_id, query or offset/next_offset) to read only the needed passages. Do not infer document contents from filenames and do not assume full attachment text is already in context.\n" +
		"[/Attachment list]"
	if strings.TrimSpace(content) == "" {
		return note
	}
	return content + "\n\n" + note
}

func toInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case float64:
		if n <= 0 || n != math.Trunc(n) || n >= float64(1<<63) {
			return 0, false
		}
		return int64(n), true
	case int64:
		return n, n > 0
	case int:
		return int64(n), n > 0
	case json.Number:
		i, err := n.Int64()
		return i, err == nil && i > 0
	case string:
		if n == "" {
			return 0, false
		}
		for _, r := range n {
			if r < '0' || r > '9' {
				return 0, false
			}
		}
		i, err := strconv.ParseInt(n, 10, 64)
		return i, err == nil && i > 0
	}
	return 0, false
}

func (s *MessageService) PrepareRetry(sessionID, userID, targetMessageID int64) (*model.Message, error) {
	return s.PrepareRetryContext(context.Background(), sessionID, userID, targetMessageID)
}

func (s *MessageService) PrepareRetryContext(ctx context.Context, sessionID, userID, targetMessageID int64) (*model.Message, error) {
	message, err := s.messageRepo.PrepareRetryForActiveSession(ctx, sessionID, userID, targetMessageID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	return message, nil
}

// PersistCompressionCheckpoint 持久化压缩检查点：
//  1. 将摘要消息存为新消息（role=user, schema_version=v1）
//  2. 将 beforeMessageID 之前的所有消息标记为已压缩（compressed_at + compression_summary_id）
//
// Agent context 的 ListBySession 会过滤已压缩消息，只返回摘要 + 检查点之后的新消息；
// UI 历史窗口使用独立的全历史查询，压缩前消息始终可查看。
// 压缩检查点来源：auto=对话流自动触发，manual=用户 /compact 手动触发。
// 前端据 compaction_kind 决定是否显示撤销按钮（自动压缩不显示）。
const (
	CompactionKindAuto   = "auto"
	CompactionKindManual = "manual"
)

func (s *MessageService) PersistCompressionCheckpoint(sessionID, userID int64, summaryData []byte, beforeMessageID int64, kind string) error {
	return s.PersistCompressionCheckpointContext(context.Background(), sessionID, userID, summaryData, beforeMessageID, kind)
}

func (s *MessageService) PersistCompressionCheckpointContext(ctx context.Context, sessionID, userID int64, summaryData []byte, beforeMessageID int64, kind string) error {
	return s.persistCompressionCheckpointContext(ctx, sessionID, userID, summaryData, beforeMessageID, kind)
}

func (s *MessageService) PersistCompressionCheckpointAndTransitionContext(ctx context.Context, sessionID, userID int64, runID string, summaryData []byte, beforeMessageID int64, kind string, input repository.ChatRunTransitionInput, expectedAnswerSelectionRevision *int64) (repository.ChatRunRecord, bool, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return repository.ChatRunRecord{}, false, fmt.Errorf("run id is required")
	}
	summaryData = markCompactionSummary(summaryData, kind, beforeMessageID)
	summaryMsg := &model.Message{SessionID: sessionID, SchemaVersion: "v1", MessageData: summaryData}
	record, transitioned, err := s.messageRepo.PersistCheckpointAndTransitionActiveRun(ctx, sessionID, userID, runID, summaryMsg, beforeMessageID, input, expectedAnswerSelectionRevision)
	if err != nil {
		return repository.ChatRunRecord{}, false, fmt.Errorf("failed to persist compression checkpoint: %w", err)
	}
	return record, transitioned, nil
}

func (s *MessageService) persistCompressionCheckpointContext(ctx context.Context, sessionID, userID int64, summaryData []byte, beforeMessageID int64, kind string) error {
	if _, err := s.sessionRepo.GetByIDContext(ctx, sessionID, userID); err != nil {
		return fmt.Errorf("session not found or access denied")
	}

	// 注入 metadata.compaction_summary 标记：摘要消息以 role=user 落库，
	// 前端据此把它渲染成"以上对话已压缩"分割线，而非普通用户气泡。
	// kind 区分来源（auto/manual）：自动压缩前端不显示撤销按钮。
	summaryData = markCompactionSummary(summaryData, kind, beforeMessageID)

	summaryMsg := &model.Message{
		SessionID:     sessionID,
		SchemaVersion: "v1",
		MessageData:   summaryData,
	}
	// 摘要写入 + 旧消息标记同事务，避免中途崩溃留下孤立摘要或未标记旧消息。
	if err := s.messageRepo.PersistCheckpointForActiveSession(ctx, sessionID, userID, summaryMsg, beforeMessageID); err != nil {
		return fmt.Errorf("failed to persist compression checkpoint: %w", err)
	}

	return nil
}

// markCompactionSummary 在摘要 message_data 的 metadata 中打上压缩类型和逻辑位置。
// 解析失败时原样返回，保证压缩主流程不因标记失败而中断。
func markCompactionSummary(summaryData []byte, kind string, beforeMessageID int64) []byte {
	var data map[string]interface{}
	if err := json.Unmarshal(summaryData, &data); err != nil {
		return summaryData
	}
	meta, ok := data["metadata"].(map[string]interface{})
	if !ok {
		meta = map[string]interface{}{}
	}
	meta["compaction_summary"] = true
	if kind != "" {
		meta["compaction_kind"] = kind
	}
	if beforeMessageID > 0 {
		meta["compaction_before_message_id"] = beforeMessageID
	}
	data["metadata"] = meta
	if out, err := json.Marshal(data); err == nil {
		return out
	}
	return summaryData
}

// UndoLastCompaction 撤销会话最近一次压缩检查点：恢复被压消息、软删摘要。
// 找不到可撤销的压缩摘要时返回错误。返回恢复的消息条数。
func (s *MessageService) UndoLastCompaction(sessionID, userID int64) (int64, error) {
	restored, err := s.messageRepo.UndoLatestManualCheckpointForActiveSession(context.Background(), sessionID, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return 0, ErrSessionNotFound
	}
	return restored, err
}

// isCompactionSummaryMessage 判断消息是否为压缩摘要（metadata.compaction_summary=true）。
func isCompactionSummaryMessage(msg *model.Message) bool {
	_, ok := compactionSummaryKind(msg)
	return ok
}

func compactionSummaryKind(msg *model.Message) (string, bool) {
	if msg == nil {
		return "", false
	}
	var data map[string]interface{}
	if err := json.Unmarshal(msg.MessageData, &data); err != nil {
		return "", false
	}
	meta, ok := data["metadata"].(map[string]interface{})
	if !ok {
		return "", false
	}
	flag, ok := meta["compaction_summary"].(bool)
	if !ok || !flag {
		return "", false
	}
	kind, _ := meta["compaction_kind"].(string)
	return strings.TrimSpace(kind), true
}

func isEphemeralErrorMessage(msg *model.Message) bool {
	var data map[string]interface{}
	if err := json.Unmarshal(msg.MessageData, &data); err != nil {
		return false
	}
	meta, ok := data["metadata"].(map[string]interface{})
	if !ok {
		return false
	}
	flag, ok := meta["ephemeral_error"].(bool)
	return ok && flag
}

func truncateMemoryContext(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "\n[truncated]"
}
