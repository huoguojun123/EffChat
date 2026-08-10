package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

func TestChannelFromInputNormalizesAndPreservesBlankKey(t *testing.T) {
	enabled := false
	item, replaceKey, err := channelFromInput(&AIChannelInput{
		Key:         " OpenRouter ",
		DisplayName: " OpenRouter ",
		Adapter:     " OPENAI_COMPATIBLE ",
		BaseURL:     " https://gateway.example.com/v1 ",
		APIKey:      "   ",
		Enabled:     &enabled,
		SortOrder:   7,
	})
	if err != nil {
		t.Fatalf("channelFromInput() error = %v", err)
	}
	if item.Key != "openrouter" || item.Adapter != AdapterOpenAICompatible {
		t.Fatalf("normalized key/adapter = %q/%q", item.Key, item.Adapter)
	}
	if item.BaseURL != "https://gateway.example.com/v1" {
		t.Fatalf("base_url = %q", item.BaseURL)
	}
	if item.Enabled {
		t.Fatal("enabled should preserve explicit false")
	}
	if replaceKey {
		t.Fatal("blank api_key should preserve existing stored key")
	}
}

func TestChannelService_RecreatedKeyDoesNotRestoreDeletedCredential(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()
	suffix := time.Now().UnixNano()
	channelKey := fmt.Sprintf("credential-channel-%d", suffix)
	repo := repository.NewChannelRepository(db)
	svc := NewChannelService(repo)
	enabled := true
	if _, err := svc.SaveAIChannel(&AIChannelInput{Key: channelKey, DisplayName: "Credential Channel", Adapter: AdapterOpenAICompatible, BaseURL: "https://example.test/v1", APIKey: "old-key", Enabled: &enabled}); err != nil {
		t.Fatalf("save channel: %v", err)
	}
	if err := svc.DeleteAIChannel(channelKey); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	if _, err := svc.SaveAIChannel(&AIChannelInput{Key: channelKey, DisplayName: "Recreated Channel", Adapter: AdapterOpenAICompatible, BaseURL: "https://example.test/v1", Enabled: &enabled}); err != nil {
		t.Fatalf("recreate channel: %v", err)
	}
	channel, err := repo.GetAIChannel(channelKey)
	if err != nil || channel == nil || channel.APIKeySet {
		t.Fatalf("recreated channel = %#v, err=%v; want no credential", channel, err)
	}

	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM ai_channels WHERE channel_key = $1", channelKey)
	})
}

func TestChannelServiceConcurrentExternalServiceSaveAllocatesUniqueSortOrders(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)

	svc := NewChannelService(repository.NewChannelRepository(db))
	inputs := []ExternalServiceInput{
		{Key: "tavily_search", DisplayName: "Tavily", Kind: ServiceKindSearch, APIKey: "tavily-key"},
		{Key: "brave_search", DisplayName: "Brave", Kind: ServiceKindSearch, APIKey: "brave-key"},
		{Key: "exa_search", DisplayName: "Exa", Kind: ServiceKindSearch, APIKey: "exa-key"},
		{Key: "bocha_search", DisplayName: "Bocha", Kind: ServiceKindSearch, APIKey: "bocha-key"},
	}
	start := make(chan struct{})
	results := make(chan error, len(inputs))
	var workers sync.WaitGroup
	for i := range inputs {
		input := inputs[i]
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := svc.SaveExternalServiceContext(context.Background(), &input)
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("save external service concurrently: %v", err)
		}
	}

	services, err := svc.ListExternalServices(true)
	if err != nil {
		t.Fatalf("list saved external services: %v", err)
	}
	orders := make(map[int]string, len(services))
	for _, item := range services {
		if item.Kind != ServiceKindSearch {
			continue
		}
		if previous := orders[item.SortOrder]; previous != "" {
			t.Fatalf("services %q and %q share sort order %d", previous, item.Key, item.SortOrder)
		}
		orders[item.SortOrder] = item.Key
	}
	for _, order := range []int{10, 20, 30, 40} {
		if orders[order] == "" {
			t.Fatalf("missing assigned sort order %d: %#v", order, orders)
		}
	}
}

func TestChannelServiceExternalServiceEditPreservesSortOrder(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	svc := NewChannelService(repository.NewChannelRepository(db))
	first, err := svc.SaveExternalService(&ExternalServiceInput{
		Key: "tavily_search", DisplayName: "Tavily", Kind: ServiceKindSearch, APIKey: "key", SortOrder: 900,
	})
	if err != nil {
		t.Fatalf("create external service: %v", err)
	}
	updated, err := svc.SaveExternalService(&ExternalServiceInput{
		Key: "tavily_search", DisplayName: "Tavily Updated", Kind: ServiceKindSearch, SortOrder: 700,
	})
	if err != nil {
		t.Fatalf("update external service: %v", err)
	}
	if first.SortOrder != 10 || updated.SortOrder != first.SortOrder {
		t.Fatalf("external service sort order changed from %d to %d", first.SortOrder, updated.SortOrder)
	}
}

func TestChannelServiceClassifiesInvalidExternalServiceOrder(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()

	svc := NewChannelService(repository.NewChannelRepository(db))
	if _, err := svc.SaveExternalService(&ExternalServiceInput{
		Key: "search_one", DisplayName: "Search One", Kind: ServiceKindSearch, BaseURL: "https://search.example.com",
	}); err != nil {
		t.Fatalf("create external service: %v", err)
	}
	if _, err := svc.ReorderExternalServices(ServiceKindSearch, []string{"missing"}); !errors.Is(err, ErrChannelInvalid) {
		t.Fatalf("reorder error = %v, want invalid channel configuration", err)
	}
}

func TestChannelServiceExternalServiceSaveHonorsLockCancellation(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(3)

	blocker, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("open advisory lock connection: %v", err)
	}
	defer blocker.Close()
	if _, err := blocker.ExecContext(context.Background(), `SELECT pg_advisory_lock(hashtext('effchat_external_service_order'), hashtext('search'))`); err != nil {
		t.Fatalf("hold external service order lock: %v", err)
	}
	defer func() {
		_, _ = blocker.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext('effchat_external_service_order'), hashtext('search'))`)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = NewChannelService(repository.NewChannelRepository(db)).SaveExternalServiceContext(ctx, &ExternalServiceInput{
		Key: "tavily_search", DisplayName: "Tavily", Kind: ServiceKindSearch, APIKey: "key",
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("save error = %v, want context deadline", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM external_services WHERE service_key = 'tavily_search'`).Scan(&count); err != nil {
		t.Fatalf("count canceled external service save: %v", err)
	}
	if count != 0 {
		t.Fatalf("canceled external service save committed %d rows", count)
	}
}

func TestChannelServiceExternalServiceReorderHonorsRowCancellation(t *testing.T) {
	db := setupMessageTestDB(t)
	defer db.Close()
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	svc := NewChannelService(repository.NewChannelRepository(db))
	key := fmt.Sprintf("context_reorder_%d", time.Now().UnixNano())
	if _, err := svc.SaveExternalServiceContext(context.Background(), &ExternalServiceInput{Key: key, DisplayName: "Context Reorder", Kind: ServiceKindSearch, APIKey: "fixture-key"}); err != nil {
		t.Fatalf("seed external service: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DELETE FROM external_services WHERE service_key = $1", key) })

	blocker, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin blocker transaction: %v", err)
	}
	defer blocker.Rollback()
	var lockedKey string
	if err := blocker.QueryRowContext(context.Background(), `SELECT service_key FROM external_services WHERE service_key = $1 FOR UPDATE`, key).Scan(&lockedKey); err != nil {
		t.Fatalf("lock external service row: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := svc.ReorderExternalServicesContext(ctx, ServiceKindSearch, []string{key}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("reorder error = %v, want context deadline", err)
	}
}

func TestChannelFromInputRejectsInvalidAdapter(t *testing.T) {
	_, _, err := channelFromInput(&AIChannelInput{
		Key:         "openai",
		DisplayName: "OpenAI",
		Adapter:     "openrouter",
		BaseURL:     "https://api.openai.com/v1",
	})
	if err == nil {
		t.Fatal("expected invalid adapter error")
	}
}

func TestChannelFromInputAcceptsOpenAIResponsesAdapter(t *testing.T) {
	item, _, err := channelFromInput(&AIChannelInput{
		Key:         "responses",
		DisplayName: "OpenAI Responses",
		Adapter:     AdapterOpenAIResponses,
		BaseURL:     "https://api.openai.com/v1/responses",
	})
	if err != nil {
		t.Fatalf("channelFromInput() error = %v", err)
	}
	if item.Adapter != AdapterOpenAIResponses || item.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("responses channel = %#v", item)
	}
}

func TestNormalizeOpenAICompatibleBaseURLTrimsConcreteEndpoints(t *testing.T) {
	tests := map[string]string{
		"https://api.openai.com/v1/responses":        "https://api.openai.com/v1",
		"https://api.openai.com/v1/chat/completions": "https://api.openai.com/v1",
		"https://api.openai.com/v1/models":           "https://api.openai.com/v1",
		"https://gateway.example.com":                "https://gateway.example.com",
	}
	for input, want := range tests {
		for _, adapter := range []string{AdapterOpenAICompatible, AdapterOpenAIResponses} {
			if got := NormalizeOpenAICompatibleBaseURL(adapter, input); got != want {
				t.Fatalf("NormalizeOpenAICompatibleBaseURL(%q, %q) = %q, want %q", adapter, input, got, want)
			}
		}
	}
}

func TestExternalServiceFromInputValidatesServiceSpecificFields(t *testing.T) {
	tests := []struct {
		name    string
		input   ExternalServiceInput
		wantErr bool
	}{
		{
			name: "searxng requires base url",
			input: ExternalServiceInput{
				Key:         "searxng",
				DisplayName: "SearXNG",
				Kind:        ServiceKindSearch,
			},
			wantErr: true,
		},
		{
			name: "tavily search can omit base url",
			input: ExternalServiceInput{
				Key:         "tavily_search",
				DisplayName: "Tavily",
				Kind:        ServiceKindSearch,
				APIKey:      "tvly-key",
			},
		},
		{
			name: "basic crawler is not admin configurable",
			input: ExternalServiceInput{
				Key:         "basic",
				DisplayName: "Basic crawler",
				Kind:        ServiceKindCrawler,
			},
			wantErr: true,
		},
		{
			name: "crawler cannot be registered as search",
			input: ExternalServiceInput{
				Key:         "firecrawl",
				DisplayName: "Firecrawl",
				Kind:        ServiceKindSearch,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := externalServiceFromInput(&tt.input)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestExternalServiceFromInputRejectsLegacyTavilyKey(t *testing.T) {
	_, _, err := externalServiceFromInput(&ExternalServiceInput{
		Key:         "tavily",
		DisplayName: "Tavily",
		Kind:        ServiceKindSearch,
		APIKey:      "tvly-key",
	})
	if err == nil {
		t.Fatal("expected legacy tavily key to be rejected")
	}
}

func TestBuildSearchRuntimeConfigUsesPersistedProviderOrder(t *testing.T) {
	services := []*model.ExternalService{
		{Key: "searxng", Kind: ServiceKindSearch, BaseURL: "https://searxng.example", APIKey: "", Enabled: true, SortOrder: 1},
		{Key: "jina", Kind: ServiceKindCrawler, BaseURL: "https://r.jina.ai", APIKey: "", Enabled: true, SortOrder: 2},
		{Key: "tavily_search", Kind: ServiceKindSearch, BaseURL: "", APIKey: "tvly-key", Enabled: true, SortOrder: 99},
		{Key: "basic", Kind: ServiceKindCrawler, Enabled: true, SortOrder: 3},
		{Key: "firecrawl", Kind: ServiceKindCrawler, BaseURL: "https://api.firecrawl.dev/v2", APIKey: "fc-key", Enabled: true, SortOrder: 100},
	}

	cfg := BuildSearchRuntimeConfig(services)
	if cfg.SearchProvider != "searxng" {
		t.Fatalf("SearchProvider = %q, want searxng", cfg.SearchProvider)
	}
	if !reflect.DeepEqual(cfg.SearchProviders, []string{"searxng", "tavily"}) {
		t.Fatalf("SearchProviders = %#v, want searxng,tavily", cfg.SearchProviders)
	}
	if cfg.CrawlerImpl != "jina" {
		t.Fatalf("CrawlerImpl = %q, want jina", cfg.CrawlerImpl)
	}
	if !reflect.DeepEqual(cfg.CrawlerProviders, []string{"jina", "firecrawl", "basic"}) {
		t.Fatalf("CrawlerProviders = %#v, want jina,firecrawl,basic", cfg.CrawlerProviders)
	}
}

func TestBuildSearchRuntimeConfigKeepsSearchAndExtractKeysIndependent(t *testing.T) {
	cfg := BuildSearchRuntimeConfig([]*model.ExternalService{
		{Key: "tavily_search", Kind: ServiceKindSearch, APIKey: "search-key", BaseURL: "https://search.example", Enabled: true, SortOrder: 20},
		{Key: "exa_search", Kind: ServiceKindSearch, APIKey: "exa-search-key", BaseURL: "https://exa.example/search", Enabled: true, SortOrder: 10},
		{Key: "tavily_extract", Kind: ServiceKindCrawler, APIKey: "extract-key", BaseURL: "https://extract.example", Enabled: true, SortOrder: 10},
		{Key: "exa_extract", Kind: ServiceKindCrawler, APIKey: "exa-extract-key", BaseURL: "https://exa.example/contents", Enabled: true, SortOrder: 20},
	})
	if !reflect.DeepEqual(cfg.SearchProviders, []string{"exa", "tavily"}) {
		t.Fatalf("SearchProviders = %#v", cfg.SearchProviders)
	}
	if cfg.TavilySearchAPIKey != "search-key" || cfg.TavilyExtractAPIKey != "extract-key" {
		t.Fatalf("tavily keys should remain independent: %#v", cfg)
	}
	if !reflect.DeepEqual(cfg.CrawlerProviders, []string{"tavily", "exa", "basic"}) {
		t.Fatalf("CrawlerProviders = %#v", cfg.CrawlerProviders)
	}
}

func TestBuildSearchRuntimeConfigIgnoresLegacyTavilyKey(t *testing.T) {
	cfg := BuildSearchRuntimeConfig([]*model.ExternalService{
		{Key: "tavily", Kind: ServiceKindSearch, APIKey: "legacy-key", Enabled: true},
		{Key: "searxng", Kind: ServiceKindSearch, BaseURL: "https://searxng.example", Enabled: true},
	})

	if !reflect.DeepEqual(cfg.SearchProviders, []string{"searxng"}) {
		t.Fatalf("SearchProviders = %#v, want only searxng", cfg.SearchProviders)
	}
}

func TestBuildSearchRuntimeConfigAllowsKeylessSearXNGOnly(t *testing.T) {
	cfg := BuildSearchRuntimeConfig([]*model.ExternalService{
		{Key: "searxng", Kind: ServiceKindSearch, BaseURL: "https://searxng.example", Enabled: true},
		{Key: "tavily_search", Kind: ServiceKindSearch, BaseURL: "https://api.tavily.com/search", Enabled: true},
	})

	if cfg.SearchProvider != "searxng" {
		t.Fatalf("SearchProvider = %q, want searxng", cfg.SearchProvider)
	}
	if !reflect.DeepEqual(cfg.SearchProviders, []string{"searxng"}) {
		t.Fatalf("SearchProviders = %#v, want only searxng", cfg.SearchProviders)
	}
}

func TestBuildSearchRuntimeConfigSkipsDisabledServices(t *testing.T) {
	cfg := BuildSearchRuntimeConfig([]*model.ExternalService{
		{Key: "tavily_search", Kind: ServiceKindSearch, APIKey: "tvly-key", Enabled: false},
		{Key: "searxng", Kind: ServiceKindSearch, BaseURL: "https://searxng.example", Enabled: true},
		{Key: "firecrawl", Kind: ServiceKindCrawler, APIKey: "fc-key", Enabled: false},
		{Key: "jina", Kind: ServiceKindCrawler, BaseURL: "https://r.jina.ai", Enabled: true},
	})

	if cfg.SearchProvider != "searxng" {
		t.Fatalf("SearchProvider = %q, want searxng", cfg.SearchProvider)
	}
	if !reflect.DeepEqual(cfg.SearchProviders, []string{"searxng"}) {
		t.Fatalf("SearchProviders = %#v, want only searxng", cfg.SearchProviders)
	}
	if cfg.CrawlerImpl != "jina" {
		t.Fatalf("CrawlerImpl = %q, want jina", cfg.CrawlerImpl)
	}
	if !reflect.DeepEqual(cfg.CrawlerProviders, []string{"jina", "basic"}) {
		t.Fatalf("CrawlerProviders = %#v, want jina,basic", cfg.CrawlerProviders)
	}
}

func TestBuildSearchRuntimeConfigAlwaysEnablesBasicCrawlerFallback(t *testing.T) {
	cfg := BuildSearchRuntimeConfig(nil)

	if cfg.CrawlerImpl != "basic" {
		t.Fatalf("CrawlerImpl = %q, want basic", cfg.CrawlerImpl)
	}
	if !reflect.DeepEqual(cfg.CrawlerProviders, []string{"basic"}) {
		t.Fatalf("CrawlerProviders = %#v, want only basic", cfg.CrawlerProviders)
	}
}

func TestCanReuseExternalServiceCredentialRequiresSameEndpoint(t *testing.T) {
	saved := &model.ExternalService{Key: "tavily_search", Kind: ServiceKindSearch, BaseURL: "https://api.example/v1/", APIKey: "saved-key"}
	if !CanReuseExternalServiceCredential(saved, &model.ExternalService{Key: "tavily_search", Kind: ServiceKindSearch, BaseURL: "https://api.example/v1"}) {
		t.Fatal("unchanged endpoint should reuse its saved credential")
	}
	if CanReuseExternalServiceCredential(saved, &model.ExternalService{Key: "tavily_search", Kind: ServiceKindSearch, BaseURL: "https://other.example/v1"}) {
		t.Fatal("a changed endpoint must not receive the saved credential")
	}
	if !CanReuseExternalServiceCredential(&model.ExternalService{Key: "tavily_search", Kind: ServiceKindSearch}, &model.ExternalService{Key: "tavily_search", Kind: ServiceKindSearch, BaseURL: "https://api.tavily.com/search"}) {
		t.Fatal("an explicit default endpoint should retain the saved credential")
	}
}
