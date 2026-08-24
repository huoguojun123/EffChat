# EffChat

<p align="center">
  <img src="frontend/public/pwa-512x512.png" width="104" alt="EffChat logo">
</p>

<p align="center">
  <strong>A calm, self-hosted AI chat workbench for people who want control over models, files, conversations, and runtime data.</strong>
</p>

<p align="center">
  <a href="README.zh-CN.md">简体中文</a> · <strong>English</strong>
</p>

<p align="center">
  <a href="https://github.com/huoguojun123/EffChat/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/huoguojun123/EffChat/ci.yml?branch=main&label=CI"></a>
  <a href="https://github.com/huoguojun123/EffChat/releases"><img alt="Release" src="https://img.shields.io/github/v/release/huoguojun123/EffChat?include_prereleases&sort=semver"></a>
  <a href="https://hub.docker.com/r/gjhuo/effchat"><img alt="Docker pulls" src="https://img.shields.io/docker/pulls/gjhuo/effchat"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache--2.0-blue"></a>
</p>

> [!WARNING]
> EffChat is beta software. Back up your data before upgrading. Prerelease versions may change migrations, configuration compatibility, or public APIs.

## Why EffChat

EffChat keeps the useful parts of an AI workspace together without turning a personal deployment into a platform project. It gives you a focused chat UI, durable runs, editable conversation memory, governed tools, and a file pipeline that stays on infrastructure you control.

## Highlights

- 💬 **Durable conversations** — streaming runs continue after a browser disconnect and recover after refresh.
- 🔁 **Answer versions** — compare, select, retry, and remove multiple answers from the same turn.
- 🧠 **Scoped memory** — structured memory belongs to a conversation, stays visible, and can be revised or undone.
- 🗜️ **Context compaction** — reduce what the model sees without deleting the conversation history you can read.
- 📎 **Document understanding** — extract PDF, Office, Markdown, text, spreadsheets, and images through a managed file flow.
- 🧰 **Tools and Skills** — let the Agent search, read files, fetch pages, and use approved capabilities with visible run states.
- 🌐 **Web search and extraction** — configure Tavily, Brave, Exa, Bocha, SearXNG, Firecrawl, Jina, or local Basic fallback independently.
- 🤖 **Multi-provider models** — use OpenAI-compatible, OpenAI Responses, Anthropic native, and Google native channels.
- 🛡️ **Admin governance** — manage models, providers, quotas, user groups, files, fonts, Tools, Skills, and usage in one place.
- 📱 **Desktop and PWA** — responsive chat, installable app shell, light/dark themes, Markdown, math, Mermaid, and code previews.

## Screenshots

<p align="center">
  <a href="docs/assets/screenshots/chat-workspace.png"><img src="docs/assets/screenshots/chat-workspace.png" alt="EffChat chat workspace" width="1080"></a>
</p>
<p align="center"><sub>Chat workspace with streaming answers, Markdown, Mermaid, tools, and conversation organization.</sub></p>

<p align="center">
  <a href="docs/assets/screenshots/file-and-tools.png"><img src="docs/assets/screenshots/file-and-tools.png" alt="EffChat files and tools" width="560"></a>
</p>
<p align="center"><sub>Files and tools working together inside a conversation.</sub></p>

<p align="center">
  <a href="docs/assets/screenshots/admin-settings.png"><img src="docs/assets/screenshots/admin-settings.png" alt="EffChat administration" width="1080"></a>
</p>
<p align="center"><sub>Model channels, provider settings, and connection checks in the admin workspace.</sub></p>

Screenshots use demonstration content or sanitized settings. Click an image to view its original resolution.

## Quick start

### Guided installer

For a guided personal setup, the installer asks for the directory, web port, and PostgreSQL source, then writes one private `.env` and one Compose file. Running it again can update a recognized EffChat deployment without replacing its data or credentials.

```bash
curl -fsSL https://raw.githubusercontent.com/huoguojun123/EffChat/main/scripts/install.sh | bash
```

### Docker Compose with published images

This is the clearest path for a server or home lab. It uses one EffChat application image for the `web`, `backend`, `py-extractor`, and one-shot `migrate` roles, plus the official PostgreSQL image.

```bash
git clone https://github.com/huoguojun123/EffChat.git
cd EffChat
cp .env.docker.example .env.docker
# Set at least POSTGRES_PASSWORD and JWT_SECRET.
docker compose --env-file .env.docker -f docker-compose.registry.yml pull
docker compose --env-file .env.docker -f docker-compose.registry.yml up -d
```

Open `http://localhost:8088`. The first account on a fresh instance becomes the immutable super administrator.

### Build from source

```bash
git clone https://github.com/huoguojun123/EffChat.git
cd EffChat
cp .env.docker.example .env.docker
scripts/docker-build.sh up
```

Use the same `.env.docker`, data directory, and PostgreSQL contract for local development and deployment. See [Docker Compose deployment](docs/deployment.md) for upgrades, backups, recovery, reverse proxying, and external PostgreSQL.

## Configure your instance

EffChat ships the application and local document pipeline, not model access or commercial service credits. Configure only what you need from the admin workspace:

- **Models:** protocol, Base URL, API key, model identifier, and capabilities.
- **Web search:** your Tavily, Brave, Exa, or Bocha key, or a self-hosted SearXNG JSON endpoint.
- **Web extraction:** Firecrawl, Jina Reader, Tavily Extract, Exa Extract, and the credential-free Basic fallback run as separate provider chains.
- **Documents:** PDF, DOCX, PPTX, XLSX, CSV, Markdown, text, and image workflows are local by default; MinerU OCR is optional.
- **Controls:** Tools, Skills, quotas, user groups, file limits, memory capacity, fonts, and usage are managed in the admin UI.

Credentials belong in the admin UI or your private environment file, never in commits, screenshots, issues, or example configuration. See [Administrator configuration](docs/administration.md) for the field-by-field guide.

## Documentation

| Guide | When to use it |
| --- | --- |
| [Deployment](docs/deployment.md) | Install, update, back up, restore, and operate a Compose deployment |
| [Administrator configuration](docs/administration.md) | Models, search, extraction, files, Tools, Skills, quotas, and fonts |
| [Architecture](ARCHITECTURE.md) | Agent, streaming recovery, files, memory, database, and runtime boundaries |
| [Contributing](CONTRIBUTING.md) | Local development, tests, and pull requests |
| [Changelog](CHANGELOG.md) | Release notes and compatibility changes |
| [Security policy](SECURITY.md) | Private vulnerability reporting |
| [Third-party notices](THIRD_PARTY_NOTICES.md) | Dependency, font, prompt, image, and container licenses |

## Community

- [Linux.do](https://linux.do)
- [GitHub Issues](https://github.com/huoguojun123/EffChat/issues)
- [Security advisories](https://github.com/huoguojun123/EffChat/security/advisories/new)

## Contributing

Bug reports, documentation improvements, and focused pull requests are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) first. Do not include real credentials, private deployment details, production logs, or user data in issues, tests, or screenshots.

## License

EffChat source code is licensed under the [Apache License 2.0](LICENSE). Third-party components, fonts, prompts, icons, screenshots, base images, and trademarks retain their own terms; see [NOTICE](NOTICE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
