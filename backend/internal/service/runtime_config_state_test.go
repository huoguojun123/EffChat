package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func TestToolConfigServiceResolveRuntimeConfigReportsRepositoryFailure(t *testing.T) {
	runtime, state := NewToolConfigService(&repository.ToolConfigRepository{}).ResolveRuntimeConfig()
	if state.State != RuntimeStateUnavailable || state.Cause != "repository_unavailable" {
		t.Fatalf("state = %#v", state)
	}
	if runtime.IsEnabled("web_search") {
		t.Fatal("web_search should fail closed when tool configuration cannot be read")
	}
}

func TestBuildSearchRuntimeConfigWithStateDistinguishesDisabledAndUnconfigured(t *testing.T) {
	_, unconfigured := BuildSearchRuntimeConfigWithState(nil)
	if unconfigured.Search.State != RuntimeStateUnconfigured || unconfigured.Extract.State != RuntimeStateUnconfigured {
		t.Fatalf("unconfigured = %#v", unconfigured)
	}

	_, disabled := BuildSearchRuntimeConfigWithState([]*model.ExternalService{
		{Key: "searxng", Kind: ServiceKindSearch, Enabled: false},
		{Key: "jina", Kind: ServiceKindCrawler, Enabled: false},
	})
	if disabled.Search.State != RuntimeStateDisabled || disabled.Extract.State != RuntimeStateDisabled {
		t.Fatalf("disabled = %#v", disabled)
	}
}

func TestRuntimeConfigResolversSurfaceContextDeadline(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	blocker, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("hold database connection: %v", err)
	}
	defer blocker.Close()

	tests := []struct {
		name string
		run  func(context.Context) error
	}{
		{
			name: "ai channel",
			run: func(ctx context.Context) error {
				_, err := NewChannelService(repository.NewChannelRepository(db)).ResolveAIChannelContext(ctx, "test")
				return err
			},
		},
		{
			name: "search runtime",
			run: func(ctx context.Context) error {
				_, _, err := NewChannelService(repository.NewChannelRepository(db)).ResolveSearchRuntimeConfigWithStateContext(ctx)
				return err
			},
		},
		{
			name: "tool runtime",
			run: func(ctx context.Context) error {
				_, _, err := NewToolConfigService(repository.NewToolConfigRepository(db)).ResolveRuntimeConfigContext(ctx)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if err := test.run(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v, want context.DeadlineExceeded", err)
			}
		})
	}
}
