import { readFile } from "node:fs/promises"
import { resolve } from "node:path"

const dist = resolve(import.meta.dirname, "../dist")
const serviceWorker = await readFile(resolve(dist, "sw.js"), "utf8")
const indexHtml = await readFile(resolve(dist, "index.html"), "utf8")

if (!serviceWorker.includes('createHandlerBoundToURL("index.html")')) {
  throw new Error("service worker navigation fallback must target the precached index.html entry")
}

const startupChunks = Array.from(
  indexHtml.matchAll(/<link rel="modulepreload"[^>]+href="\/([^"]+)"/g),
  (match) => match[1],
)

for (const chunk of startupChunks) {
  if (!serviceWorker.includes(`url:"${chunk}"`)) {
    throw new Error(`startup chunk missing from PWA precache: ${chunk}`)
  }
}

for (const chunk of ["AdminPage-", "PromptManager-", "WorkspacePanel", "mermaid.core", "graphviz-"]) {
  if (serviceWorker.includes(chunk)) {
    throw new Error(`unexpected dynamic chunk in PWA precache: ${chunk}`)
  }
}

if (!serviceWorker.includes('url:"theme-init.js"')) {
  throw new Error("theme initialization script missing from PWA precache")
}
