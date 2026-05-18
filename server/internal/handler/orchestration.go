package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) StartIssueOrchestration(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForRequest(w, r)
	if !ok {
		return
	}
	if h.OrchestrationService == nil {
		writeError(w, http.StatusServiceUnavailable, "orchestration is unavailable")
		return
	}

	result, err := h.OrchestrationService.StartIssue(r.Context(), issue.WorkspaceID, issue.ID)
	if err != nil {
		if errors.Is(err, service.ErrTemporalUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "orchestration is unavailable")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to start orchestration")
		return
	}

	status := http.StatusAccepted
	if !result.Available {
		status = http.StatusServiceUnavailable
	}
	h.publish(protocol.EventOrchestrationUpdated, uuidToString(issue.WorkspaceID), "system", "", map[string]any{
		"issue_id": uuidToString(issue.ID),
		"plan_id":  result.Plan.ID,
		"status":   result.Plan.Status,
	})
	writeJSON(w, status, result)
}

func (h *Handler) GetIssueOrchestration(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForRequest(w, r)
	if !ok {
		return
	}
	if h.OrchestrationService == nil {
		writeJSON(w, http.StatusOK, service.OrchestrationSnapshot{})
		return
	}

	snapshot, err := h.OrchestrationService.Snapshot(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load orchestration")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *Handler) ApproveOrchestrationNode(w http.ResponseWriter, r *http.Request) {
	h.applyOrchestrationNodeAction(w, r, "approve")
}

func (h *Handler) RetryOrchestrationNode(w http.ResponseWriter, r *http.Request) {
	h.applyOrchestrationNodeAction(w, r, "retry")
}

func (h *Handler) CancelOrchestrationPlan(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if h.OrchestrationService == nil {
		writeError(w, http.StatusServiceUnavailable, "orchestration is unavailable")
		return
	}
	actorType, actorID := h.resolveActor(r, userID, r.Header.Get("X-Workspace-ID"))
	if actorType != "member" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	planID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "planId"), "planId")
	if !ok {
		return
	}
	body, ok := decodeApprovalActionBody(w, r)
	if !ok {
		return
	}

	result, err := h.OrchestrationService.ApplyPlanApprovalAction(r.Context(), service.PlanApprovalActionInput{
		PlanID:  planID,
		ActorID: parseUUID(actorID),
		Action:  "cancel",
		Reason:  body.Reason,
	})
	if err != nil {
		h.writeApprovalActionError(w, err, "failed to cancel orchestration plan")
		return
	}
	h.publish(protocol.EventOrchestrationUpdated, "", "member", userID, map[string]any{
		"plan_id": result.PlanID,
		"action":  result.Action,
	})
	writeJSON(w, http.StatusAccepted, result)
}

func (h *Handler) applyOrchestrationNodeAction(w http.ResponseWriter, r *http.Request, action string) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if h.OrchestrationService == nil {
		writeError(w, http.StatusServiceUnavailable, "orchestration is unavailable")
		return
	}
	actorType, actorID := h.resolveActor(r, userID, r.Header.Get("X-Workspace-ID"))
	if actorType != "member" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	nodeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "nodeId"), "nodeId")
	if !ok {
		return
	}
	body, ok := decodeApprovalActionBody(w, r)
	if !ok {
		return
	}

	result, err := h.OrchestrationService.ApplyApprovalAction(r.Context(), service.ApprovalActionInput{
		NodeID:  nodeID,
		ActorID: parseUUID(actorID),
		Action:  action,
		Reason:  body.Reason,
	})
	if err != nil {
		h.writeApprovalActionError(w, err, "failed to "+action+" orchestration node")
		return
	}
	h.publish(protocol.EventOrchestrationUpdated, "", "member", userID, map[string]any{
		"plan_id": result.PlanID,
		"node_id": result.NodeID,
		"action":  result.Action,
	})
	writeJSON(w, http.StatusAccepted, result)
}

type approvalActionBody struct {
	Reason string `json:"reason"`
}

func decodeApprovalActionBody(w http.ResponseWriter, r *http.Request) (approvalActionBody, bool) {
	var body struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			if !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid JSON")
				return approvalActionBody{}, false
			}
		}
	}
	return approvalActionBody{Reason: body.Reason}, true
}

func (h *Handler) writeApprovalActionError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, service.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, service.ErrTemporalUnavailable):
		writeError(w, http.StatusServiceUnavailable, "orchestration is unavailable")
	default:
		slog.Warn("orchestration approval action failed", "error", err)
		writeError(w, http.StatusInternalServerError, fallback)
	}
}

func (h *Handler) loadIssueForRequest(w http.ResponseWriter, r *http.Request) (db.Issue, bool) {
	rawID := chi.URLParam(r, "id")
	if rawID == "" {
		writeError(w, http.StatusBadRequest, "issue id is required")
		return db.Issue{}, false
	}
	return h.loadIssueForUser(w, r, rawID)
}
