# Changelog

All notable changes to EffChat will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and releases follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.4-beta.1] - 2026-08-13

> Release branch prepared. The `v0.3.4-beta.1` tag will only be created after
> pull-request and `main` GitHub Actions complete successfully.

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

[Unreleased]: https://github.com/huoguojun123/EffChat/compare/v0.3.4-beta.1...HEAD
[0.3.4-beta.1]: https://github.com/huoguojun123/EffChat/releases/tag/v0.3.4-beta.1
