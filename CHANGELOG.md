# Changelog

All notable changes to EffChat will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and releases follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/huoguojun123/EffChat/compare/v0.3.4-beta.2...HEAD
[0.3.4-beta.2]: https://github.com/huoguojun123/EffChat/releases/tag/v0.3.4-beta.2
[0.3.4-beta.1]: https://github.com/huoguojun123/EffChat/releases/tag/v0.3.4-beta.1
