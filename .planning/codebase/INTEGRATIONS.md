# External Integrations

## Database

### PostgreSQL 17 (Primary)

- **Image**: `pgvector/pgvector:pg17`
- **Extension**: pgvector (vector similarity search)
- **Driver**: `jackc/pgx/v5` (v5.8.0) with pgxpool connection pooling
- **Schema management**: Plain SQL migrations in `server/migrations/` (001–081+), executed by `cmd/migrate`
- **Query generation**: sqlc (`server/sqlc.yaml`) — handwritten SQL in `server/pkg/db/queries/` generates type-safe Go in `server/pkg/db/generated/`
- **Default connection**: `postgres://multica:multica@localhost:5432/multica?sslmode=disable`
- **Pool defaults**: 25 max conns, 5 min conns (overridable via `DATABASE_MAX_CONNS` / `DATABASE_MIN_CONNS`)
- **Multi-tenancy**: All queries filter by `workspace_id`; membership checks gate access

### Redis 7 (Cache & Message Broker)

- **Image**: `redis:7-alpine`
- **Driver**: `redis/go-redis/v9` (v9.18.0)
- **Env var**: `REDIS_URL` (when set, enables Redis-backed stores; unset = single-node in-memory mode)
- **Separate clients**: Request-path client (skill stores, model stores, liveness) and realtime relay client (blocking XREAD consumers) use distinct Redis connections to prevent starvation
- **Usage areas**:
  - WebSocket fanout via sharded Redis streams (stream keys: `ws:scope:{type}:{id}:stream`)
  - Node heartbeat tracking (`ws:node:{id}:heartbeat`, 90s TTL)
  - Runtime local-skill request store (pending request pattern across API replicas)
  - Runtime model-list request store
  - Runtime update request store
  - Runtime liveness store
- **Relay modes**: `sharded` (default), `dual`, `legacy` (configurable via `REALTIME_RELAY_MODE`)

## Authentication

### Email + Verification Code (Primary)

- **Provider**: Resend (`resend/resend-go/v2`, v2.28.0)
- **Env vars**: `RESEND_API_KEY`, `RESEND_FROM_EMAIL` (default: `noreply@multica.ai`)
- **Flow**: User submits email → server generates 6-digit code → emails via Resend → user submits code → server issues JWT
- **Rate limiting**: 1 code per 60 seconds per email
- **Dev mode**: When `RESEND_API_KEY` is unset, verification codes print to stdout instead
- **Dev shortcut**: `MULTICA_DEV_VERIFICATION_CODE` env var allows a fixed code (disabled in production via `APP_ENV`)

### Google OAuth 2.0

- **Env vars**: `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URI`
- **Flow**: Frontend redirects to Google → callback with authorization code → server exchanges code via `https://oauth2.googleapis.com/token` → fetches user info from `https://www.googleapis.com/oauth2/v2/userinfo` → find-or-create user
- **Profile sync**: New user name and avatar seeded from Google profile
- **Google Client ID** is served to the frontend via `/api/config` (no web rebuild needed to change)

### Session Management

- **JWT library**: `golang-jwt/jwt/v5` (v5.3.1)
- **JWT secret**: `JWT_SECRET` env var (auto-generated on self-host setup)
- **Cookie-based sessions**: HttpOnly `multica_auth` cookie, 30-day TTL, `SameSite: Strict`
- **CSRF protection**: Double-submit cookie pattern with HMAC-signed CSRF tokens (`multica_csrf` cookie + `X-CSRF-Token` header). CSRF token = `hex(nonce) + "." + hex(HMAC-SHA256(nonce, authToken))`
- **Secure flag**: Auto-derived from `FRONTEND_ORIGIN` scheme (HTTPS → Secure flag)
- **Cookie domain**: Optional `COOKIE_DOMAIN` env var (IP literals rejected per RFC 6265)

### Personal Access Tokens (PATs)

- **Format**: `mul_` + 40 hex chars (20 random bytes)
- **Storage**: SHA-256 hash stored in DB
- **Resolution**: Cached in-memory with TTL for high-frequency daemon requests

### Daemon Tokens

- **Format**: `mdt_` + 40 hex chars
- **Storage**: SHA-256 hash stored in DB
- **Purpose**: Authenticates daemon polling and WebSocket connections

### Signup Controls

- `ALLOW_SIGNUP` — toggle new user registration (default: true)
- `ALLOWED_EMAILS` — explicit email whitelist
- `ALLOWED_EMAIL_DOMAINS` — domain whitelist

## Real-time

### WebSocket Server

- **Library**: `gorilla/websocket` (v1.5.3)
- **Protocol**: Upgraded HTTP connections to WebSocket at `/ws`
- **Authentication**: JWT from `multica_auth` cookie OR daemon token from query param
- **Scope-based subscriptions**: Clients subscribe to scopes (`workspace:{id}`, `task:{id}`, `chat:{id}`) with authorization checks
- **Scope authorization**: `ScopeAuthorizer` interface verifies resource ownership before allowing subscription

### In-process Event Bus

- `server/internal/events/bus.go` — synchronous in-process pub/sub
- Event types: `issue:created`, `issue:updated`, `comment:created`, `inbox:new`, `agent:task_completed`, etc.
- Handlers bridge to realtime Hub for WebSocket fanout

### Redis-backed Relay (Multi-node)

- `server/internal/realtime/sharded_stream_relay.go` — Redis Streams-based cross-node relay
- Configuration via env: `REALTIME_RELAY_SHARDS` (default: 16), `REALTIME_RELAY_STREAM_MAXLEN` (default: 10000)
- Heartbeat-based node liveness with automatic failover

### Daemon WebSocket

- `server/internal/daemonws/` — separate WebSocket channel for daemon connections
- Used for real-time task dispatch, status updates, and daemon lifecycle events

## AI / LLM Providers

The platform does **not** call LLM APIs directly from the server. Instead, it orchestrates local agent runtimes (daemons) that run on user machines and each daemon invokes its own CLI tool. The server manages task queues, provides context via injected CLAUDE.md/AGENTS.md files, and collects usage metrics.

### Supported Agent Providers

The daemon supports 11 agent providers, each with its own config injection path:

| Provider   | Config File     | Skills Path          | Notes                                    |
|------------|-----------------|----------------------|------------------------------------------|
| claude     | CLAUDE.md       | .claude/skills/      | Claude Code (Anthropic)                  |
| codex      | AGENTS.md       | CODEX_HOME           | OpenAI Codex CLI                         |
| copilot    | AGENTS.md       | .github/skills/      | GitHub Copilot                           |
| gemini     | GEMINI.md       | (native discovery)   | Google Gemini CLI                        |
| opencode   | AGENTS.md       | .opencode/skills/    | OpenCode                                 |
| openclaw   | AGENTS.md       | .openclaw/skills/    | OpenClaw                                 |
| hermes     | AGENTS.md       | .agent_context/skills/ | Hermes (no native skill discovery)     |
| pi         | AGENTS.md       | .pi/skills/          | Pi CLI                                   |
| cursor     | AGENTS.md       | .cursor/skills/      | Cursor                                   |
| kimi       | AGENTS.md       | (project skills dirs)| Kimi Code CLI                            |
| kiro       | AGENTS.md       | (project skills dirs)| Kiro CLI                                 |

### LLM Model Pricing (Client-side)

The frontend maintains a pricing table in `packages/views/runtimes/utils.ts` for cost estimation. Supported model families:

| Provider   | Models                                                  |
|------------|---------------------------------------------------------|
| Anthropic  | claude-haiku-4-5, claude-sonnet-4-5/4-6, claude-opus-4-5/4-6/4-7, claude-opus-4/4-1, claude-sonnet-4, claude-haiku-3-5 |
| OpenAI     | gpt-5.5, gpt-5.4, gpt-5.4-mini, gpt-5.3-codex, gpt-5-codex, gpt-5, gpt-5-mini, gpt-5-nano, o3, o3-mini, o4-mini, gpt-4o, gpt-4o-mini |

Pricing includes input, output, cacheRead, and cacheWrite rates (USD per million tokens). Model names with dated snapshots (e.g., `claude-sonnet-4-5-20250929`) are stripped to their family name for pricing lookup.

### Token Usage Tracking

- Daemons report token usage per task (input_tokens, output_tokens, cache_read_tokens, cache_write_tokens)
- Server stores in `task_usage` table, aggregated daily via `task_usage_daily` rollups
- Usage exposed via API endpoints for runtime detail pages (cost by agent, by model, by hour, by day)

## Email / Notifications

### Resend (Email Delivery)

- **SDK**: `resend/resend-go/v2` (v2.28.0)
- **Env vars**: `RESEND_API_KEY`, `RESEND_FROM_EMAIL`
- **Email types**:
  1. **Verification codes** — 6-digit code with 10-minute expiry, styled HTML
  2. **Workspace invitations** — inviter name + workspace name + accept link, with HTML-escaped user content and subject field sanitization (max 60 runes)
- **Dev mode**: Emails print to stdout when `RESEND_API_KEY` is unset
- **Security**: User-controlled text in email subjects is sanitized (control chars stripped, length capped at 60 runes) to prevent phishing via workspace name

### In-app Notifications

- **Inbox system**: `packages/core/inbox/` — server-pushed inbox items (issue assigned, comment received, autopilot completed, etc.)
- **Notification preferences**: Per-user notification settings stored in DB (`notification_preferences` table)
- **Real-time delivery**: Inbox items pushed via WebSocket events (`inbox:new`)

## File Storage

### AWS S3

- **SDK**: `aws-sdk-go-v2/service/s3` (v1.97.3)
- **Env vars**: `S3_BUCKET`, `S3_REGION` (default: us-west-2), `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_ENDPOINT_URL` (custom endpoints)
- **Storage class**: INTELLIGENT_TIERING (STANDARD for custom endpoints like MinIO)
- **Upload**: `storage.Storage.Upload()` — key + data + content-type + filename → public URL
- **URL construction**: CDN domain > custom endpoint > virtual-hosted-style > path-style (dots in bucket name trigger path-style fallback for TLS compatibility)

### AWS CloudFront (CDN + Signed Cookies)

- **SDK**: `aws-sdk-go-v2/service/secretsmanager` (v1.41.5) for private key retrieval
- **Env vars**: `CLOUDFRONT_KEY_PAIR_ID`, `CLOUDFRONT_PRIVATE_KEY_SECRET` (Secrets Manager name/ARN), `CLOUDFRONT_PRIVATE_KEY` (base64 PEM fallback), `CLOUDFRONT_DOMAIN`
- **Signing**: RSA SHA-1 signed cookies with configurable expiration
- **Cookie domain**: Shared with `COOKIE_DOMAIN` env var
- **Private key resolution**: AWS Secrets Manager first, then env var fallback

### Local File Storage (Fallback)

- **Env vars**: `LOCAL_UPLOAD_DIR` (default: `./data/uploads`), `LOCAL_UPLOAD_BASE_URL` (default: `http://localhost:8280`)
- **Activated when**: `S3_BUCKET` is unset

### Attachment Support

- Issues and comments support file attachments (images, documents)
- CLI command: `multica attachment download <id>`
- Upload via `--attachment <path>` flag on issue create and comment add

## Third-party APIs

### Google OAuth 2.0

- **Token exchange**: `https://oauth2.googleapis.com/token`
- **User info**: `https://www.googleapis.com/oauth2/v2/userinfo`
- **Data retrieved**: email (required), name, avatar URL

### PostHog (Product Analytics)

- **Server-side**: `server/internal/analytics/posthog.go` — custom HTTP batch client (not the PostHog Go SDK)
- **Frontend**: `posthog-js` (^1.176.1) via `packages/core/analytics/`
- **Env vars**: `POSTHOG_API_KEY`, `POSTHOG_HOST` (default: `https://us.i.posthog.com`), `ANALYTICS_ENVIRONMENT`, `ANALYTICS_DISABLED`
- **Event tracking**: 20+ product events (signup, workspace_created, runtime_registered, issue_executed, agent_task_queued/dispatched/started/completed/failed, autopilot_run_*, team_invite_*, onboarding_*, etc.)
- **Batching**: Non-blocking bounded queue (1024 events), flushes every 10 seconds or when batch reaches 64
- **No-op mode**: When `POSTHOG_API_KEY` is empty, analytics are completely disabled (local dev, self-hosted)

### GitHub (via Agent CLI)

- **Not a direct server integration** — agents interact with GitHub repos through their local CLI tools
- **Repo checkout**: `multica repo checkout <url> --ref <branch>` creates a git worktree from the daemon's bare clone cache
- **Repo cache**: `server/internal/daemon/repocache/` manages bare git clone caches with worktree-based checkout per task
- **Project resources**: `github_repo` type in project resources stores URL + default branch hint

### GitHub Actions (CI/CD)

- **Release workflow**: GoReleaser builds multi-platform Go binaries, publishes to GitHub Releases
- **Homebrew tap**: `multica-ai/tap/multica` auto-updated on release
- **GHCR**: Container images published to `ghcr.io/multica-ai/multica-backend` and `ghcr.io/multica-ai/multica-web`
- **Desktop auto-update**: electron-updater pulls from GitHub Releases (published release type, not draft)

### Prometheus (Metrics)

- **Library**: `prometheus/client_golang` (v1.23.2)
- **Env var**: `METRICS_ADDR` (default: disabled; recommended: `127.0.0.1:9090`)
- **Registered collectors**: Go runtime, process, HTTP request metrics, DB pool stats, realtime Hub metrics, daemon WS metrics
- **Security**: Recommended to bind to loopback only; protect with private networking or proxy auth if exposed

## S3-Compatible Storage Backends

The S3 integration supports alternative backends via the `AWS_ENDPOINT_URL` env var:

- MinIO
- Cloudflare R2
- Backblaze B2
- Wasabi

When a custom endpoint is set, the client uses path-style addressing and storage class defaults to STANDARD.
