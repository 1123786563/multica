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
	Reasoner      EinoReasoner
}

type EinoReasoner interface {
	AnalyzeIssue(ctx context.Context, issue IssueSnapshot, input IssueWorkflowInput) (AnalyzeIssueResult, error)
	ReviewOutcome(ctx context.Context, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (ReviewOutcomeResult, error)
	SummarizeOutcome(ctx context.Context, review ReviewOutcomeResult, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (SummarizeOutcomeResult, error)
}

type StaticEinoReasoner struct{}

type resultSchemaV1 struct {
	SchemaVersion string              `json:"schema_version"`
	Summary       string              `json:"summary"`
	ChangedFiles  []string            `json:"changed_files"`
	Artifacts     []ResultArtifactRef `json:"artifacts"`
	Tests         []ResultTestRef     `json:"tests"`
	Risks         []string            `json:"risks"`
	Evidence      []ResultEvidenceRef `json:"evidence"`
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
	reasoner := a.Reasoner
	if reasoner == nil {
		reasoner = StaticEinoReasoner{}
	}
	result, err := reasoner.AnalyzeIssue(ctx, issue, input)
	if err != nil {
		return AnalyzeIssueResult{}, err
	}
	if strings.TrimSpace(result.ProblemSummary) == "" ||
		strings.TrimSpace(result.ExecutionAdvice) == "" ||
		strings.TrimSpace(result.RecommendedAgentPrompt) == "" {
		return AnalyzeIssueResult{}, fmt.Errorf("malformed analyzer output")
	}
	if strings.TrimSpace(result.ReasonCode) == "" {
		result.ReasonCode = "analysis_ready"
	}
	if strings.TrimSpace(result.RecommendedAction) == "" {
		result.RecommendedAction = "none"
	}
	result.RecommendedAgentPrompt = withResultSchemaContract(result.RecommendedAgentPrompt)
	if err := a.projectAnalysis(ctx, input, issue, result); err != nil {
		return AnalyzeIssueResult{}, err
	}
	if err := a.projectNode(ctx, input.PlanID, "analyze", "completed", result.ReasonCode, result.RecommendedAction, 1); err != nil {
		return AnalyzeIssueResult{}, err
	}
	if hasEinoTrace(result.Trace) {
		planID, err := util.ParseUUID(input.PlanID)
		if err != nil {
			return AnalyzeIssueResult{}, err
		}
		if err := a.recordArtifact(ctx, planID, "analysis_trace", "eino", "analysis trace", einoTraceData(result.Trace, safeAnalyzeIssueOutput(result))); err != nil {
			return AnalyzeIssueResult{}, err
		}
	}
	return result, nil
}

func (StaticEinoReasoner) AnalyzeIssue(ctx context.Context, issue IssueSnapshot, input IssueWorkflowInput) (AnalyzeIssueResult, error) {
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
		AgentPrompt:        input.AgentPrompt,
	})
}

func (a ActivitySet) ValidateOutcome(ctx context.Context, input ValidateOutcomeInput) (ValidateOutcomeResult, error) {
	finish := func(result ValidateOutcomeResult) (ValidateOutcomeResult, error) {
		status := result.Status
		if result.ShouldRetry {
			status = "failed"
		}
		if strings.TrimSpace(status) == "" {
			status = "completed"
		}
		if err := a.projectNode(ctx, input.Dispatch.PlanID, "validate", status, result.ReasonCode, result.RecommendedAction, input.Dispatch.Attempt); err != nil {
			return ValidateOutcomeResult{}, err
		}
		return result, nil
	}
	if strings.TrimSpace(input.Outcome.WorkflowID) == "" {
		return ValidateOutcomeResult{}, fmt.Errorf("missing workflow id")
	}
	if input.Outcome.PlanID != input.Dispatch.PlanID || input.Outcome.TaskID != input.Dispatch.TaskID || input.Outcome.NodeID != input.Dispatch.NodeID || input.Outcome.Attempt != input.Dispatch.Attempt {
		return finish(ValidateOutcomeResult{
			Status:             "waiting_human",
			ReasonCode:         "signal_mismatch",
			RecommendedAction:  "review",
			NeedsHumanReview:   true,
			TerminalPlanStatus: "waiting_human",
			ProjectionSummary:  input.Analysis.ProblemSummary,
			ProjectionDetail:   "agent task outcome did not match the active orchestration node",
		})
	}
	if input.Outcome.Status != "completed" {
		return finish(ValidateOutcomeResult{
			Status:             "waiting_human",
			ReasonCode:         "agent_task_" + input.Outcome.Status,
			RecommendedAction:  "review",
			NeedsHumanReview:   true,
			TerminalPlanStatus: "waiting_human",
			ProjectionSummary:  input.Analysis.ProblemSummary,
			ProjectionDetail:   "agent task did not complete successfully",
		})
	}
	if strings.TrimSpace(string(input.Outcome.Result)) == "" {
		return finish(retryableEvidenceResult(input, "empty structured result"))
	}
	var parsed resultSchemaV1
	if err := json.Unmarshal(input.Outcome.Result, &parsed); err != nil {
		return finish(retryableEvidenceResult(input, "malformed structured result"))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(input.Outcome.Result, &raw); err != nil {
		return finish(retryableEvidenceResult(input, "malformed structured result"))
	}
	for _, key := range []string{"changed_files", "artifacts", "tests", "risks", "evidence"} {
		if _, ok := raw[key]; !ok {
			return finish(retryableEvidenceResult(input, "structured result is missing "+key))
		}
	}
	if strings.TrimSpace(parsed.SchemaVersion) != "1" {
		return finish(retryableEvidenceResult(input, "unsupported structured result schema version"))
	}
	if strings.TrimSpace(parsed.Summary) == "" || len(parsed.Evidence) == 0 {
		return finish(retryableEvidenceResult(input, "structured result is missing required evidence"))
	}
	changedFiles := nonEmptyStrings(parsed.ChangedFiles)
	if len(changedFiles) == 0 {
		return finish(retryableEvidenceResult(input, "structured result is missing changed files"))
	}
	parsed.ChangedFiles = changedFiles
	for _, artifact := range parsed.Artifacts {
		if strings.TrimSpace(artifact.Label) == "" || strings.TrimSpace(artifact.Ref) == "" {
			return finish(retryableEvidenceResult(input, "structured result contains incomplete artifact reference"))
		}
	}
	for _, evidence := range parsed.Evidence {
		if strings.TrimSpace(evidence.Type) == "" || strings.TrimSpace(evidence.Ref) == "" {
			return finish(retryableEvidenceResult(input, "structured result contains incomplete evidence"))
		}
	}
	for _, test := range parsed.Tests {
		if strings.TrimSpace(test.Name) == "" || strings.TrimSpace(test.Status) == "" {
			return finish(retryableEvidenceResult(input, "structured result contains incomplete test result"))
		}
		status := strings.ToLower(strings.TrimSpace(test.Status))
		if status != "" && status != "passed" && status != "skipped" {
			failedTests := failedTestNames(parsed.Tests)
			result := ValidateOutcomeResult{
				Status:             "waiting_human",
				ReasonCode:         "tests_failed",
				RecommendedAction:  "review",
				NeedsHumanReview:   true,
				TerminalPlanStatus: "waiting_human",
				ProjectionSummary:  input.Analysis.ProblemSummary,
				ProjectionDetail:   "structured result reported failed tests: " + strings.Join(failedTests, ", "),
				FailedTests:        failedTests,
			}
			attachResultSchemaDetails(&result, parsed)
			return finish(result)
		}
	}
	if len(parsed.Risks) > 0 {
		risks := nonEmptyStrings(parsed.Risks)
		result := ValidateOutcomeResult{
			Status:             "waiting_human",
			ReasonCode:         "risks_present",
			RecommendedAction:  "review",
			NeedsHumanReview:   true,
			TerminalPlanStatus: "waiting_human",
			ProjectionSummary:  input.Analysis.ProblemSummary,
			ProjectionDetail:   "structured result reported risks: " + strings.Join(risks, ", "),
			Risks:              risks,
		}
		attachResultSchemaDetails(&result, parsed)
		return finish(result)
	}
	result := ValidateOutcomeResult{
		Status:             "completed",
		ReasonCode:         "",
		RecommendedAction:  "none",
		NeedsHumanReview:   false,
		TerminalPlanStatus: "completed",
		ProjectionSummary:  input.Analysis.ProblemSummary,
		ProjectionDetail:   "structured result validated",
	}
	attachResultSchemaDetails(&result, parsed)
	return finish(result)
}

func failedTestNames(tests []ResultTestRef) []string {
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

func attachResultSchemaDetails(result *ValidateOutcomeResult, parsed resultSchemaV1) {
	result.ResultSummary = strings.TrimSpace(parsed.Summary)
	result.ChangedFiles = nonEmptyStrings(parsed.ChangedFiles)
	result.Artifacts = parsed.Artifacts
	result.Tests = parsed.Tests
	result.Evidence = parsed.Evidence
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

func hasEinoTrace(trace EinoTrace) bool {
	return strings.TrimSpace(trace.ReasoningProfileRef) != "" ||
		strings.TrimSpace(trace.PromptProfileRef) != "" ||
		strings.TrimSpace(trace.SchemaName) != "" ||
		strings.TrimSpace(trace.SchemaVersion) != "" ||
		strings.TrimSpace(trace.ProviderLabel) != "" ||
		strings.TrimSpace(trace.Model) != "" ||
		strings.TrimSpace(trace.CapabilityMode) != "" ||
		trace.LatencyMS != 0 ||
		trace.InputTokens != 0 ||
		trace.OutputTokens != 0 ||
		trace.TotalTokens != 0
}

func einoTraceData(trace EinoTrace, parsedOutput map[string]any) map[string]any {
	return map[string]any{
		"reasoning_profile_ref": normalizeReasoningProfileRef(trace.ReasoningProfileRef),
		"prompt_profile_ref":    strings.TrimSpace(trace.PromptProfileRef),
		"schema_name":           strings.TrimSpace(trace.SchemaName),
		"schema_version":        strings.TrimSpace(trace.SchemaVersion),
		"provider_label":        strings.TrimSpace(trace.ProviderLabel),
		"model":                 strings.TrimSpace(trace.Model),
		"capability_mode":       strings.TrimSpace(trace.CapabilityMode),
		"latency_ms":            trace.LatencyMS,
		"usage": map[string]any{
			"input_tokens":  trace.InputTokens,
			"output_tokens": trace.OutputTokens,
			"total_tokens":  trace.TotalTokens,
		},
		"parsed_output": parsedOutput,
	}
}

func safeAnalyzeIssueOutput(result AnalyzeIssueResult) map[string]any {
	return map[string]any{
		"problem_summary":    result.ProblemSummary,
		"reason_code":        result.ReasonCode,
		"recommended_action": result.RecommendedAction,
	}
}

func safeReviewOutcomeOutput(result ReviewOutcomeResult) map[string]any {
	return map[string]any{
		"summary":            result.Summary,
		"high_risk":          result.HighRisk,
		"severity_label":     result.SeverityLabel,
		"recommended_action": result.RecommendedAction,
	}
}

func safeSummarizeOutcomeOutput(result SummarizeOutcomeResult) map[string]any {
	return map[string]any{
		"summary":   result.Summary,
		"trace_ref": result.TraceRef,
	}
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
	reasoner := a.Reasoner
	if reasoner == nil {
		reasoner = StaticEinoReasoner{}
	}
	result, err := reasoner.ReviewOutcome(ctx, validation, analysis, issue, dispatch)
	if err != nil {
		return ReviewOutcomeResult{}, err
	}
	summary := strings.TrimSpace(strings.Join([]string{
		analysis.ProblemSummary,
		validation.ProjectionDetail,
	}, " "))
	if strings.TrimSpace(result.Summary) == "" {
		result.Summary = summary
	}
	if strings.TrimSpace(result.RecommendedAction) == "" {
		result.RecommendedAction = "review"
	}
	nodeStatus := "completed"
	reasonCode := validation.ReasonCode
	recommendedAction := result.RecommendedAction
	if validation.NeedsHumanReview || validation.TerminalPlanStatus == "waiting_human" {
		nodeStatus = "waiting_human"
		if strings.TrimSpace(validation.RecommendedAction) != "" {
			recommendedAction = validation.RecommendedAction
		} else {
			recommendedAction = "review"
		}
	}
	if result.HighRisk {
		nodeStatus = "waiting_human"
		reasonCode = "review_high_risk"
		recommendedAction = "review"
	}
	if err := a.projectNode(ctx, dispatch.PlanID, "review", nodeStatus, reasonCode, recommendedAction, dispatch.Attempt); err != nil {
		return ReviewOutcomeResult{}, err
	}
	if hasEinoTrace(result.Trace) {
		planID, err := util.ParseUUID(dispatch.PlanID)
		if err != nil {
			return ReviewOutcomeResult{}, err
		}
		if err := a.recordArtifact(ctx, planID, "review_trace", "eino", "review trace", einoTraceData(result.Trace, safeReviewOutcomeOutput(result))); err != nil {
			return ReviewOutcomeResult{}, err
		}
	}
	return result, nil
}

func (a ActivitySet) SummarizeOutcome(ctx context.Context, review ReviewOutcomeResult, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (SummarizeOutcomeResult, error) {
	reasoner := a.Reasoner
	if reasoner == nil {
		reasoner = StaticEinoReasoner{}
	}
	result, err := reasoner.SummarizeOutcome(ctx, review, validation, analysis, issue, dispatch)
	if err != nil {
		return SummarizeOutcomeResult{}, err
	}
	if hasEinoTrace(result.Trace) {
		planID, err := util.ParseUUID(dispatch.PlanID)
		if err != nil {
			return SummarizeOutcomeResult{}, err
		}
		if err := a.recordArtifact(ctx, planID, "summary_trace", "eino", "summary trace", einoTraceData(result.Trace, safeSummarizeOutcomeOutput(result))); err != nil {
			return SummarizeOutcomeResult{}, err
		}
	}
	return result, nil
}

func (StaticEinoReasoner) ReviewOutcome(ctx context.Context, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (ReviewOutcomeResult, error) {
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
	if result.SeverityLabel == "" {
		result.SeverityLabel = "low"
	}
	return result, nil
}

func (StaticEinoReasoner) SummarizeOutcome(ctx context.Context, review ReviewOutcomeResult, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (SummarizeOutcomeResult, error) {
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
			completed_at = CASE
				WHEN $2 = 'running' THEN NULL
				WHEN $2 IN ('completed', 'failed', 'cancelled', 'waiting_human') THEN now()
				ELSE completed_at
			END
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
				completed_at = CASE
					WHEN $2 IN ('running', 'waiting_human') THEN NULL
					WHEN $2 IN ('completed', 'failed', 'cancelled') THEN COALESCE(completed_at, now())
					ELSE completed_at
				END,
				updated_at = now()
			WHERE id = $1
				AND (
					status NOT IN ('cancelled', 'completed', 'failed')
					OR ($2 = 'waiting_human' AND status IN ('completed', 'failed'))
				)
		`, nodeID, validation.Status, validation.ReasonCode, validation.RecommendedAction); err != nil {
		return err
	}

	if validation.ShouldRetry {
		if err := a.recordEvent(ctx, planID, "workflow.retrying", "system", validation.ProjectionDetail, map[string]any{
			"analysis":               analysis.ProblemSummary,
			"reason_code":            validation.ReasonCode,
			"recommended_action":     validation.RecommendedAction,
			"prior_evidence_summary": validation.PriorEvidenceSummary,
			"retry_budget": map[string]any{
				"used": dispatch.Attempt,
				"max":  maxNodeAttempts,
			},
		}); err != nil {
			return err
		}
	} else if validation.NeedsHumanReview {
		if err := a.recordEvent(ctx, planID, "workflow.waiting_human", "system", validation.ProjectionDetail, map[string]any{
			"analysis":               analysis.ProblemSummary,
			"review":                 review.Summary,
			"reason_code":            validation.ReasonCode,
			"recommended_action":     validation.RecommendedAction,
			"failed_tests":           validation.FailedTests,
			"risks":                  validation.Risks,
			"prior_evidence_summary": validation.PriorEvidenceSummary,
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
	if payload := resultEvidencePayload(validation); payload != nil {
		if err := a.recordArtifact(ctx, planID, "result_evidence", "agent", "agent result evidence", payload); err != nil {
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

func resultEvidencePayload(validation ValidateOutcomeResult) map[string]any {
	if strings.TrimSpace(validation.ResultSummary) == "" &&
		len(validation.ChangedFiles) == 0 &&
		len(validation.Artifacts) == 0 &&
		len(validation.Tests) == 0 &&
		len(validation.Evidence) == 0 {
		return nil
	}
	return map[string]any{
		"summary":       validation.ResultSummary,
		"changed_files": validation.ChangedFiles,
		"artifacts":     validation.Artifacts,
		"tests":         validation.Tests,
		"evidence":      validation.Evidence,
	}
}

func (a ActivitySet) ProjectAnalysis(ctx context.Context, input IssueWorkflowInput, issue IssueSnapshot, analysis AnalyzeIssueResult) error {
	planID, err := util.ParseUUID(input.PlanID)
	if err != nil {
		return err
	}
	if err := a.recordEvent(ctx, planID, "workflow.analysis", "system", analysis.ProblemSummary, map[string]any{
		"execution_advice":         analysis.ExecutionAdvice,
		"suspected_context":        analysis.SuspectedContext,
		"risks":                    analysis.Risks,
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
		"expected_workflow_id":   input.ExpectedWorkflow,
		"expected_plan_id":       input.ExpectedPlanID,
		"expected_node_id":       input.ExpectedNodeID,
		"expected_task_id":       input.ExpectedTaskID,
		"expected_attempt":       input.ExpectedAttempt,
		"signal_workflow_id":     input.Outcome.WorkflowID,
		"signal_plan_id":         input.Outcome.PlanID,
		"signal_node_id":         input.Outcome.NodeID,
		"signal_task_id":         input.Outcome.TaskID,
		"signal_attempt":         input.Outcome.Attempt,
		"signal_outcome_version": input.Outcome.OutcomeVersion,
		"signal_status":          input.Outcome.Status,
	})
}

func (a ActivitySet) ProjectEinoFailure(ctx context.Context, input EinoFailureProjectionInput) error {
	if a.DB == nil {
		return fmt.Errorf("projection store unavailable")
	}
	planID, err := util.ParseUUID(input.PlanID)
	if err != nil {
		return err
	}
	reasonCode := strings.TrimSpace(input.ReasonCode)
	if reasonCode == "" {
		reasonCode = EinoReasonProviderUnavailable
	}
	action := strings.TrimSpace(input.RecommendedAction)
	if action == "" {
		action = recommendedActionForEinoFailure(reasonCode)
	}
	message := strings.TrimSpace(input.Message)
	if message == "" {
		message = "Eino provider failed"
	}
	if err := a.projectNode(ctx, input.PlanID, input.WorkflowNodeKey, "failed", reasonCode, action, input.Attempt); err != nil {
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
	`, planID, reasonCode, action, message); err != nil {
		return err
	}
	if err := a.recordEvent(ctx, planID, "eino.provider_failed", "eino", message, map[string]any{
		"workflow_node_key":     input.WorkflowNodeKey,
		"reason_code":           reasonCode,
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
			"reason_code": reasonCode,
			"message":     message,
		},
	}
	if err := a.recordArtifact(ctx, planID, "provider_failure_trace", "eino", "Provider failure", trace); err != nil {
		return err
	}
	return service.CreateOrchestrationAttention(ctx, a.DB, planID, "eino_failure:"+reasonCode, "Eino provider failure: "+message)
}

func (a ActivitySet) projectAnalysis(ctx context.Context, input IssueWorkflowInput, issue IssueSnapshot, analysis AnalyzeIssueResult) error {
	return a.ProjectAnalysis(ctx, input, issue, analysis)
}

func (a ActivitySet) projectNode(ctx context.Context, planIDText, nodeKey, status, reasonCode, recommendedAction string, attempt int) error {
	if a.DB == nil {
		return nil
	}
	if strings.TrimSpace(planIDText) == "" || strings.TrimSpace(nodeKey) == "" {
		return nil
	}
	planID, err := util.ParseUUID(planIDText)
	if err != nil {
		return err
	}
	if attempt <= 0 {
		attempt = 1
	}
	if strings.TrimSpace(status) == "" {
		status = "pending"
	}
	if strings.TrimSpace(recommendedAction) == "" {
		recommendedAction = "none"
	}
	_, err = a.DB.Exec(ctx, `
		INSERT INTO orchestration_node (
			plan_id, workflow_node_key, title, status, reason_code,
			recommended_action, attempt, started_at, completed_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			CASE WHEN $4 IN ('running', 'completed', 'failed', 'waiting_human') THEN now() ELSE NULL END,
			CASE WHEN $4 IN ('completed', 'failed', 'cancelled') THEN now() ELSE NULL END
		)
		ON CONFLICT (plan_id, workflow_node_key, attempt) WHERE plan_id IS NOT NULL
		DO UPDATE SET
			title = EXCLUDED.title,
			status = EXCLUDED.status,
			reason_code = EXCLUDED.reason_code,
			recommended_action = EXCLUDED.recommended_action,
			started_at = COALESCE(orchestration_node.started_at, EXCLUDED.started_at),
			completed_at = CASE
				WHEN EXCLUDED.status IN ('completed', 'failed', 'cancelled') THEN COALESCE(orchestration_node.completed_at, EXCLUDED.completed_at, now())
				WHEN EXCLUDED.status IN ('running', 'waiting_human') THEN NULL
				ELSE orchestration_node.completed_at
			END,
			updated_at = now()
	`, planID, nodeKey, workflowNodeTitle(nodeKey), status, reasonCode, recommendedAction, attempt)
	return err
}

func workflowNodeTitle(nodeKey string) string {
	switch nodeKey {
	case "analyze":
		return "Analyze"
	case "dispatch":
		return "Dispatch agent task"
	case "validate":
		return "Validate result"
	case "review":
		return "Review handoff"
	default:
		return nodeKey
	}
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
		SELECT $1, $2, $3, $4, $5::jsonb
		WHERE NOT EXISTS (
			SELECT 1
			FROM orchestration_event
			WHERE plan_id = $1
				AND type = $2
				AND source = $3
				AND message = $4
				AND details = $5::jsonb
		)
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
		SELECT $1, $2, $3, $4, $5::jsonb
		WHERE NOT EXISTS (
			SELECT 1
			FROM orchestration_artifact
			WHERE plan_id = $1
				AND type = $2
				AND source = $3
				AND label = $4
				AND data = $5::jsonb
		)
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
	return withResultSchemaContract(b.String())
}

const resultSchemaV1Contract = `Authoritative Result Schema v1 contract:
- Return exactly one JSON object as your final assistant message, with no markdown fences and no prose.
- The daemon validates your final assistant message only. Do not use a shell command to echo the JSON and do not use multica issue comment add to deliver it.
- schema_version must be the string "1" exactly, not "1.0".
- Required shape: {"schema_version":"1","summary":"...","changed_files":["path/to/file"],"artifacts":[],"tests":[{"name":"npm test","status":"passed"}],"risks":[],"evidence":[{"type":"test","ref":"npm test"}]}
- changed_files must be a non-empty array of touched file paths.
- tests must be an array of objects with name and status. Use status "passed" or "skipped" for a successful orchestration.
- evidence must be a non-empty array of objects with type and ref. Include at least the verification command output reference.
- artifacts may be [] if there are no separate artifacts. If present, artifacts must use label and ref exactly.
- Do not output tests as an object like {"run":true,"passed":1}. Do not output artifacts as {"name":"...","path":"..."}.`

func withResultSchemaContract(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if strings.Contains(prompt, "Authoritative Result Schema v1 contract:") {
		return prompt
	}
	if prompt == "" {
		return resultSchemaV1Contract
	}
	return prompt + "\n\n" + resultSchemaV1Contract
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
