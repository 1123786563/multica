package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
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
}

func (s *recordingAgentTaskOutcomeSignaler) SignalAgentTaskOutcome(ctx context.Context, input service.AgentTaskOutcomeSignalInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, input)
	return nil
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
			AND type = 'approval_action'
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
				AND type = 'approval_action'
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

func TestNewHandlerWiresTemporalStarterWhenConfigured(t *testing.T) {
	h := New(testHandler.Queries, testPool, testHandler.Hub, testHandler.Bus, testHandler.EmailService, testHandler.Storage, nil, testHandler.Analytics, Config{
		TemporalHostPort:  "127.0.0.1:7233",
		TemporalTaskQueue: "multica-orchestration-test",
	})
	if h.OrchestrationService == nil || h.OrchestrationService.Starter == nil {
		t.Fatal("expected configured Temporal starter to be wired into orchestration service")
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
		WHERE plan_id = $1 AND node_id = $2 AND type = 'approval_action'
	`, startBody.Plan.ID, dispatched.NodeID).Scan(&auditCount); err != nil {
		t.Fatalf("count approval audits: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("agent approval should not write audit event, got %d", auditCount)
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

	calls := signaler.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one cancel signal, got %d", len(calls))
	}
	if calls[0].Action != "cancel" || calls[0].NodeID != "" {
		t.Fatalf("unexpected cancel signal: %+v", calls[0])
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
