package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
)

var (
	ErrChannelInvalid     = errors.New("invalid channel configuration")
	ErrChannelNotFound    = errors.New("channel not found")
	ErrChannelUnavailable = errors.New("channel unavailable")
)

const (
	AdapterOpenAICompatible = "openai_compatible"
	AdapterOpenAIResponses  = "openai_responses"
	AdapterAnthropic        = "anthropic"
	AdapterGoogle           = "google"

	ServiceKindSearch  = "search"
	ServiceKindCrawler = "crawler"
	ServiceKindOCR     = "ocr"
)

type ChannelService struct {
	repo *repository.ChannelRepository
}

func NewChannelService(repo *repository.ChannelRepository) *ChannelService {
	return &ChannelService{repo: repo}
}

type AIChannelInput struct {
	Key         string `json:"key" binding:"required"`
	DisplayName string `json:"display_name" binding:"required"`
	Adapter     string `json:"adapter" binding:"required"`
	BaseURL     string `json:"base_url"`
	APIKey      string `json:"api_key"`
	Enabled     *bool  `json:"enabled"`
	SortOrder   int    `json:"sort_order"`
}

type ExternalServiceInput struct {
	Key            string `json:"key" binding:"required"`
	DisplayName    string `json:"display_name" binding:"required"`
	Kind           string `json:"kind" binding:"required"`
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	Enabled        *bool  `json:"enabled"`
	SortOrder      int    `json:"sort_order"`
	MaxConcurrency int    `json:"max_concurrency"`
}

func (s *ChannelService) ListAIChannels(includeDisabled bool) ([]*model.AIChannel, error) {
	return s.ListAIChannelsContext(context.Background(), includeDisabled)
}

func (s *ChannelService) ListAIChannelsContext(ctx context.Context, includeDisabled bool) ([]*model.AIChannel, error) {
	return s.repo.ListAIChannelsContext(ctx, includeDisabled)
}

func (s *ChannelService) GetAIChannel(key string) (*model.AIChannel, error) {
	return s.GetAIChannelContext(context.Background(), key)
}

func (s *ChannelService) GetAIChannelContext(ctx context.Context, key string) (*model.AIChannel, error) {
	return s.repo.GetAIChannelContext(ctx, normalizeKey(key))
}

func (s *ChannelService) SaveAIChannel(input *AIChannelInput) (*model.AIChannel, error) {
	item, replaceKey, err := channelFromInput(input)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrChannelInvalid, err)
	}
	if err := s.repo.UpsertAIChannel(item, replaceKey); err != nil {
		return nil, err
	}
	return s.repo.GetAIChannel(item.Key)
}

func (s *ChannelService) DeleteAIChannel(key string) error {
	err := s.repo.DeleteAIChannel(key)
	if errors.Is(err, repository.ErrNotFound) {
		return ErrChannelNotFound
	}
	return err
}

func (s *ChannelService) ResolveAIChannel(key string) (*model.AIChannel, error) {
	return s.ResolveAIChannelContext(context.Background(), key)
}

func (s *ChannelService) ResolveAIChannelContext(ctx context.Context, key string) (*model.AIChannel, error) {
	key = normalizeKey(key)
	if key == "" {
		return nil, fmt.Errorf("%w: channel is required", ErrChannelInvalid)
	}
	item, err := s.repo.GetAIChannelContext(ctx, key)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrChannelNotFound
	}
	if !item.Enabled {
		return nil, ErrChannelUnavailable
	}
	if strings.TrimSpace(item.APIKey) == "" {
		return nil, ErrChannelUnavailable
	}
	return item, nil
}

func (s *ChannelService) ListExternalServices(includeDisabled bool) ([]*model.ExternalService, error) {
	return s.ListExternalServicesContext(context.Background(), includeDisabled)
}

func (s *ChannelService) ListExternalServicesContext(ctx context.Context, includeDisabled bool) ([]*model.ExternalService, error) {
	return s.repo.ListExternalServicesContext(ctx, includeDisabled)
}

func (s *ChannelService) GetExternalService(key string) (*model.ExternalService, error) {
	return s.GetExternalServiceContext(context.Background(), key)
}

func (s *ChannelService) GetExternalServiceContext(ctx context.Context, key string) (*model.ExternalService, error) {
	return s.repo.GetExternalServiceContext(ctx, normalizeKey(key))
}

func (s *ChannelService) SaveExternalService(input *ExternalServiceInput) (*model.ExternalService, error) {
	return s.SaveExternalServiceContext(context.Background(), input)
}

func (s *ChannelService) SaveExternalServiceContext(ctx context.Context, input *ExternalServiceInput) (*model.ExternalService, error) {
	item, replaceKey, err := externalServiceFromInput(input)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrChannelInvalid, err)
	}
	if err := s.repo.SaveExternalServiceContext(ctx, item, replaceKey); err != nil {
		return nil, err
	}
	return s.repo.GetExternalServiceContext(ctx, item.Key)
}

func ValidateExternalService(input *ExternalServiceInput) (*model.ExternalService, error) {
	item, _, err := externalServiceFromInput(input)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrChannelInvalid, err)
	}
	return item, nil
}

func CanReuseExternalServiceCredential(saved, candidate *model.ExternalService) bool {
	if saved == nil || candidate == nil {
		return false
	}
	return saved.Key == candidate.Key && saved.Kind == candidate.Kind && effectiveExternalServiceBaseURL(saved.Key, saved.BaseURL) == effectiveExternalServiceBaseURL(candidate.Key, candidate.BaseURL)
}

func (s *ChannelService) ReorderExternalServices(kind string, keys []string) ([]*model.ExternalService, error) {
	return s.ReorderExternalServicesContext(context.Background(), kind, keys)
}

func (s *ChannelService) ReorderExternalServicesContext(ctx context.Context, kind string, keys []string) ([]*model.ExternalService, error) {
	kind = normalizeKey(kind)
	if kind != ServiceKindSearch && kind != ServiceKindCrawler {
		return nil, fmt.Errorf("%w: invalid service kind", ErrChannelInvalid)
	}
	if err := s.repo.ReorderExternalServicesContext(ctx, kind, keys); err != nil {
		if errors.Is(err, repository.ErrExternalServiceOrderInvalid) {
			return nil, fmt.Errorf("%w: %v", ErrChannelInvalid, err)
		}
		return nil, err
	}
	return s.repo.ListExternalServicesContext(ctx, true)
}

func (s *ChannelService) DeleteExternalService(key string) error {
	err := s.repo.DeleteExternalService(key)
	if errors.Is(err, repository.ErrNotFound) {
		return ErrChannelNotFound
	}
	return err
}

type SearchRuntimeConfig struct {
	SearchProvider      string
	SearchProviders     []string
	SearXNGURL          string
	TavilySearchAPIKey  string
	TavilySearchURL     string
	BraveSearchAPIKey   string
	BraveSearchURL      string
	ExaSearchAPIKey     string
	ExaSearchURL        string
	BochaSearchAPIKey   string
	BochaSearchURL      string
	CrawlerImpl         string
	CrawlerProviders    []string
	FirecrawlAPIKey     string
	FirecrawlBaseURL    string
	JinaAPIKey          string
	JinaBaseURL         string
	TavilyExtractAPIKey string
	TavilyExtractURL    string
	ExaExtractAPIKey    string
	ExaExtractURL       string
}

type MinerUOCRConfig struct {
	Enabled        bool
	BaseURL        string
	APIKey         string
	MaxConcurrency int
}

func (s *ChannelService) ResolveSearchRuntimeConfig() SearchRuntimeConfig {
	cfg, _ := s.ResolveSearchRuntimeConfigContext(context.Background())
	return cfg
}

func (s *ChannelService) ResolveSearchRuntimeConfigWithState() (SearchRuntimeConfig, SearchRuntimeConfigState) {
	cfg, state, _ := s.ResolveSearchRuntimeConfigWithStateContext(context.Background())
	return cfg, state
}

func (s *ChannelService) ResolveSearchRuntimeConfigContext(ctx context.Context) (SearchRuntimeConfig, error) {
	cfg, _, err := s.ResolveSearchRuntimeConfigWithStateContext(ctx)
	return cfg, err
}

func (s *ChannelService) ResolveSearchRuntimeConfigWithStateContext(ctx context.Context) (SearchRuntimeConfig, SearchRuntimeConfigState, error) {
	if err := ctx.Err(); err != nil {
		return SearchRuntimeConfig{}, SearchRuntimeConfigState{}, err
	}
	if s == nil || s.repo == nil {
		cfg, state := BuildSearchRuntimeConfigWithState(nil)
		state.Search = runtimeConfigState(RuntimeStateUnavailable, "repository_unavailable", runtimeConfigVersion("external:search:unavailable", nil))
		state.Extract = runtimeConfigState(RuntimeStateUnavailable, "repository_unavailable", runtimeConfigVersion("external:crawler:unavailable", nil))
		return cfg, state, nil
	}
	services, err := s.repo.ListExternalServicesContext(ctx, true)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return SearchRuntimeConfig{}, SearchRuntimeConfigState{}, ctxErr
		}
		cfg, state := BuildSearchRuntimeConfigWithState(nil)
		state.Search = runtimeConfigState(RuntimeStateUnavailable, "repository_unavailable", runtimeConfigVersion("external:search:unavailable", nil))
		state.Extract = runtimeConfigState(RuntimeStateUnavailable, "repository_unavailable", runtimeConfigVersion("external:crawler:unavailable", nil))
		return cfg, state, nil
	}
	cfg, state := BuildSearchRuntimeConfigWithState(services)
	return cfg, state, nil
}

func (s *ChannelService) ResolveMinerUOCRConfig() MinerUOCRConfig {
	config, _ := s.ResolveMinerUOCRConfigContext(context.Background())
	return config
}

// ResolveMinerUOCRConfigContext preserves repository failures for HTTP
// admission paths that must distinguish an intentionally disabled service from
// a temporarily unreadable control plane. Background recovery keeps using the
// compatibility wrapper above and treats either case as fail-closed.
func (s *ChannelService) ResolveMinerUOCRConfigContext(ctx context.Context) (MinerUOCRConfig, error) {
	if s == nil || s.repo == nil {
		return MinerUOCRConfig{}, errors.New("MinerU channel repository is unavailable")
	}
	item, err := s.repo.GetExternalServiceContext(ctx, "mineru")
	if err != nil {
		return MinerUOCRConfig{}, fmt.Errorf("resolve MinerU OCR config: %w", err)
	}
	if item == nil || !item.Enabled || strings.TrimSpace(item.APIKey) == "" {
		return MinerUOCRConfig{}, nil
	}
	baseURL := strings.TrimSpace(item.BaseURL)
	if baseURL == "" {
		baseURL = "https://mineru.net"
	}
	concurrency := item.MaxConcurrency
	if concurrency <= 0 {
		concurrency = 2
	}
	return MinerUOCRConfig{Enabled: true, BaseURL: baseURL, APIKey: item.APIKey, MaxConcurrency: concurrency}, nil
}

func BuildSearchRuntimeConfig(services []*model.ExternalService) SearchRuntimeConfig {
	cfg, _ := BuildSearchRuntimeConfigWithState(services)
	return cfg
}

func BuildSearchRuntimeConfigWithState(services []*model.ExternalService) (SearchRuntimeConfig, SearchRuntimeConfigState) {
	cfg := SearchRuntimeConfig{}
	ordered := make([]*model.ExternalService, 0, len(services))
	for _, svc := range services {
		if svc == nil || !svc.Enabled {
			continue
		}
		ordered = append(ordered, svc)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].SortOrder == ordered[j].SortOrder {
			return ordered[i].Key < ordered[j].Key
		}
		return ordered[i].SortOrder < ordered[j].SortOrder
	})
	for _, svc := range ordered {
		key := normalizeKey(svc.Key)
		switch {
		case svc.Kind == ServiceKindSearch && key == "tavily_search" && strings.TrimSpace(svc.APIKey) != "":
			cfg.SearchProviders = append(cfg.SearchProviders, "tavily")
			cfg.TavilySearchAPIKey, cfg.TavilySearchURL = svc.APIKey, svc.BaseURL
		case svc.Kind == ServiceKindSearch && key == "brave_search" && strings.TrimSpace(svc.APIKey) != "":
			cfg.SearchProviders = append(cfg.SearchProviders, "brave")
			cfg.BraveSearchAPIKey, cfg.BraveSearchURL = svc.APIKey, svc.BaseURL
		case svc.Kind == ServiceKindSearch && key == "exa_search" && strings.TrimSpace(svc.APIKey) != "":
			cfg.SearchProviders = append(cfg.SearchProviders, "exa")
			cfg.ExaSearchAPIKey, cfg.ExaSearchURL = svc.APIKey, svc.BaseURL
		case svc.Kind == ServiceKindSearch && key == "bocha_search" && strings.TrimSpace(svc.APIKey) != "":
			cfg.SearchProviders = append(cfg.SearchProviders, "bocha")
			cfg.BochaSearchAPIKey, cfg.BochaSearchURL = svc.APIKey, svc.BaseURL
		case svc.Kind == ServiceKindSearch && key == "searxng" && strings.TrimSpace(svc.BaseURL) != "":
			cfg.SearchProviders = append(cfg.SearchProviders, "searxng")
			cfg.SearXNGURL = svc.BaseURL
		case svc.Kind == ServiceKindCrawler && key == "firecrawl" && strings.TrimSpace(svc.APIKey) != "":
			cfg.CrawlerProviders = append(cfg.CrawlerProviders, "firecrawl")
			cfg.FirecrawlAPIKey, cfg.FirecrawlBaseURL = svc.APIKey, svc.BaseURL
		case svc.Kind == ServiceKindCrawler && key == "jina":
			cfg.CrawlerProviders = append(cfg.CrawlerProviders, "jina")
			cfg.JinaAPIKey, cfg.JinaBaseURL = svc.APIKey, svc.BaseURL
		case svc.Kind == ServiceKindCrawler && key == "tavily_extract" && strings.TrimSpace(svc.APIKey) != "":
			cfg.CrawlerProviders = append(cfg.CrawlerProviders, "tavily")
			cfg.TavilyExtractAPIKey, cfg.TavilyExtractURL = svc.APIKey, svc.BaseURL
		case svc.Kind == ServiceKindCrawler && key == "exa_extract" && strings.TrimSpace(svc.APIKey) != "":
			cfg.CrawlerProviders = append(cfg.CrawlerProviders, "exa")
			cfg.ExaExtractAPIKey, cfg.ExaExtractURL = svc.APIKey, svc.BaseURL
		}
	}
	if len(cfg.SearchProviders) > 0 {
		cfg.SearchProvider = cfg.SearchProviders[0]
	}
	// External crawlers keep the exact administrator-defined order. Basic is
	// not persisted as an external service and remains the final local fallback.
	cfg.CrawlerProviders = append(cfg.CrawlerProviders, "basic")
	cfg.CrawlerImpl = cfg.CrawlerProviders[0]
	return cfg, SearchRuntimeConfigState{
		Search:  externalServiceRuntimeState(ServiceKindSearch, services, len(cfg.SearchProviders) > 0),
		Extract: externalServiceRuntimeState(ServiceKindCrawler, services, len(cfg.CrawlerProviders) > 1),
	}
}

func externalServiceRuntimeState(kind string, services []*model.ExternalService, ready bool) RuntimeConfigState {
	version := externalServiceVersion(kind, services)
	configured, enabled := false, false
	for _, item := range services {
		if item == nil || item.Kind != kind {
			continue
		}
		configured = true
		enabled = enabled || item.Enabled
	}
	if ready {
		return runtimeConfigState(RuntimeStateReady, "", version)
	}
	if configured && !enabled {
		return runtimeConfigState(RuntimeStateDisabled, "administrator_disabled", version)
	}
	return runtimeConfigState(RuntimeStateUnconfigured, "not_configured", version)
}

func channelFromInput(input *AIChannelInput) (*model.AIChannel, bool, error) {
	if input == nil {
		return nil, false, fmt.Errorf("channel input is required")
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	item := &model.AIChannel{
		Key:         normalizeKey(input.Key),
		DisplayName: strings.TrimSpace(input.DisplayName),
		Adapter:     normalizeKey(input.Adapter),
		BaseURL:     NormalizeOpenAICompatibleBaseURL(input.Adapter, input.BaseURL),
		APIKey:      strings.TrimSpace(input.APIKey),
		Enabled:     enabled,
		SortOrder:   input.SortOrder,
	}
	if item.Key == "" {
		return nil, false, fmt.Errorf("key is required")
	}
	if item.DisplayName == "" {
		return nil, false, fmt.Errorf("display_name is required")
	}
	if !validAdapter(item.Adapter) {
		return nil, false, fmt.Errorf("invalid adapter")
	}
	if item.BaseURL == "" {
		return nil, false, fmt.Errorf("base_url is required")
	}
	return item, item.APIKey != "", nil
}

func externalServiceFromInput(input *ExternalServiceInput) (*model.ExternalService, bool, error) {
	if input == nil {
		return nil, false, fmt.Errorf("external service input is required")
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	item := &model.ExternalService{
		Key:            normalizeKey(input.Key),
		DisplayName:    strings.TrimSpace(input.DisplayName),
		Kind:           normalizeKey(input.Kind),
		BaseURL:        strings.TrimSpace(input.BaseURL),
		APIKey:         strings.TrimSpace(input.APIKey),
		Enabled:        enabled,
		SortOrder:      input.SortOrder,
		MaxConcurrency: input.MaxConcurrency,
	}
	if item.Key == "" {
		return nil, false, fmt.Errorf("key is required")
	}
	if item.DisplayName == "" {
		return nil, false, fmt.Errorf("display_name is required")
	}
	if item.Kind != ServiceKindSearch && item.Kind != ServiceKindCrawler && item.Kind != ServiceKindOCR {
		return nil, false, fmt.Errorf("invalid service kind")
	}
	if !validExternalService(item.Key, item.Kind) {
		return nil, false, fmt.Errorf("invalid external service")
	}
	if item.Key == "searxng" && item.BaseURL == "" {
		return nil, false, fmt.Errorf("base_url is required")
	}
	if item.Key == "mineru" {
		if item.BaseURL == "" {
			item.BaseURL = "https://mineru.net"
		}
		if item.MaxConcurrency <= 0 {
			item.MaxConcurrency = 2
		}
		if item.MaxConcurrency > 20 {
			item.MaxConcurrency = 20
		}
	}
	return item, item.APIKey != "", nil
}

func validAdapter(adapter string) bool {
	switch adapter {
	case AdapterOpenAICompatible, AdapterOpenAIResponses, AdapterAnthropic, AdapterGoogle:
		return true
	default:
		return false
	}
}

func validExternalService(key, kind string) bool {
	switch key {
	case "searxng", "tavily_search", "brave_search", "exa_search", "bocha_search":
		return kind == ServiceKindSearch
	case "firecrawl", "jina", "tavily_extract", "exa_extract":
		return kind == ServiceKindCrawler
	case "mineru":
		return kind == ServiceKindOCR
	default:
		return false
	}
}

func normalizeKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeServiceBaseURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func effectiveExternalServiceBaseURL(key, value string) string {
	if baseURL := normalizeServiceBaseURL(value); baseURL != "" {
		return baseURL
	}
	switch normalizeKey(key) {
	case "tavily_search":
		return "https://api.tavily.com/search"
	case "brave_search":
		return "https://api.search.brave.com/res/v1/web/search"
	case "exa_search":
		return "https://api.exa.ai/search"
	case "bocha_search":
		return "https://api.bochaai.com/v1/web-search"
	case "firecrawl":
		return "https://api.firecrawl.dev/v2"
	case "jina":
		return "https://r.jina.ai"
	case "tavily_extract":
		return "https://api.tavily.com/extract"
	case "exa_extract":
		return "https://api.exa.ai/contents"
	default:
		return ""
	}
}

// NormalizeOpenAICompatibleBaseURL keeps OpenAI-family channel base URLs at the API root.
// Admins sometimes paste concrete OpenAI endpoints such as /responses,
// /chat/completions, or /models. Both Eino OpenAI adapters expect the root
// ending at /v1, and the model-list probe appends /models itself.
func NormalizeOpenAICompatibleBaseURL(adapter string, raw string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	adapter = normalizeKey(adapter)
	if adapter != AdapterOpenAICompatible && adapter != AdapterOpenAIResponses {
		return base
	}
	for {
		lower := strings.ToLower(base)
		trimmed := false
		for _, suffix := range []string{"/chat/completions", "/responses", "/models"} {
			if strings.HasSuffix(lower, suffix) {
				base = strings.TrimRight(base[:len(base)-len(suffix)], "/")
				trimmed = true
				break
			}
		}
		if !trimmed {
			return base
		}
	}
}
