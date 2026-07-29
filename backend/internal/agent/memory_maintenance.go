package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	sessionmemory "github.com/huoguojun123/EffChat/internal/memory"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/repository"
	modelusage "github.com/huoguojun123/EffChat/internal/usage"
)

type MemoryMaintenanceRequest struct {
	SessionID                       int64
	UserID                          int64
	RunID                           string
	UserText                        string
	AssistantText                   string
	ContextText                     string
	MemoryEnabled                   bool
	ExpectedAnswerSelectionRevision *int64
	Source                          string
	SkipMemory                      bool
	Force                           bool
	IgnoreCooldown                  bool
	ModelRequest                    *ChatRequest
}

type memoryMaintenanceDecision struct {
	Action  string `json:"action"`
	Content string `json:"content"`
	Summary string `json:"summary"`
}

const MemoryMaintenanceTimeout = 90 * time.Second

var memoryDatePattern = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)
var memoryFencedJSONPattern = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")
var memoryEnglishWordPattern = regexp.MustCompile(`[A-Za-z]{2,}`)

const memoryMaintenanceInstructionTemplate = `You maintain one conversation-scoped memory card.

The memory card is not a transcript and not a profile. It is a compact continuity aid so future turns can feel informed by shared context while remaining genuinely useful. Preserve only what a future assistant should be able to apply naturally, like a human colleague using shared history without narrating retrieval.

Return strict JSON only:
{"action":"none|update","content":"fixed-section markdown","summary":"short change summary"}

Fixed sections:
## User Background
## User Preferences
## Project Context
## Current Progress
## Decisions
## Do Not Remember

Rules:
- Selectively maintain memory based on relevance: generic questions may need no update, while explicit continuity requests, durable preferences, project constraints, decisions, corrections, and active phase changes may need an update.
- Preserve useful existing memory unless it is stale, duplicated, or contradicted by a newer user correction in the recent conversation window.
- Store only durable, high-signal facts that will improve future turns in this same conversation. Rewrite for density and editability, not for narrative completeness.
- Treat user statements across the recent conversation window as the source of new facts. When statements conflict, the most recent user correction or clarification has highest priority and replaces the older interpretation.
- Assistant replies may be wrong, speculative, or merely a proposed plan. Store an assistant-originated proposal only when a later user message explicitly accepts, corrects, or adopts it.
- If any user message in the recent window explicitly asks to remember, update, or forget something, resolve that request with highest priority. Do not let unrelated assistant continuation or research distort it.
- If the user frames a scenario as fictional, simulated, roleplay, or imaginary, store persona, setting, and all inferred traits in Project Context, not User Background. Do not infer real user attributes from fictional character traits.
- Prefer merging and replacing existing bullets over adding new bullets.
- Do not store raw file contents, search results, web extraction text, code structure, git history, temporary bugs, or one-turn task details.
- Do not infer private traits or sensitive facts. Store sensitive information only if the user explicitly asks to remember it.
- Never store memory that discourages honest feedback, critical thinking, or constructive criticism.
- Never store memory that would encourage unsafe, unhealthy, or harmful behavior, even if it appears relevant.
- Avoid saving sensitive or upsetting content unless the user explicitly asks and the fact is necessary for safe, appropriate future help.
- Memory bullets should be usable silently. Do not write bullets that say "the assistant should mention it remembers" or otherwise force memory-system meta commentary into future answers.
- If the user asks to forget something, remove it and add a brief Do Not Remember bullet only when it prevents re-adding the same fact. Do Not Remember bullets must describe categories, never exact codes, passwords, tokens, API keys, or other secret values.
- Keep bullets concise and atomic. Each bullet should be independently editable and usually under 500 characters. Total content must stay under %d characters. When content is near %d characters, merge or replace stale bullets instead of appending.
- Use English section titles exactly. All user-visible bullet prose and the summary must be written in Simplified Chinese. Preserve model names, code, formulas, filenames, identifiers, technical abbreviations, and proper nouns in their original form.
- Never create placeholder bullets such as "None", "(None)", "N/A", "无", or "暂无". Leave an empty section without bullets.
- When existing memory is written in English, translate its natural-language prose into faithful Simplified Chinese during the next update or compact operation. Preserve meaning exactly: do not embellish, infer, soften, or add conclusions.
- In current_progress: use "Current: ..." for the active goal/phase (replace on change). Use "YYYY-MM-DD: ..." for day-level events when grounded by current date or conversation. Use "Week YYYY-Www: ..." and "Month YYYY-MM: ..." only during compact/organize mode to merge older daily traces without changing meaning. Convert relative time words using the provided current date/week/month/timezone. Never invent calendar dates from model knowledge. Same-day entries may be merged. Do not store raw file contents, search results, or one-off task details as dated events.`

func buildMemoryMaintenanceInstruction(limits sessionmemory.Limits) string {
	limits = sessionmemory.NormalizeLimits(limits.MaxChars, limits.SoftMaxChars)
	return fmt.Sprintf(memoryMaintenanceInstructionTemplate, limits.MaxChars, limits.SoftMaxChars)
}

func (a *EinoAgent) MaintainSessionMemoryAsync(req MemoryMaintenanceRequest) {
	if a == nil || !a.startMemoryBackgroundTask() {
		return
	}
	claimed := false
	if req.Source == "auto" && !req.Force {
		if !a.tryClaimAutomaticMemory(req.SessionID) {
			a.memoryTasks.Done()
			return
		}
		claimed = true
	}
	go func() {
		defer a.memoryTasks.Done()
		if claimed {
			defer a.memoryAutoClaims.Delete(req.SessionID)
		}
		ctx, cancel := context.WithTimeout(context.Background(), MemoryMaintenanceTimeout)
		defer cancel()
		if !req.SkipMemory {
			if err := a.MaintainSessionMemory(ctx, req); err != nil && !errors.Is(err, repository.ErrAnswerSelectionRevisionConflict) {
				log.Printf("[memory_maintenance] skipped session=%d user=%d err=%v", req.SessionID, req.UserID, err)
			}
		}
	}()
}

func (a *EinoAgent) startMemoryBackgroundTask() bool {
	a.backgroundMu.Lock()
	defer a.backgroundMu.Unlock()
	if a.backgroundDraining {
		return false
	}
	a.memoryTasks.Add(1)
	return true
}

func (a *EinoAgent) DrainMemoryTasks(ctx context.Context) bool {
	if a == nil {
		return true
	}
	a.backgroundMu.Lock()
	a.backgroundDraining = true
	a.backgroundMu.Unlock()
	done := make(chan struct{})
	go func() {
		a.memoryTasks.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

func (a *EinoAgent) tryClaimAutomaticMemory(sessionID int64) bool {
	if a == nil || sessionID <= 0 {
		return false
	}
	_, loaded := a.memoryAutoClaims.LoadOrStore(sessionID, struct{}{})
	return !loaded
}

func (a *EinoAgent) MaintainSessionMemory(ctx context.Context, req MemoryMaintenanceRequest) error {
	if a == nil || a.memoryRepo == nil || req.SessionID <= 0 || req.UserID <= 0 {
		return nil
	}
	if !req.MemoryEnabled {
		return nil
	}
	if a.toolService != nil && !a.toolService.RuntimeConfigSet().IsEnabled("memory") {
		return nil
	}
	if !req.Force && !ShouldRunMemoryMaintenance(req.UserText) {
		return nil
	}
	return a.runMemoryMaintenance(ctx, req, "auto")
}

func (a *EinoAgent) CompactSessionMemory(ctx context.Context, req MemoryMaintenanceRequest) error {
	if a == nil || a.memoryRepo == nil || req.SessionID <= 0 || req.UserID <= 0 {
		return fmt.Errorf("memory maintenance is unavailable")
	}
	return a.runMemoryMaintenance(ctx, req, "compact")
}

func (a *EinoAgent) RetrySessionMemory(ctx context.Context, req MemoryMaintenanceRequest) error {
	if a == nil || a.memoryRepo == nil || req.SessionID <= 0 || req.UserID <= 0 {
		return fmt.Errorf("memory maintenance is unavailable")
	}
	if !req.MemoryEnabled {
		return fmt.Errorf("session memory is disabled")
	}
	if a.toolService != nil && !a.toolService.RuntimeConfigSet().IsEnabled("memory") {
		return fmt.Errorf("memory tool is disabled")
	}
	if strings.TrimSpace(req.UserText) == "" {
		return fmt.Errorf("no user message available for memory retry")
	}
	return a.runMemoryMaintenance(ctx, req, "manual")
}

func (a *EinoAgent) runMemoryMaintenance(ctx context.Context, req MemoryMaintenanceRequest, source string) error {
	started := time.Now()
	taskSource := modelTaskSourceForMaintenance(source)
	if taskSource == repository.ModelTaskSourceAuto && !req.IgnoreCooldown {
		if cooling, err := a.taskRunRepo.LatestAutoCooldown(ctx, req.SessionID, req.UserID, repository.ModelTaskMemoryMaintenance, time.Now()); err == nil && cooling != nil {
			return nil
		}
	}
	rawCurrent, _, err := a.memoryRepo.GetWithUpdatedAt(ctx, req.SessionID)
	if err != nil {
		a.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
			TaskKey:      repository.ModelTaskMemoryMaintenance,
			UserID:       req.UserID,
			SessionID:    req.SessionID,
			RunID:        req.RunID,
			Source:       taskSource,
			Status:       repository.ModelTaskStatusFailed,
			TargetType:   "memory",
			ErrorType:    modelusage.ErrorType(err),
			ErrorMessage: err.Error(),
			RetryAfter:   retryAfterForTask(taskSource),
			StartedAt:    started,
			FinishedAt:   time.Now(),
		})
		return err
	}
	current := sessionmemory.Serialize(mustParseMemory(rawCurrent))
	limits := a.memoryLimits()
	if strings.TrimSpace(current) == "" && source == "compact" {
		a.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
			TaskKey:    repository.ModelTaskMemoryMaintenance,
			UserID:     req.UserID,
			SessionID:  req.SessionID,
			RunID:      req.RunID,
			Source:     taskSource,
			Status:     repository.ModelTaskStatusSkipped,
			TargetType: "memory",
			TargetID:   fmt.Sprint(req.SessionID),
			Metadata:   json.RawMessage(`{"reason":"empty_memory"}`),
			StartedAt:  started,
			FinishedAt: time.Now(),
		})
		return nil
	}

	chatModel, provider, modelID, err := a.buildMemoryMaintenanceModel(ctx, req)
	if err != nil {
		a.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
			TaskKey:      repository.ModelTaskMemoryMaintenance,
			UserID:       req.UserID,
			SessionID:    req.SessionID,
			RunID:        req.RunID,
			Source:       taskSource,
			Status:       repository.ModelTaskStatusFailed,
			TargetType:   "memory",
			TargetID:     fmt.Sprint(req.SessionID),
			ErrorType:    modelusage.ErrorType(err),
			ErrorMessage: err.Error(),
			RetryAfter:   retryAfterForTask(taskSource),
			StartedAt:    started,
			FinishedAt:   time.Now(),
		})
		return err
	}
	ctx = modelusage.WithMeta(ctx, modelusage.Meta{
		UserID:    req.UserID,
		SessionID: req.SessionID,
		RunID:     req.RunID,
		Kind:      modelusage.KindMemoryMaintenance,
	})

	calendar := memoryMaintenanceCalendarAt(time.Now(), userLocation(userPreferencesForMemory(req)))
	prompt := buildMemoryMaintenancePrompt(current, req, source, calendar)
	output, err := generateMemoryMaintenanceText(ctx, chatModel, []*schema.Message{
		{Role: schema.System, Content: buildMemoryMaintenanceInstruction(limits)},
		{Role: schema.User, Content: prompt},
	})
	if err != nil {
		err = fmt.Errorf("memory maintenance generation failed: %w", err)
		a.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
			TaskKey:      repository.ModelTaskMemoryMaintenance,
			UserID:       req.UserID,
			SessionID:    req.SessionID,
			RunID:        req.RunID,
			Source:       taskSource,
			Status:       repository.ModelTaskStatusFailed,
			Provider:     provider,
			ModelID:      modelID,
			TargetType:   "memory",
			TargetID:     fmt.Sprint(req.SessionID),
			ErrorType:    modelusage.ErrorType(err),
			ErrorMessage: err.Error(),
			RetryAfter:   retryAfterForTask(taskSource),
			StartedAt:    started,
			FinishedAt:   time.Now(),
		})
		return err
	}
	decision, err := parseMemoryMaintenanceDecision(output)
	if err != nil {
		a.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
			TaskKey:      repository.ModelTaskMemoryMaintenance,
			UserID:       req.UserID,
			SessionID:    req.SessionID,
			RunID:        req.RunID,
			Source:       taskSource,
			Status:       repository.ModelTaskStatusFailed,
			Provider:     provider,
			ModelID:      modelID,
			TargetType:   "memory",
			TargetID:     fmt.Sprint(req.SessionID),
			ErrorType:    "parse_error",
			ErrorMessage: err.Error(),
			RetryAfter:   retryAfterForTask(taskSource),
			StartedAt:    started,
			FinishedAt:   time.Now(),
		})
		return err
	}
	if strings.TrimSpace(decision.Action) == "" || strings.EqualFold(decision.Action, "none") {
		a.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
			TaskKey:    repository.ModelTaskMemoryMaintenance,
			UserID:     req.UserID,
			SessionID:  req.SessionID,
			RunID:      req.RunID,
			Source:     taskSource,
			Status:     repository.ModelTaskStatusSkipped,
			Provider:   provider,
			ModelID:    modelID,
			TargetType: "memory",
			TargetID:   fmt.Sprint(req.SessionID),
			Metadata:   json.RawMessage(`{"reason":"model_returned_none"}`),
			StartedAt:  started,
			FinishedAt: time.Now(),
		})
		return nil
	}
	if !strings.EqualFold(decision.Action, "update") {
		err = fmt.Errorf("unsupported memory maintenance action %q", decision.Action)
		a.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
			TaskKey:      repository.ModelTaskMemoryMaintenance,
			UserID:       req.UserID,
			SessionID:    req.SessionID,
			RunID:        req.RunID,
			Source:       taskSource,
			Status:       repository.ModelTaskStatusFailed,
			Provider:     provider,
			ModelID:      modelID,
			TargetType:   "memory",
			TargetID:     fmt.Sprint(req.SessionID),
			ErrorType:    "validation_error",
			ErrorMessage: err.Error(),
			RetryAfter:   retryAfterForTask(taskSource),
			StartedAt:    started,
			FinishedAt:   time.Now(),
		})
		return err
	}
	normalized, normalizedDoc, err := sessionmemory.NormalizeWithLimits(decision.Content, limits)
	if err != nil {
		a.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
			TaskKey:      repository.ModelTaskMemoryMaintenance,
			UserID:       req.UserID,
			SessionID:    req.SessionID,
			RunID:        req.RunID,
			Source:       taskSource,
			Status:       repository.ModelTaskStatusFailed,
			Provider:     provider,
			ModelID:      modelID,
			TargetType:   "memory",
			TargetID:     fmt.Sprint(req.SessionID),
			ErrorType:    "validation_error",
			ErrorMessage: err.Error(),
			RetryAfter:   retryAfterForTask(taskSource),
			StartedAt:    started,
			FinishedAt:   time.Now(),
		})
		return err
	}
	if err := validateMemoryMaintenanceLanguage(normalizedDoc); err != nil {
		a.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
			TaskKey:      repository.ModelTaskMemoryMaintenance,
			UserID:       req.UserID,
			SessionID:    req.SessionID,
			RunID:        req.RunID,
			Source:       taskSource,
			Status:       repository.ModelTaskStatusFailed,
			Provider:     provider,
			ModelID:      modelID,
			TargetType:   "memory",
			TargetID:     fmt.Sprint(req.SessionID),
			ErrorType:    "validation_error",
			ErrorMessage: err.Error(),
			RetryAfter:   retryAfterForTask(taskSource),
			StartedAt:    started,
			FinishedAt:   time.Now(),
		})
		return err
	}
	if strings.TrimSpace(normalized) == strings.TrimSpace(current) {
		a.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
			TaskKey:    repository.ModelTaskMemoryMaintenance,
			UserID:     req.UserID,
			SessionID:  req.SessionID,
			RunID:      req.RunID,
			Source:     taskSource,
			Status:     repository.ModelTaskStatusSkipped,
			Provider:   provider,
			ModelID:    modelID,
			TargetType: "memory",
			TargetID:   fmt.Sprint(req.SessionID),
			Metadata:   json.RawMessage(`{"reason":"unchanged"}`),
			StartedAt:  started,
			FinishedAt: time.Now(),
		})
		return nil
	}
	if err := validateMemoryMaintenanceDates(current, req.UserText+"\n"+req.ContextText, normalized, calendar.CurrentDate); err != nil {
		err = fmt.Errorf("memory maintenance discarded: %w", err)
		a.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
			TaskKey:      repository.ModelTaskMemoryMaintenance,
			UserID:       req.UserID,
			SessionID:    req.SessionID,
			RunID:        req.RunID,
			Source:       taskSource,
			Status:       repository.ModelTaskStatusFailed,
			Provider:     provider,
			ModelID:      modelID,
			TargetType:   "memory",
			TargetID:     fmt.Sprint(req.SessionID),
			ErrorType:    "validation_error",
			ErrorMessage: err.Error(),
			RetryAfter:   retryAfterForTask(taskSource),
			StartedAt:    started,
			FinishedAt:   time.Now(),
		})
		return err
	}
	if err := validateMemoryMaintenanceUpdate(current, normalized, IsMemoryDeletionRequest(req.UserText)); err != nil {
		err = fmt.Errorf("memory maintenance discarded: %w", err)
		a.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
			TaskKey:      repository.ModelTaskMemoryMaintenance,
			UserID:       req.UserID,
			SessionID:    req.SessionID,
			RunID:        req.RunID,
			Source:       taskSource,
			Status:       repository.ModelTaskStatusFailed,
			Provider:     provider,
			ModelID:      modelID,
			TargetType:   "memory",
			TargetID:     fmt.Sprint(req.SessionID),
			ErrorType:    "validation_error",
			ErrorMessage: err.Error(),
			RetryAfter:   retryAfterForTask(taskSource),
			StartedAt:    started,
			FinishedAt:   time.Now(),
		})
		return err
	}
	action := "update"
	if source == "compact" {
		action = "compact"
	}
	summary := strings.TrimSpace(decision.Summary)
	if summary == "" || !containsHan(summary) {
		summary = memoryMaintenanceSummary(source, current, normalized)
	}
	_, err = a.memoryRepo.SaveWithChange(ctx, repository.SaveSessionMemoryInput{
		SessionID:                       req.SessionID,
		UserID:                          req.UserID,
		Content:                         normalized,
		Source:                          source,
		Action:                          action,
		Summary:                         summary,
		ExpectedBefore:                  rawCurrent,
		CheckBefore:                     true,
		ExpectedAnswerSelectionRevision: req.ExpectedAnswerSelectionRevision,
		MaxChars:                        limits.MaxChars,
	})
	if err != nil {
		if errors.Is(err, repository.ErrAnswerSelectionRevisionConflict) {
			a.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
				TaskKey:    repository.ModelTaskMemoryMaintenance,
				UserID:     req.UserID,
				SessionID:  req.SessionID,
				RunID:      req.RunID,
				Source:     taskSource,
				Status:     repository.ModelTaskStatusSkipped,
				Provider:   provider,
				ModelID:    modelID,
				TargetType: "memory",
				TargetID:   fmt.Sprint(req.SessionID),
				Metadata:   json.RawMessage(`{"reason":"answer_selection_changed"}`),
				StartedAt:  started,
				FinishedAt: time.Now(),
			})
			return repository.ErrAnswerSelectionRevisionConflict
		}
		a.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
			TaskKey:      repository.ModelTaskMemoryMaintenance,
			UserID:       req.UserID,
			SessionID:    req.SessionID,
			RunID:        req.RunID,
			Source:       taskSource,
			Status:       repository.ModelTaskStatusFailed,
			Provider:     provider,
			ModelID:      modelID,
			TargetType:   "memory",
			TargetID:     fmt.Sprint(req.SessionID),
			ErrorType:    modelusage.ErrorType(err),
			ErrorMessage: err.Error(),
			RetryAfter:   retryAfterForTask(taskSource),
			StartedAt:    started,
			FinishedAt:   time.Now(),
		})
		return err
	}
	a.recordUtilityTaskRun(ctx, repository.RecordModelTaskRunInput{
		TaskKey:    repository.ModelTaskMemoryMaintenance,
		UserID:     req.UserID,
		SessionID:  req.SessionID,
		RunID:      req.RunID,
		Source:     taskSource,
		Status:     repository.ModelTaskStatusSuccess,
		Provider:   provider,
		ModelID:    modelID,
		TargetType: "memory",
		TargetID:   fmt.Sprint(req.SessionID),
		StartedAt:  started,
		FinishedAt: time.Now(),
	})
	return err
}

func containsHan(value string) bool {
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func memoryMaintenanceSummary(source, before, after string) string {
	if source == "compact" {
		return "已整理会话记忆"
	}
	return sessionmemory.Summary(before, after)
}

func validateMemoryMaintenanceLanguage(doc sessionmemory.Document) error {
	for _, section := range doc.Sections {
		for _, item := range section.Items {
			if containsHan(item) || len(memoryEnglishWordPattern.FindAllString(item, -1)) < 3 {
				continue
			}
			return fmt.Errorf("memory maintenance returned non-Chinese prose in section %q", section.Title)
		}
	}
	return nil
}

func (a *EinoAgent) buildMemoryMaintenanceModel(ctx context.Context, req MemoryMaintenanceRequest) (einoModel.ToolCallingChatModel, string, string, error) {
	if req.ModelRequest == nil || strings.TrimSpace(req.ModelRequest.ModelID) == "" || strings.TrimSpace(req.ModelRequest.Provider) == "" {
		return nil, "", "", fmt.Errorf("memory maintenance requires the active chat model configuration")
	}
	modelReq := *req.ModelRequest
	modelReq.UserID = req.UserID
	modelReq.SessionID = req.SessionID
	modelReq.SkipUsage = false
	cm, err := a.buildChatModel(ctx, &modelReq, modelbank.SearchDecision{})
	if err != nil {
		return nil, modelReq.Provider, modelReq.ModelID, err
	}
	return cm, modelReq.Provider, modelReq.ModelID, nil
}

type memoryMaintenanceCalendar struct {
	CurrentDate  string
	CurrentWeek  string
	CurrentMonth string
	Timezone     string
}

func memoryMaintenanceCalendarAt(now time.Time, location *time.Location) memoryMaintenanceCalendar {
	if location == nil {
		location = userLocation(nil)
	}
	now = now.In(location)
	year, week := now.ISOWeek()
	return memoryMaintenanceCalendar{
		CurrentDate:  now.Format("2006-01-02"),
		CurrentWeek:  fmt.Sprintf("%04d-W%02d", year, week),
		CurrentMonth: now.Format("2006-01"),
		Timezone:     location.String(),
	}
}

func userPreferencesForMemory(req MemoryMaintenanceRequest) []byte {
	if req.ModelRequest == nil {
		return nil
	}
	return req.ModelRequest.UserPreferences
}

func ShouldRunMemoryMaintenance(userText string) bool {
	text := strings.ToLower(strings.TrimSpace(userText))
	if len([]rune(text)) < 8 {
		return false
	}
	if IsExplicitMemoryMaintenanceRequest(text) {
		return true
	}
	if IsMemoryReadOnlyRequest(text) {
		return false
	}
	if IsTinyChitChat(text) {
		return false
	}
	return true
}

func IsTinyChitChat(userText string) bool {
	text := strings.TrimSpace(strings.ToLower(userText))
	text = strings.Trim(text, "。.!！?？~～ ")
	if len([]rune(text)) > 12 {
		return false
	}
	switch text {
	case "好", "好的", "行", "可以", "嗯", "嗯嗯", "ok", "okay", "thanks", "thank you", "谢谢", "辛苦了", "下次再说", "下次再聊", "下次再聊吧", "回头再聊", "哈哈":
		return true
	default:
		return false
	}
}

func IsExplicitMemoryMaintenanceRequest(userText string) bool {
	text := strings.ToLower(strings.TrimSpace(userText))
	if len([]rune(text)) < 2 {
		return false
	}
	keywords := []string{
		"记住", "记一下", "保存到记忆", "存到记忆", "别忘", "忘掉", "忘记", "删除记忆", "清除记忆", "不要记",
		"更新记忆", "修改记忆", "更正记忆", "更新这个决策", "更新这条决策", "修改这个决策", "修正这个决策", "纠正这个决策",
		"remember", "save this", "save to memory", "forget", "delete memory", "do not remember",
	}
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func IsMemoryReadOnlyRequest(userText string) bool {
	text := strings.ToLower(strings.TrimSpace(userText))
	memoryHints := []string{"会话记忆", "根据记忆", "基于记忆", "从记忆", "当前记忆", "memory"}
	hasMemoryHint := false
	for _, hint := range memoryHints {
		if strings.Contains(text, hint) {
			hasMemoryHint = true
			break
		}
	}
	if !hasMemoryHint {
		return false
	}
	readOnlyHints := []string{"回答", "总结", "列出", "告诉我", "是什么", "有哪些", "展示", "查看", "what", "summarize", "based on", "according to"}
	for _, hint := range readOnlyHints {
		if strings.Contains(text, hint) {
			return true
		}
	}
	return false
}

func IsMemoryDeletionRequest(userText string) bool {
	text := strings.ToLower(strings.TrimSpace(userText))
	keywords := []string{"忘掉", "忘记", "删除记忆", "清除记忆", "清空记忆", "不要记", "forget", "delete memory", "clear memory", "do not remember"}
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func validateMemoryMaintenanceUpdate(before, after string, allowEmpty bool) error {
	beforeDoc := mustParseMemory(before)
	afterDoc := mustParseMemory(after)
	beforeItems := countMemoryItems(beforeDoc)
	afterItems := countMemoryItems(afterDoc)
	if afterItems == 0 && beforeItems > 0 && !allowEmpty {
		return fmt.Errorf("produced empty memory")
	}
	if beforeItems > 1 && afterItems == 1 {
		for _, section := range afterDoc.Sections {
			for _, item := range section.Items {
				if len([]rune(strings.TrimSpace(item))) < 10 {
					return fmt.Errorf("produced suspicious single-item memory")
				}
			}
		}
	}
	return nil
}

func validateMemoryMaintenanceDates(current, userText, after, currentDate string) error {
	allowed := map[string]struct{}{}
	for _, source := range []string{current, userText, currentDate} {
		for _, date := range memoryDatePattern.FindAllString(source, -1) {
			allowed[date] = struct{}{}
		}
	}
	for _, date := range memoryDatePattern.FindAllString(after, -1) {
		if _, ok := allowed[date]; !ok {
			return fmt.Errorf("produced ungrounded date %s", date)
		}
	}
	return nil
}

func countMemoryItems(doc sessionmemory.Document) int {
	n := 0
	for _, section := range doc.Sections {
		n += len(section.Items)
	}
	return n
}

func buildMemoryMaintenancePrompt(current string, req MemoryMaintenanceRequest, source string, calendar memoryMaintenanceCalendar) string {
	var b strings.Builder
	b.WriteString("Current date: ")
	b.WriteString(calendar.CurrentDate)
	b.WriteString("\nCurrent week: ")
	b.WriteString(calendar.CurrentWeek)
	b.WriteString("\nCurrent month: ")
	b.WriteString(calendar.CurrentMonth)
	b.WriteString("\nTimezone: ")
	b.WriteString(calendar.Timezone)
	b.WriteString("\n\n")
	b.WriteString("Current memory:\n")
	if strings.TrimSpace(current) == "" {
		b.WriteString("(empty)")
	} else {
		b.WriteString(current)
	}
	b.WriteString("\n\nMode: ")
	if source == "compact" {
		b.WriteString("compact and clean the memory card. Preserve facts, remove duplication, keep sections concise.")
	} else {
		b.WriteString("update memory only if the recent conversation window contains durable information. Prefer the newest user correction when meanings conflict.")
	}
	b.WriteString("\n\nLatest user message:\n")
	b.WriteString(limitMemoryPromptText(req.UserText, 2000))
	if strings.TrimSpace(req.ContextText) != "" {
		b.WriteString("\n\nRecent conversation window:\n")
		b.WriteString(limitMemoryPromptText(req.ContextText, 8000))
	}
	return b.String()
}

func generateMemoryMaintenanceText(ctx context.Context, chatModel einoModel.ToolCallingChatModel, messages []*schema.Message) (string, error) {
	stream, err := chatModel.Stream(ctx, messages)
	if err != nil {
		return "", err
	}
	if stream == nil {
		return "", fmt.Errorf("memory maintenance stream is nil")
	}
	defer stream.Close()

	var b strings.Builder
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if chunk != nil {
			b.WriteString(chunk.Content)
		}
	}
	output := strings.TrimSpace(b.String())
	if output == "" {
		return "", fmt.Errorf("memory maintenance returned empty output")
	}
	return output, nil
}

func parseMemoryMaintenanceDecision(raw string) (memoryMaintenanceDecision, error) {
	raw = strings.TrimSpace(raw)
	candidates := fencedJSONCandidates(raw)
	candidates = append(candidates, raw)
	var lastErr error
	for _, candidate := range candidates {
		decision, err := decodeFirstJSONDecision(candidate)
		if err == nil {
			return decision, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return memoryMaintenanceDecision{}, fmt.Errorf("decode memory maintenance decision: %w", lastErr)
	}
	return memoryMaintenanceDecision{}, fmt.Errorf("memory maintenance returned non-json output")
}

func fencedJSONCandidates(raw string) []string {
	matches := memoryFencedJSONPattern.FindAllStringSubmatch(raw, -1)
	candidates := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			candidates = append(candidates, match[1])
		}
	}
	return candidates
}

func decodeFirstJSONDecision(raw string) (memoryMaintenanceDecision, error) {
	start := strings.Index(raw, "{")
	if start < 0 {
		return memoryMaintenanceDecision{}, fmt.Errorf("memory maintenance returned non-json output")
	}
	var decision memoryMaintenanceDecision
	if err := json.NewDecoder(strings.NewReader(raw[start:])).Decode(&decision); err != nil {
		return memoryMaintenanceDecision{}, err
	}
	return decision, nil
}

func mustParseMemory(content string) sessionmemory.Document {
	doc, err := sessionmemory.Parse(content)
	if err != nil {
		return sessionmemory.EmptyDocument()
	}
	return doc
}

func limitMemoryPromptText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "\n[truncated]"
}

func modelTaskSourceForMaintenance(source string) string {
	switch strings.TrimSpace(source) {
	case "auto":
		return repository.ModelTaskSourceAuto
	case "tool":
		return repository.ModelTaskSourceTool
	default:
		return repository.ModelTaskSourceManual
	}
}

func retryAfterForTask(source string) *time.Time {
	if source != repository.ModelTaskSourceAuto {
		return nil
	}
	t := time.Now().Add(30 * time.Minute)
	return &t
}

func (a *EinoAgent) memoryLimits() sessionmemory.Limits {
	if a != nil && a.configRepo != nil {
		return a.configRepo.GetMemoryLimits()
	}
	return sessionmemory.DefaultLimits()
}

func (a *EinoAgent) recordUtilityTaskRun(_ context.Context, input repository.RecordModelTaskRunInput) {
	if a == nil || a.taskRunRepo == nil {
		return
	}
	recordCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := a.taskRunRepo.Record(recordCtx, input); err != nil {
		log.Printf("[model_task_runs] record failed: task=%s session=%d err=%v", input.TaskKey, input.SessionID, err)
	}
}
