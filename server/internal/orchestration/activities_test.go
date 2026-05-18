package orchestration

import (
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

func TestValidateOutcomeRejectsArbitraryJSONAsEvidenceInsufficient(t *testing.T) {
	result, err := (ActivitySet{}).ValidateOutcome(t.Context(), ValidateOutcomeInput{
		Outcome: service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "wf-1",
			PlanID:         "plan-1",
			NodeID:         "node-1",
			TaskID:         "task-1",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"ok":true}`),
		},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  "plan-1",
			NodeID:  "node-1",
			TaskID:  "task-1",
			Attempt: 1,
		},
		Analysis: AnalyzeIssueResult{ProblemSummary: "Fix issue"},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if result.ReasonCode != "evidence_insufficient" {
		t.Fatalf("reason = %q, want evidence_insufficient", result.ReasonCode)
	}
	if !result.ShouldRetry {
		t.Fatal("malformed schema should be retryable while budget remains")
	}
}

func TestValidateOutcomeRejectsUnknownSchemaVersion(t *testing.T) {
	result, err := (ActivitySet{}).ValidateOutcome(t.Context(), ValidateOutcomeInput{
		Outcome: service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "wf-1",
			PlanID:         "plan-1",
			NodeID:         "node-1",
			TaskID:         "task-1",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"schema_version":"2","summary":"done","changed_files":["server/a.go"],"artifacts":[],"tests":[{"name":"go test","status":"passed"}],"risks":[],"evidence":[{"type":"test","ref":"go test ./..."}]}`),
		},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  "plan-1",
			NodeID:  "node-1",
			TaskID:  "task-1",
			Attempt: 1,
		},
		Analysis: AnalyzeIssueResult{ProblemSummary: "Fix issue"},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if result.ReasonCode != "evidence_insufficient" {
		t.Fatalf("reason = %q, want evidence_insufficient", result.ReasonCode)
	}
	if !result.ShouldRetry {
		t.Fatal("unknown schema version should be retryable while budget remains")
	}
}

func TestValidateOutcomeAcceptsResultSchemaV1WithEvidence(t *testing.T) {
	result, err := (ActivitySet{}).ValidateOutcome(t.Context(), ValidateOutcomeInput{
		Outcome: service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "wf-1",
			PlanID:         "plan-1",
			NodeID:         "node-1",
			TaskID:         "task-1",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"schema_version":"1","summary":"done","changed_files":["server/a.go"],"artifacts":[{"label":"diff","ref":"artifact://diff"}],"tests":[{"name":"go test ./...","status":"passed"}],"risks":[],"evidence":[{"type":"test","ref":"go test ./..."}]}`),
		},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  "plan-1",
			NodeID:  "node-1",
			TaskID:  "task-1",
			Attempt: 1,
		},
		Analysis: AnalyzeIssueResult{ProblemSummary: "Fix issue"},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if result.Status != "completed" || result.TerminalPlanStatus != "completed" {
		t.Fatalf("validation result = %+v, want completed", result)
	}
}

func TestValidateOutcomeRoutesFailedTestsToHumanReview(t *testing.T) {
	result, err := (ActivitySet{}).ValidateOutcome(t.Context(), ValidateOutcomeInput{
		Outcome: service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "wf-1",
			PlanID:         "plan-1",
			NodeID:         "node-1",
			TaskID:         "task-1",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"schema_version":"1","summary":"done","changed_files":["server/a.go"],"artifacts":[],"tests":[{"name":"go test ./...","status":"failed"}],"risks":[],"evidence":[{"type":"test","ref":"go test ./..."}]}`),
		},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  "plan-1",
			NodeID:  "node-1",
			TaskID:  "task-1",
			Attempt: 1,
		},
		Analysis: AnalyzeIssueResult{ProblemSummary: "Fix issue"},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if !result.NeedsHumanReview || result.ShouldRetry {
		t.Fatalf("failed tests should require human review without auto retry: %+v", result)
	}
	if result.ReasonCode != "tests_failed" {
		t.Fatalf("reason = %q, want tests_failed", result.ReasonCode)
	}
}

func TestValidateOutcomeRoutesRisksToHumanReview(t *testing.T) {
	result, err := (ActivitySet{}).ValidateOutcome(t.Context(), ValidateOutcomeInput{
		Outcome: service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "wf-1",
			PlanID:         "plan-1",
			NodeID:         "node-1",
			TaskID:         "task-1",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"schema_version":"1","summary":"done","changed_files":["server/a.go"],"artifacts":[],"tests":[{"name":"go test ./...","status":"passed"}],"risks":["touches billing"],"evidence":[{"type":"test","ref":"go test ./..."}]}`),
		},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  "plan-1",
			NodeID:  "node-1",
			TaskID:  "task-1",
			Attempt: 1,
		},
		Analysis: AnalyzeIssueResult{ProblemSummary: "Fix issue"},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if !result.NeedsHumanReview || result.ShouldRetry {
		t.Fatalf("risks should require human review without auto retry: %+v", result)
	}
	if result.ReasonCode != "risks_present" {
		t.Fatalf("reason = %q, want risks_present", result.ReasonCode)
	}
}
