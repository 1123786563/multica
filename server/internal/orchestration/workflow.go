package orchestration

import (
	"time"

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
	maxNodeAttempts                = 2
)

type IssueWorkflowInput struct {
	WorkspaceID string
	IssueID     string
	PlanID      string
	WorkflowID  string
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
}

type DispatchDaemonTaskInput struct {
	PlanID             string
	WorkflowNodeKey    string
	Attempt            int
	TemporalWorkflowID string
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

type ValidateOutcomeResult struct {
	Status             string
	ReasonCode         string
	RecommendedAction  string
	NeedsHumanReview   bool
	ShouldRetry        bool
	TerminalPlanStatus string
	ProjectionSummary  string
	ProjectionDetail   string
	FailedTests        []string
	Risks              []string
}

type ReviewOutcomeResult struct {
	Summary       string
	HighRisk      bool
	Concern       string
	SeverityLabel string
}

type SummarizeOutcomeResult struct {
	Summary string
}

func IssueWorkflow(ctx workflow.Context, input IssueWorkflowInput) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var issue IssueSnapshot
	if err := workflow.ExecuteActivity(ctx, LoadIssueActivityName, input).Get(ctx, &issue); err != nil {
		return err
	}

	var analysis AnalyzeIssueResult
	if err := workflow.ExecuteActivity(ctx, AnalyzeIssueActivityName, issue, input).Get(ctx, &analysis); err != nil {
		return err
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

		var review ReviewOutcomeResult
		if err := workflow.ExecuteActivity(ctx, ReviewOutcomeActivityName, validation, analysis, issue, dispatch).Get(ctx, &review); err != nil {
			return err
		}
		validation = applyOutcomePolicy(validation, review)

		var summary SummarizeOutcomeResult
		if err := workflow.ExecuteActivity(ctx, SummarizeOutcomeActivityName, review, validation, analysis, issue, dispatch).Get(ctx, &summary); err != nil {
			return err
		}

		if validation.ShouldRetry && attempt < maxNodeAttempts {
			validation.Status = "failed"
			validation.TerminalPlanStatus = "running"
			if err := workflow.ExecuteActivity(ctx, FinalizeWorkflowActivityName, validation, review, summary, input, issue, analysis, dispatch, outcome).Get(ctx, nil); err != nil {
				return err
			}
			continue
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
			case "cancel":
				validation.Status = "cancelled"
				validation.ReasonCode = "human_cancelled"
				validation.RecommendedAction = "none"
				validation.NeedsHumanReview = false
				validation.ShouldRetry = false
				validation.TerminalPlanStatus = "cancelled"
				validation.ProjectionDetail = "human cancelled orchestration"
				return workflow.ExecuteActivity(ctx, FinalizeWorkflowActivityName, validation, review, summary, input, issue, analysis, dispatch, outcome).Get(ctx, nil)
			}
		}

		return workflow.ExecuteActivity(ctx, FinalizeWorkflowActivityName, validation, review, summary, input, issue, analysis, dispatch, outcome).Get(ctx, nil)
	}
	return nil
}

func applyOutcomePolicy(validation ValidateOutcomeResult, review ReviewOutcomeResult) ValidateOutcomeResult {
	if validation.NeedsHumanReview || validation.TerminalPlanStatus == "waiting_human" {
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
	selector := workflow.NewSelector(ctx)
	selector.AddReceive(outcomeCh, func(c workflow.ReceiveChannel, _ bool) {
		var candidate service.AgentTaskOutcomeSignalInput
		c.Receive(ctx, &candidate)
		if matches, audit := correlateAgentTaskOutcome(input, dispatch, candidate); matches {
			outcome = candidate
			received = true
		} else {
			_ = workflow.ExecuteActivity(ctx, ProjectSignalAuditActivityName, audit).Get(ctx, nil)
		}
	})
	for !received {
		selector.Select(ctx)
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
			(candidate.Action == "approve" || candidate.Action == "retry" || candidate.Action == "cancel") {
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
		outcome.Attempt == dispatch.Attempt
	if matches {
		return true, SignalAuditInput{}
	}

	eventType := "signal.mismatched_rejected"
	message := "Agent Task outcome signal did not match the active orchestration node"
	if outcome.PlanID == dispatch.PlanID && outcome.NodeID == dispatch.NodeID && outcome.TaskID == dispatch.TaskID && outcome.Attempt < dispatch.Attempt {
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
