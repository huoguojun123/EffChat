package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

var ErrAcceptedContextIncomplete = errors.New("accepted message context is incomplete")

// AcceptedAgentContext owns the durable-admission to model-input boundary. The
// accepted message is the row bound by admission, not the original retry intent:
// an edited retry has a new ID while its old target remains an idempotency key.
// Validate raw messages first; controlled attachment projection may then extend
// content without changing the durable original or weakening fail-closed checks.
func (s *MessageService) AcceptedAgentContext(ctx context.Context, sessionID, userID int64, accepted *model.Message, retry bool) ([]*model.Message, error) {
	if accepted == nil || accepted.ID <= 0 || accepted.SessionID != sessionID {
		return nil, ErrAcceptedContextIncomplete
	}
	preserveID := int64(0)
	if retry {
		preserveID = accepted.ID
	}
	messages, err := s.listAgentMessages(ctx, sessionID, userID, preserveID, nil)
	if err != nil {
		return nil, err
	}
	if err := validateAcceptedUserMessageContext(messages, accepted); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAcceptedContextIncomplete, err)
	}
	if retry {
		// Old answers and later turns must never enter the regenerated prompt.
		messages, err = messagesThroughID(messages, accepted.ID)
		if err != nil {
			return nil, err
		}
	}
	s.prepareAttachmentsForAgent(ctx, userID, sessionID, messages)
	return messages, nil
}

func validateAcceptedUserMessageContext(messages []*model.Message, accepted *model.Message) error {
	if accepted == nil || accepted.ID <= 0 {
		return errors.New("accepted user message is missing")
	}
	expected, err := repository.ParseMessageData(accepted.MessageData)
	if err != nil {
		return fmt.Errorf("parse accepted message: %w", err)
	}
	expectedRole, _ := expected["role"].(string)
	expectedContent, _ := expected["content"].(string)
	for _, message := range messages {
		if message == nil || message.ID != accepted.ID {
			continue
		}
		actual, err := repository.ParseMessageData(message.MessageData)
		if err != nil {
			return fmt.Errorf("parse reloaded message %d: %w", accepted.ID, err)
		}
		actualRole, _ := actual["role"].(string)
		actualContent, _ := actual["content"].(string)
		if actualRole != expectedRole || actualRole != "user" {
			return fmt.Errorf("reloaded message %d role mismatch", accepted.ID)
		}
		if actualContent != expectedContent {
			return fmt.Errorf("reloaded message %d content mismatch", accepted.ID)
		}
		if !reflect.DeepEqual(actual["attachments"], expected["attachments"]) {
			return fmt.Errorf("reloaded message %d attachment mismatch", accepted.ID)
		}
		return nil
	}
	return fmt.Errorf("accepted message %d is absent from agent context", accepted.ID)
}
