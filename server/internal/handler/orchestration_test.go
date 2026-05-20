package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	orchestrationpkg "github.com/multica-ai/multica/server/internal/orchestration"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func createOrchestrationTestIssue(t *testing.T, title string) string {
	t.Helper()

	agentID := createHandlerTestAgent(t, title+" agent", []byte(`{}`))
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           title,
		"status":          "backlog",
		"priority":        "none",
		"assignee_type":   "agent",
		"assignee_id":     agentID,
		"allow_duplicate": true,
	})
	w := httptest.NewRecorder()
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var issue IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &issue); err != nil {
		t.Fatalf("decode issue response: %v", err)
	}
	t.Cleanup(func() {
		cleanupReq := newRequest("DELETE", "/api/issues/"+issue.ID, nil)
		cleanupW := httptest.NewRecorder()
		testHandler.DeleteIssue(cleanupW, cleanupReq)
	})

	return issue.ID
}

func TestStartIssueOrchestrationFailClosedWhenTemporalUnavailable(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration fail closed")

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("StartIssueOrchestration: expected 503, got %d: %s", w.Code, w.Body.String())
	}

	var taskCount int
	if err := testPool.QueryRow(t.Context(), `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1`, issueID).Scan(&taskCount); err != nil {
		t.Fatalf("count agent tasks: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("expected no direct agent task fallback, got %d queued tasks", taskCount)
	}

	readW := httptest.NewRecorder()
	readReq := withURLParam(newRequest("GET", "/api/issues/"+issueID+"/orchestration", nil), "id", issueID)
	testHandler.GetIssueOrchestration(readW, readReq)
	if readW.Code != http.StatusOK {
		t.Fatalf("GetIssueOrchestration: expected 200, got %d: %s", readW.Code, readW.Body.String())
	}

	var snapshot struct {
		Plans []struct {
			Status  string `json:"status"`
			Summary struct {
				ReasonCode        string `json:"reason_code"`
				RecommendedAction string `json:"recommended_action"`
			} `json:"summary"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(readW.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode orchestration snapshot: %v", err)
	}
	if len(snapshot.Plans) != 1 {
		t.Fatalf("expected one projected orchestration plan, got %d: %s", len(snapshot.Plans), readW.Body.String())
	}
	if snapshot.Plans[0].Status != "failed" {
		t.Fatalf("expected failed plan, got %q", snapshot.Plans[0].Status)
	}
	if snapshot.Plans[0].Summary.ReasonCode != "temporal_unavailable" {
		t.Fatalf("expected temporal_unavailable reason, got %q", snapshot.Plans[0].Summary.ReasonCode)
	}
	if snapshot.Plans[0].Summary.RecommendedAction != "configure_temporal" {
		t.Fatalf("expected configure_temporal action, got %q", snapshot.Plans[0].Summary.RecommendedAction)
	}
}

func TestStartIssueOrchestrationFailClosedCreatesAttentionForIssueRelevantHumansOnly(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration fail closed attention")
	relevantMemberID := createRuntimeLocalSkillTestMember(t, "member")
	humanAssigneeID := createRuntimeLocalSkillTestMember(t, "member")
	unrelatedMemberID := createRuntimeLocalSkillTestMember(t, "member")
	agentSubscriberID := createHandlerTestAgent(t, "attention agent subscriber", []byte(`{}`))

	if _, err := testPool.Exec(t.Context(), `
		UPDATE issue
		SET assignee_type = 'member', assignee_id = $2
		WHERE id = $1
	`, issueID, humanAssigneeID); err != nil {
		t.Fatalf("set human assignee: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		INSERT INTO issue_subscriber (issue_id, user_type, user_id, reason)
		VALUES
			($1, 'member', $2, 'manual'),
			($1, 'agent', $3, 'manual')
		ON CONFLICT (issue_id, user_type, user_id) DO NOTHING
	`, issueID, relevantMemberID, agentSubscriberID); err != nil {
		t.Fatalf("seed subscribers: %v", err)
	}

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("StartIssueOrchestration: expected 503, got %d: %s", w.Code, w.Body.String())
	}

	var attentionComments int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM comment
		WHERE issue_id = $1
			AND author_type = 'member'
			AND type = 'system'
			AND content LIKE '%Temporal unavailable%'
	`, issueID).Scan(&attentionComments); err != nil {
		t.Fatalf("count attention comments: %v", err)
	}
	if attentionComments != 1 {
		t.Fatalf("expected one attention comment, got %d", attentionComments)
	}

	assertInboxCount := func(userType, userID string, want int) {
		t.Helper()
		var count int
		if err := testPool.QueryRow(t.Context(), `
			SELECT count(*)
			FROM inbox_item
			WHERE issue_id = $1
				AND recipient_type = $2
				AND recipient_id = $3
				AND type = 'orchestration_attention'
				AND severity = 'attention'
		`, issueID, userType, userID).Scan(&count); err != nil {
			t.Fatalf("count inbox for %s/%s: %v", userType, userID, err)
		}
		if count != want {
			t.Fatalf("inbox count for %s/%s = %d, want %d", userType, userID, count, want)
		}
	}
	assertInboxCount("member", testUserID, 1)
	assertInboxCount("member", humanAssigneeID, 1)
	assertInboxCount("member", relevantMemberID, 1)
	assertInboxCount("member", unrelatedMemberID, 0)
	assertInboxCount("agent", agentSubscriberID, 0)
}

func TestFinalizeWorkflowWaitingHumanCreatesAttention(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration waiting human attention")
	relevantMemberID := createRuntimeLocalSkillTestMember(t, "member")
	if _, err := testPool.Exec(t.Context(), `
		INSERT INTO issue_subscriber (issue_id, user_type, user_id, reason)
		VALUES ($1, 'member', $2, 'manual')
		ON CONFLICT (issue_id, user_type, user_id) DO NOTHING
	`, issueID, relevantMemberID); err != nil {
		t.Fatalf("seed subscriber: %v", err)
	}

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}

	activity := orchestrationpkg.ActivitySet{DB: testPool}
	if err := activity.FinalizeWorkflow(t.Context(), orchestrationpkg.ValidateOutcomeResult{
		Status:             "waiting_human",
		ReasonCode:         "tests_failed",
		RecommendedAction:  "review",
		NeedsHumanReview:   true,
		TerminalPlanStatus: "waiting_human",
		ProjectionDetail:   "structured result reported failed tests",
		FailedTests:        []string{"TestBilling"},
	}, orchestrationpkg.ReviewOutcomeResult{
		Summary:           "review failed tests",
		RecommendedAction: "review",
	}, orchestrationpkg.SummarizeOutcomeResult{
		Summary:  "handoff needs review",
		TraceRef: startBody.Plan.ID + "/" + dispatched.NodeID + "/" + dispatched.TaskID,
	}, orchestrationpkg.IssueWorkflowInput{
		PlanID:     startBody.Plan.ID,
		WorkflowID: startBody.Plan.TemporalWorkflowID,
	}, orchestrationpkg.IssueSnapshot{
		IssueID: issueID,
	}, orchestrationpkg.AnalyzeIssueResult{
		ProblemSummary: "Fix billing",
	}, dispatched, service.AgentTaskOutcomeSignalInput{
		WorkflowID: startBody.Plan.TemporalWorkflowID,
		Status:     "completed",
	}); err != nil {
		t.Fatalf("finalize workflow: %v", err)
	}
	if err := activity.FinalizeWorkflow(t.Context(), orchestrationpkg.ValidateOutcomeResult{
		Status:             "waiting_human",
		ReasonCode:         "tests_failed",
		RecommendedAction:  "review",
		NeedsHumanReview:   true,
		TerminalPlanStatus: "waiting_human",
		ProjectionDetail:   "structured result reported failed tests",
		FailedTests:        []string{"TestBilling"},
	}, orchestrationpkg.ReviewOutcomeResult{
		Summary:           "review failed tests",
		RecommendedAction: "review",
	}, orchestrationpkg.SummarizeOutcomeResult{
		Summary:  "handoff needs review",
		TraceRef: startBody.Plan.ID + "/" + dispatched.NodeID + "/" + dispatched.TaskID,
	}, orchestrationpkg.IssueWorkflowInput{
		PlanID:     startBody.Plan.ID,
		WorkflowID: startBody.Plan.TemporalWorkflowID,
	}, orchestrationpkg.IssueSnapshot{
		IssueID: issueID,
	}, orchestrationpkg.AnalyzeIssueResult{
		ProblemSummary: "Fix billing",
	}, dispatched, service.AgentTaskOutcomeSignalInput{
		WorkflowID: startBody.Plan.TemporalWorkflowID,
		Status:     "completed",
	}); err != nil {
		t.Fatalf("repeat finalize workflow: %v", err)
	}

	var comments int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM comment
		WHERE issue_id = $1
			AND type = 'system'
			AND content LIKE '%Approval required%'
	`, issueID).Scan(&comments); err != nil {
		t.Fatalf("count attention comments: %v", err)
	}
	if comments != 1 {
		t.Fatalf("expected one waiting_human attention comment, got %d", comments)
	}
	var inbox int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM inbox_item
		WHERE issue_id = $1
			AND recipient_type = 'member'
			AND recipient_id = $2
			AND type = 'orchestration_attention'
	`, issueID, relevantMemberID).Scan(&inbox); err != nil {
		t.Fatalf("count relevant inbox: %v", err)
	}
	if inbox != 1 {
		t.Fatalf("expected relevant member inbox attention, got %d", inbox)
	}
}

func TestFinalizeWorkflowRetryExhaustedCreatesAttention(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration retry exhausted attention")
	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	activity := orchestrationpkg.ActivitySet{DB: testPool}
	if err := activity.FinalizeWorkflow(t.Context(), orchestrationpkg.ValidateOutcomeResult{
		Status:             "failed",
		ReasonCode:         "retry_exhausted",
		RecommendedAction:  "none",
		TerminalPlanStatus: "failed",
		ProjectionDetail:   "orchestration retry budget exhausted",
	}, orchestrationpkg.ReviewOutcomeResult{}, orchestrationpkg.SummarizeOutcomeResult{
		Summary:  "retry budget exhausted",
		TraceRef: startBody.Plan.ID + "/" + dispatched.NodeID + "/" + dispatched.TaskID,
	}, orchestrationpkg.IssueWorkflowInput{
		PlanID:     startBody.Plan.ID,
		WorkflowID: startBody.Plan.TemporalWorkflowID,
	}, orchestrationpkg.IssueSnapshot{
		IssueID: issueID,
	}, orchestrationpkg.AnalyzeIssueResult{
		ProblemSummary: "Fix billing",
	}, dispatched, service.AgentTaskOutcomeSignalInput{
		WorkflowID: startBody.Plan.TemporalWorkflowID,
		Status:     "failed",
	}); err != nil {
		t.Fatalf("finalize workflow: %v", err)
	}
	var comments int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM comment
		WHERE issue_id = $1
			AND type = 'system'
			AND content LIKE '%Retries exhausted%'
	`, issueID).Scan(&comments); err != nil {
		t.Fatalf("count retry attention comments: %v", err)
	}
	if comments != 1 {
		t.Fatalf("expected one retry exhausted attention comment, got %d", comments)
	}
}

func TestFinalizeWorkflowSuccessDoesNotCreateAttention(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration success no attention")
	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	activity := orchestrationpkg.ActivitySet{DB: testPool}
	if err := activity.FinalizeWorkflow(t.Context(), orchestrationpkg.ValidateOutcomeResult{
		Status:             "completed",
		RecommendedAction:  "none",
		TerminalPlanStatus: "completed",
		ProjectionDetail:   "structured result validated",
	}, orchestrationpkg.ReviewOutcomeResult{}, orchestrationpkg.SummarizeOutcomeResult{
		Summary:  "done",
		TraceRef: startBody.Plan.ID + "/" + dispatched.NodeID + "/" + dispatched.TaskID,
	}, orchestrationpkg.IssueWorkflowInput{
		PlanID:     startBody.Plan.ID,
		WorkflowID: startBody.Plan.TemporalWorkflowID,
	}, orchestrationpkg.IssueSnapshot{
		IssueID: issueID,
	}, orchestrationpkg.AnalyzeIssueResult{
		ProblemSummary: "Fix billing",
	}, dispatched, service.AgentTaskOutcomeSignalInput{
		WorkflowID: startBody.Plan.TemporalWorkflowID,
		Status:     "completed",
	}); err != nil {
		t.Fatalf("finalize workflow: %v", err)
	}
	var inbox int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM inbox_item
		WHERE issue_id = $1
			AND type = 'orchestration_attention'
	`, issueID).Scan(&inbox); err != nil {
		t.Fatalf("count attention inbox: %v", err)
	}
	if inbox != 0 {
		t.Fatalf("expected no success attention inbox items, got %d", inbox)
	}
}

func TestStartIssueOrchestrationFailClosedWhenTemporalStartFails(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration Temporal unreachable")

	starter := &recordingWorkflowStarter{err: errors.New("dial tcp temporal: connection refused")}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("StartIssueOrchestration: expected 503, got %d: %s", w.Code, w.Body.String())
	}

	var taskCount int
	if err := testPool.QueryRow(t.Context(), `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1`, issueID).Scan(&taskCount); err != nil {
		t.Fatalf("count agent tasks: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("expected no direct agent task fallback, got %d queued tasks", taskCount)
	}

	readW := httptest.NewRecorder()
	readReq := withURLParam(newRequest("GET", "/api/issues/"+issueID+"/orchestration", nil), "id", issueID)
	testHandler.GetIssueOrchestration(readW, readReq)
	if readW.Code != http.StatusOK {
		t.Fatalf("GetIssueOrchestration: expected 200, got %d: %s", readW.Code, readW.Body.String())
	}

	var snapshot struct {
		Plans []struct {
			Status  string `json:"status"`
			Summary struct {
				ReasonCode        string `json:"reason_code"`
				RecommendedAction string `json:"recommended_action"`
			} `json:"summary"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(readW.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode orchestration snapshot: %v", err)
	}
	if len(snapshot.Plans) != 1 {
		t.Fatalf("expected one projected orchestration plan, got %d: %s", len(snapshot.Plans), readW.Body.String())
	}
	if snapshot.Plans[0].Status != "failed" {
		t.Fatalf("expected failed plan, got %q", snapshot.Plans[0].Status)
	}
	if snapshot.Plans[0].Summary.ReasonCode != "temporal_unavailable" {
		t.Fatalf("expected temporal_unavailable reason, got %q", snapshot.Plans[0].Summary.ReasonCode)
	}
	if snapshot.Plans[0].Summary.RecommendedAction != "configure_temporal" {
		t.Fatalf("expected configure_temporal action, got %q", snapshot.Plans[0].Summary.RecommendedAction)
	}
}

type recordingWorkflowStarter struct {
	mu    sync.Mutex
	calls []service.IssueWorkflowStartInput
	runID string
	err   error
}

func (s *recordingWorkflowStarter) StartIssueWorkflow(ctx context.Context, input service.IssueWorkflowStartInput) (service.TemporalWorkflowStart, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, input)
	if s.err != nil {
		return service.TemporalWorkflowStart{}, s.err
	}
	runID := s.runID
	if runID == "" {
		runID = "temporal-run-1"
	}
	return service.TemporalWorkflowStart{WorkflowID: input.WorkflowID, RunID: runID}, nil
}

func (s *recordingWorkflowStarter) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *recordingWorkflowStarter) recordedCalls() []service.IssueWorkflowStartInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := make([]service.IssueWorkflowStartInput, len(s.calls))
	copy(calls, s.calls)
	return calls
}

type visibilityCheckingWorkflowStarter struct {
	t *testing.T
}

func (s visibilityCheckingWorkflowStarter) StartIssueWorkflow(ctx context.Context, input service.IssueWorkflowStartInput) (service.TemporalWorkflowStart, error) {
	s.t.Helper()
	var exists bool
	if err := testPool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM orchestration_plan WHERE id = $1)`, input.PlanID).Scan(&exists); err != nil {
		return service.TemporalWorkflowStart{}, err
	}
	if !exists {
		return service.TemporalWorkflowStart{}, errors.New("plan is not visible before Temporal start")
	}
	return service.TemporalWorkflowStart{WorkflowID: input.WorkflowID, RunID: "temporal-run-visible"}, nil
}

type recordingAgentTaskOutcomeSignaler struct {
	mu    sync.Mutex
	calls []service.AgentTaskOutcomeSignalInput
	err   error
}

func (s *recordingAgentTaskOutcomeSignaler) SignalAgentTaskOutcome(ctx context.Context, input service.AgentTaskOutcomeSignalInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, input)
	return s.err
}

func (s *recordingAgentTaskOutcomeSignaler) recordedCalls() []service.AgentTaskOutcomeSignalInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := make([]service.AgentTaskOutcomeSignalInput, len(s.calls))
	copy(calls, s.calls)
	return calls
}

type auditCheckingApprovalSignaler struct {
	t     *testing.T
	mu    sync.Mutex
	calls []service.ApprovalActionSignalInput
}

func (s *auditCheckingApprovalSignaler) SignalApprovalAction(ctx context.Context, input service.ApprovalActionSignalInput) error {
	s.t.Helper()
	var count int
	query := `
		SELECT count(*)
		FROM orchestration_event
		WHERE plan_id = $1
			AND node_id = $2
			AND type = 'approval.' || $4
			AND details->>'actor_id' = $3
			AND details->>'actor_type' = 'human'
			AND details->>'action' = $4
	`
	args := []any{input.PlanID, input.NodeID, input.ActorID, input.Action}
	if input.NodeID == "" {
		query = `
			SELECT count(*)
			FROM orchestration_event
			WHERE plan_id = $1
				AND node_id IS NULL
				AND type = 'approval.' || $3
				AND details->>'actor_id' = $2
				AND details->>'actor_type' = 'human'
				AND details->>'action' = $3
		`
		args = []any{input.PlanID, input.ActorID, input.Action}
	}
	if err := testPool.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		s.t.Fatalf("approval signal sent before audit event was persisted, audit count=%d input=%+v", count, input)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, input)
	return nil
}

func (s *auditCheckingApprovalSignaler) recordedCalls() []service.ApprovalActionSignalInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := make([]service.ApprovalActionSignalInput, len(s.calls))
	copy(calls, s.calls)
	return calls
}

type recordingWorkflowCanceler struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (c *recordingWorkflowCanceler) CancelWorkflow(ctx context.Context, workflowID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, workflowID)
	return c.err
}

func (c *recordingWorkflowCanceler) recordedCalls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	calls := make([]string, len(c.calls))
	copy(calls, c.calls)
	return calls
}

type failingTaskCanceler struct {
	err error
}

func (c failingTaskCanceler) CancelTask(ctx context.Context, taskID pgtype.UUID) (*db.AgentTaskQueue, error) {
	return nil, c.err
}

type flakyTaskCanceler struct {
	mu            sync.Mutex
	delegate      service.TaskCanceler
	failRemaining int
	err           error
	calls         []string
}

func (c *flakyTaskCanceler) CancelTask(ctx context.Context, taskID pgtype.UUID) (*db.AgentTaskQueue, error) {
	c.mu.Lock()
	c.calls = append(c.calls, uuidToString(taskID))
	shouldFail := c.failRemaining > 0
	if shouldFail {
		c.failRemaining--
	}
	c.mu.Unlock()
	if shouldFail {
		return nil, c.err
	}
	return c.delegate.CancelTask(ctx, taskID)
}

func (c *flakyTaskCanceler) recordedCalls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	calls := make([]string, len(c.calls))
	copy(calls, c.calls)
	return calls
}

type recordingTaskEnqueueNotifier struct {
	mu    sync.Mutex
	calls []db.AgentTaskQueue
}

func (n *recordingTaskEnqueueNotifier) NotifyTaskEnqueued(ctx context.Context, task db.AgentTaskQueue) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, task)
}

func (n *recordingTaskEnqueueNotifier) recordedCalls() []db.AgentTaskQueue {
	n.mu.Lock()
	defer n.mu.Unlock()
	calls := make([]db.AgentTaskQueue, len(n.calls))
	copy(calls, n.calls)
	return calls
}

func TestNewHandlerWiresTemporalStarterWhenConfigured(t *testing.T) {
	h := New(testHandler.Queries, testPool, testHandler.Hub, testHandler.Bus, testHandler.EmailService, testHandler.Storage, nil, testHandler.Analytics, Config{
		TemporalHostPort:  "127.0.0.1:7233",
		TemporalTaskQueue: "multica-orchestration-test",
	})
	if h.OrchestrationService == nil || h.OrchestrationService.Starter == nil {
		t.Fatal("expected configured Temporal starter to be wired into orchestration service")
	}
	if h.OrchestrationService.TaskNotifier == nil {
		t.Fatal("expected orchestration service to notify daemon task enqueue path")
	}
}

func TestApproveOrchestrationNodeAllowsOwnerAndAuditsBeforeSignal(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration approval owner")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	signaler := &auditCheckingApprovalSignaler{t: t}
	previousApprovalSignaler := testHandler.OrchestrationService.ApprovalSignaler
	testHandler.OrchestrationService.ApprovalSignaler = signaler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.OrchestrationService.ApprovalSignaler = previousApprovalSignaler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "validation",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch approval node: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE orchestration_node
		SET status = 'waiting_human',
			reason_code = 'risk_present',
			recommended_action = 'approve'
		WHERE id = $1
	`, dispatched.NodeID); err != nil {
		t.Fatalf("mark node waiting_human: %v", err)
	}

	eventsCh := make(chan events.Event, 4)
	testHandler.Bus.Subscribe(protocol.EventOrchestrationUpdated, func(e events.Event) {
		select {
		case eventsCh <- e:
		default:
		}
	})

	approveW := httptest.NewRecorder()
	approveReq := withURLParam(
		newRequest("POST", "/api/orchestration/nodes/"+dispatched.NodeID+"/approve", map[string]any{
			"reason": "risk accepted by owner",
		}),
		"nodeId",
		dispatched.NodeID,
	)
	testHandler.ApproveOrchestrationNode(approveW, approveReq)
	if approveW.Code != http.StatusAccepted {
		t.Fatalf("ApproveOrchestrationNode: expected 202, got %d: %s", approveW.Code, approveW.Body.String())
	}

	calls := signaler.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one approval signal, got %d", len(calls))
	}
	call := calls[0]
	if call.WorkflowID != startBody.Plan.TemporalWorkflowID {
		t.Fatalf("signal workflow id = %q, want %q", call.WorkflowID, startBody.Plan.TemporalWorkflowID)
	}
	if call.PlanID != startBody.Plan.ID || call.NodeID != dispatched.NodeID {
		t.Fatalf("signal identity mismatch: %+v", call)
	}
	if call.ActorID != testUserID || call.ActorType != "human" {
		t.Fatalf("signal actor = %s/%s, want human %s", call.ActorType, call.ActorID, testUserID)
	}
	if call.Action != "approve" || call.Reason != "risk accepted by owner" {
		t.Fatalf("signal action/reason mismatch: %+v", call)
	}
	select {
	case event := <-eventsCh:
		if event.WorkspaceID != testWorkspaceID {
			t.Fatalf("approval event workspace_id = %q, want %q", event.WorkspaceID, testWorkspaceID)
		}
	default:
		t.Fatal("expected orchestration:updated event on approval")
	}
}

func TestApprovalNodeActionsAllowAdminAndHumanAssignee(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration approval authorized humans")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	signaler := &auditCheckingApprovalSignaler{t: t}
	previousApprovalSignaler := testHandler.OrchestrationService.ApprovalSignaler
	testHandler.OrchestrationService.ApprovalSignaler = signaler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.OrchestrationService.ApprovalSignaler = previousApprovalSignaler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "validation",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch approval node: %v", err)
	}
	adminID := createRuntimeLocalSkillTestMember(t, "admin")
	assigneeID := createRuntimeLocalSkillTestMember(t, "member")
	if _, err := testPool.Exec(t.Context(), `
		UPDATE issue
		SET assignee_type = 'member',
			assignee_id = $2
		WHERE id = $1
	`, issueID, assigneeID); err != nil {
		t.Fatalf("assign issue to human member: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE orchestration_node
		SET status = 'waiting_human',
			reason_code = 'tests_failed',
			recommended_action = 'retry'
		WHERE id = $1
	`, dispatched.NodeID); err != nil {
		t.Fatalf("mark node waiting_human: %v", err)
	}

	adminW := httptest.NewRecorder()
	adminReq := withURLParam(newRequestAs(adminID, "POST", "/api/orchestration/nodes/"+dispatched.NodeID+"/approve", nil), "nodeId", dispatched.NodeID)
	testHandler.ApproveOrchestrationNode(adminW, adminReq)
	if adminW.Code != http.StatusAccepted {
		t.Fatalf("admin ApproveOrchestrationNode: expected 202, got %d: %s", adminW.Code, adminW.Body.String())
	}

	assigneeW := httptest.NewRecorder()
	assigneeReq := withURLParam(newRequestAs(assigneeID, "POST", "/api/orchestration/nodes/"+dispatched.NodeID+"/retry", nil), "nodeId", dispatched.NodeID)
	testHandler.RetryOrchestrationNode(assigneeW, assigneeReq)
	if assigneeW.Code != http.StatusAccepted {
		t.Fatalf("assignee RetryOrchestrationNode: expected 202, got %d: %s", assigneeW.Code, assigneeW.Body.String())
	}

	calls := signaler.recordedCalls()
	if len(calls) != 2 {
		t.Fatalf("expected admin and assignee signals, got %+v", calls)
	}
	if calls[0].ActorID != adminID || calls[0].Action != "approve" {
		t.Fatalf("unexpected admin signal: %+v", calls[0])
	}
	if calls[1].ActorID != assigneeID || calls[1].Action != "retry" {
		t.Fatalf("unexpected assignee signal: %+v", calls[1])
	}
}

func TestApproveOrchestrationNodeRejectsNodeThatIsNotWaitingForHuman(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration approval state denied")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	signaler := &auditCheckingApprovalSignaler{t: t}
	previousApprovalSignaler := testHandler.OrchestrationService.ApprovalSignaler
	testHandler.OrchestrationService.ApprovalSignaler = signaler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.OrchestrationService.ApprovalSignaler = previousApprovalSignaler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "validation",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch approval node: %v", err)
	}

	approveW := httptest.NewRecorder()
	approveReq := withURLParam(newRequest("POST", "/api/orchestration/nodes/"+dispatched.NodeID+"/approve", nil), "nodeId", dispatched.NodeID)
	testHandler.ApproveOrchestrationNode(approveW, approveReq)
	if approveW.Code != http.StatusConflict {
		t.Fatalf("ApproveOrchestrationNode: expected 409, got %d: %s", approveW.Code, approveW.Body.String())
	}
	if calls := signaler.recordedCalls(); len(calls) != 0 {
		t.Fatalf("non-waiting node approval should not signal Temporal, got %+v", calls)
	}
}

func TestRetryOrchestrationNodeRejectsExhaustedRetryBudget(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration retry budget denied")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	signaler := &auditCheckingApprovalSignaler{t: t}
	previousApprovalSignaler := testHandler.OrchestrationService.ApprovalSignaler
	testHandler.OrchestrationService.ApprovalSignaler = signaler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.OrchestrationService.ApprovalSignaler = previousApprovalSignaler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "validation",
		Attempt:            2,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch approval node: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE orchestration_node
		SET status = 'waiting_human',
			reason_code = 'evidence_insufficient',
			recommended_action = 'review'
		WHERE id = $1
	`, dispatched.NodeID); err != nil {
		t.Fatalf("mark node waiting_human: %v", err)
	}

	retryW := httptest.NewRecorder()
	retryReq := withURLParam(newRequest("POST", "/api/orchestration/nodes/"+dispatched.NodeID+"/retry", nil), "nodeId", dispatched.NodeID)
	testHandler.RetryOrchestrationNode(retryW, retryReq)
	if retryW.Code != http.StatusConflict {
		t.Fatalf("RetryOrchestrationNode: expected 409, got %d: %s", retryW.Code, retryW.Body.String())
	}
	if calls := signaler.recordedCalls(); len(calls) != 0 {
		t.Fatalf("retry-exhausted approval should not signal Temporal, got %+v", calls)
	}
}

func TestApproveOrchestrationNodeRejectsAgentActor(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration approval agent denied")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	signaler := &auditCheckingApprovalSignaler{t: t}
	previousApprovalSignaler := testHandler.OrchestrationService.ApprovalSignaler
	testHandler.OrchestrationService.ApprovalSignaler = signaler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.OrchestrationService.ApprovalSignaler = previousApprovalSignaler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "validation",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch approval node: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE orchestration_node
		SET status = 'waiting_human',
			reason_code = 'risk_present',
			recommended_action = 'approve'
		WHERE id = $1
	`, dispatched.NodeID); err != nil {
		t.Fatalf("mark node waiting_human: %v", err)
	}

	var agentID string
	if err := testPool.QueryRow(t.Context(), `
		SELECT agent_id
		FROM agent_task_queue
		WHERE id = $1
	`, dispatched.TaskID).Scan(&agentID); err != nil {
		t.Fatalf("load dispatched agent: %v", err)
	}

	approveW := httptest.NewRecorder()
	approveReq := withURLParam(newRequest("POST", "/api/orchestration/nodes/"+dispatched.NodeID+"/approve", nil), "nodeId", dispatched.NodeID)
	approveReq.Header.Set("X-Agent-ID", agentID)
	approveReq.Header.Set("X-Task-ID", dispatched.TaskID)
	testHandler.ApproveOrchestrationNode(approveW, approveReq)
	if approveW.Code != http.StatusForbidden {
		t.Fatalf("ApproveOrchestrationNode: expected 403, got %d: %s", approveW.Code, approveW.Body.String())
	}

	if calls := signaler.recordedCalls(); len(calls) != 0 {
		t.Fatalf("agent approval should not signal Temporal, got %+v", calls)
	}
	var auditCount int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM orchestration_event
			WHERE plan_id = $1 AND node_id = $2 AND type LIKE 'approval.%'
	`, startBody.Plan.ID, dispatched.NodeID).Scan(&auditCount); err != nil {
		t.Fatalf("count approval audits: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("agent approval should not write audit event, got %d", auditCount)
	}
}

func TestGetIssueOrchestrationProjectsActionsOnlyForAuthorizedHumans(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration approval read actions")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "validation",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch approval node: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE orchestration_node
		SET status = 'waiting_human',
			reason_code = 'tests_failed',
			recommended_action = 'retry'
		WHERE id = $1
	`, dispatched.NodeID); err != nil {
		t.Fatalf("mark node waiting_human: %v", err)
	}

	type projectedActions struct {
		Plan []string
		Node []string
	}
	readActions := func(t *testing.T, userID string) projectedActions {
		t.Helper()
		readW := httptest.NewRecorder()
		readReq := withURLParam(newRequestAs(userID, "GET", "/api/issues/"+issueID+"/orchestration", nil), "id", issueID)
		testHandler.GetIssueOrchestration(readW, readReq)
		if readW.Code != http.StatusOK {
			t.Fatalf("GetIssueOrchestration: expected 200, got %d: %s", readW.Code, readW.Body.String())
		}
		var snapshot struct {
			Plans []struct {
				AvailableActions []string `json:"available_actions"`
				Nodes            []struct {
					ID               string   `json:"id"`
					AvailableActions []string `json:"available_actions"`
				} `json:"nodes"`
			} `json:"plans"`
		}
		if err := json.Unmarshal(readW.Body.Bytes(), &snapshot); err != nil {
			t.Fatalf("decode orchestration snapshot: %v", err)
		}
		for _, node := range snapshot.Plans[0].Nodes {
			if node.ID == dispatched.NodeID {
				return projectedActions{Plan: snapshot.Plans[0].AvailableActions, Node: node.AvailableActions}
			}
		}
		t.Fatalf("projected node %s not found: %s", dispatched.NodeID, readW.Body.String())
		return projectedActions{}
	}

	ownerActions := readActions(t, testUserID)
	if len(ownerActions.Plan) != 1 || ownerActions.Plan[0] != "cancel" {
		t.Fatalf("owner plan actions = %+v, want cancel", ownerActions.Plan)
	}
	if len(ownerActions.Node) != 2 || ownerActions.Node[0] != "approve" || ownerActions.Node[1] != "retry" {
		t.Fatalf("owner node actions = %+v, want approve/retry", ownerActions.Node)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE orchestration_node
		SET attempt = 2
		WHERE id = $1
	`, dispatched.NodeID); err != nil {
		t.Fatalf("mark node retry budget exhausted: %v", err)
	}
	exhaustedActions := readActions(t, testUserID)
	if len(exhaustedActions.Node) != 1 || exhaustedActions.Node[0] != "approve" {
		t.Fatalf("retry-exhausted node actions = %+v, want approve only", exhaustedActions.Node)
	}
	plainMemberID := createRuntimeLocalSkillTestMember(t, "member")
	if actions := readActions(t, plainMemberID); len(actions.Plan) != 0 || len(actions.Node) != 0 {
		t.Fatalf("unrelated member actions = %+v, want none", actions)
	}
}

func TestCancelOrchestrationPlanAuditsSignalsAndCancelsLinkedTask(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration cancel plan")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	signaler := &auditCheckingApprovalSignaler{t: t}
	previousApprovalSignaler := testHandler.OrchestrationService.ApprovalSignaler
	testHandler.OrchestrationService.ApprovalSignaler = signaler
	canceler := &recordingWorkflowCanceler{}
	previousWorkflowCanceler := testHandler.OrchestrationService.WorkflowCanceler
	testHandler.OrchestrationService.WorkflowCanceler = canceler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.OrchestrationService.ApprovalSignaler = previousApprovalSignaler
		testHandler.OrchestrationService.WorkflowCanceler = previousWorkflowCanceler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'running', started_at = now()
		WHERE id = $1
	`, dispatched.TaskID); err != nil {
		t.Fatalf("mark task running: %v", err)
	}

	cancelW := httptest.NewRecorder()
	cancelReq := withURLParam(
		newRequest("POST", "/api/orchestration/plans/"+startBody.Plan.ID+"/cancel", map[string]any{
			"reason": "stop this run",
		}),
		"planId",
		startBody.Plan.ID,
	)
	testHandler.CancelOrchestrationPlan(cancelW, cancelReq)
	if cancelW.Code != http.StatusAccepted {
		t.Fatalf("CancelOrchestrationPlan: expected 202, got %d: %s", cancelW.Code, cancelW.Body.String())
	}

	if calls := signaler.recordedCalls(); len(calls) != 0 {
		t.Fatalf("plan cancellation should cancel the workflow instead of signaling approval, got %+v", calls)
	}
	cancelCalls := canceler.recordedCalls()
	if len(cancelCalls) != 1 || cancelCalls[0] != startBody.Plan.TemporalWorkflowID {
		t.Fatalf("expected one workflow cancellation for %q, got %+v", startBody.Plan.TemporalWorkflowID, cancelCalls)
	}

	var planStatus, taskStatus string
	if err := testPool.QueryRow(t.Context(), `SELECT status FROM orchestration_plan WHERE id = $1`, startBody.Plan.ID).Scan(&planStatus); err != nil {
		t.Fatalf("load plan status: %v", err)
	}
	if err := testPool.QueryRow(t.Context(), `SELECT status FROM agent_task_queue WHERE id = $1`, dispatched.TaskID).Scan(&taskStatus); err != nil {
		t.Fatalf("load task status: %v", err)
	}
	if planStatus != "cancelled" || taskStatus != "cancelled" {
		t.Fatalf("expected cancelled plan/task, got plan=%q task=%q", planStatus, taskStatus)
	}
}

func TestCancelOrchestrationPlanDoesNotProjectCancelledWhenTaskCancelFails(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration cancel task failure")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	canceler := &recordingWorkflowCanceler{}
	previousWorkflowCanceler := testHandler.OrchestrationService.WorkflowCanceler
	testHandler.OrchestrationService.WorkflowCanceler = canceler
	previousTaskCanceler := testHandler.OrchestrationService.TaskCanceler
	testHandler.OrchestrationService.TaskCanceler = failingTaskCanceler{err: errors.New("task cancel failed")}
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.OrchestrationService.WorkflowCanceler = previousWorkflowCanceler
		testHandler.OrchestrationService.TaskCanceler = previousTaskCanceler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:          startBody.Plan.ID,
		WorkflowNodeKey: "dispatch",
		Attempt:         1,
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'running', started_at = now()
		WHERE id = $1
	`, dispatched.TaskID); err != nil {
		t.Fatalf("mark task running: %v", err)
	}

	cancelW := httptest.NewRecorder()
	cancelReq := withURLParam(
		newRequest("POST", "/api/orchestration/plans/"+startBody.Plan.ID+"/cancel", map[string]any{"reason": "stop"}),
		"planId",
		startBody.Plan.ID,
	)
	testHandler.CancelOrchestrationPlan(cancelW, cancelReq)
	if cancelW.Code != http.StatusInternalServerError {
		t.Fatalf("CancelOrchestrationPlan: expected 500, got %d: %s", cancelW.Code, cancelW.Body.String())
	}

	var planStatus, nodeStatus string
	if err := testPool.QueryRow(t.Context(), `SELECT status FROM orchestration_plan WHERE id = $1`, startBody.Plan.ID).Scan(&planStatus); err != nil {
		t.Fatalf("load plan status: %v", err)
	}
	if err := testPool.QueryRow(t.Context(), `SELECT status FROM orchestration_node WHERE id = $1`, dispatched.NodeID).Scan(&nodeStatus); err != nil {
		t.Fatalf("load node status: %v", err)
	}
	if planStatus == "cancelled" || nodeStatus == "cancelled" {
		t.Fatalf("plan/node status = %q/%q, task cancel failure must not pre-project cancellation", planStatus, nodeStatus)
	}
}

func TestCancelOrchestrationPlanRetriesSideEffectsAfterTaskCancelFailure(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration cancel task retry")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	canceler := &recordingWorkflowCanceler{}
	previousWorkflowCanceler := testHandler.OrchestrationService.WorkflowCanceler
	testHandler.OrchestrationService.WorkflowCanceler = canceler
	flakyCanceler := &flakyTaskCanceler{
		delegate:      testHandler.TaskService,
		failRemaining: 1,
		err:           errors.New("task cancel failed once"),
	}
	previousTaskCanceler := testHandler.OrchestrationService.TaskCanceler
	testHandler.OrchestrationService.TaskCanceler = flakyCanceler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.OrchestrationService.WorkflowCanceler = previousWorkflowCanceler
		testHandler.OrchestrationService.TaskCanceler = previousTaskCanceler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'running', started_at = now()
		WHERE id = $1
	`, dispatched.TaskID); err != nil {
		t.Fatalf("mark task running: %v", err)
	}

	cancel := func(wantStatus int) {
		t.Helper()
		cancelW := httptest.NewRecorder()
		cancelReq := withURLParam(
			newRequest("POST", "/api/orchestration/plans/"+startBody.Plan.ID+"/cancel", map[string]any{"reason": "stop"}),
			"planId",
			startBody.Plan.ID,
		)
		testHandler.CancelOrchestrationPlan(cancelW, cancelReq)
		if cancelW.Code != wantStatus {
			t.Fatalf("CancelOrchestrationPlan: expected %d, got %d: %s", wantStatus, cancelW.Code, cancelW.Body.String())
		}
	}

	cancel(http.StatusInternalServerError)
	cancel(http.StatusAccepted)

	if calls := canceler.recordedCalls(); len(calls) != 2 {
		t.Fatalf("Temporal cancellation should be retried while cancellation is not fully projected, got %+v", calls)
	}
	if calls := flakyCanceler.recordedCalls(); len(calls) != 2 {
		t.Fatalf("linked task cancellation should be retried after the first task cancel failure, got %+v", calls)
	}
	var taskStatus string
	if err := testPool.QueryRow(t.Context(), `
		SELECT status
		FROM agent_task_queue
		WHERE id = $1
	`, dispatched.TaskID).Scan(&taskStatus); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if taskStatus != "cancelled" {
		t.Fatalf("retried linked task cancellation left task status %q, want cancelled", taskStatus)
	}
}

func TestCancelOrchestrationPlanWithoutActiveTaskPreservesEvidence(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration cancel without task")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	canceler := &recordingWorkflowCanceler{}
	previousWorkflowCanceler := testHandler.OrchestrationService.WorkflowCanceler
	testHandler.OrchestrationService.WorkflowCanceler = canceler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.OrchestrationService.WorkflowCanceler = previousWorkflowCanceler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	var completedNodeID string
	if err := testPool.QueryRow(t.Context(), `
		INSERT INTO orchestration_node (plan_id, workflow_node_key, title, status, attempt, started_at, completed_at)
		VALUES ($1, 'analyze', 'Analyze', 'completed', 1, now(), now())
		RETURNING id
	`, startBody.Plan.ID).Scan(&completedNodeID); err != nil {
		t.Fatalf("insert completed node: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		INSERT INTO orchestration_event (plan_id, node_id, type, source, message, details)
		VALUES ($1, $2, 'node.completed', 'workflow', 'Analyze completed', '{"summary":"ready"}'::jsonb)
	`, startBody.Plan.ID, completedNodeID); err != nil {
		t.Fatalf("insert completed event: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		INSERT INTO orchestration_artifact (plan_id, node_id, type, source, label, uri, data)
		VALUES ($1, $2, 'analysis', 'workflow', 'Analysis summary', 'artifact://analysis', '{"recommendation":"dispatch"}'::jsonb)
	`, startBody.Plan.ID, completedNodeID); err != nil {
		t.Fatalf("insert completed artifact: %v", err)
	}

	cancelW := httptest.NewRecorder()
	cancelReq := withURLParam(
		newRequest("POST", "/api/orchestration/plans/"+startBody.Plan.ID+"/cancel", map[string]any{
			"reason": "operator stopped run",
		}),
		"planId",
		startBody.Plan.ID,
	)
	testHandler.CancelOrchestrationPlan(cancelW, cancelReq)
	if cancelW.Code != http.StatusAccepted {
		t.Fatalf("CancelOrchestrationPlan: expected 202, got %d: %s", cancelW.Code, cancelW.Body.String())
	}
	cancelCalls := canceler.recordedCalls()
	if len(cancelCalls) != 1 || cancelCalls[0] != startBody.Plan.TemporalWorkflowID {
		t.Fatalf("expected one workflow cancellation for %q, got %+v", startBody.Plan.TemporalWorkflowID, cancelCalls)
	}

	readW := httptest.NewRecorder()
	readReq := withURLParam(newRequest("GET", "/api/issues/"+issueID+"/orchestration", nil), "id", issueID)
	testHandler.GetIssueOrchestration(readW, readReq)
	if readW.Code != http.StatusOK {
		t.Fatalf("GetIssueOrchestration: expected 200, got %d: %s", readW.Code, readW.Body.String())
	}
	var snapshot struct {
		Plans []struct {
			Status  string `json:"status"`
			Summary struct {
				ReasonCode string `json:"reason_code"`
			} `json:"summary"`
			Nodes []struct {
				ID               string   `json:"id"`
				WorkflowNodeKey  string   `json:"workflow_node_key"`
				Status           string   `json:"status"`
				AvailableActions []string `json:"available_actions"`
			} `json:"nodes"`
			Events []struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"events"`
			Artifacts []struct {
				Label string `json:"label"`
				URI   string `json:"uri"`
			} `json:"artifacts"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(readW.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode orchestration snapshot: %v", err)
	}
	if len(snapshot.Plans) != 1 {
		t.Fatalf("expected one plan in snapshot, got %d", len(snapshot.Plans))
	}
	plan := snapshot.Plans[0]
	if plan.Status != "cancelled" || plan.Summary.ReasonCode != "human_cancelled" {
		t.Fatalf("snapshot plan status/reason = %q/%q, want cancelled/human_cancelled", plan.Status, plan.Summary.ReasonCode)
	}
	var sawCompletedNode, sawCancelAudit, sawArtifact bool
	for _, node := range plan.Nodes {
		if node.ID == completedNodeID && node.WorkflowNodeKey == "analyze" && node.Status == "completed" && len(node.AvailableActions) == 0 {
			sawCompletedNode = true
		}
	}
	for _, event := range plan.Events {
		if event.Type == "approval.cancel" {
			sawCancelAudit = true
		}
	}
	for _, artifact := range plan.Artifacts {
		if artifact.Label == "Analysis summary" && artifact.URI == "artifact://analysis" {
			sawArtifact = true
		}
	}
	if !sawCompletedNode || !sawCancelAudit || !sawArtifact {
		t.Fatalf("snapshot missing preserved evidence: node=%v cancelAudit=%v artifact=%v plan=%+v", sawCompletedNode, sawCancelAudit, sawArtifact, plan)
	}
}

func TestCancelOrchestrationPlanIsIdempotentAfterAlreadyCancelled(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration cancel idempotent")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	canceler := &recordingWorkflowCanceler{}
	previousWorkflowCanceler := testHandler.OrchestrationService.WorkflowCanceler
	testHandler.OrchestrationService.WorkflowCanceler = canceler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.OrchestrationService.WorkflowCanceler = previousWorkflowCanceler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	cancel := func(reason string) {
		t.Helper()
		cancelW := httptest.NewRecorder()
		cancelReq := withURLParam(
			newRequest("POST", "/api/orchestration/plans/"+startBody.Plan.ID+"/cancel", map[string]any{"reason": reason}),
			"planId",
			startBody.Plan.ID,
		)
		testHandler.CancelOrchestrationPlan(cancelW, cancelReq)
		if cancelW.Code != http.StatusAccepted {
			t.Fatalf("CancelOrchestrationPlan: expected 202, got %d: %s", cancelW.Code, cancelW.Body.String())
		}
	}

	cancel("first stop")
	cancel("second stop")

	if calls := canceler.recordedCalls(); len(calls) != 1 {
		t.Fatalf("repeated cancel should call Temporal once, got %+v", calls)
	}
	var auditCount int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM orchestration_event
		WHERE plan_id = $1 AND type = 'approval.cancel'
	`, startBody.Plan.ID).Scan(&auditCount); err != nil {
		t.Fatalf("count cancel audit events: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("repeated cancel should write one audit event, got %d", auditCount)
	}
}

func TestCancelOrchestrationPlanTemporalCancelFailureLeavesPlanRetryable(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration cancel temporal failure")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	canceler := &recordingWorkflowCanceler{err: errors.New("temporal cancel unavailable")}
	previousWorkflowCanceler := testHandler.OrchestrationService.WorkflowCanceler
	testHandler.OrchestrationService.WorkflowCanceler = canceler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.OrchestrationService.WorkflowCanceler = previousWorkflowCanceler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	cancelW := httptest.NewRecorder()
	cancelReq := withURLParam(
		newRequest("POST", "/api/orchestration/plans/"+startBody.Plan.ID+"/cancel", map[string]any{"reason": "stop"}),
		"planId",
		startBody.Plan.ID,
	)
	testHandler.CancelOrchestrationPlan(cancelW, cancelReq)
	if cancelW.Code != http.StatusInternalServerError {
		t.Fatalf("CancelOrchestrationPlan: expected 500, got %d: %s", cancelW.Code, cancelW.Body.String())
	}

	var planStatus string
	if err := testPool.QueryRow(t.Context(), `SELECT status FROM orchestration_plan WHERE id = $1`, startBody.Plan.ID).Scan(&planStatus); err != nil {
		t.Fatalf("load plan status: %v", err)
	}
	if planStatus == "cancelled" {
		t.Fatal("failed Temporal cancellation must not leave plan permanently cancelled")
	}
	var auditCount int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM orchestration_event
		WHERE plan_id = $1 AND type = 'approval.cancel'
	`, startBody.Plan.ID).Scan(&auditCount); err != nil {
		t.Fatalf("count cancel audit events: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("cancel attempt should keep one audit event for retry/debugging, got %d", auditCount)
	}
}

func TestFinalizeWorkflowAfterCancelledPlanDoesNotMoveIssueOrWriteHandoff(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration late finalize after cancel")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	canceler := &recordingWorkflowCanceler{}
	previousWorkflowCanceler := testHandler.OrchestrationService.WorkflowCanceler
	testHandler.OrchestrationService.WorkflowCanceler = canceler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.OrchestrationService.WorkflowCanceler = previousWorkflowCanceler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}

	cancelW := httptest.NewRecorder()
	cancelReq := withURLParam(newRequest("POST", "/api/orchestration/plans/"+startBody.Plan.ID+"/cancel", nil), "planId", startBody.Plan.ID)
	testHandler.CancelOrchestrationPlan(cancelW, cancelReq)
	if cancelW.Code != http.StatusAccepted {
		t.Fatalf("CancelOrchestrationPlan: expected 202, got %d: %s", cancelW.Code, cancelW.Body.String())
	}

	activity := orchestrationpkg.ActivitySet{DB: testPool}
	if err := activity.FinalizeWorkflow(t.Context(), orchestrationpkg.ValidateOutcomeResult{
		Status:             "completed",
		RecommendedAction:  "none",
		TerminalPlanStatus: "completed",
		ProjectionDetail:   "late completion after cancel",
	}, orchestrationpkg.ReviewOutcomeResult{
		Summary:           "late review clean",
		RecommendedAction: "accept",
	}, orchestrationpkg.SummarizeOutcomeResult{
		Summary:  "late done",
		TraceRef: startBody.Plan.ID + "/" + dispatched.NodeID + "/" + dispatched.TaskID,
	}, orchestrationpkg.IssueWorkflowInput{
		PlanID:     startBody.Plan.ID,
		WorkflowID: startBody.Plan.TemporalWorkflowID,
	}, orchestrationpkg.IssueSnapshot{
		IssueID: issueID,
	}, orchestrationpkg.AnalyzeIssueResult{
		ProblemSummary: "Fix billing",
	}, dispatched, service.AgentTaskOutcomeSignalInput{
		WorkflowID: startBody.Plan.TemporalWorkflowID,
		Status:     "completed",
	}); err != nil {
		t.Fatalf("late finalize workflow: %v", err)
	}

	var issueStatus string
	if err := testPool.QueryRow(t.Context(), `SELECT status FROM issue WHERE id = $1`, issueID).Scan(&issueStatus); err != nil {
		t.Fatalf("load issue status: %v", err)
	}
	if issueStatus == "in_review" {
		t.Fatal("late finalize after cancellation must not move issue to review")
	}
	var completedEvents int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM orchestration_event
		WHERE plan_id = $1 AND type = 'workflow.completed'
	`, startBody.Plan.ID).Scan(&completedEvents); err != nil {
		t.Fatalf("count completed events: %v", err)
	}
	if completedEvents != 0 {
		t.Fatalf("late finalize after cancellation must not write workflow.completed, got %d", completedEvents)
	}
	var handoffArtifacts int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM orchestration_artifact
		WHERE plan_id = $1 AND type = 'review_handoff'
	`, startBody.Plan.ID).Scan(&handoffArtifacts); err != nil {
		t.Fatalf("count handoff artifacts: %v", err)
	}
	if handoffArtifacts != 0 {
		t.Fatalf("late finalize after cancellation must not write review_handoff, got %d", handoffArtifacts)
	}
}

func TestRepeatedTemporalUnavailableRunsCreatePerPlanAttentionComments(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration repeated unavailable attention")

	start := func() {
		t.Helper()
		w := httptest.NewRecorder()
		req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
		testHandler.StartIssueOrchestration(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("StartIssueOrchestration: expected 503, got %d: %s", w.Code, w.Body.String())
		}
	}

	start()
	start()

	var failedPlans int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM orchestration_plan
		WHERE issue_id = $1 AND status = 'failed' AND reason_code = 'temporal_unavailable'
	`, issueID).Scan(&failedPlans); err != nil {
		t.Fatalf("count failed plans: %v", err)
	}
	if failedPlans != 2 {
		t.Fatalf("expected two failed orchestration plans, got %d", failedPlans)
	}
	var attentionComments int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM comment
		WHERE issue_id = $1
			AND type = 'system'
			AND content LIKE '%Temporal unavailable%'
	`, issueID).Scan(&attentionComments); err != nil {
		t.Fatalf("count attention comments: %v", err)
	}
	if attentionComments != 2 {
		t.Fatalf("each failed plan should create issue-visible attention, got %d comments", attentionComments)
	}
}

func TestStartIssueOrchestrationCreatesAndReusesActiveRun(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration active run")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
	})

	start := func() map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
		testHandler.StartIssueOrchestration(w, req)
		if w.Code != http.StatusAccepted {
			t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode start response: %v", err)
		}
		return body
	}

	first := start()
	second := start()

	if starter.callCount() != 1 {
		t.Fatalf("expected one Temporal start call after duplicate start, got %d", starter.callCount())
	}
	call := starter.recordedCalls()[0]
	if call.WorkspaceID != testWorkspaceID {
		t.Fatalf("workflow input workspace = %q, want %q", call.WorkspaceID, testWorkspaceID)
	}
	if call.IssueID != issueID {
		t.Fatalf("workflow input issue = %q, want %q", call.IssueID, issueID)
	}
	if call.PlanID == "" {
		t.Fatal("workflow input plan id is empty")
	}
	wantWorkflowID := "multica/" + testWorkspaceID + "/issue/" + issueID + "/run/" + call.PlanID
	if call.WorkflowID != wantWorkflowID {
		t.Fatalf("workflow id = %q, want %q", call.WorkflowID, wantWorkflowID)
	}

	firstPlan := first["plan"].(map[string]any)
	secondPlan := second["plan"].(map[string]any)
	if firstPlan["id"] != secondPlan["id"] {
		t.Fatalf("duplicate start created a new active plan: first=%v second=%v", firstPlan["id"], secondPlan["id"])
	}
	if second["reused"] != true {
		t.Fatalf("expected duplicate start to report reused=true, got %v", second["reused"])
	}
	if firstPlan["status"] != "running" {
		t.Fatalf("expected running plan, got %v", firstPlan["status"])
	}
	if firstPlan["temporal_workflow_id"] != wantWorkflowID {
		t.Fatalf("response workflow id = %v, want %q", firstPlan["temporal_workflow_id"], wantWorkflowID)
	}
}

func TestStartIssueOrchestrationRepairsStartingPlanWithoutWorkflowID(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration repair stuck starting")

	var planID string
	if err := testPool.QueryRow(t.Context(), `
		INSERT INTO orchestration_plan (
			workspace_id, issue_id, status, reason_code, recommended_action,
			workflow_type, projection_version, last_synced_at
		)
		VALUES ($1, $2, 'starting', '', 'none', 'issue_mvp', 1, now())
		RETURNING id
	`, testWorkspaceID, issueID).Scan(&planID); err != nil {
		t.Fatalf("seed stuck starting plan: %v", err)
	}

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	calls := starter.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("expected stuck starting plan to be started once, got %d calls", len(calls))
	}
	if calls[0].PlanID != planID {
		t.Fatalf("started plan id = %q, want existing stuck plan %q", calls[0].PlanID, planID)
	}

	var body struct {
		Plan struct {
			ID                 string `json:"id"`
			Status             string `json:"status"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
		Reused bool `json:"reused"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if body.Plan.ID != planID {
		t.Fatalf("response plan id = %q, want %q", body.Plan.ID, planID)
	}
	if body.Plan.Status != "running" || body.Plan.TemporalWorkflowID == "" {
		t.Fatalf("stuck starting plan was not repaired to running with workflow id: %+v", body.Plan)
	}
	if !body.Reused {
		t.Fatal("expected repair to report reused=true for the existing active plan")
	}
}

func TestStartIssueOrchestrationRestartsStartingPlanWithWorkflowID(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration repair prewritten workflow id")

	var planID string
	prewrittenWorkflowID := "multica/" + testWorkspaceID + "/issue/" + issueID + "/run/prewritten"
	if err := testPool.QueryRow(t.Context(), `
		INSERT INTO orchestration_plan (
			workspace_id, issue_id, status, reason_code, recommended_action,
			workflow_type, projection_version, last_synced_at, temporal_workflow_id, updated_at
		)
		VALUES ($1, $2, 'starting', '', 'none', 'issue_mvp', 1, now(), $3, now() - interval '5 minutes')
		RETURNING id
	`, testWorkspaceID, issueID, prewrittenWorkflowID).Scan(&planID); err != nil {
		t.Fatalf("seed stuck starting plan: %v", err)
	}

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	calls := starter.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("expected stuck starting plan with workflow id to be started once, got %d calls", len(calls))
	}
	if calls[0].PlanID != planID {
		t.Fatalf("started plan id = %q, want existing stuck plan %q", calls[0].PlanID, planID)
	}
	wantWorkflowID := "multica/" + testWorkspaceID + "/issue/" + issueID + "/run/" + planID
	if calls[0].WorkflowID != wantWorkflowID {
		t.Fatalf("workflow id = %q, want recomputed %q", calls[0].WorkflowID, wantWorkflowID)
	}
}

func TestGetIssueOrchestrationDoesNotInventProgressWithoutProjectionRows(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration projection baseline")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	readW := httptest.NewRecorder()
	readReq := withURLParam(newRequest("GET", "/api/issues/"+issueID+"/orchestration", nil), "id", issueID)
	testHandler.GetIssueOrchestration(readW, readReq)
	if readW.Code != http.StatusOK {
		t.Fatalf("GetIssueOrchestration: expected 200, got %d: %s", readW.Code, readW.Body.String())
	}

	var snapshot struct {
		Plans []struct {
			Nodes []struct {
				WorkflowNodeKey string `json:"workflow_node_key"`
				Status          string `json:"status"`
			} `json:"nodes"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(readW.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode orchestration snapshot: %v", err)
	}
	if len(snapshot.Plans) != 1 {
		t.Fatalf("expected one plan, got %d: %s", len(snapshot.Plans), readW.Body.String())
	}
	for _, node := range snapshot.Plans[0].Nodes {
		if node.Status != "pending" {
			t.Fatalf("expected unresolved projection to stay pending, got %s=%q", node.WorkflowNodeKey, node.Status)
		}
	}
}

func TestStartIssueOrchestrationCommitsPlanBeforeTemporalStart(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration visible plan")

	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = visibilityCheckingWorkflowStarter{t: t}
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
}

func TestStartIssueOrchestrationPublishesOrchestrationUpdated(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration event")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
	})

	eventsCh := make(chan events.Event, 2)
	testHandler.Bus.Subscribe(protocol.EventOrchestrationUpdated, func(e events.Event) {
		select {
		case eventsCh <- e:
		default:
		}
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	select {
	case event := <-eventsCh:
		if event.WorkspaceID != testWorkspaceID {
			t.Fatalf("event workspace = %q, want %q", event.WorkspaceID, testWorkspaceID)
		}
		payload, ok := event.Payload.(map[string]any)
		if !ok {
			t.Fatalf("event payload type = %T, want map", event.Payload)
		}
		if payload["issue_id"] != issueID {
			t.Fatalf("event issue_id = %v, want %q", payload["issue_id"], issueID)
		}
	default:
		t.Fatal("expected orchestration:updated event")
	}
}

func TestGetIssueOrchestrationReturnsEmptyArraysNotNull(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration arrays")

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("StartIssueOrchestration: expected 503, got %d: %s", w.Code, w.Body.String())
	}

	readW := httptest.NewRecorder()
	readReq := withURLParam(newRequest("GET", "/api/issues/"+issueID+"/orchestration", nil), "id", issueID)
	testHandler.GetIssueOrchestration(readW, readReq)
	if readW.Code != http.StatusOK {
		t.Fatalf("GetIssueOrchestration: expected 200, got %d: %s", readW.Code, readW.Body.String())
	}

	var raw map[string]any
	if err := json.Unmarshal(readW.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw snapshot: %v", err)
	}
	plans, ok := raw["plans"].([]any)
	if !ok || len(plans) != 1 {
		t.Fatalf("plans = %#v, want one array item", raw["plans"])
	}
	plan, ok := plans[0].(map[string]any)
	if !ok {
		t.Fatalf("plan = %#v, want object", plans[0])
	}
	for _, key := range []string{"nodes", "events", "artifacts"} {
		if _, ok := plan[key].([]any); !ok {
			t.Fatalf("%s = %#v, want JSON array", key, plan[key])
		}
	}
}

func TestStartIssueOrchestrationCreatesNewRunAfterTerminalPlan(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration terminal rerun")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
	})

	startOnce := func() string {
		t.Helper()
		w := httptest.NewRecorder()
		req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
		testHandler.StartIssueOrchestration(w, req)
		if w.Code != http.StatusAccepted {
			t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
		}
		var body struct {
			Plan struct {
				ID string `json:"id"`
			} `json:"plan"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode start response: %v", err)
		}
		return body.Plan.ID
	}

	firstPlanID := startOnce()
	if _, err := testPool.Exec(t.Context(), `
		UPDATE orchestration_plan
		SET status = 'completed', completed_at = now(), updated_at = now()
		WHERE id = $1
	`, firstPlanID); err != nil {
		t.Fatalf("mark first plan completed: %v", err)
	}

	secondPlanID := startOnce()
	if secondPlanID == firstPlanID {
		t.Fatalf("terminal rerun reused completed plan %s", firstPlanID)
	}
	calls := starter.recordedCalls()
	if len(calls) != 2 {
		t.Fatalf("expected two Temporal starts across terminal rerun, got %d", len(calls))
	}
	if calls[0].WorkflowID == calls[1].WorkflowID {
		t.Fatalf("terminal rerun reused workflow id %q", calls[0].WorkflowID)
	}

	readW := httptest.NewRecorder()
	readReq := withURLParam(newRequest("GET", "/api/issues/"+issueID+"/orchestration", nil), "id", issueID)
	testHandler.GetIssueOrchestration(readW, readReq)
	if readW.Code != http.StatusOK {
		t.Fatalf("GetIssueOrchestration: expected 200, got %d: %s", readW.Code, readW.Body.String())
	}
	var snapshot struct {
		Plans []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(readW.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode orchestration snapshot: %v", err)
	}
	if len(snapshot.Plans) != 2 {
		t.Fatalf("expected active and historical terminal plans, got %d: %s", len(snapshot.Plans), readW.Body.String())
	}
	var sawFirst, sawSecond bool
	for _, plan := range snapshot.Plans {
		if plan.ID == firstPlanID && plan.Status == "completed" {
			sawFirst = true
		}
		if plan.ID == secondPlanID && plan.Status == "running" {
			sawSecond = true
		}
	}
	if !sawFirst || !sawSecond {
		t.Fatalf("snapshot missing completed/running plans: %+v", snapshot.Plans)
	}
}

func TestStartIssueOrchestrationConcurrentStartsReuseOneActiveRun(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration concurrent start")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
	})

	const startCount = 8
	var wg sync.WaitGroup
	responses := make(chan map[string]any, startCount)
	errs := make(chan string, startCount)
	for i := 0; i < startCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
			testHandler.StartIssueOrchestration(w, req)
			if w.Code != http.StatusAccepted {
				errs <- w.Body.String()
				return
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				errs <- err.Error()
				return
			}
			responses <- body
		}()
	}
	wg.Wait()
	close(responses)
	close(errs)

	for errText := range errs {
		t.Fatalf("concurrent start failed: %s", errText)
	}

	var planID any
	for body := range responses {
		plan := body["plan"].(map[string]any)
		if planID == nil {
			planID = plan["id"]
			continue
		}
		if plan["id"] != planID {
			t.Fatalf("concurrent start returned multiple active plans: first=%v next=%v", planID, plan["id"])
		}
	}
	if starter.callCount() != 1 {
		t.Fatalf("expected one Temporal start call after concurrent starts, got %d", starter.callCount())
	}
	var activeCount int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM orchestration_plan
		WHERE issue_id = $1 AND status IN ('starting', 'running', 'waiting_human')
	`, issueID).Scan(&activeCount); err != nil {
		t.Fatalf("count active orchestration plans: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("expected one active orchestration plan, got %d", activeCount)
	}
}

func TestStartIssueOrchestrationRepairsWorkflowAlreadyStartedProjection(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration already started repair")

	starter := &recordingWorkflowStarter{
		err: service.WorkflowAlreadyStartedError{RunID: "temporal-run-existing"},
	}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	calls := starter.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one Temporal start call, got %d", len(calls))
	}

	var body struct {
		Plan struct {
			ID                 string `json:"id"`
			Status             string `json:"status"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
			TemporalRunID      string `json:"temporal_run_id"`
			Summary            struct {
				ReasonCode        string `json:"reason_code"`
				RecommendedAction string `json:"recommended_action"`
			} `json:"summary"`
		} `json:"plan"`
		Available bool `json:"available"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if !body.Available {
		t.Fatal("expected repaired already-started workflow to be available")
	}
	if body.Plan.Status != "running" {
		t.Fatalf("expected repaired plan to be running, got %q", body.Plan.Status)
	}
	if body.Plan.TemporalWorkflowID != calls[0].WorkflowID {
		t.Fatalf("workflow id = %q, want %q", body.Plan.TemporalWorkflowID, calls[0].WorkflowID)
	}
	if body.Plan.TemporalRunID != "temporal-run-existing" {
		t.Fatalf("run id = %q, want temporal-run-existing", body.Plan.TemporalRunID)
	}
	if body.Plan.Summary.ReasonCode != "" || body.Plan.Summary.RecommendedAction != "none" {
		t.Fatalf("expected repaired plan summary to be cleared, got %+v", body.Plan.Summary)
	}
}

func TestDispatchAgentTaskCreatesAndReusesLinkedTaskForNodeAttempt(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration dispatch bridge")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	notifier := &recordingTaskEnqueueNotifier{}
	previousNotifier := testHandler.OrchestrationService.TaskNotifier
	testHandler.OrchestrationService.TaskNotifier = notifier
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.OrchestrationService.TaskNotifier = previousNotifier
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	input := service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	}
	first, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), input)
	if err != nil {
		t.Fatalf("dispatch first task: %v", err)
	}
	second, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), input)
	if err != nil {
		t.Fatalf("dispatch second task: %v", err)
	}
	if first.TaskID == "" {
		t.Fatal("first dispatch returned empty task id")
	}
	if second.TaskID != first.TaskID {
		t.Fatalf("dispatch was not idempotent: first=%s second=%s", first.TaskID, second.TaskID)
	}
	if !second.Reused {
		t.Fatal("second dispatch should report reused=true")
	}

	var taskCount int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM agent_task_queue
		WHERE orchestration_plan_id = $1
			AND orchestration_node_id = $2
			AND orchestration_attempt = 1
	`, startBody.Plan.ID, first.NodeID).Scan(&taskCount); err != nil {
		t.Fatalf("count linked tasks: %v", err)
	}
	if taskCount != 1 {
		t.Fatalf("expected one linked task, got %d", taskCount)
	}

	var taskWorkflowID, nodeStatus string
	if err := testPool.QueryRow(t.Context(), `
		SELECT atq.temporal_workflow_id, n.status
		FROM agent_task_queue atq
		JOIN orchestration_node n ON n.id = atq.orchestration_node_id
		WHERE atq.id = $1
	`, first.TaskID).Scan(&taskWorkflowID, &nodeStatus); err != nil {
		t.Fatalf("load linked task/node: %v", err)
	}
	if taskWorkflowID != startBody.Plan.TemporalWorkflowID {
		t.Fatalf("task workflow id = %q, want %q", taskWorkflowID, startBody.Plan.TemporalWorkflowID)
	}
	if nodeStatus != "running" {
		t.Fatalf("node status = %q, want running", nodeStatus)
	}
	notifyCalls := notifier.recordedCalls()
	if len(notifyCalls) != 1 {
		t.Fatalf("new orchestration task should notify daemon enqueue path once, got %d", len(notifyCalls))
	}
	if uuidToString(notifyCalls[0].ID) != first.TaskID {
		t.Fatalf("notified task id = %q, want %q", uuidToString(notifyCalls[0].ID), first.TaskID)
	}
	if !notifyCalls[0].RuntimeID.Valid {
		t.Fatal("notified task must include runtime id for empty-claim invalidation")
	}
}

func TestDispatchAgentTaskStoresAnalyzerPromptForDaemonClaim(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration dispatch prompt")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
		AgentPrompt:        "Use the analyzer prompt and report Result Schema v1.",
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}

	var rawContext []byte
	if err := testPool.QueryRow(t.Context(), `
		SELECT context
		FROM agent_task_queue
		WHERE id = $1
	`, dispatched.TaskID).Scan(&rawContext); err != nil {
		t.Fatalf("read task context: %v", err)
	}
	var taskContext struct {
		Type   string `json:"type"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(rawContext, &taskContext); err != nil {
		t.Fatalf("decode task context: %v", err)
	}
	if taskContext.Type != service.OrchestrationTaskContextType {
		t.Fatalf("task context type = %q, want %q", taskContext.Type, service.OrchestrationTaskContextType)
	}
	if taskContext.Prompt != "Use the analyzer prompt and report Result Schema v1." {
		t.Fatalf("task context prompt = %q", taskContext.Prompt)
	}
}

func TestCompleteLinkedAgentTaskSignalsTemporalAndCompletesProjectionNode(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration completion signal")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	signaler := &recordingAgentTaskOutcomeSignaler{}
	previousSignaler := testHandler.TaskService.OrchestrationSignaler
	testHandler.TaskService.OrchestrationSignaler = signaler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.TaskService.OrchestrationSignaler = previousSignaler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'running', started_at = now()
		WHERE id = $1
	`, dispatched.TaskID); err != nil {
		t.Fatalf("mark task running: %v", err)
	}

	result := []byte(`{"output":"implemented and tested"}`)
	if _, err := testHandler.TaskService.CompleteTask(t.Context(), parseUUID(dispatched.TaskID), result, "session-1", "/tmp/work"); err != nil {
		t.Fatalf("complete task: %v", err)
	}

	calls := signaler.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one Temporal signal, got %d", len(calls))
	}
	call := calls[0]
	if call.WorkflowID != startBody.Plan.TemporalWorkflowID {
		t.Fatalf("signal workflow id = %q, want %q", call.WorkflowID, startBody.Plan.TemporalWorkflowID)
	}
	if call.PlanID != startBody.Plan.ID || call.NodeID != dispatched.NodeID || call.TaskID != dispatched.TaskID {
		t.Fatalf("signal identity mismatch: %+v", call)
	}
	if call.Status != "completed" {
		t.Fatalf("signal status = %q, want completed", call.Status)
	}
	if call.OutcomeVersion != 1 {
		t.Fatalf("signal outcome version = %d, want 1", call.OutcomeVersion)
	}
	if call.ResultReference == "" {
		t.Fatal("signal result reference must point at the persisted Agent Task result")
	}
	if string(call.Result) != string(result) {
		t.Fatalf("signal result = %s, want %s", call.Result, result)
	}

	var nodeStatus string
	if err := testPool.QueryRow(t.Context(), `
		SELECT status
		FROM orchestration_node
		WHERE id = $1
	`, dispatched.NodeID).Scan(&nodeStatus); err != nil {
		t.Fatalf("load node status: %v", err)
	}
	if nodeStatus != "completed" {
		t.Fatalf("node status = %q, want completed", nodeStatus)
	}

	readW := httptest.NewRecorder()
	readReq := withURLParam(newRequest("GET", "/api/issues/"+issueID+"/orchestration", nil), "id", issueID)
	testHandler.GetIssueOrchestration(readW, readReq)
	if readW.Code != http.StatusOK {
		t.Fatalf("GetIssueOrchestration: expected 200, got %d: %s", readW.Code, readW.Body.String())
	}
	var snapshot struct {
		Plans []struct {
			Nodes []struct {
				WorkflowNodeKey string `json:"workflow_node_key"`
				Status          string `json:"status"`
			} `json:"nodes"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(readW.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode orchestration snapshot: %v", err)
	}
	if len(snapshot.Plans) != 1 {
		t.Fatalf("expected one plan in snapshot, got %d", len(snapshot.Plans))
	}
	var sawCompletedDispatch bool
	for _, node := range snapshot.Plans[0].Nodes {
		if node.WorkflowNodeKey == "dispatch" && node.Status == "completed" {
			sawCompletedDispatch = true
		}
	}
	if !sawCompletedDispatch {
		t.Fatalf("snapshot did not expose completed dispatch node: %+v", snapshot.Plans[0].Nodes)
	}
}

func TestCompleteLinkedAgentTaskSignalFailureDoesNotCompleteProjectionNode(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration signal failure")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	signaler := &recordingAgentTaskOutcomeSignaler{err: errors.New("signal down")}
	previousSignaler := testHandler.TaskService.OrchestrationSignaler
	testHandler.TaskService.OrchestrationSignaler = signaler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.TaskService.OrchestrationSignaler = previousSignaler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'running', started_at = now()
		WHERE id = $1
	`, dispatched.TaskID); err != nil {
		t.Fatalf("mark task running: %v", err)
	}

	if _, err := testHandler.TaskService.CompleteTask(t.Context(), parseUUID(dispatched.TaskID), []byte(`{"ok":true}`), "session-1", "/tmp/work"); err == nil {
		t.Fatal("complete task should return the Temporal signal failure")
	}

	var nodeStatus string
	if err := testPool.QueryRow(t.Context(), `SELECT status FROM orchestration_node WHERE id = $1`, dispatched.NodeID).Scan(&nodeStatus); err != nil {
		t.Fatalf("load node status: %v", err)
	}
	if nodeStatus == "completed" {
		t.Fatalf("node status = %q, signal failure must not pre-project completion", nodeStatus)
	}
}

func TestDaemonCompleteLinkedAgentTaskSignalsRawStructuredResult(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration daemon completion result")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	signaler := &recordingAgentTaskOutcomeSignaler{}
	previousSignaler := testHandler.TaskService.OrchestrationSignaler
	testHandler.TaskService.OrchestrationSignaler = signaler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.TaskService.OrchestrationSignaler = previousSignaler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'running', started_at = now()
		WHERE id = $1
	`, dispatched.TaskID); err != nil {
		t.Fatalf("mark task running: %v", err)
	}

	rawResult := `{"schema_version":"1","summary":"done","changed_files":["server/a.go"],"artifacts":[],"tests":[{"name":"go test ./...","status":"passed"}],"risks":[],"evidence":[{"type":"test","ref":"go test ./..."}]}`
	completeW := httptest.NewRecorder()
	completeReq := newDaemonTokenRequest("POST", "/api/daemon/tasks/"+dispatched.TaskID+"/complete",
		map[string]any{
			"output":     rawResult,
			"session_id": "session-1",
			"work_dir":   "/tmp/work",
		},
		testWorkspaceID, "legit-daemon")
	completeReq = withURLParam(completeReq, "taskId", dispatched.TaskID)
	testHandler.CompleteTask(completeW, completeReq)
	if completeW.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", completeW.Code, completeW.Body.String())
	}

	calls := signaler.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one Temporal signal, got %d", len(calls))
	}
	if string(calls[0].Result) != rawResult {
		t.Fatalf("orchestration signal result = %s, want raw structured result %s", calls[0].Result, rawResult)
	}
	var persisted string
	if err := testPool.QueryRow(t.Context(), `
		SELECT result::text
		FROM agent_task_queue
		WHERE id = $1
	`, dispatched.TaskID).Scan(&persisted); err != nil {
		t.Fatalf("read persisted result: %v", err)
	}
	var persistedPayload map[string]any
	if err := json.Unmarshal([]byte(persisted), &persistedPayload); err != nil {
		t.Fatalf("decode persisted result: %v", err)
	}
	if persistedPayload["schema_version"] != "1" {
		t.Fatalf("persisted orchestration result = %s, want top-level Result Schema v1", persisted)
	}
	if _, ok := persistedPayload["output"]; ok {
		t.Fatalf("persisted orchestration result must not be wrapped in output: %s", persisted)
	}
}

func TestDaemonCompleteLinkedAgentTaskNormalizesMalformedOutputForValidation(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration daemon malformed completion result")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	signaler := &recordingAgentTaskOutcomeSignaler{}
	previousSignaler := testHandler.TaskService.OrchestrationSignaler
	testHandler.TaskService.OrchestrationSignaler = signaler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.TaskService.OrchestrationSignaler = previousSignaler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'running', started_at = now()
		WHERE id = $1
	`, dispatched.TaskID); err != nil {
		t.Fatalf("mark task running: %v", err)
	}

	completeW := httptest.NewRecorder()
	completeReq := newDaemonTokenRequest("POST", "/api/daemon/tasks/"+dispatched.TaskID+"/complete",
		map[string]any{
			"output":     "```json\nnot json\n```",
			"session_id": "session-1",
			"work_dir":   "/tmp/work",
		},
		testWorkspaceID, "legit-daemon")
	completeReq = withURLParam(completeReq, "taskId", dispatched.TaskID)
	testHandler.CompleteTask(completeW, completeReq)
	if completeW.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200 for malformed orchestration output, got %d: %s", completeW.Code, completeW.Body.String())
	}

	calls := signaler.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one Temporal signal, got %d", len(calls))
	}
	if string(calls[0].Result) != "\"```json\\nnot json\\n```\"" {
		t.Fatalf("malformed output should be stored and signaled as a JSON string, got %s", calls[0].Result)
	}

	validation, err := (orchestrationpkg.ActivitySet{}).ValidateOutcome(t.Context(), orchestrationpkg.ValidateOutcomeInput{
		Outcome: calls[0],
		Analysis: orchestrationpkg.AnalyzeIssueResult{
			ProblemSummary: "Fix issue",
		},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  calls[0].PlanID,
			NodeID:  calls[0].NodeID,
			TaskID:  calls[0].TaskID,
			Attempt: calls[0].Attempt,
		},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if validation.ReasonCode != "evidence_insufficient" || validation.RecommendedAction != "retry" {
		t.Fatalf("malformed output validation = %+v, want retryable evidence_insufficient", validation)
	}
}

func TestWorkflowWaitingHumanOverridesTaskCompletionProjectionNode(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration waiting human after failed tests")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	previousSignaler := testHandler.TaskService.OrchestrationSignaler
	testHandler.TaskService.OrchestrationSignaler = nil
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.TaskService.OrchestrationSignaler = previousSignaler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'running', started_at = now()
		WHERE id = $1
	`, dispatched.TaskID); err != nil {
		t.Fatalf("mark task running: %v", err)
	}

	result := []byte(`{"schema_version":"1","summary":"done","changed_files":["server/a.go"],"artifacts":[],"tests":[{"name":"go test ./...","status":"failed"}],"risks":[],"evidence":[{"type":"test","ref":"go test ./..."}]}`)
	if _, err := testHandler.TaskService.CompleteTask(t.Context(), parseUUID(dispatched.TaskID), result, "session-1", "/tmp/work"); err != nil {
		t.Fatalf("complete task: %v", err)
	}

	activity := orchestrationpkg.ActivitySet{DB: testPool}
	if err := activity.FinalizeWorkflow(t.Context(), orchestrationpkg.ValidateOutcomeResult{
		Status:             "waiting_human",
		ReasonCode:         "tests_failed",
		RecommendedAction:  "review",
		NeedsHumanReview:   true,
		TerminalPlanStatus: "waiting_human",
		ProjectionDetail:   "structured result reported failed tests",
		FailedTests:        []string{"go test ./..."},
	}, orchestrationpkg.ReviewOutcomeResult{
		Summary:           "review failed tests",
		RecommendedAction: "review",
	}, orchestrationpkg.SummarizeOutcomeResult{
		Summary:  "handoff needs review",
		TraceRef: startBody.Plan.ID + "/" + dispatched.NodeID + "/" + dispatched.TaskID,
	}, orchestrationpkg.IssueWorkflowInput{
		PlanID:     startBody.Plan.ID,
		WorkflowID: startBody.Plan.TemporalWorkflowID,
	}, orchestrationpkg.IssueSnapshot{
		IssueID: issueID,
	}, orchestrationpkg.AnalyzeIssueResult{
		ProblemSummary: "Fix failed tests",
	}, dispatched, service.AgentTaskOutcomeSignalInput{
		WorkflowID:     startBody.Plan.TemporalWorkflowID,
		PlanID:         startBody.Plan.ID,
		NodeID:         dispatched.NodeID,
		TaskID:         dispatched.TaskID,
		Attempt:        1,
		OutcomeVersion: 1,
		Status:         "completed",
		Result:         result,
	}); err != nil {
		t.Fatalf("finalize workflow: %v", err)
	}

	readW := httptest.NewRecorder()
	readReq := withURLParam(newRequest("GET", "/api/issues/"+issueID+"/orchestration", nil), "id", issueID)
	testHandler.GetIssueOrchestration(readW, readReq)
	if readW.Code != http.StatusOK {
		t.Fatalf("GetIssueOrchestration: expected 200, got %d: %s", readW.Code, readW.Body.String())
	}
	var snapshot struct {
		Plans []struct {
			Status string `json:"status"`
			Nodes  []struct {
				ID               string   `json:"id"`
				Status           string   `json:"status"`
				AvailableActions []string `json:"available_actions"`
			} `json:"nodes"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(readW.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode orchestration snapshot: %v", err)
	}
	if len(snapshot.Plans) != 1 || snapshot.Plans[0].Status != "waiting_human" {
		t.Fatalf("plan status = %+v, want waiting_human", snapshot.Plans)
	}
	for _, node := range snapshot.Plans[0].Nodes {
		if node.ID != dispatched.NodeID {
			continue
		}
		if node.Status != "waiting_human" {
			t.Fatalf("node status = %q, want waiting_human", node.Status)
		}
		if len(node.AvailableActions) != 2 || node.AvailableActions[0] != "approve" || node.AvailableActions[1] != "retry" {
			t.Fatalf("node actions = %+v, want approve/retry", node.AvailableActions)
		}
		return
	}
	t.Fatalf("snapshot missing dispatched node %s: %+v", dispatched.NodeID, snapshot.Plans[0].Nodes)
}

func TestCompleteLinkedAgentTaskPublishesOrchestrationUpdated(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration completion event")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	signaler := &recordingAgentTaskOutcomeSignaler{}
	previousSignaler := testHandler.TaskService.OrchestrationSignaler
	testHandler.TaskService.OrchestrationSignaler = signaler
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.TaskService.OrchestrationSignaler = previousSignaler
	})

	eventsCh := make(chan events.Event, 4)
	testHandler.Bus.Subscribe(protocol.EventOrchestrationUpdated, func(e events.Event) {
		select {
		case eventsCh <- e:
		default:
		}
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	select {
	case <-eventsCh:
	default:
		t.Fatal("expected orchestration:updated event on start")
	}

	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'running', started_at = now()
		WHERE id = $1
	`, dispatched.TaskID); err != nil {
		t.Fatalf("mark task running: %v", err)
	}
	if _, err := testHandler.TaskService.CompleteTask(t.Context(), parseUUID(dispatched.TaskID), []byte(`{"ok":true}`), "session-1", "/tmp/work"); err != nil {
		t.Fatalf("complete task: %v", err)
	}

	select {
	case event := <-eventsCh:
		payload, ok := event.Payload.(map[string]any)
		if !ok {
			t.Fatalf("event payload type = %T, want map", event.Payload)
		}
		if payload["plan_id"] != startBody.Plan.ID {
			t.Fatalf("event plan_id = %v, want %q", payload["plan_id"], startBody.Plan.ID)
		}
		if payload["status"] != "running" && payload["status"] != "completed" && payload["status"] != "failed" {
			t.Fatalf("unexpected orchestration status %v", payload["status"])
		}
	default:
		t.Fatal("expected orchestration:updated event on completion")
	}
}

func TestFailLinkedAgentTaskDoesNotCreateGenericRetryTask(t *testing.T) {
	issueID := createOrchestrationTestIssue(t, "Orchestration failure retry isolation")

	starter := &recordingWorkflowStarter{}
	previousStarter := testHandler.OrchestrationService.Starter
	testHandler.OrchestrationService.Starter = starter
	previousSignaler := testHandler.TaskService.OrchestrationSignaler
	testHandler.TaskService.OrchestrationSignaler = nil
	t.Cleanup(func() {
		testHandler.OrchestrationService.Starter = previousStarter
		testHandler.TaskService.OrchestrationSignaler = previousSignaler
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/orchestration/start", nil), "id", issueID)
	testHandler.StartIssueOrchestration(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartIssueOrchestration: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var startBody struct {
		Plan struct {
			ID                 string `json:"id"`
			TemporalWorkflowID string `json:"temporal_workflow_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	dispatched, err := testHandler.OrchestrationService.DispatchAgentTask(t.Context(), service.DispatchAgentTaskInput{
		PlanID:             startBody.Plan.ID,
		WorkflowNodeKey:    "dispatch",
		Attempt:            1,
		TemporalWorkflowID: startBody.Plan.TemporalWorkflowID,
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	if _, err := testPool.Exec(t.Context(), `
		UPDATE agent_task_queue
		SET status = 'running', started_at = now()
		WHERE id = $1
	`, dispatched.TaskID); err != nil {
		t.Fatalf("mark task running: %v", err)
	}

	if _, err := testHandler.TaskService.FailTask(t.Context(), parseUUID(dispatched.TaskID), "runtime timed out", "session-1", "/tmp/work", "timeout"); err != nil {
		t.Fatalf("fail task: %v", err)
	}

	var retryCount int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM agent_task_queue
		WHERE parent_task_id = $1
	`, dispatched.TaskID).Scan(&retryCount); err != nil {
		t.Fatalf("count retry children: %v", err)
	}
	if retryCount != 0 {
		t.Fatalf("orchestration-linked failures must be retried by the workflow, got %d generic retry tasks", retryCount)
	}
}
