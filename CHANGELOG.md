# Changelog

All notable changes to EffChat will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and releases follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.4.1-beta.8] - 2026-08-27

### Added

- Added conservative builtin candidates for DeepSeek V4 Flash Vision Experimental, GLM-5.3-Flash, Qwen 3.8 Max/Flash, Qwen 3.7 Flash, and MiniMax M2.7 HighSpeed.

### Fixed

- Corrected MiniMax M3 image capability, MiniMax M2.7 context/output limits, and Qwen 3.7 Max output metadata against current official documentation.
- Extended the existing Qwen Chat Completions preserved-thinking boundary to Qwen 3.8 without adding a new adapter or mixing Responses API fields into that protocol.

### Verification

- Added registry, runtime-profile, thinking-family, and request-fixture coverage for the refreshed model contracts; full backend, frontend, dependency, install, and registry Compose gates passed where locally available.
- No database seed, migration, API, provider/channel, deployment-topology, Korea, or persisted-user-data changes.

## [0.4.1-beta.7] - 2026-08-24

### Fixed

- Stabilized retry presentation so an assistant answer is replaced only after the retry is accepted by the HTTP/SSE or durable-run admission path.
- Kept local assistant output attached to its user turn during retry, synchronization, and finalization instead of appending late responses to the end of the conversation.
- Shared same-generation message-window reconciliation between cursor polling and active-run recovery, preventing one tab from invalidating another tab's live recovery.
- Replaced the ambiguous automatic retry notice with a live attempt counter and deadline-based countdown, and added a small, stable scroll gap above the composer for streamed answers.

### Changed

- Published the bilingual README with English as the default entry point and a separate Simplified Chinese guide. Screenshots remain the current sanitized assets and can be refined in a later documentation pass.

### Verification

- Frontend lint, 364 Vitest tests, production/PWA build, focused retry and cross-tab Playwright coverage, and non-destructive local unified-image validation passed.
- No database, API, migration, deployment topology, Korea, or persisted-user-data changes.

## [0.4.1-beta.6] - 2026-08-23

### Fixed

- Normalized legacy bare `<br>`, `<br/>`, and `<br />` tags emitted by historical model responses so they render as line breaks instead of visible tags in regular Markdown and table cells.
- Kept fenced code, inline code, attributed HTML, scripts, and other raw HTML outside this compatibility conversion and unchanged by the renderer safety boundary.

### Verification

- Markdown-focused tests (17/17), the full frontend suite (58 files, 354 tests), lint, production/PWA build, and non-destructive local Docker verification passed.
- No migration, API, deployment-topology, or persisted-user-data changes; Korea was not accessed or updated.

## [0.4.1-beta.5] - 2026-08-23

### Fixed

- Rebalanced mobile answer controls so version navigation, copy, retry, and usage remain aligned without wrapping or shrinking touch targets.
- Replaced the hard chat topbar divider with a restrained translucent fade while preserving light and dark surface hierarchy.
- Improved cross-platform Chinese body rendering with regular weight, platform-native CJK fallbacks, and tighter paragraph rhythm while retaining Plus Jakarta Sans for Latin text.
- Widened the standard and compact desktop message columns and reduced excess assistant-block spacing without changing the 15px chat baseline, font-size slider, custom font slots, or user-message proportions.

### Verification

- Frontend lint, 330 unit tests, production/PWA build, 12-case responsive Playwright coverage, and a non-destructive local unified-image update passed; schema and persisted data were unchanged, and Korea was not accessed.

## [0.4.1-beta.4] - 2026-08-22

### Fixed

- Protected the first registered account as an immutable, active super administrator; existing installations backfill the earliest account through migration `054` without changing user data.
- Restored the immutable migration baseline so upgrades do not fail checksum validation after the super administrator schema addition.
- Improved cross-platform chat density, reading-column width, sidebar title space, composer spacing, and product font fallbacks for Windows-style desktop viewports.
- Prevented responsive model-management filters from being populated by browser autofill and kept administrator controls consistent across narrow layouts.

### Verification

- Backend, frontend, migration contract, local Docker migration, health, schema, and data-preservation checks passed. Korea was not accessed or updated.

## [0.4.1-beta.3] - 2026-08-21

### Fixed

- Improved mobile system-prompt selection with a single-column list and preview flow that remains usable within narrow touch viewports.
- Preserved authentication credentials across transient startup, network, and timeout failures; credentials are cleared only after an explicit `401` response.
- Kept high-frequency mobile controls at approximately 44 CSS px touch targets and aligned PWA install metadata with the actual icon contract.
- Added regression coverage for mobile prompt selection, PWA session recovery, production update behavior, and the public build-time PWA contract.

## [0.4.1-beta.2] - 2026-08-18

### Fixed

- Render persisted and streaming reasoning content with the existing safe
  Markdown renderer, preserving headings, lists, emphasis, code, and paragraph
  structure while keeping the compact reasoning density and disabling artifact
  previews.

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

[Unreleased]: https://github.com/huoguojun123/EffChat/compare/v0.4.1-beta.6...HEAD
[0.4.1-beta.6]: https://github.com/huoguojun123/EffChat/releases/tag/v0.4.1-beta.6
[0.4.1-beta.5]: https://github.com/huoguojun123/EffChat/releases/tag/v0.4.1-beta.5
[0.4.1-beta.4]: https://github.com/huoguojun123/EffChat/releases/tag/v0.4.1-beta.4
[0.4.1-beta.3]: https://github.com/huoguojun123/EffChat/releases/tag/v0.4.1-beta.3
[0.4.1-beta.2]: https://github.com/huoguojun123/EffChat/releases/tag/v0.4.1-beta.2
[0.4.1-beta.1]: https://github.com/huoguojun123/EffChat/releases/tag/v0.4.1-beta.1
[0.4.0-beta.3]: https://github.com/huoguojun123/EffChat/releases/tag/v0.4.0-beta.3
[0.4.0-beta.2]: https://github.com/huoguojun123/EffChat/releases/tag/v0.4.0-beta.2
[0.4.0-beta.1]: https://github.com/huoguojun123/EffChat/releases/tag/v0.4.0-beta.1
[0.3.4-beta.3]: https://github.com/huoguojun123/EffChat/releases/tag/v0.3.4-beta.3
[0.3.4-beta.2]: https://github.com/huoguojun123/EffChat/releases/tag/v0.3.4-beta.2
[0.3.4-beta.1]: https://github.com/huoguojun123/EffChat/releases/tag/v0.3.4-beta.1
