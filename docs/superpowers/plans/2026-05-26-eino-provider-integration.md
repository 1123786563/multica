# Eino Provider Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Multica's production Eino reasoning path use a real worker-scoped Eino provider for analyze, advisory review, and summarization, with strict structured outputs, visible failure projection, safe trace artifacts, and run-level reasoning profile binding.

**Architecture:** Temporal remains the orchestration lifecycle source of truth. The orchestration worker owns Eino provider configuration and injects one `EinoReasoner` into activities; Workflow code passes the bound reasoning profile reference through deterministic input and invokes projection activities for visible failures. Multica projection remains plan/node/event/artifact based, with Eino traces stored as orchestration artifacts and exposed through the existing issue orchestration API.

**Tech Stack:** Go, Temporal Go SDK, CloudWeGo Eino OpenAI ChatModel, PostgreSQL migrations, sqlc-adjacent handwritten orchestration queries, Next/core TypeScript schemas with zod, Vitest, Go tests.

---

## Source Decisions

- `CONTEXT.md`
- `docs/adr/0005-eino-reasons-inside-fixed-workflow.md`
- `docs/adr/0007-eino-review-is-advisory.md`
- `docs/adr/0014-projection-side-effects-through-activities.md`
- `docs/adr/0019-attention-comments-target-issue-relevant-humans.md`
- `docs/adr/0020-worker-scoped-eino-reasoning-provider.md`
- `docs/temporal-orchestration-mvp-checkout.md`

## Non-Goals

- No workspace-owned Eino API keys.
- No database-backed provider registry.
- No workspace provider selector UI.
- No prompt-only JSON fallback.
- No full raw prompt or raw provider response persistence.
- No Eino-generated Agent Task Result Schema.
- No automatic whole-run restart after provider failure.
- No profile replacement to resume old runs.

## File Map

- Modify: `server/internal/orchestration/workflow.go`
  - Add `ReasoningProfileRef` to workflow input.
  - Add Eino-specific activity options / retry policy.
  - Skip review/summarize during automatic evidence-repair retry.
  - Project Eino failure after Eino activity retry exhaustion.
- Modify: `server/internal/orchestration/activities.go`
  - Replace analyze-only interface with three-node `EinoReasoner`.
  - Add `ProjectEinoFailure`.
  - Persist Eino trace artifacts.
  - Keep deterministic validation as the owner of Result Schema decisions.
- Modify: `server/internal/orchestration/eino_analyzer.go`
  - Rename or split into Eino reasoner/provider implementation.
  - Add strict JSON schemas for analyze, review, and summarize.
  - Add failure classification and safe metadata extraction.
- Modify: `server/internal/orchestration/worker.go`
  - Inject production `EinoReasoner`.
  - Guard static provider behind explicit dev/mock allow flag.
  - Fail worker startup when production provider config is missing or incompatible.
- Modify: `server/cmd/orchestration-worker/main.go`
  - Read `ORCHESTRATION_EINO_ALLOW_STATIC`.
  - Set default reasoning profile reference.
- Modify: `server/internal/service/orchestration.go`
  - Add `reasoning_profile_ref` to plan creation, snapshot reads, DTOs, and start result.
  - Keep historical plans read-compatible with `legacy/default`.
- Create: `server/migrations/096_orchestration_reasoning_profile_ref.up.sql`
- Create: `server/migrations/096_orchestration_reasoning_profile_ref.down.sql`
- Modify: `packages/core/types/orchestration.ts`
  - Add `reasoning_profile_ref` to `IssueOrchestrationPlan`.
  - Document safe Eino trace data shape through typed metadata helpers if needed.
- Modify: `packages/core/api/schemas.ts`
  - Parse `reasoning_profile_ref` with default `legacy/default`.
  - Preserve safe artifact trace metadata.
- Modify: `packages/core/api/schema.test.ts`
  - Add malformed/legacy orchestration response coverage.
- Modify: `packages/views/issues/components/issue-detail.tsx`
  - Render reasoning profile reference, human-readable failure reason, and safe Eino trace metadata.
  - Do not render raw prompts or raw responses.
- Modify: `packages/views/issues/components/issue-detail.test.tsx`
  - Cover reasoning profile and Eino provider failure display.
- Modify: `docs/temporal-orchestration-mvp-checkout.md`
  - Update live provider smoke test description from analyze-only to three-node synthetic fixture.
  - Document static guard and provider failure projection.

## Stable Contracts

### Eino Failure Reason Codes

Use these stable codes in node `reason_code`, failure events, attention detail, and tests:

```go
const (
	EinoReasonProviderIncompatible = "provider_incompatible"
	EinoReasonProviderAuthFailed   = "provider_auth_failed"
	EinoReasonProviderTimeout      = "provider_timeout"
	EinoReasonProviderRateLimited  = "provider_rate_limited"
	EinoReasonProviderUnavailable  = "provider_unavailable"
	EinoReasonOutputMalformed      = "eino_output_malformed"
)
```

Recommended actions:

```go
func recommendedActionForEinoFailure(reason string) string {
	switch reason {
	case EinoReasonProviderIncompatible, EinoReasonProviderAuthFailed:
		return "configure_provider"
	case EinoReasonProviderTimeout, EinoReasonProviderRateLimited, EinoReasonProviderUnavailable:
		return "retry_later"
	case EinoReasonOutputMalformed:
		return "configure_provider"
	default:
		return "configure_provider"
	}
}
```

### Trace Artifact Types

Use these `orchestration_artifact.type` values:

```text
analysis_trace
review_trace
summary_trace
provider_failure_trace
```

Each trace artifact `data` must be safe to expose through the read API:

```json
{
  "reasoning_profile_ref": "worker-default",
  "prompt_profile_ref": "analyze/v1",
  "schema_name": "multica_analyze_issue",
  "schema_version": "1",
  "provider_label": "openai-compatible",
  "model": "configured-model-name",
  "capability_mode": "json_schema",
  "latency_ms": 1234,
  "usage": {
    "input_tokens": 100,
    "output_tokens": 200,
    "total_tokens": 300
  },
  "parsed_output": {
    "problem_summary": "short safe summary"
  },
  "failure": {
    "reason_code": "provider_timeout",
    "message": "Provider timed out"
  }
}
```

Do not store:

- raw prompt
- raw provider response
- API key
- Authorization headers
- full base URL with secret query parameters
- raw Agent Task logs
- raw diffs

## Task 1: DB and API Contract Baseline

**Files:**
- Create: `server/migrations/096_orchestration_reasoning_profile_ref.up.sql`
- Create: `server/migrations/096_orchestration_reasoning_profile_ref.down.sql`
- Modify: `server/internal/service/orchestration.go`
- Modify: `packages/core/types/orchestration.ts`
- Modify: `packages/core/api/schemas.ts`
- Modify: `packages/core/api/schema.test.ts`

- [ ] **Step 1: Write frontend schema tests for legacy and new plan fields**

Add tests to `packages/core/api/schema.test.ts` that parse both old and new orchestration payloads.

```ts
import { IssueOrchestrationSchema } from "./schemas";

it("defaults missing orchestration reasoning profile ref for historical plans", () => {
  const parsed = IssueOrchestrationSchema.parse({
    plans: [
      {
        id: "plan-1",
        issue_id: "issue-1",
        status: "completed",
        workflow_type: "issue_mvp",
        projection_version: 1,
        created_at: "2026-05-26T00:00:00Z",
        updated_at: "2026-05-26T00:00:00Z",
        summary: { reason_code: "", recommended_action: "none" },
        available_actions: [],
        nodes: [],
        events: [],
        artifacts: [],
      },
    ],
  });

  expect(parsed.plans[0]?.reasoning_profile_ref).toBe("legacy/default");
});

it("preserves safe Eino provider trace metadata on orchestration artifacts", () => {
  const parsed = IssueOrchestrationSchema.parse({
    plans: [
      {
        id: "plan-1",
        issue_id: "issue-1",
        status: "failed",
        reasoning_profile_ref: "worker-default",
        workflow_type: "issue_mvp",
        projection_version: 1,
        created_at: "2026-05-26T00:00:00Z",
        updated_at: "2026-05-26T00:00:00Z",
        summary: {
          reason_code: "provider_timeout",
          recommended_action: "retry_later",
        },
        available_actions: [],
        nodes: [],
        events: [],
        artifacts: [
          {
            id: "artifact-1",
            type: "provider_failure_trace",
            source: "eino",
            label: "Provider failure",
            data: {
              reasoning_profile_ref: "worker-default",
              schema_name: "multica_review_outcome",
              schema_version: "1",
              provider_label: "openai-compatible",
              model: "test-model",
              latency_ms: 1500,
              usage: { input_tokens: 10, output_tokens: 0, total_tokens: 10 },
              failure: {
                reason_code: "provider_timeout",
                message: "Provider timed out",
              },
            },
          },
        ],
      },
    ],
  });

  expect(parsed.plans[0]?.artifacts[0]?.data).toMatchObject({
    reasoning_profile_ref: "worker-default",
    schema_name: "multica_review_outcome",
  });
});
```

- [ ] **Step 2: Run frontend schema tests and verify they fail**

Run:

```bash
pnpm --filter @multica/core exec vitest run api/schema.test.ts
```

Expected: FAIL because `IssueOrchestrationPlan` and schema do not yet expose/default `reasoning_profile_ref`.

- [ ] **Step 3: Add migration**

Create `server/migrations/096_orchestration_reasoning_profile_ref.up.sql`:

```sql
ALTER TABLE orchestration_plan
ADD COLUMN IF NOT EXISTS reasoning_profile_ref text NOT NULL DEFAULT 'legacy/default';

COMMENT ON COLUMN orchestration_plan.reasoning_profile_ref
IS 'Semantic Eino reasoning profile reference bound at orchestration run start; legacy/default is used only for historical read compatibility.';
```

Create `server/migrations/096_orchestration_reasoning_profile_ref.down.sql`:

```sql
ALTER TABLE orchestration_plan
DROP COLUMN IF EXISTS reasoning_profile_ref;
```

- [ ] **Step 4: Add Go DTO field and reads**

In `server/internal/service/orchestration.go`, add:

```go
type OrchestrationPlan struct {
	ID                  string                  `json:"id"`
	IssueID             string                  `json:"issue_id"`
	Status              string                  `json:"status"`
	ReasoningProfileRef string                  `json:"reasoning_profile_ref"`
	TemporalWorkflowID  string                  `json:"temporal_workflow_id,omitempty"`
	TemporalRunID       string                  `json:"temporal_run_id,omitempty"`
	WorkflowType        string                  `json:"workflow_type"`
	ProjectionVersion   int                     `json:"projection_version"`
	CreatedAt           time.Time               `json:"created_at"`
	UpdatedAt           time.Time               `json:"updated_at"`
	Summary             OrchestrationSummary    `json:"summary"`
	AvailableActions    []string                `json:"available_actions"`
	Nodes               []OrchestrationNode     `json:"nodes"`
	Events              []OrchestrationEvent    `json:"events"`
	Artifacts           []OrchestrationArtifact `json:"artifacts"`
}
```

Update the `getPlanWithActions` select:

```sql
SELECT id, issue_id, status, reason_code, recommended_action,
	COALESCE(reasoning_profile_ref, 'legacy/default'),
	COALESCE(temporal_workflow_id, ''), COALESCE(temporal_run_id, ''),
	workflow_type, projection_version, created_at, updated_at
FROM orchestration_plan
WHERE id = $1
```

Scan into `&plan.ReasoningProfileRef` before temporal fields.

- [ ] **Step 5: Add TS type and schema default**

In `packages/core/types/orchestration.ts`:

```ts
export interface IssueOrchestrationPlan {
  id: string;
  issue_id: string;
  status: string;
  reasoning_profile_ref: string;
  temporal_workflow_id?: string;
  temporal_run_id?: string;
  workflow_type: string;
  projection_version: number;
  created_at: string;
  updated_at: string;
  summary: IssueOrchestrationSummary;
  available_actions: string[];
  nodes: IssueOrchestrationNode[];
  events: IssueOrchestrationEvent[];
  artifacts: IssueOrchestrationArtifact[];
}
```

In `packages/core/api/schemas.ts`, add to `IssueOrchestrationPlanSchema`:

```ts
reasoning_profile_ref: z.string().default("legacy/default"),
```

- [ ] **Step 6: Run focused tests**

Run:

```bash
pnpm --filter @multica/core exec vitest run api/schema.test.ts
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add server/migrations/096_orchestration_reasoning_profile_ref.up.sql \
  server/migrations/096_orchestration_reasoning_profile_ref.down.sql \
  server/internal/service/orchestration.go \
  packages/core/types/orchestration.ts \
  packages/core/api/schemas.ts \
  packages/core/api/schema.test.ts
git commit -m "feat: add orchestration reasoning profile contract"
```

## Task 2: Eino Reasoner Interface and Strict Schemas

**Files:**
- Modify: `server/internal/orchestration/workflow.go`
- Modify: `server/internal/orchestration/activities.go`
- Modify: `server/internal/orchestration/eino_analyzer.go`
- Modify: `server/internal/orchestration/eino_analyzer_test.go`
- Modify: `server/internal/orchestration/activities_test.go`
- Modify: `server/internal/orchestration/worker.go`

- [ ] **Step 1: Write failing reasoner tests**

In `server/internal/orchestration/eino_analyzer_test.go`, add parser tests for review and summary:

```go
func TestParseEinoReviewOutcomeOutputRejectsAuthoritativeFields(t *testing.T) {
	raw := `{
		"summary":"Evidence looks good",
		"high_risk":false,
		"concern":"",
		"severity_label":"low",
		"evidence":["tests passed"],
		"risks":[],
		"recommended_action":"accept",
		"is_success":true
	}`
	if _, err := parseEinoReviewOutcomeOutput(raw); err == nil {
		t.Fatal("review output with authoritative fields must be rejected")
	}
}

func TestParseEinoSummarizeOutcomeOutputRejectsWorkflowAction(t *testing.T) {
	raw := `{
		"summary":"Ready for review",
		"trace_ref":"plan/node/task",
		"terminal_plan_status":"completed"
	}`
	if _, err := parseEinoSummarizeOutcomeOutput(raw); err == nil {
		t.Fatal("summary output with workflow action must be rejected")
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
cd server && go test -count=1 ./internal/orchestration -run 'TestParseEino(Review|Summarize)'
```

Expected: FAIL because review/summarize parse functions do not exist.

- [ ] **Step 3: Replace analyze-only interface with three-node reasoner**

In `server/internal/orchestration/activities.go`:

```go
type ActivitySet struct {
	DB            service.OrchestrationDB
	Queries       *db.Queries
	Orchestration *service.OrchestrationService
	Reasoner      EinoReasoner
}

type EinoReasoner interface {
	AnalyzeIssue(ctx context.Context, issue IssueSnapshot, input IssueWorkflowInput) (AnalyzeIssueResult, error)
	ReviewOutcome(ctx context.Context, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (ReviewOutcomeResult, error)
	SummarizeOutcome(ctx context.Context, review ReviewOutcomeResult, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (SummarizeOutcomeResult, error)
}
```

Rename `StaticIssueAnalyzer` to `StaticEinoReasoner` and implement the three methods with the current local behavior. Update `AnalyzeIssue`, `ReviewOutcome`, and `SummarizeOutcome` activities to call `a.Reasoner`, defaulting to `StaticEinoReasoner{}` only in tests where no reasoner is injected.

- [ ] **Step 4: Add strict review and summary output structs**

In `server/internal/orchestration/eino_analyzer.go`:

```go
type einoReviewOutcomeJSON struct {
	Summary           string    `json:"summary"`
	HighRisk          bool      `json:"high_risk"`
	Concern           string    `json:"concern"`
	SeverityLabel     string    `json:"severity_label"`
	Evidence          *[]string `json:"evidence"`
	Risks             *[]string `json:"risks"`
	RecommendedAction string    `json:"recommended_action"`
}

type einoSummarizeOutcomeJSON struct {
	Summary  string `json:"summary"`
	TraceRef string `json:"trace_ref"`
}
```

Add parser functions using `json.Decoder.DisallowUnknownFields()` and missing-field checks. Required review fields: `summary`, `high_risk`, `concern`, `severity_label`, `evidence`, `risks`, `recommended_action`. Required summary fields: `summary`, `trace_ref`.

- [ ] **Step 5: Add Eino reasoner methods**

Rename `EinoIssueAnalyzer` to `EinoChatReasoner` or keep the existing type and add methods:

```go
func (a EinoIssueAnalyzer) ReviewOutcome(ctx context.Context, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (ReviewOutcomeResult, error) {
	if a.model == nil {
		return ReviewOutcomeResult{}, errors.New("Eino reasoning provider is not configured")
	}
	resp, err := a.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(einoReviewOutcomeSystemPrompt()),
		schema.UserMessage(einoReviewOutcomeUserPrompt(validation, analysis, issue, dispatch)),
	}, einomodel.WithMaxTokens(a.maxOutputTokens), einomodel.WithTemperature(0))
	if err != nil {
		return ReviewOutcomeResult{}, classifyEinoProviderError(err)
	}
	if resp == nil {
		return ReviewOutcomeResult{}, newEinoFailure(EinoReasonProviderUnavailable, "Eino review outcome provider returned no response")
	}
	return parseEinoReviewOutcomeOutput(resp.Content)
}

func (a EinoIssueAnalyzer) SummarizeOutcome(ctx context.Context, review ReviewOutcomeResult, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (SummarizeOutcomeResult, error) {
	if a.model == nil {
		return SummarizeOutcomeResult{}, errors.New("Eino reasoning provider is not configured")
	}
	resp, err := a.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(einoSummarizeOutcomeSystemPrompt()),
		schema.UserMessage(einoSummarizeOutcomeUserPrompt(review, validation, analysis, issue, dispatch)),
	}, einomodel.WithMaxTokens(a.maxOutputTokens), einomodel.WithTemperature(0))
	if err != nil {
		return SummarizeOutcomeResult{}, classifyEinoProviderError(err)
	}
	if resp == nil {
		return SummarizeOutcomeResult{}, newEinoFailure(EinoReasonProviderUnavailable, "Eino summarize outcome provider returned no response")
	}
	return parseEinoSummarizeOutcomeOutput(resp.Content)
}
```

Keep prompts focused on structured evidence summaries, not raw diffs/logs.

- [ ] **Step 6: Update worker injection**

In `server/internal/orchestration/worker.go`, update `NewWorkerActivitySet`:

```go
reasoner, err := newWorkerEinoReasoner(ctx, cfg)
if err != nil {
	return ActivitySet{}, err
}
return ActivitySet{
	DB:            pool,
	Queries:       queries,
	Orchestration: orchestrationSvc,
	Reasoner:      reasoner,
}, nil
```

- [ ] **Step 7: Run focused Go tests**

Run:

```bash
cd server && go test -count=1 ./internal/orchestration
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add server/internal/orchestration/workflow.go \
  server/internal/orchestration/activities.go \
  server/internal/orchestration/eino_analyzer.go \
  server/internal/orchestration/eino_analyzer_test.go \
  server/internal/orchestration/activities_test.go \
  server/internal/orchestration/worker.go
git commit -m "feat: route all eino reasoning nodes through provider"
```

## Task 3: Worker Config, Static Guard, and Profile Binding

**Files:**
- Modify: `server/internal/orchestration/workflow.go`
- Modify: `server/internal/orchestration/worker.go`
- Modify: `server/cmd/orchestration-worker/main.go`
- Modify: `server/internal/orchestration/checkout_doc_test.go`
- Modify: `server/internal/orchestration/eino_analyzer_test.go`
- Modify: `server/internal/service/orchestration.go`

- [ ] **Step 1: Write failing tests for static guard and profile binding**

In `server/internal/orchestration/eino_analyzer_test.go`:

```go
func TestNewWorkerEinoReasonerRejectsStaticWithoutExplicitAllow(t *testing.T) {
	_, err := newWorkerEinoReasoner(t.Context(), EinoReasoningConfig{
		Provider: EinoProviderStatic,
	})
	if err == nil {
		t.Fatal("static Eino provider must require explicit dev/mock allow flag")
	}
}

func TestNewWorkerEinoReasonerAllowsExplicitStaticMock(t *testing.T) {
	reasoner, err := newWorkerEinoReasoner(t.Context(), EinoReasoningConfig{
		Provider:    EinoProviderStatic,
		AllowStatic: true,
	})
	if err != nil {
		t.Fatalf("explicit static mock should be allowed: %v", err)
	}
	if _, ok := reasoner.(StaticEinoReasoner); !ok {
		t.Fatalf("expected StaticEinoReasoner, got %T", reasoner)
	}
}
```

In `server/internal/orchestration/workflow_test.go`, assert `IssueWorkflowInput` carries `ReasoningProfileRef` through start fixtures and defaults to `worker-default` in test helpers.

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
cd server && go test -count=1 ./internal/orchestration -run 'TestNewWorkerEinoReasoner|TestIssueWorkflow'
```

Expected: FAIL because `AllowStatic` and `ReasoningProfileRef` do not exist yet.

- [ ] **Step 3: Extend config and workflow input**

In `server/internal/orchestration/eino_analyzer.go`:

```go
type EinoReasoningConfig struct {
	Provider            string
	APIKey              string
	Model               string
	BaseURL             string
	Timeout             time.Duration
	MaxOutputTokens     int
	AllowStatic         bool
	ReasoningProfileRef string
}
```

In `server/internal/orchestration/workflow.go`:

```go
type IssueWorkflowInput struct {
	WorkspaceID         string
	IssueID             string
	PlanID              string
	WorkflowID          string
	ReasoningProfileRef string
}
```

Add a helper:

```go
func normalizeReasoningProfileRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "worker-default"
	}
	return ref
}
```

- [ ] **Step 4: Guard static provider**

In `server/internal/orchestration/worker.go`:

```go
func newWorkerEinoReasoner(ctx context.Context, cfg EinoReasoningConfig) (EinoReasoner, error) {
	if strings.EqualFold(strings.TrimSpace(cfg.Provider), EinoProviderStatic) {
		if !cfg.AllowStatic {
			return nil, fmt.Errorf("static Eino provider requires explicit development/mock allow flag")
		}
		return StaticEinoReasoner{}, nil
	}
	reasoner, err := NewEinoIssueAnalyzer(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("configure Eino reasoning provider: %w", err)
	}
	return reasoner, nil
}
```

- [ ] **Step 5: Read environment config**

In `server/cmd/orchestration-worker/main.go`, set:

```go
AllowStatic:         os.Getenv("ORCHESTRATION_EINO_ALLOW_STATIC") == "1",
ReasoningProfileRef: envString("ORCHESTRATION_EINO_PROFILE_REF", "worker-default"),
```

Add:

```go
func envString(name, def string) string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	return raw
}
```

- [ ] **Step 6: Bind profile at start**

In `server/internal/service/orchestration.go`, set `reasoning_profile_ref` when inserting `orchestration_plan`. For MVP use:

```go
reasoningProfileRef := "worker-default"
```

Pass it into `IssueWorkflowInput{ReasoningProfileRef: reasoningProfileRef}` when starting Temporal. Use the same value in the plan insert so Workflow input and projection agree.

- [ ] **Step 7: Run tests**

Run:

```bash
cd server && go test -count=1 ./internal/orchestration ./internal/service
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add server/internal/orchestration/workflow.go \
  server/internal/orchestration/worker.go \
  server/internal/orchestration/eino_analyzer.go \
  server/cmd/orchestration-worker/main.go \
  server/internal/orchestration/checkout_doc_test.go \
  server/internal/orchestration/eino_analyzer_test.go \
  server/internal/service/orchestration.go
git commit -m "feat: bind eino reasoning profile at run start"
```

## Task 4: Eino Failure Projection

**Files:**
- Modify: `server/internal/orchestration/workflow.go`
- Modify: `server/internal/orchestration/activities.go`
- Modify: `server/internal/orchestration/worker.go`
- Modify: `server/internal/orchestration/workflow_test.go`
- Modify: `server/internal/orchestration/activities_test.go`
- Modify: `server/internal/service/orchestration.go`

- [ ] **Step 1: Write failing workflow test**

In `server/internal/orchestration/workflow_test.go`, add a test where `ReviewOutcomeActivityName` returns a non-retryable Eino failure after Temporal retries are exhausted in the test harness and assert `ProjectEinoFailureActivityName` is called with `workflow_node_key == "review"`.

```go
func TestIssueWorkflowProjectsEinoFailureAfterReviewFailure(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	registerIssueWorkflowTestActivities(env)

	input := IssueWorkflowInput{
		WorkspaceID:         "workspace-1",
		IssueID:             "issue-1",
		PlanID:              "plan-1",
		WorkflowID:          "workflow-1",
		ReasoningProfileRef: "worker-default",
	}

	env.OnActivity(LoadIssueActivityName, mock.Anything, mock.Anything).Return(IssueSnapshot{
		WorkspaceID: "workspace-1",
		IssueID:     "issue-1",
		Title:       "Provider failure projection",
	}, nil)
	env.OnActivity(AnalyzeIssueActivityName, mock.Anything, mock.Anything, mock.Anything).Return(AnalyzeIssueResult{
		ProblemSummary:         "Provider failure projection",
		ExecutionAdvice:        "Keep the failure visible",
		RecommendedAgentPrompt: "Implement the failure projection",
		ReasonCode:             "eino_analysis_ready",
		RecommendedAction:      "none",
	}, nil)
	env.OnActivity(DispatchTaskActivityName, mock.Anything, mock.Anything).Return(service.DispatchAgentTaskResult{
		PlanID:  "plan-1",
		TaskID:  "task-1",
		NodeID:  "node-1",
		Attempt: 1,
	}, nil)
	env.OnActivity(ValidateOutcomeActivityName, mock.Anything, mock.Anything).Return(ValidateOutcomeResult{
		Status:             "completed",
		TerminalPlanStatus: "completed",
	}, nil)
	env.OnActivity(ReviewOutcomeActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(ReviewOutcomeResult{}, newEinoFailure(EinoReasonProviderTimeout, "provider timed out")).Once()
	env.OnActivity(ProjectEinoFailureActivityName, mock.Anything, mock.MatchedBy(func(in EinoFailureProjectionInput) bool {
		return in.WorkflowNodeKey == "review" &&
			in.ReasonCode == EinoReasonProviderTimeout &&
				in.RecommendedAction == "retry_later" &&
				in.ReasoningProfileRef == "worker-default"
	})).Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AgentTaskOutcomeSignalName, service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "workflow-1",
			PlanID:         "plan-1",
			NodeID:         "node-1",
			TaskID:         "task-1",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"schema_version":"1","summary":"done","changed_files":["server/internal/orchestration/workflow.go"],"artifacts":[],"tests":[{"name":"go test","status":"passed"}],"risks":[],"evidence":[{"type":"test","ref":"go test"}]}`),
		})
	}, time.Second)

	env.ExecuteWorkflow(IssueWorkflow, input)
	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow should complete with failure projection after Eino provider failure")
	}
	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("workflow should still fail after projecting Eino provider failure")
	}
}
```

- [ ] **Step 2: Run test and verify it fails**

Run:

```bash
cd server && go test -count=1 ./internal/orchestration -run TestIssueWorkflowProjectsEinoFailureAfterReviewFailure
```

Expected: FAIL because `ProjectEinoFailureActivityName` and `EinoFailureProjectionInput` do not exist.

- [ ] **Step 3: Add failure projection types and activity name**

In `server/internal/orchestration/workflow.go`:

```go
const ProjectEinoFailureActivityName = "orchestration.project_eino_failure"

type EinoFailureProjectionInput struct {
	PlanID              string
	WorkflowNodeKey     string
	ReasonCode          string
	RecommendedAction   string
	Message             string
	ReasoningProfileRef string
	SchemaName          string
	SchemaVersion       string
	ProviderLabel       string
	Model               string
	CapabilityMode      string
	LatencyMS           int64
}
```

- [ ] **Step 4: Implement `ProjectEinoFailure`**

In `server/internal/orchestration/activities.go`, implement:

```go
func (a ActivitySet) ProjectEinoFailure(ctx context.Context, input EinoFailureProjectionInput) error {
	if a.DB == nil {
		return fmt.Errorf("projection store unavailable")
	}
	planID, err := util.ParseUUID(input.PlanID)
	if err != nil {
		return err
	}
	action := input.RecommendedAction
	if strings.TrimSpace(action) == "" {
		action = recommendedActionForEinoFailure(input.ReasonCode)
	}
	if err := a.projectNode(ctx, input.PlanID, input.WorkflowNodeKey, "failed", input.ReasonCode, action, 1); err != nil {
		return err
	}
	if _, err := a.DB.Exec(ctx, `
		UPDATE orchestration_plan
		SET status = 'failed',
			reason_code = $2,
			recommended_action = $3,
			sync_error = $4,
			updated_at = now(),
			completed_at = now()
		WHERE id = $1 AND status NOT IN ('cancelled', 'completed')
	`, planID, input.ReasonCode, action, input.Message); err != nil {
		return err
	}
	if err := a.recordEvent(ctx, planID, "eino.provider_failed", "eino", input.Message, map[string]any{
		"workflow_node_key":     input.WorkflowNodeKey,
		"reason_code":           input.ReasonCode,
		"recommended_action":    action,
		"reasoning_profile_ref": input.ReasoningProfileRef,
	}); err != nil {
		return err
	}
	trace := map[string]any{
		"reasoning_profile_ref": input.ReasoningProfileRef,
		"schema_name":           input.SchemaName,
		"schema_version":        input.SchemaVersion,
		"provider_label":        input.ProviderLabel,
		"model":                 input.Model,
		"capability_mode":       input.CapabilityMode,
		"latency_ms":            input.LatencyMS,
		"failure": map[string]any{
			"reason_code": input.ReasonCode,
			"message":     input.Message,
		},
	}
	if err := a.recordArtifact(ctx, planID, pgtype.UUID{}, "provider_failure_trace", "eino", "Provider failure", "", trace); err != nil {
		return err
	}
	return service.CreateOrchestrationAttention(ctx, a.DB, planID, "eino_failure:"+input.ReasonCode, "Eino provider failure: "+input.Message)
}
```

Use the existing artifact helper signature in `activities.go`; if it differs, adapt only parameter names, not semantics.

- [ ] **Step 5: Register activity**

In `server/internal/orchestration/worker.go`:

```go
w.RegisterActivityWithOptions(activities.ProjectEinoFailure, activity.RegisterOptions{Name: ProjectEinoFailureActivityName})
```

- [ ] **Step 6: Wrap Eino activity calls in Workflow**

In `IssueWorkflow`, when `AnalyzeIssue`, `ReviewOutcome`, or `SummarizeOutcome` returns an Eino failure after Temporal activity retry exhaustion, execute `ProjectEinoFailureActivityName` with the node key and return the original error. Do not enter Approval Gate and do not skip summary.

- [ ] **Step 7: Run focused tests**

Run:

```bash
cd server && go test -count=1 ./internal/orchestration
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add server/internal/orchestration/workflow.go \
  server/internal/orchestration/activities.go \
  server/internal/orchestration/worker.go \
  server/internal/orchestration/workflow_test.go \
  server/internal/orchestration/activities_test.go \
  server/internal/service/orchestration.go
git commit -m "feat: project eino provider failures"
```

## Task 5: Retry Policy and Review Invocation Boundary

**Files:**
- Modify: `server/internal/orchestration/workflow.go`
- Modify: `server/internal/orchestration/workflow_test.go`
- Modify: `server/internal/orchestration/activities_test.go`

- [ ] **Step 1: Write tests for retry invocation boundary**

In `server/internal/orchestration/workflow_test.go`, add a test where validation returns `ShouldRetry: true` on attempt 1. Assert `ReviewOutcomeActivityName` and `SummarizeOutcomeActivityName` are not called before the second dispatch.

In `TestIssueWorkflowRetriesEvidenceInsufficientOnceBeforeFinalizing`, change the review/summarize expectations from two calls to one call:

```go
env.OnActivity(ReviewOutcomeActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
	Return(ReviewOutcomeResult{Summary: "review"}, nil).Once()
env.OnActivity(SummarizeOutcomeActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
	Return(SummarizeOutcomeResult{Summary: "summary"}, nil).Once()
```

Keep the existing two-dispatch, two-validation, and two-finalize assertions in that test. The test now proves review and summarize only run after the final non-retry validation path, not during automatic evidence repair.

- [ ] **Step 2: Run and verify failure or existing over-calling**

Run:

```bash
cd server && go test -count=1 ./internal/orchestration -run TestIssueWorkflowRetriesEvidenceInsufficientOnceBeforeFinalizing
```

Expected: FAIL if current workflow calls review/summarize during automatic retry.

- [ ] **Step 3: Add Eino activity options**

In `workflow.go`, use separate options:

```go
func defaultActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
	}
}

func einoActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 90 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    20 * time.Second,
			MaximumAttempts:    3,
		},
	}
}
```

Import `go.temporal.io/sdk/temporal`. Use Eino options only around analyze/review/summarize calls.

- [ ] **Step 4: Skip review/summarize during automatic retry**

In the workflow loop, after `ValidateOutcome`, preserve the existing retry projection but do not call review/summarize when `validation.ShouldRetry && attempt < maxNodeAttempts`. Use an empty `ReviewOutcomeResult{}` and `SummarizeOutcomeResult{}` only for `FinalizeWorkflow` retry projection if the activity signature requires them.

- [ ] **Step 5: Run tests**

Run:

```bash
cd server && go test -count=1 ./internal/orchestration
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/internal/orchestration/workflow.go \
  server/internal/orchestration/workflow_test.go \
  server/internal/orchestration/activities_test.go
git commit -m "fix: bound eino retries and review invocation"
```

## Task 6: Trace Artifact Projection for Successful Nodes

**Files:**
- Modify: `server/internal/orchestration/activities.go`
- Modify: `server/internal/orchestration/eino_analyzer.go`
- Modify: `server/internal/orchestration/activities_test.go`

- [ ] **Step 1: Write tests for success trace artifacts**

In `activities_test.go`, add tests that `AnalyzeIssue`, `ReviewOutcome`, and `SummarizeOutcome` write artifact types `analysis_trace`, `review_trace`, and `summary_trace` with safe metadata and without raw prompt/response.

```go
type stubEinoReasoner struct {
	analysis AnalyzeIssueResult
	review   ReviewOutcomeResult
	summary  SummarizeOutcomeResult
	trace    EinoTrace
	err      error
}

func (r stubEinoReasoner) AnalyzeIssue(ctx context.Context, issue IssueSnapshot, input IssueWorkflowInput) (AnalyzeIssueResult, error) {
	result := r.analysis
	result.Trace = r.trace
	return result, r.err
}

func (r stubEinoReasoner) ReviewOutcome(ctx context.Context, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (ReviewOutcomeResult, error) {
	result := r.review
	result.Trace = r.trace
	return result, r.err
}

func (r stubEinoReasoner) SummarizeOutcome(ctx context.Context, review ReviewOutcomeResult, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (SummarizeOutcomeResult, error) {
	result := r.summary
	result.Trace = r.trace
	return result, r.err
}

func TestAnalyzeIssueProjectsSafeProviderTrace(t *testing.T) {
	var artifactData map[string]any
	activity := ActivitySet{
		DB: &captureArtifactDB{
			onArtifact: func(sql string, args ...any) {
				if !strings.Contains(strings.ToLower(sql), "orchestration_artifact") {
					return
				}
				for _, arg := range args {
					raw, ok := arg.([]byte)
					if !ok {
						continue
					}
					var data map[string]any
					if json.Unmarshal(raw, &data) == nil {
						artifactData = data
					}
				}
			},
		},
		Reasoner: stubEinoReasoner{
			analysis: AnalyzeIssueResult{
				ProblemSummary:         "Fix issue",
				ExecutionAdvice:        "Inspect focused files",
				SuspectedContext:       "server/internal/orchestration",
				RecommendedAgentPrompt: "Fix the issue",
				ReasonCode:             "eino_analysis_ready",
				RecommendedAction:      "none",
			},
			trace: EinoTrace{
				ReasoningProfileRef: "worker-default",
				SchemaName:          "multica_analyze_issue",
				SchemaVersion:       "1",
				ProviderLabel:      "openai-compatible",
				Model:              "test-model",
				CapabilityMode:     "json_schema",
				LatencyMS:          12,
			},
		},
	}
	_, err := activity.AnalyzeIssue(t.Context(), IssueSnapshot{Title: "Fix issue"}, IssueWorkflowInput{
		PlanID:              "11111111-1111-1111-1111-111111111111",
		ReasoningProfileRef: "worker-default",
	})
	if err != nil {
		t.Fatalf("AnalyzeIssue returned error: %v", err)
	}
	if artifactData == nil {
		t.Fatal("expected trace artifact data")
	}
	if artifactData["raw_prompt"] != nil || artifactData["raw_response"] != nil {
		t.Fatal("trace artifact must not persist raw prompt or raw response")
	}
}
```

- [ ] **Step 2: Run test and verify failure**

Run:

```bash
cd server && go test -count=1 ./internal/orchestration -run TestAnalyzeIssueProjectsSafeProviderTrace
```

Expected: FAIL because trace support is not implemented yet.

- [ ] **Step 3: Add trace metadata type**

In `eino_analyzer.go`:

```go
type EinoTrace struct {
	ReasoningProfileRef string
	PromptProfileRef    string
	SchemaName          string
	SchemaVersion       string
	ProviderLabel       string
	Model               string
	CapabilityMode      string
	LatencyMS           int64
	InputTokens         int
	OutputTokens        int
	TotalTokens         int
}
```

If returning trace alongside results makes method signatures too noisy, keep trace on result structs as an unexported or exported `Trace EinoTrace` field and omit it from JSON DTOs.

- [ ] **Step 4: Write trace artifacts**

In `AnalyzeIssue`, `ReviewOutcome`, and `SummarizeOutcome` activities, call `recordArtifact` after successful provider parsing:

```go
traceData := map[string]any{
	"reasoning_profile_ref": trace.ReasoningProfileRef,
	"prompt_profile_ref":    trace.PromptProfileRef,
	"schema_name":           trace.SchemaName,
	"schema_version":        trace.SchemaVersion,
	"provider_label":        trace.ProviderLabel,
	"model":                 trace.Model,
	"capability_mode":       trace.CapabilityMode,
	"latency_ms":            trace.LatencyMS,
	"usage": map[string]any{
		"input_tokens":  trace.InputTokens,
		"output_tokens": trace.OutputTokens,
		"total_tokens":  trace.TotalTokens,
	},
	"parsed_output": safeParsedOutput(result),
}
```

Use artifact types `analysis_trace`, `review_trace`, and `summary_trace`.

- [ ] **Step 5: Run tests**

Run:

```bash
cd server && go test -count=1 ./internal/orchestration
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/internal/orchestration/activities.go \
  server/internal/orchestration/eino_analyzer.go \
  server/internal/orchestration/activities_test.go
git commit -m "feat: project safe eino trace artifacts"
```

## Task 7: Issue Detail UI and Reason Text

**Files:**
- Modify: `packages/views/issues/components/issue-detail.tsx`
- Modify: `packages/views/issues/components/issue-detail.test.tsx`
- Modify: `packages/views/locales/en.json`
- Modify: `packages/views/locales/zh.json`

- [ ] **Step 1: Write UI tests**

In `issue-detail.test.tsx`, add a fixture with `reasoning_profile_ref: "worker-default"` and a `provider_failure_trace` artifact. Assert:

```ts
expect(screen.getByText("worker-default")).toBeInTheDocument();
expect(screen.getByText(/Provider timed out/i)).toBeInTheDocument();
expect(screen.queryByText(/raw_prompt/i)).not.toBeInTheDocument();
expect(screen.queryByText(/raw_response/i)).not.toBeInTheDocument();
```

- [ ] **Step 2: Run UI test and verify failure**

Run:

```bash
pnpm --filter @multica/views exec vitest run issues/components/issue-detail.test.tsx
```

Expected: FAIL because UI does not render reasoning profile / reason text yet.

- [ ] **Step 3: Add reason-code mapping**

In `issue-detail.tsx`, add local mapping or use locale keys:

```ts
const ORCHESTRATION_REASON_LABELS: Record<string, string> = {
  provider_incompatible: "Provider configuration is incompatible",
  provider_auth_failed: "Provider authentication failed",
  provider_timeout: "Provider timed out",
  provider_rate_limited: "Provider rate limited the request",
  provider_unavailable: "Provider is unavailable",
  eino_output_malformed: "Provider returned malformed structured output",
};

function formatReasonLabel(code: string): string {
  return ORCHESTRATION_REASON_LABELS[code] ?? code;
}
```

If this file already uses i18n helpers, add equivalent `en` and `zh` locale keys instead of a hard-coded map.

- [ ] **Step 4: Render safe trace metadata**

In the orchestration panel:

- Show `plan.reasoning_profile_ref` near plan metadata.
- For trace artifacts, show `provider_label`, `model`, `schema_name`, `latency_ms`, and usage counters when present.
- Never render keys named `raw_prompt`, `raw_response`, `api_key`, or `authorization`.

- [ ] **Step 5: Run tests**

Run:

```bash
pnpm --filter @multica/views exec vitest run issues/components/issue-detail.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add packages/views/issues/components/issue-detail.tsx \
  packages/views/issues/components/issue-detail.test.tsx \
  packages/views/locales/en.json \
  packages/views/locales/zh.json
git commit -m "feat: show eino reasoning trace in issue detail"
```

## Task 8: Live Provider Smoke and Checkout Docs

**Files:**
- Modify: `server/internal/orchestration/eino_analyzer_test.go`
- Modify: `server/internal/orchestration/checkout_doc_test.go`
- Modify: `docs/temporal-orchestration-mvp-checkout.md`

- [ ] **Step 1: Expand live smoke test**

Rename `TestEinoIssueAnalyzerLiveProviderSmoke` to:

```go
func TestEinoReasonerLiveProviderSmoke(t *testing.T)
```

The test must:

1. Require `ORCHESTRATION_EINO_LIVE_TEST=1`.
2. Require `ORCHESTRATION_EINO_API_KEY` and `ORCHESTRATION_EINO_MODEL`.
3. Create a production `EinoReasoner`.
4. Call `AnalyzeIssue` with a synthetic issue.
5. Call `ReviewOutcome` with synthetic validation/result evidence.
6. Call `SummarizeOutcome` with the review result.
7. Assert required fields are non-empty.
8. Assert no authoritative fields are accepted by parser tests.

- [ ] **Step 2: Run skipped path**

Run:

```bash
cd server && go test -count=1 ./internal/orchestration -run TestEinoReasonerLiveProviderSmoke -v
```

Expected: PASS with skip message unless `ORCHESTRATION_EINO_LIVE_TEST=1` is set.

- [ ] **Step 3: Run live path when provider env is available**

Run:

```bash
cd server && set -a; source ../.env; set +a; ORCHESTRATION_EINO_LIVE_TEST=1 go test -count=1 ./internal/orchestration -run TestEinoReasonerLiveProviderSmoke -v
```

Expected: PASS against configured provider. If env is not present, record "not run: missing provider env" in implementation notes.

- [ ] **Step 4: Update checkout doc**

In `docs/temporal-orchestration-mvp-checkout.md`, replace analyze-only language with:

```md
Eino live provider smoke test:

```bash
cd server && set -a; source ../.env; set +a; ORCHESTRATION_EINO_LIVE_TEST=1 go test -count=1 ./internal/orchestration -run TestEinoReasonerLiveProviderSmoke -v
```

This smoke test verifies AnalyzeIssue, ReviewOutcome, and SummarizeOutcome against the real worker-scoped Eino provider path using a synthetic fixture. It does not start Temporal, connect to the database, create an Issue, dispatch an Agent Task, or verify browser UI.
```

- [ ] **Step 5: Update doc tests**

Update `checkout_doc_test.go` expected strings to include:

- `ORCHESTRATION_EINO_ALLOW_STATIC`
- `TestEinoReasonerLiveProviderSmoke`
- "AnalyzeIssue, ReviewOutcome, and SummarizeOutcome"

- [ ] **Step 6: Run docs and orchestration tests**

Run:

```bash
cd server && go test -count=1 ./internal/orchestration
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add server/internal/orchestration/eino_analyzer_test.go \
  server/internal/orchestration/checkout_doc_test.go \
  docs/temporal-orchestration-mvp-checkout.md
git commit -m "test: expand eino live provider smoke coverage"
```

## Final Verification

Run the focused bundle:

```bash
cd server && go test -count=1 ./internal/orchestration
cd server && go test -count=1 ./internal/service
cd server && go test -count=1 ./internal/handler -run 'Test(StartIssueOrchestration|CompleteLinkedAgentTask|ApproveOrchestration|CancelOrchestration|FinalizeWorkflow.*Attention)'
pnpm --filter @multica/core exec vitest run api/schema.test.ts
pnpm --filter @multica/views exec vitest run issues/components/issue-detail.test.tsx
git diff --check
```

When provider env is available, also run:

```bash
cd server && set -a; source ../.env; set +a; ORCHESTRATION_EINO_LIVE_TEST=1 go test -count=1 ./internal/orchestration -run TestEinoReasonerLiveProviderSmoke -v
```

## Self-Review Checklist

- [ ] `CONTEXT.md` terms map to implementation behavior.
- [ ] ADR 0020 remains accurate after implementation.
- [ ] No production path silently uses `StaticEinoReasoner`.
- [ ] Analyze, review, and summarize all use the configured provider.
- [ ] Review output cannot set terminal plan status.
- [ ] Summary failure cannot be skipped to complete the run.
- [ ] Automatic evidence retry does not call review/summarize.
- [ ] Provider failure is visible in plan/node/event/artifact/attention.
- [ ] Eino traces do not persist raw prompt, raw response, API key, raw logs, or raw diffs.
- [ ] Historical plans parse as `legacy/default`.
- [ ] New runs bind `worker-default` or configured profile ref in workflow input and plan projection.
- [ ] Frontend schemas preserve safe trace metadata and default missing profile refs.
- [ ] UI shows human-readable reason labels plus stable reason code detail.
