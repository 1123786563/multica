package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestCreateIssueStartsActiveOrchestrationPlan(t *testing.T) {
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Implement orchestration kernel model", agentID)

	getReq := withURLParam(newRequest(http.MethodGet, "/api/issues/"+created.ID+"/orchestration", nil), "id", created.ID)
	getW := httptest.NewRecorder()
	testHandler.GetIssueOrchestration(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("GetIssueOrchestration: expected 200, got %d: %s", getW.Code, getW.Body.String())
	}

	var resp struct {
		Plans []struct {
			ID         string `json:"id"`
			SourceID   string `json:"source_id"`
			SourceType string `json:"source_type"`
			Status     string `json:"status"`
		} `json:"plans"`
		Nodes []struct {
			Key          string   `json:"key"`
			Type         string   `json:"type"`
			Status       string   `json:"status"`
			Position     int      `json:"position"`
			Dependencies []string `json:"dependencies"`
		} `json:"nodes"`
		Events []struct {
			EventType string `json:"event_type"`
			NodeID    string `json:"node_id"`
		} `json:"events"`
	}
	if err := json.Unmarshal(getW.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode orchestration response: %v", err)
	}
	if len(resp.Plans) != 1 {
		t.Fatalf("expected one active orchestration plan, got %+v", resp.Plans)
	}
	if resp.Plans[0].SourceType != "issue" || resp.Plans[0].SourceID != created.ID || resp.Plans[0].Status != "running" {
		t.Fatalf("unexpected plan: %+v", resp.Plans[0])
	}
	wantNodes := []struct {
		key          string
		nodeType     string
		status       string
		position     int
		dependencies []string
	}{
		{key: "plan", nodeType: "plan", status: "dispatched", position: 1, dependencies: []string{}},
		{key: "execute", nodeType: "execute", status: "pending", position: 2, dependencies: []string{"plan"}},
		{key: "verify", nodeType: "verify", status: "pending", position: 3, dependencies: []string{"execute"}},
	}
	if len(resp.Nodes) != len(wantNodes) {
		t.Fatalf("expected %d nodes, got %d: %+v", len(wantNodes), len(resp.Nodes), resp.Nodes)
	}
	for i, want := range wantNodes {
		got := resp.Nodes[i]
		if got.Key != want.key || got.Type != want.nodeType || got.Status != want.status || got.Position != want.position {
			t.Fatalf("node %d = %+v, want key=%q type=%q status=%q position=%d", i, got, want.key, want.nodeType, want.status, want.position)
		}
		if len(got.Dependencies) != len(want.dependencies) {
			t.Fatalf("node %d dependencies = %+v, want %+v", i, got.Dependencies, want.dependencies)
		}
		for j, dep := range want.dependencies {
			if got.Dependencies[j] != dep {
				t.Fatalf("node %d dependency %d = %q, want %q", i, j, got.Dependencies[j], dep)
			}
		}
	}

	wantEventTypes := map[string]bool{
		"plan.created": true,
		"node.created": true,
	}
	seenEventTypes := map[string]bool{}
	nodeCreatedCount := 0
	for _, event := range resp.Events {
		seenEventTypes[event.EventType] = true
		if event.EventType == "node.created" && event.NodeID != "" {
			nodeCreatedCount++
		}
	}
	for want := range wantEventTypes {
		if !seenEventTypes[want] {
			t.Fatalf("expected initial event %q, got %+v", want, resp.Events)
		}
	}
	if nodeCreatedCount != 3 {
		t.Fatalf("expected 3 node.created events with node ids, got %d from %+v", nodeCreatedCount, resp.Events)
	}
}

func TestCreateIssueStartsActiveOrchestrationPlanWithoutWorkspaceFlag(t *testing.T) {
	setHandlerTestOrchestrationEnabled(t, false)

	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Start orchestration plan without workspace flag", agentID)

	getReq := withURLParam(newRequest(http.MethodGet, "/api/issues/"+created.ID+"/orchestration", nil), "id", created.ID)
	getW := httptest.NewRecorder()
	testHandler.GetIssueOrchestration(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("GetIssueOrchestration: expected 200, got %d: %s", getW.Code, getW.Body.String())
	}

	var resp IssueOrchestrationResponse
	if err := json.Unmarshal(getW.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode orchestration response: %v", err)
	}
	if len(resp.Plans) != 1 {
		t.Fatalf("expected one orchestration plan with workspace flag disabled, got %+v", resp.Plans)
	}
	if len(resp.Nodes) != 3 {
		t.Fatalf("expected plan/execute/verify nodes with workspace flag disabled, got %+v", resp.Nodes)
	}
	tasks, err := testHandler.Queries.ListTasksByIssue(context.Background(), parseUUID(created.ID))
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one orchestration task, got %d", len(tasks))
	}
	if _, ok := service.ParseOrchestrationTaskContext(tasks[0].Context); !ok {
		t.Fatalf("orchestration task should carry orchestration context even with workspace flag disabled: %s", string(tasks[0].Context))
	}
}

func TestOnIssueAssignedReusesExistingActivePlan(t *testing.T) {
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Re-trigger orchestration idempotently", agentID)
	first := getOrchestrationResponse(t, created.ID)
	if len(first.Plans) != 1 {
		t.Fatalf("expected first orchestration run")
	}

	issue, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(created.ID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	if _, err := testHandler.TaskService.Orchestrator.OnIssueAssigned(context.Background(), issue); err != nil {
		t.Fatalf("ensure active plan again: %v", err)
	}

	second := getOrchestrationResponse(t, created.ID)
	if len(second.Plans) != 1 {
		t.Fatalf("expected second orchestration run")
	}
	if second.Plans[0].ID != first.Plans[0].ID {
		t.Fatalf("expected active run reuse, got first=%s second=%s", first.Plans[0].ID, second.Plans[0].ID)
	}

	var runCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM orchestration_plan
		WHERE source_type = 'issue' AND source_id = $1 AND status = 'running'
	`, created.ID).Scan(&runCount); err != nil {
		t.Fatalf("count active runs: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("expected exactly one active run, got %d", runCount)
	}
}

func TestGetIssueOrchestrationUsesIssueWorkspaceScope(t *testing.T) {
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Read orchestration follows issue scope", agentID)

	req := withURLParam(newRequest(http.MethodGet, "/api/issues/"+created.ID+"/orchestration", nil), "id", created.ID)
	req.Header.Set("X-Workspace-ID", "11111111-1111-1111-1111-111111111111")
	w := httptest.NewRecorder()
	testHandler.GetIssueOrchestration(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected wrong workspace to return 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRiskBearingResultMovesRunToWaitingForApproval(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Risk-bearing orchestration result requires approval", agentID)
	issueID := parseUUID(created.ID)

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one orchestration plan, got %d", len(plans))
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one orchestration task, got %d", len(tasks))
	}

	claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, tasks[0].RuntimeID)
	if err != nil {
		t.Fatalf("claim orchestration task: %v", err)
	}
	if claimed == nil {
		t.Fatal("expected claimed orchestration task")
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start orchestration task: %v", err)
	}

	riskResult := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Implementation is complete but requires operator approval before rollout.",
		"changed_files": ["server/internal/service/orchestrator.go"],
		"criteria_evidence": [{"criterion": "node_objective", "evidence": "Implemented the requested orchestration behavior."}],
		"risks": ["Database migration needs operator approval before execution."],
		"confidence": 0.91
	}`)
	if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, riskResult, "", ""); err != nil {
		t.Fatalf("complete orchestration task with risk-bearing result: %v", err)
	}

	nodes, err := testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("reload orchestration nodes: %v", err)
	}
	targetNode := nodeByType(nodes, "plan")
	if !targetNode.ID.Valid {
		t.Fatalf("expected plan node, got %+v", nodes)
	}
	if targetNode.Status != "waiting_human" {
		t.Fatalf("expected plan node waiting_human, got %q", targetNode.Status)
	}

	plan, err := testHandler.Queries.GetOrchestrationPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("reload orchestration plan: %v", err)
	}
	if plan.Status != "waiting_human" {
		t.Fatalf("expected plan waiting_human, got %q", plan.Status)
	}

	events, err := testHandler.Queries.ListOrchestrationEventsByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list orchestration events: %v", err)
	}
	hasEvaluationWaitingHuman := false
	hasNodeWaitingHuman := false
	hasPlanWaitingHuman := false
	for _, event := range events {
		switch event.EventType {
		case "evaluation.waiting_human":
			hasEvaluationWaitingHuman = true
		case "node.waiting_human":
			hasNodeWaitingHuman = true
		case "plan.waiting_human":
			hasPlanWaitingHuman = true
		}
	}
	if !hasEvaluationWaitingHuman || !hasNodeWaitingHuman || !hasPlanWaitingHuman {
		t.Fatalf("expected waiting-human events, got evaluation=%v node=%v plan=%v", hasEvaluationWaitingHuman, hasNodeWaitingHuman, hasPlanWaitingHuman)
	}

	resp := getOrchestrationResponse(t, created.ID)
	if len(resp.Nodes) != 3 {
		t.Fatalf("expected 3 nodes in read API, got %+v", resp.Nodes)
	}
	var targetRespNode OrchestrationNodeResponse
	for _, node := range resp.Nodes {
		if node.Type == "plan" {
			targetRespNode = node
			break
		}
	}
	if targetRespNode.ID == "" || targetRespNode.Summary == nil {
		t.Fatalf("expected node summary in read API, got %+v", resp.Nodes)
	}
	summary := targetRespNode.Summary
	if summary.ReasonCode != "waiting_for_approval" {
		t.Fatalf("expected waiting_for_approval, got %q", summary.ReasonCode)
	}
	if summary.RecommendedAction != "approve" {
		t.Fatalf("expected approve action, got %q", summary.RecommendedAction)
	}
	if !summary.ActionEnabled {
		t.Fatal("expected approval action to be enabled")
	}
	if summary.LatestEvaluationStatus != "waiting_human" {
		t.Fatalf("expected waiting_human evaluation status, got %q", summary.LatestEvaluationStatus)
	}
	if summary.LatestAgentSummary != "Implementation is complete but requires operator approval before rollout." {
		t.Fatalf("unexpected latest agent summary %q", summary.LatestAgentSummary)
	}

	taskCtx, ok := service.ParseOrchestrationTaskContext(claimed.Context)
	if !ok {
		t.Fatalf("task does not carry orchestration context: %s", string(claimed.Context))
	}
	if taskCtx.OrchestrationNodeID != targetRespNode.ID {
		t.Fatalf("expected matching node id, task=%q api=%q", taskCtx.OrchestrationNodeID, targetRespNode.ID)
	}
}

func TestApproveOrchestrationNodeRejectsOrdinaryMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	memberUserID := createRuntimeLocalSkillTestMember(t, "member")
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Approval permission boundary", agentID)
	issueID := parseUUID(created.ID)

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one orchestration plan, got %d", len(plans))
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one orchestration task, got %d", len(tasks))
	}

	claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, tasks[0].RuntimeID)
	if err != nil {
		t.Fatalf("claim orchestration task: %v", err)
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start orchestration task: %v", err)
	}

	riskResult := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Implementation is complete but requires operator approval before rollout.",
		"changed_files": ["server/internal/service/orchestrator.go"],
		"criteria_evidence": [{"criterion": "node_objective", "evidence": "Implemented the requested orchestration behavior."}],
		"risks": ["Database migration needs operator approval before execution."],
		"confidence": 0.91
	}`)
	if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, riskResult, "", ""); err != nil {
		t.Fatalf("complete orchestration task with risk-bearing result: %v", err)
	}

	resp := getOrchestrationResponse(t, created.ID)
	if len(resp.Nodes) != 3 {
		t.Fatalf("expected three orchestration nodes, got %d", len(resp.Nodes))
	}
	targetNodeID := ""
	for _, node := range resp.Nodes {
		if node.Type == "plan" {
			targetNodeID = node.ID
			break
		}
	}
	if targetNodeID == "" {
		t.Fatalf("expected plan node in response, got %+v", resp.Nodes)
	}

	req := withURLParam(
		newRequestAsUser(memberUserID, http.MethodPost, "/api/orchestration/nodes/"+targetNodeID+"/approve", nil),
		"nodeId",
		targetNodeID,
	)
	w := httptest.NewRecorder()
	testHandler.ApproveOrchestrationNode(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for ordinary member approval, got %d: %s", w.Code, w.Body.String())
	}
}

func TestApproveOrchestrationNodeAllowsWorkspaceOwner(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Owner can approve orchestration", agentID)
	issueID := parseUUID(created.ID)

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one orchestration plan, got %d", len(plans))
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, tasks[0].RuntimeID)
	if err != nil {
		t.Fatalf("claim orchestration task: %v", err)
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start orchestration task: %v", err)
	}

	riskResult := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Implementation is complete but requires operator approval before rollout.",
		"changed_files": ["server/internal/service/orchestrator.go"],
		"criteria_evidence": [{"criterion": "node_objective", "evidence": "Implemented the requested orchestration behavior."}],
		"risks": ["Database migration needs operator approval before execution."],
		"confidence": 0.91
	}`)
	if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, riskResult, "", ""); err != nil {
		t.Fatalf("complete orchestration task with risk-bearing result: %v", err)
	}

	resp := getOrchestrationResponse(t, created.ID)
	targetNodeID := ""
	for _, node := range resp.Nodes {
		if node.Type == "plan" {
			targetNodeID = node.ID
			break
		}
	}
	if targetNodeID == "" {
		t.Fatalf("expected plan node in response, got %+v", resp.Nodes)
	}
	req := withURLParam(newRequest(http.MethodPost, "/api/orchestration/nodes/"+targetNodeID+"/approve", nil), "nodeId", targetNodeID)
	w := httptest.NewRecorder()
	testHandler.ApproveOrchestrationNode(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected owner approval to succeed, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRequestChangesOrchestrationNodeCarriesChangeRequestIntoRetryTask(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Owner can request orchestration changes", agentID)
	issueID := parseUUID(created.ID)

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one orchestration plan, got %d", len(plans))
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, tasks[0].RuntimeID)
	if err != nil {
		t.Fatalf("claim orchestration task: %v", err)
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start orchestration task: %v", err)
	}

	riskResult := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Implementation is complete but requires operator approval before rollout.",
		"changed_files": ["server/internal/service/orchestrator.go"],
		"criteria_evidence": [{"criterion": "node_objective", "evidence": "Implemented the requested orchestration behavior."}],
		"risks": ["Database migration needs operator approval before execution."],
		"confidence": 0.91
	}`)
	if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, riskResult, "", ""); err != nil {
		t.Fatalf("complete orchestration task with risk-bearing result: %v", err)
	}

	resp := getOrchestrationResponse(t, created.ID)
	targetNodeID := ""
	for _, node := range resp.Nodes {
		if node.Type == "plan" {
			targetNodeID = node.ID
			break
		}
	}
	if targetNodeID == "" {
		t.Fatalf("expected plan node in response, got %+v", resp.Nodes)
	}
	changeRequest := "Do not touch production DB yet; provide a dry-run migration plan and rollback notes."
	req := withURLParam(
		newRequest(http.MethodPost, "/api/orchestration/nodes/"+targetNodeID+"/request-changes", map[string]any{
			"change_request": changeRequest,
		}),
		"nodeId",
		targetNodeID,
	)
	w := httptest.NewRecorder()
	testHandler.RequestChangesOrchestrationNode(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected request_changes to succeed, got %d: %s", w.Code, w.Body.String())
	}

	tasks, err = testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks after request_changes: %v", err)
	}
	var retryTask db.AgentTaskQueue
	for _, task := range tasks {
		if task.Status == "queued" {
			retryTask = task
			break
		}
	}
	if !retryTask.ID.Valid {
		t.Fatal("expected queued retry task after request_changes")
	}
	retryCtx, ok := service.ParseOrchestrationTaskContext(retryTask.Context)
	if !ok {
		t.Fatalf("retry task does not carry orchestration context: %s", string(retryTask.Context))
	}
	if retryCtx.ChangeRequest != changeRequest {
		t.Fatalf("expected change request to propagate, got %q", retryCtx.ChangeRequest)
	}

	events, err := testHandler.Queries.ListOrchestrationEventsByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list orchestration events: %v", err)
	}
	hasRequestChangesEvent := false
	for _, event := range events {
		if event.EventType == "node.change_requested" {
			hasRequestChangesEvent = true
			break
		}
	}
	if !hasRequestChangesEvent {
		t.Fatal("expected node.change_requested event after request_changes action")
	}
}

func TestGetIssueOrchestrationIncludesPermissionsAndApprovalHistory(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Orchestration read model includes approval metadata", agentID)
	issueID := parseUUID(created.ID)

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one orchestration plan, got %d", len(plans))
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, tasks[0].RuntimeID)
	if err != nil {
		t.Fatalf("claim orchestration task: %v", err)
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start orchestration task: %v", err)
	}
	riskResult := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Implementation is complete but requires operator approval before rollout.",
		"changed_files": ["server/internal/service/orchestrator.go"],
		"criteria_evidence": [{"criterion": "node_objective", "evidence": "Implemented the requested orchestration behavior."}],
		"risks": ["Database migration needs operator approval before execution."],
		"confidence": 0.91
	}`)
	if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, riskResult, "", ""); err != nil {
		t.Fatalf("complete orchestration task with risk-bearing result: %v", err)
	}

	respBefore := getOrchestrationResponse(t, created.ID)
	if len(respBefore.Nodes) != 3 {
		t.Fatalf("expected three nodes before approval action, got %d", len(respBefore.Nodes))
	}
	var targetNode OrchestrationNodeResponse
	for _, node := range respBefore.Nodes {
		if node.Type == "plan" {
			targetNode = node
			break
		}
	}
	if targetNode.ID == "" {
		t.Fatalf("expected plan node before approval action, got %+v", respBefore.Nodes)
	}
	if targetNode.Permissions == nil {
		t.Fatal("expected node permissions before approval action")
	}
	if !targetNode.Permissions.CanApprove || !targetNode.Permissions.CanRequestChanges {
		t.Fatalf("expected owner permissions before action, got %+v", targetNode.Permissions)
	}
	changeRequest := "Add rollback notes and a dry-run plan before approval."
	req := withURLParam(
		newRequest(http.MethodPost, "/api/orchestration/nodes/"+targetNode.ID+"/request-changes", map[string]any{
			"change_request": changeRequest,
		}),
		"nodeId",
		targetNode.ID,
	)
	w := httptest.NewRecorder()
	testHandler.RequestChangesOrchestrationNode(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("request changes: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	resp := getOrchestrationResponse(t, created.ID)
	if len(resp.Nodes) != 3 {
		t.Fatalf("expected three nodes in orchestration response, got %d", len(resp.Nodes))
	}
	node := OrchestrationNodeResponse{}
	for _, item := range resp.Nodes {
		if item.Type == "plan" {
			node = item
			break
		}
	}
	if node.ID == "" {
		t.Fatalf("expected plan node in orchestration response, got %+v", resp.Nodes)
	}
	if node.Permissions == nil {
		t.Fatal("expected node permissions in read API")
	}
	if node.Permissions.CanRetry {
		t.Fatalf("expected waiting_human node to hide retry permission, got %+v", node.Permissions)
	}
	if len(node.ApprovalHistory) == 0 {
		t.Fatal("expected approval history entries in read API")
	}

	var foundChangeRequest bool
	for _, item := range node.ApprovalHistory {
		if item.Action == "request_changes" {
			foundChangeRequest = true
			if item.ActorType != "member" || item.ActorID == nil || *item.ActorID != testUserID {
				t.Fatalf("expected member actor on request_changes history, got %+v", item)
			}
			if item.ChangeRequest == nil || *item.ChangeRequest != changeRequest {
				t.Fatalf("expected change_request detail in history, got %+v", item)
			}
		}
	}
	if !foundChangeRequest {
		t.Fatalf("expected request_changes history item, got %+v", node.ApprovalHistory)
	}
}

func TestGetIssueOrchestrationIncludesLinkedTaskAndArtifactCount(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Orchestration read model includes linked task and artifact count", agentID)
	issueID := parseUUID(created.ID)

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one issue task, got %d", len(tasks))
	}
	claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, tasks[0].RuntimeID)
	if err != nil {
		t.Fatalf("claim orchestration task: %v", err)
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start orchestration task: %v", err)
	}
	result := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Implemented the requested orchestration behavior.",
		"changed_files": ["server/internal/service/orchestrator.go"],
		"criteria_evidence": [{"criterion": "node_objective", "evidence": "Implementation result includes changed files."}],
		"confidence": 0.82
	}`)
	if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, result, "", ""); err != nil {
		t.Fatalf("complete orchestration task: %v", err)
	}

	resp := getOrchestrationResponse(t, created.ID)
	if len(resp.Nodes) != 3 {
		t.Fatalf("expected three nodes, got %d", len(resp.Nodes))
	}
	var node OrchestrationNodeResponse
	for _, item := range resp.Nodes {
		if item.Type == "plan" {
			node = item
			break
		}
	}
	if node.ID == "" {
		t.Fatalf("expected plan node, got %+v", resp.Nodes)
	}
	if node.LinkedTaskID == nil || *node.LinkedTaskID != uuidToString(started.ID) {
		t.Fatalf("expected linked task id %s, got %+v", uuidToString(started.ID), node.LinkedTaskID)
	}
	if node.ArtifactCount < 2 {
		t.Fatalf("expected artifact count to include normalized summary/diff artifacts, got %d", node.ArtifactCount)
	}
}

func TestRiskBearingCompletionPublishesOrchestrationUpdatedEvent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	gotPayload := make(chan map[string]any, 1)
	testHandler.Bus.Subscribe("orchestration:updated", func(e events.Event) {
		if payload, ok := e.Payload.(map[string]any); ok {
			select {
			case gotPayload <- payload:
			default:
			}
		}
	})

	ctx := context.Background()
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Orchestration updated event", agentID)
	issueID := parseUUID(created.ID)

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one orchestration plan, got %d", len(plans))
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, tasks[0].RuntimeID)
	if err != nil {
		t.Fatalf("claim orchestration task: %v", err)
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start orchestration task: %v", err)
	}
	riskResult := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Implementation is complete but requires operator approval before rollout.",
		"changed_files": ["server/internal/service/orchestrator.go"],
		"criteria_evidence": [{"criterion": "node_objective", "evidence": "Implemented the requested orchestration behavior."}],
		"risks": ["Database migration needs operator approval before execution."],
		"confidence": 0.91
	}`)
	if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, riskResult, "", ""); err != nil {
		t.Fatalf("complete orchestration task with risk-bearing result: %v", err)
	}

	select {
	case payload := <-gotPayload:
		if payload["issue_id"] != created.ID {
			t.Fatalf("orchestration:updated issue_id = %v, want %s", payload["issue_id"], created.ID)
		}
		if payload["run_id"] != uuidToString(plans[0].ID) {
			t.Fatalf("orchestration:updated run_id = %v, want %s", payload["run_id"], uuidToString(plans[0].ID))
		}
		if _, ok := payload["changed_at"].(string); !ok {
			t.Fatalf("expected changed_at string in payload, got %+v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive orchestration:updated event within timeout")
	}
}

func TestRiskBearingCompletionCreatesAttentionComment(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Risk-bearing orchestration creates attention comment", agentID)
	issueID := parseUUID(created.ID)

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, tasks[0].RuntimeID)
	if err != nil {
		t.Fatalf("claim orchestration task: %v", err)
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start orchestration task: %v", err)
	}
	riskResult := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Implementation is complete but requires operator approval before rollout.",
		"changed_files": ["server/internal/service/orchestrator.go"],
		"criteria_evidence": [{"criterion": "node_objective", "evidence": "Implemented the requested orchestration behavior."}],
		"risks": ["Database migration needs operator approval before execution."],
		"confidence": 0.91
	}`)
	if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, riskResult, "", ""); err != nil {
		t.Fatalf("complete orchestration task with risk-bearing result: %v", err)
	}

	issue, err := testHandler.Queries.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	comments, err := testHandler.Queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID:     issueID,
		WorkspaceID: issue.WorkspaceID,
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if len(comments) == 0 {
		t.Fatal("expected at least one attention comment")
	}

	last := comments[len(comments)-1]
	if last.AuthorType != "agent" {
		t.Fatalf("expected attention comment author_type=agent, got %q", last.AuthorType)
	}
	if !strings.Contains(last.Content, "Approval required") {
		t.Fatalf("expected attention comment to include reason title, got %q", last.Content)
	}
	if !strings.Contains(last.Content, "Approve") {
		t.Fatalf("expected attention comment to include recommended action, got %q", last.Content)
	}
	if !strings.Contains(last.Content, "Implementation is complete but requires operator approval before rollout.") {
		t.Fatalf("expected attention comment to include evidence summary, got %q", last.Content)
	}
	if strings.Contains(last.Content, "mention://agent/") {
		t.Fatalf("attention comment must not mention agent assignee, got %q", last.Content)
	}
}

func TestRetryExhaustionCreatesAttentionComment(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Retry exhaustion attention comment",
		"description":   "Malformed structured output should exhaust retries and notify humans.",
		"status":        "todo",
		"priority":      "high",
		"assignee_type": "agent",
		"assignee_id":   agentID,
		"acceptance_criteria": []map[string]any{
			{"text": "Kernel persists attention state after retry exhaustion"},
		},
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	issueID := parseUUID(created.ID)
	t.Cleanup(func() {
		req := withURLParam(newRequest(http.MethodDelete, "/api/issues/"+created.ID, nil), "id", created.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), req)
	})

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected exactly one orchestration plan, got %d", len(plans))
	}

	invalidResult := []byte(`{"output":"legacy summary only"}`)
	completeQueuedNode := func(nodeID string) {
		t.Helper()
		tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
		if err != nil {
			t.Fatalf("list issue tasks: %v", err)
		}
		var queued db.AgentTaskQueue
		for _, task := range tasks {
			taskCtx, ok := service.ParseOrchestrationTaskContext(task.Context)
			if task.Status == "queued" && ok && taskCtx.OrchestrationNodeID == nodeID {
				queued = task
				break
			}
		}
		if !queued.ID.Valid {
			t.Fatalf("expected queued task for node %s", nodeID)
		}

		claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, queued.RuntimeID)
		if err != nil {
			t.Fatalf("claim task: %v", err)
		}
		if claimed == nil {
			t.Fatal("expected claimed task")
		}
		started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
		if err != nil {
			t.Fatalf("start task: %v", err)
		}
		if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, invalidResult, "", ""); err != nil {
			t.Fatalf("complete task: %v", err)
		}
	}

	nodes, err := testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list orchestration nodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected at least one orchestration node")
	}
	targetNode := nodeByType(nodes, "plan")
	if !targetNode.ID.Valid {
		t.Fatalf("expected plan node, got %+v", nodes)
	}
	completeQueuedNode(uuidToString(targetNode.ID))

	nodes, err = testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("reload nodes after first attempt: %v", err)
	}
	targetNode = nodeByType(nodes, targetNode.Type)
	completeQueuedNode(uuidToString(targetNode.ID))

	issue, err := testHandler.Queries.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	comments, err := testHandler.Queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID:     issueID,
		WorkspaceID: issue.WorkspaceID,
		Limit:       100,
	})
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if len(comments) == 0 {
		t.Fatal("expected attention comment after retry exhaustion")
	}

	last := comments[len(comments)-1]
	if !strings.Contains(last.Content, "Retries exhausted") {
		t.Fatalf("expected retry exhaustion reason in attention comment, got %q", last.Content)
	}
	if !strings.Contains(last.Content, "Retry") {
		t.Fatalf("expected retry recommendation in attention comment, got %q", last.Content)
	}
	if strings.Contains(last.Content, "mention://agent/") {
		t.Fatalf("attention comment must not mention agent assignee, got %q", last.Content)
	}
}

func TestSuccessfulVerificationDoesNotCreateAttentionComment(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Successful orchestration should not create attention comment",
		"description":   "Happy path should move to review without extra success attention comments.",
		"status":        "todo",
		"priority":      "medium",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	issueID := parseUUID(created.ID)
	t.Cleanup(func() {
		req := withURLParam(newRequest(http.MethodDelete, "/api/issues/"+created.ID, nil), "id", created.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), req)
	})

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one issue task, got %d", len(tasks))
	}
	claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, tasks[0].RuntimeID)
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	result := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Implemented the requested orchestration behavior.",
		"changed_files": ["server/internal/service/orchestrator.go"],
		"criteria_evidence": [{"criterion": "node_objective", "evidence": "Implementation result includes changed files."}],
		"confidence": 0.82
	}`)
	if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, result, "", ""); err != nil {
		t.Fatalf("complete orchestration task: %v", err)
	}

	issue, err := testHandler.Queries.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	comments, err := testHandler.Queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID:     issueID,
		WorkspaceID: issue.WorkspaceID,
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if len(comments) != 0 {
		t.Fatalf("expected no success attention comment for orchestration happy path, got %d comments", len(comments))
	}
}

func TestRuntimeFailureCreatesAttentionCommentAfterRetryExhausted(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Runtime failure attention comment",
		"description":   "Runtime failures that exhaust retries should notify humans.",
		"status":        "todo",
		"priority":      "urgent",
		"assignee_type": "agent",
		"assignee_id":   agentID,
		"acceptance_criteria": []map[string]any{
			{"text": "Runtime task failures are surfaced after retry exhaustion"},
		},
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	issueID := parseUUID(created.ID)
	t.Cleanup(func() {
		req := withURLParam(newRequest(http.MethodDelete, "/api/issues/"+created.ID, nil), "id", created.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), req)
	})

	for attempt := 0; attempt < 2; attempt++ {
		tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
		if err != nil {
			t.Fatalf("list issue tasks: %v", err)
		}
		var queued db.AgentTaskQueue
		for _, task := range tasks {
			if task.Status == "queued" {
				queued = task
				break
			}
		}
		if !queued.ID.Valid {
			t.Fatalf("expected queued task on attempt %d", attempt+1)
		}
		claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, queued.RuntimeID)
		if err != nil {
			t.Fatalf("claim task: %v", err)
		}
		if claimed == nil {
			t.Fatal("expected claimed task")
		}
		started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
		if err != nil {
			t.Fatalf("start task: %v", err)
		}
		if _, err := testHandler.TaskService.FailTask(ctx, started.ID, "runtime crashed", "", "", "runtime_offline"); err != nil {
			t.Fatalf("fail task: %v", err)
		}
	}

	issue, err := testHandler.Queries.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	comments, err := testHandler.Queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID:     issueID,
		WorkspaceID: issue.WorkspaceID,
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if len(comments) == 0 {
		t.Fatal("expected runtime-failure attention comment after retry exhaustion")
	}
	last := comments[len(comments)-1]
	if !strings.Contains(last.Content, "Retries exhausted") && !strings.Contains(last.Content, "Runtime failed") {
		t.Fatalf("expected failure reason in attention comment, got %q", last.Content)
	}
	if strings.Contains(last.Content, "mention://agent/") {
		t.Fatalf("attention comment must not mention agent assignee, got %q", last.Content)
	}
}

func TestRetryExhaustionAttentionCommentIsDeduplicated(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Retry exhaustion dedup",
		"description":   "Retry exhaustion should not spam duplicate attention comments.",
		"status":        "todo",
		"priority":      "high",
		"assignee_type": "agent",
		"assignee_id":   agentID,
		"acceptance_criteria": []map[string]any{
			{"text": "Kernel posts one attention comment per exhausted state"},
		},
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	issueID := parseUUID(created.ID)
	t.Cleanup(func() {
		req := withURLParam(newRequest(http.MethodDelete, "/api/issues/"+created.ID, nil), "id", created.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), req)
	})

	invalidResult := []byte(`{"output":"legacy summary only"}`)
	for attempt := 0; attempt < 2; attempt++ {
		tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
		if err != nil {
			t.Fatalf("list issue tasks: %v", err)
		}
		var queued db.AgentTaskQueue
		for _, task := range tasks {
			if task.Status == "queued" {
				queued = task
				break
			}
		}
		claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, queued.RuntimeID)
		if err != nil {
			t.Fatalf("claim task: %v", err)
		}
		started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
		if err != nil {
			t.Fatalf("start task: %v", err)
		}
		if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, invalidResult, "", ""); err != nil {
			t.Fatalf("complete task: %v", err)
		}
	}

	issue, err := testHandler.Queries.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	comments, err := testHandler.Queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID:     issueID,
		WorkspaceID: issue.WorkspaceID,
		Limit:       100,
	})
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}

	count := 0
	for _, comment := range comments {
		if strings.Contains(comment.Content, "Retries exhausted") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one retry-exhausted attention comment, got %d", count)
	}
}

func TestAttentionStateSubscribesRelevantHumansButNotAgentAssignee(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)
	humanAssigneeID := createRuntimeLocalSkillTestMember(t, "member")
	extraSubscriberID := createRuntimeLocalSkillTestMember(t, "member")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Attention audience selection",
		"description":   "Approval attention should subscribe relevant humans only.",
		"status":        "todo",
		"priority":      "medium",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	issueID := parseUUID(created.ID)
	t.Cleanup(func() {
		req := withURLParam(newRequest(http.MethodDelete, "/api/issues/"+created.ID, nil), "id", created.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), req)
	})

	if _, err := testPool.Exec(ctx, `
		UPDATE issue
		SET assignee_type = 'member', assignee_id = $2
		WHERE id = $1
	`, created.ID, humanAssigneeID); err != nil {
		t.Fatalf("set human assignee: %v", err)
	}
	if err := testHandler.Queries.AddIssueSubscriber(ctx, db.AddIssueSubscriberParams{
		IssueID:  issueID,
		UserType: "member",
		UserID:   parseUUID(extraSubscriberID),
		Reason:   "manual",
	}); err != nil {
		t.Fatalf("add manual subscriber: %v", err)
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, tasks[0].RuntimeID)
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	riskResult := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Implementation is complete but requires operator approval before rollout.",
		"changed_files": ["server/internal/service/orchestrator.go"],
		"criteria_evidence": [{"criterion": "node_objective", "evidence": "Implemented the requested orchestration behavior."}],
		"risks": ["Database migration needs operator approval before execution."],
		"confidence": 0.91
	}`)
	if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, riskResult, "", ""); err != nil {
		t.Fatalf("complete orchestration task: %v", err)
	}

	for _, memberID := range []string{testUserID, humanAssigneeID, extraSubscriberID} {
		subscribed, err := testHandler.Queries.IsIssueSubscriber(ctx, db.IsIssueSubscriberParams{
			IssueID:  issueID,
			UserType: "member",
			UserID:   parseUUID(memberID),
		})
		if err != nil {
			t.Fatalf("check subscriber %s: %v", memberID, err)
		}
		if !subscribed {
			t.Fatalf("expected member %s to be subscribed after attention state", memberID)
		}
	}

	agentSubscribed, err := testHandler.Queries.IsIssueSubscriber(ctx, db.IsIssueSubscriberParams{
		IssueID:  issueID,
		UserType: "agent",
		UserID:   parseUUID(agentID),
	})
	if err != nil {
		t.Fatalf("check agent subscriber: %v", err)
	}
	if agentSubscribed {
		t.Fatalf("agent assignee must not be auto-subscribed as approval audience")
	}
}

func TestRetryOrchestrationNodeRejectsOrdinaryMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	memberUserID := createRuntimeLocalSkillTestMember(t, "member")
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Retry permission boundary", agentID)
	issueID := parseUUID(created.ID)

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, tasks[0].RuntimeID)
	if err != nil {
		t.Fatalf("claim orchestration task: %v", err)
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start orchestration task: %v", err)
	}
	riskResult := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Implementation is complete but requires operator approval before rollout.",
		"changed_files": ["server/internal/service/orchestrator.go"],
		"criteria_evidence": [{"criterion": "node_objective", "evidence": "Implemented the requested orchestration behavior."}],
		"risks": ["Database migration needs operator approval before execution."],
		"confidence": 0.91
	}`)
	if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, riskResult, "", ""); err != nil {
		t.Fatalf("complete orchestration task with risk-bearing result: %v", err)
	}

	resp := getOrchestrationResponse(t, created.ID)
	req := withURLParam(
		newRequestAsUser(memberUserID, http.MethodPost, "/api/orchestration/nodes/"+resp.Nodes[0].ID+"/retry", nil),
		"nodeId",
		resp.Nodes[0].ID,
	)
	w := httptest.NewRecorder()
	testHandler.RetryOrchestrationNode(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for ordinary member retry, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCancelOrchestrationPlanRejectsOrdinaryMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	memberUserID := createRuntimeLocalSkillTestMember(t, "member")
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Cancel permission boundary", agentID)
	issueID := parseUUID(created.ID)

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one orchestration plan, got %d", len(plans))
	}

	req := withURLParam(
		newRequestAsUser(memberUserID, http.MethodPost, "/api/orchestration/plans/"+uuidToString(plans[0].ID)+"/cancel", nil),
		"planId",
		uuidToString(plans[0].ID),
	)
	w := httptest.NewRecorder()
	testHandler.CancelOrchestrationPlan(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for ordinary member cancel, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRetryAndCancelRecordMemberActor(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Retry and cancel audit actor", agentID)
	issueID := parseUUID(created.ID)

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, tasks[0].RuntimeID)
	if err != nil {
		t.Fatalf("claim orchestration task: %v", err)
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start orchestration task: %v", err)
	}
	riskResult := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Implementation is complete but requires operator approval before rollout.",
		"changed_files": ["server/internal/service/orchestrator.go"],
		"criteria_evidence": [{"criterion": "node_objective", "evidence": "Implemented the requested orchestration behavior."}],
		"risks": ["Database migration needs operator approval before execution."],
		"confidence": 0.91
	}`)
	if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, riskResult, "", ""); err != nil {
		t.Fatalf("complete orchestration task with risk-bearing result: %v", err)
	}

	resp := getOrchestrationResponse(t, created.ID)
	retryReq := withURLParam(newRequest(http.MethodPost, "/api/orchestration/nodes/"+resp.Nodes[0].ID+"/retry", nil), "nodeId", resp.Nodes[0].ID)
	retryW := httptest.NewRecorder()
	testHandler.RetryOrchestrationNode(retryW, retryReq)
	if retryW.Code != http.StatusOK {
		t.Fatalf("expected owner retry to succeed, got %d: %s", retryW.Code, retryW.Body.String())
	}

	cancelReq := withURLParam(newRequest(http.MethodPost, "/api/orchestration/plans/"+uuidToString(plans[0].ID)+"/cancel", nil), "planId", uuidToString(plans[0].ID))
	cancelW := httptest.NewRecorder()
	testHandler.CancelOrchestrationPlan(cancelW, cancelReq)
	if cancelW.Code != http.StatusOK {
		t.Fatalf("expected owner cancel to succeed, got %d: %s", cancelW.Code, cancelW.Body.String())
	}

	events, err := testHandler.Queries.ListOrchestrationEventsByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list orchestration events: %v", err)
	}
	var sawRetryActor, sawCancelActor bool
	for _, event := range events {
		if event.EventType == "node.retry_requested" {
			if event.ActorType == "member" && event.ActorID.Valid && uuidToString(event.ActorID) == testUserID {
				sawRetryActor = true
			}
		}
		if event.EventType == "plan.cancelled" {
			if event.ActorType == "member" && event.ActorID.Valid && uuidToString(event.ActorID) == testUserID {
				sawCancelActor = true
			}
		}
	}
	if !sawRetryActor || !sawCancelActor {
		t.Fatalf("expected member actor on retry/cancel events, got retry=%v cancel=%v", sawRetryActor, sawCancelActor)
	}
}

func TestCancelOrchestrationPlanCancelsActiveNodesAndLinkedTasks(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Cancel active orchestration plan", agentID)
	issueID := parseUUID(created.ID)

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one orchestration plan, got %d", len(plans))
	}

	if err := testHandler.TaskService.Orchestrator.CancelPlan(ctx, plans[0].ID, "member", parseUUID(testUserID)); err != nil {
		t.Fatalf("cancel orchestration plan: %v", err)
	}

	plan, err := testHandler.Queries.GetOrchestrationPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("reload plan: %v", err)
	}
	if plan.Status != "cancelled" {
		t.Fatalf("expected cancelled plan, got %q", plan.Status)
	}
	nodes, err := testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("expected three orchestration nodes, got %+v", nodes)
	}
	for _, node := range nodes {
		if node.Status != "cancelled" {
			t.Fatalf("expected every active node to be cancelled, got %+v", nodes)
		}
	}
	tasks, err := testHandler.Queries.ListOrchestrationTasksByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list orchestration tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != "cancelled" {
		t.Fatalf("expected linked task to be cancelled, got %+v", tasks)
	}
	events, err := testHandler.Queries.ListOrchestrationEventsByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var sawNodeCancelled, sawPlanCancelled bool
	for _, event := range events {
		if event.EventType == "node.cancelled" {
			sawNodeCancelled = true
		}
		if event.EventType == "plan.cancelled" {
			sawPlanCancelled = true
		}
	}
	if !sawNodeCancelled || !sawPlanCancelled {
		t.Fatalf("expected cancellation events, got node=%v plan=%v", sawNodeCancelled, sawPlanCancelled)
	}
}

func TestIssueCancellationCancelsActiveOrchestrationPlan(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Issue cancellation cancels orchestration", agentID)
	issueID := parseUUID(created.ID)

	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPut, "/api/issues/"+created.ID, map[string]any{
		"status": "cancelled",
	}), "id", created.ID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue cancel: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 1 || plans[0].Status != "cancelled" {
		t.Fatalf("expected issue cancellation to cancel active plan, got %+v", plans)
	}
	tasks, err := testHandler.Queries.ListOrchestrationTasksByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list orchestration tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != "cancelled" {
		t.Fatalf("expected issue cancellation to cancel linked task, got %+v", tasks)
	}
}

func TestCancelOrchestrationPlanPreservesCompletedTaskEvidenceAndEvents(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Cancel preserves completed evidence", agentID)
	issueID := parseUUID(created.ID)

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, tasks[0].RuntimeID)
	if err != nil {
		t.Fatalf("claim orchestration task: %v", err)
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start orchestration task: %v", err)
	}
	riskResult := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Completed work needs human approval before rollout.",
		"changed_files": ["server/internal/service/orchestrator.go"],
		"criteria_evidence": [{"criterion": "node_objective", "evidence": "Implemented cancellation behavior."}],
		"risks": ["Operator approval required."],
		"confidence": 0.91
	}`)
	if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, riskResult, "", ""); err != nil {
		t.Fatalf("complete orchestration task: %v", err)
	}
	if err := testHandler.TaskService.Orchestrator.CancelPlan(ctx, plans[0].ID, "member", parseUUID(testUserID)); err != nil {
		t.Fatalf("cancel waiting plan: %v", err)
	}
	tasks, err = testHandler.Queries.ListOrchestrationTasksByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list orchestration tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != "completed" {
		t.Fatalf("expected completed linked task to be preserved, got %+v", tasks)
	}
	artifacts, err := testHandler.Queries.ListOrchestrationArtifactsByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(artifacts) == 0 {
		t.Fatal("expected persisted evidence/artifacts to remain after cancellation")
	}
	events, err := testHandler.Queries.ListOrchestrationEventsByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var sawTaskCompleted, sawPlanCancelled bool
	for _, event := range events {
		if event.EventType == "task.completed" {
			sawTaskCompleted = true
		}
		if event.EventType == "plan.cancelled" {
			sawPlanCancelled = true
		}
	}
	if !sawTaskCompleted || !sawPlanCancelled {
		t.Fatalf("expected history to include completed and cancelled events, got completed=%v cancelled=%v", sawTaskCompleted, sawPlanCancelled)
	}
}

func TestRecoverOrchestrationPlanAdvancesCompletedLinkedTask(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Recover completed orchestration task", agentID)
	issueID := parseUUID(created.ID)
	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, tasks[0].RuntimeID)
	if err != nil {
		t.Fatalf("claim orchestration task: %v", err)
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start orchestration task: %v", err)
	}
	validResult := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Recovered task completed successfully.",
		"changed_files": ["server/internal/service/orchestrator.go"],
		"criteria_evidence": [{"criterion": "node_objective", "evidence": "Recovered result is valid."}],
		"risks": [],
		"confidence": 0.95
	}`)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'completed', completed_at = now(), result = $2
		WHERE id = $1
	`, started.ID, validResult); err != nil {
		t.Fatalf("simulate completed task while advancement interrupted: %v", err)
	}

	if err := testHandler.TaskService.Orchestrator.RecoverPlan(ctx, plans[0].ID); err != nil {
		t.Fatalf("recover plan: %v", err)
	}
	nodes, err := testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	recoveredNode := nodeByType(nodes, "plan")
	if !recoveredNode.ID.Valid || recoveredNode.Status != "completed" {
		t.Fatalf("expected recovery to complete plan node, got %+v", nodes)
	}
	tasks, err = testHandler.Queries.ListOrchestrationTasksByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list orchestration tasks: %v", err)
	}
	recoveredNodeTaskCount := 0
	for _, task := range tasks {
		taskCtx, ok := service.ParseOrchestrationTaskContext(task.Context)
		if ok && taskCtx.OrchestrationNodeID == uuidToString(recoveredNode.ID) {
			recoveredNodeTaskCount++
		}
	}
	if recoveredNodeTaskCount != 1 {
		t.Fatalf("expected recovery not to create duplicate tasks for recovered node, got %d tasks: %+v", recoveredNodeTaskCount, tasks)
	}
}

func TestRecoverOrchestrationPlanBlocksUnrecoverableFailedTask(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Recover failed orchestration task", agentID)
	issueID := parseUUID(created.ID)
	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	nodes, err := testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	targetNode := nodeByType(nodes, "plan")
	if !targetNode.ID.Valid {
		t.Fatalf("expected plan node, got %+v", nodes)
	}
	if _, err := testPool.Exec(ctx, `UPDATE orchestration_node SET max_attempts = 1 WHERE id = $1`, targetNode.ID); err != nil {
		t.Fatalf("set max attempts: %v", err)
	}
	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, tasks[0].RuntimeID)
	if err != nil {
		t.Fatalf("claim orchestration task: %v", err)
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start orchestration task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'failed', completed_at = now(), failure_reason = 'runtime_recovery', error = 'lost runtime'
		WHERE id = $1
	`, started.ID); err != nil {
		t.Fatalf("simulate failed task while advancement interrupted: %v", err)
	}

	if err := testHandler.TaskService.Orchestrator.RecoverPlan(ctx, plans[0].ID); err != nil {
		t.Fatalf("recover plan: %v", err)
	}
	plan, err := testHandler.Queries.GetOrchestrationPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("reload plan: %v", err)
	}
	if plan.Status != "failed" {
		t.Fatalf("expected unrecoverable failed task to fail plan, got %q", plan.Status)
	}
	nodes, err = testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	targetNode = nodeByType(nodes, "plan")
	if !targetNode.ID.Valid || targetNode.Status != "failed" {
		t.Fatalf("expected unrecoverable failed task to fail plan node, got %+v", nodes)
	}
	tasks, err = testHandler.Queries.ListOrchestrationTasksByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list orchestration tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected recovery not to duplicate failed task, got %d", len(tasks))
	}
}

func handlerTestAgentID(t *testing.T) string {
	t.Helper()

	name := strings.NewReplacer("/", "-", " ", "-", "(", "-", ")", "-").Replace(t.Name())
	var runtimeID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at
		)
		VALUES ($1, NULL, $2, 'cloud', $3, 'online', $4, '{}'::jsonb, now())
		RETURNING id
	`, testWorkspaceID, "Orchestration Test Runtime "+name, "handler_test_runtime", "Orchestration test runtime").Scan(&runtimeID); err != nil {
		t.Fatalf("create handler test runtime: %v", err)
	}

	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 1, $4)
		RETURNING id
	`, testWorkspaceID, "Orchestration Test Agent "+name, runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create handler test agent: %v", err)
	}
	return agentID
}

func setHandlerTestOrchestrationEnabled(t *testing.T, enabled bool) {
	t.Helper()

	value := "false"
	if enabled {
		value = "true"
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE workspace
		SET settings = jsonb_set(settings, '{orchestration_enabled}', $2::jsonb, true)
		WHERE id = $1
	`, testWorkspaceID, value); err != nil {
		t.Fatalf("set orchestration flag: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `
			UPDATE workspace
			SET settings = jsonb_set(COALESCE(settings, '{}'::jsonb), '{orchestration_enabled}', 'true'::jsonb, true)
			WHERE id = $1
		`, testWorkspaceID)
	})
}

func createOrchestrationTestIssue(t *testing.T, title, agentID string) IssueResponse {
	t.Helper()

	createReq := newRequest(http.MethodPost, "/api/issues", map[string]any{
		"title":         title,
		"status":        "todo",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	createW := httptest.NewRecorder()
	testHandler.CreateIssue(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", createW.Code, createW.Body.String())
	}

	var created IssueResponse
	if err := json.Unmarshal(createW.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	t.Cleanup(func() {
		req := withURLParam(newRequest(http.MethodDelete, "/api/issues/"+created.ID, nil), "id", created.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), req)
	})
	return created
}

func getOrchestrationResponse(t *testing.T, issueID string) IssueOrchestrationResponse {
	t.Helper()

	getReq := withURLParam(newRequest(http.MethodGet, "/api/issues/"+issueID+"/orchestration", nil), "id", issueID)
	getW := httptest.NewRecorder()
	testHandler.GetIssueOrchestration(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("GetIssueOrchestration: expected 200, got %d: %s", getW.Code, getW.Body.String())
	}
	var resp IssueOrchestrationResponse
	if err := json.Unmarshal(getW.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode orchestration response: %v", err)
	}
	return resp
}

func nodeByType(nodes []db.OrchestrationNode, nodeType string) db.OrchestrationNode {
	for _, node := range nodes {
		if node.Type == nodeType {
			return node
		}
	}
	return db.OrchestrationNode{}
}
