package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type ActivitySet struct {
	DB            service.OrchestrationDB
	Queries       *db.Queries
	Orchestration *service.OrchestrationService
}

type resultSchemaV1 struct {
	SchemaVersion string           `json:"schema_version"`
	Summary       string           `json:"summary"`
	ChangedFiles  []string         `json:"changed_files"`
	Artifacts     []resultRef      `json:"artifacts"`
	Tests         []resultTest     `json:"tests"`
	Risks         []string         `json:"risks"`
	Evidence      []resultEvidence `json:"evidence"`
}

type resultRef struct {
	Label string `json:"label"`
	Ref   string `json:"ref"`
}

type resultTest struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type resultEvidence struct {
	Type string `json:"type"`
	Ref  string `json:"ref"`
}

func (a ActivitySet) LoadIssue(ctx context.Context, input IssueWorkflowInput) (IssueSnapshot, error) {
	if a.Queries == nil {
		return IssueSnapshot{}, fmt.Errorf("issue loader unavailable")
	}
	issueID, err := util.ParseUUID(input.IssueID)
	if err != nil {
		return IssueSnapshot{}, err
	}
	issue, err := a.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return IssueSnapshot{}, err
	}
	return IssueSnapshot{
		WorkspaceID:    util.UUIDToString(issue.WorkspaceID),
		IssueID:        util.UUIDToString(issue.ID),
		Title:          issue.Title,
		Description:    textValue(issue.Description),
		AssigneeType:   issue.AssigneeType.String,
		AssigneeID:     uuidText(issue.AssigneeID),
		Priority:       issue.Priority,
		Status:         issue.Status,
		AcceptanceText: string(issue.AcceptanceCriteria),
	}, nil
}

func (a ActivitySet) AnalyzeIssue(ctx context.Context, issue IssueSnapshot, input IssueWorkflowInput) (AnalyzeIssueResult, error) {
	result := AnalyzeIssueResult{
		ProblemSummary:         summarizeIssue(issue),
		ExecutionAdvice:        "Dispatch the agent with a narrow fix, preserve the existing issue/task contract, and validate the result before marking the run complete.",
		SuspectedContext:       strings.TrimSpace(issue.Title + " " + issue.Description),
		RecommendedAgentPrompt: buildAgentPrompt(issue, input),
		ReasonCode:             "analysis_ready",
		RecommendedAction:      "none",
	}
	if len(issue.Description) > 0 {
		result.Risks = append(result.Risks, "review the issue description for hidden acceptance criteria")
	}
	if err := a.projectAnalysis(ctx, input, issue, result); err != nil {
		return AnalyzeIssueResult{}, err
	}
	return result, nil
}

func (a ActivitySet) DispatchDaemonTask(ctx context.Context, input DispatchDaemonTaskInput) (service.DispatchAgentTaskResult, error) {
	if a.Orchestration == nil {
		return service.DispatchAgentTaskResult{}, fmt.Errorf("orchestration service unavailable")
	}
	return a.Orchestration.DispatchAgentTask(ctx, service.DispatchAgentTaskInput{
		PlanID:             input.PlanID,
		WorkflowNodeKey:    input.WorkflowNodeKey,
		Attempt:            input.Attempt,
		TemporalWorkflowID: input.TemporalWorkflowID,
	})
}

func (a ActivitySet) ValidateOutcome(ctx context.Context, input ValidateOutcomeInput) (ValidateOutcomeResult, error) {
	if strings.TrimSpace(input.Outcome.WorkflowID) == "" {
		return ValidateOutcomeResult{}, fmt.Errorf("missing workflow id")
	}
	if input.Outcome.PlanID != input.Dispatch.PlanID || input.Outcome.TaskID != input.Dispatch.TaskID || input.Outcome.NodeID != input.Dispatch.NodeID || input.Outcome.Attempt != input.Dispatch.Attempt {
		return ValidateOutcomeResult{
			Status:             "waiting_human",
			ReasonCode:         "signal_mismatch",
			RecommendedAction:  "review",
			NeedsHumanReview:   true,
			TerminalPlanStatus: "waiting_human",
			ProjectionSummary:  input.Analysis.ProblemSummary,
			ProjectionDetail:   "agent task outcome did not match the active orchestration node",
		}, nil
	}
	if input.Outcome.Status != "completed" {
		return ValidateOutcomeResult{
			Status:             "waiting_human",
			ReasonCode:         "agent_task_" + input.Outcome.Status,
			RecommendedAction:  "review",
			NeedsHumanReview:   true,
			TerminalPlanStatus: "waiting_human",
			ProjectionSummary:  input.Analysis.ProblemSummary,
			ProjectionDetail:   "agent task did not complete successfully",
		}, nil
	}
	if strings.TrimSpace(string(input.Outcome.Result)) == "" {
		return retryableEvidenceResult(input, "empty structured result"), nil
	}
	var parsed resultSchemaV1
	if err := json.Unmarshal(input.Outcome.Result, &parsed); err != nil {
		return retryableEvidenceResult(input, "malformed structured result"), nil
	}
	if strings.TrimSpace(parsed.SchemaVersion) != "1" {
		return retryableEvidenceResult(input, "unsupported structured result schema version"), nil
	}
	if strings.TrimSpace(parsed.Summary) == "" || len(parsed.Evidence) == 0 {
		return retryableEvidenceResult(input, "structured result is missing required evidence"), nil
	}
	for _, evidence := range parsed.Evidence {
		if strings.TrimSpace(evidence.Type) == "" || strings.TrimSpace(evidence.Ref) == "" {
			return retryableEvidenceResult(input, "structured result contains incomplete evidence"), nil
		}
	}
	for _, test := range parsed.Tests {
		status := strings.ToLower(strings.TrimSpace(test.Status))
		if status != "" && status != "passed" && status != "skipped" {
			failedTests := failedTestNames(parsed.Tests)
			return ValidateOutcomeResult{
				Status:             "waiting_human",
				ReasonCode:         "tests_failed",
				RecommendedAction:  "review",
				NeedsHumanReview:   true,
				TerminalPlanStatus: "waiting_human",
				ProjectionSummary:  input.Analysis.ProblemSummary,
				ProjectionDetail:   "structured result reported failed tests: " + strings.Join(failedTests, ", "),
				FailedTests:        failedTests,
			}, nil
		}
	}
	if len(parsed.Risks) > 0 {
		risks := nonEmptyStrings(parsed.Risks)
		return ValidateOutcomeResult{
			Status:             "waiting_human",
			ReasonCode:         "risks_present",
			RecommendedAction:  "review",
			NeedsHumanReview:   true,
			TerminalPlanStatus: "waiting_human",
			ProjectionSummary:  input.Analysis.ProblemSummary,
			ProjectionDetail:   "structured result reported risks: " + strings.Join(risks, ", "),
			Risks:              risks,
		}, nil
	}
	return ValidateOutcomeResult{
		Status:             "completed",
		ReasonCode:         "",
		RecommendedAction:  "none",
		NeedsHumanReview:   false,
		TerminalPlanStatus: "completed",
		ProjectionSummary:  input.Analysis.ProblemSummary,
		ProjectionDetail:   "structured result validated",
	}, nil
}

func failedTestNames(tests []resultTest) []string {
	names := make([]string, 0)
	for _, test := range tests {
		status := strings.ToLower(strings.TrimSpace(test.Status))
		if status == "" || status == "passed" || status == "skipped" {
			continue
		}
		name := strings.TrimSpace(test.Name)
		if name == "" {
			name = status
		}
		names = append(names, name)
	}
	return names
}

func nonEmptyStrings(values []string) []string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if s := strings.TrimSpace(value); s != "" {
			trimmed = append(trimmed, s)
		}
	}
	return trimmed
}

func retryableEvidenceResult(input ValidateOutcomeInput, detail string) ValidateOutcomeResult {
	return ValidateOutcomeResult{
		Status:             "waiting_human",
		ReasonCode:         "evidence_insufficient",
		RecommendedAction:  "retry",
		ShouldRetry:        true,
		TerminalPlanStatus: "waiting_human",
		ProjectionSummary:  input.Analysis.ProblemSummary,
		ProjectionDetail:   detail,
	}
}

func (a ActivitySet) ReviewOutcome(ctx context.Context, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (ReviewOutcomeResult, error) {
	summary := strings.TrimSpace(strings.Join([]string{
		analysis.ProblemSummary,
		validation.ProjectionDetail,
	}, " "))
	result := ReviewOutcomeResult{Summary: summary}
	if len(validation.FailedTests) > 0 {
		result.Evidence = append(result.Evidence, validation.FailedTests...)
	}
	result.Risks = append(append(result.Risks, validation.Risks...), analysis.Risks...)
	switch {
	case validation.NeedsHumanReview:
		result.RecommendedAction = "review"
	case validation.Status == "completed":
		result.RecommendedAction = "accept"
	default:
		result.RecommendedAction = "review"
	}
	for _, risk := range analysis.Risks {
		normalized := strings.ToLower(strings.TrimSpace(risk))
		if strings.Contains(normalized, "high") || strings.Contains(normalized, "destructive") || strings.Contains(normalized, "migration") {
			result.HighRisk = true
			result.Concern = risk
			result.SeverityLabel = "high"
			break
		}
	}
	return result, nil
}

func (a ActivitySet) SummarizeOutcome(ctx context.Context, review ReviewOutcomeResult, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (SummarizeOutcomeResult, error) {
	summary := strings.TrimSpace(strings.Join([]string{
		analysis.ExecutionAdvice,
		review.Summary,
	}, "\n"))
	traceRef := strings.Join([]string{dispatch.PlanID, dispatch.NodeID, dispatch.TaskID}, "/")
	return SummarizeOutcomeResult{Summary: summary, TraceRef: traceRef}, nil
}

func (a ActivitySet) FinalizeWorkflow(ctx context.Context, validation ValidateOutcomeResult, review ReviewOutcomeResult, summary SummarizeOutcomeResult, input IssueWorkflowInput, issue IssueSnapshot, analysis AnalyzeIssueResult, dispatch service.DispatchAgentTaskResult, outcome service.AgentTaskOutcomeSignalInput) error {
	if a.DB == nil {
		return fmt.Errorf("projection store unavailable")
	}
	planID, err := util.ParseUUID(input.PlanID)
	if err != nil {
		return err
	}
	status := validation.TerminalPlanStatus
	if status == "" {
		status = "running"
	}
	tag, err := a.DB.Exec(ctx, `
		UPDATE orchestration_plan
		SET status = $2,
			reason_code = $3,
			recommended_action = $4,
			updated_at = now(),
			completed_at = CASE WHEN $2 IN ('completed', 'failed', 'cancelled', 'waiting_human') THEN now() ELSE completed_at END
		WHERE id = $1 AND status NOT IN ('cancelled', 'completed', 'failed')
	`, planID, status, validation.ReasonCode, validation.RecommendedAction)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}

	nodeID, err := util.ParseUUID(dispatch.NodeID)
	if err != nil {
		return err
	}
	if _, err := a.DB.Exec(ctx, `
		UPDATE orchestration_node
		SET status = $2,
			reason_code = $3,
			recommended_action = $4,
			completed_at = CASE WHEN $2 IN ('completed', 'failed', 'cancelled') THEN COALESCE(completed_at, now()) ELSE completed_at END,
			updated_at = now()
		WHERE id = $1 AND status NOT IN ('cancelled', 'completed', 'failed')
	`, nodeID, validation.Status, validation.ReasonCode, validation.RecommendedAction); err != nil {
		return err
	}

	if validation.ShouldRetry {
		if err := a.recordEvent(ctx, planID, "workflow.retrying", "system", validation.ProjectionDetail, map[string]any{
			"analysis":           analysis.ProblemSummary,
			"reason_code":        validation.ReasonCode,
			"recommended_action": validation.RecommendedAction,
			"retry_budget": map[string]any{
				"used": dispatch.Attempt,
				"max":  maxNodeAttempts,
			},
		}); err != nil {
			return err
		}
	} else if validation.NeedsHumanReview {
		if err := a.recordEvent(ctx, planID, "workflow.waiting_human", "system", validation.ProjectionDetail, map[string]any{
			"analysis":           analysis.ProblemSummary,
			"review":             review.Summary,
			"reason_code":        validation.ReasonCode,
			"recommended_action": validation.RecommendedAction,
			"failed_tests":       validation.FailedTests,
			"risks":              validation.Risks,
			"retry_budget": map[string]any{
				"used": dispatch.Attempt,
				"max":  maxNodeAttempts,
			},
		}); err != nil {
			return err
		}
		if err := service.CreateOrchestrationAttention(ctx, a.DB, planID, "waiting_human:"+validation.ReasonCode, "Approval required: "+validation.ProjectionDetail); err != nil {
			return err
		}
	} else {
		eventType := "workflow.completed"
		if status == "failed" {
			eventType = "workflow.failed"
		} else if status == "cancelled" {
			eventType = "workflow.cancelled"
		}
		if err := a.recordEvent(ctx, planID, eventType, "system", summary.Summary, map[string]any{
			"analysis": analysis.ProblemSummary,
			"dispatch": dispatch.TaskID,
		}); err != nil {
			return err
		}
		if status == "failed" {
			message := "Runtime failed: " + strings.TrimSpace(summary.Summary)
			dedupeKey := "failed:" + validation.ReasonCode
			if validation.ReasonCode == "retry_exhausted" {
				message = "Retries exhausted: " + strings.TrimSpace(summary.Summary)
				dedupeKey = "retry_exhausted"
			}
			if err := service.CreateOrchestrationAttention(ctx, a.DB, planID, dedupeKey, message); err != nil {
				return err
			}
		}
	}
	if status == "completed" && validation.Status == "completed" {
		issueID, err := util.ParseUUID(issue.IssueID)
		if err != nil {
			return err
		}
		if _, err := a.DB.Exec(ctx, `
			UPDATE issue
			SET status = $2,
				updated_at = now()
			WHERE id = $1
				AND status NOT IN ('done', 'cancelled')
		`, issueID, "in_review"); err != nil {
			return err
		}
	}
	if err := a.recordArtifact(ctx, planID, "review_handoff", "system", "review handoff summary", map[string]any{
		"summary":            summary.Summary,
		"trace_ref":          summary.TraceRef,
		"plan_status":        status,
		"validation_reason":  validation.ReasonCode,
		"review_concern":     review.Concern,
		"recommended_action": review.RecommendedAction,
	}); err != nil {
		return err
	}

	return nil
}

func (a ActivitySet) ProjectAnalysis(ctx context.Context, input IssueWorkflowInput, issue IssueSnapshot, analysis AnalyzeIssueResult) error {
	planID, err := util.ParseUUID(input.PlanID)
	if err != nil {
		return err
	}
	if err := a.recordEvent(ctx, planID, "workflow.analysis", "system", analysis.ProblemSummary, map[string]any{
		"execution_advice":         analysis.ExecutionAdvice,
		"suspected_context":        analysis.SuspectedContext,
		"recommended_agent_prompt": analysis.RecommendedAgentPrompt,
	}); err != nil {
		return err
	}
	return a.recordArtifact(ctx, planID, "analysis_prompt", "system", "recommended agent prompt", map[string]any{
		"prompt": analysis.RecommendedAgentPrompt,
		"issue":  issue.IssueID,
	})
}

func (a ActivitySet) ProjectSignalAudit(ctx context.Context, input SignalAuditInput) error {
	planID, err := util.ParseUUID(input.PlanID)
	if err != nil {
		return err
	}
	return a.recordEvent(ctx, planID, input.EventType, "system", input.Message, map[string]any{
		"expected_workflow_id": input.ExpectedWorkflow,
		"expected_plan_id":     input.ExpectedPlanID,
		"expected_node_id":     input.ExpectedNodeID,
		"expected_task_id":     input.ExpectedTaskID,
		"expected_attempt":     input.ExpectedAttempt,
		"signal_workflow_id":   input.Outcome.WorkflowID,
		"signal_plan_id":       input.Outcome.PlanID,
		"signal_node_id":       input.Outcome.NodeID,
		"signal_task_id":       input.Outcome.TaskID,
		"signal_attempt":       input.Outcome.Attempt,
		"signal_status":        input.Outcome.Status,
	})
}

func (a ActivitySet) projectAnalysis(ctx context.Context, input IssueWorkflowInput, issue IssueSnapshot, analysis AnalyzeIssueResult) error {
	return a.ProjectAnalysis(ctx, input, issue, analysis)
}

func (a ActivitySet) recordEvent(ctx context.Context, planID pgtype.UUID, eventType, source, message string, details map[string]any) error {
	if a.DB == nil {
		return fmt.Errorf("projection store unavailable")
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = a.DB.Exec(ctx, `
		INSERT INTO orchestration_event (plan_id, type, source, message, details)
		VALUES ($1, $2, $3, $4, $5)
	`, planID, eventType, source, message, raw)
	return err
}

func (a ActivitySet) recordArtifact(ctx context.Context, planID pgtype.UUID, artifactType, source, label string, data map[string]any) error {
	if a.DB == nil {
		return fmt.Errorf("projection store unavailable")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = a.DB.Exec(ctx, `
		INSERT INTO orchestration_artifact (plan_id, type, source, label, data)
		VALUES ($1, $2, $3, $4, $5)
	`, planID, artifactType, source, label, raw)
	return err
}

func summarizeIssue(issue IssueSnapshot) string {
	summary := strings.TrimSpace(issue.Title)
	if summary == "" {
		summary = "Analyze the issue and prepare an implementation plan."
	}
	return summary
}

func buildAgentPrompt(issue IssueSnapshot, input IssueWorkflowInput) string {
	var b strings.Builder
	b.WriteString("You are working on a Multica issue.\n")
	b.WriteString("Issue: ")
	b.WriteString(issue.Title)
	b.WriteString("\n")
	if issue.Description != "" {
		b.WriteString("Description: ")
		b.WriteString(issue.Description)
		b.WriteString("\n")
	}
	if issue.AcceptanceText != "" {
		b.WriteString("Acceptance criteria: ")
		b.WriteString(issue.AcceptanceText)
		b.WriteString("\n")
	}
	b.WriteString("Plan ID: ")
	b.WriteString(input.PlanID)
	b.WriteString("\n")
	b.WriteString("Write the smallest change that satisfies the issue and keep the orchestration contract intact.")
	return b.String()
}

func textValue(v pgtype.Text) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

func uuidText(v pgtype.UUID) string {
	if !v.Valid {
		return ""
	}
	return util.UUIDToString(v)
}
