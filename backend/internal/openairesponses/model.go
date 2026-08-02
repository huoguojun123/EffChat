// Package openairesponses owns EffChat's configuration and product-boundary
// normalization for Eino's official OpenAI Responses model. Main conversations
// keep the native AgenticModel through Eino's typed ReAct Agent; tool-free
// utility calls may use the classic adapter. The upstream component remains
// responsible for the wire protocol and SSE parsing.
package openairesponses

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/openai/openai-go/v3/responses"
)

var _ einoModel.ToolCallingChatModel = (*chatModel)(nil)

// Config contains only the Responses settings EffChat owns. Server-hosted
// tools, MCP tools, previous_response_id, and response auto-caching are
// intentionally absent because PostgreSQL and the Eino Tool interface remain
// the product state and governance boundary.
type Config struct {
	BaseURL     string
	APIKey      string
	HTTPClient  *http.Client
	Model       string
	MaxTokens   *int
	Temperature *float32
	TopP        *float32
	Reasoning   *responses.ReasoningParam
}

// NewChatModel exposes a stateless Responses model through the classic Eino
// contract for tool-free utility calls. No total request timeout is set here:
// the shared first-output and cancellation lifecycle remains the only stream
// deadline owner.
func NewChatModel(ctx context.Context, cfg *Config) (einoModel.ToolCallingChatModel, error) {
	model, err := NewAgenticModel(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return newChatModel(model), nil
}

// NewAgenticModel returns the official Eino Responses model without adapting
// it to the classic message contract. The main conversation uses this path so
// Eino's typed Agentic ReAct graph owns local Tool execution end to end.
func NewAgenticModel(ctx context.Context, cfg *Config) (einoModel.AgenticModel, error) {
	if cfg == nil {
		return nil, fmt.Errorf("openai responses config is required")
	}
	store := false
	maxRetries := 0
	model, err := agenticopenai.NewResponsesModel(ctx, &agenticopenai.ResponsesConfig{
		BaseURL:         cfg.BaseURL,
		APIKey:          cfg.APIKey,
		HTTPClient:      cfg.HTTPClient,
		MaxRetries:      &maxRetries,
		Model:           cfg.Model,
		MaxTokens:       cfg.MaxTokens,
		Temperature:     cfg.Temperature,
		TopP:            cfg.TopP,
		Reasoning:       cfg.Reasoning,
		Store:           &store,
		EnableAutoCache: false,
	})
	if err != nil {
		return nil, err
	}
	return model, nil
}

type chatModel struct {
	base  einoModel.AgenticModel
	tools []*schema.ToolInfo
}

func newChatModel(base einoModel.AgenticModel) einoModel.ToolCallingChatModel {
	return &chatModel{base: base}
}

func (m *chatModel) Generate(ctx context.Context, input []*schema.Message, opts ...einoModel.Option) (*schema.Message, error) {
	if m == nil || m.base == nil {
		return nil, fmt.Errorf("openai responses model is unavailable")
	}
	messages, err := toAgenticMessages(input)
	if err != nil {
		return nil, err
	}
	out, err := m.base.Generate(ctx, messages, m.options(opts)...)
	if err != nil {
		return nil, err
	}
	state := responseState{}
	converted, terminalErr := fromAgenticMessage(out, &state)
	if terminalErr != nil {
		return nil, terminalErr
	}
	return converted, nil
}

func (m *chatModel) Stream(ctx context.Context, input []*schema.Message, opts ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	if m == nil || m.base == nil {
		return nil, fmt.Errorf("openai responses model is unavailable")
	}
	messages, err := toAgenticMessages(input)
	if err != nil {
		return nil, err
	}
	reader, err := m.base.Stream(ctx, messages, m.options(opts)...)
	if err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, fmt.Errorf("openai responses returned a nil stream")
	}

	out, writer := schema.Pipe[*schema.Message](1)
	go func() {
		defer writer.Close()
		defer reader.Close()

		state := responseState{}
		for {
			chunk, recvErr := reader.Recv()
			if errors.Is(recvErr, io.EOF) {
				return
			}
			if recvErr != nil {
				_ = writer.Send(nil, recvErr)
				return
			}
			converted, terminalErr := fromAgenticMessage(chunk, &state)
			if converted != nil {
				if closed := writer.Send(converted, nil); closed {
					return
				}
			}
			if terminalErr != nil {
				_ = writer.Send(nil, terminalErr)
				return
			}
		}
	}()
	return out, nil
}

func (m *chatModel) WithTools(tools []*schema.ToolInfo) (einoModel.ToolCallingChatModel, error) {
	if m == nil || m.base == nil {
		return nil, fmt.Errorf("openai responses model is unavailable")
	}
	clone := &chatModel{base: m.base}
	clone.tools = append([]*schema.ToolInfo(nil), tools...)
	return clone, nil
}

func (m *chatModel) options(opts []einoModel.Option) []einoModel.Option {
	result := append([]einoModel.Option(nil), opts...)
	// ToolCallingChatModel binds tools immutably through WithTools, while the
	// AgenticModel contract accepts them per call. Appending this option bridges
	// those concurrency models without mutating the upstream model.
	return append(result, einoModel.WithTools(append([]*schema.ToolInfo(nil), m.tools...)))
}
