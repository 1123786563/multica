# Temporal Orchestration MVP Checkout

This checkout proves the Temporal-backed orchestration MVP cut line across Temporal, Eino-style activities, daemon-backed Agent Task execution, projection, API, and Issue Detail. Run it from the repository root unless a command says `cd server`.

## Local Setup

Start Temporal explicitly. `make dev` does not start Temporal and should still fail closed when Temporal is absent.

```bash
temporal server start-dev --ip 127.0.0.1 --port 7233
```

Use the same Temporal profile for the API and worker:

```bash
export TEMPORAL_HOST_PORT=127.0.0.1:7233
export TEMPORAL_NAMESPACE=default
export TEMPORAL_TASK_QUEUE=multica-orchestration
export ORCHESTRATION_EINO_PROVIDER=openai-compatible
export ORCHESTRATION_EINO_API_KEY=replace-me
export ORCHESTRATION_EINO_MODEL=replace-me
# Optional for OpenAI-compatible gateways; leave empty for provider default.
export ORCHESTRATION_EINO_BASE_URL=
export ORCHESTRATION_EINO_TIMEOUT=60s
```

Start the Multica processes in separate terminals:

```bash
TEMPORAL_HOST_PORT=127.0.0.1:7233 TEMPORAL_NAMESPACE=default TEMPORAL_TASK_QUEUE=multica-orchestration make server
TEMPORAL_HOST_PORT=127.0.0.1:7233 TEMPORAL_NAMESPACE=default TEMPORAL_TASK_QUEUE=multica-orchestration ORCHESTRATION_EINO_PROVIDER=openai-compatible ORCHESTRATION_EINO_API_KEY=replace-me ORCHESTRATION_EINO_MODEL=replace-me make orchestration-worker
make daemon
pnpm dev:web
```

The worker command registers `IssueWorkflow` plus the fixed activity chain: load issue, analyze issue, dispatch daemon task, validate outcome, review outcome, summarize outcome, finalize workflow, and projection/audit activities. Production worker startup fails closed if the Eino reasoning provider is not configured; use `ORCHESTRATION_EINO_PROVIDER=static` only for explicit local mock/dev runs.

## Happy path

1. Create or assign an Issue to an online agent runtime.
2. Start orchestration from the API or Issue Detail.
3. Confirm the run creates one active `orchestration_plan` with a Temporal workflow id.
4. Confirm the workflow projects analysis, dispatches a linked `agent_task_queue` row, and waits for the `agent_task_outcome` Signal.
5. Complete the daemon task with a structured result using `schema_version: "1"`, non-empty evidence, passed tests, and no risks.
6. Confirm validation, advisory review, summary, and review handoff run.
7. Confirm the Issue moves to `in_review`, not `done`, and no default attention comment is created.

Expected trace: start -> analyze -> dispatch -> completed Signal -> validation -> review -> summary -> review handoff.

## Fail-closed path

Unset `TEMPORAL_HOST_PORT` or stop Temporal, then start orchestration.

Expected evidence:

- API returns unavailable / fail-closed behavior.
- Projection contains a failed plan with `temporal_unavailable` and `configure_temporal`.
- No direct Agent Task fallback is created.
- A low-noise attention comment and `orchestration_attention` inbox item are created for Issue-relevant humans only.

## Failure / retry / approval path

Use an orchestration-linked task outcome with malformed JSON, missing evidence, failed tests, or non-empty risks.

Expected evidence:

- Malformed or missing evidence is classified as `evidence_insufficient`.
- Retryable evidence failure schedules a bounded retry while retry budget remains.
- Failed tests, risks, high-risk review concerns, or retry exhaustion route to `waiting_human` / Approval Gate.
- Human-only approve and retry actions write audit events before signaling Temporal.
- Retry exhausted and runtime failure create attention comments; successful runs do not.

## Issue Detail

Issue Detail must show the Linear Orchestration Panel trace from projection rows:

- plan status, reason, and recommended action
- node list with status, reason, attempt, and available human actions
- Expanded events including workflow events and signal audit events
- Evidence and Artifacts including analysis prompt and `review_handoff`
- attention comments in the Issue timeline when humans need to act

## Validation Commands

Backend workflow and activity checks:

```bash
cd server && go test -count=1 ./internal/orchestration
```

AnalyzeIssue live provider smoke test:

```bash
cd server && set -a; source ../.env; set +a; ORCHESTRATION_EINO_LIVE_TEST=1 go test -count=1 ./internal/orchestration -run TestEinoIssueAnalyzerLiveProviderSmoke -v
```

This smoke test only verifies AnalyzeIssue against the real Eino OpenAI-compatible ChatModel provider path. It does not start Temporal, connect to the database, create an Issue, dispatch an Agent Task, or verify `ReviewOutcome` / `SummarizeOutcome` with a live provider. Without `ORCHESTRATION_EINO_LIVE_TEST=1`, the live provider test is skipped and no external provider call is made.

Backend API and DB-backed orchestration checks:

```bash
cd server && go test -count=1 ./internal/handler -run 'Test(StartIssueOrchestration|CompleteLinkedAgentTask|ApproveOrchestration|CancelOrchestration|FinalizeWorkflow.*Attention)'
```

Backend service checks:

```bash
cd server && go test -count=1 ./internal/service
```

Frontend API contract checks:

```bash
pnpm --filter @multica/core exec vitest run api/schema.test.ts
```

Frontend Issue Detail component checks:

```bash
pnpm --filter @multica/views exec vitest run issues/components/issue-detail.test.tsx
```

Minimal E2E coverage should be run after `make server`, `make orchestration-worker`, `make daemon`, and `pnpm dev:web` are up:

```bash
pnpm exec playwright test e2e/orchestration.spec.ts
```

If the Playwright spec is not present in a checkout, record that as a residual manual setup requirement and use the backend/API/UI tests above as the automated MVP checkout proof.

## Final evidence

For a completed checkout, paste the exact commands and outcomes here:

- `make -n orchestration-worker`: passed; target resolves to env check, Postgres setup, and `cd server && go run ./cmd/orchestration-worker`.
- `cd server && go test -count=1 ./internal/orchestration ./internal/service`: passed.
- `cd server && set -a; source ../.env; set +a; ORCHESTRATION_EINO_LIVE_TEST=1 go test -count=1 ./internal/orchestration -run TestEinoIssueAnalyzerLiveProviderSmoke -v`: passed against the real provider configured in local `.env`.
- `cd server && go test -count=1 ./internal/handler -run 'Test(StartIssueOrchestration|CompleteLinkedAgentTask|ApproveOrchestration|CancelOrchestration|FinalizeWorkflow.*Attention)'`: passed.
- `pnpm --filter @multica/core exec vitest run api/schema.test.ts`: passed, 21 tests.
- `pnpm --filter @multica/views exec vitest run issues/components/issue-detail.test.tsx`: passed, 19 tests.
- Minimal E2E or manual browser validation: `e2e/orchestration.spec.ts` is present and covers Issue Detail projection after start; run it with Temporal, worker, daemon, and web processes running before treating browser-level E2E as complete.

Residual manual setup requirements:

- Temporal must be started explicitly with `temporal server start-dev`.
- API and worker must share `TEMPORAL_HOST_PORT=127.0.0.1:7233`, namespace, and task queue.
- A local daemon runtime must be authenticated and online for real Agent Task execution.
- Extend `e2e/orchestration.spec.ts` or capture manual browser evidence for the full happy/failure paths before treating browser-level E2E as complete.
