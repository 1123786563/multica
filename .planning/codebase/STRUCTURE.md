# Directory Structure

## Root Layout

```
multica/
├── .github/                  -- GitHub Actions CI/CD workflows
│   └── workflows/
│       ├── ci.yml            -- Full CI: Go tests, TS typecheck, unit tests, E2E
│       ├── release.yml       -- GoReleaser multi-platform binary builds
│       └── desktop-smoke.yml -- Electron desktop smoke tests
├── apps/
│   ├── web/                  -- Next.js web app (App Router, port 3300)
│   └── desktop/              -- Electron desktop app (electron-vite)
├── packages/
│   ├── core/                 -- Headless business logic (no react-dom)
│   ├── ui/                   -- Atomic UI components (no business logic)
│   ├── views/                -- Shared business pages/components
│   ├── tsconfig/             -- Shared TypeScript configuration
│   └── eslint-config/        -- Shared ESLint configurations
├── server/                   -- Go backend (Chi router, sqlc, WebSocket)
├── e2e/                      -- Playwright end-to-end tests
├── scripts/                  -- Dev/CI utility scripts
├── docs/                     -- Internal documentation (design, product)
├── docker/                   -- Docker support files
├── pnpm-workspace.yaml       -- Workspace config with catalog pinning
├── turbo.json                -- Turborepo pipeline configuration
├── Makefile                  -- Primary developer entry point (make dev)
├── package.json              -- Root scripts (dev:web, build, typecheck)
├── pnpm-lock.yaml            -- Lockfile (525KB)
├── playwright.config.ts      -- Playwright E2E configuration
├── Dockerfile                -- Multi-stage Docker build for backend
├── Dockerfile.web            -- Frontend-only Docker build
├── docker-compose.yml        -- PostgreSQL service
├── .goreleaser.yml           -- GoReleaser config for CLI distribution
├── CLAUDE.md                 -- AI agent instructions
├── AGENTS.md                 -- Agent-specific instructions
└── README.md                 -- Project overview
```

## server/ (Go Backend)

215 Go source files, 134 test files.

```
server/
├── cmd/
│   ├── server/               -- HTTP server entry point
│   │   ├── main.go           -- Boot: DB pool, Redis, event bus, Hub, router, background workers
│   │   ├── router.go         -- Chi router: middleware stack, all route definitions
│   │   ├── listeners.go      -- Event bus → WS broadcast wiring
│   │   ├── scope_authorizer.go -- WS scope subscription authorization
│   │   ├── subscribers.go    -- Subscriber event listeners
│   │   ├── autopilot_listeners.go   -- Autopilot event handlers
│   │   ├── autopilot_scheduler.go   -- Cron-based autopilot trigger execution
│   │   ├── autopilot_failure_monitor.go -- Stale autopilot run detection
│   │   ├── notification_listeners.go -- WS → email notification dispatch
│   │   ├── activity_listeners.go    -- Activity feed event handlers
│   │   ├── runtime_sweeper.go       -- Marks stale runtimes offline
│   │   ├── health.go                -- Health/readiness check handlers
│   │   ├── health_realtime.go       -- Real-time subsystem metrics
│   │   └── dbstats.go              -- Periodic DB connection pool logging
│   ├── multica/              -- CLI binary (cobra-based)
│   │   ├── main.go           -- Root command
│   │   ├── cmd_auth.go       -- Login/logout
│   │   ├── cmd_daemon.go     -- Daemon start/stop/status
│   │   ├── cmd_issue.go      -- Issue CRUD from CLI
│   │   ├── cmd_agent.go      -- Agent management
│   │   ├── cmd_task.go       -- Task operations (new)
│   │   ├── cmd_workspace.go  -- Workspace management
│   │   ├── cmd_runtime.go    -- Runtime inspection
│   │   ├── cmd_skill.go      -- Skill management
│   │   ├── cmd_autopilot.go  -- Autopilot management
│   │   ├── cmd_project.go    -- Project management
│   │   ├── cmd_setup.go      -- First-time setup wizard
│   │   └── ...               -- More CLI subcommands
│   └── migrate/              -- Database migration runner
├── internal/
│   ├── handler/              -- HTTP handlers (50+ files)
│   │   ├── handler.go        -- Handler struct, UUID parsing helpers, loader functions
│   │   ├── issue.go          -- Issue CRUD, batch operations, search
│   │   ├── agent.go          -- Agent CRUD, archive/restore, task listing
│   │   ├── runtime.go        -- Runtime listing, usage, activity
│   │   ├── daemon.go         -- Daemon register/deregister/heartbeat
│   │   ├── daemon_ws.go      -- Daemon WebSocket handler
│   │   ├── auth.go           -- Send-code, verify-code, Google login
│   │   ├── chat.go           -- Chat sessions, messages
│   │   ├── autopilot.go      -- Autopilot CRUD, trigger, run
│   │   ├── skill.go          -- Skill CRUD, import
│   │   ├── workspace.go      -- Workspace CRUD, membership
│   │   ├── orchestration.go  -- Orchestration plan/run endpoints (new)
│   │   ├── task_lifecycle.go -- Task claim/start/complete/fail/progress
│   │   ├── comment.go        -- Comment CRUD, resolve/unresolve
│   │   ├── inbox.go          -- Inbox list, read, archive
│   │   ├── project.go        -- Project CRUD
│   │   ├── project_resource.go -- Project resource management
│   │   ├── label.go          -- Label CRUD
│   │   ├── pin.go            -- Pin CRUD, reorder
│   │   ├── config.go         -- Public configuration endpoint
│   │   ├── file.go           -- File upload (S3/local)
│   │   ├── feedback.go       -- User feedback
│   │   ├── search.go         -- Issue search
│   │   ├── subscriber.go     -- Issue subscriber management
│   │   ├── invitation.go     -- Invitation create/accept/decline
│   │   ├── reaction.go       -- Comment reactions
│   │   ├── issue_reaction.go -- Issue-level reactions
│   │   ├── config.go         -- Runtime config (signup, rollup)
│   │   ├── heartbeat_scheduler.go -- Batched heartbeat processing
│   │   ├── runtime_*.go      -- Runtime models/skills/update stores (Redis + in-memory)
│   │   └── reserved_slugs.json -- Reserved URL slugs
│   ├── service/              -- Business logic services
│   │   ├── task.go           -- TaskService: enqueue, claim, start, complete, fail, retry
│   │   ├── autopilot.go      -- AutopilotService: schedule, run, failure handling
│   │   ├── orchestrator.go   -- Orchestrator: plan/node/run lifecycle (new)
│   │   ├── email.go          -- EmailService: verification codes via Resend
│   │   ├── cron.go           -- Cron scheduling helpers
│   │   └── empty_claim_cache.go -- Redis cache for "no queued tasks" fast path
│   ├── realtime/             -- WebSocket hub and event broadcasting
│   │   ├── hub.go            -- Scope-based room Hub, connection lifecycle
│   │   ├── broadcaster.go    -- Broadcaster interface, scope constants
│   │   ├── metrics.go        -- Connection/message counters
│   │   ├── redis_relay.go    -- Redis Streams relay (legacy, single-stream)
│   │   ├── sharded_stream_relay.go -- Sharded Redis Streams relay
│   │   └── relay_lifecycle.go -- DualWriteBroadcaster, MirroredRelay
│   ├── daemonws/             -- Daemon WebSocket hub (separate from browser hub)
│   │   ├── hub.go            -- Daemon connection management
│   │   ├── notifier.go       -- Task wakeup via Redis relay
│   │   └── metrics.go        -- Daemon WS metrics
│   ├── daemon/               -- Daemon client logic (runs locally)
│   │   ├── daemon.go         -- Daemon struct, workspace management, task polling
│   │   ├── client.go         -- HTTP client for server API
│   │   ├── prompt.go         -- Agent prompt construction
│   │   ├── config.go         -- Daemon configuration
│   │   ├── types.go          -- Task, Runtime, Agent data types
│   │   ├── health.go         -- Runtime health monitoring
│   │   ├── gc.go             -- Orphaned worktree cleanup
│   │   ├── identity.go       -- Runtime identity management
│   │   ├── wakeup.go         -- Task wakeup handling
│   │   ├── local_skills.go   -- Local skill detection/reporting
│   │   ├── poisoned.go       -- Poisoned task detection
│   │   ├── diskusage.go      -- Disk usage monitoring
│   │   ├── update_report.go  -- Self-update flow
│   │   ├── helpers.go        -- Shared test/helpers
│   │   ├── execenv/          -- Execution environment setup
│   │   └── repocache/        -- Git repo/worktree cache
│   ├── middleware/            -- HTTP middleware
│   │   ├── auth.go           -- JWT + PAT authentication
│   │   ├── daemon_auth.go    -- Daemon token authentication
│   │   ├── workspace.go      -- Workspace resolution + membership check
│   │   ├── client.go         -- X-Client-* header extraction
│   │   ├── cloudfront.go     -- CloudFront cookie refresh
│   │   ├── csp.go            -- Content-Security-Policy
│   │   └── request_logger.go -- Structured request logging
│   ├── auth/                 -- Authentication utilities
│   │   ├── jwt.go            -- JWT signing/verification, CSRF
│   │   ├── cookie.go         -- Cookie management
│   │   ├── pat_cache.go      -- PAT Redis cache
│   │   ├── daemon_token_cache.go -- Daemon token Redis cache
│   │   └── cloudfront.go     -- CloudFront signed URLs
│   ├── events/               -- In-process event bus
│   │   └── bus.go            -- Synchronous pub/sub with panic recovery
│   ├── analytics/            -- Analytics client (PostHog)
│   ├── metrics/              -- HTTP metrics (Prometheus-compatible)
│   ├── logger/               -- Structured logging setup (slog)
│   ├── storage/              -- File storage (S3 or local filesystem)
│   ├── mention/              -- @mention parsing and notification
│   ├── cli/                  -- CLI utilities (Homebrew detection)
│   ├── util/                 -- UUID parsing, type conversion helpers
│   └── migrations/           -- Go migration helpers
├── pkg/
│   ├── db/
│   │   ├── queries/          -- Hand-written SQL (30 domain files)
│   │   │   ├── issue.sql     -- Issue queries (CRUD, search, batch)
│   │   │   ├── agent.sql     -- Agent queries
│   │   │   ├── task_usage.sql -- Usage tracking
│   │   │   ├── orchestration.sql -- Orchestration plan/node/run (new)
│   │   │   └── ...
│   │   └── generated/        -- sqlc-generated Go code
│   │       ├── models.go     -- Go struct definitions for all DB tables
│   │       ├── db.go         -- DB connection interface
│   │       └── *.sql.go      -- ~30 generated query files
│   ├── protocol/             -- Shared event type constants
│   │   └── events.go         -- 50+ event type constants
│   ├── agent/                -- Agent CLI interaction logic
│   └── redact/               -- Sensitive data redaction
├── migrations/               -- PostgreSQL migrations (101 pairs, 001-081+)
├── bin/                      -- Built binaries (server, CLI)
└── data/                     -- Runtime data directory
```

## apps/web/ (Next.js Web App)

App Router-based Next.js app. Minimal platform-specific code; all business UI comes from shared packages.

```
apps/web/
├── app/                      -- Next.js App Router pages
│   ├── layout.tsx            -- Root layout: CoreProvider, WebNavigationProvider
│   ├── robots.ts             -- robots.txt generation
│   ├── sitemap.ts            -- sitemap.xml generation
│   ├── not-found.tsx         -- 404 page
│   ├── auth/callback/        -- Auth callback handler
│   ├── (landing)/            -- Public marketing pages
│   │   ├── page.tsx          -- Homepage
│   │   ├── layout.tsx        -- Landing layout (no sidebar)
│   │   ├── about/            -- About page
│   │   ├── download/         -- Desktop download page
│   │   └── changelog/        -- Changelog page
│   ├── (auth)/               -- Authentication pages
│   │   ├── login/            -- Login page
│   │   ├── workspaces/new/   -- Create workspace
│   │   ├── onboarding/       -- Onboarding flow
│   │   ├── invite/[id]/      -- Accept invitation
│   │   └── invitations/      -- List invitations
│   └── [workspaceSlug]/      -- Workspace-scoped pages
│       ├── layout.tsx        -- Workspace resolver + CoreProvider
│       └── (dashboard)/      -- Dashboard pages (sidebar layout)
│           ├── layout.tsx    -- DashboardLayout + ChatWindow + SearchCommand
│           ├── issues/       -- Issue list and detail
│           ├── my-issues/    -- Personal issues view
│           ├── agents/       -- Agent list and detail
│           ├── runtimes/     -- Runtime list and detail
│           ├── projects/     -- Project list and detail
│           ├── autopilots/   -- Autopilot list and detail
│           ├── skills/       -- Skill list and detail
│           ├── inbox/        -- Inbox
│           └── settings/     -- Workspace settings
├── features/
│   ├── landing/              -- Landing page components + i18n
│   └── auth/                 -- Auth-specific feature code
├── platform/
│   └── navigation.tsx        -- WebNavigationProvider (wraps Next.js router)
├── test/
│   ├── setup.ts              -- Vitest setup
│   └── helpers.tsx           -- Test utilities
└── public/
    └── images/               -- Static assets
```

## apps/desktop/ (Electron Desktop App)

Built with electron-vite. Three process layers.

```
apps/desktop/
├── src/
│   ├── main/                 -- Electron main process (Node.js)
│   │   ├── index.ts          -- Window creation, IPC setup
│   │   ├── updater.ts        -- Auto-update logic
│   │   ├── cli-bootstrap.ts  -- CLI binary management
│   │   ├── runtime-config-loader.ts -- App URL resolution
│   │   ├── cli-release-asset.ts -- GitHub release asset handling
│   │   ├── app-version.ts    -- Version management
│   │   └── external-url.ts   -- External URL opening
│   ├── preload/              -- Preload scripts (IPC bridge)
│   │   └── index.ts          -- desktopAPI, auto-update, notification bridge
│   ├── renderer/             -- Renderer process (React)
│   │   └── src/
│   │       ├── App.tsx       -- Root app with workspace route setup
│   │       ├── main.tsx      -- React entry point
│   │       ├── platform/
│   │       │   ├── navigation.tsx    -- DesktopNavigationProvider + TabNavigationProvider
│   │       │   ├── daemon-ipc-bridge.ts -- Daemon IPC communication
│   │       │   └── i18n-adapter.ts   -- Desktop i18n adapter
│   │       ├── stores/
│   │       │   ├── tab-store.ts      -- Tab management (per-workspace tab groups)
│   │       │   └── window-overlay-store.ts -- Overlay state (new-workspace, invite, etc.)
│   │       ├── components/
│   │       │   ├── tab-bar.tsx       -- Multi-tab UI
│   │       │   ├── tab-content.tsx   -- Per-tab content wrapper
│   │       │   ├── window-overlay.tsx -- Modal overlay for transitions
│   │       │   ├── daemon-panel.tsx  -- Daemon status panel
│   │       │   ├── daemon-runtime-card.tsx -- Runtime card
│   │       │   ├── daemon-settings-tab.tsx -- Daemon settings
│   │       │   ├── desktop-runtimes-page.tsx -- Desktop runtimes view
│   │       │   ├── update-notification.tsx -- Update banner
│   │       │   └── pageview-tracker.tsx -- Page view analytics
│   │       ├── hooks/
│   │       │   ├── use-tab-history.ts -- Tab navigation history
│   │       │   ├── use-tab-sync.ts    -- Tab state synchronization
│   │       │   └── use-document-title.ts -- Document title management
│   │       └── pages/         -- Desktop-specific page components
│   │           ├── login.tsx
│   │           ├── runtime-detail-page.tsx
│   │           ├── issue-detail-page.tsx
│   │           ├── agent-detail-page.tsx
│   │           └── project-detail-page.tsx
│   └── shared/               -- Shared types between main/renderer
├── resources/
│   └── bin/                  -- Bundled daemon binary
├── build/                    -- Electron-builder config
├── scripts/                  -- Build scripts
├── test/                     -- Desktop tests
└── out/                      -- Build output
```

## packages/core/ (Headless Business Logic)

Zero react-dom, zero localStorage, zero process.env dependencies. All shared Zustand stores and API logic lives here.

```
packages/core/
├── api/                      -- API client layer
│   ├── client.ts             -- ApiClient: typed HTTP methods for all endpoints (~60 methods)
│   ├── ws-client.ts          -- WSClient: WebSocket with auto-reconnect, event subscription
│   ├── schema.ts             -- parseWithFallback: zod validation + graceful fallback
│   └── schemas.ts            -- Zod schemas for API response validation
├── platform/                 -- Cross-platform initialization
│   ├── core-provider.tsx     -- CoreProvider: ApiClient, stores, WS, QueryClient
│   ├── auth-initializer.tsx  -- Session recovery on boot
│   ├── workspace-storage.ts  -- setCurrentWorkspace singleton (URL-driven)
│   ├── storage.ts            -- StorageAdapter interface + default (localStorage)
│   ├── persist-storage.ts    -- Zustand persist storage adapter
│   ├── storage-cleanup.ts    -- Workspace-scoped storage cleanup
│   └── keyboard.ts           -- Cross-platform keyboard shortcuts
├── realtime/                 -- WebSocket real-time layer
│   ├── provider.tsx          -- WSProvider: connection lifecycle, workspace switching
│   ├── hooks.ts              -- useWSEvent, useWSReconnect
│   └── use-realtime-sync.ts  -- Central WS → Query cache sync (40+ event handlers)
├── auth/                     -- Authentication
│   ├── index.ts              -- Auth store (createAuthStore factory)
│   └── store.ts              -- Zustand store: user, session, login/logout
├── issues/                   -- Issues domain
│   ├── queries.ts            -- TanStack Query keys + fetchers
│   ├── mutations.ts          -- TanStack Query mutations (optimistic)
│   ├── ws-updaters.ts        -- WS event → cache update helpers
│   ├── cache-helpers.ts      -- Query cache manipulation utilities
│   ├── index.ts              -- Public exports
│   ├── config/               -- Issue configuration (statuses, priorities, etc.)
│   └── stores/               -- Client-only Zustand stores
│       ├── view-store.ts     -- List/board view mode, sort, filters
│       ├── draft-store.ts    -- Comment/description drafts (persisted)
│       ├── selection-store.ts -- Multi-select state
│       ├── comment-collapse-store.ts -- Thread collapse state
│       ├── create-mode-store.ts -- Issue creation mode state
│       ├── quick-create-store.ts -- Quick create dialog state
│       ├── recent-issues-store.ts -- Recently viewed issues
│       ├── my-issues-view-store.ts -- My Issues tab/view preferences
│       ├── issues-scope-store.ts -- Issue scope for keyboard navigation
│       └── view-store-context.tsx -- View store provider
├── agents/                   -- Agents domain
│   ├── queries.ts            -- Agent queries + presence derivation
│   ├── mutations.ts          -- Agent CRUD mutations
│   └── index.ts
├── runtimes/                 -- Runtimes domain
│   ├── queries.ts            -- Runtime queries, usage, activity
│   └── index.ts
├── chat/                     -- Chat domain
│   ├── queries.ts            -- Chat session/message queries
│   ├── mutations.ts          -- Chat mutations
│   ├── index.ts              -- Chat store factory
│   └── store.ts              -- Zustand store: active session
├── projects/                 -- Projects domain
├── autopilots/               -- Autopilots domain
├── skills/                   -- Skills domain
├── pins/                     -- Pins (sidebar pinned items)
├── labels/                   -- Labels domain
├── inbox/                    -- Inbox domain
├── workspace/                -- Workspace domain
├── permissions/              -- Permission utilities
├── modals/                   -- Modal state management
├── hooks/                    -- Shared React hooks
├── navigation/               -- Navigation store (cross-platform)
├── paths/                    -- Route path definitions + reserved slugs
├── notification-preferences/ -- Notification preferences queries
├── onboarding/               -- Onboarding state
├── analytics/                -- Analytics (PostHog) client
├── config/                   -- Feature flags, config
├── constants/                -- Shared constants
├── feedback/                 -- Feedback submission
├── i18n/                     -- i18n setup (i18next)
│   └── react/                -- React i18n bindings
├── types/                    -- Shared TypeScript types
│   ├── agent.ts              -- Agent/Task/Runtime type definitions
│   ├── events.ts             -- WS event type definitions
│   ├── storage.ts            -- StorageAdapter type
│   └── ...
├── provider.tsx              -- QueryProvider (TanStack Query client setup)
└── logger.ts                 -- Logger utility
```

## packages/ui/ (Atomic UI Components)

Zero `@multica/core` imports. Pure presentational components built on Base UI primitives.

```
packages/ui/
├── components/
│   ├── ui/                   -- 58 shadcn components (Base UI variant)
│   │   ├── button.tsx
│   │   ├── dialog.tsx
│   │   ├── dropdown-menu.tsx
│   │   ├── data-table.tsx
│   │   ├── command.tsx       -- Command palette (cmd+k)
│   │   ├── combobox.tsx
│   │   ├── editor.tsx        -- Rich text editor (Tiptap)
│   │   ├── toast.tsx
│   │   └── ...               -- 50+ more components
│   └── common/               -- 12 domain-adjacent shared components
│       ├── actor-avatar.tsx  -- Member/agent avatar with distinct styling
│       ├── multica-icon.tsx  -- App icon
│       ├── emoji-picker.tsx
│       ├── reaction-bar.tsx
│       ├── mention-hover-card.tsx
│       ├── error-boundary.tsx
│       ├── capability-banner.tsx
│       ├── file-upload-button.tsx
│       ├── submit-button.tsx
│       ├── theme-provider.tsx
│       ├── unicode-spinner.tsx
│       └── quick-emoji-picker.tsx
├── hooks/                    -- 3 shared hooks
│   ├── use-auto-scroll.ts
│   ├── use-mobile.ts
│   └── use-scroll-fade.ts
├── markdown/                 -- Markdown rendering utilities
├── styles/                   -- Shared CSS (design tokens, scrollbar, keyframes)
│   └── globals.css
├── lib/                      -- Utility functions (cn, etc.)
└── components.json           -- shadcn config (Base UI variant)
```

## packages/views/ (Shared Business Pages)

Zero `next/*`, zero `react-router-dom`, zero Zustand stores. Uses `NavigationAdapter` for routing. Contains all shared page components and domain-specific components.

```
packages/views/
├── layout/                   -- App shell layout
│   ├── dashboard-layout.tsx  -- Main layout: sidebar + content area
│   ├── app-sidebar.tsx       -- Workspace sidebar (navigation, workspace switcher)
│   ├── dashboard-guard.tsx   -- Workspace membership guard
│   ├── workspace-loader.tsx  -- Workspace data preloader
│   ├── workspace-presence-prefetch.tsx -- Agent presence prefetch
│   ├── page-header.tsx       -- Page title + actions header
│   └── help-launcher.tsx     -- Help/feedback launcher
├── navigation/               -- Navigation adapter (platform-agnostic)
│   ├── context.tsx           -- NavigationProvider + useNavigation
│   ├── app-link.tsx          -- Cross-platform Link component
│   └── types.ts              -- NavigationAdapter interface
├── issues/                   -- Issues pages
│   ├── components/           -- Issue-specific components
│   │   ├── issue-list.tsx
│   │   ├── issue-detail.tsx
│   │   ├── comment-input.tsx
│   │   ├── execution-log-section.tsx
│   │   └── ...
│   ├── hooks/                -- Issue-specific hooks
│   ├── actions/              -- Issue action handlers
│   └── utils/                -- Issue utilities
├── agents/                   -- Agent pages
│   └── components/
│       ├── tabs/             -- Agent detail tabs (overview, activity, skills)
│       └── inspector/        -- Agent configuration inspector
├── chat/                     -- Chat interface
│   ├── components/
│   │   ├── chat-window.tsx   -- Slide-over chat panel
│   │   ├── chat-fab.tsx      -- Chat FAB trigger
│   │   └── ...
│   └── lib/
├── runtimes/                 -- Runtime pages
│   └── components/
│       └── charts/           -- Usage/activity charts
├── autopilots/               -- Autopilot pages
│   └── components/
│       └── pickers/          -- Autopilot trigger pickers
├── projects/                 -- Project pages
│   └── components/
├── skills/                   -- Skill pages
│   ├── components/
│   ├── hooks/
│   └── lib/
├── settings/                 -- Settings pages
│   └── components/
│       └── workspace-tab.tsx
├── inbox/                    -- Inbox pages
│   └── components/
├── labels/                   -- Label components
│   └── label-chip.tsx
├── workspace/                -- Workspace components
│   ├── create-workspace-form.tsx
│   ├── new-workspace-page.tsx
│   ├── workspace-avatar.tsx
│   └── no-access-page.tsx
├── my-issues/                -- My Issues view
│   └── components/
├── modals/                   -- Shared modal components
│   ├── registry.tsx          -- Modal registration system
│   ├── create-issue.tsx
│   ├── create-issue-dialog.tsx
│   ├── issue-picker-modal.tsx
│   ├── create-project.tsx
│   ├── create-workspace.tsx
│   ├── set-parent-issue.tsx
│   ├── add-child-issue.tsx
│   ├── delete-issue-confirm.tsx
│   ├── feedback.tsx
│   └── backlog-agent-hint.tsx
├── search/                   -- Global search (cmd+k)
├── invite/                   -- Invitation acceptance page
├── invitations/              -- Invitations list
├── members/                  -- Member management
├── auth/                     -- Shared auth pages
│   └── login-page.tsx
├── onboarding/               -- Onboarding components
├── common/                   -- Shared view utilities
│   └── task-transcript/      -- Task transcript display
├── platform/                 -- Platform-specific view utilities
│   ├── drag-strip.tsx        -- macOS drag region
│   ├── use-immersive-mode.ts
│   ├── use-desktop-unread-badge.ts
│   └── open-external.ts
├── editor/                   -- Shared editor components
├── i18n/                     -- View-level i18n utilities
└── locales/                  -- Translation files
    ├── en/                   -- English translations
    │   └── issues.json
    └── zh-Hans/              -- Simplified Chinese translations
        └── issues.json
```

## e2e/ (End-to-End Tests)

Playwright tests requiring running backend + frontend.

```
e2e/
├── fixtures.ts               -- TestApiClient fixture for data setup/teardown
├── helpers.ts                -- Login, API client creation, workspace helpers
├── env.ts                    -- Environment configuration
├── auth.spec.ts              -- Authentication flow tests
├── issues.spec.ts            -- Issue CRUD and detail page tests
├── comments.spec.ts          -- Comment creation and resolution tests
├── navigation.spec.ts        -- Navigation and routing tests
└── settings.spec.ts          -- Workspace settings tests
```

## Key Configuration Files

| File | Purpose |
|------|---------|
| `pnpm-workspace.yaml` | Workspace packages + catalog version pinning |
| `turbo.json` | Turborepo task pipeline definitions |
| `package.json` | Root scripts, dev dependencies, package manager version |
| `Makefile` | Dev entry points: `make dev`, `make setup`, `make check` |
| `.env.example` | All environment variables with documentation |
| `server/go.mod` | Go module definition |
| `server/sqlc.yaml` | sqlc code generation config |
| `apps/web/next.config.ts` | Next.js configuration |
| `apps/desktop/electron.vite.config.ts` | electron-vite configuration |
| `apps/desktop/electron-builder.yml` | Electron packaging config |
| `packages/ui/components.json` | shadcn component generator config |
| `packages/tsconfig/base.json` | Shared TS config for all packages |
| `packages/tsconfig/react-library.json` | React-specific TS config |
| `playwright.config.ts` | Playwright E2E test config |
| `.goreleaser.yml` | Multi-platform CLI binary release config |
| `docker-compose.yml` | PostgreSQL service definition |
| `server/internal/handler/reserved_slugs.json` | Reserved URL slugs (code-generated to TS) |
| `scripts/generate-reserved-slugs.mjs` | Reserved slugs generator (JSON → TS) |

### File counts by area

| Area | Source files | Test files |
|------|-------------|------------|
| Go server | 215 | 134 |
| TS packages (core, ui, views) | 496 | ~100+ |
| Desktop app | 51 | ~8 |
| Web app | ~40 | ~5 |
| E2E tests | 5 specs | -- |
| Migrations | 101 pairs | -- |
