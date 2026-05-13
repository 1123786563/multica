package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (o *Orchestrator) RetryNode(ctx context.Context, nodeID pgtype.UUID, actorType string, actorID pgtype.UUID) (*db.AgentTaskQueue, error) {
	node, err := o.Queries.GetOrchestrationNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	plan, err := o.Queries.GetOrchestrationPlan(ctx, node.PlanID)
	if err != nil {
		return nil, err
	}
	if !node.AssigneeAgentID.Valid {
		return nil, fmt.Errorf("node has no assignee")
	}
	agent, err := o.Queries.GetAgent(ctx, node.AssigneeAgentID)
	if err != nil {
		return nil, err
	}
	if !agent.RuntimeID.Valid {
		return nil, fmt.Errorf("agent has no runtime")
	}
	var task db.AgentTaskQueue
	err = o.runInTx(ctx, func(qtx *db.Queries) error {
		if err := qtx.ReadyOrchestrationNode(ctx, nodeID); err != nil {
			return err
		}
		t, err := o.dispatchNodeTask(ctx, qtx, dispatchNodeInput{
			Plan:     plan,
			Node:     node,
			Agent:    agent,
			IssueID:  plan.SourceID,
			Priority: 0,
		})
		if err != nil {
			return err
		}
		task = t
		_, err = qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    plan.ID,
			NodeID:    node.ID,
			TaskID:    task.ID,
			EventType: "node.retry_requested",
			ActorType: actorType,
			ActorID:   actorID,
			Payload:   mustJSON(map[string]any{"task_id": util.UUIDToString(task.ID)}),
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	o.notifyTaskQueued(ctx, task)
	return &task, nil
}

func (o *Orchestrator) ApproveNode(ctx context.Context, nodeID pgtype.UUID, actorType string, actorID pgtype.UUID) error {
	node, err := o.Queries.GetOrchestrationNode(ctx, nodeID)
	if err != nil {
		return err
	}
	plan, err := o.Queries.GetOrchestrationPlan(ctx, node.PlanID)
	if err != nil {
		return err
	}
	var downstreamTasks []db.AgentTaskQueue
	err = o.runInTx(ctx, func(qtx *db.Queries) error {
		if err := qtx.CompleteOrchestrationNode(ctx, nodeID); err != nil {
			return err
		}
		if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    plan.ID,
			NodeID:    node.ID,
			EventType: "node.approved",
			ActorType: actorType,
			ActorID:   actorID,
			Payload:   mustJSON(map[string]any{"reason": "manual_approval"}),
		}); err != nil {
			return err
		}
		next, err := o.dispatchReadyNodes(ctx, qtx, plan, plan.SourceID, 0, nil, nil, false)
		if err != nil {
			return err
		}
		downstreamTasks = next
		if len(next) > 0 {
			return qtx.UpdateOrchestrationPlanStatus(ctx, db.UpdateOrchestrationPlanStatusParams{ID: plan.ID, Status: "running"})
		}
		complete, err := orchestrationPlanComplete(ctx, qtx, plan.ID)
		if err != nil || !complete {
			return err
		}
		if err := qtx.UpdateOrchestrationPlanStatus(ctx, db.UpdateOrchestrationPlanStatusParams{ID: plan.ID, Status: "completed"}); err != nil {
			return err
		}
		if plan.SourceType == "issue" {
			if _, err := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: plan.SourceID, Status: "in_review"}); err != nil {
				return err
			}
		}
		_, err = qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    plan.ID,
			NodeID:    node.ID,
			EventType: "plan.completed",
			ActorType: "kernel",
			Payload:   mustJSON(map[string]any{"reason": "manual_approval"}),
		})
		return err
	})
	if err != nil {
		return err
	}
	o.publishOrchestrationUpdated(ctx, plan)
	for _, task := range downstreamTasks {
		o.notifyTaskQueued(ctx, task)
	}
	return nil
}

func (o *Orchestrator) RequestNodeChanges(ctx context.Context, nodeID pgtype.UUID, changeRequest string, actorType string, actorID pgtype.UUID) (*db.AgentTaskQueue, error) {
	node, err := o.Queries.GetOrchestrationNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	plan, err := o.Queries.GetOrchestrationPlan(ctx, node.PlanID)
	if err != nil {
		return nil, err
	}
	if !node.AssigneeAgentID.Valid {
		return nil, fmt.Errorf("node has no assignee")
	}
	agent, err := o.Queries.GetAgent(ctx, node.AssigneeAgentID)
	if err != nil {
		return nil, err
	}
	if !agent.RuntimeID.Valid {
		return nil, fmt.Errorf("agent has no runtime")
	}
	changeRequest = strings.TrimSpace(changeRequest)
	if changeRequest == "" {
		return nil, fmt.Errorf("change_request is required")
	}

	var task db.AgentTaskQueue
	err = o.runInTx(ctx, func(qtx *db.Queries) error {
		if err := qtx.ReadyOrchestrationNode(ctx, nodeID); err != nil {
			return err
		}
		t, err := o.dispatchNodeTask(ctx, qtx, dispatchNodeInput{
			Plan:          plan,
			Node:          node,
			Agent:         agent,
			IssueID:       plan.SourceID,
			Priority:      0,
			ChangeRequest: changeRequest,
		})
		if err != nil {
			return err
		}
		task = t
		if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    plan.ID,
			NodeID:    node.ID,
			TaskID:    task.ID,
			EventType: "node.change_requested",
			ActorType: actorType,
			ActorID:   actorID,
			Payload: mustJSON(map[string]any{
				"task_id":            util.UUIDToString(task.ID),
				"change_request":     changeRequest,
				"recommended_action": "request_changes",
			}),
		}); err != nil {
			return err
		}
		return qtx.UpdateOrchestrationPlanStatus(ctx, db.UpdateOrchestrationPlanStatusParams{ID: plan.ID, Status: "running"})
	})
	if err != nil {
		return nil, err
	}
	o.publishOrchestrationUpdated(ctx, plan)
	o.notifyTaskQueued(ctx, task)
	return &task, nil
}

func (o *Orchestrator) CancelPlan(ctx context.Context, planID pgtype.UUID, actorType string, actorID pgtype.UUID) error {
	plan, err := o.Queries.GetOrchestrationPlan(ctx, planID)
	if err != nil {
		return err
	}
	var cancelledTasks []db.AgentTaskQueue
	if err := o.runInTx(ctx, func(qtx *db.Queries) error {
		if err := qtx.UpdateOrchestrationPlanStatus(ctx, db.UpdateOrchestrationPlanStatusParams{ID: planID, Status: "cancelled"}); err != nil {
			return err
		}
		nodes, err := qtx.CancelActiveOrchestrationNodesByPlan(ctx, planID)
		if err != nil {
			return err
		}
		for _, node := range nodes {
			if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
				PlanID:    planID,
				NodeID:    node.ID,
				EventType: "node.cancelled",
				ActorType: actorType,
				ActorID:   actorID,
				Payload: mustJSON(map[string]any{
					"node_type": node.Type,
				}),
			}); err != nil {
				return err
			}
		}
		cancelledTasks, err = qtx.CancelActiveOrchestrationTasksByPlan(ctx, planID)
		if err != nil {
			return err
		}
		_, err = qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    planID,
			EventType: "plan.cancelled",
			ActorType: actorType,
			ActorID:   actorID,
			Payload: mustJSON(map[string]any{
				"cancelled_nodes": len(nodes),
				"cancelled_tasks": len(cancelledTasks),
			}),
		})
		return err
	}); err != nil {
		return err
	}
	if o.TaskSvc != nil {
		o.TaskSvc.BroadcastCancelledTasks(ctx, cancelledTasks)
	}
	o.publishOrchestrationUpdated(ctx, plan)
	return nil
}

func (o *Orchestrator) CancelActivePlanForIssue(ctx context.Context, issueID pgtype.UUID, actorType string, actorID pgtype.UUID) error {
	plan, err := o.Queries.GetActiveOrchestrationPlanBySource(ctx, db.GetActiveOrchestrationPlanBySourceParams{
		SourceType: "issue",
		SourceID:   issueID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	return o.CancelPlan(ctx, plan.ID, actorType, actorID)
}
