# Testing

## Test Infrastructure

### TypeScript Testing (Vitest)
- **Runner**: Vitest (^4.1.0), configured per package.
- **packages/core/**: Node environment (no DOM), `vitest.config.ts` at package root.
- **packages/views/**: jsdom environment, `@vitejs/plugin-react`, setup file at `./test/setup.ts`.
- **apps/web/**: jsdom environment, framework-specific mocks (`next/navigation`).
- Test files colocated: `<file>.test.ts(x)` alongside the source file.

### Go Testing
- Standard `go test` with `testing` package.
- Tests colocated as `<file>_test.go` in the same package (white-box testing).
- Database-backed tests connect to a real PostgreSQL instance.

### E2E Testing (Playwright)
- Playwright (^1.58.2) configured at repo root in `playwright.config.ts`.
- Chromium only, headless mode.
- Tests must be self-contained with their own data setup/teardown.
- Servers must already be running (no auto-start).

### Test Dependencies (from pnpm catalog)
All test dependencies are version-pinned via `pnpm-workspace.yaml` catalog:
- `vitest: ^4.1.0`
- `jsdom: ^29.0.1`
- `@vitejs/plugin-react: ^6.0.1`
- `@testing-library/react: ^16.3.2`
- `@testing-library/jest-dom: ^6.9.1`
- `@testing-library/user-event: ^14.6.1`

## Unit Tests

### Location Patterns

Tests follow the code, not the app:

| What you are testing | Where the test lives | Environment |
|---|---|---|
| Shared business logic (stores, queries, hooks) | `packages/core/*.test.ts` | Node (no DOM) |
| Shared UI components (pages, forms, modals) | `packages/views/*.test.tsx` | jsdom |
| Platform-specific wiring (cookies, redirects) | `apps/web/*.test.tsx` | jsdom + framework mocks |
| End-to-end user flows | `e2e/*.spec.ts` | Real browser |

Never test shared component behavior in an app test file. If a test requires mocking `next/navigation` or `react-router-dom` to test a component from `@multica/views`, the test is in the wrong place.

### Core Package Tests
- Pure logic tests using Vitest with `globals: true`.
- No DOM, no React — just function-level testing.
- Common pattern: test utility functions, store behavior, cache helpers, WebSocket updaters.
- Example files: `utils.test.ts`, `auth/store.test.ts`, `api/client.test.ts`, `api/schema.test.ts`.

### Views Package Tests
- jsdom environment with React Testing Library.
- Setup file (`packages/views/test/setup.ts`) provides:
  - `@testing-library/jest-dom/vitest` matchers.
  - In-memory `localStorage` polyfill for jsdom.
  - `window.matchMedia` stub (for `useIsMobile()`).
  - `ResizeObserver` stub (for components like input-otp).
  - `document.elementFromPoint` stub.

### Mocking Conventions

**Zustand store mocking** (the established pattern):
```ts
const mockSendCode = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector?: (s: unknown) => unknown) => {
      const state = { sendCode: mockSendCode };
      return selector ? selector(state) : state;
    },
    {
      getState: () => ({ sendCode: mockSendCode }),
    },
  ),
}));
```

Key points:
- Use `vi.hoisted()` for mock function creation.
- Zustand stores are both callable and have `.getState()` — mock both.
- Use `Object.assign(selectorFn, { getState })` pattern.

**API mocking**:
```ts
const mockApiListWorkspaces = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/api", () => ({
  api: { listWorkspaces: mockApiListWorkspaces },
}));
```

**Framework mocking** (web app tests only):
```ts
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/login",
  useSearchParams: () => new URLSearchParams(),
}));
```

In `packages/views/` tests: never mock `next/*` or `react-router-dom`. In `apps/web/` tests: mock framework-specific APIs only.

**I18n test wrapper** (reusable across views tests):
```ts
const TEST_RESOURCES = {
  en: { common: enCommon, auth: enAuth, settings: enSettings },
};
function I18nWrapper({ children }) {
  return <I18nProvider locale="en" resources={TEST_RESOURCES}>{children}</I18nProvider>;
}
```

### API Schema Tests
- `packages/core/api/schema.test.ts` tests the `parseWithFallback` defense boundary.
- Tests cover: null body, wrong shape, unknown enum values, preserved unknown fields.
- Tests also exist per-endpoint in `client.test.ts` using `vi.stubGlobal("fetch", ...)`.

### Locale Parity Tests
- `packages/views/locales/parity.test.ts` enforces key parity between EN and zh-Hans.
- Checks every namespace has matching keys (accounting for plural rules).
- Also verifies all JSON files on disk are registered in the `RESOURCES` index.

## Integration Tests

### Go Integration Tests
- Go tests connect to a real PostgreSQL database (not mocked).
- `TestMain` in `server/internal/handler/handler_test.go` sets up:
  1. Database connection via `DATABASE_URL` (or default `localhost`).
  2. Test fixtures: user, workspace, runtime, agent.
  3. Real `Handler`, `Hub`, `Bus` instances.
  4. Cleanup runs after all tests.
- Tests use `httptest.NewRecorder()` and `httptest.NewRequest()` for HTTP handler testing.
- `newRequest()` helper sets standard headers (`X-User-ID`, `X-Workspace-ID`, `Content-Type`).
- `withURLParam()` helper injects chi URL parameters for route testing.

### Event Bus Integration
- Tests subscribe to `events.Bus` to capture published events.
- Pattern: `testHandler.Bus.Subscribe(eventType, handler)`.
- Used to verify WS event payloads (e.g., `issue:deleted` carries UUID, not identifier).

### Server-Level Integration Tests
- `server/cmd/server/` contains integration tests for:
  - Notification listeners
  - Activity listeners
  - Subscriber listeners
  - Autopilot listeners
  - Runtime sweeper
  - Health checks
  - Scope authorization

## E2E Tests

### Playwright Setup
- Configuration in `playwright.config.ts` at repo root.
- `testDir: "./e2e"`, timeout 30s, 0 retries.
- Base URL from `PLAYWRIGHT_BASE_URL` or `FRONTEND_ORIGIN` env (default `http://localhost:3300`).
- Servers must be running before tests execute.

### Test Fixtures (`e2e/fixtures.ts`)
- `TestApiClient` class provides API helper for data setup/teardown:
  - `login(email, name)` — authenticates via send-code -> DB read -> verify-code flow.
  - `ensureWorkspace(name, slug)` — creates or finds workspace.
  - `createIssue(title, opts)` — creates issue and tracks for cleanup.
  - `cleanup()` — deletes all tracked issues.
  - `dismissStarterContent()` — dismisses onboarding starter content.
- Uses raw `fetch` (zero build-time coupling to the web app).
- Tracks created issues for automatic cleanup.

### Test Helpers (`e2e/helpers.ts`)
- `loginAsDefault(page, testInfo)` — logs in as E2E user, ensures workspace, injects token into localStorage.
- `createTestApi(testInfo)` — creates authenticated `TestApiClient`.
- Test identity is per-worker/retry: `e2e-{workerIndex}-{repeatEachIndex}-{retry}@multica.ai`.
- Workspace slugs: `e2e-{workerIndex}-{repeatEachIndex}-{retry}-workspace`.

### Test Structure
```ts
test.describe("Feature", () => {
  let api: TestApiClient;
  test.beforeEach(async ({ page }, testInfo) => {
    api = await createTestApi(testInfo);
    await loginAsDefault(page, testInfo);
  });
  test.afterEach(async () => {
    if (api) await api.cleanup();
  });
  test("does something", async ({ page }) => {
    // ... test body
  });
});
```

### E2E Test Files
- `e2e/auth.spec.ts` — Authentication flows
- `e2e/issues.spec.ts` — Issue CRUD, views, navigation
- `e2e/comments.spec.ts` — Comment creation and display
- `e2e/navigation.spec.ts` — Page navigation and routing
- `e2e/settings.spec.ts` — Settings page interactions

## Go Tests

### Standard Patterns
- `t.Helper()` for test helper functions.
- `t.Cleanup(func())` for per-test cleanup.
- `t.Fatalf()` for unrecoverable assertion failures.
- `t.Fatalf()` includes expected/got values in error messages.
- Table-driven tests using `[]struct{ name string; ... }` with `t.Run()`.
- `t.Setenv()` for environment variable manipulation (restored automatically).

### Test Fixtures
- `TestMain` creates shared fixtures (user, workspace, agent, runtime) used across all tests.
- Per-test data created within test functions, cleaned up via `defer` or `t.Cleanup()`.
- Helper functions: `createHandlerTestAgent(t, name, mcpConfig)`, `handlerTestRuntimeID(t)`.
- `assertJSONEqual(t, got, want)` for normalized JSON comparison.

### Handler Test Pattern
```go
func TestFeature(t *testing.T) {
    // Setup
    w := httptest.NewRecorder()
    req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
        "title": "Test issue",
    })
    testHandler.CreateIssue(w, req)
    if w.Code != http.StatusCreated {
        t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
    }

    // Decode and verify
    var created IssueResponse
    json.NewDecoder(w.Body).Decode(&created)
    // assertions...
}
```

### UUID Validation Guard Tests
Extensive tests verify that malformed UUIDs are rejected at every boundary:
- `TestDeleteIssueRejectsInvalidUUID`
- `TestCreateIssueRejectsMalformedAssigneeID`
- `TestUpdateAgentRejectsMalformedAgentID`
- `TestRequestBodyUUIDFieldsRejectMalformed` (table-driven)
- And many more — search for `RejectsMalformed` or `RejectsInvalidUUID`.

## Test Running Commands

### TypeScript Tests
```bash
pnpm test                  # All TS tests (via Turborepo, all packages)
pnpm typecheck             # TypeScript type check (all packages)

# Single test in a package
pnpm --filter @multica/views exec vitest run auth/login-page.test.tsx
pnpm --filter @multica/core exec vitest run runtimes/version.test.ts
pnpm --filter @multica/web exec vitest run app/\(auth\)/login/page.test.tsx
```

### Go Tests
```bash
make test                  # Run Go tests (ensures DB exists + migrated)
cd server && go test ./... # Run all Go tests directly
cd server && go test ./internal/handler/ -run TestName  # Single test
```

### E2E Tests
```bash
pnpm exec playwright test                          # All E2E tests
pnpm exec playwright test e2e/issues.spec.ts       # Single file
```
Requires backend + frontend running already.

### Full Verification
```bash
make check                 # Runs typecheck + TS tests + Go tests + Playwright E2E
```
Uses `scripts/check.sh` which runs the full pipeline.

## CI Pipeline

### GitHub Actions (`ci.yml`)
Runs on push to `main` and PRs to `main`. Concurrency: cancel in-progress for same PR.

**Frontend job** (ubuntu-latest):
1. Checkout
2. Setup pnpm
3. Setup Node.js 22
4. `pnpm install`
5. Verify `reserved-slugs.ts` is up to date (re-runs generator, fails on drift)
6. `pnpm exec turbo build typecheck lint test --filter='!@multica/docs'`

**Backend job** (ubuntu-latest):
- Services: `pgvector/pgvector:pg17` (PostgreSQL) + `redis:7-alpine`
- Environment: `DATABASE_URL`, `REDIS_TEST_URL`
- Steps:
  1. Checkout
  2. Setup Go 1.26.1
  3. `cd server && go build ./...`
  4. `cd server && go run ./cmd/migrate up`
  5. `cd server && go test ./...`

### Release Pipeline (`release.yml`)
Triggered by version tags (`v*.*.*`, excluding `-dirty`).

Jobs:
1. **verify** — validates tag format, runs Go tests.
2. **release** — GoReleaser builds multi-platform binaries, publishes to GitHub Releases + Homebrew tap.
3. **docker-backend-build/merge** — multi-arch Docker images (amd64 + arm64) for backend.
4. **docker-web-build/merge** — multi-arch Docker images for web frontend.
5. **desktop** — Linux and Windows desktop installers (macOS ships separately).

### Desktop Smoke Test (`desktop-smoke.yml`)
Smoke tests for the desktop app build.

## Coverage

There is no explicit coverage threshold or coverage reporting tool configured in the project. The project relies on:
- Convention: tests colocated with source, following the code not the app.
- CI enforcement: both frontend and backend tests must pass in CI.
- Schema guard tests: malformed API responses are explicitly tested.
- Locale parity tests: translation key coverage enforced in CI.
- Reserved slug sync: generator drift caught in CI.
