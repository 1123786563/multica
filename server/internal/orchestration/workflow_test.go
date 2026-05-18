package orchestration

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/multica-ai/multica/server/internal/service"
)

func registerIssueWorkflowTestActivities(env *testsuite.TestWorkflowEnvironment) {
	env.RegisterWorkflowWithOptions(IssueWorkflow, workflow.RegisterOptions{Name: IssueWorkflowName})
	env.RegisterActivityWithOptions(ActivitySet{}.LoadIssue, activity.RegisterOptions{Name: LoadIssueActivityName})
	env.RegisterActivityWithOptions(ActivitySet{}.AnalyzeIssue, activity.RegisterOptions{Name: AnalyzeIssueActivityName})
	env.RegisterActivityWithOptions(ActivitySet{}.DispatchDaemonTask, activity.RegisterOptions{Name: DispatchTaskActivityName})
	env.RegisterActivityWithOptions(ActivitySet{}.ValidateOutcome, activity.RegisterOptions{Name: ValidateOutcomeActivityName})
	env.RegisterActivityWithOptions(ActivitySet{}.ReviewOutcome, activity.RegisterOptions{Name: ReviewOutcomeActivityName})
	env.RegisterActivityWithOptions(ActivitySet{}.SummarizeOutcome, activity.RegisterOptions{Name: SummarizeOutcomeActivityName})
	env.RegisterActivityWithOptions(ActivitySet{}.FinalizeWorkflow, activity.RegisterOptions{Name: FinalizeWorkflowActivityName})
	env.RegisterActivityWithOptions(ActivitySet{}.ProjectAnalysis, activity.RegisterOptions{Name: ProjectAnalysisActivityName})
	env.RegisterActivityWithOptions(ActivitySet{}.ProjectSignalAudit, activity.RegisterOptions{Name: ProjectSignalAuditActivityName})
}

func TestIssueWorkflowWaitsForOutcomeSignalAndFinalizes(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	registerIssueWorkflowTestActivities(env)

	env.OnActivity(LoadIssueActivityName, mock.Anything, mock.Anything).Return(IssueSnapshot{
		IssueID:     "issue-1",
		WorkspaceID: "ws-1",
		Title:       "Fix the orchestration path",
		Description: "Make the workflow observable",
	}, nil)
	env.OnActivity(AnalyzeIssueActivityName, mock.Anything, mock.Anything, mock.Anything).Return(AnalyzeIssueResult{
		ProblemSummary:         "Fix the orchestration path",
		ExecutionAdvice:        "Keep the flow deterministic",
		RecommendedAgentPrompt: "Implement the orchestration fix",
		ReasonCode:             "analysis_ready",
		RecommendedAction:      "none",
	}, nil)
	env.OnActivity(DispatchTaskActivityName, mock.Anything, mock.Anything).Return(service.DispatchAgentTaskResult{
		PlanID:  "plan-1",
		TaskID:  "task-1",
		NodeID:  "node-1",
		Attempt: 1,
	}, nil)
	env.OnActivity(ProjectSignalAuditActivityName, mock.Anything, mock.MatchedBy(func(input SignalAuditInput) bool {
		return input.EventType == "signal.mismatched_rejected" &&
			input.Outcome.TaskID == "old-task" &&
			input.ExpectedTaskID == "task-1"
	})).Return(nil).Once()
	env.OnActivity(ValidateOutcomeActivityName, mock.Anything, mock.Anything).Return(ValidateOutcomeResult{
		Status:             "completed",
		TerminalPlanStatus: "completed",
	}, nil)
	env.OnActivity(ReviewOutcomeActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ReviewOutcomeResult{Summary: "review"}, nil)
	env.OnActivity(SummarizeOutcomeActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(SummarizeOutcomeResult{Summary: "summary"}, nil)
	env.OnActivity(FinalizeWorkflowActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AgentTaskOutcomeSignalName, service.AgentTaskOutcomeSignalInput{
			WorkflowID: "wf-1",
			PlanID:     "plan-1",
			NodeID:     "node-1",
			TaskID:     "task-1",
			Attempt:    1,
			Status:     "completed",
			Result:     json.RawMessage(`{"ok":true}`),
		})
	}, time.Second)

	env.ExecuteWorkflow(IssueWorkflow, IssueWorkflowInput{
		WorkspaceID: "ws-1",
		IssueID:     "issue-1",
		PlanID:      "plan-1",
		WorkflowID:  "wf-1",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
}

func TestIssueWorkflowIgnoresMismatchedTaskSignalUntilMatchingOutcomeArrives(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	registerIssueWorkflowTestActivities(env)

	env.OnActivity(LoadIssueActivityName, mock.Anything, mock.Anything).Return(IssueSnapshot{
		IssueID:     "issue-1",
		WorkspaceID: "ws-1",
		Title:       "Fix the orchestration path",
	}, nil)
	env.OnActivity(AnalyzeIssueActivityName, mock.Anything, mock.Anything, mock.Anything).Return(AnalyzeIssueResult{
		ProblemSummary:         "Fix the orchestration path",
		ExecutionAdvice:        "Keep the flow deterministic",
		RecommendedAgentPrompt: "Implement the orchestration fix",
		ReasonCode:             "analysis_ready",
		RecommendedAction:      "none",
	}, nil)
	env.OnActivity(DispatchTaskActivityName, mock.Anything, mock.Anything).Return(service.DispatchAgentTaskResult{
		PlanID:  "plan-1",
		TaskID:  "task-1",
		NodeID:  "node-1",
		Attempt: 1,
	}, nil)
	env.OnActivity(ProjectSignalAuditActivityName, mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity(ValidateOutcomeActivityName, mock.Anything, mock.Anything).Return(ValidateOutcomeResult{
		Status:             "completed",
		TerminalPlanStatus: "completed",
	}, nil).Once()
	env.OnActivity(ReviewOutcomeActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ReviewOutcomeResult{Summary: "review"}, nil)
	env.OnActivity(SummarizeOutcomeActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(SummarizeOutcomeResult{Summary: "summary"}, nil)
	env.OnActivity(FinalizeWorkflowActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.MatchedBy(func(outcome service.AgentTaskOutcomeSignalInput) bool {
		return outcome.TaskID == "task-1"
	})).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AgentTaskOutcomeSignalName, service.AgentTaskOutcomeSignalInput{
			WorkflowID: "wf-1",
			PlanID:     "plan-1",
			NodeID:     "node-1",
			TaskID:     "old-task",
			Attempt:    1,
			Status:     "completed",
			Result:     json.RawMessage(`{"ok":false}`),
		})
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AgentTaskOutcomeSignalName, service.AgentTaskOutcomeSignalInput{
			WorkflowID: "wf-1",
			PlanID:     "plan-1",
			NodeID:     "node-1",
			TaskID:     "task-1",
			Attempt:    1,
			Status:     "completed",
			Result:     json.RawMessage(`{"ok":true}`),
		})
	}, 2*time.Second)

	env.ExecuteWorkflow(IssueWorkflow, IssueWorkflowInput{
		WorkspaceID: "ws-1",
		IssueID:     "issue-1",
		PlanID:      "plan-1",
		WorkflowID:  "wf-1",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

func TestIssueWorkflowRetriesEvidenceInsufficientOnceBeforeFinalizing(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	registerIssueWorkflowTestActivities(env)

	env.OnActivity(LoadIssueActivityName, mock.Anything, mock.Anything).Return(IssueSnapshot{
		IssueID:     "issue-1",
		WorkspaceID: "ws-1",
		Title:       "Fix the orchestration path",
	}, nil)
	env.OnActivity(AnalyzeIssueActivityName, mock.Anything, mock.Anything, mock.Anything).Return(AnalyzeIssueResult{
		ProblemSummary:         "Fix the orchestration path",
		ExecutionAdvice:        "Keep the flow deterministic",
		RecommendedAgentPrompt: "Implement the orchestration fix",
		ReasonCode:             "analysis_ready",
		RecommendedAction:      "none",
	}, nil)
	env.OnActivity(DispatchTaskActivityName, mock.Anything, mock.MatchedBy(func(input DispatchDaemonTaskInput) bool {
		return input.Attempt == 1
	})).Return(service.DispatchAgentTaskResult{
		PlanID:  "plan-1",
		TaskID:  "task-1",
		NodeID:  "node-1",
		Attempt: 1,
	}, nil).Once()
	env.OnActivity(DispatchTaskActivityName, mock.Anything, mock.MatchedBy(func(input DispatchDaemonTaskInput) bool {
		return input.Attempt == 2
	})).Return(service.DispatchAgentTaskResult{
		PlanID:  "plan-1",
		TaskID:  "task-2",
		NodeID:  "node-2",
		Attempt: 2,
	}, nil).Once()
	env.OnActivity(ValidateOutcomeActivityName, mock.Anything, mock.MatchedBy(func(input ValidateOutcomeInput) bool {
		return input.Dispatch.Attempt == 1
	})).Return(ValidateOutcomeResult{
		Status:            "waiting_human",
		ReasonCode:        "evidence_insufficient",
		RecommendedAction: "retry",
		ShouldRetry:       true,
		ProjectionDetail:  "missing evidence references",
	}, nil).Once()
	env.OnActivity(ValidateOutcomeActivityName, mock.Anything, mock.MatchedBy(func(input ValidateOutcomeInput) bool {
		return input.Dispatch.Attempt == 2
	})).Return(ValidateOutcomeResult{
		Status:             "completed",
		RecommendedAction:  "none",
		TerminalPlanStatus: "completed",
		ProjectionDetail:   "structured result validated",
	}, nil).Once()
	env.OnActivity(ReviewOutcomeActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ReviewOutcomeResult{Summary: "review"}, nil).Twice()
	env.OnActivity(SummarizeOutcomeActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(SummarizeOutcomeResult{Summary: "summary"}, nil).Twice()
	env.OnActivity(FinalizeWorkflowActivityName, mock.Anything, mock.MatchedBy(func(validation ValidateOutcomeResult) bool {
		return validation.ShouldRetry &&
			validation.Status == "failed" &&
			validation.TerminalPlanStatus == "running"
	}), mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.MatchedBy(func(dispatch service.DispatchAgentTaskResult) bool {
		return dispatch.Attempt == 1 && dispatch.TaskID == "task-1"
	}), mock.MatchedBy(func(outcome service.AgentTaskOutcomeSignalInput) bool {
		return outcome.Attempt == 1 && outcome.TaskID == "task-1"
	})).Return(nil).Once()
	env.OnActivity(FinalizeWorkflowActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.MatchedBy(func(dispatch service.DispatchAgentTaskResult) bool {
		return dispatch.Attempt == 2 && dispatch.TaskID == "task-2"
	}), mock.MatchedBy(func(outcome service.AgentTaskOutcomeSignalInput) bool {
		return outcome.Attempt == 2 && outcome.TaskID == "task-2"
	})).Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AgentTaskOutcomeSignalName, service.AgentTaskOutcomeSignalInput{
			WorkflowID: "wf-1",
			PlanID:     "plan-1",
			NodeID:     "node-1",
			TaskID:     "task-1",
			Attempt:    1,
			Status:     "completed",
			Result:     json.RawMessage(`{"schema_version":"1","summary":"missing evidence","changed_files":[],"artifacts":[],"tests":[],"risks":[],"evidence":[]}`),
		})
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AgentTaskOutcomeSignalName, service.AgentTaskOutcomeSignalInput{
			WorkflowID: "wf-1",
			PlanID:     "plan-1",
			NodeID:     "node-2",
			TaskID:     "task-2",
			Attempt:    2,
			Status:     "completed",
			Result:     json.RawMessage(`{"schema_version":"1","summary":"done","changed_files":["server/a.go"],"artifacts":[],"tests":[{"name":"go test","status":"passed"}],"risks":[],"evidence":[{"type":"test","ref":"go test ./..."}]}`),
		})
	}, 2*time.Second)

	env.ExecuteWorkflow(IssueWorkflow, IssueWorkflowInput{
		WorkspaceID: "ws-1",
		IssueID:     "issue-1",
		PlanID:      "plan-1",
		WorkflowID:  "wf-1",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

func TestIssueWorkflowWaitsForApprovalSignalBeforeCompletingHumanGate(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	registerIssueWorkflowTestActivities(env)

	env.OnActivity(LoadIssueActivityName, mock.Anything, mock.Anything).Return(IssueSnapshot{
		IssueID:     "issue-1",
		WorkspaceID: "ws-1",
		Title:       "Fix the orchestration path",
	}, nil)
	env.OnActivity(AnalyzeIssueActivityName, mock.Anything, mock.Anything, mock.Anything).Return(AnalyzeIssueResult{
		ProblemSummary:         "Fix the orchestration path",
		ExecutionAdvice:        "Keep the flow deterministic",
		RecommendedAgentPrompt: "Implement the orchestration fix",
		ReasonCode:             "analysis_ready",
		RecommendedAction:      "none",
	}, nil)
	env.OnActivity(DispatchTaskActivityName, mock.Anything, mock.Anything).Return(service.DispatchAgentTaskResult{
		PlanID:  "plan-1",
		TaskID:  "task-1",
		NodeID:  "node-1",
		Attempt: 1,
	}, nil).Once()
	env.OnActivity(ValidateOutcomeActivityName, mock.Anything, mock.Anything).Return(ValidateOutcomeResult{
		Status:             "waiting_human",
		ReasonCode:         "tests_failed",
		RecommendedAction:  "approve_or_retry",
		NeedsHumanReview:   true,
		TerminalPlanStatus: "waiting_human",
		ProjectionDetail:   "reported tests failed",
	}, nil).Once()
	env.OnActivity(ReviewOutcomeActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ReviewOutcomeResult{Summary: "review"}, nil).Once()
	env.OnActivity(SummarizeOutcomeActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(SummarizeOutcomeResult{Summary: "summary"}, nil).Once()
	env.OnActivity(FinalizeWorkflowActivityName, mock.Anything, mock.MatchedBy(func(validation ValidateOutcomeResult) bool {
		return validation.TerminalPlanStatus == "waiting_human"
	}), mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity(FinalizeWorkflowActivityName, mock.Anything, mock.MatchedBy(func(validation ValidateOutcomeResult) bool {
		return validation.TerminalPlanStatus == "completed" && validation.ReasonCode == "human_approved"
	}), mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AgentTaskOutcomeSignalName, service.AgentTaskOutcomeSignalInput{
			WorkflowID: "wf-1",
			PlanID:     "plan-1",
			NodeID:     "node-1",
			TaskID:     "task-1",
			Attempt:    1,
			Status:     "completed",
			Result:     json.RawMessage(`{"schema_version":"1","summary":"done","changed_files":["server/a.go"],"artifacts":[],"tests":[{"name":"go test","status":"failed"}],"risks":[],"evidence":[{"type":"test","ref":"go test ./..."}]}`),
		})
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ApprovalActionSignalName, service.ApprovalActionSignalInput{
			WorkflowID: "wf-1",
			PlanID:     "plan-1",
			NodeID:     "node-1",
			ActorID:    "user-1",
			ActorType:  "human",
			Action:     "approve",
			Reason:     "accept risk",
		})
	}, 2*time.Second)

	env.ExecuteWorkflow(IssueWorkflow, IssueWorkflowInput{
		WorkspaceID: "ws-1",
		IssueID:     "issue-1",
		PlanID:      "plan-1",
		WorkflowID:  "wf-1",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

func TestIssueWorkflowRoutesHighRiskReviewConcernToHumanGate(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	registerIssueWorkflowTestActivities(env)

	env.OnActivity(LoadIssueActivityName, mock.Anything, mock.Anything).Return(IssueSnapshot{
		IssueID:     "issue-1",
		WorkspaceID: "ws-1",
		Title:       "Fix the orchestration path",
	}, nil)
	env.OnActivity(AnalyzeIssueActivityName, mock.Anything, mock.Anything, mock.Anything).Return(AnalyzeIssueResult{
		ProblemSummary:         "Fix the orchestration path",
		ExecutionAdvice:        "Keep the flow deterministic",
		RecommendedAgentPrompt: "Implement the orchestration fix",
		ReasonCode:             "analysis_ready",
		RecommendedAction:      "none",
	}, nil)
	env.OnActivity(DispatchTaskActivityName, mock.Anything, mock.Anything).Return(service.DispatchAgentTaskResult{
		PlanID:  "plan-1",
		TaskID:  "task-1",
		NodeID:  "node-1",
		Attempt: 1,
	}, nil).Once()
	env.OnActivity(ValidateOutcomeActivityName, mock.Anything, mock.Anything).Return(ValidateOutcomeResult{
		Status:             "completed",
		RecommendedAction:  "none",
		TerminalPlanStatus: "completed",
		ProjectionDetail:   "structured result validated",
	}, nil).Once()
	env.OnActivity(ReviewOutcomeActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ReviewOutcomeResult{
		Summary:       "review flagged destructive operation",
		HighRisk:      true,
		Concern:       "destructive database migration",
		SeverityLabel: "high",
	}, nil).Once()
	env.OnActivity(SummarizeOutcomeActivityName, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(SummarizeOutcomeResult{Summary: "summary"}, nil).Once()
	env.OnActivity(FinalizeWorkflowActivityName, mock.Anything, mock.MatchedBy(func(validation ValidateOutcomeResult) bool {
		return validation.TerminalPlanStatus == "waiting_human" &&
			validation.ReasonCode == "review_high_risk" &&
			validation.RecommendedAction == "review"
	}), mock.MatchedBy(func(review ReviewOutcomeResult) bool {
		return review.HighRisk && review.Concern == "destructive database migration"
	}), mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity(FinalizeWorkflowActivityName, mock.Anything, mock.MatchedBy(func(validation ValidateOutcomeResult) bool {
		return validation.TerminalPlanStatus == "completed" && validation.ReasonCode == "human_approved"
	}), mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AgentTaskOutcomeSignalName, service.AgentTaskOutcomeSignalInput{
			WorkflowID: "wf-1",
			PlanID:     "plan-1",
			NodeID:     "node-1",
			TaskID:     "task-1",
			Attempt:    1,
			Status:     "completed",
			Result:     json.RawMessage(`{"schema_version":"1","summary":"done","changed_files":["server/a.go"],"artifacts":[],"tests":[{"name":"go test","status":"passed"}],"risks":[],"evidence":[{"type":"test","ref":"go test ./..."}]}`),
		})
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ApprovalActionSignalName, service.ApprovalActionSignalInput{
			WorkflowID: "wf-1",
			PlanID:     "plan-1",
			NodeID:     "node-1",
			ActorID:    "user-1",
			ActorType:  "human",
			Action:     "approve",
			Reason:     "reviewed concern",
		})
	}, 2*time.Second)

	env.ExecuteWorkflow(IssueWorkflow, IssueWorkflowInput{
		WorkspaceID: "ws-1",
		IssueID:     "issue-1",
		PlanID:      "plan-1",
		WorkflowID:  "wf-1",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	env.AssertExpectations(t)
}

func TestCorrelateAgentTaskOutcomeClassifiesIgnoredSignals(t *testing.T) {
	input := IssueWorkflowInput{PlanID: "plan-1", WorkflowID: "wf-1"}
	dispatch := service.DispatchAgentTaskResult{
		PlanID:  "plan-1",
		NodeID:  "node-1",
		TaskID:  "task-1",
		Attempt: 2,
	}

	cases := []struct {
		name      string
		outcome   service.AgentTaskOutcomeSignalInput
		eventType string
	}{
		{
			name: "stale attempt",
			outcome: service.AgentTaskOutcomeSignalInput{
				WorkflowID: "wf-1",
				PlanID:     "plan-1",
				NodeID:     "node-1",
				TaskID:     "task-1",
				Attempt:    1,
			},
			eventType: "signal.stale_ignored",
		},
		{
			name: "wrong task",
			outcome: service.AgentTaskOutcomeSignalInput{
				WorkflowID: "wf-1",
				PlanID:     "plan-1",
				NodeID:     "node-1",
				TaskID:     "task-old",
				Attempt:    2,
			},
			eventType: "signal.mismatched_rejected",
		},
		{
			name: "wrong node",
			outcome: service.AgentTaskOutcomeSignalInput{
				WorkflowID: "wf-1",
				PlanID:     "plan-1",
				NodeID:     "node-old",
				TaskID:     "task-1",
				Attempt:    2,
			},
			eventType: "signal.mismatched_rejected",
		},
		{
			name: "wrong plan",
			outcome: service.AgentTaskOutcomeSignalInput{
				WorkflowID: "wf-1",
				PlanID:     "plan-old",
				NodeID:     "node-1",
				TaskID:     "task-1",
				Attempt:    2,
			},
			eventType: "signal.mismatched_rejected",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matches, audit := correlateAgentTaskOutcome(input, dispatch, tc.outcome)
			if matches {
				t.Fatal("unexpected matching signal")
			}
			if audit.EventType != tc.eventType {
				t.Fatalf("event type = %q, want %q", audit.EventType, tc.eventType)
			}
			if audit.ExpectedTaskID != dispatch.TaskID || audit.ExpectedAttempt != dispatch.Attempt {
				t.Fatalf("audit expected identity not preserved: %+v", audit)
			}
		})
	}

	matches, audit := correlateAgentTaskOutcome(input, dispatch, service.AgentTaskOutcomeSignalInput{
		WorkflowID: "wf-1",
		PlanID:     "plan-1",
		NodeID:     "node-1",
		TaskID:     "task-1",
		Attempt:    2,
	})
	if !matches {
		t.Fatalf("matching outcome was rejected: %+v", audit)
	}
}
