package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateIssueStartsActiveOrchestrationRunWhenWorkspaceEnabled(t *testing.T) {
	setHandlerTestOrchestrationEnabled(t, true)

	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Implement orchestration kernel model", agentID)

	getReq := withURLParam(newRequest(http.MethodGet, "/api/issues/"+created.ID+"/orchestration", nil), "id", created.ID)
	getW := httptest.NewRecorder()
	testHandler.GetIssueOrchestration(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("GetIssueOrchestration: expected 200, got %d: %s", getW.Code, getW.Body.String())
	}

	var resp struct {
		Run *struct {
			ID      string `json:"id"`
			IssueID string `json:"issue_id"`
			Status  string `json:"status"`
		} `json:"run"`
		Nodes []struct {
			Key    string `json:"key"`
			Kind   string `json:"kind"`
			Status string `json:"status"`
		} `json:"nodes"`
		Events []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	if err := json.Unmarshal(getW.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode orchestration response: %v", err)
	}
	if resp.Run == nil {
		t.Fatalf("expected active orchestration run, got nil")
	}
	if resp.Run.IssueID != created.ID || resp.Run.Status != "active" {
		t.Fatalf("unexpected run: %+v", *resp.Run)
	}
	wantNodes := []string{"plan", "execute", "verify"}
	if len(resp.Nodes) != len(wantNodes) {
		t.Fatalf("expected %d nodes, got %d: %+v", len(wantNodes), len(resp.Nodes), resp.Nodes)
	}
	for i, want := range wantNodes {
		if resp.Nodes[i].Key != want {
			t.Fatalf("node %d key = %q, want %q", i, resp.Nodes[i].Key, want)
		}
	}
	if len(resp.Events) == 0 || resp.Events[0].Type != "run_started" {
		t.Fatalf("expected initial run_started event, got %+v", resp.Events)
	}
}

func TestCreateIssueSkipsOrchestrationRowsWhenWorkspaceDisabled(t *testing.T) {
	setHandlerTestOrchestrationEnabled(t, false)

	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Keep direct task path without orchestration", agentID)

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
	if resp.Run != nil || len(resp.Nodes) != 0 || len(resp.Events) != 0 {
		t.Fatalf("expected no orchestration rows, got %+v", resp)
	}

	var taskCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_task_queue
		WHERE issue_id = $1 AND agent_id = $2
	`, created.ID, agentID).Scan(&taskCount); err != nil {
		t.Fatalf("count direct issue tasks: %v", err)
	}
	if taskCount == 0 {
		t.Fatalf("expected disabled workspace to keep direct issue-to-task behavior")
	}
}

func TestEnsureActiveRunForIssueReusesExistingActiveRun(t *testing.T) {
	setHandlerTestOrchestrationEnabled(t, true)

	agentID := handlerTestAgentID(t)
	created := createOrchestrationTestIssue(t, "Re-trigger orchestration idempotently", agentID)
	first := getOrchestrationResponse(t, created.ID)
	if first.Run == nil {
		t.Fatalf("expected first orchestration run")
	}

	issue, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(created.ID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	if _, err := testHandler.OrchestrationService.EnsureActiveRunForIssue(context.Background(), issue, "member", testUserID); err != nil {
		t.Fatalf("ensure active run again: %v", err)
	}

	second := getOrchestrationResponse(t, created.ID)
	if second.Run == nil {
		t.Fatalf("expected second orchestration run")
	}
	if second.Run.ID != first.Run.ID {
		t.Fatalf("expected active run reuse, got first=%s second=%s", first.Run.ID, second.Run.ID)
	}

	var runCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM orchestration_run
		WHERE issue_id = $1 AND status = 'active'
	`, created.ID).Scan(&runCount); err != nil {
		t.Fatalf("count active runs: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("expected exactly one active run, got %d", runCount)
	}
}

func TestGetIssueOrchestrationUsesIssueWorkspaceScope(t *testing.T) {
	setHandlerTestOrchestrationEnabled(t, true)

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

func handlerTestAgentID(t *testing.T) string {
	t.Helper()

	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id
		FROM agent
		WHERE workspace_id = $1 AND archived_at IS NULL
		ORDER BY created_at ASC
		LIMIT 1
	`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatalf("load handler test agent: %v", err)
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
		testPool.Exec(context.Background(), `UPDATE workspace SET settings = '{}'::jsonb WHERE id = $1`, testWorkspaceID)
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
