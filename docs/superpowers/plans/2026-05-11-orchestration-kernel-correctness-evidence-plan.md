# Orchestration Kernel Correctness and Evidence Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden Multica's orchestration kernel so plan/node/task state transitions, retry attempts, events, feature-flag compatibility, structured result validation, and evidence evaluation are explicit and tested.

**Architecture:** Keep the existing `TaskService -> Orchestrator -> agent_task_queue` architecture. Add focused service-layer units for result validation, artifact normalization, and hard-check evaluation; keep the orchestrator as the transaction coordinator that applies evaluation decisions and writes events.

**Tech Stack:** Go 1.26.1, pgx/sqlc, Chi handlers, Cobra CLI, Vitest/React Testing Library, TanStack Query, zod API schemas.

---

## File Structure

### Server Service Layer

- Create `server/internal/service/orchestration_result.go`
  - Owns orchestration result schema parsing, validation, compatibility handling, artifact normalization, and test-result helpers.
  - Moves logic currently embedded in `orchestrator.go`: `AgentResultArtifact`, `AgentStructuredResult`, `legacyTaskPayload`, `parseAgentResult`, `normalizedArtifacts`, `testResultFailed`.

- Create `server/internal/service/orchestration_evaluator.go`
  - Owns `Evaluator`, `EvaluationInput`, `EvaluationResult`, `HardCheckEvaluator`.
  - Replaces loose `hardCheckNodeResult` / `shouldWaitForHuman` with explicit evaluation results.

- Modify `server/internal/service/orchestrator.go`
  - Uses `ParseAgentResultPayload`, `NormalizeArtifacts`, and `HardCheckEvaluator`.
  - Writes standardized events: `node.evaluating`, `evaluation.passed`, `evaluation.failed`, `evaluation.invalid_result`, `artifact.recorded`, `plan.failed`, `plan.waiting_human`.
  - Adds attempt metadata to `node.dispatched` event.
  - Ensures retry and waiting-human decisions come from evaluator result.

- Modify `server/internal/service/task.go`
  - Keeps non-orchestration task completion behavior unchanged.
  - Adds orchestration failure callback when `FailTask` is called on orchestration tasks.

- Create or modify `server/internal/service/orchestration_result_test.go`
  - Unit tests for result validation, legacy compatibility mode, artifact normalization, status handling, confidence bounds, and criteria evidence validation.

- Create or modify `server/internal/service/orchestration_evaluator_test.go`
  - Unit tests for node-type evidence requirements and evaluator decisions.

- Modify `server/internal/service/orchestrator_test.go`
  - Keep existing tests.
  - Adjust tests that expected empty artifact type to become `summary`; structured protocol now rejects empty type, while legacy compatibility remains separate.

### Database Queries

- Modify `server/pkg/db/queries/orchestration.sql`
  - Add update queries that return updated rows when attempt count or status is needed in event payloads.
  - Add cancel-node query only if needed for plan cancellation behavior.

- Regenerate `server/pkg/db/generated/orchestration.sql.go`
  - Run `make sqlc`.

### Handler and CLI

- Modify `server/internal/handler/daemon.go`
  - Preserve `TaskCompleteRequest.Result`.
  - Ensure handler forwards raw `result` exactly when present.
  - Keep legacy `{output: ...}` behavior when `result` is absent.

- Modify `server/cmd/multica/cmd_task.go`
  - Keep `multica task complete --task-id <id> --result <file>`.
  - Add local validation that result file contains a JSON object, not an array/string.

- Add `server/cmd/multica/cmd_task_test.go`
  - Tests local CLI validation for missing task ID, missing result, invalid JSON, non-object JSON.

### Frontend/Core

- Modify `packages/core/api/schemas.ts`
  - Add optional defensive fields if server begins returning validation/evaluator metadata in event payloads.
  - Keep `.loose()` and fallback behavior.

- Modify `packages/views/issues/components/issue-detail.tsx`
  - Display `evaluation.invalid_result`, `evaluation.failed`, and `evaluation.waiting_human` reason when present.
  - Keep UI compact; no graph view.

- Modify `packages/views/issues/components/issue-detail.test.tsx`
  - Add tests for evaluator/validation reason rendering and action button state.

---

## Task 1: Add Result Protocol Unit Tests

**Files:**
- Create: `server/internal/service/orchestration_result_test.go`
- Later Modify: `server/internal/service/orchestration_result.go`

- [ ] **Step 1: Write failing validation tests**

Create `server/internal/service/orchestration_result_test.go` with:

```go
package service

import (
	"strings"
	"testing"
)

func TestParseAgentResultPayloadStructuredValid(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{
		"status":"completed",
		"summary":"Implemented kernel evidence checks.",
		"changed_files":["server/internal/service/orchestrator.go"],
		"criteria_evidence":[{"criterion":"has evidence","evidence":"changed_files present"}],
		"confidence":0.8
	}`), ResultParseOptions{AllowLegacyCompatibility: true})

	if !validation.Valid {
		t.Fatalf("expected valid result, got errors: %#v", validation.Errors)
	}
	if validation.CompatibilityMode {
		t.Fatal("structured payload must not use compatibility mode")
	}
	if validation.Result.Status != "completed" || validation.Result.Summary == "" {
		t.Fatalf("unexpected result: %#v", validation.Result)
	}
}

func TestParseAgentResultPayloadRejectsUnknownArtifactType(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{
		"status":"completed",
		"summary":"done",
		"artifacts":[{"type":"mystery","content":{}}],
		"criteria_evidence":[{"criterion":"c","evidence":"e"}]
	}`), ResultParseOptions{AllowLegacyCompatibility: true})

	if validation.Valid {
		t.Fatal("unknown artifact type should be invalid")
	}
	if !hasValidationCode(validation.Errors, "unknown_artifact_type") {
		t.Fatalf("expected unknown_artifact_type, got %#v", validation.Errors)
	}
}

func TestParseAgentResultPayloadRejectsInvalidConfidence(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{
		"status":"completed",
		"summary":"done",
		"confidence":1.2,
		"criteria_evidence":[{"criterion":"c","evidence":"e"}]
	}`), ResultParseOptions{AllowLegacyCompatibility: true})

	if validation.Valid {
		t.Fatal("confidence above 1 should be invalid")
	}
	if !hasValidationCode(validation.Errors, "invalid_confidence") {
		t.Fatalf("expected invalid_confidence, got %#v", validation.Errors)
	}
}

func TestParseAgentResultPayloadRejectsCompletedWithoutSummary(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{
		"status":"completed",
		"changed_files":["a.go"],
		"criteria_evidence":[{"criterion":"c","evidence":"e"}]
	}`), ResultParseOptions{AllowLegacyCompatibility: true})

	if validation.Valid {
		t.Fatal("completed result without summary should be invalid")
	}
	if !hasValidationCode(validation.Errors, "missing_summary") {
		t.Fatalf("expected missing_summary, got %#v", validation.Errors)
	}
}

func TestParseAgentResultPayloadLegacyCompatibility(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{"output":"legacy summary"}`), ResultParseOptions{AllowLegacyCompatibility: true})

	if !validation.Valid {
		t.Fatalf("legacy output should parse in compatibility mode: %#v", validation.Errors)
	}
	if !validation.CompatibilityMode {
		t.Fatal("expected compatibility mode")
	}
	if validation.Result.Status != "completed" || validation.Result.Summary != "legacy summary" {
		t.Fatalf("unexpected legacy result: %#v", validation.Result)
	}
}

func TestParseAgentResultPayloadRejectsLegacyWhenDisabled(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{"output":"legacy summary"}`), ResultParseOptions{AllowLegacyCompatibility: false})

	if validation.Valid {
		t.Fatal("legacy output should be invalid when compatibility is disabled")
	}
	if !hasValidationCode(validation.Errors, "missing_status") {
		t.Fatalf("expected missing_status, got %#v", validation.Errors)
	}
}

func TestParseAgentResultPayloadNeedsHumanRequiresNextActionOrRisk(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{
		"status":"needs_human",
		"summary":"Need product confirmation."
	}`), ResultParseOptions{AllowLegacyCompatibility: true})

	if validation.Valid {
		t.Fatal("needs_human without next_actions or risks should be invalid")
	}
	if !hasValidationCode(validation.Errors, "missing_human_followup") {
		t.Fatalf("expected missing_human_followup, got %#v", validation.Errors)
	}
}

func TestNormalizeArtifactsRejectsEmptyTypeForStructuredPayload(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{
		"status":"completed",
		"summary":"done",
		"artifacts":[{"content":{}}],
		"criteria_evidence":[{"criterion":"c","evidence":"e"}]
	}`), ResultParseOptions{AllowLegacyCompatibility: true})

	if validation.Valid {
		t.Fatal("empty artifact type should be invalid")
	}
	if !hasValidationCode(validation.Errors, "missing_artifact_type") {
		t.Fatalf("expected missing_artifact_type, got %#v", validation.Errors)
	}
}

func hasValidationCode(errors []ValidationError, code string) bool {
	for _, err := range errors {
		if err.Code == code || strings.Contains(err.Message, code) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
cd server && go test ./internal/service -run 'TestParseAgentResultPayload|TestNormalizeArtifactsRejectsEmptyType' -count=1
```

Expected: FAIL because `ParseAgentResultPayload`, `ResultParseOptions`, and `ValidationError` do not exist yet.

- [ ] **Step 3: Commit failing tests**

```bash
git add server/internal/service/orchestration_result_test.go
git commit -m "test(orchestration): specify structured result validation"
```

---

## Task 2: Implement Result Protocol Parser and Normalizer

**Files:**
- Create: `server/internal/service/orchestration_result.go`
- Modify: `server/internal/service/orchestrator.go`
- Modify: `server/internal/service/orchestrator_test.go`

- [ ] **Step 1: Add result protocol implementation**

Create `server/internal/service/orchestration_result.go`:

```go
package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

var allowedResultStatuses = map[string]bool{
	"completed":   true,
	"failed":      true,
	"blocked":     true,
	"needs_human": true,
}

var allowedArtifactTypes = map[string]bool{
	"diff":           true,
	"file":           true,
	"log":            true,
	"test_result":    true,
	"pr":             true,
	"decision":       true,
	"review_result":  true,
	"command_output": true,
	"summary":        true,
}

type ResultParseOptions struct {
	AllowLegacyCompatibility bool
}

type ValidationError struct {
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

type ResultValidation struct {
	Valid             bool                  `json:"valid"`
	Result            AgentStructuredResult `json:"-"`
	Errors            []ValidationError     `json:"errors,omitempty"`
	CompatibilityMode bool                  `json:"compatibility_mode"`
}

type AgentResultArtifact struct {
	Type     string          `json:"type"`
	URI      string          `json:"uri,omitempty"`
	Content  json.RawMessage `json:"content,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type AgentStructuredResult struct {
	Status           string                `json:"status"`
	Summary          string                `json:"summary"`
	Artifacts        []AgentResultArtifact `json:"artifacts"`
	ChangedFiles     []string              `json:"changed_files"`
	TestResult       json.RawMessage       `json:"test_result"`
	Claims           []string              `json:"claims"`
	CriteriaEvidence []CriteriaEvidence    `json:"criteria_evidence"`
	Risks            []string              `json:"risks"`
	NextActions      []string              `json:"next_actions"`
	Confidence       float64               `json:"confidence"`
}

type CriteriaEvidence struct {
	Criterion    string   `json:"criterion"`
	Evidence     string   `json:"evidence"`
	ArtifactRefs []string `json:"artifact_refs,omitempty"`
}

type legacyTaskPayload struct {
	Output string `json:"output"`
}

func ParseAgentResultPayload(raw []byte, opts ResultParseOptions) ResultValidation {
	var validation ResultValidation
	if len(raw) == 0 {
		validation.Errors = append(validation.Errors, validationError("empty_payload", "", "result payload is empty"))
		return validation
	}

	var result AgentStructuredResult
	if err := json.Unmarshal(raw, &result); err != nil {
		validation.Errors = append(validation.Errors, validationError("invalid_json", "", err.Error()))
		return validation
	}

	if result.Status == "" && opts.AllowLegacyCompatibility {
		var legacy legacyTaskPayload
		if err := json.Unmarshal(raw, &legacy); err == nil && strings.TrimSpace(legacy.Output) != "" {
			result.Status = "completed"
			result.Summary = strings.TrimSpace(legacy.Output)
			validation.CompatibilityMode = true
		}
	}

	validation.Result = result
	validation.Errors = validateAgentStructuredResult(result)
	validation.Valid = len(validation.Errors) == 0
	return validation
}

func validateAgentStructuredResult(result AgentStructuredResult) []ValidationError {
	var errs []ValidationError
	status := strings.TrimSpace(result.Status)
	if status == "" {
		errs = append(errs, validationError("missing_status", "status", "status is required"))
	} else if !allowedResultStatuses[status] {
		errs = append(errs, validationError("invalid_status", "status", fmt.Sprintf("unsupported status %q", status)))
	}
	if status == "completed" && strings.TrimSpace(result.Summary) == "" {
		errs = append(errs, validationError("missing_summary", "summary", "completed result requires summary"))
	}
	if result.Confidence < 0 || result.Confidence > 1 {
		errs = append(errs, validationError("invalid_confidence", "confidence", "confidence must be between 0 and 1"))
	}
	for i, artifact := range result.Artifacts {
		field := fmt.Sprintf("artifacts[%d].type", i)
		if strings.TrimSpace(artifact.Type) == "" {
			errs = append(errs, validationError("missing_artifact_type", field, "artifact type is required"))
			continue
		}
		if !allowedArtifactTypes[artifact.Type] {
			errs = append(errs, validationError("unknown_artifact_type", field, fmt.Sprintf("unsupported artifact type %q", artifact.Type)))
		}
	}
	if status == "failed" && strings.TrimSpace(result.Summary) == "" && len(result.Risks) == 0 {
		errs = append(errs, validationError("missing_failure_summary", "summary", "failed result requires summary or risks"))
	}
	if status == "blocked" && strings.TrimSpace(result.Summary) == "" && len(result.Risks) == 0 {
		errs = append(errs, validationError("missing_blocked_summary", "summary", "blocked result requires summary or risks"))
	}
	if status == "needs_human" && (strings.TrimSpace(result.Summary) == "" || (len(result.NextActions) == 0 && len(result.Risks) == 0)) {
		errs = append(errs, validationError("missing_human_followup", "next_actions", "needs_human result requires summary and next_actions or risks"))
	}
	return errs
}

func validationError(code, field, message string) ValidationError {
	return ValidationError{Code: code, Field: field, Message: message}
}

func NormalizeArtifacts(result AgentStructuredResult) []AgentResultArtifact {
	artifacts := append([]AgentResultArtifact{}, result.Artifacts...)
	if len(result.ChangedFiles) > 0 {
		content, _ := json.Marshal(map[string]any{"changed_files": result.ChangedFiles})
		artifacts = append(artifacts, AgentResultArtifact{Type: "diff", Content: content})
	}
	if len(result.TestResult) > 0 {
		artifacts = append(artifacts, AgentResultArtifact{Type: "test_result", Content: result.TestResult})
	}
	if strings.TrimSpace(result.Summary) != "" {
		content, _ := json.Marshal(map[string]string{"summary": result.Summary})
		artifacts = append(artifacts, AgentResultArtifact{Type: "summary", Content: content})
	}
	return artifacts
}

func hasArtifactType(artifacts []AgentResultArtifact, artifactType string) bool {
	for _, artifact := range artifacts {
		if artifact.Type == artifactType {
			return true
		}
	}
	return false
}

func testResultFailed(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var result struct {
		Status  string `json:"status"`
		Passed  *bool  `json:"passed"`
		Success *bool  `json:"success"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return false
	}
	if strings.EqualFold(result.Status, "failed") || strings.EqualFold(result.Status, "failure") {
		return true
	}
	if result.Passed != nil && !*result.Passed {
		return true
	}
	return result.Success != nil && !*result.Success
}

func criteriaEvidenceValid(items []CriteriaEvidence) bool {
	for _, item := range items {
		if strings.TrimSpace(item.Criterion) == "" || strings.TrimSpace(item.Evidence) == "" {
			return false
		}
	}
	return len(items) > 0
}
```

- [ ] **Step 2: Remove moved types and helpers from orchestrator**

In `server/internal/service/orchestrator.go`, delete the duplicated definitions for:

```go
type AgentResultArtifact struct { ... }
type AgentStructuredResult struct { ... }
type legacyTaskPayload struct { ... }
func parseAgentResult(raw []byte) AgentStructuredResult { ... }
func hasArtifactType(...)
func testResultFailed(...)
func normalizedArtifacts(...)
```

Replace current parser calls:

```go
result := parseAgentResult(rawResult)
```

with:

```go
validation := ParseAgentResultPayload(rawResult, ResultParseOptions{AllowLegacyCompatibility: true})
result := validation.Result
```

Replace current artifact normalization:

```go
for _, artifact := range normalizedArtifacts(result) {
```

with:

```go
for _, artifact := range NormalizeArtifacts(result) {
```

- [ ] **Step 3: Update tests that still call old helper names**

In `server/internal/service/orchestrator_test.go`, replace:

```go
artifacts := normalizedArtifacts(result)
```

with:

```go
artifacts := NormalizeArtifacts(result)
```

Replace `parseAgentResult(...)` tests with `ParseAgentResultPayload(...)`.

Delete or rewrite `TestNormalizedArtifactsEmptyType` because empty artifact type is now invalid at validation time. Use this replacement:

```go
func TestEmptyArtifactTypeInvalidInStructuredResult(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{
		"status":"completed",
		"summary":"done",
		"artifacts":[{"content":{}}],
		"criteria_evidence":[{"criterion":"c","evidence":"e"}]
	}`), ResultParseOptions{AllowLegacyCompatibility: true})
	if validation.Valid {
		t.Fatal("empty artifact type should be invalid")
	}
}
```

- [ ] **Step 4: Run result protocol tests**

Run:

```bash
cd server && go test ./internal/service -run 'TestParseAgentResultPayload|TestNormalizeArtifacts|TestEmptyArtifactType|TestTestResultFailed' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/service/orchestration_result.go server/internal/service/orchestration_result_test.go server/internal/service/orchestrator.go server/internal/service/orchestrator_test.go
git commit -m "feat(orchestration): add structured result protocol"
```

---

## Task 3: Add HardCheckEvaluator Tests

**Files:**
- Create: `server/internal/service/orchestration_evaluator_test.go`
- Later Create: `server/internal/service/orchestration_evaluator.go`

- [ ] **Step 1: Write failing evaluator tests**

Create `server/internal/service/orchestration_evaluator_test.go`:

```go
package service

import (
	"context"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestHardCheckEvaluatorInvalidValidationFails(t *testing.T) {
	evaluator := HardCheckEvaluator{}
	eval, err := evaluator.Evaluate(context.Background(), EvaluationInput{
		Node: db.OrchestrationNode{Type: "implement"},
		Validation: ResultValidation{
			Valid:  false,
			Errors: []ValidationError{{Code: "missing_status", Field: "status", Message: "status is required"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if eval.Pass || eval.RecommendedAction != "retry" || eval.Reason != "invalid_result" {
		t.Fatalf("unexpected eval: %#v", eval)
	}
}

func TestHardCheckEvaluatorImplementRequiresEvidence(t *testing.T) {
	evaluator := HardCheckEvaluator{}
	eval, err := evaluator.Evaluate(context.Background(), EvaluationInput{
		Node:       db.OrchestrationNode{Type: "implement"},
		Validation: ResultValidation{Valid: true},
		Result: AgentStructuredResult{
			Status:           "completed",
			Summary:          "done",
			CriteriaEvidence: []CriteriaEvidence{{Criterion: "c", Evidence: "e"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if eval.Pass || eval.Reason != "missing_implementation_artifact" {
		t.Fatalf("unexpected eval: %#v", eval)
	}
}

func TestHardCheckEvaluatorTestRequiresPassingTestResult(t *testing.T) {
	evaluator := HardCheckEvaluator{}
	eval, err := evaluator.Evaluate(context.Background(), EvaluationInput{
		Node:       db.OrchestrationNode{Type: "test"},
		Validation: ResultValidation{Valid: true},
		Result: AgentStructuredResult{
			Status:           "completed",
			Summary:          "verified",
			TestResult:       []byte(`{"passed":false}`),
			CriteriaEvidence: []CriteriaEvidence{{Criterion: "tests", Evidence: "go test failed"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if eval.Pass || eval.Reason != "test_result_failed" {
		t.Fatalf("unexpected eval: %#v", eval)
	}
}

func TestHardCheckEvaluatorNeedsHuman(t *testing.T) {
	evaluator := HardCheckEvaluator{}
	eval, err := evaluator.Evaluate(context.Background(), EvaluationInput{
		Node:       db.OrchestrationNode{Type: "implement"},
		Validation: ResultValidation{Valid: true},
		Result: AgentStructuredResult{
			Status:      "needs_human",
			Summary:     "Need scope confirmation.",
			NextActions: []string{"Confirm whether tests are required."},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if eval.Pass || eval.RecommendedAction != "ask_human" || eval.Reason != "agent_needs_human" {
		t.Fatalf("unexpected eval: %#v", eval)
	}
}

func TestHardCheckEvaluatorAcceptanceCriteriaRequireEvidence(t *testing.T) {
	evaluator := HardCheckEvaluator{}
	eval, err := evaluator.Evaluate(context.Background(), EvaluationInput{
		Node:       db.OrchestrationNode{Type: "implement"},
		Validation: ResultValidation{Valid: true},
		AcceptanceCriteria: []AcceptanceCriterion{
			{Criterion: "must include tests"},
		},
		Result: AgentStructuredResult{
			Status:       "completed",
			Summary:      "done",
			ChangedFiles: []string{"a.go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if eval.Pass || eval.Reason != "missing_criteria_evidence" {
		t.Fatalf("unexpected eval: %#v", eval)
	}
}

func TestHardCheckEvaluatorPassesImplementWithChangedFilesAndEvidence(t *testing.T) {
	evaluator := HardCheckEvaluator{}
	eval, err := evaluator.Evaluate(context.Background(), EvaluationInput{
		Node:       db.OrchestrationNode{Type: "implement"},
		Validation: ResultValidation{Valid: true},
		AcceptanceCriteria: []AcceptanceCriterion{
			{Criterion: "must include tests"},
		},
		Result: AgentStructuredResult{
			Status:           "completed",
			Summary:          "done",
			ChangedFiles:     []string{"a.go"},
			CriteriaEvidence: []CriteriaEvidence{{Criterion: "must include tests", Evidence: "go test passed"}},
			Confidence:       0.9,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !eval.Pass || eval.Reason != "hard_check_passed" {
		t.Fatalf("unexpected eval: %#v", eval)
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
cd server && go test ./internal/service -run TestHardCheckEvaluator -count=1
```

Expected: FAIL because `HardCheckEvaluator`, `EvaluationInput`, `EvaluationResult`, and `AcceptanceCriterion` do not exist.

- [ ] **Step 3: Commit failing tests**

```bash
git add server/internal/service/orchestration_evaluator_test.go
git commit -m "test(orchestration): specify hard check evaluator"
```

---

## Task 4: Implement HardCheckEvaluator

**Files:**
- Create: `server/internal/service/orchestration_evaluator.go`
- Modify: `server/internal/service/orchestrator.go`
- Modify: `server/internal/service/orchestrator_test.go`

- [ ] **Step 1: Add evaluator implementation**

Create `server/internal/service/orchestration_evaluator.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type Evaluator interface {
	Evaluate(ctx context.Context, input EvaluationInput) (EvaluationResult, error)
}

type EvaluationInput struct {
	Plan               db.OrchestrationPlan
	Node               db.OrchestrationNode
	Task               db.AgentTaskQueue
	Result             AgentStructuredResult
	Validation         ResultValidation
	AcceptanceCriteria []AcceptanceCriterion
}

type AcceptanceCriterion struct {
	Criterion string `json:"criterion"`
}

type EvaluationResult struct {
	Pass              bool     `json:"pass"`
	Score             float64  `json:"score"`
	Reason            string   `json:"reason"`
	FailedCriteria    []string `json:"failed_criteria,omitempty"`
	MissingArtifacts   []string `json:"missing_artifacts,omitempty"`
	Risks              []string `json:"risks,omitempty"`
	RecommendedAction string   `json:"recommended_action"`
}

type HardCheckEvaluator struct{}

func (HardCheckEvaluator) Evaluate(ctx context.Context, input EvaluationInput) (EvaluationResult, error) {
	_ = ctx
	result := input.Result
	if !input.Validation.Valid {
		return EvaluationResult{Pass: false, Reason: "invalid_result", RecommendedAction: "retry", Score: 0}, nil
	}
	switch result.Status {
	case "failed":
		return EvaluationResult{Pass: false, Reason: "agent_reported_failed", RecommendedAction: "retry", Risks: result.Risks}, nil
	case "blocked":
		return EvaluationResult{Pass: false, Reason: "agent_reported_blocked", RecommendedAction: "ask_human", Risks: result.Risks}, nil
	case "needs_human":
		return EvaluationResult{Pass: false, Reason: "agent_needs_human", RecommendedAction: "ask_human", Risks: result.Risks}, nil
	}
	if strings.TrimSpace(result.Summary) == "" {
		return EvaluationResult{Pass: false, Reason: "missing_summary", RecommendedAction: "retry"}, nil
	}
	if testResultFailed(result.TestResult) {
		return EvaluationResult{Pass: false, Reason: "test_result_failed", RecommendedAction: "retry"}, nil
	}
	if failed := missingCriteriaEvidence(input.AcceptanceCriteria, result.CriteriaEvidence); len(failed) > 0 {
		return EvaluationResult{Pass: false, Reason: "missing_criteria_evidence", FailedCriteria: failed, RecommendedAction: "retry"}, nil
	}
	switch input.Node.Type {
	case "implement", "fix":
		if len(result.ChangedFiles) == 0 && !hasArtifactType(result.Artifacts, "diff") && !hasArtifactType(result.Artifacts, "file") {
			return EvaluationResult{Pass: false, Reason: "missing_implementation_artifact", MissingArtifacts: []string{"diff", "file"}, RecommendedAction: "retry"}, nil
		}
	case "test":
		if len(result.TestResult) == 0 && !hasArtifactType(result.Artifacts, "test_result") {
			return EvaluationResult{Pass: false, Reason: "missing_test_result", MissingArtifacts: []string{"test_result"}, RecommendedAction: "retry"}, nil
		}
	case "review":
		if !hasArtifactType(result.Artifacts, "review_result") {
			return EvaluationResult{Pass: false, Reason: "missing_review_result", MissingArtifacts: []string{"review_result"}, RecommendedAction: "retry"}, nil
		}
	case "design":
		if !hasArtifactType(result.Artifacts, "decision") && len(result.CriteriaEvidence) == 0 {
			return EvaluationResult{Pass: false, Reason: "missing_design_evidence", MissingArtifacts: []string{"decision"}, RecommendedAction: "retry"}, nil
		}
	}
	return EvaluationResult{Pass: true, Reason: "hard_check_passed", RecommendedAction: "complete", Score: result.Confidence}, nil
}

func missingCriteriaEvidence(criteria []AcceptanceCriterion, evidence []CriteriaEvidence) []string {
	if len(criteria) == 0 {
		if len(evidence) == 0 {
			return []string{"node_objective"}
		}
		if !criteriaEvidenceValid(evidence) {
			return []string{"node_objective"}
		}
		return nil
	}
	if !criteriaEvidenceValid(evidence) {
		out := make([]string, 0, len(criteria))
		for _, criterion := range criteria {
			out = append(out, criterion.Criterion)
		}
		return out
	}
	evidenceByCriterion := map[string]bool{}
	for _, item := range evidence {
		evidenceByCriterion[strings.TrimSpace(item.Criterion)] = strings.TrimSpace(item.Evidence) != ""
	}
	var missing []string
	for _, criterion := range criteria {
		key := strings.TrimSpace(criterion.Criterion)
		if key == "" {
			continue
		}
		if !evidenceByCriterion[key] {
			missing = append(missing, key)
		}
	}
	return missing
}

func ParseAcceptanceCriteria(raw json.RawMessage) []AcceptanceCriterion {
	if len(raw) == 0 {
		return nil
	}
	var objects []AcceptanceCriterion
	if err := json.Unmarshal(raw, &objects); err == nil {
		return objects
	}
	var stringsOnly []string
	if err := json.Unmarshal(raw, &stringsOnly); err == nil {
		out := make([]AcceptanceCriterion, 0, len(stringsOnly))
		for _, item := range stringsOnly {
			out = append(out, AcceptanceCriterion{Criterion: item})
		}
		return out
	}
	return nil
}
```

- [ ] **Step 2: Replace old hard check calls**

In `server/internal/service/orchestrator.go`, replace:

```go
pass, reason := hardCheckNodeResult(node, result)
waitHuman := !pass && shouldWaitForHuman(result)
```

with:

```go
eval, err := (HardCheckEvaluator{}).Evaluate(ctx, EvaluationInput{
	Plan:               plan,
	Node:               node,
	Task:               task,
	Result:             result,
	Validation:         validation,
	AcceptanceCriteria: ParseAcceptanceCriteria(taskCtx.AcceptanceCriteria),
})
if err != nil {
	return err
}
pass := eval.Pass
reason := eval.Reason
waitHuman := !pass && eval.RecommendedAction == "ask_human"
```

Remove `hardCheckNodeResult` and `shouldWaitForHuman` from `orchestrator.go`.

- [ ] **Step 3: Update evaluator payload**

In `OnTaskCompleted`, update payload from:

```go
payload := mustJSON(map[string]any{
	"pass":    pass,
	"reason":  reason,
	"summary": result.Summary,
})
```

to:

```go
payload := mustJSON(map[string]any{
	"pass":               eval.Pass,
	"reason":             eval.Reason,
	"summary":            result.Summary,
	"recommended_action": eval.RecommendedAction,
	"validation_errors":  validation.Errors,
	"compatibility_mode": validation.CompatibilityMode,
})
```

- [ ] **Step 4: Update old hard check tests**

In `server/internal/service/orchestrator_test.go`, remove `TestHardCheckNodeResult` and `TestShouldWaitForHuman`, because the new canonical tests live in `orchestration_evaluator_test.go`.

- [ ] **Step 5: Run evaluator tests**

Run:

```bash
cd server && go test ./internal/service -run 'TestHardCheckEvaluator|TestParseAcceptanceCriteria' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/internal/service/orchestration_evaluator.go server/internal/service/orchestration_evaluator_test.go server/internal/service/orchestrator.go server/internal/service/orchestrator_test.go
git commit -m "feat(orchestration): extract hard check evaluator"
```

---

## Task 5: Add Attempt Count and Event Completeness Tests

**Files:**
- Modify: `server/internal/service/orchestrator_test.go`
- Modify: `server/internal/handler/handler_test.go`

- [ ] **Step 1: Add service-level event payload test**

Append to `server/internal/service/orchestrator_test.go`:

```go
func TestNodeDispatchedEventPayloadIncludesAttemptMetadata(t *testing.T) {
	payload := nodeDispatchedPayload("task-1", "run-1", 1, 2)
	if payload["task_id"] != "task-1" {
		t.Fatalf("missing task_id: %#v", payload)
	}
	if payload["run_id"] != "run-1" {
		t.Fatalf("missing run_id: %#v", payload)
	}
	if payload["attempt_count"] != 1 {
		t.Fatalf("missing attempt_count: %#v", payload)
	}
	if payload["max_attempts"] != 2 {
		t.Fatalf("missing max_attempts: %#v", payload)
	}
}
```

- [ ] **Step 2: Add integration event assertions**

In `server/internal/handler/handler_test.go`, find the existing orchestration kernel test around `"Exercise orchestration kernel from issue create"`. After task completion and event reload, add assertions equivalent to:

```go
eventTypes := map[string]bool{}
for _, event := range events {
	eventTypes[event.EventType] = true
}
for _, required := range []string{
	"plan.created",
	"node.created",
	"node.dispatched",
	"node.running",
	"node.evaluating",
	"task.completed",
	"evaluation.failed",
	"node.retry_scheduled",
} {
	if !eventTypes[required] {
		t.Fatalf("missing orchestration event %q; got %#v", required, eventTypes)
	}
}
```

For the success path, assert:

```go
for _, required := range []string{
	"evaluation.passed",
	"node.completed",
	"plan.completed",
} {
	if !eventTypes[required] {
		t.Fatalf("missing orchestration event %q; got %#v", required, eventTypes)
	}
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
cd server && go test ./internal/service ./internal/handler -run 'TestNodeDispatchedEventPayloadIncludesAttemptMetadata|Test.*orchestration|Test.*Orchestration' -count=1
```

Expected: FAIL because `nodeDispatchedPayload` and new event types are not implemented.

- [ ] **Step 4: Commit failing tests**

```bash
git add server/internal/service/orchestrator_test.go server/internal/handler/handler_test.go
git commit -m "test(orchestration): require attempt metadata and audit events"
```

---

## Task 6: Implement Attempt Metadata and Audit Events

**Files:**
- Modify: `server/internal/service/orchestrator.go`
- Modify: `server/pkg/db/queries/orchestration.sql`
- Regenerate: `server/pkg/db/generated/orchestration.sql.go`

- [ ] **Step 1: Add sqlc query for dispatch returning node**

In `server/pkg/db/queries/orchestration.sql`, replace:

```sql
-- name: MarkOrchestrationNodeDispatched :exec
UPDATE orchestration_node
SET status = 'dispatched', attempt_count = attempt_count + 1, updated_at = now()
WHERE id = $1;
```

with:

```sql
-- name: MarkOrchestrationNodeDispatched :one
UPDATE orchestration_node
SET status = 'dispatched', attempt_count = attempt_count + 1, updated_at = now()
WHERE id = $1
RETURNING *;
```

- [ ] **Step 2: Regenerate sqlc**

Run:

```bash
make sqlc
```

Expected: `server/pkg/db/generated/orchestration.sql.go` updates `MarkOrchestrationNodeDispatched` to return `db.OrchestrationNode`.

- [ ] **Step 3: Add payload helper**

In `server/internal/service/orchestrator.go`, add near `mustJSON`:

```go
func nodeDispatchedPayload(taskID, runID string, attemptCount, maxAttempts int32) map[string]any {
	return map[string]any{
		"task_id":       taskID,
		"run_id":        runID,
		"attempt_count": int(attemptCount),
		"max_attempts":  int(maxAttempts),
	}
}
```

- [ ] **Step 4: Use returned node in dispatch event**

In `dispatchNodeTask`, replace:

```go
if err := qtx.MarkOrchestrationNodeDispatched(ctx, in.Node.ID); err != nil {
	return db.AgentTaskQueue{}, err
}
_, err = qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
	PlanID:    in.Plan.ID,
	NodeID:    in.Node.ID,
	TaskID:    task.ID,
	EventType: "node.dispatched",
	ActorType: "kernel",
	Payload:   mustJSON(map[string]any{"task_id": util.UUIDToString(task.ID), "run_id": util.UUIDToString(runID)}),
})
```

with:

```go
updatedNode, err := qtx.MarkOrchestrationNodeDispatched(ctx, in.Node.ID)
if err != nil {
	return db.AgentTaskQueue{}, err
}
_, err = qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
	PlanID:    in.Plan.ID,
	NodeID:    in.Node.ID,
	TaskID:    task.ID,
	EventType: "node.dispatched",
	ActorType: "kernel",
	Payload: mustJSON(nodeDispatchedPayload(
		util.UUIDToString(task.ID),
		util.UUIDToString(runID),
		updatedNode.AttemptCount,
		updatedNode.MaxAttempts,
	)),
})
```

- [ ] **Step 5: Write node running event**

In `OnTaskStarted`, after `MarkOrchestrationNodeRunning`, create `node.running` event:

```go
if err := o.runInTx(ctx, func(qtx *db.Queries) error {
	if err := qtx.MarkOrchestrationNodeRunning(ctx, nodeID); err != nil {
		return err
	}
	_, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
		PlanID:    task.OrchestrationPlanID,
		NodeID:    nodeID,
		TaskID:    task.ID,
		EventType: "node.running",
		ActorType: "kernel",
		Payload:   mustJSON(map[string]any{"task_id": util.UUIDToString(task.ID)}),
	})
	return err
}); err != nil {
	o.Logger.Warn("orchestration: failed to mark node running", "task_id", util.UUIDToString(task.ID), "error", err)
}
```

Use `task.OrchestrationPlanID` if valid; otherwise parse `taskCtx.OrchestrationPlanID`.

- [ ] **Step 6: Write evaluation and artifact events**

In `OnTaskCompleted`, after `MarkOrchestrationNodeEvaluating`, write:

```go
if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
	PlanID:    planID,
	NodeID:    nodeID,
	TaskID:    task.ID,
	EventType: "node.evaluating",
	ActorType: "kernel",
	Payload:   mustJSON(map[string]any{"task_id": util.UUIDToString(task.ID)}),
}); err != nil {
	return err
}
```

For each artifact created, capture the returned artifact and write:

```go
created, err := qtx.CreateOrchestrationArtifact(ctx, db.CreateOrchestrationArtifactParams{...})
if err != nil {
	return err
}
if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
	PlanID:    planID,
	NodeID:    nodeID,
	TaskID:    task.ID,
	EventType: "artifact.recorded",
	ActorType: "kernel",
	Payload: mustJSON(map[string]any{
		"artifact_id": util.UUIDToString(created.ID),
		"type":        created.Type,
	}),
}); err != nil {
	return err
}
```

After evaluator result, write one of:

```go
eventType := "evaluation.failed"
if validation.Valid && eval.Pass {
	eventType = "evaluation.passed"
} else if !validation.Valid {
	eventType = "evaluation.invalid_result"
} else if eval.RecommendedAction == "ask_human" {
	eventType = "evaluation.waiting_human"
}
```

- [ ] **Step 7: Write plan failed and waiting-human events**

When setting plan `waiting_human`, add `plan.waiting_human`.

When setting plan `failed`, add `plan.failed`.

Payload must include `reason`.

- [ ] **Step 8: Run tests**

Run:

```bash
cd server && go test ./internal/service ./internal/handler -run 'TestNodeDispatchedEventPayloadIncludesAttemptMetadata|Test.*orchestration|Test.*Orchestration' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add server/internal/service/orchestrator.go server/pkg/db/queries/orchestration.sql server/pkg/db/generated/orchestration.sql.go server/internal/service/orchestrator_test.go server/internal/handler/handler_test.go
git commit -m "feat(orchestration): event state transitions with attempt metadata"
```

---

## Task 7: Add Feature Flag and Retry Boundary Integration Tests

**Files:**
- Modify: `server/internal/handler/handler_test.go`
- Modify: `server/internal/service/orchestrator.go`

- [ ] **Step 1: Add feature flag off test**

In `server/internal/handler/handler_test.go`, add a test near existing orchestration handler tests:

```go
func TestIssueAssignedToAgent_OrchestrationFlagOffUsesLegacyTaskPath(t *testing.T) {
	ctx := context.Background()
	resetTestDB(t)
	testHandler := newTestHandler(t)
	seedBasicWorkspace(t, testHandler)

	issue := createIssueViaHandler(t, testHandler, map[string]any{
		"title":       "Legacy task path",
		"description": "Should not create orchestration plan.",
		"assignee_id": uuidToString(testAgentID),
	})

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   parseUUID(issue.ID),
	})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(plans) != 0 {
		t.Fatalf("expected no orchestration plans, got %d", len(plans))
	}

	tasks, err := testHandler.Queries.ListAgentTasksByIssue(ctx, parseUUID(issue.ID))
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one legacy task, got %d", len(tasks))
	}
	if _, ok := service.ParseOrchestrationTaskContext(tasks[0].Context); ok {
		t.Fatalf("legacy task should not carry orchestration context: %s", string(tasks[0].Context))
	}
}
```

Adjust helper names to the existing test helpers in this file. Do not invent new test infrastructure if equivalent helpers already exist.

- [ ] **Step 2: Add retry exhaustion test**

Add a test that creates an orchestration issue with flag on, completes the first task with an invalid result twice, and asserts no third task is created:

```go
invalidResult := []byte(`{"status":"completed","summary":"done without evidence"}`)
if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, invalidResult, "", ""); err != nil {
	t.Fatalf("complete first attempt: %v", err)
}

// Claim/start retry task, then complete with same invalid result.
if _, err := testHandler.TaskService.CompleteTask(ctx, startedRetry.ID, invalidResult, "", ""); err != nil {
	t.Fatalf("complete retry attempt: %v", err)
}

nodes, err := testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plan.ID)
if err != nil {
	t.Fatalf("list nodes: %v", err)
}
if nodes[0].AttemptCount != nodes[0].MaxAttempts {
	t.Fatalf("attempt_count=%d max_attempts=%d", nodes[0].AttemptCount, nodes[0].MaxAttempts)
}
if nodes[0].Status != "failed" {
	t.Fatalf("expected node failed after retry exhaustion, got %s", nodes[0].Status)
}
```

- [ ] **Step 3: Run tests and verify failures**

Run:

```bash
cd server && go test ./internal/handler -run 'TestIssueAssignedToAgent_OrchestrationFlagOffUsesLegacyTaskPath|Test.*Retry.*Exhaust' -count=1
```

Expected: feature flag test may already pass; retry exhaustion may fail if attempt count or max-attempt check is off.

- [ ] **Step 4: Implement minimal fixes**

If retry condition currently uses a stale pre-dispatch node, reload node before checking retry:

```go
latestNode, err := qtx.GetOrchestrationNode(ctx, nodeID)
if err != nil {
	return err
}
if latestNode.AttemptCount < latestNode.MaxAttempts {
	...
}
```

Use `latestNode` when dispatching retry task so event payload reflects the current node.

- [ ] **Step 5: Run tests**

Run:

```bash
cd server && go test ./internal/handler -run 'TestIssueAssignedToAgent_OrchestrationFlagOffUsesLegacyTaskPath|Test.*Retry.*Exhaust|Test.*Orchestration' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/internal/handler/handler_test.go server/internal/service/orchestrator.go
git commit -m "fix(orchestration): enforce feature flag and retry boundaries"
```

---

## Task 8: Add Orchestration FailTask Callback

**Files:**
- Modify: `server/internal/service/orchestrator.go`
- Modify: `server/internal/service/task.go`
- Modify: `server/internal/service/orchestrator_test.go` or `server/internal/handler/handler_test.go`

- [ ] **Step 1: Add failing test for runtime task failure**

Add an integration test that starts an orchestration task, calls `FailTask`, and asserts:

- `task.failed` event exists.
- either retry task is queued when attempts remain, or node/plan fail when exhausted.
- failed attempt consumes attempt count.

Use this assertion block:

```go
events, err := testHandler.Queries.ListOrchestrationEventsByPlan(ctx, plan.ID)
if err != nil {
	t.Fatalf("list events: %v", err)
}
eventTypes := map[string]bool{}
for _, event := range events {
	eventTypes[event.EventType] = true
}
if !eventTypes["task.failed"] {
	t.Fatalf("missing task.failed event: %#v", eventTypes)
}
if !eventTypes["node.retry_scheduled"] {
	t.Fatalf("expected retry after task failure, got events %#v", eventTypes)
}
```

- [ ] **Step 2: Run test and verify failure**

Run:

```bash
cd server && go test ./internal/handler -run 'Test.*Orchestration.*FailTask|Test.*TaskFailure.*Orchestration' -count=1
```

Expected: FAIL because `FailTask` does not notify orchestrator yet.

- [ ] **Step 3: Add orchestrator failure callback**

In `server/internal/service/orchestrator.go`, add:

```go
func (o *Orchestrator) OnTaskFailed(ctx context.Context, task db.AgentTaskQueue, failureReason string) error {
	taskCtx, ok := ParseOrchestrationTaskContext(task.Context)
	if !ok {
		return nil
	}
	planID, err := util.ParseUUID(taskCtx.OrchestrationPlanID)
	if err != nil {
		return err
	}
	nodeID, err := util.ParseUUID(taskCtx.OrchestrationNodeID)
	if err != nil {
		return err
	}
	plan, err := o.Queries.GetOrchestrationPlan(ctx, planID)
	if err != nil {
		return err
	}
	node, err := o.Queries.GetOrchestrationNode(ctx, nodeID)
	if err != nil {
		return err
	}

	var retryTask *db.AgentTaskQueue
	err = o.runInTx(ctx, func(qtx *db.Queries) error {
		payload := mustJSON(map[string]any{"reason": failureReason})
		if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    planID,
			NodeID:    nodeID,
			TaskID:    task.ID,
			EventType: "task.failed",
			ActorType: "agent",
			ActorID:   task.AgentID,
			Payload:   payload,
		}); err != nil {
			return err
		}
		latestNode, err := qtx.GetOrchestrationNode(ctx, nodeID)
		if err != nil {
			return err
		}
		if latestNode.AttemptCount < latestNode.MaxAttempts {
			if err := qtx.ReadyOrchestrationNode(ctx, nodeID); err != nil {
				return err
			}
			agent := db.Agent{ID: task.AgentID, RuntimeID: task.RuntimeID}
			next, err := o.dispatchNodeTask(ctx, qtx, dispatchNodeInput{
				Plan:               plan,
				Node:               node,
				Agent:              agent,
				IssueID:            task.IssueID,
				Priority:           task.Priority,
				AcceptanceCriteria: taskCtx.AcceptanceCriteria,
				ContextRefs:        taskCtx.ContextRefs,
			})
			if err != nil {
				return err
			}
			retryTask = &next
			_, err = qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
				PlanID:    planID,
				NodeID:    nodeID,
				TaskID:    next.ID,
				EventType: "node.retry_scheduled",
				ActorType: "kernel",
				Payload:   mustJSON(map[string]any{"reason": failureReason, "previous_task_id": util.UUIDToString(task.ID), "next_task_id": util.UUIDToString(next.ID)}),
			})
			return err
		}
		if err := qtx.FailOrchestrationNode(ctx, nodeID); err != nil {
			return err
		}
		if err := qtx.UpdateOrchestrationPlanStatus(ctx, db.UpdateOrchestrationPlanStatusParams{ID: planID, Status: "failed"}); err != nil {
			return err
		}
		if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    planID,
			NodeID:    nodeID,
			TaskID:    task.ID,
			EventType: "node.failed",
			ActorType: "kernel",
			Payload:   payload,
		}); err != nil {
			return err
		}
		_, err = qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    planID,
			EventType: "plan.failed",
			ActorType: "kernel",
			Payload:   payload,
		})
		return err
	})
	if err != nil {
		return err
	}
	if retryTask != nil {
		o.notifyTaskQueued(ctx, *retryTask)
	}
	return nil
}
```

- [ ] **Step 4: Call callback from FailTask**

In `server/internal/service/task.go`, after failed task is persisted and before broadcast, add:

```go
if _, ok := ParseOrchestrationTaskContext(task.Context); ok && s.Orchestrator != nil {
	if err := s.Orchestrator.OnTaskFailed(ctx, task, failureReason); err != nil {
		slog.Warn("orchestration failure callback failed", "task_id", util.UUIDToString(task.ID), "error", err)
	}
}
```

Use the local variable names already present in `FailTask`.

- [ ] **Step 5: Run tests**

Run:

```bash
cd server && go test ./internal/service ./internal/handler -run 'Test.*Orchestration.*FailTask|Test.*TaskFailure.*Orchestration|TestFailTask' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/internal/service/orchestrator.go server/internal/service/task.go server/internal/handler/handler_test.go
git commit -m "feat(orchestration): route task failures through kernel"
```

---

## Task 9: Harden CLI Structured Result Validation

**Files:**
- Modify: `server/cmd/multica/cmd_task.go`
- Create: `server/cmd/multica/cmd_task_test.go`

- [ ] **Step 1: Write CLI tests**

Create `server/cmd/multica/cmd_task_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskCompleteRequiresResultFile(t *testing.T) {
	cmd := taskCompleteCmd
	cmd.SetArgs([]string{"--task-id", "task-1"})
	err := runTaskComplete(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "--result is required") {
		t.Fatalf("expected --result error, got %v", err)
	}
}

func TestTaskCompleteRejectsInvalidJSONFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, []byte(`{bad`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := taskCompleteCmd
	cmd.SetArgs([]string{"--task-id", "task-1", "--result", path})
	err := runTaskComplete(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "result must be valid JSON") {
		t.Fatalf("expected invalid JSON error, got %v", err)
	}
}

func TestTaskCompleteRejectsNonObjectJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, []byte(`["not","object"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := taskCompleteCmd
	cmd.SetArgs([]string{"--task-id", "task-1", "--result", path})
	err := runTaskComplete(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "result must be a JSON object") {
		t.Fatalf("expected JSON object error, got %v", err)
	}
}
```

If global Cobra command reuse causes flag contamination, instantiate a fresh command in a helper inside the test file:

```go
func newTestTaskCompleteCmd() *cobra.Command {
	cmd := *taskCompleteCmd
	cmd.Flags().String("task-id", "", "Task ID to complete")
	cmd.Flags().String("result", "", "Path to structured result JSON")
	return &cmd
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
cd server && go test ./cmd/multica -run TestTaskComplete -count=1
```

Expected: non-object JSON test fails because current command accepts any JSON.

- [ ] **Step 3: Add object validation**

In `server/cmd/multica/cmd_task.go`, replace:

```go
var raw json.RawMessage
if err := json.Unmarshal(data, &raw); err != nil {
	return fmt.Errorf("result must be valid JSON: %w", err)
}
```

with:

```go
var obj map[string]any
if err := json.Unmarshal(data, &obj); err != nil {
	return fmt.Errorf("result must be valid JSON: %w", err)
}
if obj == nil {
	return fmt.Errorf("result must be a JSON object")
}
raw := json.RawMessage(data)
```

- [ ] **Step 4: Run CLI tests**

Run:

```bash
cd server && go test ./cmd/multica -run TestTaskComplete -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/cmd/multica/cmd_task.go server/cmd/multica/cmd_task_test.go
git commit -m "fix(cli): require structured task result object"
```

---

## Task 10: Expose Evaluator Reason in Issue Detail

**Files:**
- Modify: `packages/views/issues/components/issue-detail.tsx`
- Modify: `packages/views/issues/components/issue-detail.test.tsx`
- Modify: `packages/views/locales/en/issues.json`
- Modify: `packages/views/locales/zh-Hans/issues.json`

- [ ] **Step 1: Add frontend test**

In `packages/views/issues/components/issue-detail.test.tsx`, add a test using the existing render helper for issue detail. Seed orchestration query data with:

```ts
events: [
  {
    id: "event-1",
    plan_id: "plan-1",
    node_id: "node-1",
    task_id: "task-1",
    event_type: "evaluation.invalid_result",
    actor_type: "kernel",
    actor_id: null,
    payload: { reason: "missing_summary" },
    created_at: "2026-05-11T00:00:00Z",
  },
],
```

Assert the rendered section contains `missing_summary`.

- [ ] **Step 2: Run test and verify current behavior**

Run:

```bash
pnpm --filter @multica/views exec vitest run issues/components/issue-detail.test.tsx
```

Expected: may already pass if current code reads any `task.completed` reason only it will fail for `evaluation.invalid_result`.

- [ ] **Step 3: Update evaluator event selection**

In `packages/views/issues/components/issue-detail.tsx`, replace:

```ts
const latestEvaluatorEvent = [...events].reverse().find((event) => event.event_type === "task.completed");
```

with:

```ts
const latestEvaluatorEvent = [...events]
  .reverse()
  .find((event) =>
    [
      "evaluation.invalid_result",
      "evaluation.failed",
      "evaluation.waiting_human",
      "evaluation.passed",
      "task.completed",
    ].includes(event.event_type),
  );
```

Keep existing `payload.reason` extraction.

- [ ] **Step 4: Run view tests**

Run:

```bash
pnpm --filter @multica/views exec vitest run issues/components/issue-detail.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add packages/views/issues/components/issue-detail.tsx packages/views/issues/components/issue-detail.test.tsx packages/views/locales/en/issues.json packages/views/locales/zh-Hans/issues.json
git commit -m "fix(issues): surface orchestration evaluator reason"
```

---

## Task 11: Full Verification

**Files:**
- No new files unless fixing failures.

- [ ] **Step 1: Run service tests**

```bash
cd server && go test ./internal/service -count=1
```

Expected: PASS.

- [ ] **Step 2: Run handler tests impacted by orchestration**

```bash
cd server && go test ./internal/handler -run 'Test.*Orchestration|Test.*orchestration|TestCompleteTask|TestFailTask' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run CLI tests**

```bash
cd server && go test ./cmd/multica -count=1
```

Expected: PASS.

- [ ] **Step 4: Run frontend checks**

```bash
pnpm --filter @multica/views exec vitest run issues/components/issue-detail.test.tsx
pnpm --filter @multica/core exec vitest run api/schema.test.ts
pnpm typecheck
```

Expected: PASS.

- [ ] **Step 5: Run broader backend tests**

```bash
make test
```

Expected: PASS.

- [ ] **Step 6: Commit verification fixes**

If any command fails due to the intended changes, fix the issue and commit:

```bash
git add <changed-files>
git commit -m "test(orchestration): complete correctness verification"
```

If a failure is unrelated to this work, record the exact failing command, package, and error in the final handoff.

---

## Task 12: Update Design Spec Status

**Files:**
- Modify: `docs/superpowers/specs/2026-05-11-orchestration-kernel-correctness-evidence-design.md`
- Modify: `docs/superpowers/plans/2026-05-11-orchestration-kernel-correctness-evidence-plan.md`

- [ ] **Step 1: Update spec status**

Change:

```markdown
状态：Draft for review
```

to:

```markdown
状态：Implementation planned
```

- [ ] **Step 2: Add plan link to spec**

After the associated design line, add:

```markdown
实施计划：`docs/superpowers/plans/2026-05-11-orchestration-kernel-correctness-evidence-plan.md`
```

- [ ] **Step 3: Run doc red-flag scan**

```bash
rg -n "TB""D|TO""DO|FIX""ME|place""holder|待""定|后续""补充" docs/superpowers/specs/2026-05-11-orchestration-kernel-correctness-evidence-design.md docs/superpowers/plans/2026-05-11-orchestration-kernel-correctness-evidence-plan.md
```

Expected: no matches.

- [ ] **Step 4: Commit docs**

```bash
git add docs/superpowers/specs/2026-05-11-orchestration-kernel-correctness-evidence-design.md docs/superpowers/plans/2026-05-11-orchestration-kernel-correctness-evidence-plan.md
git commit -m "docs(orchestration): plan kernel correctness implementation"
```

---

## Self-Review Checklist

- Spec section 3.1 kernel correctness maps to Tasks 5, 6, 7, 8, 11.
- Spec section 3.2 evidence contract maps to Tasks 1, 2, 3, 4, 9.
- Spec section 7 attempt semantics maps to Tasks 5, 6, 7.
- Spec section 8 event completeness maps to Tasks 5, 6, 8.
- Spec section 9 feature flag behavior maps to Task 7.
- Spec section 10 structured result protocol maps to Tasks 1, 2, 9.
- Spec section 11 CLI completion protocol maps to Task 9.
- Spec section 12 schema validation maps to Tasks 1, 2.
- Spec section 13 HardCheckEvaluator maps to Tasks 3, 4.
- Spec section 15 UI impact maps to Task 10.
- Spec section 16 test plan maps to Tasks 1, 3, 5, 7, 8, 9, 10, 11.

The plan intentionally postpones LLM planner/evaluator, multi-agent strategy, full plan graph UI, and policy DSL because the design spec marks them as non-goals.
