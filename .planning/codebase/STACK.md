# Technology Stack

## Languages & Runtimes

| Layer          | Language   | Version                              |
|----------------|------------|--------------------------------------|
| Backend        | Go         | 1.26.1 (go.mod), 1.26-alpine (Docker) |
| Frontend       | TypeScript | ^5.9.3 (catalog)                     |
| Web runtime    | Node.js    | 22 (CI, Docker)                      |
| Desktop runtime| Electron   | ^39.2.6                              |
| Database       | SQL        | PostgreSQL 17 (pgvector/pgvector:pg17) |

The Go server compiles to a static binary (`CGO_ENABLED=0`) with two entry points:
- `cmd/server` — HTTP/WebSocket API server
- `cmd/multica` — CLI binary (daemon control, issue management, agent ops) released via GoReleaser to Homebrew and GitHub Releases

## Frontend

### Frameworks & Rendering

| Concern                | Library / Tool                          | Version / Notes                     |
|------------------------|-----------------------------------------|-------------------------------------|
| Web framework          | Next.js (App Router)                    | ^16.2.3                             |
| Desktop framework      | Electron + electron-vite                | ^39.2.6 / ^5.0.0                    |
| Desktop packaging      | electron-builder                        | ^26.0.12                            |
| Desktop auto-update    | electron-updater                        | ^6.8.3 (GitHub provider)            |
| Desktop routing        | react-router-dom                        | ^7.6.0                              |
| UI primitives          | @base-ui/react (shadcn Base UI variant) | ^1.3.0                              |
| Rich text editor       | Tiptap (prosemirror-based)              | ^3.22.1 (core + 10+ extensions)     |
| Charts / data viz      | Recharts                                | 3.8.0                               |
| Docs site              | Fumadocs (next + mdx)                   | fumadocs-core ^15.5.2, fumadocs-mdx ^12.0.3 |

### State Management

| Concern       | Library                | Version / Notes                                   |
|---------------|------------------------|---------------------------------------------------|
| Server state  | TanStack React Query   | ^5.96.2 (query cache + WS invalidation)           |
| Client state  | Zustand                | ^5.0.0 (stores in packages/core, not views)       |
| Schema validation | Zod                | ^4.1.5 (API response parsing with parseWithFallback) |

### Styling

| Concern           | Tool                                     |
|-------------------|------------------------------------------|
| CSS framework     | Tailwind CSS v4 (@tailwindcss/postcss, @tailwindcss/vite) |
| Component styling | class-variance-authority ^0.7.1, tailwind-merge ^3.4.0, tw-animate-css ^1.4.0 |
| Theme switching   | next-themes ^0.4.6                       |

### UI Libraries (notable)

| Library                     | Purpose                                |
|-----------------------------|----------------------------------------|
| cmdk ^1.1.1                 | Command palette (keyboard navigation)  |
| vaul ^1.1.2                 | Drawer component                       |
| sonner ^2.0.7               | Toast notifications                    |
| embla-carousel-react ^8.6.0 | Carousel / slider                      |
| react-day-picker ^9.14.0    | Date picker                            |
| react-resizable-panels ^4.7.5 | Resizable layout panels              |
| input-otp ^1.4.2            | OTP code input                         |
| emoji-mart ^5.6.0           | Emoji picker                           |
| shiki ^3.21.0               | Syntax highlighting                    |
| lowlight ^3.3.0             | Code block highlighting (via Tiptap)   |
| katex ^0.16.45              | Math rendering (LaTeX)                 |
| mermaid ^11.14.0            | Diagram rendering                      |
| motion ^12.38.0             | Animation library (views package)      |
| date-fns ^4.1.0             | Date formatting / manipulation         |
| react-markdown ^10.1.0      | Markdown rendering                     |
| @dnd-kit (core/sortable/utilities) | Drag and drop                  |
| react-markdown + rehype/remark plugins | GFM, raw HTML, math, breaks |

### Internationalization

| Library                           | Version        |
|-----------------------------------|----------------|
| i18next                           | ^26.0.8        |
| react-i18next                     | ^17.0.6        |
| @formatjs/intl-localematcher      | ^0.8.4         |
| eslint-plugin-i18next             | ^6.1.4         |

Locales live in `packages/views/locales/` (en, zh-Hans confirmed).

### Analytics (frontend)

| Library     | Version     |
|-------------|-------------|
| posthog-js  | ^1.176.1    |

## Backend

### Go Router & Middleware

| Library             | Version  | Purpose                              |
|---------------------|----------|--------------------------------------|
| go-chi/chi/v5       | 5.2.5    | HTTP router                          |
| go-chi/cors         | 1.2.2    | CORS middleware                      |

### Database Access

| Library            | Version | Purpose                                     |
|--------------------|---------|---------------------------------------------|
| jackc/pgx/v5       | 5.8.0   | PostgreSQL driver + connection pool (pgxpool)|
| sqlc               | (CLI)   | SQL-to-Go code generator (sqlc.yaml config) |

Migration files are plain SQL in `server/migrations/` (001 through 081+).

### Authentication & Security

| Library             | Version  | Purpose                           |
|---------------------|----------|-----------------------------------|
| golang-jwt/jwt/v5   | 5.3.1    | JWT token signing/verification   |
| Google OAuth2       | (stdlib) | Google login flow (userinfo + token exchange) |

Auth flow: email verification code (Resend) OR Google OAuth2. Session via HttpOnly cookies (30-day TTL) with HMAC-based CSRF protection. Personal Access Tokens (mul_*) and Daemon Tokens (mdt_*). CloudFront signed cookies for private file access.

### Real-time

| Library               | Version  | Purpose                                    |
|-----------------------|----------|--------------------------------------------|
| gorilla/websocket     | 1.5.3    | WebSocket upgrade + connection management  |
| redis/go-redis/v9     | 9.18.0   | Redis streams for multi-node WS fanout     |

Real-time architecture: in-process Hub (single-node) or Redis-backed sharded stream relay (multi-node). Events flow through an in-process `events.Bus` → realtime Hub → WebSocket clients. Scope-based fanout (workspace, task, chat session).

### Task Scheduling

| Library             | Version  | Purpose                    |
|---------------------|----------|----------------------------|
| robfig/cron/v3      | 3.0.1    | Scheduled task triggers    |

Autopilot scheduler runs every 30 seconds, polling for due triggers.

### Observability

| Library                         | Version   | Purpose                          |
|---------------------------------|-----------|----------------------------------|
| prometheus/client_golang        | 1.23.2    | Prometheus metrics exposition    |
| lmittmann/tint                  | 1.1.3     | Structured logging (slog handler)|

Metrics server on separate port (default disabled, env: `METRICS_ADDR`). HTTP request metrics, Go runtime, process, DB pool, realtime Hub, and daemon WS metrics.

### CLI

| Library           | Version   | Purpose                     |
|-------------------|-----------|-----------------------------|
| spf13/cobra       | 1.10.2    | CLI command framework       |
| pelletier/go-toml/v2 | 2.3.0  | TOML config parsing (daemon)|

### Other Go Dependencies

| Library             | Version  | Purpose                            |
|---------------------|----------|------------------------------------|
| google/uuid         | 1.6.0    | UUID generation                    |
| oklog/ulid/v2       | 2.1.1    | ULID generation (Redis stream IDs) |
| mattn/go-shellwords | 1.0.13   | Shell argument parsing             |

## Database & Storage

### Primary Database

- **PostgreSQL 17** via `pgvector/pgvector:pg17` Docker image
- **pgvector extension** included (for vector similarity search, e.g. embeddings)
- Connection pooling via pgxpool (pgx/v5)
- Default pool: 25 max conns, 5 min conns (configurable via env)
- Migrations: plain SQL files in `server/migrations/`, run via `cmd/migrate`

### Cache / Message Broker

- **Redis 7** (`redis:7-alpine` in CI; configurable via `REDIS_URL`)
- Used for:
  - Multi-node WebSocket fanout (sharded Redis streams)
  - Runtime liveness tracking
  - Daemon model-list request store (cross-replica state)
  - Local skill request store
  - Runtime update request store
- Optional: server runs in single-node mode without Redis (in-memory stores)

### File Storage

- **Primary**: AWS S3 (`aws-sdk-go-v2/service/s3` v1.97.3)
  - Storage class: INTELLIGENT_TIERING (STANDARD for custom endpoints like MinIO)
  - CDN: AWS CloudFront (signed cookies for private access)
  - S3-compatible backends supported via `AWS_ENDPOINT_URL` (MinIO, R2, B2, Wasabi)
- **Fallback**: Local filesystem (`LOCAL_UPLOAD_DIR=./data/uploads`) when S3_BUCKET is unset
- **AWS Secrets Manager** (`aws-sdk-go-v2/service/secretsmanager` v1.41.5) for CloudFront signing key

## Build & Dev Tools

### Monorepo Management

| Tool              | Version    | Purpose                                  |
|-------------------|------------|------------------------------------------|
| pnpm              | 10.28.2    | Package manager (corepack-activated)     |
| Turborepo         | ^2.5.4     | Monorepo build orchestration             |
| pnpm catalog      | (pnpm-workspace.yaml) | Centralized version pinning    |
| Make              | GNU Make   | Build automation, dev setup, Docker ops  |

### Testing

| Tool                         | Version    | Scope                              |
|------------------------------|------------|------------------------------------|
| Vitest                       | ^4.1.0     | TS unit tests (core, views, apps)  |
| @testing-library/react       | ^16.3.2    | Component tests (jsdom)            |
| @testing-library/jest-dom    | ^6.9.1     | DOM assertion matchers             |
| @testing-library/user-event  | ^14.6.1   | User interaction simulation        |
| jsdom                        | ^29.0.1    | DOM environment for TS tests       |
| @playwright/test             | ^1.58.2    | E2E tests (Chromium)               |
| go test                      | stdlib     | Go unit + integration tests        |

E2E tests use a `TestApiClient` fixture that directly queries PostgreSQL for setup and teardown.

### Linting & Type Checking

| Tool                | Version     | Purpose                     |
|---------------------|-------------|-----------------------------|
| ESLint 9            | ^9.0.0      | JS/TS linting (flat config) |
| typescript-eslint   | ^8.35.0     | TS-specific rules           |
| eslint-plugin-react | ^7.37.0     | React lint rules            |
| @next/eslint-plugin-next | ^16.2.0 | Next.js rules            |
| TypeScript tsc      | ^5.9.3      | Type checking               |

### Code Generation

| Tool    | Purpose                                          |
|---------|--------------------------------------------------|
| sqlc    | SQL queries in `server/pkg/db/queries/` → Go code in `server/pkg/db/generated/` |
| shadcn  | UI component scaffolding (`pnpm ui:add <name>`)  |
| reserved-slugs generator | `server/internal/handler/reserved_slugs.json` → `packages/core/paths/reserved-slugs.ts` |

## Infrastructure

### Docker

| Service        | Image                   | Port  |
|----------------|-------------------------|-------|
| PostgreSQL     | pgvector/pgvector:pg17  | 5432  |
| Backend        | Alpine 3.21 + Go binary | 8280  |
| Frontend (web) | Node 22 Alpine (standalone Next.js) | 3300 |

Docker Compose files:
- `docker-compose.yml` — dev PostgreSQL only
- `docker-compose.selfhost.yml` — production self-host (PostgreSQL + backend + frontend)
- `docker-compose.selfhost.build.yml` — build override for pre-release images

### CI/CD (GitHub Actions)

| Workflow              | Trigger                    | Runs                                  |
|-----------------------|----------------------------|---------------------------------------|
| CI (`ci.yml`)         | push/PR to main            | Frontend: build + typecheck + lint + test; Backend: build + migrate + test (PostgreSQL 17 + Redis 7) |
| Release (`release.yml`) | Tag push `v*.*.*`        | Go tests → GoReleaser (multi-platform binaries) → GitHub Releases + Homebrew tap |
| Desktop Smoke (`desktop-smoke.yml`) | Manual workflow_dispatch | Electron build on Linux + Windows |

CI services: `pgvector/pgvector:pg17`, `redis:7-alpine`.

### Desktop Distribution

- macOS: DMG + ZIP (notarized via notarytool, requires Apple Developer ID)
- Linux: AppImage + DEB + RPM
- Windows: NSIS installer
- Auto-update via electron-updater (GitHub provider, published releases)
- Custom protocol scheme: `multica://`
