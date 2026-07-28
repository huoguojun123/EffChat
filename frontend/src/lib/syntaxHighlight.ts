import { createBundledHighlighter, createSingletonShorthands } from "shiki/core"
import { createJavaScriptRegexEngine } from "shiki/engine/javascript"
import { escapeHtml } from "@/lib/htmlText"
import type { ShikiThemeId } from "@/lib/themes"

const bundledLanguages = {
  bash: () => import("@shikijs/langs/bash"),
  css: () => import("@shikijs/langs/css"),
  diff: () => import("@shikijs/langs/diff"),
  docker: () => import("@shikijs/langs/docker"),
  go: () => import("@shikijs/langs/go"),
  html: () => import("@shikijs/langs/html"),
  java: () => import("@shikijs/langs/java"),
  javascript: () => import("@shikijs/langs/javascript"),
  json: () => import("@shikijs/langs/json"),
  jsx: () => import("@shikijs/langs/jsx"),
  markdown: () => import("@shikijs/langs/markdown"),
  python: () => import("@shikijs/langs/python"),
  rust: () => import("@shikijs/langs/rust"),
  sql: () => import("@shikijs/langs/sql"),
  toml: () => import("@shikijs/langs/toml"),
  tsx: () => import("@shikijs/langs/tsx"),
  typescript: () => import("@shikijs/langs/typescript"),
  xml: () => import("@shikijs/langs/xml"),
  yaml: () => import("@shikijs/langs/yaml"),
}

const bundledThemes = {
  "catppuccin-latte": () => import("@shikijs/themes/catppuccin-latte"),
  "catppuccin-mocha": () => import("@shikijs/themes/catppuccin-mocha"),
  "everforest-dark": () => import("@shikijs/themes/everforest-dark"),
  "everforest-light": () => import("@shikijs/themes/everforest-light"),
  "gruvbox-dark-medium": () => import("@shikijs/themes/gruvbox-dark-medium"),
  "gruvbox-light-medium": () => import("@shikijs/themes/gruvbox-light-medium"),
  "github-dark": () => import("@shikijs/themes/github-dark"),
  "github-light": () => import("@shikijs/themes/github-light"),
  "one-dark-pro": () => import("@shikijs/themes/one-dark-pro"),
  "one-light": () => import("@shikijs/themes/one-light"),
}

type SupportedLanguage = keyof typeof bundledLanguages
type SupportedTheme = keyof typeof bundledThemes

const createHighlighter = createBundledHighlighter<SupportedLanguage, SupportedTheme>({
  langs: bundledLanguages,
  themes: bundledThemes,
  engine: () => createJavaScriptRegexEngine(),
})

const { codeToHtml } = createSingletonShorthands(createHighlighter)

const languageAliases: Record<string, string> = {
  bash: "bash",
  cjs: "javascript",
  cmd: "bat",
  console: "bash",
  htm: "html",
  js: "javascript",
  jsx: "jsx",
  md: "markdown",
  mjs: "javascript",
  py: "python",
  rb: "ruby",
  sh: "bash",
  shell: "bash",
  ts: "typescript",
  tsx: "tsx",
  yml: "yaml",
  zsh: "bash",
}

export async function highlightCodeToHtml(
  code: string,
  language?: string,
  lightTheme: ShikiThemeId = "github-light",
  darkTheme: ShikiThemeId = "github-dark"
) {
  const lang = normalizeLanguage(language) || inferInlineLanguage(code)
  if (!isSupportedLanguage(lang)) {
    return fallbackCodeHtml(code)
  }
  return codeToHtml(code, {
    lang,
    themes: {
      light: lightTheme,
      dark: darkTheme,
    },
    defaultColor: false,
    cssVariablePrefix: "--shiki-",
  })
}

export async function highlightInlineCodeToHtml(
  code: string,
  lightTheme: ShikiThemeId = "github-light",
  darkTheme: ShikiThemeId = "github-dark"
) {
  const lang = inferInlineLanguage(code)
  if (!isSupportedLanguage(lang)) {
    return escapeHtml(code)
  }
  const html = await highlightCodeToHtml(code, undefined, lightTheme, darkTheme)
  return extractCodeInnerHtml(html)
}

function normalizeLanguage(language?: string) {
  const normalized = language?.trim().toLowerCase()
  if (!normalized) return ""
  return languageAliases[normalized] || normalized
}

function isSupportedLanguage(language: string): language is SupportedLanguage {
  return language in bundledLanguages
}

function inferInlineLanguage(code: string) {
  const value = code.trim()
  if (!value) return "text"
  if (/^<\/?[a-z][\s\S]*>$/i.test(value)) return "html"
  if (/^[{[][\s\S]*[}\]]$/.test(value)) return "json"
  if (/^(SELECT|INSERT|UPDATE|DELETE|CREATE|ALTER|DROP)\b/i.test(value)) return "sql"
  if (/^(npm|pnpm|yarn|bun|go|git|docker|kubectl|curl|ssh|cd|ls|cat|rg)\b/.test(value)) return "bash"
  if (/^[.#]?[a-z-]+\s*:\s*[^;]+;?$/i.test(value)) return "css"
  if (/[=;(){}[\]]|=>|\b(const|let|var|function|return|import|export|await|async|type|interface)\b/.test(value)) return "typescript"
  return "text"
}

function extractCodeInnerHtml(html: string) {
  const match = html.match(/<code[^>]*>([\s\S]*?)<\/code>/)
  return match ? match[1].replace(/^<span class="line">([\s\S]*)<\/span>$/u, "$1") : escapeHtml(html)
}

function fallbackCodeHtml(code: string) {
  return `<pre class="shiki"><code>${escapeHtml(code)}</code></pre>`
}
