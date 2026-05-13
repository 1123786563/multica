package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type OrchestrationPlanResponse struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	SourceType    string          `json:"source_type"`
	SourceID      string          `json:"source_id"`
	Objective     string          `json:"objective"`
	Status        string          `json:"status"`
	Policy        json.RawMessage `json:"policy"`
	Metadata      json.RawMessage `json:"metadata"`
	CreatedByType *string         `json:"created_by_type"`
	CreatedByID   *string         `json:"created_by_id"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
}

type OrchestrationNodeResponse struct {
	ID                 string                   `json:"id"`
	PlanID             string                   `json:"plan_id"`
	Key                string                   `json:"key"`
	Type               string                   `json:"type"`
	Title              string                   `json:"title"`
	Description        *string                  `json:"description"`
	Status             string                   `json:"status"`
	Position           int                      `json:"position"`
	Dependencies       []string                 `json:"dependencies"`
	AssigneeAgentID    *string                  `json:"assignee_agent_id"`
	InputContract      json.RawMessage          `json:"input_contract"`
	OutputContract     json.RawMessage          `json:"output_contract"`
	EvaluatorPolicy    json.RawMessage          `json:"evaluator_policy"`
	RetryPolicy        json.RawMessage          `json:"retry_policy"`
	RuntimeConstraints json.RawMessage          `json:"runtime_constraints"`
	AttemptCount       int32                    `json:"attempt_count"`
	MaxAttempts        int32                    `json:"max_attempts"`
	LinkedTaskID       *string                  `json:"linked_task_id"`
	ArtifactCount      int                      `json:"artifact_count"`
	StartedAt          *string                  `json:"started_at"`
	CompletedAt        *string                  `json:"completed_at"`
	CreatedAt          string                   `json:"created_at"`
	UpdatedAt          string                   `json:"updated_at"`
	Summary            *NodeSummaryDTO          `json:"summary,omitempty"`
	Permissions        *NodePermissionsDTO      `json:"permissions,omitempty"`
	ApprovalHistory    []ApprovalHistoryItemDTO `json:"approval_history,omitempty"`
}

type NodeSummaryDTO struct {
	Status                 string `json:"status"`
	ReasonCode             string `json:"reason_code"`
	ReasonTitle            string `json:"reason_title"`
	ReasonDetail           string `json:"reason_detail"`
	RecommendedAction      string `json:"recommended_action"`
	ActionEnabled          bool   `json:"action_enabled"`
	AttemptCount           int32  `json:"attempt_count"`
	MaxAttempts            int32  `json:"max_attempts"`
	LatestEvaluationStatus string `json:"latest_evaluation_status,omitempty"`
	LatestAgentSummary     string `json:"latest_agent_summary,omitempty"`
	PriorEvidenceSummary   string `json:"prior_evidence_summary,omitempty"`
	UpdatedAt              string `json:"updated_at,omitempty"`
}

type NodePermissionsDTO struct {
	CanApprove        bool `json:"can_approve"`
	CanRequestChanges bool `json:"can_request_changes"`
	CanRetry          bool `json:"can_retry"`
}

type ApprovalHistoryItemDTO struct {
	Action        string  `json:"action"`
	ActorType     string  `json:"actor_type"`
	ActorID       *string `json:"actor_id"`
	CreatedAt     string  `json:"created_at"`
	ChangeRequest *string `json:"change_request,omitempty"`
}

type OrchestrationEventResponse struct {
	ID        string          `json:"id"`
	PlanID    string          `json:"plan_id"`
	NodeID    *string         `json:"node_id"`
	TaskID    *string         `json:"task_id"`
	EventType string          `json:"event_type"`
	ActorType string          `json:"actor_type"`
	ActorID   *string         `json:"actor_id"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
}

type OrchestrationArtifactResponse struct {
	ID          string          `json:"id"`
	PlanID      string          `json:"plan_id"`
	NodeID      *string         `json:"node_id"`
	TaskID      *string         `json:"task_id"`
	Type        string          `json:"type"`
	URI         *string         `json:"uri"`
	Content     json.RawMessage `json:"content"`
	Metadata    json.RawMessage `json:"metadata"`
	ContentHash *string         `json:"content_hash"`
	CreatedAt   string          `json:"created_at"`
}

type IssueOrchestrationResponse struct {
	Plans     []OrchestrationPlanResponse     `json:"plans"`
	Nodes     []OrchestrationNodeResponse     `json:"nodes"`
	Events    []OrchestrationEventResponse    `json:"events"`
	Artifacts []OrchestrationArtifactResponse `json:"artifacts"`
}

type RequestChangesOrchestrationNodeRequest struct {
	ChangeRequest string `json:"change_request"`
}

func (h *Handler) enqueueAssignedAgentWork(ctx context.Context, issue db.Issue) {
	if h.TaskService.Orchestrator == nil {
		return
	}
	_, _ = h.TaskService.Orchestrator.OnIssueAssigned(ctx, issue)
}

func (h *Handler) GetIssueOrchestration(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}
	plans, err := h.Queries.ListOrchestrationPlansBySource(r.Context(), db.ListOrchestrationPlansBySourceParams{
		SourceType: "issue",
		SourceID:   issue.ID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list orchestration plans")
		return
	}
	resp := IssueOrchestrationResponse{
		Plans:     make([]OrchestrationPlanResponse, 0, len(plans)),
		Nodes:     []OrchestrationNodeResponse{},
		Events:    []OrchestrationEventResponse{},
		Artifacts: []OrchestrationArtifactResponse{},
	}
	member, _ := h.workspaceMember(w, r, uuidToString(issue.WorkspaceID))
	for _, plan := range plans {
		resp.Plans = append(resp.Plans, orchestrationPlanToResponse(plan))
		nodes, err := h.Queries.ListOrchestrationNodesByPlan(r.Context(), plan.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list orchestration nodes")
			return
		}
		events, err := h.Queries.ListOrchestrationEventsByPlan(r.Context(), plan.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list orchestration events")
			return
		}
		eventsByNode := make(map[string][]db.OrchestrationEvent, len(nodes))
		for _, event := range events {
			if event.NodeID.Valid {
				eventsByNode[uuidToString(event.NodeID)] = append(eventsByNode[uuidToString(event.NodeID)], event)
			}
			resp.Events = append(resp.Events, orchestrationEventToResponse(event))
		}
		artifactsByNode := make(map[string][]db.OrchestrationArtifact, len(nodes))
		artifacts, err := h.Queries.ListOrchestrationArtifactsByPlan(r.Context(), plan.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list orchestration artifacts")
			return
		}
		for _, artifact := range artifacts {
			if artifact.NodeID.Valid {
				artifactsByNode[uuidToString(artifact.NodeID)] = append(artifactsByNode[uuidToString(artifact.NodeID)], artifact)
			}
			resp.Artifacts = append(resp.Artifacts, orchestrationArtifactToResponse(artifact))
		}
		for _, node := range nodes {
			resp.Nodes = append(resp.Nodes, orchestrationNodeToResponse(
				node,
				eventsByNode[uuidToString(node.ID)],
				artifactsByNode[uuidToString(node.ID)],
				nodePermissions(node, plan, issue, member),
				approvalHistory(eventsByNode[uuidToString(node.ID)]),
			))
		}
	}
	sort.Slice(resp.Nodes, func(i, j int) bool {
		if resp.Nodes[i].PlanID != resp.Nodes[j].PlanID {
			return resp.Nodes[i].PlanID < resp.Nodes[j].PlanID
		}
		if resp.Nodes[i].Position != resp.Nodes[j].Position {
			return resp.Nodes[i].Position < resp.Nodes[j].Position
		}
		return resp.Nodes[i].CreatedAt < resp.Nodes[j].CreatedAt
	})
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ApproveOrchestrationNode(w http.ResponseWriter, r *http.Request) {
	node, plan, ok := h.loadOrchestrationNodeForUser(w, r)
	if !ok {
		return
	}
	member, ok := h.requireOrchestrationApprovalPermission(w, r, plan)
	if !ok {
		return
	}
	if h.TaskService.Orchestrator == nil {
		writeError(w, http.StatusServiceUnavailable, "orchestrator unavailable")
		return
	}
	if err := h.TaskService.Orchestrator.ApproveNode(r.Context(), node.ID, "member", member.UserID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan_id": uuidToString(plan.ID), "node_id": uuidToString(node.ID), "status": "approved"})
}

func (h *Handler) RetryOrchestrationNode(w http.ResponseWriter, r *http.Request) {
	node, plan, ok := h.loadOrchestrationNodeForUser(w, r)
	if !ok {
		return
	}
	member, ok := h.requireOrchestrationApprovalPermission(w, r, plan)
	if !ok {
		return
	}
	if h.TaskService.Orchestrator == nil {
		writeError(w, http.StatusServiceUnavailable, "orchestrator unavailable")
		return
	}
	task, err := h.TaskService.Orchestrator.RetryNode(r.Context(), node.ID, "member", member.UserID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, taskToResponse(*task))
}

func (h *Handler) RequestChangesOrchestrationNode(w http.ResponseWriter, r *http.Request) {
	node, plan, ok := h.loadOrchestrationNodeForUser(w, r)
	if !ok {
		return
	}
	member, ok := h.requireOrchestrationApprovalPermission(w, r, plan)
	if !ok {
		return
	}
	if h.TaskService.Orchestrator == nil {
		writeError(w, http.StatusServiceUnavailable, "orchestrator unavailable")
		return
	}
	var req RequestChangesOrchestrationNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	task, err := h.TaskService.Orchestrator.RequestNodeChanges(r.Context(), node.ID, req.ChangeRequest, "member", member.UserID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, taskToResponse(*task))
}

func (h *Handler) CancelOrchestrationPlan(w http.ResponseWriter, r *http.Request) {
	planID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "planId"), "planId")
	if !ok {
		return
	}
	plan, err := h.Queries.GetOrchestrationPlan(r.Context(), planID)
	if err != nil {
		writeError(w, http.StatusNotFound, "orchestration plan not found")
		return
	}
	member, ok := h.requireOrchestrationApprovalPermission(w, r, plan)
	if !ok {
		return
	}
	if h.TaskService.Orchestrator == nil {
		writeError(w, http.StatusServiceUnavailable, "orchestrator unavailable")
		return
	}
	if err := h.TaskService.Orchestrator.CancelPlan(r.Context(), plan.ID, "member", member.UserID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan_id": uuidToString(plan.ID), "status": "cancelled"})
}

func (h *Handler) loadOrchestrationNodeForUser(w http.ResponseWriter, r *http.Request) (db.OrchestrationNode, db.OrchestrationPlan, bool) {
	nodeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "nodeId"), "nodeId")
	if !ok {
		return db.OrchestrationNode{}, db.OrchestrationPlan{}, false
	}
	node, err := h.Queries.GetOrchestrationNode(r.Context(), nodeID)
	if err != nil {
		writeError(w, http.StatusNotFound, "orchestration node not found")
		return db.OrchestrationNode{}, db.OrchestrationPlan{}, false
	}
	plan, err := h.Queries.GetOrchestrationPlan(r.Context(), node.PlanID)
	if err != nil {
		writeError(w, http.StatusNotFound, "orchestration plan not found")
		return db.OrchestrationNode{}, db.OrchestrationPlan{}, false
	}
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(plan.WorkspaceID), "orchestration node not found"); !ok {
		return db.OrchestrationNode{}, db.OrchestrationPlan{}, false
	}
	return node, plan, true
}

func (h *Handler) requireOrchestrationApprovalPermission(w http.ResponseWriter, r *http.Request, plan db.OrchestrationPlan) (db.Member, bool) {
	member, ok := h.requireWorkspaceMember(w, r, uuidToString(plan.WorkspaceID), "orchestration node not found")
	if !ok {
		return db.Member{}, false
	}
	if roleAllowed(member.Role, "owner", "admin") {
		return member, true
	}
	if plan.SourceType != "issue" || !plan.SourceID.Valid {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return db.Member{}, false
	}
	issue, err := h.Queries.GetIssue(r.Context(), plan.SourceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return db.Member{}, false
	}
	if isIssueApprovalActor(issue, member.UserID) {
		return member, true
	}
	writeError(w, http.StatusForbidden, "insufficient permissions")
	return db.Member{}, false
}

func isIssueApprovalActor(issue db.Issue, userID pgtype.UUID) bool {
	if !userID.Valid {
		return false
	}
	if issue.CreatorType == "member" && issue.CreatorID.Valid && issue.CreatorID == userID {
		return true
	}
	if strings.TrimSpace(issue.AssigneeType.String) == "member" && issue.AssigneeID.Valid && issue.AssigneeID == userID {
		return true
	}
	return false
}

func nodePermissions(node db.OrchestrationNode, plan db.OrchestrationPlan, issue db.Issue, member db.Member) *NodePermissionsDTO {
	canApproveLike := false
	if member.UserID.Valid && (roleAllowed(member.Role, "owner", "admin") || isIssueApprovalActor(issue, member.UserID)) {
		canApproveLike = true
	}
	return &NodePermissionsDTO{
		CanApprove:        canApproveLike && node.Status == "waiting_human",
		CanRequestChanges: canApproveLike && node.Status == "waiting_human",
		CanRetry:          node.Status == "failed",
	}
}

func approvalHistory(events []db.OrchestrationEvent) []ApprovalHistoryItemDTO {
	items := make([]ApprovalHistoryItemDTO, 0)
	for _, event := range events {
		if event.EventType != "node.approved" && event.EventType != "node.change_requested" {
			continue
		}
		item := ApprovalHistoryItemDTO{
			Action:    mapApprovalAction(event.EventType),
			ActorType: event.ActorType,
			ActorID:   uuidToPtr(event.ActorID),
			CreatedAt: timestampToString(event.CreatedAt),
		}
		if event.EventType == "node.change_requested" {
			var payload struct {
				ChangeRequest string `json:"change_request"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			if strings.TrimSpace(payload.ChangeRequest) != "" {
				item.ChangeRequest = &payload.ChangeRequest
			}
		}
		items = append(items, item)
	}
	return items
}

func mapApprovalAction(eventType string) string {
	switch eventType {
	case "node.approved":
		return "approve"
	case "node.change_requested":
		return "request_changes"
	default:
		return eventType
	}
}

func latestNodeTaskID(events []db.OrchestrationEvent) *string {
	for i := len(events) - 1; i >= 0; i-- {
		if id := uuidToPtr(events[i].TaskID); id != nil {
			return id
		}
	}
	return nil
}

func orchestrationPlanToResponse(plan db.OrchestrationPlan) OrchestrationPlanResponse {
	return OrchestrationPlanResponse{
		ID:            uuidToString(plan.ID),
		WorkspaceID:   uuidToString(plan.WorkspaceID),
		SourceType:    plan.SourceType,
		SourceID:      uuidToString(plan.SourceID),
		Objective:     plan.Objective,
		Status:        plan.Status,
		Policy:        jsonRawOrEmpty(plan.Policy),
		Metadata:      jsonRawOrEmpty(plan.Metadata),
		CreatedByType: textToPtr(plan.CreatedByType),
		CreatedByID:   uuidToPtr(plan.CreatedByID),
		CreatedAt:     timestampToString(plan.CreatedAt),
		UpdatedAt:     timestampToString(plan.UpdatedAt),
	}
}

func orchestrationNodeToResponse(node db.OrchestrationNode, events []db.OrchestrationEvent, artifacts []db.OrchestrationArtifact, permissions *NodePermissionsDTO, history []ApprovalHistoryItemDTO) OrchestrationNodeResponse {
	summary := service.BuildNodeSummaryFromRecords(node, events)
	return OrchestrationNodeResponse{
		ID:                 uuidToString(node.ID),
		PlanID:             uuidToString(node.PlanID),
		Key:                node.Type,
		Type:               node.Type,
		Title:              node.Title,
		Description:        textToPtr(node.Description),
		Status:             node.Status,
		Position:           nodePositionFromType(node.Type),
		Dependencies:       nodeDependenciesFromType(node.Type),
		AssigneeAgentID:    uuidToPtr(node.AssigneeAgentID),
		InputContract:      jsonRawOrEmpty(node.InputContract),
		OutputContract:     jsonRawOrEmpty(node.OutputContract),
		EvaluatorPolicy:    jsonRawOrEmpty(node.EvaluatorPolicy),
		RetryPolicy:        jsonRawOrEmpty(node.RetryPolicy),
		RuntimeConstraints: jsonRawOrEmpty(node.RuntimeConstraints),
		AttemptCount:       node.AttemptCount,
		MaxAttempts:        node.MaxAttempts,
		LinkedTaskID:       latestNodeTaskID(events),
		ArtifactCount:      len(artifacts),
		StartedAt:          timestampToPtr(node.StartedAt),
		CompletedAt:        timestampToPtr(node.CompletedAt),
		CreatedAt:          timestampToString(node.CreatedAt),
		UpdatedAt:          timestampToString(node.UpdatedAt),
		Summary:            nodeSummaryToDTO(summary),
		Permissions:        permissions,
		ApprovalHistory:    history,
	}
}

func nodePositionFromType(nodeType string) int {
	switch nodeType {
	case "plan":
		return 1
	case "execute":
		return 2
	case "verify":
		return 3
	default:
		return 0
	}
}

func nodeDependenciesFromType(nodeType string) []string {
	switch nodeType {
	case "plan":
		return []string{}
	case "execute":
		return []string{"plan"}
	case "verify":
		return []string{"execute"}
	default:
		return []string{}
	}
}

func nodeSummaryToDTO(summary service.NodeSummary) *NodeSummaryDTO {
	return &NodeSummaryDTO{
		Status:                 summary.Status,
		ReasonCode:             summary.ReasonCode,
		ReasonTitle:            summary.ReasonTitle,
		ReasonDetail:           summary.ReasonDetail,
		RecommendedAction:      summary.RecommendedAction,
		ActionEnabled:          summary.ActionEnabled,
		AttemptCount:           summary.AttemptCount,
		MaxAttempts:            summary.MaxAttempts,
		LatestEvaluationStatus: summary.LatestEvaluationStatus,
		LatestAgentSummary:     summary.LatestAgentSummary,
		PriorEvidenceSummary:   summary.PriorEvidenceSummary,
		UpdatedAt:              summary.UpdatedAt,
	}
}

func orchestrationEventToResponse(event db.OrchestrationEvent) OrchestrationEventResponse {
	return OrchestrationEventResponse{
		ID:        uuidToString(event.ID),
		PlanID:    uuidToString(event.PlanID),
		NodeID:    uuidToPtr(event.NodeID),
		TaskID:    uuidToPtr(event.TaskID),
		EventType: event.EventType,
		ActorType: event.ActorType,
		ActorID:   uuidToPtr(event.ActorID),
		Payload:   jsonRawOrEmpty(event.Payload),
		CreatedAt: timestampToString(event.CreatedAt),
	}
}

func orchestrationArtifactToResponse(artifact db.OrchestrationArtifact) OrchestrationArtifactResponse {
	return OrchestrationArtifactResponse{
		ID:          uuidToString(artifact.ID),
		PlanID:      uuidToString(artifact.PlanID),
		NodeID:      uuidToPtr(artifact.NodeID),
		TaskID:      uuidToPtr(artifact.TaskID),
		Type:        artifact.Type,
		URI:         textToPtr(artifact.Uri),
		Content:     jsonRawOrEmpty(artifact.Content),
		Metadata:    jsonRawOrEmpty(artifact.Metadata),
		ContentHash: textToPtr(artifact.ContentHash),
		CreatedAt:   timestampToString(artifact.CreatedAt),
	}
}

func jsonRawOrEmpty(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(raw)
}
