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
	Score             float64  `json:"score"`
	Reason            string   `json:"reason"`
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
		return retryableEvaluation(result, EvaluationResult{Pass: false, Reason: "missing_summary"}), nil
	}
	if testResultFailed(result.TestResult) || artifactTestResultFailed(result.Artifacts) {
		return retryableEvaluation(result, EvaluationResult{Pass: false, Reason: "test_result_failed"}), nil
	}
	if failed := missingCriteriaEvidence(input.AcceptanceCriteria, result.CriteriaEvidence); len(failed) > 0 {
		return retryableEvaluation(result, EvaluationResult{Pass: false, Reason: "missing_criteria_evidence", FailedCriteria: failed}), nil
	}

	switch input.Node.Type {
	case "implement", "fix":
		if len(result.ChangedFiles) == 0 && !hasArtifactType(result.Artifacts, "diff") && !hasArtifactType(result.Artifacts, "file") {
			return retryableEvaluation(result, EvaluationResult{Pass: false, Reason: "missing_implementation_artifact", MissingArtifacts: []string{"diff", "file"}}), nil
		}
	case "test":
		if len(result.TestResult) == 0 && !hasArtifactType(result.Artifacts, "test_result") {
			return retryableEvaluation(result, EvaluationResult{Pass: false, Reason: "missing_test_result", MissingArtifacts: []string{"test_result"}}), nil
		}
	case "review":
		if !hasArtifactType(result.Artifacts, "review_result") {
			return retryableEvaluation(result, EvaluationResult{Pass: false, Reason: "missing_review_result", MissingArtifacts: []string{"review_result"}}), nil
		}
	case "design":
		if !hasArtifactType(result.Artifacts, "decision") && len(result.CriteriaEvidence) == 0 {
			return retryableEvaluation(result, EvaluationResult{Pass: false, Reason: "missing_design_evidence", MissingArtifacts: []string{"decision"}}), nil
		}
	}

	return EvaluationResult{Pass: true, Reason: "hard_check_passed", RecommendedAction: "complete", Score: result.Confidence}, nil
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
