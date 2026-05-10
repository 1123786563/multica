# Architecture

## System Overview

Multica is an AI-native task management platform with three runtime surfaces:

```
                    ┌─────────────────────┐
                    │    PostgreSQL 17     │
                    │  (pgvector enabled)  │
                    └──────────┬──────────┘
                               │
                    ┌──────────▼──────────┐
                    │    Go HTTP Server    │
                    │  (Chi, port 8280)    │
                    │                      │
                    │  handler → service   │
                    │       → sqlc → DB    │
                    │                      │
                    │  gorilla/websocket   │
                    │  Redis Streams relay │
                    └───┬─────────────┬───┘
                        │             │
               ┌────────▼───┐  ┌─────▼──────────┐
               │  Next.js   │  │   Electron      │
               │  Web App   │  │   Desktop App   │
               │  (port     │  │  (electron-vite)│
               │   3300)    │  │                 │
               └──────┬─────┘  └──────┬─────────┘
                      │               │
               ┌──────▼───────────────▼──────┐
               │     Shared Packages          │
               │  core/  ui/  views/          │
               │  tsconfig/  eslint-config/   │
               └──────────────────────────────┘
```

The Go server is a monolithic HTTP + WebSocket backend. The frontend is a monorepo (pnpm workspaces + Turborepo) sharing business logic across the Next.js web app and the Electron desktop app via internal packages that export raw `.ts`/`.tsx` files.

## Backend Architecture

### Layering: handler → service → sqlc → PostgreSQL

The Go backend follows a strict three-layer pattern:

1. **Handler layer** (`server/internal/handler/`) -- 50+ handler files, one per domain entity. Handlers parse HTTP requests, validate UUIDs, authorize membership, and call `db.Queries` (sqlc-generated) or `service.TaskService` / `service.AutopilotService`. They never contain business logic beyond request/response mapping.

2. **Service layer** (`server/internal/service/`) -- Encapsulates multi-step business logic that spans handler boundaries. Two primary services:
   - `TaskService` -- Task lifecycle (enqueue, claim, start, complete, fail, cancel, retry). Owns agent status reconciliation, task analytics capture, and dispatching wakeup notifications to daemons.
   - `AutopilotService` -- Autopilot lifecycle (schedule triggers, run execution, failure monitoring, retry logic).
   - `Orchestrator` -- Orchestration kernel for multi-node agent plans. Parses structured agent results, evaluates acceptance criteria, and enqueues follow-up node tasks.

3. **Data layer** (`server/pkg/db/`) -- sqlc-generated Go code from hand-written SQL queries in `server/pkg/db/queries/*.sql`. The generated code lives in `server/pkg/db/generated/` (models.go + ~30 query files). 101 migrations in `server/migrations/`.

### Handler struct

The `handler.Handler` struct is the central dependency container for the HTTP layer:

```go
type Handler struct {
    Queries            *db.Queries           // sqlc queries
    DB                 dbExecutor            // raw pool (for direct SQL)
    TxStarter          txStarter             // pgxpool.Pool (for transactions)
    Hub                *realtime.Hub         // browser WS hub
    DaemonHub          *daemonws.Hub         // daemon WS hub
    Bus                *events.Bus           // in-process event bus
    TaskService        *service.TaskService  // task lifecycle
    AutopilotService   *service.AutopilotService
    EmailService       *service.EmailService
    // ... Redis-backed stores, auth caches, storage backend, analytics
}
```

Construction is in `handler.New()`; the router wires additional Redis-backed stores conditionally.

### Middleware stack

Applied in order via Chi router:

1. `RequestID` -- Chi's built-in request ID
2. `ClientMetadata` -- X-Client-Platform, X-Client-Version, X-Client-OS headers
3. `RequestLogger` -- structured request logging
4. `HTTPMetrics` (optional) -- Prometheus-compatible HTTP metrics
5. `Recoverer` -- panic recovery → 500
6. `ContentSecurityPolicy` -- CSP headers
7. `cors.Handler` -- CORS with configurable origins
8. Route-specific:
   - `Auth(queries, patCache)` -- JWT cookie or `mul_` PAT token validation
   - `DaemonAuth(queries, patCache, daemonTokenCache)` -- daemon token or fallback to user auth
   - `RequireWorkspaceMember(queries)` -- resolves X-Workspace-Slug/X-Workspace-ID → workspace UUID + membership check
   - `RequireWorkspaceRole(...)` -- additionally checks role (owner, admin)

### UUID parsing convention

Three tiers enforced across all handlers:
- `parseUUID(s)` -- panics on invalid input. Used for DB round-trips only.
- `parseUUIDOrBadRequest(w, s, fieldName)` -- writes 400 on invalid. Used for raw user input.
- Resource loaders (`loadIssueForUser`, `loadAgentForUser`, etc.) -- resolve human-readable identifiers (e.g. `MUL-123`) and validate workspace membership.

### Router structure

Defined in `server/cmd/server/router.go`:
- `/health`, `/readyz`, `/healthz` -- unauthenticated health checks
- `/ws` -- browser WebSocket endpoint
- `/auth/*` -- public auth (send-code, verify-code, google, logout)
- `/api/config` -- public configuration
- `/api/daemon/*` -- daemon-authenticated routes (register, heartbeat, WS, task claim/complete/fail, gc-check)
- `/api/me`, `/api/upload-file`, `/api/cli-token`, `/api/feedback` -- user-authenticated, workspace-independent
- `/api/workspaces/*` -- workspace CRUD, membership, invitations
- `/api/invitations/*` -- user's invitations
- `/api/tokens/*` -- PAT management
- `/api/*` (workspace-scoped) -- issues, labels, projects, agents, skills, runtimes, tasks, chat, inbox, autopilots, pins, usage, notifications

### Real-time system

The real-time subsystem is multi-layered:

1. **Event Bus** (`server/internal/events/`) -- in-process synchronous pub/sub. Handlers and services publish `Event{Type, WorkspaceID, ActorID, Payload}`. No external dependencies.

2. **Hub** (`server/internal/realtime/hub.go`) -- WebSocket connection manager using scope-based rooms. Scopes: `workspace`, `user`, `task`, `chat`, `daemon_runtime`. Clients subscribe/unsubscribe via WS frames. Slow clients are evicted.

3. **Redis Relay** (optional, multi-node) -- when `REDIS_URL` is set:
   - `ShardedStreamRelay` -- partitions events by scope into Redis Streams, uses XREADGROUP consumers per scope
   - `DualWriteBroadcaster` -- writes events to both local Hub (fast path) and Redis relay (cross-node delivery)
   - `MirroredRelay` -- dual-write to both sharded and legacy stream for migration
   - Client-side dedup via per-connection LRU of seen event IDs (ULID-based)

4. **Listener wiring** (`server/cmd/server/listeners.go`) -- maps event types to broadcast actions:
   - Personal events (inbox, invitations) → `SendToUser(userID)`
   - Workspace events → `BroadcastToWorkspace(workspaceID)`
   - Daemon events → `Broadcast()`

5. **Daemon WebSocket** (`server/internal/daemonws/`) -- separate hub for daemon connections. Supports WebSocket-based heartbeats and task-wakeup notifications.

### Auth flow

Auth uses email verification codes (Resend) with JWT cookies:
- `POST /auth/send-code` → email with 6-digit code
- `POST /auth/verify-code` → sets `multica_auth` HttpOnly cookie (HMAC-signed JWT)
- `POST /auth/google` → Google OAuth flow
- CSRF protection: cookie-based auth requires `X-CSRF-Token` header for state-changing requests
- PATs (`mul_` prefix): hashed in DB, cached in Redis with TTL, `last_used_at` updated once per TTL window
- Desktop: `mdt_` daemon tokens for CLI/daemon auth, separate cache

### Multi-tenancy

All data is workspace-scoped:
- `X-Workspace-Slug` or `X-Workspace-ID` header on every request
- Middleware resolves slug → UUID, injects workspace ID and member into context
- All sqlc queries filter by `workspace_id`
- Membership checks gate access; role-based authorization for admin/owner actions
- Reserved slugs (`/login`, `/workspaces`, etc.) live in `reserved_slugs.json` and are code-generated to TypeScript

## Frontend Architecture

### Monorepo structure

```
pnpm workspaces + Turborepo
├── apps/web/          -- Next.js (App Router)
├── apps/desktop/      -- Electron (electron-vite)
├── packages/core/     -- headless business logic (zero react-dom)
├── packages/ui/       -- atomic UI components (zero business logic)
├── packages/views/    -- shared business pages (zero framework imports)
├── packages/tsconfig/ -- shared TypeScript configs
└── packages/eslint-config/ -- shared ESLint configs
```

### Internal packages pattern

All shared packages export raw `.ts`/`.tsx` files -- no build step, no `dist/`. The consuming app's bundler (Next.js or electron-vite) compiles them directly. This gives:
- Zero-config HMR across package boundaries
- Instant go-to-definition (source maps are trivial)
- Single compilation pipeline

### Package dependency graph

```
apps/web       → packages/views, packages/core, packages/ui
apps/desktop   → packages/views, packages/core, packages/ui
packages/views → packages/core, packages/ui
packages/core  → (external deps only: zustand, tanstack-query, zod, i18next)
packages/ui    → (external deps only: @base-ui/react, tailwind)
```

**Hard boundaries:**
- `packages/ui/` -- zero `@multica/core` imports
- `packages/views/` -- zero `next/*`, zero `react-router-dom`, zero Zustand stores
- `packages/core/` -- zero `react-dom`, zero `localStorage`, zero `process.env`

### Platform bridge pattern

`CoreProvider` from `packages/core/platform/` is the shared initialization point:

```
CoreProvider
├── ApiClient (HTTP client, token management)
├── AuthStore (Zustand: user, session state)
├── ChatStore (Zustand: active chat session)
├── QueryProvider (TanStack Query client)
├── AuthInitializer (session recovery)
├── WSProvider (WebSocket connection, auto-reconnect)
└── I18nProvider (i18next, locale sync)
```

Each app wraps its root with `<CoreProvider>` and provides platform-specific props.

### NavigationAdapter

`packages/views/navigation/context.tsx` provides `useNavigation()` and `<AppLink>`. Each app injects its adapter:

- **Web** (`apps/web/platform/navigation.tsx`) -- wraps Next.js `useRouter()`, `usePathname()`, `useSearchParams()`
- **Desktop** (`apps/desktop/src/renderer/src/platform/navigation.tsx`) -- wraps react-router-dom `DataRouter`, handles:
  - Cross-workspace navigation → tab store workspace switch
  - Transition flows → `WindowOverlay` (new-workspace, invite, onboarding)
  - Per-tab router subscription for shell-level pathname sync

### Desktop tab system

Desktop uses per-workspace tab groups with in-memory `DataRouter` instances:
- `stores/tab-store.ts` -- tab CRUD, workspace grouping, active tab tracking
- `stores/window-overlay-store.ts` -- overlay state for pre-workspace flows
- `TabNavigationProvider` -- per-tab router wrapper
- `DesktopNavigationProvider` -- root-level provider that mirrors active tab router

## State Management

### Server state: TanStack Query

All data fetched from the API lives in the TanStack Query cache. Key patterns:

- **Workspace-scoped query keys** -- every query includes `wsId` so workspace switching is automatic
- **WS events invalidate queries** -- `useRealtimeSync` (core/realtime) maps 40+ event types to targeted `qc.invalidateQueries()` calls
- **Debounced prefix invalidation** -- rapid events (bulk updates) are debounced by event prefix (100ms)
- **Optimistic mutations** -- mutations apply locally first, rollback on error, invalidate on settle
- **Schema validation** -- `parseWithFallback(data, schema, fallback)` from `packages/core/api/schema.ts` validates API responses with zod, returns fallback on failure

### Client state: Zustand

All shared Zustand stores live in `packages/core/` (never in `packages/views/`). Key stores:

- `issues/stores/view-store.ts` -- list/board view mode, sort, filters, column layout
- `issues/stores/draft-store.ts` -- comment and description drafts
- `issues/stores/selection-store.ts` -- multi-select state
- `issues/stores/comment-collapse-store.ts` -- thread collapse state
- `chat/` -- active chat session, unread state
- `auth/` -- user, session, login/logout actions
- `platform/workspace-storage.ts` -- `setCurrentWorkspace(slug, uuid)` singleton for URL-driven workspace identity

**Hard rule:** server data is never duplicated into Zustand stores.

## Data Flow

### HTTP request lifecycle

```
Client → Chi middleware stack → Handler
  → parseUUID / parseUUIDOrBadRequest
  → resolveWorkspaceID (slug → UUID via DB)
  → requireWorkspaceMember / requireWorkspaceRole
  → Queries.Xxx(ctx, params)      [sqlc, read path]
  → Queries.XxxTx(ctx, params)    [sqlc, write path, inside transaction]
  → Bus.Publish(event)            [async side effects]
  → writeJSON(w, status, response)
```

### WebSocket event flow

```
Handler/Service publishes event
  → events.Bus.Publish(Event)
    → type-specific listeners (listeners.go)
    → global listener (SubscribeAll)
      → Broadcaster.BroadcastToWorkspace / SendToUser
        → Hub.BroadcastToScope (local)
        → Redis Relay (multi-node, if configured)
          → Hub.BroadcastToScopeDedup (remote, deduped)

Client receives WS frame
  → WSClient.onMessage dispatches to handlers
    → useRealtimeSync invalidates TanStack Query cache
    → Components re-render from fresh query data
```

### Task lifecycle (agent execution)

```
1. Enqueue: user mentions agent / quick-create / chat message / autopilot trigger
   → TaskService.EnqueueTaskForIssue / EnqueueChatTask / etc.
   → INSERT INTO agent_task_queue (status='queued')
   → Bus.Publish("task:queued")
   → Wakeup.NotifyTaskAvailable(runtimeID, taskID)

2. Claim: daemon polls /tasks/claim or receives WS wakeup
   → TaskService.ClaimTaskForRuntime
   → SELECT ... FOR UPDATE SKIP LOCKED (prevents double-claim)
   → UPDATE status='dispatched'
   → Bus.Publish("task:dispatch")

3. Start: daemon invokes the agent CLI
   → TaskService.StartTask
   → UPDATE status='running'

4. Progress: daemon reports streaming messages
   → TaskService.ReportProgress / ReportTaskMessages
   → Bus.Publish("task:message") → WS → live timeline

5. Complete/Fail: agent finishes
   → TaskService.CompleteTask / FailTask
   → UPDATE status='completed'/'failed'
   → TaskService.MaybeRetryFailedTask (on failure)
   → Orchestrator.OnTaskComplete (if orchestration node)
   → Bus.Publish("task:completed"/"task:failed")
   → WS → query invalidation → UI update
```

## Authentication & Multi-tenancy

### Auth flow

```
Browser/Desktop → POST /auth/send-code {email}
                ← 200 (code sent via Resend)
                → POST /auth/verify-code {email, code}
                ← Set-Cookie: multica_auth=<JWT>; HttpOnly; SameSite=Lax
```

The JWT contains `{sub: userID, email}`. Subsequent requests send the cookie automatically. PATs (`mul_` prefix) are used by CLI and daemon for programmatic access.

### Workspace isolation

- Every request to workspace-scoped endpoints carries `X-Workspace-Slug` header
- Middleware resolves slug → UUID, verifies membership, injects context
- All DB queries include `WHERE workspace_id = $1`
- WS connections are scoped to a single workspace (query param)
- Frontend: `setCurrentWorkspace(slug, uuid)` singleton in `packages/core/platform/workspace-storage.ts`

## Agent System

### Architecture

Agents are first-class entities in Multica, stored in the `agents` table with workspace scoping. Each agent has:
- Skills (many-to-many via `agent_skills`)
- A runtime (local daemon or cloud) that executes tasks
- Assignee status (agents can be assigned issues, appear with purple styling + robot icon)

### Daemon

The daemon (`server/internal/daemon/`) is a long-running local process that:
- Registers runtimes (one per agent CLI binary detected on the host)
- Polls the server for available tasks (HTTP or WS wakeup)
- Manages git worktrees for task execution (via `repocache` package)
- Reports heartbeat, task progress, and results back to the server
- Supports self-update (downloads new binary, restarts)

### Runtimes

Runtimes represent the execution environment for an agent. The server tracks:
- Online/offline status (heartbeat + sweeper)
- Local skills (CLI commands available on the daemon host)
- Models (LLM models supported by the runtime)
- Usage metrics (token counts, cost, duration per task)

### Task queue

The `agent_task_queue` table is the central task queue:
- `status` transitions: `queued → dispatched → running → completed/failed/cancelled`
- Claim uses `SELECT ... FOR UPDATE SKIP LOCKED` for safe concurrent claiming
- Retry logic on failure (configurable per agent, up to 3 retries)
- Orphan recovery (daemon detects stale running tasks after restart)

## Orchestration Kernel

The orchestration kernel (migration 081, new code) enables multi-node agent plans:

### Data model

- **OrchestrationPlan** -- defines a directed acyclic graph of nodes
- **OrchestrationNode** -- individual step with input/output contracts, acceptance criteria
- **OrchestrationRun** -- execution instance of a plan
- Node types: `agent_task` (run an agent), `evaluation` (judge results), `parallel` (fan-out)

### Lifecycle

1. Plan created with nodes and edges
2. Run starts → root nodes enqueued as tasks via `TaskService`
3. Each task carries `OrchestrationContext` (plan_id, node_id, run_id, contracts)
4. On task completion, `Orchestrator.OnTaskComplete`:
   - Parses `AgentStructuredResult` from task output
   - Evaluates acceptance criteria
   - If node has downstream dependents whose inputs are now satisfied, enqueues their tasks
5. Run completes when all terminal nodes reach a terminal state

### Evaluation

The `AgentStructuredResult` schema includes:
- `status`, `summary`, `confidence`
- `artifacts` (typed output: code, document, etc.)
- `changed_files`, `test_result`
- `claims`, `criteria_evidence` (maps to acceptance criteria)
- `risks`, `next_actions`

The orchestrator evaluates these against node-level acceptance criteria to determine pass/fail/retry.
