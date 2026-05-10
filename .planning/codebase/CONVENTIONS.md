# Coding Conventions

This document codifies the coding conventions, patterns, and architectural rules found across the Multica codebase. Authoritative rules are defined in `CLAUDE.md` and `apps/docs/content/docs/developers/conventions.mdx`.

## TypeScript Conventions

### Strict Mode and Types
- TypeScript strict mode is enabled; keep types explicit.
- Types use `PascalCase` (`Issue`, `AgentRuntime`). Never use `IPrefix` or `_t` suffix.
- Enums: prefer string literal unions (`type IssueStatus = "backlog" | "todo" | ...`). Reserve `enum` for runtime-iterable cases.
- API responses on the wire are `snake_case`; the API client converts to `camelCase` at the boundary. Inside TS code, always `camelCase`.

### Type Definitions
- All domain types live in `packages/core/types/` (e.g., `issue.ts`, `agent.ts`, `workspace.ts`).
- Types are simple interfaces with explicit field types. Nullable fields use `string | null` or `* | null`.
- String literal union types for enums: `IssueStatus`, `IssuePriority`, `IssueAssigneeType`.

### TanStack Query Key Factories
- Query keys use factory functions in `<feature>/queries.ts` (e.g., `issueKeys.detail(id)`).
- All workspace-scoped queries must key on `wsId` so workspace switching is automatic.
- Example pattern from `packages/core/issues/queries.ts`:
  ```ts
  export const issueKeys = {
    all: (wsId: string) => ["issues", wsId] as const,
    list: (wsId: string) => [...issueKeys.all(wsId), "list"] as const,
    detail: (wsId: string, id: string) => [...issueKeys.all(wsId), "detail", id] as const,
  };
  ```

### API Response Compatibility
- Parse API responses with `parseWithFallback` from `packages/core/api/schema.ts` — never bare `as` casts on response bodies.
- `parseWithFallback(data, schema, fallback, { endpoint })` validates via zod and returns `fallback` on failure without throwing.
- Schemas are intentionally lenient (`z.string()` for enums so unknown values still parse). Downstream code handles unknown enum values via `default` branches.
- Optional-chain and default everywhere downstream. Use explicit boolean checks (`=== true`) over truthy/falsy negation.
- When adding or changing an endpoint, add the schema in the same PR and write at least one test with malformed input.

## Go Conventions

### Naming and Formatting
- Standard `gofmt` + `go vet`. No exceptions.
- Handler files mirror domain: `agent.go`, `auth.go`, `runtime.go`, `issue.go`.
- Tests colocated as `<file>_test.go` in the same package.

### Handler Patterns
- Chi router (`github.com/go-chi/chi/v5`) for HTTP routing.
- Handlers are methods on the `Handler` struct, which holds `*db.Queries`, `dbExecutor`, `TxStarter`, and domain services.
- Response types use structs with `json:` tags (e.g., `IssueResponse`, `AgentResponse`). Fields are `snake_case` in JSON.
- Responses use helper functions: `writeJSON(w, status, v)`, `writeError(w, status, msg)`.

### UUID Parsing Convention
Three-tier system for UUID handling (established after bug #1661):

1. **`parseUUIDOrBadRequest(w, s, fieldName)`** — for unvalidated user input (URL params, request bodies, headers). Writes 400 on invalid input; returns `(uuid, ok)`.
2. **`parseUUID(s)`** (panicking variant) — for trusted round-trips (sqlc-returned UUIDs passed back into queries, test fixtures). A panic here means a raw user-input string slipped in — that is a real bug. Chi's `middleware.Recoverer` converts to 500.
3. **`util.ParseUUID(s) (pgtype.UUID, error)`** — the only safe variant outside the handler package. Always check the error.

Resource loaders (e.g., `loadIssueForUser`, `loadAgentForUser`) resolve identifiers (both UUID and human-readable like `MUL-123`) before any write operation. All subsequent DB calls use `entity.ID` from the resolved object.

### Error Handling
- `isNotFound(err)` checks `pgx.ErrNoRows`.
- `isUniqueViolation(err)` checks PostgreSQL error code `23505`.
- Helper functions return `(value, bool)` where `false` means the handler already wrote an error response.
- Domain events published via `h.publish()`, `h.publishTask()`, `h.publishChat()`.

### Workspace Middleware
- `middleware.RequireWorkspaceMember(queries)` — validates membership, injects member + workspace ID into context.
- `middleware.RequireWorkspaceRole(queries, roles...)` — additionally checks role.
- `middleware.RequireWorkspaceMemberFromURL(queries, param)` — resolves from chi URL param.
- Workspace resolution priority: context > `X-Workspace-Slug` header > `?workspace_slug` query > `X-Workspace-ID` header > `?workspace_id` query.

## React Component Patterns

### shadcn / Base UI
- All UI primitives use shadcn components built on `@base-ui/react` (not Radix).
- Install via `pnpm ui:add <component>` — adds to `packages/ui/components/ui/`.
- Use shadcn design tokens (semantic tokens like `bg-background`, `text-muted-foreground`). Never hardcoded Tailwind colors.

### Component Location Rules
- Shared components: `packages/views/<domain>/` (e.g., `packages/views/issues/`).
- Atomic UI components: `packages/ui/components/ui/`.
- Platform-specific wiring only in `apps/web/platform/` or `apps/desktop/src/renderer/src/platform/`.
- Zero `next/*` or `react-router-dom` imports in shared code.

### Platform Bridge Pattern
- `CoreProvider` in `packages/core/platform/core-provider.tsx` initializes API client, auth/workspace stores, WS connection, and QueryClient.
- Each app wraps its root with `<CoreProvider>` and provides its own `NavigationAdapter`.
- `packages/core/platform/types.ts` defines `CoreProviderProps` with platform-agnostic configuration.

### State Management
- **TanStack Query** owns all server state. WS events invalidate queries (never write to stores directly).
- **Zustand** owns all client state. Stores live in `packages/core/` (never in `packages/views/`).
- Never duplicate server data into Zustand.
- Mutations are optimistic by default.
- Zustand store naming: `<feature>-store.ts`, exported as `use<Feature>Store`.
- Persisted stores use `persist` middleware with `createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage))`.

## API Conventions

### Request Format
- All API routes live under `/api/` prefix.
- Auth routes under `/auth/` (public, no auth required).
- Daemon routes under `/api/daemon/` (require daemon token or valid user token).
- Protected routes use `middleware.Auth(queries, patCache)`.
- Workspace-scoped routes use `middleware.RequireWorkspaceMember(queries)`.
- Workspace identified via `X-Workspace-Slug` or `X-Workspace-ID` headers.

### Response Format
- JSON responses using `writeJSON(w, status, v)`.
- Error responses: `{"error": "message"}` via `writeError(w, status, msg)`.
- Standard HTTP status codes: 200 (OK), 201 (Created), 204 (No Content), 400 (Bad Request), 401 (Unauthorized), 403 (Forbidden), 404 (Not Found), 409 (Conflict), 429 (Too Many Requests), 500 (Internal Server Error).
- List endpoints return `{ items: [...], total: N }` or just `[{...}]`.
- Nullable fields use `*string` (pointer) in Go response structs, rendered as `null` or omitted (`omitempty`) in JSON.

### Pagination
- Offset-based: `?limit=N&offset=M`.
- Issue board uses per-status buckets with `byStatus` key in the cache.

## Database Conventions

### sqlc Usage
- SQL queries defined in `server/pkg/db/queries/<domain>.sql`.
- sqlc generates Go code into `server/pkg/db/generated/`.
- Configuration in `server/sqlc.yaml`: PostgreSQL engine, `pgx/v5` driver, JSON tags enabled.
- Query annotations: `-- name: FunctionName :one/:many/:exec`.

### Migration Naming
- Files: `NNN_descriptive_name.up.sql` + `NNN_descriptive_name.down.sql` (always both directions).
- Sequential numbering (`001_init`, `002_agent_config`, ..., `081_orchestration_kernel`).
- Run via `make migrate-up` / `make migrate-down`.

### Table and Column Conventions
- Tables: `snake_case` singular (`user`, `workspace`, `agent_runtime`, `agent_task_queue`).
- Columns: `snake_case` (`workspace_id`, `created_at`, `last_seen_at`).
- Foreign keys: `<table>_id`.
- Booleans: `is_<state>` or `<state>_at` (timestamp form preferred for state changes).
- UUIDs: `gen_random_uuid()` default, stored as `pgtype.UUID` in Go.
- Timestamps: `TIMESTAMPTZ NOT NULL DEFAULT now()`.

### Reserved Slugs
- Source of truth: `server/internal/handler/reserved_slugs.json`.
- TypeScript generated from it: `packages/core/paths/reserved-slugs.ts` via `pnpm generate:reserved-slugs`.
- CI re-runs the generator and fails on drift.

## Naming Conventions

### Route Naming
- Pre-workspace routes: single word (`/login`, `/inbox`) or `/{noun}/{verb}` (`/workspaces/new`). Never hyphenated groups at root (`/new-workspace`).
- Workspace-scoped routes: `/{slug}/{section}` (`/:slug/issues`, `/:slug/agents`).

### File Naming
- TypeScript/TSX: `kebab-case.tsx` / `kebab-case.ts` (e.g., `agent-row-actions.tsx`).
- Components: `PascalCase` (e.g., `AgentRowActions`).
- Hooks: `useCamelCase` (e.g., `useWorkspaceId`).
- Stores (Zustand): `<feature>-store.ts`.
- Tests: colocated as `<file>.test.ts(x)`.
- Go: standard Go conventions (`gofmt`, `go vet`).

### Variable Naming
- TypeScript: `camelCase` for variables and functions.
- Go: standard Go conventions (exported = `PascalCase`, unexported = `camelCase`).

## i18n Conventions

### Translation File Structure
- Locale JSON files in `packages/views/locales/<locale>/<namespace>.json`.
- Two locales: `en` (English) and `zh-Hans` (Simplified Chinese).
- 20 namespaces: `common`, `auth`, `settings`, `issues`, `agents`, `editor`, `onboarding`, `invite`, `labels`, `members`, `my-issues`, `search`, `inbox`, `workspace`, `projects`, `autopilots`, `skills`, `chat`, `modals`, `runtimes`, `layout`.
- Resource registry in `packages/views/locales/index.ts` — single source of truth.

### Key Naming
- Three-level nesting: `feature.component.action` or `feature.section.label`.
- Examples: `issues.toolbar.batch_update_success`, `issues.detail.comment_form.placeholder`, `inbox.empty.title`.

### Parity Enforcement
- `packages/views/locales/parity.test.ts` enforces EN/zh-Hans key parity in CI.
- EN uses `_one` + `_other` for plurals; zh only uses `_other`.
- Platform-specific copy in `web` or `desktop` sub-sections within each namespace.

### Glossary
- Entities (issue, skill, task) stay as lowercase English in Chinese UI strings.
- Concepts translate fully: Workspace -> 工作区, Agent -> 智能体, Project -> 项目.
- Brands and acronyms never translated.
- Single space between English and Chinese text.
- See `apps/docs/content/docs/developers/conventions.mdx` for the complete glossary.

## Commit Conventions
- Conventional format: `feat(scope)`, `fix(scope)`, `refactor(scope)`, `docs`, `test(scope)`, `chore(scope)`.
- Atomic commits grouped by logical intent.
- Comments in code: English only.

## Package Boundary Rules

Strict dependency direction enforced by convention:

| Package | May depend on | Must NOT depend on |
|---|---|---|
| `packages/core` | nothing app-specific | `react-dom`, `localStorage`, `process.env`, `next/*`, UI libraries |
| `packages/ui` | nothing | `@multica/core`, business logic |
| `packages/views` | `core/`, `ui/` | `next/*`, `react-router-dom`, Zustand stores (import from core) |
| `apps/web/platform/` | `next/*` APIs | other apps |
| `apps/desktop/.../platform/` | `react-router-dom`, electron APIs | other apps |

Key rules:
- If the same logic exists in both apps, it must be extracted to a shared package.
- `packages/core/` has zero `react-dom` (use `StorageAdapter` instead of `localStorage`), zero `process.env`.
- `packages/ui/` imports nothing from `@multica/core`.
- `packages/views/` never imports from `next/*` or `react-router-dom`. Uses `NavigationAdapter` for all routing.
