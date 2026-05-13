package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type dispatchNodeInput struct {
	Plan                 db.OrchestrationPlan
	Node                 db.OrchestrationNode
	Agent                db.Agent
	IssueID              pgtype.UUID
	Priority             int32
	AcceptanceCriteria   json.RawMessage
	ContextRefs          json.RawMessage
	ForceFreshSession    bool
	PriorEvidenceSummary string
	ChangeRequest        string
}

type planNodeSpec struct {
	Type           string
	Title          string
	Description    pgtype.Text
	OutputContract []byte
}

func jsonHasContent(raw []byte) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "[]" && trimmed != "{}"
}

func (o *Orchestrator) dispatchReadyNodes(ctx context.Context, qtx *db.Queries, plan db.OrchestrationPlan, issueID pgtype.UUID, priority int32, acceptanceCriteria, contextRefs json.RawMessage, forceFreshSession bool) ([]db.AgentTaskQueue, error) {
	nodes, err := qtx.ListOrchestrationNodesByPlan(ctx, plan.ID)
	if err != nil {
		return nil, err
	}
	edges, err := qtx.ListOrchestrationEdgesByPlan(ctx, plan.ID)
	if err != nil {
		return nil, err
	}
	nodeByID := make(map[string]db.OrchestrationNode, len(nodes))
	for _, node := range nodes {
		nodeByID[util.UUIDToString(node.ID)] = node
	}

	var tasks []db.AgentTaskQueue
	for _, node := range nodes {
		if node.Status != "pending" && node.Status != "ready" {
			continue
		}
		if !nodeDependenciesCompleted(node, edges, nodeByID) {
			continue
		}
		if !node.AssigneeAgentID.Valid {
			if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
				PlanID:    plan.ID,
				NodeID:    node.ID,
				EventType: "node.dispatch_blocked",
				ActorType: "kernel",
				Payload:   mustJSON(map[string]any{"reason": "missing_assignee"}),
			}); err != nil {
				return nil, err
			}
			continue
		}
		agent, err := qtx.GetAgent(ctx, node.AssigneeAgentID)
		if err != nil {
			return nil, fmt.Errorf("load node agent: %w", err)
		}
		if agent.ArchivedAt.Valid || !agent.RuntimeID.Valid {
			if err := qtx.WaitOrchestrationNodeForHuman(ctx, node.ID); err != nil {
				return nil, err
			}
			if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
				PlanID:    plan.ID,
				NodeID:    node.ID,
				EventType: "node.waiting_human",
				ActorType: "kernel",
				Payload:   mustJSON(map[string]any{"reason": "agent_unavailable"}),
			}); err != nil {
				return nil, err
			}
			continue
		}
		task, err := o.dispatchNodeTask(ctx, qtx, dispatchNodeInput{
			Plan:               plan,
			Node:               node,
			Agent:              agent,
			IssueID:            issueID,
			Priority:           priority,
			AcceptanceCriteria: acceptanceCriteria,
			ContextRefs:        contextRefs,
			ForceFreshSession:  forceFreshSession,
		})
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func nodeDependenciesCompleted(node db.OrchestrationNode, edges []db.OrchestrationEdge, nodeByID map[string]db.OrchestrationNode) bool {
	nodeID := util.UUIDToString(node.ID)
	for _, edge := range edges {
		if util.UUIDToString(edge.ToNodeID) != nodeID {
			continue
		}
		upstream, ok := nodeByID[util.UUIDToString(edge.FromNodeID)]
		if !ok || upstream.Status != "completed" {
			return false
		}
	}
	return true
}

func orchestrationPlanComplete(ctx context.Context, qtx *db.Queries, planID pgtype.UUID) (bool, error) {
	nodes, err := qtx.ListOrchestrationNodesByPlan(ctx, planID)
	if err != nil {
		return false, err
	}
	if len(nodes) == 0 {
		return false, nil
	}
	for _, node := range nodes {
		if node.Status != "completed" && node.Status != "skipped" {
			return false, nil
		}
	}
	return true, nil
}

func (o *Orchestrator) dispatchNodeTask(ctx context.Context, qtx *db.Queries, in dispatchNodeInput) (db.AgentTaskQueue, error) {
	runUUID, err := uuid.NewV7()
	if err != nil {
		return db.AgentTaskQueue{}, err
	}
	runID := pgtype.UUID{Bytes: runUUID, Valid: true}
	attempt := in.Node.AttemptCount + 1
	taskContext, err := json.Marshal(OrchestrationTaskContext{
		Type:                 orchestrationContextType,
		OrchestrationPlanID:  util.UUIDToString(in.Plan.ID),
		OrchestrationNodeID:  util.UUIDToString(in.Node.ID),
		OrchestrationRunID:   util.UUIDToString(runID),
		NodeType:             in.Node.Type,
		Attempt:              attempt,
		Objective:            in.Plan.Objective,
		NodeTitle:            in.Node.Title,
		NodeDescription:      in.Node.Description.String,
		InputContract:        json.RawMessage(in.Node.InputContract),
		OutputContract:       json.RawMessage(in.Node.OutputContract),
		ExpectedResultSchema: expectedResultSchemaForNode(in.Node),
		PriorEvidenceSummary: strings.TrimSpace(in.PriorEvidenceSummary),
		ChangeRequest:        strings.TrimSpace(in.ChangeRequest),
		AcceptanceCriteria:   in.AcceptanceCriteria,
		ContextRefs:          in.ContextRefs,
	})
	if err != nil {
		return db.AgentTaskQueue{}, err
	}
	o.Logger.Info("orchestration: dispatching node task",
		"agent_id", util.UUIDToString(in.Agent.ID),
		"plan_id", util.UUIDToString(in.Plan.ID),
		"node_id", util.UUIDToString(in.Node.ID),
		"component", "orchestrator",
	)
	task, err := qtx.CreateOrchestrationNodeTask(ctx, db.CreateOrchestrationNodeTaskParams{
		AgentID:             in.Agent.ID,
		RuntimeID:           in.Agent.RuntimeID,
		IssueID:             in.IssueID,
		Priority:            in.Priority,
		Context:             taskContext,
		OrchestrationPlanID: in.Plan.ID,
		OrchestrationNodeID: in.Node.ID,
		OrchestrationRunID:  runID,
		ForceFreshSession:   pgtype.Bool{Bool: in.ForceFreshSession, Valid: in.ForceFreshSession},
	})
	if err != nil {
		return db.AgentTaskQueue{}, fmt.Errorf("create node task: %w", err)
	}
	updatedNode, err := qtx.MarkOrchestrationNodeDispatched(ctx, in.Node.ID)
	if err != nil {
		return db.AgentTaskQueue{}, err
	}
	_, err = qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
		PlanID:    in.Plan.ID,
		NodeID:    in.Node.ID,
		TaskID:    task.ID,
		EventType: "node.dispatched",
		ActorType: "kernel",
		Payload: mustJSON(nodeDispatchedPayload(
			util.UUIDToString(task.ID),
			util.UUIDToString(runID),
			updatedNode.AttemptCount,
			updatedNode.MaxAttempts,
		)),
	})
	if err != nil {
		return db.AgentTaskQueue{}, err
	}
	return task, nil
}

func nodeDispatchedPayload(taskID, runID string, attemptCount, maxAttempts int32) map[string]any {
	return map[string]any{
		"task_id":       taskID,
		"run_id":        runID,
		"attempt_count": attemptCount,
		"max_attempts":  maxAttempts,
	}
}

func expectedResultSchemaForNode(node db.OrchestrationNode) json.RawMessage {
	if len(node.OutputContract) > 0 && json.Valid(node.OutputContract) {
		return json.RawMessage(node.OutputContract)
	}
	return json.RawMessage(`{"required":["summary"]}`)
}

func buildRetryEvidenceSummary(result AgentStructuredResult, validation ResultValidation, eval EvaluationResult) string {
	parts := make([]string, 0, 4)
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		parts = append(parts, "Previous agent summary: "+summary)
	}
	if reason := strings.TrimSpace(eval.Reason); reason != "" {
		parts = append(parts, "Kernel reason: "+reason)
	}
	if detail := strings.TrimSpace(eval.ReasonDetail); detail != "" {
		parts = append(parts, "Kernel detail: "+detail)
	}
	if len(validation.Errors) > 0 {
		errParts := make([]string, 0, len(validation.Errors))
		for _, err := range validation.Errors {
			msg := strings.TrimSpace(err.Code)
			if field := strings.TrimSpace(err.Field); field != "" {
				msg += "@" + field
			}
			if text := strings.TrimSpace(err.Message); text != "" {
				if msg != "" {
					msg += ": "
				}
				msg += text
			}
			if msg != "" {
				errParts = append(errParts, msg)
			}
		}
		if len(errParts) > 0 {
			parts = append(parts, "Validation errors: "+strings.Join(errParts, "; "))
		}
	}
	return strings.Join(parts, "\n")
}

func buildRetryChangeRequest(eval EvaluationResult) string {
	if detail := strings.TrimSpace(eval.ReasonDetail); detail != "" {
		return detail
	}
	if reason := strings.TrimSpace(eval.Reason); reason != "" {
		return "Address kernel evaluation failure: " + reason
	}
	return ""
}
