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

const acceptanceCriteriaFallback = "acceptance_criteria"

type EvaluationResult struct {
	Pass              bool     `json:"pass"`
	Status            string   `json:"status"`
	Score             float64  `json:"score"`
	Reason            string   `json:"reason"`
	ReasonDetail      string   `json:"reason_detail,omitempty"`
	FailedCriteria    []string `json:"failed_criteria,omitempty"`
	MissingArtifacts  []string `json:"missing_artifacts,omitempty"`
	Risks             []string `json:"risks,omitempty"`
	RecommendedAction string   `json:"recommended_action"`
}

type HardCheckEvaluator struct{}

func (HardCheckEvaluator) Evaluate(ctx context.Context, input EvaluationInput) (EvaluationResult, error) {
	_ = ctx
	result := input.Result
	if !input.Validation.Valid {
		return EvaluationResult{
			Pass:              false,
			Status:            "evidence_insufficient",
			Reason:            "evidence_insufficient",
			ReasonDetail:      "Structured result payload did not satisfy the orchestration result contract.",
			RecommendedAction: "retry",
			Score:             0,
		}, nil
	}

	switch result.Status {
	case "failed":
		return EvaluationResult{
			Pass:              false,
			Status:            "failed",
			Reason:            "agent_reported_failed",
			ReasonDetail:      "Agent reported that execution failed and requires another attempt or operator review.",
			RecommendedAction: "retry",
			Risks:             result.Risks,
		}, nil
	case "blocked":
		return EvaluationResult{
			Pass:              false,
			Status:            "waiting_human",
			Reason:            "agent_reported_blocked",
			ReasonDetail:      "Agent reported a blocker that requires human input before this node can proceed.",
			RecommendedAction: "ask_human",
			Risks:             result.Risks,
		}, nil
	case "needs_human":
		return EvaluationResult{
			Pass:              false,
			Status:            "waiting_human",
			Reason:            "agent_needs_human",
			ReasonDetail:      "Agent requested human input before the node can be completed.",
			RecommendedAction: "ask_human",
			Risks:             result.Risks,
		}, nil
	}

	if strings.TrimSpace(result.Summary) == "" {
		return retryableEvaluation(result, EvaluationResult{
			Pass:         false,
			Status:       "failed",
			Reason:       "missing_summary",
			ReasonDetail: "Completed orchestration results must include a summary.",
		}), nil
	}
	if len(result.Risks) > 0 {
		return EvaluationResult{
			Pass:              false,
			Status:            "waiting_human",
			Reason:            "risk_requires_approval",
			ReasonDetail:      "Structured result reported risks that require human approval before orchestration can continue.",
			RecommendedAction: "ask_human",
			Risks:             result.Risks,
			Score:             result.Confidence,
		}, nil
	}
	if testResultFailed(result.TestResult) || artifactTestResultFailed(result.Artifacts) {
		return retryableEvaluation(result, EvaluationResult{
			Pass:         false,
			Status:       "failed",
			Reason:       "test_result_failed",
			ReasonDetail: "Reported test output contains a failing result.",
		}), nil
	}
	if failed := missingCriteriaEvidence(input.AcceptanceCriteria, result.CriteriaEvidence); len(failed) > 0 {
		return retryableEvaluation(result, EvaluationResult{
			Pass:           false,
			Status:         "failed",
			Reason:         "missing_criteria_evidence",
			ReasonDetail:   "Structured result did not include evidence for all required acceptance criteria.",
			FailedCriteria: failed,
		}), nil
	}

	switch input.Node.Type {
	case "execute":
		if len(result.ChangedFiles) == 0 && !hasArtifactType(result.Artifacts, "diff") && !hasArtifactType(result.Artifacts, "file") {
			return retryableEvaluation(result, EvaluationResult{
				Pass:             false,
				Status:           "failed",
				Reason:           "missing_implementation_artifact",
				ReasonDetail:     "Implementation nodes must attach changed files or diff/file artifacts.",
				MissingArtifacts: []string{"diff", "file"},
			}), nil
		}
	case "verify":
		if len(result.TestResult) == 0 && !hasArtifactType(result.Artifacts, "test_result") {
			return retryableEvaluation(result, EvaluationResult{
				Pass:             false,
				Status:           "failed",
				Reason:           "missing_test_result",
				ReasonDetail:     "Test nodes must report a test result artifact or structured test result payload.",
				MissingArtifacts: []string{"test_result"},
			}), nil
		}
	case "plan":
		if !hasArtifactType(result.Artifacts, "decision") && len(result.CriteriaEvidence) == 0 {
			return retryableEvaluation(result, EvaluationResult{
				Pass:             false,
				Status:           "failed",
				Reason:           "missing_design_evidence",
				ReasonDetail:     "Design nodes must include a decision artifact or equivalent criteria evidence.",
				MissingArtifacts: []string{"decision"},
			}), nil
		}
	}

	return EvaluationResult{
		Pass:              true,
		Status:            "passed",
		Reason:            "hard_check_passed",
		ReasonDetail:      "Kernel hard checks passed for this node.",
		RecommendedAction: "complete",
		Score:             result.Confidence,
	}, nil
}

func retryableEvaluation(result AgentStructuredResult, eval EvaluationResult) EvaluationResult {
	eval.RecommendedAction = "retry"
	if result.Confidence > 0 && result.Confidence < 0.5 {
		eval.RecommendedAction = "ask_human"
	}
	return eval
}

func missingCriteriaEvidence(criteria []AcceptanceCriterion, evidence []CriteriaEvidence) []string {
	if len(criteria) == 0 {
		if len(evidence) == 0 || !criteriaEvidenceValid(evidence) {
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
	if !jsonHasContent(raw) {
		return nil
	}

	var objects []struct {
		Criterion string `json:"criterion"`
		Text      string `json:"text"`
	}
	if err := json.Unmarshal(raw, &objects); err == nil {
		out := make([]AcceptanceCriterion, 0, len(objects))
		for _, item := range objects {
			criterion := strings.TrimSpace(item.Criterion)
			if criterion == "" {
				criterion = strings.TrimSpace(item.Text)
			}
			if criterion != "" {
				out = append(out, AcceptanceCriterion{Criterion: criterion})
			}
		}
		if len(out) > 0 {
			return out
		}
		return []AcceptanceCriterion{{Criterion: acceptanceCriteriaFallback}}
	}
	var stringsOnly []string
	if err := json.Unmarshal(raw, &stringsOnly); err == nil {
		out := make([]AcceptanceCriterion, 0, len(stringsOnly))
		for _, item := range stringsOnly {
			criterion := strings.TrimSpace(item)
			if criterion != "" {
				out = append(out, AcceptanceCriterion{Criterion: criterion})
			}
		}
		if len(out) == 0 {
			return []AcceptanceCriterion{{Criterion: acceptanceCriteriaFallback}}
		}
		return out
	}
	return []AcceptanceCriterion{{Criterion: acceptanceCriteriaFallback}}
}

func artifactTestResultFailed(artifacts []AgentResultArtifact) bool {
	for _, artifact := range artifacts {
		if artifact.Type == "test_result" && testResultFailed(artifact.Content) {
			return true
		}
	}
	return false
}
