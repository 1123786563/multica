package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (o *Orchestrator) OnIssueAssigned(ctx context.Context, issue db.Issue) (*db.AgentTaskQueue, error) {
	if !issue.AssigneeID.Valid {
		return nil, fmt.Errorf("issue has no assignee")
	}
	if existing, err := o.Queries.GetActiveOrchestrationPlanBySource(ctx, db.GetActiveOrchestrationPlanBySourceParams{
		SourceType: "issue",
		SourceID:   issue.ID,
	}); err == nil && existing.ID.Valid {
		o.Logger.Info("orchestration: active plan already exists, skipping",
			"plan_id", util.UUIDToString(existing.ID),
			"issue_id", util.UUIDToString(issue.ID),
		)
		return nil, nil
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("lookup active plan: %w", err)
	}

	return o.startIssuePlan(ctx, issue, false)
}

func (o *Orchestrator) RerunIssue(ctx context.Context, issue db.Issue) (*db.AgentTaskQueue, error) {
	if !issue.AssigneeID.Valid {
		return nil, fmt.Errorf("issue has no assignee")
	}
	if existing, err := o.Queries.GetActiveOrchestrationPlanBySource(ctx, db.GetActiveOrchestrationPlanBySourceParams{
		SourceType: "issue",
		SourceID:   issue.ID,
	}); err == nil && existing.ID.Valid {
		if err := o.CancelPlan(ctx, existing.ID, "kernel", pgtype.UUID{}); err != nil {
			return nil, fmt.Errorf("cancel active plan: %w", err)
		}
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("lookup active plan: %w", err)
	}

	return o.startIssuePlan(ctx, issue, true)
}

func (o *Orchestrator) startIssuePlan(ctx context.Context, issue db.Issue, forceFreshSession bool) (*db.AgentTaskQueue, error) {
	if !issue.AssigneeID.Valid {
		return nil, fmt.Errorf("issue has no assignee")
	}

	agent, err := o.Queries.GetAgent(ctx, issue.AssigneeID)
	if err != nil {
		return nil, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		return nil, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		return nil, fmt.Errorf("agent has no runtime")
	}

	o.Logger.Info("orchestration: creating plan for issue",
		"issue_id", util.UUIDToString(issue.ID),
		"issue_title", issue.Title,
		"agent_id", util.UUIDToString(agent.ID),
		"component", "orchestrator",
	)

	policy := []byte(`{"auto_execute":true,"planner":"simple","evaluator":"hard_check"}`)
	empty := []byte(`{}`)
	outputContract := []byte(`{"required":["summary","criteria_evidence"],"min_confidence":0.5}`)
	objective := strings.TrimSpace(issue.Title)
	if issue.Description.Valid && strings.TrimSpace(issue.Description.String) != "" {
		objective = objective + "\n\n" + strings.TrimSpace(issue.Description.String)
	}

	var queuedTasks []db.AgentTaskQueue
	err = o.runInTx(ctx, func(qtx *db.Queries) error {
		// Lock any existing active plan row to prevent concurrent plan creation.
		existing, err := qtx.LockActiveOrchestrationPlanBySource(ctx, db.LockActiveOrchestrationPlanBySourceParams{
			SourceType: "issue",
			SourceID:   issue.ID,
		})
		if err == nil && existing.ID.Valid {
			o.Logger.Info("orchestration: active plan already exists (locked), skipping",
				"plan_id", util.UUIDToString(existing.ID),
				"issue_id", util.UUIDToString(issue.ID),
			)
			return nil
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lock active plan: %w", err)
		}

		plan, err := qtx.CreateOrchestrationPlan(ctx, db.CreateOrchestrationPlanParams{
			WorkspaceID:   issue.WorkspaceID,
			SourceType:    "issue",
			SourceID:      issue.ID,
			Objective:     objective,
			Status:        "running",
			Policy:        policy,
			Metadata:      empty,
			CreatedByType: pgtype.Text{String: issue.CreatorType, Valid: issue.CreatorType != ""},
			CreatedByID:   issue.CreatorID,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				o.Logger.Info("orchestration: concurrent plan creation detected, skipping",
					"issue_id", util.UUIDToString(issue.ID),
				)
				return nil
			}
			return fmt.Errorf("create plan: %w", err)
		}
		o.Logger.Info("orchestration: plan created",
			"plan_id", util.UUIDToString(plan.ID),
			"objective", truncate(objective, 100),
			"component", "orchestrator",
		)
		if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    plan.ID,
			EventType: "plan.created",
			ActorType: "kernel",
			Payload:   mustJSON(map[string]any{"source_type": "issue", "source_id": util.UUIDToString(issue.ID)}),
		}); err != nil {
			return err
		}
		specs := buildPlanNodeSpecs(issue, outputContract)
		nodes := make([]db.OrchestrationNode, 0, len(specs))
		for i, spec := range specs {
			status := "pending"
			if i == 0 {
				status = "ready"
			}
			node, err := qtx.CreateOrchestrationNode(ctx, db.CreateOrchestrationNodeParams{
				PlanID:             plan.ID,
				Type:               spec.Type,
				Title:              spec.Title,
				Description:        spec.Description,
				Status:             status,
				AssigneeAgentID:    issue.AssigneeID,
				InputContract:      empty,
				OutputContract:     spec.OutputContract,
				EvaluatorPolicy:    []byte(`{"mode":"hard_check"}`),
				RetryPolicy:        []byte(`{"max_auto_retries":2}`),
				RuntimeConstraints: empty,
				MaxAttempts:        2,
			})
			if err != nil {
				return fmt.Errorf("create node: %w", err)
			}
			nodes = append(nodes, node)
			o.Logger.Info("orchestration: node created",
				"plan_id", util.UUIDToString(plan.ID),
				"node_id", util.UUIDToString(node.ID),
				"node_type", node.Type,
				"title", node.Title,
				"component", "orchestrator",
			)
			if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
				PlanID:    plan.ID,
				NodeID:    node.ID,
				EventType: "node.created",
				ActorType: "kernel",
				Payload:   mustJSON(map[string]any{"node_type": node.Type, "title": node.Title}),
			}); err != nil {
				return err
			}
			if i > 0 {
				if _, err := qtx.CreateOrchestrationEdge(ctx, db.CreateOrchestrationEdgeParams{
					PlanID:     plan.ID,
					FromNodeID: nodes[i-1].ID,
					ToNodeID:   node.ID,
					Type:       "blocks",
					Metadata:   empty,
				}); err != nil {
					return fmt.Errorf("create edge: %w", err)
				}
			}
		}
		tasks, err := o.dispatchReadyNodes(ctx, qtx, plan, issue.ID, priorityToInt(issue.Priority), json.RawMessage(issue.AcceptanceCriteria), json.RawMessage(issue.ContextRefs), forceFreshSession)
		if err != nil {
			return err
		}
		queuedTasks = tasks
		return nil
	})
	if err != nil {
		o.Logger.Error("orchestration: OnIssueAssigned failed",
			"issue_id", util.UUIDToString(issue.ID),
			"error", err,
			"component", "orchestrator",
		)
		return nil, err
	}

	for _, task := range queuedTasks {
		o.notifyTaskQueued(ctx, task)
	}
	o.Logger.Info("orchestration: task dispatched for issue",
		"task_count", len(queuedTasks),
		"issue_id", util.UUIDToString(issue.ID),
		"component", "orchestrator",
	)
	if len(queuedTasks) == 0 {
		return nil, nil
	}
	return &queuedTasks[0], nil
}

func buildPlanNodeSpecs(issue db.Issue, defaultOutputContract []byte) []planNodeSpec {
	description := issue.Description
	return []planNodeSpec{
		{
			Type:           "plan",
			Title:          "Plan issue: " + issue.Title,
			Description:    description,
			OutputContract: []byte(`{"required":["summary","criteria_evidence"]}`),
		},
		{
			Type:           "execute",
			Title:          "Execute issue: " + issue.Title,
			Description:    description,
			OutputContract: defaultOutputContract,
		},
		{
			Type:           "verify",
			Title:          "Verify issue: " + issue.Title,
			Description:    description,
			OutputContract: []byte(`{"required":["summary","test_result","criteria_evidence"]}`),
		},
	}
}
