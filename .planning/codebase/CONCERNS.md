# Technical Concerns & Debt

_Generated 2026-05-10 from codebase analysis of /Users/yongjunwu/my-idea/multica (commit e076bbaf)._

---

## Security Concerns

### JWT Secret Defaults to Hardcoded Dev Value

- **File:** `server/internal/auth/jwt.go:15`
- The constant `defaultJWTSecret = "multica-dev-secret-change-in-production"` is used when `JWT_SECRET` env var is unset. There is no startup warning or health-check failure when the default is active. Any deployment missing the env var silently runs with a publicly known secret.
- **Risk:** Critical if deployed without the env var. Token forgery becomes trivial.

### Internal Error Details Leaked in HTTP Responses

- **Files:** 30+ handler endpoints across `server/internal/handler/*.go`
- Pattern: `writeError(w, http.StatusInternalServerError, "failed to X: "+err.Error())`
- Examples:
  - `server/internal/handler/agent.go:512` — `"failed to create agent: "+err.Error()`
  - `server/internal/handler/agent.go:690` — `"failed to update agent: "+err.Error()`
  - `server/internal/handler/comment.go:230` — `"failed to create comment: "+err.Error()`
  - `server/internal/handler/runtime_update.go:268` — `"failed to load update: "+err.Error()`
- **Risk:** Internal error messages (PostgreSQL error text, file paths, connection details) can reach the client. This is a low-severity information disclosure.

### Google OAuth Token Body Logged at Error Level

- **File:** `server/internal/handler/auth.go:492`
- `slog.Error("google oauth token exchange returned error", "status", tokenResp.StatusCode, "body", string(tokenBody))`
- The full response body from Google's token endpoint is logged when status is non-200. This may contain `access_token` or `id_token` fields in error responses.
- **Risk:** Sensitive OAuth tokens could end up in log aggregation systems.

### Unbounded Request Body Reads (No Size Limit)

- **Files (4 unbounded `io.ReadAll(r.Body)` calls without `MaxBytesReader`):**
  - `server/internal/handler/autopilot.go:324`
  - `server/internal/handler/issue.go:1378` (UpdateIssue)
  - `server/internal/handler/issue.go:1732` (BatchUpdateIssues)
  - `server/internal/handler/project.go:363`
- The `file.go` upload handler correctly uses `http.MaxBytesReader` (100MB limit) and `skill.go` uses `io.LimitReader`, but the JSON endpoints above read unbounded bodies.
- **Risk:** A malicious client can send multi-GB bodies to exhaust server memory.

### WebSocket Origin Bypass for Mobile Clients

- **File:** `server/internal/realtime/hub.go:87-90`
- When `client_platform=mobile` is in the query string and there is no auth cookie, the origin check is skipped entirely. This was added in commit `4b8939e7` to support mobile WebSocket connections.
- **Risk:** Any attacker can bypass origin validation by sending `?client_platform=mobile` without cookies. If the WebSocket protocol has any actions beyond what a normal authenticated user can do, this could be exploited.

### No Rate Limiting on Authentication Endpoints

- No rate limiting middleware was found anywhere in the codebase. The `SendCode` endpoint (`server/internal/handler/auth.go:253`), `GoogleLogin` endpoint (`auth.go:446`), and `CreateInvitation` endpoint (`invitation.go:56`) have no request-rate guards.
- **Risk:** Brute-force verification codes, OAuth abuse, invitation spam.

### Label Name Allows Control Characters

- **File:** `server/internal/handler/label.go:93`
- `TODO(labels): consider restricting to a charset that excludes newlines, tabs, and control characters.`
- Label names can contain newlines and control characters, which may cause rendering issues or XSS vectors in downstream UI.

### CSP Includes `unsafe-inline` for Styles

- **File:** `server/internal/middleware/csp.go`
- `"style-src 'self' 'unsafe-inline'"` is present in the Content Security Policy. This is a common pattern with CSS-in-JS and Tailwind but weakens XSS protection for style injection attacks.

---

## Performance Concerns

### N+1 Query: BatchUpdateIssues

- **File:** `server/internal/handler/issue.go:1794-1855`
- `BatchUpdateIssues` iterates over `req.IssueIDs`, executing `GetIssueInWorkspace` + `UpdateIssue` per ID in a loop. Each iteration is a separate DB round-trip.
- A batch of 50 issues = 100 DB queries.
- This should be replaced with a single batch update query.

### N+1 Query: BroadcastCancelledTasks

- **File:** `server/internal/service/task.go:642-646`
- Iterates over `cancelled` tasks, calling `ReconcileAgentStatus` per task. Each reconcile runs `RefreshAgentStatusFromTasks` — a DB query per call. Tasks for the same agent cause redundant reconciles.
- Partial mitigation exists at line 1387 where `affectedAgents` deduplicates, but `BroadcastCancelledTasks` does not deduplicate.

### N+1 Query: LoadAgentSkills

- **File:** `server/internal/service/task.go:1467-1474`
- For each skill, calls `ListSkillFiles` individually. An agent with 10 skills makes 10 DB queries.
- Should be replaced with a single JOIN query.

### Workspace Slug-to-UUID Lookup Not Cached

- **File:** `server/internal/middleware/workspace.go:98`
- `// TODO: cache slug→UUID lookup (slug is immutable, safe to cache with short TTL)`
- Every authenticated request with a slug header hits the DB to resolve the workspace UUID. Under high QPS this becomes a hot-path bottleneck.

### SELECT * in 88 SQL Queries

- **Files:** `server/pkg/db/queries/*.sql`
- 88 queries use `SELECT *` which fetches all columns including potentially large JSONB fields (`context_refs`, `policy`, `metadata`, etc.) even when callers only need a few columns.
- While sqlc generates typed structs and the bandwidth is local (same-host DB), this wastes memory on large rows.

### Dynamic Search Query Construction

- **Files:** `server/internal/handler/issue.go:268-486`, `server/internal/handler/project.go:530-598`
- Issue and project search build dynamic SQL via `fmt.Sprintf` with parameterized placeholders (`$1`, `$2`, etc.). The `nextArg` pattern is safe (uses parameterized queries), but the approach makes query plans opaque to analysis and prevents sqlc compile-time verification.
- The LIKE-based search with `LOWER()` on every row may not leverage GIN indexes effectively for very large tables.

### Task Usage Rollup (Resolved, but Pattern Worth Noting)

- **File:** `server/migrations/073_task_usage_daily_rollup.up.sql`
- The `SUM() GROUP BY DATE(created_at)` on raw `task_usage` was causing DB load spikes (commit `eb067ff0`). This was resolved with a daily rollup materialization table. The pattern of "expensive aggregate on growing event table" is a known category — similar patterns should be watched in `orchestration_event` (migration 081).

### Full Timeline Re-render on WS Events (Recently Fixed)

- **Commit:** `80720108` (perf(issues): stop full timeline re-render on every WS event)
- `useCreateComment.onSettled` invalidated the timeline query on every comment submit, causing every thread to re-render. Fixed by dropping the redundant invalidation since WS broadcasts already keep the cache fresh.
- Pattern to watch: any `onSettled` that invalidates a broad query key may re-introduce this.

---

## Architectural Concerns

### API Response Compatibility Is Opt-In Per Endpoint

- **Files:** `packages/core/api/client.ts`, `packages/core/api/schemas.ts`
- Only 7-8 endpoints use `parseWithFallback` with Zod schemas. The remaining ~60+ endpoints use the generic `this.fetch<T>()` which does `res.json() as Promise<T>` — a bare type assertion with no runtime validation.
- The `Attachment` upload endpoint (`client.ts:1042`) uses raw `res.json() as Promise<Attachment>`.
- Three concrete incidents already occurred from this pattern (#2143, #2147, #2192). The desktop app cannot be force-updated, so every unprotected endpoint is a ticking bomb.

### Dynamic SQL in Handler Package (Outside sqlc)

- The `buildSearchQuery` functions in `issue.go` and `project.go` build dynamic SQL with `fmt.Sprintf`. This bypasses sqlc's compile-time type checking and makes query correctness dependent on runtime string construction.
- The parameterization is correct (no injection risk), but the pattern is fragile — adding a new filter requires careful arg-index tracking.

### Goroutine Fire-and-Forget in Auth Middleware

- **File:** `server/internal/middleware/auth.go:87`
- `go queries.UpdatePersonalAccessTokenLastUsed(context.Background(), pat.ID)`
- Uses `context.Background()` with no error handling. If the DB is down, the goroutine silently fails. No cancellation or timeout.

### Heavy Goroutine Usage in Daemon

- **File:** `server/internal/daemon/daemon.go`
- 9 `go func` launches in this file alone. Not all use context-based cancellation — some rely on channel closes or WaitGroups, which can leak if the parent context is cancelled unexpectedly.

### No API Versioning Strategy

- The API has no version prefix (all routes are `/api/...`). The backward-compatibility approach relies entirely on frontend defensive parsing (see API Response Compatibility above). There is no mechanism to deprecate or sunset endpoints.

---

## Tech Debt

### TODO Comments (3 found)

| File | Line | Comment |
|---|---|---|
| `server/internal/handler/label.go` | 93 | `TODO(labels): consider restricting to a charset that excludes newlines, tabs, and control characters` |
| `server/internal/middleware/workspace.go` | 98 | `TODO: cache slug→UUID lookup (slug is immutable, safe to cache with short TTL)` |
| `server/pkg/agent/kiro.go` | 270 | `TODO: drop one field once Kiro lands on a single canonical payload` |

### Orchestration Kernel (Unmerged WIP)

- **Files:** `server/migrations/081_orchestration_kernel.*`, `server/internal/service/orchestrator.go`, `server/internal/handler/orchestration.go`, `server/internal/service/orchestrator_test.go`
- These files are in the working tree but not committed. The migration adds 5 new tables (`orchestration_plan`, `orchestration_node`, `orchestration_edge`, `orchestration_event`, `orchestration_artifact`) with extensive JSONB columns. The `orchestration_event` table has no rollup strategy and may repeat the `task_usage` performance pattern.

### Legacy Timeline API Compatibility Shim

- **Commit:** `11a6288c` (fix(timeline): legacy array shape for pre-#2128 clients)
- The server detects legacy callers (no pagination params) and returns the old bare array format. This shim must be maintained until all desktop clients are on v0.2.26+.

---

## Test Coverage Gaps

### Go Handler Tests (17 files without tests)

The following handler files have no corresponding `_test.go`:

| File | Risk | Notes |
|---|---|---|
| `auth.go` | High | Core auth flow: signup, login, OAuth |
| `comment.go` | High | Comment CRUD, XSS surface |
| `issue.go` | High | Issue CRUD, search, batch update |
| `daemon_ws.go` | Medium | Daemon WebSocket handler |
| `autopilot.go` | Medium | Autopilot CRUD + trigger |
| `chat.go` | Medium | Chat session management |
| `inbox.go` | Medium | Inbox notification logic |
| `project.go` | Medium | Project CRUD + search |
| `skill_create.go` | Medium | Skill creation/import |
| `orchestration.go` | Medium | New orchestration endpoints |
| `personal_access_token.go` | Medium | PAT management |
| `notification_preference.go` | Low | Notification settings |
| `issue_reaction.go` | Low | Reaction toggle |
| `pin.go` | Low | Pin/unpin |
| `reaction.go` | Low | Reaction CRUD |
| `workspace_reserved_slugs.go` | Low | Slug validation |
| `task_lifecycle.go` | Medium | Task lifecycle management |

### Go Service Tests (2 files without tests)

| File | Risk |
|---|---|
| `service/task.go` | High — core task orchestration logic |
| `service/cron.go` | Medium — scheduled job management |

### Frontend Component Tests (40+ files without tests)

Key gaps in `packages/views/`:
- `issues/components/`: 20 files untested (board view, list view, comment card, execution log)
- `settings/components/`: 8 files untested (all settings tabs)
- `layout/`: 7 files untested (dashboard guard, workspace loader)

### E2E Test Coverage

Only 5 E2E test files exist (`auth`, `comments`, `issues`, `navigation`, `settings`). Missing coverage for:
- Agent management flows
- Autopilot creation and execution
- Chat sessions
- File uploads
- Batch operations
- Desktop-specific behaviors (tabs, workspaces switching)

---

## Dependency Risks

### Frontend

| Dependency | Version | Risk |
|---|---|---|
| `next` | `^16.2.3` | Major version (16) — fast-moving, frequent breaking changes |
| `react` / `react-dom` | `19.2.3` | React 19 is very recent; ecosystem still catching up |
| `@tiptap/*` (12 packages) | `^3.22.1` | Heavy editor dependency surface — 12 separate packages |
| `recharts` | `3.8.0` | Pinned exact version; may indicate compatibility issues |
| `electron-updater` | `^6.8.3` | Auto-update mechanism — supply chain risk |
| `lowlight` | `^3.3.0` | Used for code highlighting — adds grammar data to bundle |

### Backend

| Dependency | Version | Risk |
|---|---|---|
| `golang-jwt/jwt/v5` | `5.3.1` | HMAC-only (no RSA/ECDSA) — acceptable but limits key rotation strategies |
| `gorilla/websocket` | `1.5.3` | Gorilla is archived; this is the last release. No future security fixes. |
| `pgx/v5` | `5.8.0` | Current and well-maintained |
| `redis/go-redis/v9` | `9.18.0` | Current |

### Key Risk: gorilla/websocket Is Archived

- The `gorilla/websocket` library was archived by its maintainers in 2022 and brought back in 2023 under new ownership, but the project's long-term maintenance is uncertain. This is the sole WebSocket library powering real-time features.

---

## Known Issues

### Recent Bug Fixes (Last 80 Commits — 45 Were Bug Fixes)

#### High-Impact Incidents

| Commit | Issue | Description |
|---|---|---|
| `e076bbaf` | #2334 | OpenAI Codex/GPT models showing $0 cost — pricing data missing |
| `80720108` | #2329 | Full timeline re-render on every WS event — caused UI flashing during AI streaming |
| `c5754615` | #2323 | Provider 429/out-of-credit agent runs marked as `completed` instead of `failed` |
| `eb067ff0` | #2256 | `task_usage` aggregate queries dominating DB load — fixed with daily rollup table |
| `11a6288c` | #2143, #2147 | Timeline API shape change broke all desktop clients <= v0.2.25 — "timeline.filter is not a function" |
| `099dda06` | #2192 | Timeline off-by-one: exact-limit comments failed to show "Show older" |
| `48e3131b` | #2208 | Desktop frontend hardening against API response drift (MUL-1828) |
| `6b7294aa` | #2076 | Linux Cellar deletion orphaning runtimes after brew upgrade |

#### Recurring Patterns

1. **API response shape drift** (3 incidents: #2143, #2147, #2192): Desktop clients break when backend response shapes change. The `parseWithFallback` system was built in response but coverage is still partial.

2. **Timeline rendering** (4 commits): The timeline/comment system has been the source of repeated bugs — orphaned replies, off-by-one pagination, full re-renders, state sync issues. The area has high churn.

3. **Agent runtime edge cases** (6+ commits): Poisoned-image sessions (6d9ebb0f), ACP failure promotion (b73a301b), OpenClaw version blocking (0af67c81), stderr/stdout confusion (af971e1e), inline system prompt whitelist (d713b570). Agent runtime integration is the most fragile surface.

4. **Daemon self-management** (3 commits): Brew prefix symlinks (6b7294aa), health responsiveness (f1dc3dc9), GC extension (823f124d). The daemon's self-update and health systems need careful attention.

#### Reverted Commit

- `d964d37f` — Reverted `--content-file` / `--description-file` CLI flags for non-ASCII on Windows (original #2247, reverted #2252).

---

## Migration Concerns

### Pattern: Retroactive Index Additions

Multiple migrations add indexes that should have existed from the start:
- `074_task_usage_updated_at_index.up.sql` — Missing `updated_at` index on `task_usage`
- `075_task_usage_created_at_index.up.sql` — Missing `created_at` index on `task_usage`
- `078_task_usage_created_at_legacy_index.up.sql` — Another `created_at` index
- `080_agent_task_queue_queued_index.up.sql` — Missing partial index for queued tasks (89k+ row table was doing full scans)

This suggests new tables are being created without analyzing query patterns upfront. The `CONCURRENTLY` pattern used for all index additions indicates these were added to production after performance issues appeared.

### Down Migrations Are Destructive

- `081_orchestration_kernel.down.sql` drops 5 tables with `DROP TABLE IF EXISTS`. This is standard but means rollback destroys all orchestration data with no recovery path.
- `073_task_usage_daily_rollup.down.sql` drops the rollup table, which would restore the expensive raw-query pattern.

### Orchestration Kernel Migration (081) Concerns

- New `orchestration_event` table has no rollup or archiving strategy. If orchestration is heavily used, this table will grow unbounded like `task_usage` did before the daily rollup fix.
- Heavy use of JSONB columns (`policy`, `metadata`, `input_contract`, `output_contract`, `evaluator_policy`, `retry_policy`, `runtime_constraints`). These are opaque to SQL analysis and may cause performance issues as data grows.
- No foreign key from `orchestration_node.assignee_agent_id` to a workspace — cross-workspace agent assignment is not prevented at the DB level.
