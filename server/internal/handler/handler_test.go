package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var testHandler *Handler
var testPool *pgxpool.Pool
var testUserID string
var testWorkspaceID string
var testRuntimeID string

const (
	handlerTestEmail         = "handler-test@multica.ai"
	handlerTestName          = "Handler Test User"
	handlerTestWorkspaceSlug = "handler-tests"
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("Skipping tests: could not connect to database: %v\n", err)
		os.Exit(0)
	}
	if err := pool.Ping(ctx); err != nil {
		fmt.Printf("Skipping tests: database not reachable: %v\n", err)
		pool.Close()
		os.Exit(0)
	}

	queries := db.New(pool)
	hub := realtime.NewHub()
	go hub.Run()
	bus := events.New()
	emailSvc := service.NewEmailService()
	testHandler = New(queries, pool, hub, bus, emailSvc, nil, nil, analytics.NoopClient{}, Config{AllowSignup: true})
	testPool = pool

	testUserID, testWorkspaceID, err = setupHandlerTestFixture(ctx, pool)
	if err != nil {
		fmt.Printf("Failed to set up handler test fixture: %v\n", err)
		pool.Close()
		os.Exit(1)
	}

	code := m.Run()
	if err := cleanupHandlerTestFixture(context.Background(), pool); err != nil {
		fmt.Printf("Failed to clean up handler test fixture: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	pool.Close()
	os.Exit(code)
}

func setupHandlerTestFixture(ctx context.Context, pool *pgxpool.Pool) (string, string, error) {
	if _, err := pool.Exec(ctx, `
		DELETE FROM orchestration_artifact;
		DELETE FROM orchestration_event;
		DELETE FROM orchestration_edge;
		DELETE FROM orchestration_node;
		DELETE FROM orchestration_plan;
	`); err != nil {
		return "", "", err
	}

	if _, err := pool.Exec(ctx, `
		ALTER TABLE orchestration_node
		    DROP CONSTRAINT IF EXISTS orchestration_node_type_check;
		ALTER TABLE orchestration_node
		    ADD CONSTRAINT orchestration_node_type_check
		    CHECK (type IN ('plan', 'execute', 'verify'));
	`); err != nil {
		return "", "", err
	}

	if err := cleanupHandlerTestFixture(ctx, pool); err != nil {
		return "", "", err
	}

	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, handlerTestName, handlerTestEmail).Scan(&userID); err != nil {
		return "", "", err
	}

	var workspaceID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix, settings)
		VALUES ($1, $2, $3, $4, '{"orchestration_enabled": true}'::jsonb)
		RETURNING id
	`, "Handler Tests", handlerTestWorkspaceSlug, "Temporary workspace for handler tests", "HAN").Scan(&workspaceID); err != nil {
		return "", "", err
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, workspaceID, userID); err != nil {
		return "", "", err
	}

	var runtimeID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at
		)
		VALUES ($1, NULL, $2, 'cloud', $3, 'online', $4, '{}'::jsonb, now())
		RETURNING id
	`, workspaceID, "Handler Test Runtime", "handler_test_runtime", "Handler test runtime").Scan(&runtimeID); err != nil {
		return "", "", err
	}
	testRuntimeID = runtimeID

	if _, err := pool.Exec(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 1, $4)
	`, workspaceID, "Handler Test Agent", runtimeID, userID); err != nil {
		return "", "", err
	}

	return userID, workspaceID, nil
}

func cleanupHandlerTestFixture(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, handlerTestWorkspaceSlug); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, handlerTestEmail); err != nil {
		return err
	}
	return nil
}

func newRequest(method, path string, body any) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	return req
}

func withURLParam(req *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func handlerTestRuntimeID(t *testing.T) string {
	t.Helper()

	var runtimeID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM agent_runtime WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`,
		testWorkspaceID,
	).Scan(&runtimeID); err != nil {
		t.Fatalf("failed to load handler test runtime: %v", err)
	}

	return runtimeID
}

func createHandlerTestAgent(t *testing.T, name string, mcpConfig []byte) string {
	t.Helper()

	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, mcp_config
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'private', 1, $4, '', '{}'::jsonb, '[]'::jsonb, $5)
		RETURNING id
	`, testWorkspaceID, name, handlerTestRuntimeID(t), testUserID, mcpConfig).Scan(&agentID); err != nil {
		t.Fatalf("failed to create handler test agent: %v", err)
	}

	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})

	return agentID
}

func fetchAgentMcpConfig(t *testing.T, agentID string) []byte {
	t.Helper()

	var mcpConfig []byte
	if err := testPool.QueryRow(context.Background(), `SELECT mcp_config FROM agent WHERE id = $1`, agentID).Scan(&mcpConfig); err != nil {
		t.Fatalf("failed to load agent mcp_config: %v", err)
	}

	return mcpConfig
}

func assertJSONEqual(t *testing.T, got []byte, want string) {
	t.Helper()

	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("failed to unmarshal got JSON %q: %v", string(got), err)
	}

	var wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("failed to unmarshal want JSON %q: %v", want, err)
	}

	gotJSON, err := json.Marshal(gotValue)
	if err != nil {
		t.Fatalf("failed to marshal normalized got JSON: %v", err)
	}
	wantJSON, err := json.Marshal(wantValue)
	if err != nil {
		t.Fatalf("failed to marshal normalized want JSON: %v", err)
	}

	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("expected JSON %s, got %s", string(wantJSON), string(gotJSON))
	}
}

func TestIssueCRUD(t *testing.T) {
	// Create
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":    "Test issue from Go test",
		"status":   "todo",
		"priority": "medium",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	if created.Title != "Test issue from Go test" {
		t.Fatalf("CreateIssue: expected title 'Test issue from Go test', got '%s'", created.Title)
	}
	if created.Status != "todo" {
		t.Fatalf("CreateIssue: expected status 'todo', got '%s'", created.Status)
	}
	issueID := created.ID

	// Get
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/issues/"+issueID, nil)
	req = withURLParam(req, "id", issueID)
	testHandler.GetIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var fetched IssueResponse
	json.NewDecoder(w.Body).Decode(&fetched)
	if fetched.ID != issueID {
		t.Fatalf("GetIssue: expected id '%s', got '%s'", issueID, fetched.ID)
	}

	// Update - partial (only status)
	w = httptest.NewRecorder()
	status := "in_progress"
	req = newRequest("PUT", "/api/issues/"+issueID, map[string]any{
		"status": status,
	})
	req = withURLParam(req, "id", issueID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var updated IssueResponse
	json.NewDecoder(w.Body).Decode(&updated)
	if updated.Status != "in_progress" {
		t.Fatalf("UpdateIssue: expected status 'in_progress', got '%s'", updated.Status)
	}
	if updated.Title != "Test issue from Go test" {
		t.Fatalf("UpdateIssue: title should be preserved, got '%s'", updated.Title)
	}
	if updated.Priority != "medium" {
		t.Fatalf("UpdateIssue: priority should be preserved, got '%s'", updated.Priority)
	}

	// List
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/issues?workspace_id="+testWorkspaceID, nil)
	testHandler.ListIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListIssues: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var listResp map[string]any
	json.NewDecoder(w.Body).Decode(&listResp)
	issues := listResp["issues"].([]any)
	if len(issues) == 0 {
		t.Fatal("ListIssues: expected at least 1 issue")
	}

	// Delete
	w = httptest.NewRecorder()
	req = newRequest("DELETE", "/api/issues/"+issueID, nil)
	req = withURLParam(req, "id", issueID)
	testHandler.DeleteIssue(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteIssue: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// Verify deleted
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/issues/"+issueID, nil)
	req = withURLParam(req, "id", issueID)
	testHandler.GetIssue(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetIssue after delete: expected 404, got %d", w.Code)
	}
}

// TestDeleteIssueByIdentifier guards against #1661 — DELETE /api/issues/{id}
// must actually delete the row when the path segment is a human-readable
// identifier ("HAN-42") rather than a UUID. Before the PR #1680 + MUL-1410
// refactor, parseUUID(rawString) silently produced a zero UUID, the SQL
// DELETE matched nothing, and the handler still returned 204.
//
// Also asserts the issue:deleted WS event payload carries the resolved UUID,
// not the raw identifier — frontend caches key by UUID and would otherwise
// leave stale entries on other clients after an identifier-path delete.
func TestDeleteIssueByIdentifier(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":    "Issue to delete by identifier",
		"status":   "todo",
		"priority": "medium",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	if created.Identifier == "" {
		t.Fatalf("CreateIssue: expected identifier to be populated, got empty")
	}

	// Capture the issue:deleted event payload via the bus.
	gotPayload := make(chan map[string]any, 1)
	testHandler.Bus.Subscribe(protocol.EventIssueDeleted, func(e events.Event) {
		if payload, ok := e.Payload.(map[string]any); ok {
			select {
			case gotPayload <- payload:
			default:
			}
		}
	})

	// Delete using the human-readable identifier (e.g. "HAN-1") rather than the UUID.
	w = httptest.NewRecorder()
	req = newRequest("DELETE", "/api/issues/"+created.Identifier, nil)
	req = withURLParam(req, "id", created.Identifier)
	testHandler.DeleteIssue(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteIssue by identifier: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// Verify the row is actually gone — the silent-data-loss bug would have
	// returned 204 here too, but the row would still exist.
	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM issue WHERE id = $1`, created.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Fatalf("DeleteIssue by identifier returned 204 but row still exists (count=%d) — silent-data-loss regression", count)
	}

	// Event payload must carry the resolved UUID, not the identifier string.
	select {
	case payload := <-gotPayload:
		issueID, _ := payload["issue_id"].(string)
		if issueID != created.ID {
			t.Fatalf("issue:deleted event payload issue_id = %q; want resolved UUID %q (must not leak identifier %q)", issueID, created.ID, created.Identifier)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive issue:deleted event within timeout")
	}
}

// TestDeleteIssueRejectsInvalidUUID verifies that a path segment that is
// neither a valid UUID nor a valid identifier returns 404 (not 204) — the
// handler must never silently succeed on malformed input.
func TestDeleteIssueRejectsInvalidUUID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/issues/not-a-uuid-or-identifier", nil)
	req = withURLParam(req, "id", "not-a-uuid-or-identifier")
	testHandler.DeleteIssue(w, req)
	if w.Code == http.StatusNoContent {
		t.Fatalf("DeleteIssue with invalid id: must not return 204; got %d", w.Code)
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("DeleteIssue with invalid id: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateIssueDefaultStatusIsTodo verifies that issues created without an
// explicit status default to "todo" so the daemon picks them up immediately.
// Before this fix the default was "backlog", which daemons ignore.
func TestCreateIssueDefaultStatusIsTodo(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Issue with no explicit status",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	if created.Status != "todo" {
		t.Fatalf("CreateIssue: expected default status 'todo', got '%s'", created.Status)
	}

	// Cleanup
	cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
	cleanupReq = withURLParam(cleanupReq, "id", created.ID)
	testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
}

// TestCreateIssueExplicitBacklogPreserved verifies that explicitly requesting
// "backlog" status is still respected — only the implicit default changed.
func TestCreateIssueExplicitBacklogPreserved(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "Explicit backlog issue",
		"status": "backlog",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	if created.Status != "backlog" {
		t.Fatalf("CreateIssue: expected explicit 'backlog' to be preserved, got '%s'", created.Status)
	}

	// Cleanup
	cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
	cleanupReq = withURLParam(cleanupReq, "id", created.ID)
	testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
}

func TestCreateSubIssueInheritsParentProject(t *testing.T) {
	var projectID, parentID, childID string
	defer func() {
		for _, issueID := range []string{childID, parentID} {
			if issueID == "" {
				continue
			}
			req := newRequest("DELETE", "/api/issues/"+issueID, nil)
			req = withURLParam(req, "id", issueID)
			testHandler.DeleteIssue(httptest.NewRecorder(), req)
		}
		if projectID != "" {
			req := newRequest("DELETE", "/api/projects/"+projectID, nil)
			req = withURLParam(req, "id", projectID)
			testHandler.DeleteProject(httptest.NewRecorder(), req)
		}
	}()

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Sub-issue inheritance project",
	})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProject: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	json.NewDecoder(w.Body).Decode(&project)
	projectID = project.ID

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":      "Parent with project",
		"project_id": projectID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue parent: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var parent IssueResponse
	json.NewDecoder(w.Body).Decode(&parent)
	parentID = parent.ID
	if parent.ProjectID == nil || *parent.ProjectID != projectID {
		t.Fatalf("CreateIssue parent: expected project_id %q, got %v", projectID, parent.ProjectID)
	}

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           "Child without explicit project",
		"parent_issue_id": parentID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue child: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var child IssueResponse
	json.NewDecoder(w.Body).Decode(&child)
	childID = child.ID

	if child.ParentIssueID == nil || *child.ParentIssueID != parentID {
		t.Fatalf("CreateIssue child: expected parent_issue_id %q, got %v", parentID, child.ParentIssueID)
	}
	if child.ProjectID == nil || *child.ProjectID != projectID {
		t.Fatalf("CreateIssue child: expected inherited project_id %q, got %v", projectID, child.ProjectID)
	}
}

func TestCreateSubIssueUsesExplicitProjectOverParentProject(t *testing.T) {
	var parentProjectID, childProjectID, parentID, childID string
	defer func() {
		for _, issueID := range []string{childID, parentID} {
			if issueID == "" {
				continue
			}
			req := newRequest("DELETE", "/api/issues/"+issueID, nil)
			req = withURLParam(req, "id", issueID)
			testHandler.DeleteIssue(httptest.NewRecorder(), req)
		}
		for _, projectID := range []string{childProjectID, parentProjectID} {
			if projectID == "" {
				continue
			}
			req := newRequest("DELETE", "/api/projects/"+projectID, nil)
			req = withURLParam(req, "id", projectID)
			testHandler.DeleteProject(httptest.NewRecorder(), req)
		}
	}()

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Parent project",
	})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProject parent: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var parentProject ProjectResponse
	json.NewDecoder(w.Body).Decode(&parentProject)
	parentProjectID = parentProject.ID

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Child explicit project",
	})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProject child: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var childProject ProjectResponse
	json.NewDecoder(w.Body).Decode(&childProject)
	childProjectID = childProject.ID

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":      "Parent with project",
		"project_id": parentProjectID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue parent: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var parent IssueResponse
	json.NewDecoder(w.Body).Decode(&parent)
	parentID = parent.ID
	if parent.ProjectID == nil || *parent.ProjectID != parentProjectID {
		t.Fatalf("CreateIssue parent: expected project_id %q, got %v", parentProjectID, parent.ProjectID)
	}

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":           "Child with explicit project",
		"parent_issue_id": parentID,
		"project_id":      childProjectID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue child: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var child IssueResponse
	json.NewDecoder(w.Body).Decode(&child)
	childID = child.ID

	if child.ParentIssueID == nil || *child.ParentIssueID != parentID {
		t.Fatalf("CreateIssue child: expected parent_issue_id %q, got %v", parentID, child.ParentIssueID)
	}
	if child.ProjectID == nil || *child.ProjectID != childProjectID {
		t.Fatalf("CreateIssue child: expected explicit project_id %q, got %v", childProjectID, child.ProjectID)
	}
}

// TestCreateIssueRejectsNonexistentMemberAssignee covers the bug where any
// well-formed UUID was accepted as assignee_id without checking workspace
// membership.
func TestCreateIssueRejectsNonexistentMemberAssignee(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Ghost member assignee",
		"assignee_type": "member",
		"assignee_id":   "00000000-0000-0000-0000-000000000000",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue: expected 400 for nonexistent member, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateIssueRejectsNonexistentAgentAssignee verifies the same check on
// the agent branch — previously rejected with 403 "agent not found"; we want a
// consistent 400 from the new validator.
func TestCreateIssueRejectsNonexistentAgentAssignee(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Ghost agent assignee",
		"assignee_type": "agent",
		"assignee_id":   "00000000-0000-0000-0000-000000000000",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue: expected 400 for nonexistent agent, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateIssueRejectsAssigneeTypeWithoutID rejects requests where only one
// of the two fields was supplied — historically this would create an issue
// with an inconsistent state.
func TestCreateIssueRejectsAssigneeTypeWithoutID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Lone assignee_type",
		"assignee_type": "member",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue: expected 400 when only assignee_type is set, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateIssueRejectsAssigneeIDWithoutType is the symmetric case.
func TestCreateIssueRejectsAssigneeIDWithoutType(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":       "Lone assignee_id",
		"assignee_id": testUserID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue: expected 400 when only assignee_id is set, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateIssueRejectsUnknownAssigneeType guards against typos like
// "members" or "user" that previously sneaked through.
func TestCreateIssueRejectsUnknownAssigneeType(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Bogus assignee_type",
		"assignee_type": "user",
		"assignee_id":   testUserID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue: expected 400 for unknown assignee_type, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateIssueAcceptsValidMemberAssignee is the positive control — the
// validator must not block legitimate workspace members.
func TestCreateIssueAcceptsValidMemberAssignee(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Valid member assignee",
		"assignee_type": "member",
		"assignee_id":   testUserID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201 for valid member assignee, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
	cleanupReq = withURLParam(cleanupReq, "id", created.ID)
	testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
}

func TestCreateIssueUsesKernelCompletionByDefault(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Orchestration Test Agent", []byte(`{}`))
	skillID := insertHandlerTestSkill(t, "orchestration-runtime-adapter-skill", "# Runtime adapter skill\nUse existing task lifecycle.")
	if _, err := testPool.Exec(ctx, `INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`, agentID, skillID); err != nil {
		t.Fatalf("attach skill to orchestration agent: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Exercise orchestration kernel from issue create",
		"description":   "The runtime must not be allowed to decide the issue is complete.",
		"status":        "todo",
		"priority":      "urgent",
		"assignee_type": "agent",
		"assignee_id":   agentID,
		"acceptance_criteria": []map[string]any{
			{"text": "Kernel evaluates structured evidence before closing the issue"},
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
	if plans[0].Status != "running" {
		t.Fatalf("expected running plan after issue create, got %q", plans[0].Status)
	}

	nodes, err := testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list orchestration nodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("expected plan/execute/verify nodes, got %d", len(nodes))
	}
	nodeByType := func(nodes []db.OrchestrationNode, nodeType string) db.OrchestrationNode {
		t.Helper()
		for _, node := range nodes {
			if node.Type == nodeType {
				return node
			}
		}
		t.Fatalf("missing %s node", nodeType)
		return db.OrchestrationNode{}
	}
	planNode := nodeByType(nodes, "plan")
	executeNode := nodeByType(nodes, "execute")
	verifyNode := nodeByType(nodes, "verify")
	if planNode.Status != "dispatched" || planNode.AttemptCount != 1 {
		t.Fatalf("expected dispatched plan node with one attempt, got status=%q attempts=%d", planNode.Status, planNode.AttemptCount)
	}
	if executeNode.Status != "pending" || verifyNode.Status != "pending" {
		t.Fatalf("downstream nodes should wait on graph dependencies, got %q/%q", executeNode.Status, verifyNode.Status)
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one orchestration task, got %d", len(tasks))
	}
	taskCtx, ok := service.ParseOrchestrationTaskContext(tasks[0].Context)
	if !ok {
		t.Fatalf("queued task does not carry orchestration context: %s", string(tasks[0].Context))
	}
	if taskCtx.OrchestrationPlanID != uuidToString(plans[0].ID) || taskCtx.OrchestrationNodeID != uuidToString(planNode.ID) {
		t.Fatalf("task context does not point to created plan/node: %#v", taskCtx)
	}
	if taskCtx.OrchestrationRunID == "" {
		t.Fatalf("task context should include orchestration run id: %#v", taskCtx)
	}
	if taskCtx.NodeType != "plan" || taskCtx.Attempt != 1 {
		t.Fatalf("task context should identify node type and attempt, got node_type=%q attempt=%d", taskCtx.NodeType, taskCtx.Attempt)
	}
	if len(taskCtx.ExpectedResultSchema) == 0 {
		t.Fatalf("task context should include expected result schema")
	}
	if !json.Valid(taskCtx.ExpectedResultSchema) {
		t.Fatalf("expected result schema should be valid JSON: %s", string(taskCtx.ExpectedResultSchema))
	}

	assertOrchestrationEvents := func(required []string) {
		t.Helper()
		events, err := testHandler.Queries.ListOrchestrationEventsByPlan(ctx, plans[0].ID)
		if err != nil {
			t.Fatalf("list orchestration events: %v", err)
		}
		eventTypes := map[string]bool{}
		for _, event := range events {
			eventTypes[event.EventType] = true
		}
		for _, required := range required {
			if !eventTypes[required] {
				t.Fatalf("missing orchestration event %q; got %#v", required, eventTypes)
			}
		}
	}

	claimW := httptest.NewRecorder()
	claimReq := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+uuidToString(tasks[0].RuntimeID)+"/claim", nil, testWorkspaceID, "test-orchestration-claim")
	claimReq = withURLParam(claimReq, "runtimeId", uuidToString(tasks[0].RuntimeID))
	testHandler.ClaimTaskByRuntime(claimW, claimReq)
	if claimW.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", claimW.Code, claimW.Body.String())
	}
	var claimResp struct {
		Task *struct {
			ID    string `json:"id"`
			Agent *struct {
				Skills []struct {
					Name    string `json:"name"`
					Content string `json:"content"`
				} `json:"skills"`
			} `json:"agent"`
			Orchestration service.OrchestrationTaskContext `json:"orchestration"`
		} `json:"task"`
	}
	if err := json.NewDecoder(claimW.Body).Decode(&claimResp); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if claimResp.Task == nil || claimResp.Task.ID != uuidToString(tasks[0].ID) {
		t.Fatalf("claim orchestration task: expected %s, got %#v", uuidToString(tasks[0].ID), claimResp.Task)
	}
	if claimResp.Task.Orchestration.OrchestrationNodeID != uuidToString(planNode.ID) || claimResp.Task.Orchestration.Attempt != 1 {
		t.Fatalf("claim response should include orchestration node context, got %#v", claimResp.Task.Orchestration)
	}
	if claimResp.Task.Agent == nil || len(claimResp.Task.Agent.Skills) == 0 {
		t.Fatalf("claim response should include agent-bound skill context")
	}
	if !strings.HasPrefix(claimResp.Task.Agent.Skills[0].Name, "orchestration-runtime-adapter-skill") {
		t.Fatalf("unexpected claim skill context: %#v", claimResp.Task.Agent.Skills)
	}
	claimed, err := testHandler.Queries.GetAgentTask(ctx, tasks[0].ID)
	if err != nil {
		t.Fatalf("reload claimed orchestration task: %v", err)
	}
	started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("start orchestration task: %v", err)
	}
	if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, []byte(`{"output":"I completed the whole issue."}`), "", ""); err != nil {
		t.Fatalf("complete orchestration task after legacy output: %v", err)
	}

	issueAfterLegacyOutput, err := testHandler.Queries.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("reload issue after legacy output: %v", err)
	}
	if issueAfterLegacyOutput.Status == "done" {
		t.Fatal("legacy runtime output must not close the issue without evaluator evidence")
	}

	nodes, err = testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("reload orchestration nodes: %v", err)
	}
	planNode = nodeByType(nodes, "plan")
	if planNode.Status != "dispatched" || planNode.AttemptCount != 2 {
		t.Fatalf("failed evaluation should schedule retry, got status=%q attempts=%d", planNode.Status, planNode.AttemptCount)
	}
	plansAfterRetry, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("reload orchestration plans: %v", err)
	}
	if plansAfterRetry[0].Status != "running" {
		t.Fatalf("plan should remain running after retry scheduling, got %q", plansAfterRetry[0].Status)
	}

	tasks, err = testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks after retry: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected retry task after evaluator rejection, got %d tasks", len(tasks))
	}
	assertOrchestrationEvents([]string{
		"plan.created",
		"node.created",
		"node.dispatched",
		"node.running",
		"node.evaluating",
		"task.completed",
		"evaluation.invalid_result",
		"node.retry_scheduled",
	})

	var retryTask db.AgentTaskQueue
	for _, task := range tasks {
		if task.Status == "queued" {
			retryTask = task
		}
	}
	if !retryTask.ID.Valid {
		t.Fatal("expected queued retry task")
	}
	claimedRetry, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, retryTask.RuntimeID)
	if err != nil {
		t.Fatalf("claim retry task: %v", err)
	}
	if claimedRetry == nil || uuidToString(claimedRetry.ID) != uuidToString(retryTask.ID) {
		t.Fatalf("claim retry task: expected %s, got %#v", uuidToString(retryTask.ID), claimedRetry)
	}
	retryTaskCtx, ok := service.ParseOrchestrationTaskContext(claimedRetry.Context)
	if !ok {
		t.Fatalf("retry task does not carry orchestration context: %s", string(claimedRetry.Context))
	}
	if !strings.Contains(retryTaskCtx.PriorEvidenceSummary, "Kernel reason: evidence_insufficient") {
		t.Fatalf("retry task should include prior evidence summary, got %#v", retryTaskCtx)
	}
	if !strings.Contains(retryTaskCtx.ChangeRequest, "Structured result payload did not satisfy the orchestration result contract.") {
		t.Fatalf("retry task should include change request, got %#v", retryTaskCtx)
	}
	startedRetry, err := testHandler.TaskService.StartTask(ctx, claimedRetry.ID)
	if err != nil {
		t.Fatalf("start retry task: %v", err)
	}
	planResult := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Planned the issue context and identified the implementation path.",
		"criteria_evidence": [{"criterion": "Kernel evaluates structured evidence before closing the issue", "evidence": "Structured result includes changed files and passing tests"}],
		"confidence": 0.82
	}`)
	if _, err := testHandler.TaskService.CompleteTask(ctx, startedRetry.ID, planResult, "", ""); err != nil {
		t.Fatalf("complete orchestration task after structured result: %v", err)
	}

	nodes, err = testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("reload nodes after plan completion: %v", err)
	}
	planNode = nodeByType(nodes, "plan")
	executeNode = nodeByType(nodes, "execute")
	verifyNode = nodeByType(nodes, "verify")
	if planNode.Status != "completed" || executeNode.Status != "dispatched" || verifyNode.Status != "pending" {
		t.Fatalf("plan completion should dispatch execute only, got %q/%q/%q", planNode.Status, executeNode.Status, verifyNode.Status)
	}
	issueAfterPlan, err := testHandler.Queries.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("reload issue after plan: %v", err)
	}
	if issueAfterPlan.Status == "done" {
		t.Fatal("issue must stay open until all plan nodes pass evaluation")
	}

	completeQueuedNode := func(node db.OrchestrationNode, result []byte) {
		t.Helper()
		tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
		if err != nil {
			t.Fatalf("list tasks for node %s: %v", node.Type, err)
		}
		var queued db.AgentTaskQueue
		for _, task := range tasks {
			taskCtx, ok := service.ParseOrchestrationTaskContext(task.Context)
			if task.Status == "queued" && ok && taskCtx.OrchestrationNodeID == uuidToString(node.ID) {
				queued = task
				break
			}
		}
		if !queued.ID.Valid {
			t.Fatalf("expected queued task for %s node", node.Type)
		}
		claimed, err := testHandler.TaskService.ClaimTaskForRuntime(ctx, queued.RuntimeID)
		if err != nil {
			t.Fatalf("claim %s task: %v", node.Type, err)
		}
		if claimed == nil || uuidToString(claimed.ID) != uuidToString(queued.ID) {
			t.Fatalf("claim %s task: expected %s, got %#v", node.Type, uuidToString(queued.ID), claimed)
		}
		started, err := testHandler.TaskService.StartTask(ctx, claimed.ID)
		if err != nil {
			t.Fatalf("start %s task: %v", node.Type, err)
		}
		if _, err := testHandler.TaskService.CompleteTask(ctx, started.ID, result, "", ""); err != nil {
			t.Fatalf("complete %s task: %v", node.Type, err)
		}
	}

	executeResult := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Implemented the requested orchestration behavior.",
		"changed_files": ["server/internal/service/orchestrator.go"],
		"criteria_evidence": [{"criterion": "Kernel evaluates structured evidence before closing the issue", "evidence": "Implementation result includes changed files"}],
		"confidence": 0.82
	}`)
	completeQueuedNode(executeNode, executeResult)
	nodes, err = testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("reload nodes after execute completion: %v", err)
	}
	executeNode = nodeByType(nodes, "execute")
	verifyNode = nodeByType(nodes, "verify")
	if executeNode.Status != "completed" || verifyNode.Status != "dispatched" {
		t.Fatalf("execute completion should dispatch verify, got execute=%q verify=%q", executeNode.Status, verifyNode.Status)
	}

	verifyResult := []byte(`{
		"schema_version": 1,
		"status": "completed",
		"summary": "Verified the implementation against the acceptance criteria.",
		"test_result": {"status": "passed", "passed": true},
		"criteria_evidence": [{"criterion": "Kernel evaluates structured evidence before closing the issue", "evidence": "Test node reports passing test evidence"}],
		"confidence": 0.82
	}`)
	completeQueuedNode(verifyNode, verifyResult)

	finalPlan, err := testHandler.Queries.GetOrchestrationPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("reload final plan: %v", err)
	}
	if finalPlan.Status != "completed" {
		t.Fatalf("expected evaluator-approved plan completion, got %q", finalPlan.Status)
	}
	finalNodes, err := testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("reload final nodes: %v", err)
	}
	for _, node := range finalNodes {
		if node.Status != "completed" {
			t.Fatalf("expected evaluator-approved node completion for %s, got %q", node.Type, node.Status)
		}
	}
	finalIssue, err := testHandler.Queries.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("reload final issue: %v", err)
	}
	if finalIssue.Status != "in_review" {
		t.Fatalf("expected kernel to move issue to review after evaluator approval, got %q", finalIssue.Status)
	}
	if finalIssue.Status == "done" {
		t.Fatal("kernel verification must not automatically mark issue done")
	}
	assertOrchestrationEvents([]string{
		"evaluation.passed",
		"node.completed",
		"plan.completed",
	})

	getReq := withURLParam(newRequest(http.MethodGet, "/api/issues/"+created.ID+"/orchestration", nil), "id", created.ID)
	getW := httptest.NewRecorder()
	testHandler.GetIssueOrchestration(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("GetIssueOrchestration: expected 200, got %d: %s", getW.Code, getW.Body.String())
	}
	var orchestrationResp IssueOrchestrationResponse
	if err := json.NewDecoder(getW.Body).Decode(&orchestrationResp); err != nil {
		t.Fatalf("decode orchestration response: %v", err)
	}
	if len(orchestrationResp.Artifacts) == 0 {
		t.Fatal("expected persisted node evidence artifacts in orchestration read API")
	}
	hasSummary, hasChangedFiles, hasTestResult, hasLinkedTask := false, false, false, false
	for _, artifact := range orchestrationResp.Artifacts {
		if artifact.TaskID != nil {
			hasLinkedTask = true
		}
		switch artifact.Type {
		case "summary":
			hasSummary = true
		case "diff":
			var content struct {
				ChangedFiles []string `json:"changed_files"`
			}
			if err := json.Unmarshal(artifact.Content, &content); err == nil && len(content.ChangedFiles) > 0 {
				hasChangedFiles = true
			}
		case "test_result":
			hasTestResult = true
		}
	}
	if !hasSummary || !hasChangedFiles || !hasTestResult || !hasLinkedTask {
		t.Fatalf("orchestration read API missing evidence fields: summary=%v changed_files=%v test_result=%v linked_task=%v artifacts=%+v", hasSummary, hasChangedFiles, hasTestResult, hasLinkedTask, orchestrationResp.Artifacts)
	}
}

func TestDaemonCompleteTask_UsesExplicitStructuredResultForOrchestration(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Explicit Result Agent", []byte(`{}`))

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Daemon explicit result",
		"description":   "Structured result should be honored even when output is plain prose.",
		"status":        "todo",
		"priority":      "urgent",
		"assignee_type": "agent",
		"assignee_id":   agentID,
		"acceptance_criteria": []map[string]any{
			{"text": "Structured result protocol is used"},
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

	nodes, err := testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list orchestration nodes: %v", err)
	}
	var planNode db.OrchestrationNode
	for _, node := range nodes {
		if node.Type == "plan" {
			planNode = node
			break
		}
	}
	if !planNode.ID.Valid {
		t.Fatal("missing plan node")
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

	daemonReq := newDaemonTokenRequest("POST", "/api/daemon/tasks/"+uuidToString(started.ID)+"/complete", map[string]any{
		"output": "human-readable terminal text",
		"result": map[string]any{
			"schema_version":    1,
			"status":            "completed",
			"summary":           "Planned the issue context and identified the implementation path.",
			"criteria_evidence": []map[string]any{{"criterion": "Structured result protocol is used", "evidence": "Explicit result payload satisfied the evaluator."}},
			"confidence":        0.82,
		},
	}, testWorkspaceID, "legit-daemon")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("taskId", uuidToString(started.ID))
	daemonReq = daemonReq.WithContext(context.WithValue(daemonReq.Context(), chi.RouteCtxKey, rctx))
	w = httptest.NewRecorder()
	testHandler.CompleteTask(w, daemonReq)
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	nodes, err = testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("reload nodes: %v", err)
	}
	for _, node := range nodes {
		if node.ID == planNode.ID && node.Status != "completed" {
			t.Fatalf("expected plan node to complete from explicit result payload, got %q", node.Status)
		}
	}
}

func TestIssueAssignedToAgent_OrchestrationFlagOffStillUsesOrchestrationPath(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Legacy Path Agent", []byte(`{}`))

	setHandlerTestOrchestrationEnabled(t, false)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Legacy path without flag",
		"description":   "Disabled workspaces should keep the existing direct task path.",
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
	if _, ok := service.ParseOrchestrationTaskContext(tasks[0].Context); !ok {
		t.Fatalf("orchestration task should carry orchestration context: %s", string(tasks[0].Context))
	}
}

func TestIssueAssignedToAgent_DoesNotFallbackToLegacyWhenOrchestratorUnavailable(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Unavailable Orchestrator Agent", []byte(`{}`))

	original := testHandler.TaskService.Orchestrator
	testHandler.TaskService.Orchestrator = nil
	t.Cleanup(func() {
		testHandler.TaskService.Orchestrator = original
	})

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "No legacy fallback when orchestrator unavailable",
		"description":   "Agent-assigned issues must not silently enqueue legacy tasks.",
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

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 0 {
		t.Fatalf("expected no orchestration plan when orchestrator is unavailable, got %d", len(plans))
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected no fallback legacy task when orchestrator is unavailable, got %d", len(tasks))
	}
}

func TestCreateIssueStopsAfterRetryExhaustionByDefault(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Retry Exhaust Agent", []byte(`{}`))

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Retry exhaustion",
		"description":   "Invalid evaluator results should stop after max attempts.",
		"status":        "todo",
		"priority":      "urgent",
		"assignee_type": "agent",
		"assignee_id":   agentID,
		"acceptance_criteria": []map[string]any{
			{"text": "Structured result must include evidence"},
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

	nodeByType := func(nodes []db.OrchestrationNode, nodeType string) db.OrchestrationNode {
		t.Helper()
		for _, node := range nodes {
			if node.Type == nodeType {
				return node
			}
		}
		t.Fatalf("missing %s node", nodeType)
		return db.OrchestrationNode{}
	}

	invalidResult := []byte(`{"status":"completed","summary":"done without evidence"}`)

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
	planNode := nodeByType(nodes, "plan")
	completeQueuedNode(uuidToString(planNode.ID))

	nodes, err = testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("reload nodes after first attempt: %v", err)
	}
	planNode = nodeByType(nodes, "plan")
	if planNode.AttemptCount != 2 {
		t.Fatalf("expected retry to increment attempt count to 2, got %d", planNode.AttemptCount)
	}
	if planNode.Status != "dispatched" {
		t.Fatalf("expected plan node dispatched after first failed evaluation, got %q", planNode.Status)
	}

	completeQueuedNode(uuidToString(planNode.ID))

	nodes, err = testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("reload nodes after retry exhaustion: %v", err)
	}
	planNode = nodeByType(nodes, "plan")
	if planNode.AttemptCount != planNode.MaxAttempts {
		t.Fatalf("attempt_count=%d max_attempts=%d", planNode.AttemptCount, planNode.MaxAttempts)
	}
	if planNode.Status != "failed" {
		t.Fatalf("expected node failed after retry exhaustion, got %s", planNode.Status)
	}

	finalPlan, err := testHandler.Queries.GetOrchestrationPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("reload plan after retry exhaustion: %v", err)
	}
	if finalPlan.Status != "failed" {
		t.Fatalf("expected failed plan after retry exhaustion, got %q", finalPlan.Status)
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks after retry exhaustion: %v", err)
	}
	queuedCount := 0
	queuedForPlan := 0
	for _, task := range tasks {
		if task.Status != "queued" {
			continue
		}
		queuedCount++
		taskCtx, ok := service.ParseOrchestrationTaskContext(task.Context)
		if ok && taskCtx.OrchestrationNodeID == uuidToString(planNode.ID) {
			queuedForPlan++
		}
	}
	if queuedForPlan != 0 {
		t.Fatalf("expected no queued retry task after exhaustion, got %d", queuedForPlan)
	}
	if queuedCount != 0 {
		t.Fatalf("expected no queued tasks after exhaustion, got %d", queuedCount)
	}

	getReq := withURLParam(newRequest(http.MethodGet, "/api/issues/"+created.ID+"/orchestration", nil), "id", created.ID)
	getW := httptest.NewRecorder()
	testHandler.GetIssueOrchestration(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("GetIssueOrchestration: expected 200, got %d: %s", getW.Code, getW.Body.String())
	}
	var orchestrationResp IssueOrchestrationResponse
	if err := json.NewDecoder(getW.Body).Decode(&orchestrationResp); err != nil {
		t.Fatalf("decode orchestration response after retry exhaustion: %v", err)
	}
	var planSummary *NodeSummaryDTO
	for _, node := range orchestrationResp.Nodes {
		if node.ID == uuidToString(planNode.ID) {
			planSummary = node.Summary
			break
		}
	}
	if planSummary == nil {
		t.Fatal("expected plan node summary after retry exhaustion")
	}
	if planSummary.ReasonCode != "retry_exhausted" {
		t.Fatalf("expected retry_exhausted reason code, got %q", planSummary.ReasonCode)
	}
	if planSummary.LatestEvaluationStatus != "evidence_insufficient" {
		t.Fatalf("expected evidence_insufficient latest evaluation status, got %q", planSummary.LatestEvaluationStatus)
	}
	if planSummary.RecommendedAction != "retry" {
		t.Fatalf("expected retry recommended action, got %q", planSummary.RecommendedAction)
	}
	if !strings.Contains(planSummary.PriorEvidenceSummary, "Kernel reason: evidence_insufficient") {
		t.Fatalf("expected prior evidence summary in read API, got %#v", planSummary)
	}
}

func TestOrchestrationFailTaskRoutesThroughKernelRetry(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "FailTask Kernel Agent", []byte(`{}`))

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "FailTask retry path",
		"description":   "Kernel should own orchestration task failure retries.",
		"status":        "todo",
		"priority":      "urgent",
		"assignee_type": "agent",
		"assignee_id":   agentID,
		"acceptance_criteria": []map[string]any{
			{"text": "Runtime task failures are retried by kernel state machine"},
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

	nodeByType := func(nodes []db.OrchestrationNode, nodeType string) db.OrchestrationNode {
		t.Helper()
		for _, node := range nodes {
			if node.Type == nodeType {
				return node
			}
		}
		t.Fatalf("missing %s node", nodeType)
		return db.OrchestrationNode{}
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
	if _, err := testHandler.TaskService.FailTask(ctx, started.ID, "runtime crashed", "", "", "runtime_offline"); err != nil {
		t.Fatalf("fail orchestration task: %v", err)
	}

	nodes, err := testHandler.Queries.ListOrchestrationNodesByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list orchestration nodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected orchestration nodes")
	}
	planNode := nodeByType(nodes, "plan")
	if planNode.AttemptCount != 2 {
		t.Fatalf("expected failed attempt to consume retry budget, got attempt_count=%d", planNode.AttemptCount)
	}
	if planNode.Status != "dispatched" {
		t.Fatalf("expected retry node to be dispatched, got %q", planNode.Status)
	}

	events, err := testHandler.Queries.ListOrchestrationEventsByPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	eventTypes := map[string]bool{}
	for _, event := range events {
		eventTypes[event.EventType] = true
	}
	if !eventTypes["task.failed"] {
		t.Fatalf("missing task.failed event: %#v", eventTypes)
	}
	if !eventTypes["node.retry_scheduled"] {
		t.Fatalf("expected retry after task failure, got events %#v", eventTypes)
	}

	tasks, err = testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list tasks after fail: %v", err)
	}
	queuedCount := 0
	for _, task := range tasks {
		if task.Status == "queued" {
			queuedCount++
		}
	}
	if queuedCount != 1 {
		t.Fatalf("expected exactly one queued retry task, got %d", queuedCount)
	}
}

// TestCreateIssueRejectsMalformedAssigneeID covers the case where parseUUID
// silently produces an invalid pgtype.UUID and the validator would otherwise
// treat (no type + unparseable id) as "no assignee" and accept the request.
func TestCreateIssueRejectsMalformedAssigneeID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":       "Malformed assignee_id only",
		"assignee_id": "not-a-uuid",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue: expected 400 for malformed assignee_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateIssueRejectsMalformedAttachmentIDBeforeWrite(t *testing.T) {
	var before int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM issue WHERE workspace_id = $1`, testWorkspaceID).Scan(&before); err != nil {
		t.Fatalf("count issues before: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":          "Malformed attachment issue",
		"attachment_ids": []string{"not-a-uuid"},
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateIssue: expected 400 for malformed attachment_ids, got %d: %s", w.Code, w.Body.String())
	}

	var after int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM issue WHERE workspace_id = $1`, testWorkspaceID).Scan(&after); err != nil {
		t.Fatalf("count issues after: %v", err)
	}
	if after != before {
		t.Fatalf("CreateIssue: malformed attachment_ids should not create issue, count before=%d after=%d", before, after)
	}
}

// TestUpdateIssueRejectsMalformedAssigneeID is the equivalent for the update
// path, where the same parseUUID-shaped gap existed on a previously-unassigned
// issue.
func TestUpdateIssueRejectsMalformedAssigneeID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Update malformed assignee target",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	defer func() {
		cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
		cleanupReq = withURLParam(cleanupReq, "id", created.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
	}()

	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+created.ID, map[string]any{
		"assignee_id": "not-a-uuid",
	})
	req = withURLParam(req, "id", created.ID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateIssue: expected 400 for malformed assignee_id, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpdateIssueRejectsNonexistentMemberAssignee verifies the same gap is
// closed on the update path — UpdateIssue previously only validated agents.
func TestUpdateIssueRejectsNonexistentMemberAssignee(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Update assignee target",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	defer func() {
		cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
		cleanupReq = withURLParam(cleanupReq, "id", created.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
	}()

	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+created.ID, map[string]any{
		"assignee_type": "member",
		"assignee_id":   "00000000-0000-0000-0000-000000000000",
	})
	req = withURLParam(req, "id", created.ID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateIssue: expected 400 for nonexistent member, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpdateIssueAllowsExplicitUnassign verifies that sending null for both
// fields still works after the new validator landed — clearing the assignee
// must not be misclassified as a mismatched pair.
func TestUpdateIssueAllowsExplicitUnassign(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Issue to unassign",
		"assignee_type": "member",
		"assignee_id":   testUserID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	defer func() {
		cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
		cleanupReq = withURLParam(cleanupReq, "id", created.ID)
		testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
	}()

	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+created.ID, map[string]any{
		"assignee_type": nil,
		"assignee_id":   nil,
	})
	req = withURLParam(req, "id", created.ID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200 for unassign, got %d: %s", w.Code, w.Body.String())
	}
	var updated IssueResponse
	json.NewDecoder(w.Body).Decode(&updated)
	if updated.AssigneeType != nil || updated.AssigneeID != nil {
		t.Fatalf("UpdateIssue: expected assignee cleared, got type=%v id=%v", updated.AssigneeType, updated.AssigneeID)
	}
}

func TestUpdateIssueAssignAgent_FlagOffStillUsesOrchestrationPath(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Update Orchestration Agent", []byte(`{}`))

	setHandlerTestOrchestrationEnabled(t, false)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":    "Assign agent via update",
		"status":   "todo",
		"priority": "medium",
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

	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+created.ID, map[string]any{
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	req = withURLParam(req, "id", created.ID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one orchestration plan after agent assignment, got %d", len(plans))
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one orchestration task after agent assignment, got %d", len(tasks))
	}
	if _, ok := service.ParseOrchestrationTaskContext(tasks[0].Context); !ok {
		t.Fatalf("orchestration task should carry orchestration context after agent assignment: %s", string(tasks[0].Context))
	}
}

func TestBatchUpdateIssuesAssignAgent_FlagOffStillUsesOrchestrationPath(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Batch Update Orchestration Agent", []byte(`{}`))

	setHandlerTestOrchestrationEnabled(t, false)

	createIssue := func(title string) string {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
			"title":    title,
			"status":   "todo",
			"priority": "medium",
		})
		testHandler.CreateIssue(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
		}
		var created IssueResponse
		if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
			t.Fatalf("decode created issue: %v", err)
		}
		return created.ID
	}

	issueA := createIssue("Batch assign agent A")
	issueB := createIssue("Batch assign agent B")

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/issues/batch", map[string]any{
		"issue_ids": []string{issueA, issueB},
		"updates": map[string]any{
			"assignee_type": "agent",
			"assignee_id":   agentID,
		},
	})
	testHandler.BatchUpdateIssues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("BatchUpdateIssues: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	for _, rawID := range []string{issueA, issueB} {
		issueID := parseUUID(rawID)
		plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
			SourceType: "issue",
			SourceID:   issueID,
		})
		if err != nil {
			t.Fatalf("list orchestration plans for %s: %v", rawID, err)
		}
		if len(plans) != 1 {
			t.Fatalf("expected one orchestration plan for %s after batch assignment, got %d", rawID, len(plans))
		}

		tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
		if err != nil {
			t.Fatalf("list issue tasks for %s: %v", rawID, err)
		}
		if len(tasks) != 1 {
			t.Fatalf("expected one orchestration task for %s after batch assignment, got %d", rawID, len(tasks))
		}
		if _, ok := service.ParseOrchestrationTaskContext(tasks[0].Context); !ok {
			t.Fatalf("orchestration task should carry orchestration context for %s after batch assignment: %s", rawID, string(tasks[0].Context))
		}
	}
}

func TestCommentCRUD(t *testing.T) {
	// Create an issue first
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Comment test issue",
	})
	testHandler.CreateIssue(w, req)
	var issue IssueResponse
	json.NewDecoder(w.Body).Decode(&issue)
	issueID := issue.ID

	// Create comment
	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content": "Test comment from Go test",
	})
	req = withURLParam(req, "id", issueID)
	testHandler.CreateComment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateComment: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// List comments
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/issues/"+issueID+"/comments", nil)
	req = withURLParam(req, "id", issueID)
	testHandler.ListComments(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListComments: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var comments []CommentResponse
	json.NewDecoder(w.Body).Decode(&comments)
	if len(comments) != 1 {
		t.Fatalf("ListComments: expected 1 comment, got %d", len(comments))
	}
	if comments[0].Content != "Test comment from Go test" {
		t.Fatalf("ListComments: expected content 'Test comment from Go test', got '%s'", comments[0].Content)
	}

	// Cleanup
	w = httptest.NewRecorder()
	req = newRequest("DELETE", "/api/issues/"+issueID, nil)
	req = withURLParam(req, "id", issueID)
	testHandler.DeleteIssue(w, req)
}

func TestCreateCommentRejectsMalformedParentID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Comment malformed parent issue",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	json.NewDecoder(w.Body).Decode(&issue)

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues/"+issue.ID+"/comments", map[string]any{
		"content":   "bad parent",
		"parent_id": "not-a-uuid",
	})
	req = withURLParam(req, "id", issue.ID)
	testHandler.CreateComment(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateComment: expected 400 for malformed parent_id, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = newRequest("DELETE", "/api/issues/"+issue.ID, nil)
	req = withURLParam(req, "id", issue.ID)
	testHandler.DeleteIssue(w, req)
}

func TestGetChatSessionRejectsMalformedSessionID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/chat/sessions/not-a-uuid", nil)
	req = withURLParam(req, "sessionId", "not-a-uuid")
	testHandler.GetChatSession(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("GetChatSession: expected 400 for malformed sessionId, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateAutopilotRejectsMalformedAssigneeID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/autopilots", map[string]any{
		"title":          "Malformed assignee autopilot",
		"assignee_id":    "not-a-uuid",
		"execution_mode": "run_only",
	})
	testHandler.CreateAutopilot(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateAutopilot: expected 400 for malformed assignee_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTriggerAutopilotCreateIssue_UsesOrchestrationPath(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Autopilot Orchestration Agent", []byte(`{}`))

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/autopilots", map[string]any{
		"title":                      "Autopilot create issue orchestration",
		"assignee_id":                agentID,
		"execution_mode":             "create_issue",
		"issue_title_template":       "Autopilot generated issue",
		"issue_description_template": "Generated from autopilot.",
		"status":                     "active",
	})
	testHandler.CreateAutopilot(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateAutopilot: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created map[string]any
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created autopilot: %v", err)
	}
	autopilotID, _ := created["id"].(string)
	if autopilotID == "" {
		t.Fatal("expected autopilot id")
	}

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/autopilots/"+autopilotID+"/trigger", nil)
	req = withURLParam(req, "id", autopilotID)
	testHandler.TriggerAutopilot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("TriggerAutopilot: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		SELECT i.id
		FROM issue i
		WHERE i.origin_type = 'autopilot'
		ORDER BY i.created_at DESC
		LIMIT 1
	`).Scan(&issueID); err != nil {
		t.Fatalf("load autopilot-created issue: %v", err)
	}

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   parseUUID(issueID),
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one orchestration plan for autopilot-created issue, got %d", len(plans))
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one task for autopilot-created issue, got %d", len(tasks))
	}
	if _, ok := service.ParseOrchestrationTaskContext(tasks[0].Context); !ok {
		t.Fatalf("autopilot create_issue task should carry orchestration context: %s", string(tasks[0].Context))
	}
}

func TestImportStarterContent_WelcomeIssueUsesOrchestrationPath(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Starter Content Orchestration Agent", []byte(`{}`))

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/onboarding/starter-content/import", map[string]any{
		"workspace_id": testWorkspaceID,
		"project": map[string]any{
			"title": "Starter project",
		},
		"welcome_issue_template": map[string]any{
			"title":       "Welcome issue",
			"description": "Welcome template",
			"priority":    "high",
		},
		"agent_guided_sub_issues": []map[string]any{},
		"self_serve_sub_issues":   []map[string]any{},
		"selected_agent_id":       agentID,
	})
	testHandler.ImportStarterContent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ImportStarterContent: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	ctx := context.Background()
	var issueID string
	if err := testPool.QueryRow(ctx, `
		SELECT id
		FROM issue
		WHERE workspace_id = $1 AND title = 'Welcome issue'
		ORDER BY created_at DESC
		LIMIT 1
	`, testWorkspaceID).Scan(&issueID); err != nil {
		t.Fatalf("load welcome issue: %v", err)
	}

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   parseUUID(issueID),
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one orchestration plan for welcome issue, got %d", len(plans))
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one task for welcome issue, got %d", len(tasks))
	}
	if _, ok := service.ParseOrchestrationTaskContext(tasks[0].Context); !ok {
		t.Fatalf("welcome issue task should carry orchestration context: %s", string(tasks[0].Context))
	}
}

func TestUpdateAutopilotRejectsMalformedID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/autopilots/not-a-uuid", map[string]any{
		"title": "Malformed autopilot id",
	})
	req = withURLParam(req, "id", "not-a-uuid")
	testHandler.UpdateAutopilot(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateAutopilot: expected 400 for malformed id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateAgentRejectsMalformedAgentID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/agents/not-a-uuid", map[string]any{
		"name": "Malformed agent id",
	})
	req = withURLParam(req, "id", "not-a-uuid")
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateAgent: expected 400 for malformed id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateAgentRejectsMalformedRuntimeID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/agents", map[string]any{
		"name":       "Malformed runtime agent",
		"runtime_id": "not-a-uuid",
	})
	testHandler.CreateAgent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateAgent: expected 400 for malformed runtime_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateAgentRejectsMalformedRuntimeID(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Malformed Runtime Update", nil)

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/agents/"+agentID, map[string]any{
		"runtime_id": "not-a-uuid",
	})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateAgent: expected 400 for malformed runtime_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreatePinRejectsMalformedItemID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/pins", map[string]any{
		"item_type": "issue",
		"item_id":   "not-a-uuid",
	})
	testHandler.CreatePin(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreatePin: expected 400 for malformed item_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateWorkspaceRejectsMalformedID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/workspaces/not-a-uuid", map[string]any{
		"name": "Malformed workspace id",
	})
	req = withURLParam(req, "id", "not-a-uuid")
	testHandler.UpdateWorkspace(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateWorkspace: expected 400 for malformed id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateMemberRejectsMalformedMemberID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("PATCH", "/api/workspaces/"+testWorkspaceID+"/members/not-a-uuid", map[string]any{
		"role": "member",
	})
	req = withURLParams(req, "id", testWorkspaceID, "memberId", "not-a-uuid")
	testHandler.UpdateMember(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateMember: expected 400 for malformed memberId, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRevokeInvitationRejectsMalformedInvitationID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/workspaces/"+testWorkspaceID+"/invitations/not-a-uuid", nil)
	req = withURLParams(req, "id", testWorkspaceID, "invitationId", "not-a-uuid")
	testHandler.RevokeInvitation(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("RevokeInvitation: expected 400 for malformed invitationId, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetMyInvitationRejectsMalformedID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/invitations/not-a-uuid", nil)
	req = withURLParam(req, "id", "not-a-uuid")
	testHandler.GetMyInvitation(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("GetMyInvitation: expected 400 for malformed id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAddReactionRejectsMalformedCommentID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/comments/not-a-uuid/reactions", map[string]any{
		"emoji": "thumbs_up",
	})
	req = withURLParam(req, "commentId", "not-a-uuid")
	testHandler.AddReaction(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("AddReaction: expected 400 for malformed commentId, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateCommentRejectsMalformedCommentID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/comments/not-a-uuid", map[string]any{
		"content": "updated",
	})
	req = withURLParam(req, "commentId", "not-a-uuid")
	testHandler.UpdateComment(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateComment: expected 400 for malformed commentId, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMarkInboxReadRejectsMalformedItemID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/inbox/not-a-uuid/read", nil)
	req = withURLParam(req, "id", "not-a-uuid")
	testHandler.MarkInboxRead(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("MarkInboxRead: expected 400 for malformed id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRevokePersonalAccessTokenRejectsMalformedID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/tokens/not-a-uuid", nil)
	req = withURLParam(req, "id", "not-a-uuid")
	testHandler.RevokePersonalAccessToken(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("RevokePersonalAccessToken: expected 400 for malformed id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRequestBodyUUIDFieldsRejectMalformed(t *testing.T) {
	tests := []struct {
		name   string
		req    *http.Request
		handle func(http.ResponseWriter, *http.Request)
	}{
		{
			name: "daemon register workspace_id",
			req: newRequest("POST", "/api/daemon/register", map[string]any{
				"workspace_id": "not-a-uuid",
				"daemon_id":    "daemon-malformed-workspace",
				"runtimes": []map[string]any{
					{"name": "codex", "type": "codex", "status": "online"},
				},
			}),
			handle: testHandler.DaemonRegister,
		},
		{
			name: "import starter content workspace_id",
			req: newRequest("POST", "/api/onboarding/starter-content/import", map[string]any{
				"workspace_id": "not-a-uuid",
				"project": map[string]any{
					"title": "Getting Started",
				},
			}),
			handle: testHandler.ImportStarterContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tt.handle(w, tt.req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: expected 400 for malformed body UUID, got %d: %s", tt.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestDaemonDeregisterRejectsMalformedRuntimeID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/daemon/deregister", map[string]any{
		"runtime_ids": []string{"not-a-uuid"},
	})
	testHandler.DaemonDeregister(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("DaemonDeregister: expected 400 for malformed runtime_ids, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetIssueGCCheckRejectsMalformedIssueID(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/daemon/issues/not-a-uuid/gc-check", nil)
	req = withURLParam(req, "issueId", "not-a-uuid")
	testHandler.GetIssueGCCheck(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("GetIssueGCCheck: expected 400 for malformed issueId, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSetAgentSkillsRejectsMalformedSkillID(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Malformed Skill Assignment", nil)

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/agents/"+agentID+"/skills", map[string]any{
		"skill_ids": []string{"not-a-uuid"},
	})
	req = withURLParam(req, "id", agentID)
	testHandler.SetAgentSkills(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("SetAgentSkills: expected 400 for malformed skill_ids, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAgentCRUD(t *testing.T) {
	// List agents
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/agents?workspace_id="+testWorkspaceID, nil)
	testHandler.ListAgents(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListAgents: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var agents []AgentResponse
	json.NewDecoder(w.Body).Decode(&agents)
	if len(agents) == 0 {
		t.Fatal("ListAgents: expected at least 1 agent")
	}

	// Update agent status
	agentID := agents[0].ID
	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/agents/"+agentID, map[string]any{
		"status": "idle",
	})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var updated AgentResponse
	json.NewDecoder(w.Body).Decode(&updated)
	if updated.Status != "idle" {
		t.Fatalf("UpdateAgent: expected status 'idle', got '%s'", updated.Status)
	}
	if updated.Name != agents[0].Name {
		t.Fatalf("UpdateAgent: name should be preserved, got '%s'", updated.Name)
	}
}

func TestUpdateAgentMcpConfigAbsentPreservesValue(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Mcp Preserve", []byte(`{"preset":"keep"}`))

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/agents/"+agentID, map[string]any{
		"name": "Handler Mcp Preserve Updated",
	})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var updated AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("UpdateAgent: decode response: %v", err)
	}
	assertJSONEqual(t, updated.McpConfig, `{"preset":"keep"}`)
	assertJSONEqual(t, fetchAgentMcpConfig(t, agentID), `{"preset":"keep"}`)
}

func TestUpdateAgentMcpConfigNullClearsValue(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Mcp Clear", []byte(`{"preset":"clear"}`))

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/agents/"+agentID, map[string]any{
		"mcp_config": nil,
	})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var updated AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("UpdateAgent: decode response: %v", err)
	}
	assertJSONEqual(t, updated.McpConfig, `null`)
	if fetchAgentMcpConfig(t, agentID) != nil {
		t.Fatalf("UpdateAgent: expected DB mcp_config to be SQL NULL")
	}
}

func TestUpdateAgentMcpConfigObjectUpdatesValue(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Mcp Update", []byte(`{"preset":"old"}`))

	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/agents/"+agentID, map[string]any{
		"mcp_config": map[string]any{"preset": "new"},
	})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var updated AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("UpdateAgent: decode response: %v", err)
	}
	assertJSONEqual(t, updated.McpConfig, `{"preset":"new"}`)
	assertJSONEqual(t, fetchAgentMcpConfig(t, agentID), `{"preset":"new"}`)
}

func TestCreateAgentMcpConfigNullStoresSQLNull(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/agents", map[string]any{
		"name":        "Handler Mcp Create Null",
		"runtime_id":  handlerTestRuntimeID(t),
		"mcp_config":  nil,
		"custom_env":  map[string]string{},
		"custom_args": []string{},
	})
	testHandler.CreateAgent(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateAgent: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("CreateAgent: decode response: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, created.ID)
	})

	assertJSONEqual(t, created.McpConfig, `null`)
	if fetchAgentMcpConfig(t, created.ID) != nil {
		t.Fatalf("CreateAgent: expected DB mcp_config to be SQL NULL")
	}
}

func TestWorkspaceCRUD(t *testing.T) {
	// List workspaces
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/workspaces", nil)
	testHandler.ListWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListWorkspaces: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var workspaces []WorkspaceResponse
	json.NewDecoder(w.Body).Decode(&workspaces)
	if len(workspaces) == 0 {
		t.Fatal("ListWorkspaces: expected at least 1 workspace")
	}

	// Get workspace
	wsID := workspaces[0].ID
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/workspaces/"+wsID, nil)
	req = withURLParam(req, "id", wsID)
	testHandler.GetWorkspace(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetWorkspace: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateWorkspaceUsesRequestedSlug(t *testing.T) {
	const slug = "handler-create-workspace-requested"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, slug)
	})

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/workspaces", map[string]string{
		"name": "Handler Create Workspace Requested",
		"slug": slug,
	})
	testHandler.CreateWorkspace(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateWorkspace: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created WorkspaceResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("CreateWorkspace: decode response: %v", err)
	}
	if created.Slug != slug {
		t.Fatalf("CreateWorkspace: expected slug %q, got %q", slug, created.Slug)
	}
}

func TestCreateWorkspaceSlugConflictReturnsConflict(t *testing.T) {
	ctx := context.Background()
	retriedSlug := handlerTestWorkspaceSlug + "-2"

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, retriedSlug)
	})

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/workspaces", map[string]string{
		"name": "Duplicate Handler Workspace",
		"slug": handlerTestWorkspaceSlug,
	})
	testHandler.CreateWorkspace(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("CreateWorkspace: expected 409, got %d: %s", w.Code, w.Body.String())
	}

	var count int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM workspace WHERE slug = $1`, retriedSlug).Scan(&count); err != nil {
		t.Fatalf("CreateWorkspace: check retried slug: %v", err)
	}
	if count != 0 {
		t.Fatalf("CreateWorkspace: expected no fallback slug %q, got %d rows", retriedSlug, count)
	}
}

func TestCreateWorkspaceInvalidSlugReturnsBadRequest(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/workspaces", map[string]string{
		"name": "Invalid Slug Workspace",
		"slug": "invalid slug",
	})
	testHandler.CreateWorkspace(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateWorkspace: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSendCode(t *testing.T) {
	w := httptest.NewRecorder()
	body := map[string]string{"email": "sendcode-test@multica.ai"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest("POST", "/auth/send-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	testHandler.SendCode(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("SendCode: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["message"] == "" {
		t.Fatal("SendCode: expected non-empty message")
	}

	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM verification_code WHERE email = $1`, "sendcode-test@multica.ai")
	})
}

func TestSendCodeDbError(t *testing.T) {
	// We can't easily mock the DB here without changing architecture,
	// but we can simulate a DB error by closing the pool temporarily or
	// using a cancelled context if the query respects it.

	// Create a handler with a "broken" queries object is hard because it's a struct.
	// Instead, let's use a context that is already cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	w := httptest.NewRecorder()
	body := map[string]string{"email": "dberror-test@multica.ai"}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest("POST", "/auth/send-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctx)

	testHandler.SendCode(w, req)

	// If the DB query respects the cancelled context, it should return an error.
	// pgx usually returns context.Canceled which is not what isNotFound checks for.
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("SendCode (db error): expected 500, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "failed to lookup user" {
		t.Fatalf("SendCode (db error): expected error message 'failed to lookup user', got '%s'", resp["error"])
	}
}

func TestSendCodeRateLimit(t *testing.T) {
	const email = "ratelimit-test@multica.ai"
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM verification_code WHERE email = $1`, email)
	})

	// First request should succeed
	w := httptest.NewRecorder()
	body := map[string]string{"email": email}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req := httptest.NewRequest("POST", "/auth/send-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	testHandler.SendCode(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("SendCode (first): expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Second request within 60s should be rate limited
	w = httptest.NewRecorder()
	buf.Reset()
	json.NewEncoder(&buf).Encode(body)
	req = httptest.NewRequest("POST", "/auth/send-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	testHandler.SendCode(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("SendCode (second): expected 429, got %d: %s", w.Code, w.Body.String())
	}
}

func TestVerifyCode(t *testing.T) {
	const email = "verify-test@multica.ai"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM verification_code WHERE email = $1`, email)
		user, err := testHandler.Queries.GetUserByEmail(ctx, email)
		if err == nil {
			workspaces, listErr := testHandler.Queries.ListWorkspaces(ctx, user.ID)
			if listErr == nil {
				for _, workspace := range workspaces {
					_ = testHandler.Queries.DeleteWorkspace(ctx, workspace.ID)
				}
			}
		}
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)
	})

	// Send code first
	w := httptest.NewRecorder()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(map[string]string{"email": email})
	req := httptest.NewRequest("POST", "/auth/send-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	testHandler.SendCode(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("SendCode: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Read code from DB
	dbCode, err := testHandler.Queries.GetLatestVerificationCode(ctx, email)
	if err != nil {
		t.Fatalf("GetLatestVerificationCode: %v", err)
	}

	// Verify with correct code
	w = httptest.NewRecorder()
	buf.Reset()
	json.NewEncoder(&buf).Encode(map[string]string{"email": email, "code": dbCode.Code})
	req = httptest.NewRequest("POST", "/auth/verify-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	testHandler.VerifyCode(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("VerifyCode: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp LoginResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Token == "" {
		t.Fatal("VerifyCode: expected non-empty token")
	}
	if resp.User.Email != email {
		t.Fatalf("VerifyCode: expected email '%s', got '%s'", email, resp.User.Email)
	}
}

func createVerificationCodeForTest(t *testing.T, email, code string) {
	t.Helper()

	_, err := testPool.Exec(context.Background(), `
		INSERT INTO verification_code (email, code, expires_at)
		VALUES ($1, $2, now() + interval '10 minutes')
	`, email, code)
	if err != nil {
		t.Fatalf("create verification code: %v", err)
	}
}

func TestVerifyCodeRejectsDevCodeUnlessExplicitlyConfigured(t *testing.T) {
	t.Setenv(devVerificationCodeEnv, "")
	t.Setenv("APP_ENV", "")

	const email = "dev-code-disabled-test@multica.ai"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM verification_code WHERE email = $1`, email)
	})

	createVerificationCodeForTest(t, email, "123456")

	w := httptest.NewRecorder()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(map[string]string{"email": email, "code": "888888"})
	req := httptest.NewRequest("POST", "/auth/verify-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	testHandler.VerifyCode(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("VerifyCode (disabled dev code): expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestVerifyCodeAcceptsConfiguredDevCodeOutsideProduction(t *testing.T) {
	t.Setenv(devVerificationCodeEnv, "888888")
	t.Setenv("APP_ENV", "development")

	const email = "dev-code-enabled-test@multica.ai"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM verification_code WHERE email = $1`, email)
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)
	})

	createVerificationCodeForTest(t, email, "123456")

	w := httptest.NewRecorder()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(map[string]string{"email": email, "code": "888888"})
	req := httptest.NewRequest("POST", "/auth/verify-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	testHandler.VerifyCode(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("VerifyCode (enabled dev code): expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestVerifyCodeRejectsConfiguredDevCodeInProduction(t *testing.T) {
	t.Setenv(devVerificationCodeEnv, "888888")
	t.Setenv("APP_ENV", "production")

	const email = "dev-code-production-test@multica.ai"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM verification_code WHERE email = $1`, email)
	})

	createVerificationCodeForTest(t, email, "123456")

	w := httptest.NewRecorder()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(map[string]string{"email": email, "code": "888888"})
	req := httptest.NewRequest("POST", "/auth/verify-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	testHandler.VerifyCode(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("VerifyCode (production dev code): expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestVerifyCodeWrongCode(t *testing.T) {
	t.Setenv(devVerificationCodeEnv, "")

	const email = "wrong-code-test@multica.ai"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM verification_code WHERE email = $1`, email)
	})

	// Send code
	w := httptest.NewRecorder()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(map[string]string{"email": email})
	req := httptest.NewRequest("POST", "/auth/send-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	testHandler.SendCode(w, req)

	// Verify with wrong code
	w = httptest.NewRecorder()
	buf.Reset()
	json.NewEncoder(&buf).Encode(map[string]string{"email": email, "code": "000000"})
	req = httptest.NewRequest("POST", "/auth/verify-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	testHandler.VerifyCode(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("VerifyCode (wrong code): expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestVerifyCodeBruteForceProtection(t *testing.T) {
	t.Setenv(devVerificationCodeEnv, "")

	const email = "bruteforce-test@multica.ai"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM verification_code WHERE email = $1`, email)
	})

	// Send code
	w := httptest.NewRecorder()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(map[string]string{"email": email})
	req := httptest.NewRequest("POST", "/auth/send-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	testHandler.SendCode(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("SendCode: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Read actual code so we can try it after lockout
	dbCode, err := testHandler.Queries.GetLatestVerificationCode(ctx, email)
	if err != nil {
		t.Fatalf("GetLatestVerificationCode: %v", err)
	}

	// Exhaust all 5 attempts with wrong codes
	for i := 0; i < 5; i++ {
		w = httptest.NewRecorder()
		buf.Reset()
		json.NewEncoder(&buf).Encode(map[string]string{"email": email, "code": "000000"})
		req = httptest.NewRequest("POST", "/auth/verify-code", &buf)
		req.Header.Set("Content-Type", "application/json")
		testHandler.VerifyCode(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: expected 400, got %d", i+1, w.Code)
		}
	}

	// Now even the correct code should be rejected (code is locked out)
	w = httptest.NewRecorder()
	buf.Reset()
	json.NewEncoder(&buf).Encode(map[string]string{"email": email, "code": dbCode.Code})
	req = httptest.NewRequest("POST", "/auth/verify-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	testHandler.VerifyCode(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("after lockout: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestVerifyCodeNewUserHasNoWorkspace(t *testing.T) {
	const email = "workspace-verify-test@multica.ai"
	ctx := context.Background()

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM verification_code WHERE email = $1`, email)
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)
	})

	// Send code
	w := httptest.NewRecorder()
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(map[string]string{"email": email})
	req := httptest.NewRequest("POST", "/auth/send-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	testHandler.SendCode(w, req)

	// Read code from DB
	dbCode, err := testHandler.Queries.GetLatestVerificationCode(ctx, email)
	if err != nil {
		t.Fatalf("GetLatestVerificationCode: %v", err)
	}

	// Verify
	w = httptest.NewRecorder()
	buf.Reset()
	json.NewEncoder(&buf).Encode(map[string]string{"email": email, "code": dbCode.Code})
	req = httptest.NewRequest("POST", "/auth/verify-code", &buf)
	req.Header.Set("Content-Type", "application/json")
	testHandler.VerifyCode(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("VerifyCode: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	user, err := testHandler.Queries.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}

	// New users should have no workspaces (/workspaces/new creates one)
	workspaces, err := testHandler.Queries.ListWorkspaces(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(workspaces) != 0 {
		t.Fatalf("ListWorkspaces: expected 0 workspaces for new user, got %d", len(workspaces))
	}
}

func TestResolveActor(t *testing.T) {
	ctx := context.Background()

	// Look up the agent created by the test fixture.
	var agentID string
	err := testPool.QueryRow(ctx,
		`SELECT id FROM agent WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, "Handler Test Agent",
	).Scan(&agentID)
	if err != nil {
		t.Fatalf("failed to find test agent: %v", err)
	}

	// Create a task for the agent so we can test X-Task-ID validation.
	var issueID string
	err = testPool.QueryRow(ctx,
		`INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, position)
		 VALUES ($1, 'resolveActor test', 'todo', 'none', 'member', $2, 9999, 0)
		 RETURNING id`, testWorkspaceID, testUserID,
	).Scan(&issueID)
	if err != nil {
		t.Fatalf("failed to create test issue: %v", err)
	}

	// Look up runtime_id for the agent.
	var runtimeID string
	err = testPool.QueryRow(ctx, `SELECT runtime_id FROM agent WHERE id = $1`, agentID).Scan(&runtimeID)
	if err != nil {
		t.Fatalf("failed to get agent runtime_id: %v", err)
	}

	var taskID string
	err = testPool.QueryRow(ctx,
		`INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority)
		 VALUES ($1, $2, $3, 'queued', 0)
		 RETURNING id`, agentID, runtimeID, issueID,
	).Scan(&taskID)
	if err != nil {
		t.Fatalf("failed to create test task: %v", err)
	}

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	})

	tests := []struct {
		name          string
		agentIDHeader string
		taskIDHeader  string
		wantActorType string
		wantIsAgent   bool
	}{
		{
			name:          "no headers returns member",
			wantActorType: "member",
		},
		{
			name:          "valid agent ID returns agent",
			agentIDHeader: agentID,
			wantActorType: "agent",
			wantIsAgent:   true,
		},
		{
			name:          "non-existent agent ID returns member",
			agentIDHeader: "00000000-0000-0000-0000-000000000099",
			wantActorType: "member",
		},
		{
			name:          "valid agent + valid task returns agent",
			agentIDHeader: agentID,
			taskIDHeader:  taskID,
			wantActorType: "agent",
			wantIsAgent:   true,
		},
		{
			name:          "valid agent + wrong task returns member",
			agentIDHeader: agentID,
			taskIDHeader:  "00000000-0000-0000-0000-000000000099",
			wantActorType: "member",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newRequest("GET", "/test", nil)
			if tt.agentIDHeader != "" {
				req.Header.Set("X-Agent-ID", tt.agentIDHeader)
			}
			if tt.taskIDHeader != "" {
				req.Header.Set("X-Task-ID", tt.taskIDHeader)
			}

			actorType, actorID := testHandler.resolveActor(req, testUserID, testWorkspaceID)

			if actorType != tt.wantActorType {
				t.Errorf("actorType = %q, want %q", actorType, tt.wantActorType)
			}
			if tt.wantIsAgent {
				if actorID != tt.agentIDHeader {
					t.Errorf("actorID = %q, want agent %q", actorID, tt.agentIDHeader)
				}
			} else {
				if actorID != testUserID {
					t.Errorf("actorID = %q, want user %q", actorID, testUserID)
				}
			}
		})
	}
}

// TestBacklogNoTriggerOnCreate verifies that creating a backlog issue with an
// agent assignee does NOT enqueue a task — backlog is a parking lot.
func TestBacklogNoTriggerOnCreate(t *testing.T) {
	ctx := context.Background()

	var agentID string
	err := testPool.QueryRow(ctx,
		`SELECT id FROM agent WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, "Handler Test Agent",
	).Scan(&agentID)
	if err != nil {
		t.Fatalf("failed to find test agent: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Backlog no-trigger test",
		"status":        "backlog",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)

	var taskCount int
	err = testPool.QueryRow(ctx,
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1`,
		created.ID,
	).Scan(&taskCount)
	if err != nil {
		t.Fatalf("failed to count tasks: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("expected no tasks for backlog issue on creation, got %d", taskCount)
	}

	// Cleanup
	cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
	cleanupReq = withURLParam(cleanupReq, "id", created.ID)
	testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
}

// TestBacklogToTodoTriggersAgent verifies that moving an agent-assigned issue
// from "backlog" to "todo" enters the orchestration path exactly once
// (none on creation, one orchestration task on status transition).
func TestBacklogToTodoTriggersAgent(t *testing.T) {
	ctx := context.Background()

	var agentID string
	err := testPool.QueryRow(ctx,
		`SELECT id FROM agent WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, "Handler Test Agent",
	).Scan(&agentID)
	if err != nil {
		t.Fatalf("failed to find test agent: %v", err)
	}

	// Create a backlog issue assigned to the agent — should NOT trigger.
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Backlog trigger test",
		"status":        "backlog",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)

	// Move the issue from backlog to todo — should trigger.
	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/issues/"+created.ID, map[string]any{
		"status": "todo",
	})
	req = withURLParam(req, "id", created.ID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	issueID := parseUUID(created.ID)
	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected exactly 1 orchestration plan after backlog->todo transition, got %d", len(plans))
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected exactly 1 task after backlog->todo transition, got %d", len(tasks))
	}
	if _, ok := service.ParseOrchestrationTaskContext(tasks[0].Context); !ok {
		t.Fatalf("backlog->todo task should carry orchestration context: %s", string(tasks[0].Context))
	}

	// Cleanup
	testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, created.ID)
	cleanupReq := newRequest("DELETE", "/api/issues/"+created.ID, nil)
	cleanupReq = withURLParam(cleanupReq, "id", created.ID)
	testHandler.DeleteIssue(httptest.NewRecorder(), cleanupReq)
}

func TestCommentTriggerAssignedAgent_FlagOffStillUsesOrchestrationPath(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "On Comment Orchestration Agent", []byte(`{"triggers":["on_comment"]}`))

	setHandlerTestOrchestrationEnabled(t, false)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Comment-triggered orchestration",
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
	if _, err := testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID); err != nil {
		t.Fatalf("clear initial task queue: %v", err)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM orchestration_artifact WHERE plan_id IN (SELECT id FROM orchestration_plan WHERE source_type = 'issue' AND source_id = $1)`, issueID); err != nil {
		t.Fatalf("clear orchestration artifacts: %v", err)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM orchestration_event WHERE plan_id IN (SELECT id FROM orchestration_plan WHERE source_type = 'issue' AND source_id = $1)`, issueID); err != nil {
		t.Fatalf("clear orchestration events: %v", err)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM orchestration_edge WHERE plan_id IN (SELECT id FROM orchestration_plan WHERE source_type = 'issue' AND source_id = $1)`, issueID); err != nil {
		t.Fatalf("clear orchestration edges: %v", err)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM orchestration_node WHERE plan_id IN (SELECT id FROM orchestration_plan WHERE source_type = 'issue' AND source_id = $1)`, issueID); err != nil {
		t.Fatalf("clear orchestration nodes: %v", err)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM orchestration_plan WHERE source_type = 'issue' AND source_id = $1`, issueID); err != nil {
		t.Fatalf("clear orchestration plans: %v", err)
	}

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues/"+created.ID+"/comments", map[string]any{
		"content": "Please re-check this issue.",
	})
	req = withURLParam(req, "id", created.ID)
	testHandler.CreateComment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateComment: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one orchestration plan after comment trigger, got %d", len(plans))
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one orchestration task after comment trigger, got %d", len(tasks))
	}
	if _, ok := service.ParseOrchestrationTaskContext(tasks[0].Context); !ok {
		t.Fatalf("comment-triggered task should carry orchestration context: %s", string(tasks[0].Context))
	}
}

func TestRerunIssue_UsesOrchestrationPathAndFreshSession(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Rerun Orchestration Agent", []byte(`{}`))

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Rerun orchestration issue",
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

	initialPlans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list initial orchestration plans: %v", err)
	}
	if len(initialPlans) != 1 {
		t.Fatalf("expected one initial orchestration plan, got %d", len(initialPlans))
	}

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues/"+created.ID+"/rerun", nil)
	req = withURLParam(req, "id", created.ID)
	testHandler.RerunIssue(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("RerunIssue: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans after rerun: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected rerun to create a fresh orchestration plan, got %d plans", len(plans))
	}
	if plans[0].Status != "running" {
		t.Fatalf("expected newest rerun plan to be running, got %q", plans[0].Status)
	}
	if plans[1].Status != "cancelled" {
		t.Fatalf("expected prior plan to be cancelled on rerun, got %q", plans[1].Status)
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks after rerun: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected initial task plus rerun task, got %d", len(tasks))
	}
	if tasks[0].Status != "queued" {
		t.Fatalf("expected newest rerun task to be queued, got %q", tasks[0].Status)
	}
	if _, ok := service.ParseOrchestrationTaskContext(tasks[0].Context); !ok {
		t.Fatalf("rerun task should carry orchestration context: %s", string(tasks[0].Context))
	}
	if !tasks[0].ForceFreshSession {
		t.Fatal("rerun orchestration task should force a fresh session")
	}
	if tasks[1].Status != "cancelled" {
		t.Fatalf("expected prior task to be cancelled on rerun, got %q", tasks[1].Status)
	}
}

func TestRerunIssue_DoesNotFallbackToLegacyWhenOrchestratorUnavailable(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Rerun Unavailable Orchestrator Agent", []byte(`{}`))

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Rerun without orchestrator",
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

	original := testHandler.TaskService.Orchestrator
	testHandler.TaskService.Orchestrator = nil
	t.Cleanup(func() {
		testHandler.TaskService.Orchestrator = original
	})

	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/issues/"+created.ID+"/rerun", nil)
	req = withURLParam(req, "id", created.ID)
	testHandler.RerunIssue(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("RerunIssue: expected 400 when orchestrator unavailable, got %d: %s", w.Code, w.Body.String())
	}

	plans, err := testHandler.Queries.ListOrchestrationPlansBySource(ctx, db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		t.Fatalf("list orchestration plans after unavailable rerun: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected original orchestration plan to remain untouched, got %d plans", len(plans))
	}

	tasks, err := testHandler.Queries.ListTasksByIssue(ctx, issueID)
	if err != nil {
		t.Fatalf("list issue tasks after unavailable rerun: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected no new legacy rerun task when orchestrator unavailable, got %d tasks", len(tasks))
	}
	if _, ok := service.ParseOrchestrationTaskContext(tasks[0].Context); !ok {
		t.Fatalf("existing task should remain orchestration task: %s", string(tasks[0].Context))
	}
}

func TestDaemonRegisterMissingWorkspaceReturns404(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/daemon/register", bytes.NewBufferString(`{
		"workspace_id":"00000000-0000-0000-0000-000000000001",
		"daemon_id":"local-daemon",
		"device_name":"test-machine",
		"runtimes":[{"name":"Local Codex","type":"codex","version":"1.0.0","status":"online"}]
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)

	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("DaemonRegister: expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "workspace not found") {
		t.Fatalf("DaemonRegister: expected workspace not found error, got %s", w.Body.String())
	}
}

// TestAgentReplyDoesNotInheritParentMentions verifies that agent-authored
// replies do NOT inherit parent-comment mentions, preventing agent-to-agent
// re-trigger loops (e.g. "No reply needed" chains). Member-authored replies
// still inherit parent mentions as expected.
func TestAgentReplyDoesNotInheritParentMentions(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Create two agents.
	agentA := createHandlerTestAgent(t, "Loop Agent A", nil)
	agentB := createHandlerTestAgent(t, "Loop Agent B", nil)

	// Create an unassigned issue so on_comment doesn't fire.
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "Agent mention inheritance test",
		"status": "todo",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	json.NewDecoder(w.Body).Decode(&issue)
	issueID := issue.ID

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
		testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id = $1`, issueID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	})

	// Helper: count queued tasks for a given agent on this issue.
	countTasks := func(agentID string) int {
		var n int
		err := testPool.QueryRow(ctx,
			`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'`,
			issueID, agentID,
		).Scan(&n)
		if err != nil {
			t.Fatalf("failed to count tasks: %v", err)
		}
		return n
	}

	// Helper: cancel all tasks for an agent on this issue.
	cancelTasks := func(agentID string) {
		_, err := testPool.Exec(ctx,
			`UPDATE agent_task_queue SET status = 'cancelled' WHERE issue_id = $1 AND agent_id = $2`,
			issueID, agentID,
		)
		if err != nil {
			t.Fatalf("failed to cancel tasks: %v", err)
		}
	}

	postComment := func(issueID string, body map[string]any, headers map[string]string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := newRequest("POST", "/api/issues/"+issueID+"/comments", body)
		r = withURLParam(r, "id", issueID)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		testHandler.CreateComment(w, r)
		return w
	}

	// 1. Member posts top-level comment mentioning Agent B.
	mentionB := fmt.Sprintf("[@Agent B](mention://agent/%s) please review", agentB)
	w = postComment(issueID, map[string]any{"content": mentionB}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("member mention comment: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var parentComment CommentResponse
	json.NewDecoder(w.Body).Decode(&parentComment)
	if countTasks(agentB) != 1 {
		t.Fatalf("expected 1 task for Agent B after member mention, got %d", countTasks(agentB))
	}

	// 2. Cancel Agent B's task so it's free to be re-triggered.
	cancelTasks(agentB)
	if countTasks(agentB) != 0 {
		t.Fatalf("expected 0 tasks for Agent B after cancel, got %d", countTasks(agentB))
	}

	// 3. Agent A posts a reply in the same thread with NO mentions.
	// With the fix, this must NOT inherit the parent mention of Agent B.
	w = postComment(issueID, map[string]any{
		"content":   "No reply needed — just an acknowledgment.",
		"parent_id": parentComment.ID,
	}, map[string]string{"X-Agent-ID": agentA})
	if w.Code != http.StatusCreated {
		t.Fatalf("agent A reply: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if countTasks(agentB) != 0 {
		t.Fatalf("expected 0 tasks for Agent B after agent reply (no parent inheritance), got %d", countTasks(agentB))
	}

	// 4. Cancel any stray tasks.
	cancelTasks(agentB)

	// 5. Member posts a reply in the same thread with NO mentions.
	// This SHOULD inherit the parent mention and re-trigger Agent B.
	w = postComment(issueID, map[string]any{
		"content":   "Thanks for the review.",
		"parent_id": parentComment.ID,
	}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("member reply: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if countTasks(agentB) != 1 {
		t.Fatalf("expected 1 task for Agent B after member reply (parent inheritance allowed), got %d", countTasks(agentB))
	}
}

// TestMemberReplyToAgentRootDoesNotInheritParentMentions is the regression
// for MUL-1535. When an agent posts a comment that @mentions another agent
// (e.g. J posting a PR completion that @mentions a reviewer agent), a later
// member reply in the same thread with no explicit mentions must NOT inherit
// the @reviewer mention. The reviewer was a one-shot delegation; subsequent
// member follow-ups are directed at the assignee, not the reviewer.
func TestMemberReplyToAgentRootDoesNotInheritParentMentions(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	jAgent := createHandlerTestAgent(t, "J", nil)
	reviewerAgent := createHandlerTestAgent(t, "Reviewer", nil)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "PR review delegation no-leak test",
		"status": "todo",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	json.NewDecoder(w.Body).Decode(&issue)
	issueID := issue.ID

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
		testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id = $1`, issueID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	})

	countTasks := func(agentID string) int {
		var n int
		err := testPool.QueryRow(ctx,
			`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'`,
			issueID, agentID,
		).Scan(&n)
		if err != nil {
			t.Fatalf("failed to count tasks: %v", err)
		}
		return n
	}

	// 1. Agent J posts a PR-completion comment that @mentions Reviewer for review.
	// This is a deliberate handoff and must enqueue a task for Reviewer.
	w = httptest.NewRecorder()
	r := newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content": fmt.Sprintf("PR ready. [@Reviewer](mention://agent/%s) please review this.", reviewerAgent),
	})
	r = withURLParam(r, "id", issueID)
	r.Header.Set("X-Agent-ID", jAgent)
	testHandler.CreateComment(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("J PR completion: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var rootComment CommentResponse
	json.NewDecoder(w.Body).Decode(&rootComment)
	if got := countTasks(reviewerAgent); got != 1 {
		t.Fatalf("expected 1 task for Reviewer after explicit mention, got %d", got)
	}

	// Cancel reviewer's task so it's free to be re-triggered if the bug returns.
	if _, err := testPool.Exec(ctx,
		`UPDATE agent_task_queue SET status = 'cancelled' WHERE issue_id = $1 AND agent_id = $2`,
		issueID, reviewerAgent,
	); err != nil {
		t.Fatalf("cancel reviewer task: %v", err)
	}

	// 2. Member posts a plain follow-up reply under J's PR comment, with no
	// explicit mentions. The pre-fix code path inherited mentions from the
	// parent regardless of the parent author, which re-triggered Reviewer.
	// With the fix, the reply must NOT inherit because the parent was
	// authored by an agent.
	w = httptest.NewRecorder()
	r = newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content":   "How do I test this after merging?",
		"parent_id": rootComment.ID,
	})
	r = withURLParam(r, "id", issueID)
	testHandler.CreateComment(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("member follow-up: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if got := countTasks(reviewerAgent); got != 0 {
		t.Fatalf("expected 0 tasks for Reviewer after plain member reply (no inheritance from agent root), got %d", got)
	}
}

// TestAgentExplicitMentionStillTriggers documents the boundary the structural
// fix preserves: suppressing implicit parent-mention inheritance for agent
// authors does NOT block deliberate handoffs. An agent that explicitly
// @mentions another agent in its own comment content still enqueues a task
// for that mentioned agent.
func TestAgentExplicitMentionStillTriggers(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentA := createHandlerTestAgent(t, "Handoff Agent A", nil)
	agentB := createHandlerTestAgent(t, "Handoff Agent B", nil)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "Agent explicit handoff test",
		"status": "todo",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	json.NewDecoder(w.Body).Decode(&issue)
	issueID := issue.ID

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
		testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id = $1`, issueID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	})

	countTasks := func(agentID string) int {
		var n int
		err := testPool.QueryRow(ctx,
			`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND agent_id = $2 AND status = 'queued'`,
			issueID, agentID,
		).Scan(&n)
		if err != nil {
			t.Fatalf("failed to count tasks: %v", err)
		}
		return n
	}

	// Agent A posts a top-level comment that explicitly @mentions Agent B —
	// a deliberate handoff. This must enqueue a task for Agent B, and must
	// not enqueue a self-trigger for Agent A.
	explicitMention := fmt.Sprintf("[@Agent B](mention://agent/%s) please take it from here", agentB)
	w = httptest.NewRecorder()
	r := newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content": explicitMention,
	})
	r = withURLParam(r, "id", issueID)
	r.Header.Set("X-Agent-ID", agentA)
	testHandler.CreateComment(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("agent A handoff: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if got := countTasks(agentB); got != 1 {
		t.Fatalf("expected 1 task for Agent B after explicit mention by Agent A, got %d", got)
	}
	if got := countTasks(agentA); got != 0 {
		t.Fatalf("expected 0 tasks for Agent A (no self-trigger on own mention), got %d", got)
	}
}
