package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/huoguojun123/EffChat/internal/repository"
)

type PromptTemplateData struct {
	SystemName             string
	CurrentDate            string
	CurrentTime            string
	CurrentDateTime        string
	Timezone               string
	UserName               string
	UserNickname           string
	UserDisplayName        string
	UserRole               string
	UserBlock              string
	UserPreferenceBlock    string
	SessionTitle           string
	ModelID                string
	Provider               string
	MessageFormat          string
	Temperature            string
	MaxTokens              string
	SearchMode             string
	SessionBlock           string
	SessionPreferenceBlock string
	SessionPrompt          string
	CapabilityBlock        string
}

func loadPromptTemplate(configRepo *repository.ConfigRepository) (string, error) {
	templateText := defaultSystemPromptTemplate()
	if configRepo == nil {
		return templateText, nil
	}
	item, err := configRepo.Get("system_prompt_template")
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return templateText, nil
		}
		return "", fmt.Errorf("system prompt configuration is unavailable")
	}
	var value string
	if err := json.Unmarshal(item.Value, &value); err != nil {
		return "", fmt.Errorf("system prompt configuration is invalid")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("system prompt configuration is empty")
	}
	if err := repository.ValidateSystemPromptTemplate(value); err != nil {
		return "", fmt.Errorf("system prompt configuration is invalid")
	}
	return value, nil
}

func renderPromptTemplate(templateText string, data PromptTemplateData) (string, error) {
	rendered, err := executePromptTemplate(repository.NormalizeSystemPromptTemplate(templateText), data)
	if err == nil {
		return strings.TrimSpace(rendered), nil
	}
	return "", fmt.Errorf("system prompt configuration cannot execute: %w", err)
}

func executePromptTemplate(templateText string, data PromptTemplateData) (string, error) {
	tpl, err := template.New("system_prompt").Parse(templateText)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return strings.ReplaceAll(buf.String(), "\r", ""), nil
}

func buildPromptTemplateData(req *ChatRequest) PromptTemplateData {
	location := userLocation(req.UserPreferences)
	now := req.PromptTime
	if now.IsZero() {
		now = time.Now()
	}
	now = now.In(location)
	timezone := location.String()
	systemName := strings.TrimSpace(req.SystemName)
	if systemName == "" {
		systemName = "EffChat"
	}
	userPreferenceBlock := formatPreferenceBlock(parsePreferenceMap(req.UserPreferences))
	sessionPreferenceBlock := formatPreferenceBlock(parseSessionPreferenceMap(req.SessionMetadata))
	userDisplayName := strings.TrimSpace(req.UserDisplayName)
	if userDisplayName == "" {
		userDisplayName = strings.TrimSpace(req.UserNickname)
	}
	if userDisplayName == "" {
		userDisplayName = strings.TrimSpace(req.UserName)
	}
	return PromptTemplateData{
		SystemName:             systemName,
		CurrentDate:            now.Format("2006-01-02"),
		CurrentTime:            now.Format("15:04:05"),
		CurrentDateTime:        now.Format("2006-01-02 15:04:05"),
		Timezone:               timezone,
		UserName:               strings.TrimSpace(req.UserName),
		UserNickname:           strings.TrimSpace(req.UserNickname),
		UserDisplayName:        userDisplayName,
		UserRole:               strings.TrimSpace(req.UserRole),
		UserBlock:              formatUserBlock(req, userDisplayName),
		UserPreferenceBlock:    userPreferenceBlock,
		SessionTitle:           strings.TrimSpace(req.SessionTitle),
		ModelID:                strings.TrimSpace(req.ModelID),
		Provider:               strings.TrimSpace(req.Provider),
		MessageFormat:          strings.TrimSpace(req.MessageFormat),
		Temperature:            formatFloat(req.Temperature),
		MaxTokens:              formatInt(req.MaxTokens),
		SearchMode:             formatSearchMode(req.SearchMode),
		SessionBlock:           formatSessionBlock(req),
		SessionPreferenceBlock: sessionPreferenceBlock,
		SessionPrompt:          strings.TrimSpace(req.SystemPrompt),
		CapabilityBlock:        formatCapabilityBlock(req.ModelID, req.Provider),
	}
}

const defaultUserTimezone = "Asia/Shanghai"

func userLocation(preferences []byte) *time.Location {
	timezone := defaultUserTimezone
	if value, ok := parsePreferenceMap(preferences)["timezone"].(string); ok && strings.TrimSpace(value) != "" {
		timezone = strings.TrimSpace(value)
	}
	location, err := time.LoadLocation(timezone)
	if err == nil {
		return location
	}
	location, err = time.LoadLocation(defaultUserTimezone)
	if err == nil {
		return location
	}
	return time.UTC
}

func defaultSystemPromptTemplate() string {
	// 单一权威来源在 repository 包（被 Admin 配置默认值与本 runtime 同时引用），
	// 避免两处各存一份模板字符串导致漂移。
	return repository.DefaultSystemPromptTemplate
}
