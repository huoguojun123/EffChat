# Contributing to EffChat

EffChat is a small self-hosted project in active pre-release development.
Focused bug fixes, tests, documentation corrections, and scoped improvements
are welcome.

## Before Opening a Change

- Search existing issues and pull requests.
- Open an issue before a large feature or architecture change.
- Do not add real credentials, user data, private hosts, absolute local paths,
  database dumps, generated reports, or deployment backups.
- Keep one pull request focused on one problem.
- Follow the existing Go backend and TypeScript/React frontend boundaries.

## Local Setup

Requirements:

- Go version declared in `backend/go.mod`
- Node.js 22 or newer and npm
- Docker with Docker Compose

Start the complete stack:

```bash
cp .env.docker.example .env.docker
# Replace POSTGRES_PASSWORD and JWT_SECRET before starting.
scripts/docker-build.sh up
```

## Verification

Run the checks relevant to your change:

```bash
cd backend
go test ./...
go vet ./...

cd ../frontend
npm ci
npm run lint
npm test
npm run build

cd ..
docker compose --env-file .env.docker.example config
git diff --check
```

Database or migration changes must also pass:

```bash
scripts/test-postgres.sh
```

## Pull Requests

- Use an English title and commit messages.
- Explain behavior, motivation, data or compatibility impact, and verification.
- Add or update focused tests for non-trivial behavior.
- Update `README.md`, `ARCHITECTURE.md`, or public deployment documentation
  when behavior or operational requirements change.
- Include screenshots for visible UI changes on desktop and mobile.
- Confirm that the change contains only synthetic fixtures and public-safe
  documentation.

By contributing, you agree that your contribution is licensed under the
project license.
