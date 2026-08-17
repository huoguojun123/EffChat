# Changelog

All notable changes to EffChat will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and releases follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.4.1-beta.1] - 2026-08-17

### Added

- Added conservative builtin catalog candidates for current Gemini, Claude,
  Grok, DeepSeek, Qwen, GLM, MiniMax, and Kimi model families without seeding
  or rewriting administrator-owned PostgreSQL model records.
- Added first-class Kimi K3, K2.7 Code, and K2.6 runtime profiles while keeping
  the existing OpenAI-compatible adapter, local Tool ownership, streaming,
  usage, and persistence paths.

### Fixed

- Aligned current provider thinking contracts, including Gemini generation
  levels, Claude adaptive effort, OpenAI Responses `max`, DeepSeek V4,
  Grok, Qwen, GLM, MiniMax, Doubao, and model-specific Kimi controls.
- Prevented incompatible sampling, penalty, token-limit, and thinking fields
  from leaking across model families while preserving saved administrator
  profiles and unknown-model fail-safe behavior.

### Changed

- Expanded local protocol fixtures and runtime-profile tests for thinking,
  utility suppression, Tool continuation, and outgoing request JSON without
  adding migrations, public API fields, provider fallbacks, or deployment
  topology changes.

## [0.4.0-beta.3] - 2026-08-16

### Fixed

- Added one CSS-viewport-driven standard/compact density contract for desktop
  product chrome, including sidebar, administration navigation, dialogs,
  controls, spacing, and composer geometry.
- Normalized visible product labels and metadata to the existing 12px and 14px
  typography tokens while leaving user Markdown, code, previews, and the
  independent chat font-size control unchanged.
- Kept runtime textarea auto-resizing aligned with the shared composer height
  token so compact Windows-style viewports do not jump back to the standard
  desktop height after input.

### Changed

- Added deterministic browser regression coverage for standard desktop,
  1536x864 at device scale 1.25, low-height desktop, and mobile layouts.
- Preserved mobile spacing and 44 CSS px touch targets without adding OS, DPR,
  browser, CSS zoom, or platform-specific branches.

## [0.4.0-beta.2] - 2026-08-16

### Added

- Added a single EffChat application image that serves the independent web,
  backend, Python extractor, and one-shot migration container roles.
- Added installer and runtime support for either a dedicated PostgreSQL service
  or an external PostgreSQL connection through fields or `DATABASE_URL`.

### Changed

- Reduced the personal deployment input to one active `.env`, one `compose.yml`,
  and runtime data while embedding application migrations in the image.
- Updated CI and release publishing to build one multi-architecture EffChat
  manifest with backend, frontend, and Python license archives.
- Preserved existing deployment settings, secrets, ports, database selection,
  data, storage, and backups when upgrading older three-image layouts.

### Security

- Kept PostgreSQL outside the application image and preserved per-role process,
  health, permission, resource, and failure isolation.
- Added guarded dotenv round trips for special-character secrets and continued
  to reject unknown deployment directories or destructive volume operations.

## [0.4.0-beta.1] - 2026-08-14

### Added

- Added a one-command personal installer that guides first-time setup, creates
  local random secrets, downloads release-matched Compose and migrations, and
  starts published images.
- Added a guarded in-place update path for recognized EffChat registry
  deployments, including layouts where Compose and the environment file live
  in separate parent/child directories.

### Changed

- Reworked the public documentation around a Chinese-first bilingual README,
  richer product and architecture details, readable product screenshots, and
  clearer personal, registry Compose, and local source deployment paths.
- Clarified contribution requirements and the licensing boundary for
  dependencies, prompts, fonts, icons, screenshots, and interoperability names.
- Preserved existing environment values, ports, project names, data, storage,
  and backups during installer updates while archiving replaced deployment
  files under `deployment-backups/`.
- Refreshed the compatible Go, frontend, Python extractor, and GitHub Actions
  dependency batches without changing product APIs, migrations, or deployment
  configuration contracts.

### Security

- Generate installer-managed secrets with restrictive file permissions and
  refuse unknown, customized, or ambiguous existing deployment directories.
- Updated `golang.org/x/image` to `v0.45.0` to fix the reachable VP8L
  excessive-memory-allocation vulnerability reported as GO-2026-6222.

## [0.3.4-beta.3] - 2026-08-14

### Fixed

- Kept a single active conversation-compaction checkpoint when later
  checkpoints compress earlier summaries.
- Anchored checkpoint dividers by their logical conversation boundary while
  retaining compatibility with legacy direct-message pointers.
- Reconciled completed SSE and RunHub activity through the same canonical
  message-window projection used by initial load, refresh, and pagination.

## [0.3.4-beta.2] - 2026-08-14

### Added

- Added a registry-only Docker Compose template for pulling the published
  backend, web, and Python extractor images without rebuilding applications on
  the deployment host.

### Changed

- Kept the existing environment, PostgreSQL, migration, storage, port, network,
  and health contracts when switching an existing deployment to registry
  images.
- Updated the public deployment guide and CI contract checks for the registry
  Compose path.

### Security

- Updated the Go 1.26 patch toolchain to 1.26.6 for the current standard-library
  security fixes and refreshed the pinned Alpine builder manifest.
- Updated the existing transitive Nano ID override to 3.3.18 after the
  production dependency audit began rejecting 3.3.17.

## [0.3.4-beta.1] - 2026-08-13

> Released after pull-request, `main`, Gitleaks, and release-image workflows
> completed successfully.

### Added

- Deployment status, usage governance, conversation search, session export,
  managed document extraction, and code-block previews.
- PostgreSQL migration tracking and reproducible Docker source builds.

### Changed

- Prepared the repository identity, deployment templates, public documentation,
  contribution workflow, and automated checks for the initial open-source
  release.
- Consolidated the product identity as EffChat.
- Refined streaming recovery, run reconciliation, file lifecycle handling, and
  administrator configuration.

### Security

- Added release-mode JWT secret validation, bounded authentication attempts,
  restricted backend port exposure, proxy trust controls, and security headers.
- Refreshed compatible frontend dependency patches for Mermaid rendering,
  sanitization, identifier generation, and build-time URI/glob processing.

[Unreleased]: https://github.com/huoguojun123/EffChat/compare/v0.4.1-beta.1...HEAD
[0.4.1-beta.1]: https://github.com/huoguojun123/EffChat/releases/tag/v0.4.1-beta.1
[0.4.0-beta.3]: https://github.com/huoguojun123/EffChat/releases/tag/v0.4.0-beta.3
[0.4.0-beta.2]: https://github.com/huoguojun123/EffChat/releases/tag/v0.4.0-beta.2
[0.4.0-beta.1]: https://github.com/huoguojun123/EffChat/releases/tag/v0.4.0-beta.1
[0.3.4-beta.3]: https://github.com/huoguojun123/EffChat/releases/tag/v0.3.4-beta.3
[0.3.4-beta.2]: https://github.com/huoguojun123/EffChat/releases/tag/v0.3.4-beta.2
[0.3.4-beta.1]: https://github.com/huoguojun123/EffChat/releases/tag/v0.3.4-beta.1
