package service

import (
	"context"
	"encoding/json"
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
	if eval.Pass || eval.RecommendedAction != "retry" || eval.Reason != "missing_implementation_artifact" {
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
	if eval.Pass || eval.RecommendedAction != "retry" || eval.Reason != "test_result_failed" {
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
	if eval.Pass || eval.RecommendedAction != "retry" || eval.Reason != "missing_criteria_evidence" {
		t.Fatalf("unexpected eval: %#v", eval)
	}
}

func TestHardCheckEvaluatorMismatchedCriteriaEvidenceFails(t *testing.T) {
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
			CriteriaEvidence: []CriteriaEvidence{
				{Criterion: "must include docs", Evidence: "docs updated"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if eval.Pass || eval.Reason != "missing_criteria_evidence" || len(eval.FailedCriteria) != 1 || eval.FailedCriteria[0] != "must include tests" {
		t.Fatalf("unexpected eval: %#v", eval)
	}
}

func TestHardCheckEvaluatorLowConfidenceFailureAsksHuman(t *testing.T) {
	evaluator := HardCheckEvaluator{}
	eval, err := evaluator.Evaluate(context.Background(), EvaluationInput{
		Node:       db.OrchestrationNode{Type: "implement"},
		Validation: ResultValidation{Valid: true},
		Result: AgentStructuredResult{
			Status:           "completed",
			Summary:          "done",
			CriteriaEvidence: []CriteriaEvidence{{Criterion: "c", Evidence: "e"}},
			Confidence:       0.3,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if eval.Pass || eval.Reason != "missing_implementation_artifact" || eval.RecommendedAction != "ask_human" {
		t.Fatalf("unexpected eval: %#v", eval)
	}
}

func TestHardCheckEvaluatorMalformedAcceptanceCriteriaFailClosed(t *testing.T) {
	evaluator := HardCheckEvaluator{}
	eval, err := evaluator.Evaluate(context.Background(), EvaluationInput{
		Node:               db.OrchestrationNode{Type: "implement"},
		Validation:         ResultValidation{Valid: true},
		AcceptanceCriteria: ParseAcceptanceCriteria(json.RawMessage(`[{}]`)),
		Result: AgentStructuredResult{
			Status:       "completed",
			Summary:      "done",
			ChangedFiles: []string{"a.go"},
			CriteriaEvidence: []CriteriaEvidence{
				{Criterion: "node_objective", Evidence: "done"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if eval.Pass || eval.Reason != "missing_criteria_evidence" || len(eval.FailedCriteria) != 1 || eval.FailedCriteria[0] != "acceptance_criteria" {
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
	if !eval.Pass || eval.RecommendedAction != "complete" || eval.Reason != "hard_check_passed" {
		t.Fatalf("unexpected eval: %#v", eval)
	}
}

func TestParseAcceptanceCriteriaHandlesTextField(t *testing.T) {
	criteria := ParseAcceptanceCriteria(json.RawMessage(`[{"text":" must include tests "}]`))
	if len(criteria) != 1 || criteria[0].Criterion != "must include tests" {
		t.Fatalf("unexpected criteria: %#v", criteria)
	}
}

func TestParseAcceptanceCriteriaHandlesCriterionField(t *testing.T) {
	criteria := ParseAcceptanceCriteria(json.RawMessage(`[{"criterion":" must include tests "}]`))
	if len(criteria) != 1 || criteria[0].Criterion != "must include tests" {
		t.Fatalf("unexpected criteria: %#v", criteria)
	}
}

func TestParseAcceptanceCriteriaHandlesStringArrays(t *testing.T) {
	criteria := ParseAcceptanceCriteria(json.RawMessage(`[" must include tests "," "]`))
	if len(criteria) != 1 || criteria[0].Criterion != "must include tests" {
		t.Fatalf("unexpected criteria: %#v", criteria)
	}
}
