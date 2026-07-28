package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/huoguojun123/effchat/internal/model"
	"github.com/huoguojun123/effchat/internal/modelbank"
	"github.com/huoguojun123/effchat/internal/service"
)

// modelsDevURL 是 models.dev 公布的全量模型能力目录（社区维护，约 2MB）。
// 区别于网关 /v1/models：网关只给模型 ID，能力靠推断；models.dev 提供精确的
// vision/tool_use/reasoning/context/output 元数据。
const modelsDevURL = "https://models.dev/api.json"
const modelsDevCatalogMaxBytes = 8 << 20

type modelsDevProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	Models map[string]modelsDevModel `json:"models"`
}

type modelsDevModel struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Reasoning  bool   `json:"reasoning"`
	ToolCall   bool   `json:"tool_call"`
	Modalities struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
}

// ListModelsDevCatalogHandler 从 models.dev 拉取目录并映射为可导入的模型列表（admin）。
// 已存在于库中的模型回显 DB 记录（保留管理员的本地编辑），新模型给出 enabled=false 的草稿。
func ListModelsDevCatalogHandler(modelService *service.ModelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		catalog, err := fetchModelsDevCatalog(c.Request.Context())
		if err != nil {
			writeServerError(c, http.StatusBadGateway, "models_dev_catalog_fetch_failed", "failed to fetch models.dev catalog", err)
			return
		}
		existing, err := modelService.List(false)
		if err != nil {
			writeServerError(c, http.StatusInternalServerError, "models_dev_local_models_load_failed", "failed to load local models", err)
			return
		}
		models := modelsDevCatalogModels(catalog, existing)

		sort.Slice(models, func(i, j int) bool {
			if models[i].Provider != models[j].Provider {
				return models[i].Provider < models[j].Provider
			}
			return models[i].ID < models[j].ID
		})

		c.JSON(http.StatusOK, gin.H{"models": models, "total": len(models)})
	}
}

func modelsDevCatalogModels(catalog map[string]modelsDevProvider, existing []*model.Model) []*model.Model {
	existingByID := make(map[string]*model.Model, len(existing))
	for _, item := range existing {
		if item != nil {
			existingByID[item.ID] = item
		}
	}

	models := make([]*model.Model, 0, 128)
	index := 0
	for _, provider := range sortedCatalogProviders(catalog) {
		p, ok := catalog[provider]
		if !ok {
			continue
		}
		for id, meta := range p.Models {
			if item := existingByID[id]; item != nil {
				models = append(models, item)
				continue
			}
			models = append(models, modelsDevToModel(provider, id, meta, index))
			index++
		}
	}
	return models
}

// fetchModelsDevCatalog 拉取并解析 models.dev 目录。
func fetchModelsDevCatalog(ctx context.Context) (map[string]modelsDevProvider, error) {
	return fetchModelsDevCatalogFrom(ctx, &http.Client{Timeout: 30 * time.Second}, modelsDevURL)
}

func fetchModelsDevCatalogFrom(ctx context.Context, client *http.Client, url string) (map[string]modelsDevProvider, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cached := cachedCatalog(); cached != nil {
		return cached, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return staleCatalogOrError(fmt.Errorf("failed to fetch models.dev catalog: %w", err))
	}
	defer resp.Body.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return staleCatalogOrError(fmt.Errorf("failed to fetch models.dev catalog: status %d", resp.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, modelsDevCatalogMaxBytes+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return staleCatalogOrError(fmt.Errorf("failed to read models.dev catalog: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(body) > modelsDevCatalogMaxBytes {
		return staleCatalogOrError(fmt.Errorf("models.dev catalog exceeds %d bytes", modelsDevCatalogMaxBytes))
	}
	var catalog map[string]modelsDevProvider
	if err := json.Unmarshal(body, &catalog); err != nil {
		return staleCatalogOrError(fmt.Errorf("failed to decode models.dev catalog: %w", err))
	}
	if len(catalog) == 0 {
		return staleCatalogOrError(fmt.Errorf("models.dev catalog is empty"))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	storeCatalog(catalog)
	return catalog, nil
}

// models.dev 全量目录约 2MB，进程内缓存避免每次编辑/导入都重拉。
var (
	catalogCacheMu  sync.RWMutex
	catalogCache    map[string]modelsDevProvider
	catalogCachedAt time.Time
)

const catalogCacheTTL = 10 * time.Minute

func cachedCatalog() map[string]modelsDevProvider {
	catalogCacheMu.RLock()
	defer catalogCacheMu.RUnlock()
	if catalogCache == nil || time.Since(catalogCachedAt) > catalogCacheTTL {
		return nil
	}
	return catalogCache
}

func staleCatalog() map[string]modelsDevProvider {
	catalogCacheMu.RLock()
	defer catalogCacheMu.RUnlock()
	return catalogCache
}

func staleCatalogOrError(err error) (map[string]modelsDevProvider, error) {
	if cached := staleCatalog(); cached != nil {
		return cached, nil
	}
	return nil, err
}

func storeCatalog(catalog map[string]modelsDevProvider) {
	catalogCacheMu.Lock()
	defer catalogCacheMu.Unlock()
	catalogCache = catalog
	catalogCachedAt = time.Now()
}

// GetModelsDevCatalogModelHandler 按 ID 返回 models.dev 的【原始能力】（admin）。
// 区别于列表接口：本接口不回显 DB 记录，专供编辑面板用 models.dev 的精确能力刷新字段，
// 即使该模型已存在于库中，也返回目录里的能力值供管理员确认后覆盖。
func GetModelsDevCatalogModelHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(modelIDParam(c))
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "model id is required"})
			return
		}
		provider := strings.ToLower(strings.TrimSpace(c.Query("provider")))

		catalog, err := fetchModelsDevCatalog(c.Request.Context())
		if err != nil {
			writeServerError(c, http.StatusBadGateway, "models_dev_catalog_fetch_failed", "failed to fetch models.dev catalog", err)
			return
		}

		if provider != "" {
			p, ok := catalog[provider]
			if !ok {
				c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("provider %q not found in models.dev catalog", provider)})
				return
			}
			if meta, ok := p.Models[id]; ok {
				c.JSON(http.StatusOK, gin.H{"model": modelsDevToModel(provider, id, meta, 0)})
				return
			}
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("model %q not found in models.dev catalog provider %q", id, provider)})
			return
		}

		for _, provider := range sortedCatalogProviders(catalog) {
			p, ok := catalog[provider]
			if !ok {
				continue
			}
			if meta, ok := p.Models[id]; ok {
				c.JSON(http.StatusOK, gin.H{"model": modelsDevToModel(provider, id, meta, 0)})
				return
			}
		}

		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("model %q not found in models.dev catalog", id)})
	}
}

func sortedCatalogProviders(catalog map[string]modelsDevProvider) []string {
	providers := make([]string, 0, len(catalog))
	for provider := range catalog {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	return providers
}

// modelsDevToModel 把 models.dev 的条目映射为本项目的 model.Model。
// 能力字段直接采用 models.dev 的精确值；search_impl 是本项目特有概念，按 provider 补默认。
func modelsDevToModel(provider, id string, meta modelsDevModel, index int) *model.Model {
	display := meta.Name
	if display == "" {
		display = inferDisplayName(id)
	}
	m := &model.Model{
		ID:             id,
		DisplayName:    display,
		Provider:       provider,
		Enabled:        false,
		SortOrder:      2000 + index,
		ContextWindow:  meta.Limit.Context,
		MaxOutput:      meta.Limit.Output,
		Vision:         modalitiesHaveImage(meta.Modalities.Input),
		ToolUse:        meta.ToolCall,
		Reasoning:      meta.Reasoning,
		ThinkingFormat: modelbank.NormalizeThinkingFormat(""),
		SearchImpl:     searchImplForProvider(provider),
	}
	return modelbank.ApplyThinkingRuntimeMetadata(m)
}

// modalitiesHaveImage 判断输入模态是否含图像 → 对应本项目的 vision 能力。
func modalitiesHaveImage(input []string) bool {
	for _, mod := range input {
		if strings.EqualFold(mod, "image") {
			return true
		}
	}
	return false
}

// searchImplForProvider 返回 provider 默认的联网搜索实现方式。
func searchImplForProvider(provider string) string {
	switch provider {
	case "anthropic":
		return "tool"
	case "google":
		return "params"
	case "perplexity":
		return "internal"
	default:
		return ""
	}
}
