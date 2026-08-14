<p align="center">
  <img src="frontend/public/pwa-512x512.png" width="112" alt="EffChat logo">
</p>

<h1 align="center">EffChat</h1>

<p align="center">
  A self-hosted agent workbench that keeps models, data, and runtime under your control.
</p>

<p align="center">
  <a href="README.md">中文</a> · <strong>English</strong>
</p>

<p align="center">
  <a href="https://github.com/huoguojun123/EffChat/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/huoguojun123/EffChat/ci.yml?branch=main&label=CI"></a>
  <a href="https://github.com/huoguojun123/EffChat/releases"><img alt="Release" src="https://img.shields.io/github/v/release/huoguojun123/EffChat?include_prereleases&sort=semver"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache--2.0-blue"></a>
  <img alt="Go" src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white">
  <img alt="React" src="https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=111">
</p>

<p align="center">
  <a href="#quick-start">Quick start</a> ·
  <a href="#interface-preview">Preview</a> ·
  <a href="#why-effchat">Highlights</a> ·
  <a href="#documentation">Docs</a>
</p>

EffChat is designed for individuals and small teams that want control over their models, data, and runtime. It stays simple to deploy and quiet to use while treating the difficult parts behind a chat box as first-class product behavior: run recovery after disconnects, selectable answer attempts, context compaction that preserves history, and explicit lifecycles for files, memory, web tools, and quotas.

> [!WARNING]
> EffChat is still beta software. Create a consistent backup before upgrading; migrations, configuration compatibility, and public APIs may change in later prereleases.

## Interface preview

The README reserves three screenshot slots. Add sanitized images with the expected filenames under `docs/assets/screenshots/`; they will appear here without another README edit.

<p align="center">
  <img src="docs/assets/screenshots/chat-workspace.webp" alt="Chat workspace" width="100%">
</p>

<table>
  <tr>
    <td width="50%" align="center">
      <img src="docs/assets/screenshots/file-and-tools.webp" alt="Files and tools" width="100%"><br>
      <sub>Files, extraction, and tool runs</sub>
    </td>
    <td width="50%" align="center">
      <img src="docs/assets/screenshots/admin-settings.webp" alt="Administration" width="100%"><br>
      <sub>Models, channels, and governance</sub>
    </td>
  </tr>
</table>

## Why EffChat

### A conversation is more than one HTTP request

- **Runs survive disconnects:** backend execution continues and persists after the browser disconnects, then recovers through RunHub and PostgreSQL.
- **Answers remain comparable:** multiple attempts for the same turn are persisted, selectable, and removable. The selected attempt is the one sent into future context.
- **Retries do not duplicate facts:** zero-output transient failures can retry safely; once text, reasoning, or tool output exists, the provider call is not silently replayed.
- **Compaction does not erase history:** checkpoints change the model context while keeping the original conversation visible to the user.

### Files are part of the agent workflow

- Uploads, staged ownership, extracted text, session files, previews, and authenticated downloads share one ownership boundary.
- An isolated Python sidecar handles common documents and spreadsheets. PDF workflows can use MinerU OCR with local parsing as a fallback.
- Large files are read in bounded windows, and extraction, OCR, deletion, and cleanup expose explicit states instead of feeding partial content to the agent.

### Memory stays deliberately scoped

- Memory is scoped to one conversation. EffChat does not build cross-session profiles, a vector database, or implicit RAG.
- Automatic maintenance, manual organization, retries, change history, and undo are supported.
- Passwords, tokens, authorization values, and private keys are protected before storage and before old memory is sent back to a model.

### Web capabilities are independently governed

- Search and webpage extraction are separate tool chains with independent ordering, enablement, and quotas.
- Firecrawl, Jina, Tavily Extract, Exa Extract, and other configured providers run in administrator order; credential-free Basic extraction remains the final local fallback.
- Long pages are locally filtered for relevant content first. Model refinement is optional, and failures fall back to relevant source passages instead of a naive prefix truncation.

### Models, tools, and quotas stay visible

- Supports OpenAI-compatible Chat Completions, OpenAI Responses, Anthropic native, and Google native adapters.
- Models, channels, Tools, Skills, web services, user groups, quotas, fonts, and system settings are managed from the admin UI.
- Messages, model tokens, tools, search, extraction, and OCR share consistent usage accounting without pretending to be a commercial billing system.

## Architecture at a glance

```mermaid
flowchart LR
    Browser["Browser / PWA"] --> Web["React Web"]
    Web --> API["Go API"]
    API --> Agent["Eino ReAct Agent"]
    Agent --> Models["Model channels"]
    Agent --> Tools["Tools / Skills"]
    API --> DB[(PostgreSQL)]
    API --> Storage["Managed local storage"]
    API --> Extractor["Python Extractor / OCR"]
```

- **Backend:** Go, Gin, Eino
- **Frontend:** React 19, TypeScript, Vite, Tailwind CSS v4
- **Database:** PostgreSQL
- **Extractor:** isolated Python sidecar
- **Deployment:** Docker Compose

See [ARCHITECTURE.md](ARCHITECTURE.md) for the complete design and runtime invariants.

## Quick start

### One-command personal install

Follow the prompts for an installation directory and web port; safe defaults handle the rest. The installer downloads the matching Compose template and migrations, creates local random secrets, pulls the published images, and starts EffChat.

```bash
curl -fsSL https://raw.githubusercontent.com/huoguojun123/EffChat/main/scripts/install.sh | bash
```

Open `http://127.0.0.1:8088`. You can also set `EFFCHAT_HOME` or `EFFCHAT_WEB_PORT` in advance to skip the corresponding prompt:

```bash
curl -fsSL https://raw.githubusercontent.com/huoguojun123/EffChat/main/scripts/install.sh | EFFCHAT_HOME=/srv/effchat bash
```

Fresh installation only accepts an empty directory. To update an existing EffChat registry deployment, run the same script, enter the existing directory, and type `update`; it preserves the environment file and data/storage/backups, updates images, Compose, and migrations, and archives the previous deployment files under `deployment-backups/`.

### Standard Docker Compose

For users who want to manage directories, environment variables, ports, upgrades, and backups themselves:

```bash
git clone https://github.com/huoguojun123/EffChat.git
cd EffChat
cp .env.docker.example .env.docker
# Replace at least POSTGRES_PASSWORD and JWT_SECRET
docker compose --env-file .env.docker -f docker-compose.registry.yml pull
docker compose --env-file .env.docker -f docker-compose.registry.yml up -d
```

To build locally instead, run `scripts/docker-build.sh up` after copying the environment template.

Open `http://localhost:8088`. The first account registered on a fresh instance becomes the administrator and can configure model channels, web services, tools, user groups, and file policies.

See [Docker Compose deployment](docs/deployment.md) for upgrades, backups, recovery, data directories, and reverse proxy configuration.

## Documentation

Most project documentation is maintained in Chinese so there is one authoritative operational description.

- [Administrator configuration](docs/administration.md)
- [Docker Compose deployment](docs/deployment.md)
- [Architecture](ARCHITECTURE.md)
- [Contributing](CONTRIBUTING.md)
- [Database migrations](backend/migrations/README.md)
- [Changelog](CHANGELOG.md)

## Current boundaries

EffChat currently focuses on being a self-hosted agent workbench. It does not include a code-execution sandbox, shell tool, browser automation, full RBAC, commercial billing, or a Skills marketplace. These capabilities materially expand the security and deployment boundary and will not be hidden behind ungoverned switches in the main process.

## Security and contributing

- Report vulnerabilities privately through [GitHub Private Vulnerability Reporting](https://github.com/huoguojun123/EffChat/security/advisories/new), not a public issue.
- Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a bug report, feature request, or pull request.
- Never include real credentials, production logs, user data, or private deployment information in examples, tests, or issues.

## License

EffChat's original source code is licensed under the [Apache License 2.0](LICENSE). Third-party dependencies, prompts, and assets remain under their respective terms; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) and [NOTICE](NOTICE). Release images also include component-level license archives generated from the dependencies actually distributed.

Maintainers should follow the [release checklist](docs/release-checklist.md).
