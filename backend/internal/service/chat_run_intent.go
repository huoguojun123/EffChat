package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/huoguojun123/EffChat/internal/model"
)

const ChatRunIntentVersion = 1

const (
	RunOperationSend          = "send"
	RunOperationRetry         = "retry"
	RunOperationCompaction    = "compaction"
	RunOperationMemoryCompact = "memory_compact"
	RunOperationMemoryRetry   = "memory_retry"
)

type RunIntent struct {
	Operation            string
	Version              int
	Hash                 string
	RetryTargetMessageID int64
}

func BuildSendRunIntent(session *model.Session, req *SendMessageRequest) RunIntent {
	schemaVersion := ""
	if session != nil {
		schemaVersion = session.MessageFormat
	}
	content := ""
	attachments := []int64{}
	thinkingEffort := ""
	if req != nil {
		content = req.Content
		if req.SchemaVersion != "" {
			schemaVersion = req.SchemaVersion
		}
		attachments = uniqueAttachmentIDs(req.Attachments)
		thinkingEffort = normalizeMessageThinkingEffort(req.ThinkingEffort)
	}
	payload := struct {
		Operation      string  `json:"operation"`
		Content        string  `json:"content"`
		SchemaVersion  string  `json:"schema_version"`
		Attachments    []int64 `json:"attachments"`
		ThinkingEffort string  `json:"thinking_effort"`
	}{
		Operation:      RunOperationSend,
		Content:        content,
		SchemaVersion:  schemaVersion,
		Attachments:    attachments,
		ThinkingEffort: thinkingEffort,
	}
	return newRunIntent(RunOperationSend, 0, payload)
}

func BuildRetryRunIntent(targetMessageID int64) RunIntent {
	payload := struct {
		Operation       string `json:"operation"`
		TargetMessageID int64  `json:"target_message_id"`
	}{Operation: RunOperationRetry, TargetMessageID: targetMessageID}
	return newRunIntent(RunOperationRetry, targetMessageID, payload)
}

func BuildEditRetryRunIntent(targetMessageID int64, content string) RunIntent {
	payload := struct {
		Operation       string `json:"operation"`
		TargetMessageID int64  `json:"target_message_id"`
		Content         string `json:"content"`
	}{
		Operation:       "edit_retry",
		TargetMessageID: targetMessageID,
		Content:         content,
	}
	return newRunIntent(RunOperationRetry, targetMessageID, payload)
}

func BuildCompactionRunIntent(source, thinkingEffort string, preserveMessageID int64) RunIntent {
	source = strings.ToLower(strings.TrimSpace(source))
	if source != "auto" {
		source = "manual"
	}
	if preserveMessageID < 0 {
		preserveMessageID = 0
	}
	payload := struct {
		Operation         string `json:"operation"`
		Source            string `json:"source"`
		ThinkingEffort    string `json:"thinking_effort"`
		PreserveMessageID int64  `json:"preserve_message_id"`
	}{
		Operation:         RunOperationCompaction,
		Source:            source,
		ThinkingEffort:    normalizeMessageThinkingEffort(thinkingEffort),
		PreserveMessageID: preserveMessageID,
	}
	return newRunIntent(RunOperationCompaction, 0, payload)
}

func BuildMemoryMaintenanceRunIntent(operation string) RunIntent {
	if operation != RunOperationMemoryRetry {
		operation = RunOperationMemoryCompact
	}
	payload := struct {
		Operation string `json:"operation"`
	}{Operation: operation}
	return newRunIntent(operation, 0, payload)
}

func newRunIntent(operation string, retryTargetMessageID int64, payload interface{}) RunIntent {
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return RunIntent{
		Operation:            operation,
		Version:              ChatRunIntentVersion,
		Hash:                 "v1:" + hex.EncodeToString(sum[:]),
		RetryTargetMessageID: retryTargetMessageID,
	}
}

func uniqueAttachmentIDs(ids []int64) []int64 {
	result := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}
