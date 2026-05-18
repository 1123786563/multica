package orchestration

import (
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/multica-ai/multica/server/internal/service"
)

const (
	IssueWorkflowName              = "IssueWorkflow"
	AgentTaskOutcomeSignalName     = "agent_task_outcome"
	LoadIssueActivityName          = "orchestration.load_issue"
	AnalyzeIssueActivityName       = "orchestration.analyze_issue"
	DispatchTaskActivityName       = "orchestration.dispatch_daemon_task"
	ValidateOutcomeActivityName    = "orchestration.validate_outcome"
	ReviewOutcomeActivityName      = "orchestration.review_outcome"
	SummarizeOutcomeActivityName   = "orchestration.summarize_outcome"
	FinalizeWorkflowActivityName   = "orchestration.finalize_workflow"
	ProjectAnalysisActivityName    = "orchestration.project_analysis"
	ProjectSignalAuditActivityName = "orchestration.project_signal_audit"
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
	TerminalPlanStatus string
	ProjectionSummary  string
	ProjectionDetail   string
}

type ReviewOutcomeResult struct {
	Summary string
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

	var dispatch service.DispatchAgentTaskResult
	if err := workflow.ExecuteActivity(ctx, DispatchTaskActivityName, DispatchDaemonTaskInput{
		PlanID:             input.PlanID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: input.WorkflowID,
	}).Get(ctx, &dispatch); err != nil {
		return err
	}

	outcomeCh := workflow.GetSignalChannel(ctx, AgentTaskOutcomeSignalName)
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

	var validation ValidateOutcomeResult
	if err := workflow.ExecuteActivity(ctx, ValidateOutcomeActivityName, ValidateOutcomeInput{
		Outcome:  outcome,
		Analysis: analysis,
		Issue:    issue,
		Dispatch: dispatch,
	}).Get(ctx, &validation); err != nil {
		return err
	}

	var review ReviewOutcomeResult
	if err := workflow.ExecuteActivity(ctx, ReviewOutcomeActivityName, validation, analysis, issue, dispatch).Get(ctx, &review); err != nil {
		return err
	}

	var summary SummarizeOutcomeResult
	if err := workflow.ExecuteActivity(ctx, SummarizeOutcomeActivityName, review, validation, analysis, issue, dispatch).Get(ctx, &summary); err != nil {
		return err
	}

	return workflow.ExecuteActivity(ctx, FinalizeWorkflowActivityName, validation, review, summary, input, issue, analysis, dispatch, outcome).Get(ctx, nil)
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
