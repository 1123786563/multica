# Orchestration Node Observability Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a server-owned node observability contract so issue detail can explain each orchestration node's current state, reason, recommended action, and retry posture without reconstructing kernel logic in the UI.

**Architecture:** Keep the existing orchestration persistence model (`plan/node/event/artifact`) unchanged. Add a derived `summary` object to orchestration node responses, computed from evaluator results, node status, and the latest task/runtime failure signals. Consume that summary in the issue detail orchestration section to render a decision-first panel for all node states, including `completed`.

**Tech Stack:** Go 1.26.1, Chi handlers, pgx/sqlc, Zod, TanStack Query, React Testing Library, Vitest.

---

## File Structure

### Server

- Modify `server/internal/service/orchestration_evaluator.go`
  - Normalize evaluator output into stable UI-safe semantics: evaluation status, reason code, reason detail, recommended action.

- Create `server/internal/service/orchestration_observability.go`
  - Own node summary DTOs and summary-building helpers from node status + events + artifacts + evaluator payloads.

- Create `server/internal/service/orchestration_observability_test.go`
  - Unit tests for summary derivation across `running`, `waiting_human`, `failed`, `retry_scheduled`, `retry_exhausted`, and `completed`.

- Modify `server/internal/handler/orchestration.go`
  - Add `summary` to node responses inside `GET /api/issues/:id/orchestration`.

### Frontend/Core

- Modify `packages/core/api/schemas.ts`
  - Add defensive parsing for `node.summary`.

- Modify `packages/views/issues/components/issue-detail.tsx`
  - Replace the current event-first top section with a decision-first panel backed only by `node.summary`.

- Modify `packages/views/issues/components/issue-detail.test.tsx`
  - Add UI coverage for summary-backed rendering across key node states and actions.

- Modify `packages/views/locales/en/issues.json`
- Modify `packages/views/locales/zh-Hans/issues.json`
  - Add strings for reason/action/status copy used by the new panel.

## Task 1: Define Summary Semantics in the Evaluator Layer

**Files:**
- Modify: `server/internal/service/orchestration_evaluator.go`
- Test: `server/internal/service/orchestration_evaluator_test.go`

- [ ] **Step 1: Add failing evaluator tests for normalized semantics**

Add the following tests to `server/internal/service/orchestration_evaluator_test.go`:

```go
func TestHardCheckEvaluatorInvalidResultMapsToRetry(t *testing.T) {
	evaluator := HardCheckEvaluator{}

	result, err := evaluator.Evaluate(EvaluationInput{
		NodeType: "implement",
		ResultValidation: ResultValidation{
			Valid: false,
			Errors: []ValidationError{{Code: "missing_summary", Message: "missing summary"}},
		},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if result.Reason != "invalid_result" {
		t.Fatalf("expected invalid_result, got %q", result.Reason)
	}
	if result.RecommendedAction != "retry" {
		t.Fatalf("expected retry action, got %q", result.RecommendedAction)
	}
}

func TestHardCheckEvaluatorMissingEvidenceMapsToRetry(t *testing.T) {
	evaluator := HardCheckEvaluator{}

	result, err := evaluator.Evaluate(EvaluationInput{
		NodeType: "implement",
		ResultValidation: ResultValidation{
			Valid: true,
			Result: AgentStructuredResult{
				Status:           "completed",
				Summary:          "done",
				CriteriaEvidence: []CriteriaEvidence{{Criterion: "criterion", Evidence: "evidence"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if result.Reason != "missing_implementation_artifact" {
		t.Fatalf("expected missing_implementation_artifact, got %q", result.Reason)
	}
	if result.RecommendedAction != "retry" {
		t.Fatalf("expected retry action, got %q", result.RecommendedAction)
	}
}
```

- [ ] **Step 2: Run the targeted Go test and verify the current baseline**

Run:

```bash
cd server && go test ./internal/service -run 'TestHardCheckEvaluatorInvalidResultMapsToRetry|TestHardCheckEvaluatorMissingEvidenceMapsToRetry' -count=1
```

Expected: PASS or FAIL only for the specific behavior under test. If the existing file already covers these cases, keep the new tests and move on without changing scope.

- [ ] **Step 3: Extend evaluation results with stable observability fields**

In `server/internal/service/orchestration_evaluator.go`, extend `EvaluationResult` with summary-oriented fields:

```go
type EvaluationResult struct {
	Pass              bool
	Reason            string
	ReasonDetail      string
	RecommendedAction string
	Score             float64
	Risks             []string
	FailedCriteria    []string
	MissingArtifacts  []string
	Status            string
}
```

Set them in evaluator branches. For example:

```go
return EvaluationResult{
	Pass:              false,
	Status:            "invalid_result",
	Reason:            "invalid_result",
	ReasonDetail:      "Structured result payload did not satisfy the orchestration result contract.",
	RecommendedAction: "retry",
	Score:             0,
}, nil
```

And for passing evaluation:

```go
return EvaluationResult{
	Pass:              true,
	Status:            "passed",
	Reason:            "completed",
	ReasonDetail:      "Kernel hard checks passed for this node.",
	RecommendedAction: "none",
	Score:             1,
}, nil
```

- [ ] **Step 4: Re-run evaluator tests**

Run:

```bash
cd server && go test ./internal/service -run 'TestHardCheckEvaluator|TestParseAgentResultPayload' -count=1
```

Expected: PASS

- [ ] **Step 5: Commit evaluator semantic changes**

```bash
git add server/internal/service/orchestration_evaluator.go server/internal/service/orchestration_evaluator_test.go
git commit -m "feat(orchestration): normalize evaluator observability semantics"
```

## Task 2: Build Server-Side Node Summary Derivation

**Files:**
- Create: `server/internal/service/orchestration_observability.go`
- Create: `server/internal/service/orchestration_observability_test.go`

- [ ] **Step 1: Write failing summary derivation tests**

Create `server/internal/service/orchestration_observability_test.go` with:

```go
package service

import "testing"

func TestBuildNodeSummaryRunning(t *testing.T) {
	summary := BuildNodeSummary(NodeObservabilityInput{
		NodeStatus:   "running",
		AttemptCount: 1,
		MaxAttempts:  2,
	})
	if summary.Status != "running" {
		t.Fatalf("expected running, got %q", summary.Status)
	}
	if summary.ReasonCode != "running" {
		t.Fatalf("expected running reason code, got %q", summary.ReasonCode)
	}
	if summary.RecommendedAction != "none" {
		t.Fatalf("expected no action, got %q", summary.RecommendedAction)
	}
}

func TestBuildNodeSummaryWaitingHumanApproval(t *testing.T) {
	summary := BuildNodeSummary(NodeObservabilityInput{
		NodeStatus: "waiting_human",
		Evaluation: &EvaluationResult{
			Status:            "waiting_human",
			Reason:            "low_confidence",
			ReasonDetail:      "Kernel requires approval before completing the node.",
			RecommendedAction: "approve",
		},
	})
	if summary.ReasonCode != "waiting_for_approval" {
		t.Fatalf("expected waiting_for_approval, got %q", summary.ReasonCode)
	}
	if summary.RecommendedAction != "approve" {
		t.Fatalf("expected approve action, got %q", summary.RecommendedAction)
	}
}

func TestBuildNodeSummaryCompleted(t *testing.T) {
	summary := BuildNodeSummary(NodeObservabilityInput{
		NodeStatus: "completed",
		Evaluation: &EvaluationResult{
			Status:            "passed",
			Reason:            "completed",
			ReasonDetail:      "Kernel hard checks passed for this node.",
			RecommendedAction: "none",
		},
		LatestAgentSummary: "Implemented the requested changes.",
	})
	if summary.ReasonCode != "completed" {
		t.Fatalf("expected completed reason code, got %q", summary.ReasonCode)
	}
	if summary.LatestAgentSummary != "Implemented the requested changes." {
		t.Fatalf("expected latest agent summary to round-trip, got %q", summary.LatestAgentSummary)
	}
}
```

- [ ] **Step 2: Run the new test file and verify failure**

Run:

```bash
cd server && go test ./internal/service -run 'TestBuildNodeSummary' -count=1
```

Expected: FAIL because the summary types and builder do not exist yet.

- [ ] **Step 3: Implement summary DTOs and builder**

Create `server/internal/service/orchestration_observability.go` with:

```go
package service

type NodeSummary struct {
	Status                 string `json:"status"`
	ReasonCode             string `json:"reason_code"`
	ReasonTitle            string `json:"reason_title"`
	ReasonDetail           string `json:"reason_detail"`
	RecommendedAction      string `json:"recommended_action"`
	ActionEnabled          bool   `json:"action_enabled"`
	AttemptCount           int32  `json:"attempt_count"`
	MaxAttempts            int32  `json:"max_attempts"`
	LatestEvaluationStatus string `json:"latest_evaluation_status"`
	LatestAgentSummary     string `json:"latest_agent_summary"`
	UpdatedAt              string `json:"updated_at"`
}

type NodeObservabilityInput struct {
	NodeStatus          string
	AttemptCount        int32
	MaxAttempts         int32
	Evaluation          *EvaluationResult
	LatestAgentSummary  string
	LastRuntimeFailure  string
	UpdatedAt           string
}
```

Implement `BuildNodeSummary` with explicit status-first mapping:

```go
func BuildNodeSummary(input NodeObservabilityInput) NodeSummary {
	summary := NodeSummary{
		Status:                 input.NodeStatus,
		AttemptCount:           input.AttemptCount,
		MaxAttempts:            input.MaxAttempts,
		LatestAgentSummary:     input.LatestAgentSummary,
		UpdatedAt:              input.UpdatedAt,
		RecommendedAction:      "none",
		LatestEvaluationStatus: "",
	}

	if input.Evaluation != nil {
		summary.LatestEvaluationStatus = input.Evaluation.Status
	}

	switch input.NodeStatus {
	case "completed":
		summary.ReasonCode = "completed"
		summary.ReasonTitle = "Completed"
		summary.ReasonDetail = "Kernel checks passed and the node is complete."
	case "running":
		summary.ReasonCode = "running"
		summary.ReasonTitle = "Running"
		summary.ReasonDetail = "A runtime is currently executing this node."
	case "waiting_human":
		summary.ReasonCode = "waiting_for_approval"
		summary.ReasonTitle = "Approval required"
		summary.ReasonDetail = "Human review is required before the node can continue."
		summary.RecommendedAction = "approve"
		summary.ActionEnabled = true
	default:
		summary.ReasonCode = "ready_to_run"
		summary.ReasonTitle = "Ready"
		summary.ReasonDetail = "This node is ready for orchestration execution."
	}

	return summary
}
```

- [ ] **Step 4: Re-run summary tests**

Run:

```bash
cd server && go test ./internal/service -run 'TestBuildNodeSummary' -count=1
```

Expected: PASS

- [ ] **Step 5: Commit summary builder**

```bash
git add server/internal/service/orchestration_observability.go server/internal/service/orchestration_observability_test.go
git commit -m "feat(orchestration): add node summary observability builder"
```

## Task 3: Expose Node Summary in the Orchestration API

**Files:**
- Modify: `server/internal/handler/orchestration.go`
- Modify: `packages/core/api/schemas.ts`

- [ ] **Step 1: Add a failing schema/UI contract test in the frontend**

In `packages/views/issues/components/issue-detail.test.tsx`, add a test fixture that includes `summary` on nodes:

```tsx
mockApiObj.getIssueOrchestration.mockResolvedValueOnce({
  plans: [{ id: "plan-1", workspace_id: "ws-1", source_type: "issue", source_id: "issue-1", objective: "obj", status: "running", policy: {}, metadata: {}, created_by_type: null, created_by_id: null, created_at: "2026-05-11T00:00:00Z", updated_at: "2026-05-11T00:00:00Z" }],
  nodes: [{
    id: "node-1",
    plan_id: "plan-1",
    type: "implement",
    title: "Implement summary contract",
    description: null,
    status: "waiting_human",
    assignee_agent_id: null,
    input_contract: {},
    output_contract: {},
    evaluator_policy: {},
    retry_policy: {},
    runtime_constraints: {},
    attempt_count: 1,
    max_attempts: 2,
    started_at: null,
    completed_at: null,
    created_at: "2026-05-11T00:00:00Z",
    updated_at: "2026-05-11T00:00:00Z",
    summary: {
      status: "waiting_human",
      reason_code: "waiting_for_approval",
      reason_title: "Approval required",
      reason_detail: "Human review is required before the node can continue.",
      recommended_action: "approve",
      action_enabled: true,
      attempt_count: 1,
      max_attempts: 2,
      latest_evaluation_status: "waiting_human",
      latest_agent_summary: "Implementation is ready for sign-off.",
      updated_at: "2026-05-11T00:00:00Z",
    },
  }],
  events: [],
  artifacts: [],
});
```

- [ ] **Step 2: Update handler response types**

In `server/internal/handler/orchestration.go`, add:

```go
type OrchestrationNodeSummaryResponse struct {
	Status                 string `json:"status"`
	ReasonCode             string `json:"reason_code"`
	ReasonTitle            string `json:"reason_title"`
	ReasonDetail           string `json:"reason_detail"`
	RecommendedAction      string `json:"recommended_action"`
	ActionEnabled          bool   `json:"action_enabled"`
	AttemptCount           int32  `json:"attempt_count"`
	MaxAttempts            int32  `json:"max_attempts"`
	LatestEvaluationStatus string `json:"latest_evaluation_status"`
	LatestAgentSummary     string `json:"latest_agent_summary"`
	UpdatedAt              string `json:"updated_at"`
}
```

Then add it to the node payload:

```go
type OrchestrationNodeResponse struct {
	// existing fields...
	Summary *OrchestrationNodeSummaryResponse `json:"summary,omitempty"`
}
```

- [ ] **Step 3: Build summary during orchestration response assembly**

Inside `GetIssueOrchestration`, gather plan events once per plan, then build summaries when converting nodes. Add a helper like:

```go
func buildNodeSummaryResponse(node db.OrchestrationNode, events []db.OrchestrationEvent) *OrchestrationNodeSummaryResponse {
	summary := service.BuildNodeSummary(service.NodeObservabilityInput{
		NodeStatus:   node.Status,
		AttemptCount: node.AttemptCount,
		MaxAttempts:  node.MaxAttempts,
		UpdatedAt:    timestampToString(node.UpdatedAt),
	})
	return &OrchestrationNodeSummaryResponse{
		Status:                 summary.Status,
		ReasonCode:             summary.ReasonCode,
		ReasonTitle:            summary.ReasonTitle,
		ReasonDetail:           summary.ReasonDetail,
		RecommendedAction:      summary.RecommendedAction,
		ActionEnabled:          summary.ActionEnabled,
		AttemptCount:           summary.AttemptCount,
		MaxAttempts:            summary.MaxAttempts,
		LatestEvaluationStatus: summary.LatestEvaluationStatus,
		LatestAgentSummary:     summary.LatestAgentSummary,
		UpdatedAt:              summary.UpdatedAt,
	}
}
```

Pass the summary into `orchestrationNodeToResponse`.

- [ ] **Step 4: Add frontend schema support**

In `packages/core/api/schemas.ts`, add:

```ts
const OrchestrationNodeSummarySchema = z.object({
  status: z.string(),
  reason_code: z.string(),
  reason_title: z.string(),
  reason_detail: z.string(),
  recommended_action: z.string(),
  action_enabled: z.boolean().default(false),
  attempt_count: z.number().default(0),
  max_attempts: z.number().default(0),
  latest_evaluation_status: z.string().default(""),
  latest_agent_summary: z.string().default(""),
  updated_at: z.string().default(""),
}).loose();
```

Then attach it to `OrchestrationNodeSchema`:

```ts
  summary: OrchestrationNodeSummarySchema.nullish(),
```

- [ ] **Step 5: Verify handler and schema changes**

Run:

```bash
cd server && go test ./internal/handler -run TestGetIssueOrchestration -count=1
pnpm --filter @multica/views exec vitest run packages/views/issues/components/issue-detail.test.tsx
```

Expected:
- Go test: PASS, or "no tests to run" if this package has no matching test yet
- Vitest: PASS or only fails on the UI changes not yet implemented

- [ ] **Step 6: Commit API summary contract**

```bash
git add server/internal/handler/orchestration.go packages/core/api/schemas.ts packages/views/issues/components/issue-detail.test.tsx
git commit -m "feat(orchestration): expose node summary in issue orchestration api"
```

## Task 4: Replace the Top Section with a Summary-Backed Decision Panel

**Files:**
- Modify: `packages/views/issues/components/issue-detail.tsx`
- Modify: `packages/views/issues/components/issue-detail.test.tsx`
- Modify: `packages/views/locales/en/issues.json`
- Modify: `packages/views/locales/zh-Hans/issues.json`

- [ ] **Step 1: Add failing UI assertions for the decision panel**

In `packages/views/issues/components/issue-detail.test.tsx`, add:

```tsx
await screen.findByText("Approval required");
expect(screen.getByText("Human review is required before the node can continue.")).toBeInTheDocument();
expect(screen.getByText("Implementation is ready for sign-off.")).toBeInTheDocument();
expect(screen.getByRole("button", { name: /approve/i })).toBeInTheDocument();
```

- [ ] **Step 2: Add i18n labels for the panel**

In `packages/views/locales/en/issues.json` add under `orchestration`:

```json
"decision_panel": "Decision",
"reason": "Reason",
"recommended_action": "Recommended action",
"latest_agent_summary": "Latest agent summary",
"last_updated": "Last updated",
"no_action": "No action required"
```

In `packages/views/locales/zh-Hans/issues.json` add:

```json
"decision_panel": "决策面板",
"reason": "原因",
"recommended_action": "建议动作",
"latest_agent_summary": "最新智能体摘要",
"last_updated": "最近更新",
"no_action": "当前无需操作"
```

- [ ] **Step 3: Rewrite the top orchestration block to consume summary**

In `packages/views/issues/components/issue-detail.tsx`, replace the current top rows that rely on `latestEvaluatorReason` / `latestEvent` with summary-backed rows:

```tsx
const currentSummary = currentNode?.summary;
const recommendedActionLabel =
  currentSummary?.recommended_action === "approve"
    ? t(($) => $.orchestration.approve)
    : currentSummary?.recommended_action === "retry"
      ? t(($) => $.orchestration.retry)
      : t(($) => $.orchestration.no_action);
```

Render:

```tsx
<PropRow label={t(($) => $.orchestration.current_node)}>
  <span className="truncate text-muted-foreground">{currentNode?.title ?? "—"}</span>
</PropRow>
<PropRow label={t(($) => $.orchestration.reason)}>
  <span className="truncate text-muted-foreground">{currentSummary?.reason_title ?? "—"}</span>
</PropRow>
<PropRow label={t(($) => $.orchestration.recommended_action)}>
  <span className="truncate text-muted-foreground">{recommendedActionLabel}</span>
</PropRow>
<PropRow label={t(($) => $.orchestration.latest_agent_summary)}>
  <span className="truncate text-muted-foreground">{currentSummary?.latest_agent_summary || "—"}</span>
</PropRow>
```

And add a compact detail block:

```tsx
{currentSummary?.reason_detail && (
  <div className="col-span-2 rounded-md border border-border/60 bg-muted/20 px-2.5 py-2 text-xs text-muted-foreground">
    {currentSummary.reason_detail}
  </div>
)}
```

- [ ] **Step 4: Keep actions gated by summary + status**

Use summary for action visibility without removing existing status safety:

```tsx
const canApprove = currentNode?.status === "waiting_human" && currentSummary?.recommended_action === "approve" && currentSummary.action_enabled;
const canRetry = ["failed", "waiting_human"].includes(currentNode?.status ?? "") && currentSummary?.recommended_action === "retry";
```

- [ ] **Step 5: Run the focused frontend test**

Run:

```bash
pnpm --filter @multica/views exec vitest run packages/views/issues/components/issue-detail.test.tsx
```

Expected: PASS

- [ ] **Step 6: Commit the decision panel**

```bash
git add packages/views/issues/components/issue-detail.tsx packages/views/issues/components/issue-detail.test.tsx packages/views/locales/en/issues.json packages/views/locales/zh-Hans/issues.json
git commit -m "feat(orchestration): add summary-backed decision panel"
```

## Task 5: End-to-End Verification and Documentation Sync

**Files:**
- Modify: `docs/superpowers/specs/2026-05-11-orchestration-node-observability-design.md` (only if implementation drift requires it)
- Modify: `docs/superpowers/plans/2026-05-11-orchestration-node-observability-plan.md` (checkbox progress only if actively executing from the file)

- [ ] **Step 1: Run backend orchestration tests**

Run:

```bash
cd server && go test ./internal/service ./internal/handler -count=1
```

Expected: PASS

- [ ] **Step 2: Run the frontend orchestration-focused test**

Run:

```bash
pnpm --filter @multica/views exec vitest run packages/views/issues/components/issue-detail.test.tsx
```

Expected: PASS

- [ ] **Step 3: Run typecheck**

Run:

```bash
pnpm typecheck
```

Expected: PASS

- [ ] **Step 4: Review for drift against the spec**

Confirm all of the following are true:

- `completed` nodes return a `summary`
- the issue detail top panel reads only `node.summary` for explanation copy
- summary derives from server-side logic, not event-only frontend heuristics
- reason/action semantics stay within the spec's constrained top-level sets

- [ ] **Step 5: Commit verification or spec touch-ups if needed**

If implementation required doc updates:

```bash
git add docs/superpowers/specs/2026-05-11-orchestration-node-observability-design.md docs/superpowers/plans/2026-05-11-orchestration-node-observability-plan.md
git commit -m "docs(orchestration): sync observability plan with implementation"
```

