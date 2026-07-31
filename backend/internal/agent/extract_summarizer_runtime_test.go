package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/modelbank"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
	"github.com/huoguojun123/EffChat/internal/testutil"
)

func TestBuildExtractSummarizerUsesAcceptedRuntimeWithoutLiveConfigRead(t *testing.T) {
	requestBodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		requestBodies <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-refiner\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"refiner\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"accepted summary\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	channelKey := fmt.Sprintf("accepted-refiner-channel-%d", time.Now().UnixNano())
	modelID := fmt.Sprintf("accepted-refiner-model-%d", time.Now().UnixNano())
	acceptedInfo := &modelbank.ModelInfo{
		ID: modelID, DisplayName: modelID, Provider: channelKey, Enabled: true,
		Capabilities: modelbank.ModelCapabilities{ContextWindow: 32000, MaxOutput: 2048},
	}
	acceptedChannel := &model.AIChannel{
		Key: channelKey, DisplayName: "Accepted refiner", Adapter: service.AdapterOpenAICompatible,
		BaseURL: server.URL + "/v1", APIKey: "accepted-refiner-key", Enabled: true,
	}

	// Repositories with no database are intentional: validated requests must
	// consume the accepted model/channel and never touch live configuration.
	agent := NewEinoAgent(
		service.NewChannelService(nil),
		nil,
		4096,
		repository.NewConfigRepository(nil),
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	built, summaryEnabled, err := agent.buildExtractSummarizer(t.Context(), &ChatRequest{
		RuntimeResolved:                true,
		RuntimeExtractSummaryEnabled:   true,
		RuntimeExtractSummaryModel:     modelID,
		RuntimeExtractSummaryModelInfo: acceptedInfo,
		RuntimeExtractSummaryChannel:   acceptedChannel,
	})
	if err != nil {
		t.Fatalf("buildExtractSummarizer() error = %v", err)
	}
	if !summaryEnabled {
		t.Fatal("accepted refinement was unexpectedly disabled")
	}
	summarizer, ok := built.(*extractSummarizer)
	if !ok || summarizer.modelID != modelID || summarizer.provider != channelKey || summarizer.runtimeVersion == "" {
		t.Fatalf("built summarizer = %#v", built)
	}
	got, err := summarizer.Summarize(t.Context(), "goal", "title", "content", "summary")
	if err != nil {
		t.Fatalf("Summarize() after setup cancellation error = %v", err)
	}
	if got != "accepted summary" {
		t.Fatalf("summary = %q", got)
	}
	var request map[string]interface{}
	select {
	case body := <-requestBodies:
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatalf("decode utility request: %v", err)
		}
	default:
		t.Fatal("utility request was not captured")
	}
	if got, _ := request["max_tokens"].(float64); int(got) != acceptedInfo.Capabilities.MaxOutput {
		t.Fatalf("utility max_tokens = %v, want model cap %d", request["max_tokens"], acceptedInfo.Capabilities.MaxOutput)
	}
}

func TestBuildExtractSummarizerKeepsAcceptedUnavailableRuntimeDegraded(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	defer db.Close()
	channelKey := fmt.Sprintf("late-refiner-channel-%d", time.Now().UnixNano())
	modelID := fmt.Sprintf("late-refiner-model-%d", time.Now().UnixNano())
	enabled := true
	channelService := service.NewChannelService(repository.NewChannelRepository(db))
	if _, err := channelService.SaveAIChannel(&service.AIChannelInput{
		Key: channelKey, DisplayName: "Late refiner", Adapter: service.AdapterOpenAICompatible,
		BaseURL: "https://late-refiner.example.test/v1", APIKey: "late-refiner-key", Enabled: &enabled,
	}); err != nil {
		t.Fatalf("save late utility channel: %v", err)
	}
	modelbank.Register(&modelbank.ModelInfo{
		ID: modelID, DisplayName: modelID, Provider: channelKey, Enabled: true,
		Capabilities: modelbank.ModelCapabilities{ContextWindow: 32000, MaxOutput: 8192},
	})

	agent := NewEinoAgent(channelService, nil, 4096, nil, nil, nil, nil, nil, nil)
	built, summaryEnabled, err := agent.buildExtractSummarizer(t.Context(), &ChatRequest{
		RuntimeResolved:              true,
		RuntimeExtractSummaryEnabled: true,
		RuntimeExtractSummaryModel:   modelID,
		// Nil accepted ModelInfo/channel mean admission fixed this run to the
		// unavailable downgrade. The now-live dependency belongs to a later run.
	})
	if err != nil {
		t.Fatalf("buildExtractSummarizer() error = %v", err)
	}
	if built != nil || summaryEnabled {
		t.Fatalf("accepted unavailable refinement reopened live dependencies: summarizer=%T enabled=%t", built, summaryEnabled)
	}
}

func TestBuildUtilityModelPreservesSetupDeadline(t *testing.T) {
	db := testutil.OpenPostgresTestDB(t)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	held, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("hold database connection: %v", err)
	}
	defer held.Close()

	channelKey := fmt.Sprintf("blocked-refiner-channel-%d", time.Now().UnixNano())
	modelID := fmt.Sprintf("blocked-refiner-model-%d", time.Now().UnixNano())
	modelbank.Register(&modelbank.ModelInfo{
		ID: modelID, DisplayName: modelID, Provider: channelKey, Enabled: true,
		Capabilities: modelbank.ModelCapabilities{ContextWindow: 32000, MaxOutput: 8192},
	})
	agent := NewEinoAgent(
		service.NewChannelService(repository.NewChannelRepository(db)),
		nil,
		4096,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _, _, err = agent.buildUtilityModelWithInfo(ctx, modelID)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("buildUtilityModelWithInfo() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("utility setup ignored its context: %s", elapsed)
	}
}

func TestExtractSummarizerRuntimeVersionTracksResolvedModelAndChannel(t *testing.T) {
	info := &modelbank.ModelInfo{
		ID: "refiner-model", Provider: "refiner-channel", Enabled: true, ThinkingFormat: "auto",
		Capabilities: modelbank.ModelCapabilities{
			ContextWindow: 32000, MaxOutput: 4096, ToolUse: true,
		},
	}
	channel := &model.AIChannel{
		Key: "refiner-channel", Adapter: service.AdapterOpenAICompatible,
		BaseURL: "https://refiner.example.test/v1", APIKey: "first-key", Enabled: true,
	}
	agent := &EinoAgent{}
	base := agent.extractSummarizerRuntimeVersion("configured-model", info, channel)
	if base == "" {
		t.Fatal("runtime version is empty")
	}
	rotated := *channel
	rotated.APIKey = "second-key"
	if got := agent.extractSummarizerRuntimeVersion("configured-model", info, &rotated); got == base {
		t.Fatal("runtime version did not change after resolved channel rotation")
	}
	if got := agent.extractSummarizerRuntimeVersion("other-configured-model", info, channel); got == base {
		t.Fatal("runtime version did not change after accepted model config changed")
	}
	changedInfo := *info
	changedInfo.Capabilities.MaxOutput = 8192
	if got := agent.extractSummarizerRuntimeVersion("configured-model", &changedInfo, channel); got == base {
		t.Fatal("runtime version did not change after resolved model material changed")
	}
}
