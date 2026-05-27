package orchestration

import (
	"errors"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/multica-ai/multica/server/internal/service"
)

const (
	IssueWorkflowName              = "IssueWorkflow"
	AgentTaskOutcomeSignalName     = "agent_task_outcome"
	ApprovalActionSignalName       = "approval_action"
	LoadIssueActivityName          = "orchestration.load_issue"
	AnalyzeIssueActivityName       = "orchestration.analyze_issue"
	DispatchTaskActivityName       = "orchestration.dispatch_daemon_task"
	ValidateOutcomeActivityName    = "orchestration.validate_outcome"
	ReviewOutcomeActivityName      = "orchestration.review_outcome"
	SummarizeOutcomeActivityName   = "orchestration.summarize_outcome"
	FinalizeWorkflowActivityName   = "orchestration.finalize_workflow"
	ProjectAnalysisActivityName    = "orchestration.project_analysis"
	ProjectSignalAuditActivityName = "orchestration.project_signal_audit"
	ProjectEinoFailureActivityName = "orchestration.project_eino_failure"
	maxNodeAttempts                = 2
)

type IssueWorkflowInput struct {
	WorkspaceID         string
	IssueID             string
	PlanID              string
	WorkflowID          string
	ReasoningProfileRef string
}

type IssueSnapshot struct {
	WorkspaceID    string
	IssueID        string
	Title          string
	Description    string
	AssigneeType   string
	AssigneeID     string
	Priority       string
	Status         string
	AcceptanceText string
}

type AnalyzeIssueResult struct {
	ProblemSummary         string
	ExecutionAdvice        string
	SuspectedContext       string
	Risks                  []string
	RecommendedAgentPrompt string
	ReasonCode             string
	RecommendedAction      string
	Trace                  EinoTrace
}

type ResultArtifactRef struct {
	Label string `json:"label"`
	Ref   string `json:"ref"`
}

type ResultTestRef struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type ResultEvidenceRef struct {
	Type string `json:"type"`
	Ref  string `json:"ref"`
}

type DispatchDaemonTaskInput struct {
	PlanID             string
	WorkflowNodeKey    string
	Attempt            int
	TemporalWorkflowID string
	AgentPrompt        string
}

type ValidateOutcomeInput struct {
	Outcome  service.AgentTaskOutcomeSignalInput
	Analysis AnalyzeIssueResult
	Issue    IssueSnapshot
	Dispatch service.DispatchAgentTaskResult
}

type SignalAuditInput struct {
	PlanID           string
	EventType        string
	Message          string
	ExpectedPlanID   string
	ExpectedNodeID   string
	ExpectedTaskID   string
	ExpectedAttempt  int
	ExpectedWorkflow string
	Outcome          service.AgentTaskOutcomeSignalInput
}

type EinoFailureProjectionInput struct {
	PlanID              string
	WorkflowNodeKey     string
	Attempt             int
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

type ValidateOutcomeResult struct {
	Status               string
	ReasonCode           string
	RecommendedAction    string
	NeedsHumanReview     bool
	ShouldRetry          bool
	TerminalPlanStatus   string
	ProjectionSummary    string
	ProjectionDetail     string
	FailedTests          []string
	Risks                []string
	ResultSummary        string
	ChangedFiles         []string
	Artifacts            []ResultArtifactRef
	Tests                []ResultTestRef
	Evidence             []ResultEvidenceRef
	PriorEvidenceSummary string
}

type ReviewOutcomeResult struct {
	Summary           string
	HighRisk          bool
	Concern           string
	SeverityLabel     string
	Evidence          []string
	Risks             []string
	RecommendedAction string
	Trace             EinoTrace
}

type SummarizeOutcomeResult struct {
	Summary  string
	TraceRef string
	Trace    EinoTrace
}

func IssueWorkflow(ctx workflow.Context, input IssueWorkflowInput) error {
	input.ReasoningProfileRef = normalizeReasoningProfileRef(input.ReasoningProfileRef)

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	einoCtx := workflow.WithActivityOptions(ctx, einoActivityOptions())

	var issue IssueSnapshot
	if err := workflow.ExecuteActivity(ctx, LoadIssueActivityName, input).Get(ctx, &issue); err != nil {
		return err
	}

	var analysis AnalyzeIssueResult
	if err := workflow.ExecuteActivity(einoCtx, AnalyzeIssueActivityName, issue, input).Get(ctx, &analysis); err != nil {
		return projectEinoFailureAndReturn(ctx, input, "analyze", 1, "multica_analyze_issue", err)
	}

	outcomeCh := workflow.GetSignalChannel(ctx, AgentTaskOutcomeSignalName)
	approvalCh := workflow.GetSignalChannel(ctx, ApprovalActionSignalName)

	for attempt := 1; attempt <= maxNodeAttempts; attempt++ {
		var dispatch service.DispatchAgentTaskResult
		if err := workflow.ExecuteActivity(ctx, DispatchTaskActivityName, DispatchDaemonTaskInput{
			PlanID:             input.PlanID,
			WorkflowNodeKey:    "dispatch",
			Attempt:            attempt,
			TemporalWorkflowID: input.WorkflowID,
			AgentPrompt:        analysis.RecommendedAgentPrompt,
		}).Get(ctx, &dispatch); err != nil {
			return err
		}

		outcome, err := waitForAgentTaskOutcome(ctx, input, dispatch, outcomeCh)
		if err != nil {
			return err
		}

		var validation ValidateOutcomeResult
		if err := workflow.ExecuteActivity(ctx, ValidateOutcomeActivityName, ValidateOutcomeInput{
			Outcome:  outcome,
			Analysis: analysis,
			Issue:    issue,
			Dispatch: dispatch,
		}).Get(ctx, &validation); err != nil {
			return err
		}
		if validation.ShouldRetry && attempt >= maxNodeAttempts {
			validation.ShouldRetry = false
			validation.NeedsHumanReview = true
			validation.TerminalPlanStatus = "waiting_human"
			validation.RecommendedAction = "review"
		}
		if validation.ShouldRetry && attempt < maxNodeAttempts {
			validation.Status = "failed"
			validation.TerminalPlanStatus = "running"
			validation.PriorEvidenceSummary = buildPriorEvidenceSummary(validation)
			if err := workflow.ExecuteActivity(ctx, FinalizeWorkflowActivityName, validation, ReviewOutcomeResult{}, SummarizeOutcomeResult{}, input, issue, analysis, dispatch, outcome).Get(ctx, nil); err != nil {
				return err
			}
			continue
		}

		var review ReviewOutcomeResult
		if err := workflow.ExecuteActivity(einoCtx, ReviewOutcomeActivityName, validation, analysis, issue, dispatch).Get(ctx, &review); err != nil {
			return projectEinoFailureAndReturn(ctx, input, "review", dispatch.Attempt, "multica_review_outcome", err)
		}
		validation = applyOutcomePolicy(validation, review)

		var summary SummarizeOutcomeResult
		if err := workflow.ExecuteActivity(einoCtx, SummarizeOutcomeActivityName, review, validation, analysis, issue, dispatch).Get(ctx, &summary); err != nil {
			return projectEinoFailureAndReturn(ctx, input, "summary", dispatch.Attempt, "multica_summarize_outcome", err)
		}

		if validation.NeedsHumanReview || validation.TerminalPlanStatus == "waiting_human" {
			if err := workflow.ExecuteActivity(ctx, FinalizeWorkflowActivityName, validation, review, summary, input, issue, analysis, dispatch, outcome).Get(ctx, nil); err != nil {
				return err
			}
			approval := waitForApprovalAction(ctx, input, dispatch, approvalCh)
			switch approval.Action {
			case "approve":
				validation.Status = "completed"
				validation.ReasonCode = "human_approved"
				validation.RecommendedAction = "none"
				validation.NeedsHumanReview = false
				validation.ShouldRetry = false
				validation.TerminalPlanStatus = "completed"
				validation.ProjectionDetail = "human approved orchestration outcome"
				return workflow.ExecuteActivity(ctx, FinalizeWorkflowActivityName, validation, review, summary, input, issue, analysis, dispatch, outcome).Get(ctx, nil)
			case "retry":
				if attempt < maxNodeAttempts {
					validation.Status = "running"
					validation.ReasonCode = "human_retry"
					validation.RecommendedAction = "none"
					validation.NeedsHumanReview = false
					validation.ShouldRetry = true
					validation.TerminalPlanStatus = "running"
					validation.ProjectionDetail = "human requested orchestration retry"
					if err := workflow.ExecuteActivity(ctx, FinalizeWorkflowActivityName, validation, review, summary, input, issue, analysis, dispatch, outcome).Get(ctx, nil); err != nil {
						return err
					}
					continue
				}
				validation.Status = "failed"
				validation.ReasonCode = "retry_exhausted"
				validation.RecommendedAction = "none"
				validation.NeedsHumanReview = false
				validation.ShouldRetry = false
				validation.TerminalPlanStatus = "failed"
				validation.ProjectionDetail = "orchestration retry budget exhausted"
				return workflow.ExecuteActivity(ctx, FinalizeWorkflowActivityName, validation, review, summary, input, issue, analysis, dispatch, outcome).Get(ctx, nil)
			}
		}

		return workflow.ExecuteActivity(ctx, FinalizeWorkflowActivityName, validation, review, summary, input, issue, analysis, dispatch, outcome).Get(ctx, nil)
	}
	return nil
}

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

func projectEinoFailureAndReturn(ctx workflow.Context, input IssueWorkflowInput, nodeKey string, attempt int, schemaName string, originalErr error) error {
	failure, ok := einoFailureFromError(originalErr)
	if !ok {
		return originalErr
	}
	if attempt <= 0 {
		attempt = 1
	}
	action := recommendedActionForEinoFailure(failure.ReasonCode)
	projection := EinoFailureProjectionInput{
		PlanID:              input.PlanID,
		WorkflowNodeKey:     nodeKey,
		Attempt:             attempt,
		ReasonCode:          failure.ReasonCode,
		RecommendedAction:   action,
		Message:             failure.Message,
		ReasoningProfileRef: normalizeReasoningProfileRef(input.ReasoningProfileRef),
		SchemaName:          schemaName,
		SchemaVersion:       "1",
		ProviderLabel:       EinoProviderOpenAICompatible,
		CapabilityMode:      "json_schema",
	}
	if err := workflow.ExecuteActivity(ctx, ProjectEinoFailureActivityName, projection).Get(ctx, nil); err != nil {
		return err
	}
	return originalErr
}

func einoFailureFromError(err error) (EinoFailureDetails, bool) {
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) {
		return EinoFailureDetails{}, false
	}
	reasonCode := strings.TrimPrefix(appErr.Type(), einoFailureErrorTypePrefix)
	if reasonCode == appErr.Type() || strings.TrimSpace(reasonCode) == "" {
		return EinoFailureDetails{}, false
	}
	details := EinoFailureDetails{
		ReasonCode: reasonCode,
		Message:    appErr.Message(),
	}
	if appErr.HasDetails() {
		var decoded EinoFailureDetails
		if decodeErr := appErr.Details(&decoded); decodeErr == nil {
			if strings.TrimSpace(decoded.ReasonCode) != "" {
				details.ReasonCode = strings.TrimSpace(decoded.ReasonCode)
			}
			if strings.TrimSpace(decoded.Message) != "" {
				details.Message = strings.TrimSpace(decoded.Message)
			}
		}
	}
	if strings.TrimSpace(details.Message) == "" {
		details.Message = appErr.Error()
	}
	return details, true
}

func buildPriorEvidenceSummary(validation ValidateOutcomeResult) string {
	parts := []string{}
	if detail := strings.TrimSpace(validation.ProjectionDetail); detail != "" {
		parts = append(parts, "reason: "+detail)
	}
	if summary := strings.TrimSpace(validation.ResultSummary); summary != "" {
		parts = append(parts, "summary: "+summary)
	}
	if len(validation.ChangedFiles) > 0 {
		parts = append(parts, "changed_files: "+strings.Join(validation.ChangedFiles, ", "))
	}
	if len(validation.Evidence) > 0 {
		refs := make([]string, 0, len(validation.Evidence))
		for _, evidence := range validation.Evidence {
			if ref := strings.TrimSpace(evidence.Ref); ref != "" {
				if typ := strings.TrimSpace(evidence.Type); typ != "" {
					refs = append(refs, typ+":"+ref)
				} else {
					refs = append(refs, ref)
				}
			}
		}
		if len(refs) > 0 {
			parts = append(parts, "evidence: "+strings.Join(refs, ", "))
		}
	}
	return strings.Join(parts, "\n")
}

func applyOutcomePolicy(validation ValidateOutcomeResult, review ReviewOutcomeResult) ValidateOutcomeResult {
	if validation.NeedsHumanReview || validation.ShouldRetry || validation.TerminalPlanStatus == "waiting_human" {
		return validation
	}
	if review.HighRisk {
		validation.Status = "waiting_human"
		validation.ReasonCode = "review_high_risk"
		validation.RecommendedAction = "review"
		validation.NeedsHumanReview = true
		validation.ShouldRetry = false
		validation.TerminalPlanStatus = "waiting_human"
		if review.Concern != "" {
			validation.ProjectionDetail = "advisory review flagged high-risk concern: " + review.Concern
		} else {
			validation.ProjectionDetail = "advisory review flagged a high-risk concern"
		}
	}
	return validation
}

func waitForAgentTaskOutcome(ctx workflow.Context, input IssueWorkflowInput, dispatch service.DispatchAgentTaskResult, outcomeCh workflow.ReceiveChannel) (service.AgentTaskOutcomeSignalInput, error) {
	var outcome service.AgentTaskOutcomeSignalInput
	received := false
	var auditErr error
	selector := workflow.NewSelector(ctx)
	selector.AddReceive(outcomeCh, func(c workflow.ReceiveChannel, _ bool) {
		var candidate service.AgentTaskOutcomeSignalInput
		c.Receive(ctx, &candidate)
		if matches, audit := correlateAgentTaskOutcome(input, dispatch, candidate); matches {
			outcome = candidate
			received = true
		} else {
			auditErr = workflow.ExecuteActivity(ctx, ProjectSignalAuditActivityName, audit).Get(ctx, nil)
		}
	})
	for !received {
		selector.Select(ctx)
		if auditErr != nil {
			return service.AgentTaskOutcomeSignalInput{}, auditErr
		}
	}
	return outcome, nil
}

func waitForApprovalAction(ctx workflow.Context, input IssueWorkflowInput, dispatch service.DispatchAgentTaskResult, approvalCh workflow.ReceiveChannel) service.ApprovalActionSignalInput {
	var approval service.ApprovalActionSignalInput
	received := false
	selector := workflow.NewSelector(ctx)
	selector.AddReceive(approvalCh, func(c workflow.ReceiveChannel, _ bool) {
		var candidate service.ApprovalActionSignalInput
		c.Receive(ctx, &candidate)
		if candidate.WorkflowID == input.WorkflowID &&
			candidate.PlanID == input.PlanID &&
			candidate.NodeID == dispatch.NodeID &&
			candidate.ActorType == "human" &&
			(candidate.Action == "approve" || candidate.Action == "retry") {
			approval = candidate
			received = true
		}
	})
	for !received {
		selector.Select(ctx)
	}
	return approval
}

func correlateAgentTaskOutcome(input IssueWorkflowInput, dispatch service.DispatchAgentTaskResult, outcome service.AgentTaskOutcomeSignalInput) (bool, SignalAuditInput) {
	matches := outcome.WorkflowID == input.WorkflowID &&
		outcome.PlanID == dispatch.PlanID &&
		outcome.NodeID == dispatch.NodeID &&
		outcome.TaskID == dispatch.TaskID &&
		outcome.Attempt == dispatch.Attempt &&
		outcome.OutcomeVersion == 1
	if matches {
		return true, SignalAuditInput{}
	}

	eventType := "signal.mismatched_rejected"
	message := "Agent Task outcome signal did not match the active orchestration node"
	if outcome.WorkflowID == input.WorkflowID &&
		outcome.PlanID == dispatch.PlanID &&
		outcome.Attempt < dispatch.Attempt {
		eventType = "signal.stale_ignored"
		message = "Agent Task outcome signal was for a stale orchestration attempt"
	}
	return false, SignalAuditInput{
		PlanID:           input.PlanID,
		EventType:        eventType,
		Message:          message,
		ExpectedPlanID:   dispatch.PlanID,
		ExpectedNodeID:   dispatch.NodeID,
		ExpectedTaskID:   dispatch.TaskID,
		ExpectedAttempt:  dispatch.Attempt,
		ExpectedWorkflow: input.WorkflowID,
		Outcome:          outcome,
	}
}
