import { describe, expect, it, vi } from "vitest";
import { adminApi } from "@/api/admin";
import { api } from "@/api/client";

vi.mock("@/api/client", () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
    upload: vi.fn(),
  },
}));

describe("adminApi model paths", () => {
  it("encodes model ids containing slashes for update and delete", () => {
    adminApi.updateModel("deepseek-ai/DeepSeek-V4-Flash", { enabled: true });
    adminApi.deleteModel("deepseek-ai/DeepSeek-V4-Flash");

    expect(api.patch).toHaveBeenCalledWith(
      "/admin/models/deepseek-ai%2FDeepSeek-V4-Flash",
      { enabled: true },
    );
    expect(api.delete).toHaveBeenCalledWith(
      "/admin/models/deepseek-ai%2FDeepSeek-V4-Flash",
    );
  });
});

describe("adminApi usage paths", () => {
  it("requests usage with encoded range", () => {
    adminApi.getUsage("30d");

    expect(api.get).toHaveBeenCalledWith("/admin/usage?range=30d");
  });

  it("requests usage with a right-open custom range", () => {
    adminApi.getUsage({ start_at: "2026-07-01T00:00:00+08:00", end_at: "2026-07-08T00:00:00+08:00" });

    expect(api.get).toHaveBeenCalledWith(
      "/admin/usage?start_at=2026-07-01T00%3A00%3A00%2B08%3A00&end_at=2026-07-08T00%3A00%3A00%2B08%3A00",
    );
  });
});

describe("adminApi system status path", () => {
  it("requests the current deployment instance status", () => {
    adminApi.getSystemStatus();

    expect(api.get).toHaveBeenCalledWith("/admin/system/status");
  });
});

describe("adminApi catalog paths", () => {
  it("requests the full models.dev catalog", () => {
    adminApi.listCatalogModels();

    expect(api.get).toHaveBeenCalledWith("/admin/models/catalog");
  });

  it("requests a provider-scoped catalog model", () => {
    adminApi.getCatalogModel("deepseek-v4-flash", "deepseek");

    expect(api.get).toHaveBeenCalledWith(
      "/admin/models/catalog/deepseek-v4-flash?provider=deepseek",
    );
  });
});

describe("adminApi model test path", () => {
  it("posts model test payload to admin endpoint", () => {
    adminApi.testModel({ id: "gpt-5.5-nx", provider: "openai" });

    expect(api.post).toHaveBeenCalledWith("/admin/models/test", {
      id: "gpt-5.5-nx",
      provider: "openai",
    });
  });
});

describe("adminApi runtime config paths", () => {
  it("uses channel and external service admin endpoints", () => {
    adminApi.listChannels();
    adminApi.saveChannel({
      key: "newapi",
      display_name: "NewAPI",
      adapter: "openai_compatible",
      base_url: "https://gateway.example.com/v1",
      api_key: "sk-test",
      enabled: true,
      sort_order: 10,
    });
    adminApi.deleteChannel("new/api");
    adminApi.listExternalServices();
    adminApi.saveExternalService({
      key: "tavily_search",
      display_name: "Tavily",
      kind: "search",
      base_url: "",
      api_key: "tvly-test",
      enabled: true,
      sort_order: 20,
      max_concurrency: 0,
    });
    adminApi.deleteExternalService("fire/crawl");

    expect(api.get).toHaveBeenCalledWith("/admin/channels");
    expect(api.post).toHaveBeenCalledWith(
      "/admin/channels",
      expect.objectContaining({ key: "newapi" }),
    );
    expect(api.delete).toHaveBeenCalledWith("/admin/channels/new%2Fapi");
    expect(api.get).toHaveBeenCalledWith("/admin/external-services");
    expect(api.post).toHaveBeenCalledWith(
      "/admin/external-services",
      expect.objectContaining({ key: "tavily_search" }),
    );
    expect(api.delete).toHaveBeenCalledWith(
      "/admin/external-services/fire%2Fcrawl",
    );
  });
});

describe("adminApi config paths", () => {
  it("saves multiple config values atomically", () => {
    adminApi.updateConfigs({
      system_name: "Mock Chat",
      title_generation_trigger: 3,
    });

    expect(api.patch).toHaveBeenCalledWith("/admin/config", {
      updates: { system_name: "Mock Chat", title_generation_trigger: 3 },
    });
  });
});
