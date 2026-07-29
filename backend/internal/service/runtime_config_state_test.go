package service

import (
	"testing"

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
