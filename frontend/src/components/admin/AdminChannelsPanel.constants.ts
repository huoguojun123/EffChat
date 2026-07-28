import type { AIChannelAdapter, ExternalServiceKind } from "@/api/admin";

export const adapterOptions: Array<{ value: AIChannelAdapter; label: string }> =
  [
    { value: "openai_compatible", label: "OpenAI-compatible" },
    { value: "anthropic", label: "Anthropic" },
    { value: "google", label: "Google Gemini" },
  ];

export const servicePresets: Record<
  string,
  {
    label: string;
    kind: ExternalServiceKind;
    baseURL: string;
    maxConcurrency?: number;
    keyOptional?: boolean;
  }
> = {
  tavily_search: {
    label: "Tavily",
    kind: "search",
    baseURL: "https://api.tavily.com/search",
  },
  brave_search: {
    label: "Brave Search",
    kind: "search",
    baseURL: "https://api.search.brave.com/res/v1/web/search",
  },
  exa_search: {
    label: "Exa",
    kind: "search",
    baseURL: "https://api.exa.ai/search",
  },
  bocha_search: {
    label: "博查",
    kind: "search",
    baseURL: "https://api.bochaai.com/v1/web-search",
  },
  searxng: { label: "SearXNG", kind: "search", baseURL: "", keyOptional: true },
  firecrawl: {
    label: "Firecrawl",
    kind: "crawler",
    baseURL: "https://api.firecrawl.dev/v2",
  },
  jina: {
    label: "Jina Reader",
    kind: "crawler",
    baseURL: "https://r.jina.ai",
    keyOptional: true,
  },
  tavily_extract: {
    label: "Tavily Extract",
    kind: "crawler",
    baseURL: "https://api.tavily.com/extract",
  },
  exa_extract: {
    label: "Exa Extract",
    kind: "crawler",
    baseURL: "https://api.exa.ai/contents",
  },
  mineru: {
    label: "MinerU 精准解析",
    kind: "ocr",
    baseURL: "https://mineru.net",
    maxConcurrency: 2,
  },
};

export const serviceOptionsByKind: Record<
  Exclude<ExternalServiceKind, "ocr">,
  string[]
> = {
  search: [
    "tavily_search",
    "brave_search",
    "exa_search",
    "bocha_search",
    "searxng",
  ],
  crawler: ["firecrawl", "jina", "tavily_extract", "exa_extract"],
};
