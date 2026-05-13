package service

import (
	"encoding/json"
	"strings"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type NodeSummary struct {
	Status                 string `json:"status"`
	ReasonCode             string `json:"reason_code"`
	ReasonTitle            string `json:"reason_title"`
	ReasonDetail           string `json:"reason_detail"`
	RecommendedAction      string `json:"recommended_action"`
	ActionEnabled          bool   `json:"action_enabled"`
	AttemptCount           int32  `json:"attempt_count"`
	MaxAttempts            int32  `json:"max_attempts"`
	LatestEvaluationStatus string `json:"latest_evaluation_status,omitempty"`
	LatestAgentSummary     string `json:"latest_agent_summary,omitempty"`
	PriorEvidenceSummary   string `json:"prior_evidence_summary,omitempty"`
	UpdatedAt              string `json:"updated_at,omitempty"`
}

type NodeObservabilityInput struct {
	NodeStatus          string
	NodeType            string
	AttemptCount        int32
	MaxAttempts         int32
	NodeUpdatedAt       time.Time
	Evaluation          *EvaluationResult
	LegacyReason        string
	LegacyAction        string
	LatestAgentSummary  string
	LatestFailureReason string
	RetryScheduled      bool
}

type orchestrationEventPayload struct {
	Reason               string `json:"reason"`
	Summary              string `json:"summary"`
	RecommendedAction    string `json:"recommended_action"`
	FailureReason        string `json:"failure_reason"`
	PriorEvidenceSummary string `json:"prior_evidence_summary"`
}

func BuildNodeSummary(input NodeObservabilityInput) NodeSummary {
	reasonCode, reasonTitle, reasonDetail, recommendedAction, latestEvaluationStatus := deriveObservabilitySemantics(input)

	if input.RetryScheduled && input.NodeStatus != "failed" && input.NodeStatus != "waiting_human" && input.NodeStatus != "completed" {
		reasonCode = "retry_scheduled"
		reasonTitle = "Retry scheduled"
		reasonDetail = "Kernel scheduled another attempt for this node after the previous run failed."
		recommendedAction = "none"
		if latestEvaluationStatus == "" {
			latestEvaluationStatus = "retry_scheduled"
		}
	}

	if reasonCode == "" {
		reasonCode = fallbackReasonCode(input.NodeStatus)
	}
	if reasonTitle == "" {
		reasonTitle = reasonCodeTitle(reasonCode)
	}
	if reasonDetail == "" {
		reasonDetail = fallbackReasonDetail(reasonCode)
	}
	if recommendedAction == "" {
		recommendedAction = fallbackRecommendedAction(reasonCode)
	}

	summary := NodeSummary{
		Status:                 input.NodeStatus,
		ReasonCode:             reasonCode,
		ReasonTitle:            reasonTitle,
		ReasonDetail:           reasonDetail,
		RecommendedAction:      recommendedAction,
		ActionEnabled:          recommendedAction == "retry" || recommendedAction == "approve",
		AttemptCount:           input.AttemptCount,
		MaxAttempts:            input.MaxAttempts,
		LatestEvaluationStatus: latestEvaluationStatus,
		LatestAgentSummary:     strings.TrimSpace(input.LatestAgentSummary),
		PriorEvidenceSummary:   strings.TrimSpace(input.LegacyReason),
	}
	if !input.NodeUpdatedAt.IsZero() {
		summary.UpdatedAt = input.NodeUpdatedAt.UTC().Format(time.RFC3339)
	}
	return summary
}

func BuildNodeSummaryFromRecords(node db.OrchestrationNode, events []db.OrchestrationEvent) NodeSummary {
	input := NodeObservabilityInput{
		NodeStatus:   node.Status,
		NodeType:     node.Type,
		AttemptCount: node.AttemptCount,
		MaxAttempts:  node.MaxAttempts,
	}
	if node.UpdatedAt.Valid {
		input.NodeUpdatedAt = node.UpdatedAt.Time
	}

	var latestRelevant time.Time
	for _, event := range events {
		if !event.NodeID.Valid || event.NodeID.Bytes != node.ID.Bytes {
			continue
		}
		if event.CreatedAt.Valid && event.CreatedAt.Time.After(latestRelevant) {
			latestRelevant = event.CreatedAt.Time
		}

		var payload orchestrationEventPayload
		_ = json.Unmarshal(event.Payload, &payload)

		switch event.EventType {
		case "task.completed":
			if strings.TrimSpace(payload.Summary) != "" {
				input.LatestAgentSummary = payload.Summary
			}
		case "evaluation.passed":
			input.Evaluation = &EvaluationResult{
				Pass:              true,
				Status:            "passed",
				Reason:            payload.Reason,
				RecommendedAction: payload.RecommendedAction,
			}
		case "evaluation.failed":
			input.Evaluation = &EvaluationResult{
				Pass:              false,
				Status:            "failed",
				Reason:            payload.Reason,
				RecommendedAction: payload.RecommendedAction,
			}
		case "evaluation.invalid_result":
			input.Evaluation = &EvaluationResult{
				Pass:              false,
				Status:            "evidence_insufficient",
				Reason:            firstNonEmpty(payload.Reason, "evidence_insufficient"),
				RecommendedAction: payload.RecommendedAction,
			}
		case "evaluation.waiting_human":
			input.Evaluation = &EvaluationResult{
				Pass:              false,
				Status:            "waiting_human",
				Reason:            payload.Reason,
				RecommendedAction: payload.RecommendedAction,
			}
		case "task.failed", "node.failed":
			if strings.TrimSpace(payload.FailureReason) != "" {
				input.LatestFailureReason = payload.FailureReason
			} else if strings.TrimSpace(payload.Reason) != "" {
				input.LatestFailureReason = payload.Reason
			}
		case "node.retry_scheduled":
			input.RetryScheduled = true
			if strings.TrimSpace(payload.Reason) != "" {
				input.LatestFailureReason = payload.Reason
			}
			if strings.TrimSpace(payload.PriorEvidenceSummary) != "" {
				input.LegacyReason = payload.PriorEvidenceSummary
			}
		case "node.waiting_human":
			if input.Evaluation == nil {
				reason := payload.Reason
				if strings.TrimSpace(reason) == "" {
					reason = "manual_approval"
				}
				input.Evaluation = &EvaluationResult{
					Status:            "waiting_human",
					Reason:            reason,
					RecommendedAction: "ask_human",
				}
			}
		}
	}

	if !latestRelevant.IsZero() && latestRelevant.After(input.NodeUpdatedAt) {
		input.NodeUpdatedAt = latestRelevant
	}

	return BuildNodeSummary(input)
}

func deriveObservabilitySemantics(input NodeObservabilityInput) (string, string, string, string, string) {
	if input.Evaluation != nil {
		switch input.NodeStatus {
		case "waiting_human":
			if requiresHumanInput(input.Evaluation.Reason) {
				return "waiting_for_human_input", "Human input required", firstNonEmpty(input.Evaluation.ReasonDetail, "This node requires additional human input before it can proceed."), "provide_input", "waiting_human"
			}
			return "waiting_for_approval", "Approval required", firstNonEmpty(input.Evaluation.ReasonDetail, "Kernel evaluation requires human approval before marking this node complete."), "approve", "waiting_human"
		case "failed":
			if input.AttemptCount >= input.MaxAttempts && input.MaxAttempts > 0 {
				return "retry_exhausted", "Retries exhausted", retryExhaustedDetail(input), "retry", firstNonEmpty(input.Evaluation.Status, "failed")
			}
			return mapFailureReason(input), firstNonEmpty(reasonCodeTitle(mapFailureReason(input)), "Failed"), failureDetail(input), "retry", firstNonEmpty(input.Evaluation.Status, "failed")
		case "completed":
			return "completed", "Completed", firstNonEmpty(input.Evaluation.ReasonDetail, "Kernel hard checks passed for this node."), "none", firstNonEmpty(input.Evaluation.Status, "passed")
		case "running":
			return "running", "Running", "This node is currently running.", "none", firstNonEmpty(input.Evaluation.Status, "running")
		}
	}

	switch input.NodeStatus {
	case "completed":
		return "completed", "Completed", "Kernel marked this node complete.", "none", ""
	case "running":
		return "running", "Running", "This node is currently running.", "none", ""
	case "waiting_human":
		return "waiting_for_approval", "Approval required", "This node is waiting for a human decision before it can proceed.", "approve", ""
	case "failed":
		if input.AttemptCount >= input.MaxAttempts && input.MaxAttempts > 0 {
			return "retry_exhausted", "Retries exhausted", retryExhaustedDetail(input), "retry", ""
		}
		return "runtime_failed", "Runtime failed", failureDetail(input), "retry", ""
	case "ready":
		return "ready_to_run", "Ready to run", "All known prerequisites are satisfied and this node is ready to dispatch.", "none", ""
	case "pending":
		return "pending_dependencies", "Waiting on dependencies", "This node is waiting for upstream work before it can run.", "none", ""
	default:
		return "", "", "", "", ""
	}
}

func mapFailureReason(input NodeObservabilityInput) string {
	if input.Evaluation != nil {
		switch input.Evaluation.Reason {
		case "invalid_result", "evidence_insufficient":
			return "evidence_insufficient"
		case "missing_summary", "test_result_failed", "missing_criteria_evidence", "missing_implementation_artifact", "missing_test_result", "missing_review_result", "missing_design_evidence":
			return "evidence_insufficient"
		}
	}
	return "runtime_failed"
}

func failureDetail(input NodeObservabilityInput) string {
	if input.Evaluation != nil && strings.TrimSpace(input.Evaluation.ReasonDetail) != "" {
		return input.Evaluation.ReasonDetail
	}
	if strings.TrimSpace(input.LatestFailureReason) != "" {
		return "Latest failure reason: " + strings.TrimSpace(input.LatestFailureReason)
	}
	return fallbackReasonDetail(mapFailureReason(input))
}

func retryExhaustedDetail(input NodeObservabilityInput) string {
	base := "Kernel exhausted all retry attempts for this node."
	if strings.TrimSpace(input.LatestFailureReason) != "" {
		return base + " Latest failure reason: " + strings.TrimSpace(input.LatestFailureReason)
	}
	if input.Evaluation != nil && strings.TrimSpace(input.Evaluation.ReasonDetail) != "" {
		return base + " " + strings.TrimSpace(input.Evaluation.ReasonDetail)
	}
	return base
}

func fallbackReasonCode(nodeStatus string) string {
	switch nodeStatus {
	case "pending":
		return "pending_dependencies"
	case "ready":
		return "ready_to_run"
	case "running":
		return "running"
	case "waiting_human":
		return "waiting_for_approval"
	case "failed":
		return "runtime_failed"
	case "completed":
		return "completed"
	default:
		return "running"
	}
}

func fallbackReasonDetail(reasonCode string) string {
	switch reasonCode {
	case "pending_dependencies":
		return "This node is waiting for upstream work before it can run."
	case "ready_to_run":
		return "All known prerequisites are satisfied and this node is ready to dispatch."
	case "running":
		return "This node is currently running."
	case "waiting_for_human_input":
		return "This node requires additional human input before it can proceed."
	case "waiting_for_approval":
		return "Kernel evaluation requires human approval before marking this node complete."
	case "retry_scheduled":
		return "Kernel scheduled another attempt for this node after the previous run failed."
	case "runtime_failed":
		return "Execution failed before the kernel could mark this node complete."
	case "invalid_result":
		return "Structured result payload did not satisfy the orchestration result contract."
	case "evidence_insufficient":
		return "Kernel hard checks did not find sufficient evidence to complete this node."
	case "retry_exhausted":
		return "Kernel exhausted all retry attempts for this node."
	case "completed":
		return "Kernel marked this node complete."
	default:
		return "Orchestration node state is available."
	}
}

func fallbackRecommendedAction(reasonCode string) string {
	switch reasonCode {
	case "waiting_for_approval":
		return "approve"
	case "waiting_for_human_input":
		return "provide_input"
	case "runtime_failed", "invalid_result", "evidence_insufficient", "retry_exhausted":
		return "retry"
	default:
		return "none"
	}
}

func reasonCodeTitle(reasonCode string) string {
	switch reasonCode {
	case "pending_dependencies":
		return "Waiting on dependencies"
	case "ready_to_run":
		return "Ready to run"
	case "running":
		return "Running"
	case "evaluation_in_progress":
		return "Evaluating result"
	case "waiting_for_human_input":
		return "Human input required"
	case "waiting_for_approval":
		return "Approval required"
	case "retry_scheduled":
		return "Retry scheduled"
	case "runtime_failed":
		return "Runtime failed"
	case "invalid_result":
		return "Invalid result"
	case "evidence_insufficient":
		return "Evidence insufficient"
	case "retry_exhausted":
		return "Retries exhausted"
	case "completed":
		return "Completed"
	default:
		return ""
	}
}

func requiresHumanInput(reason string) bool {
	switch reason {
	case "agent_needs_human", "agent_reported_blocked", "agent_unavailable":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
