package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const orchestrationContextType = "orchestration_node"

type Orchestrator struct {
	Queries   *db.Queries
	TxStarter TxStarter
	TaskSvc   *TaskService
	Logger    *slog.Logger
}

type OrchestrationTaskContext struct {
	Type                string          `json:"type"`
	OrchestrationPlanID string          `json:"orchestration_plan_id"`
	OrchestrationNodeID string          `json:"orchestration_node_id"`
	OrchestrationRunID  string          `json:"orchestration_run_id"`
	NodeType            string          `json:"node_type"`
	Objective           string          `json:"objective"`
	NodeTitle           string          `json:"node_title"`
	NodeDescription     string          `json:"node_description,omitempty"`
	InputContract       json.RawMessage `json:"input_contract,omitempty"`
	OutputContract      json.RawMessage `json:"output_contract,omitempty"`
	AcceptanceCriteria  json.RawMessage `json:"acceptance_criteria,omitempty"`
	ContextRefs         json.RawMessage `json:"context_refs,omitempty"`
}

type dispatchNodeInput struct {
	Plan               db.OrchestrationPlan
	Node               db.OrchestrationNode
	Agent              db.Agent
	IssueID            pgtype.UUID
	Priority           int32
	AcceptanceCriteria json.RawMessage
	ContextRefs        json.RawMessage
}

type planNodeSpec struct {
	Type           string
	Title          string
	Description    pgtype.Text
	OutputContract []byte
}

func NewOrchestrator(q *db.Queries, tx TxStarter, taskSvc *TaskService) *Orchestrator {
	return &Orchestrator{Queries: q, TxStarter: tx, TaskSvc: taskSvc, Logger: slog.Default()}
}

func ParseOrchestrationTaskContext(raw []byte) (OrchestrationTaskContext, bool) {
	if len(raw) == 0 {
		return OrchestrationTaskContext{}, false
	}
	var ctx OrchestrationTaskContext
	if err := json.Unmarshal(raw, &ctx); err != nil {
		return OrchestrationTaskContext{}, false
	}
	if ctx.Type != orchestrationContextType || ctx.OrchestrationPlanID == "" || ctx.OrchestrationNodeID == "" {
		return OrchestrationTaskContext{}, false
	}
	return ctx, true
}

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
		tasks, err := o.dispatchReadyNodes(ctx, qtx, plan, issue.ID, priorityToInt(issue.Priority), json.RawMessage(issue.AcceptanceCriteria), json.RawMessage(issue.ContextRefs))
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

func (o *Orchestrator) OnTaskStarted(ctx context.Context, task db.AgentTaskQueue) error {
	taskCtx, ok := ParseOrchestrationTaskContext(task.Context)
	if !ok {
		return nil
	}
	o.Logger.Info("orchestration: task started, marking node running",
		"task_id", util.UUIDToString(task.ID),
		"plan_id", taskCtx.OrchestrationPlanID,
		"node_id", taskCtx.OrchestrationNodeID,
		"component", "orchestrator",
	)
	if err := o.runInTx(ctx, func(qtx *db.Queries) error {
		return o.OnTaskStartedTx(ctx, qtx, task)
	}); err != nil {
		return err
	}
	return nil
}

func (o *Orchestrator) OnTaskStartedTx(ctx context.Context, qtx *db.Queries, task db.AgentTaskQueue) error {
	taskCtx, ok := ParseOrchestrationTaskContext(task.Context)
	if !ok {
		return nil
	}
	nodeID, err := util.ParseUUID(taskCtx.OrchestrationNodeID)
	if err != nil {
		return err
	}
	planID, err := util.ParseUUID(taskCtx.OrchestrationPlanID)
	if err != nil {
		return err
	}
	if err := qtx.MarkOrchestrationNodeRunning(ctx, nodeID); err != nil {
		return err
	}
	_, err = qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
		PlanID:    planID,
		NodeID:    nodeID,
		TaskID:    task.ID,
		EventType: "node.running",
		ActorType: "kernel",
		Payload:   mustJSON(map[string]any{"task_id": util.UUIDToString(task.ID)}),
	})
	return err
}

func (o *Orchestrator) RetryNode(ctx context.Context, nodeID pgtype.UUID) (*db.AgentTaskQueue, error) {
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
			ActorType: "kernel",
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

func (o *Orchestrator) ApproveNode(ctx context.Context, nodeID pgtype.UUID) error {
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
			ActorType: "kernel",
			Payload:   mustJSON(map[string]any{"reason": "manual_approval"}),
		}); err != nil {
			return err
		}
		next, err := o.dispatchReadyNodes(ctx, qtx, plan, plan.SourceID, 0, nil, nil)
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
			if _, err := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: plan.SourceID, Status: "done"}); err != nil {
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
	for _, task := range downstreamTasks {
		o.notifyTaskQueued(ctx, task)
	}
	return nil
}

func (o *Orchestrator) CancelPlan(ctx context.Context, planID pgtype.UUID) error {
	return o.runInTx(ctx, func(qtx *db.Queries) error {
		if err := qtx.UpdateOrchestrationPlanStatus(ctx, db.UpdateOrchestrationPlanStatusParams{ID: planID, Status: "cancelled"}); err != nil {
			return err
		}
		_, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    planID,
			EventType: "plan.cancelled",
			ActorType: "kernel",
			Payload:   []byte(`{}`),
		})
		return err
	})
}

func (o *Orchestrator) OnTaskCompleted(ctx context.Context, task db.AgentTaskQueue, rawResult []byte) error {
	var queuedTasks []db.AgentTaskQueue
	if err := o.runInTx(ctx, func(qtx *db.Queries) error {
		var err error
		queuedTasks, err = o.OnTaskCompletedTx(ctx, qtx, task, rawResult)
		return err
	}); err != nil {
		return err
	}
	for _, task := range queuedTasks {
		o.notifyTaskQueued(ctx, task)
	}
	return nil
}

func (o *Orchestrator) OnTaskCompletedTx(ctx context.Context, qtx *db.Queries, task db.AgentTaskQueue, rawResult []byte) ([]db.AgentTaskQueue, error) {
	taskCtx, ok := ParseOrchestrationTaskContext(task.Context)
	if !ok {
		return nil, nil
	}
	planID, err := util.ParseUUID(taskCtx.OrchestrationPlanID)
	if err != nil {
		return nil, err
	}
	nodeID, err := util.ParseUUID(taskCtx.OrchestrationNodeID)
	if err != nil {
		return nil, err
	}
	node, err := qtx.GetOrchestrationNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	plan, err := qtx.GetOrchestrationPlan(ctx, planID)
	if err != nil {
		return nil, err
	}
	validation := ParseAgentResultPayload(rawResult, ResultParseOptions{AllowLegacyCompatibility: true})
	result := validation.Result
	eval, err := (HardCheckEvaluator{}).Evaluate(ctx, EvaluationInput{
		Plan:               plan,
		Node:               node,
		Task:               task,
		Result:             result,
		Validation:         validation,
		AcceptanceCriteria: ParseAcceptanceCriteria(taskCtx.AcceptanceCriteria),
	})
	if err != nil {
		return nil, err
	}
	pass := eval.Pass
	reason := eval.Reason
	waitHuman := !pass && eval.RecommendedAction == "ask_human"

	o.Logger.Info("orchestration: task completed, evaluating",
		"task_id", util.UUIDToString(task.ID),
		"plan_id", util.UUIDToString(planID),
		"node_id", util.UUIDToString(nodeID),
		"pass", pass,
		"reason", reason,
		"confidence", result.Confidence,
		"wait_human", waitHuman,
		"attempt", node.AttemptCount,
		"max_attempts", node.MaxAttempts,
		"component", "orchestrator",
	)

	queuedTasks := make([]db.AgentTaskQueue, 0, 1)
	if err := qtx.MarkOrchestrationNodeEvaluating(ctx, nodeID); err != nil {
		return nil, err
	}
	if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
		PlanID:    planID,
		NodeID:    nodeID,
		TaskID:    task.ID,
		EventType: "node.evaluating",
		ActorType: "kernel",
		Payload:   mustJSON(map[string]any{"task_id": util.UUIDToString(task.ID)}),
	}); err != nil {
		return nil, err
	}
	if validation.Valid {
		for _, artifact := range NormalizeArtifacts(result) {
			content := artifact.Content
			if len(content) == 0 {
				content = []byte(`{}`)
			}
			metadata := artifact.Metadata
			if len(metadata) == 0 {
				metadata = []byte(`{}`)
			}
			created, err := qtx.CreateOrchestrationArtifact(ctx, db.CreateOrchestrationArtifactParams{
				PlanID:   planID,
				NodeID:   nodeID,
				TaskID:   task.ID,
				Type:     artifact.Type,
				Uri:      pgtype.Text{String: artifact.URI, Valid: artifact.URI != ""},
				Content:  content,
				Metadata: metadata,
			})
			if err != nil {
				return nil, err
			}
			if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
				PlanID:    planID,
				NodeID:    nodeID,
				TaskID:    task.ID,
				EventType: "artifact.recorded",
				ActorType: "kernel",
				Payload: mustJSON(map[string]any{
					"artifact_id": util.UUIDToString(created.ID),
					"type":        created.Type,
				}),
			}); err != nil {
				return nil, err
			}
		}
	}
	payload := mustJSON(map[string]any{
		"pass":               eval.Pass,
		"reason":             eval.Reason,
		"summary":            result.Summary,
		"recommended_action": eval.RecommendedAction,
		"validation_errors":  validation.Errors,
		"compatibility_mode": validation.CompatibilityMode,
	})
	if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
		PlanID:    planID,
		NodeID:    nodeID,
		TaskID:    task.ID,
		EventType: "task.completed",
		ActorType: "agent",
		ActorID:   task.AgentID,
		Payload:   payload,
	}); err != nil {
		return nil, err
	}
	evaluationEventType := "evaluation.failed"
	if validation.Valid && eval.Pass {
		evaluationEventType = "evaluation.passed"
	} else if !validation.Valid {
		evaluationEventType = "evaluation.invalid_result"
	} else if eval.RecommendedAction == "ask_human" {
		evaluationEventType = "evaluation.waiting_human"
	}
	if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
		PlanID:    planID,
		NodeID:    nodeID,
		TaskID:    task.ID,
		EventType: evaluationEventType,
		ActorType: "kernel",
		Payload:   payload,
	}); err != nil {
		return nil, err
	}
	if pass {
		o.Logger.Info("orchestration: evaluation passed, completing node",
			"plan_id", util.UUIDToString(planID),
			"node_id", util.UUIDToString(nodeID),
			"component", "orchestrator",
		)
		if err := qtx.CompleteOrchestrationNode(ctx, nodeID); err != nil {
			return nil, err
		}
		if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    planID,
			NodeID:    nodeID,
			TaskID:    task.ID,
			EventType: "node.completed",
			ActorType: "kernel",
			Payload:   payload,
		}); err != nil {
			return nil, err
		}
		next, err := o.dispatchReadyNodes(ctx, qtx, plan, task.IssueID, task.Priority, taskCtx.AcceptanceCriteria, taskCtx.ContextRefs)
		if err != nil {
			return nil, err
		}
		queuedTasks = append(queuedTasks, next...)
		if len(next) > 0 {
			if err := qtx.UpdateOrchestrationPlanStatus(ctx, db.UpdateOrchestrationPlanStatusParams{ID: planID, Status: "running"}); err != nil {
				return nil, err
			}
			return queuedTasks, nil
		}
		complete, err := orchestrationPlanComplete(ctx, qtx, planID)
		if err != nil {
			return nil, err
		}
		if !complete {
			return queuedTasks, nil
		}
		if err := qtx.UpdateOrchestrationPlanStatus(ctx, db.UpdateOrchestrationPlanStatusParams{ID: planID, Status: "completed"}); err != nil {
			return nil, err
		}
		if task.IssueID.Valid {
			if _, err := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: task.IssueID, Status: "done"}); err != nil {
				return nil, err
			}
		}
		_, err = qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    planID,
			NodeID:    nodeID,
			TaskID:    task.ID,
			EventType: "plan.completed",
			ActorType: "kernel",
			Payload:   payload,
		})
		return queuedTasks, err
	}
	if waitHuman {
		o.Logger.Warn("orchestration: low confidence, waiting for human review",
			"plan_id", util.UUIDToString(planID),
			"node_id", util.UUIDToString(nodeID),
			"confidence", result.Confidence,
			"component", "orchestrator",
		)
		if err := qtx.WaitOrchestrationNodeForHuman(ctx, nodeID); err != nil {
			return nil, err
		}
		if err := qtx.UpdateOrchestrationPlanStatus(ctx, db.UpdateOrchestrationPlanStatusParams{ID: planID, Status: "waiting_human"}); err != nil {
			return nil, err
		}
		if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    planID,
			NodeID:    nodeID,
			TaskID:    task.ID,
			EventType: "node.waiting_human",
			ActorType: "kernel",
			Payload:   payload,
		}); err != nil {
			return nil, err
		}
		_, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    planID,
			NodeID:    nodeID,
			TaskID:    task.ID,
			EventType: "plan.waiting_human",
			ActorType: "kernel",
			Payload:   payload,
		})
		return queuedTasks, err
	}
	o.Logger.Info("orchestration: evaluation failed, attempting auto-retry",
		"plan_id", util.UUIDToString(planID),
		"node_id", util.UUIDToString(nodeID),
		"attempt", node.AttemptCount,
		"max_attempts", node.MaxAttempts,
		"reason", reason,
		"component", "orchestrator",
	)
	if node.AttemptCount < node.MaxAttempts {
		if err := qtx.ReadyOrchestrationNode(ctx, nodeID); err != nil {
			return nil, err
		}
		if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    planID,
			NodeID:    nodeID,
			TaskID:    task.ID,
			EventType: "node.retry_ready",
			ActorType: "kernel",
			Payload:   payload,
		}); err != nil {
			return nil, err
		}
		agent := db.Agent{ID: task.AgentID, RuntimeID: task.RuntimeID}
		next, err := o.dispatchNodeTask(ctx, qtx, dispatchNodeInput{
			Plan:               db.OrchestrationPlan{ID: planID, Objective: taskCtx.Objective},
			Node:               node,
			Agent:              agent,
			IssueID:            task.IssueID,
			Priority:           task.Priority,
			AcceptanceCriteria: taskCtx.AcceptanceCriteria,
			ContextRefs:        taskCtx.ContextRefs,
		})
		if err != nil {
			return nil, err
		}
		queuedTasks = append(queuedTasks, next)
		_, err = qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    planID,
			NodeID:    nodeID,
			TaskID:    next.ID,
			EventType: "node.retry_scheduled",
			ActorType: "kernel",
			Payload: mustJSON(map[string]any{
				"previous_task_id": util.UUIDToString(task.ID),
				"next_task_id":     util.UUIDToString(next.ID),
				"reason":           reason,
			}),
		})
		return queuedTasks, err
	}
	o.Logger.Error("orchestration: max attempts exhausted, failing node",
		"plan_id", util.UUIDToString(planID),
		"node_id", util.UUIDToString(nodeID),
		"attempt", node.AttemptCount,
		"reason", reason,
		"component", "orchestrator",
	)
	if err := qtx.FailOrchestrationNode(ctx, nodeID); err != nil {
		return nil, err
	}
	if err := qtx.UpdateOrchestrationPlanStatus(ctx, db.UpdateOrchestrationPlanStatusParams{ID: planID, Status: "failed"}); err != nil {
		return nil, err
	}
	if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
		PlanID:    planID,
		NodeID:    nodeID,
		TaskID:    task.ID,
		EventType: "node.failed",
		ActorType: "kernel",
		Payload:   payload,
	}); err != nil {
		return nil, err
	}
	_, err = qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
		PlanID:    planID,
		NodeID:    nodeID,
		TaskID:    task.ID,
		EventType: "plan.failed",
		ActorType: "kernel",
		Payload:   payload,
	})
	return queuedTasks, err
}

func buildPlanNodeSpecs(issue db.Issue, defaultOutputContract []byte) []planNodeSpec {
	description := issue.Description
	if issue.Priority == "urgent" || jsonHasContent(issue.AcceptanceCriteria) {
		return []planNodeSpec{
			{
				Type:           "inspect",
				Title:          "Inspect issue context: " + issue.Title,
				Description:    description,
				OutputContract: []byte(`{"required":["summary","criteria_evidence"]}`),
			},
			{
				Type:           "implement",
				Title:          "Implement issue: " + issue.Title,
				Description:    description,
				OutputContract: defaultOutputContract,
			},
			{
				Type:           "test",
				Title:          "Verify issue acceptance: " + issue.Title,
				Description:    description,
				OutputContract: []byte(`{"required":["summary","test_result","criteria_evidence"]}`),
			},
		}
	}
	return []planNodeSpec{{
		Type:           "implement",
		Title:          "Implement issue: " + issue.Title,
		Description:    description,
		OutputContract: defaultOutputContract,
	}}
}

func jsonHasContent(raw []byte) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "[]" && trimmed != "{}"
}

func (o *Orchestrator) dispatchReadyNodes(ctx context.Context, qtx *db.Queries, plan db.OrchestrationPlan, issueID pgtype.UUID, priority int32, acceptanceCriteria, contextRefs json.RawMessage) ([]db.AgentTaskQueue, error) {
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
	taskContext, err := json.Marshal(OrchestrationTaskContext{
		Type:                orchestrationContextType,
		OrchestrationPlanID: util.UUIDToString(in.Plan.ID),
		OrchestrationNodeID: util.UUIDToString(in.Node.ID),
		OrchestrationRunID:  util.UUIDToString(runID),
		NodeType:            in.Node.Type,
		Objective:           in.Plan.Objective,
		NodeTitle:           in.Node.Title,
		NodeDescription:     in.Node.Description.String,
		InputContract:       json.RawMessage(in.Node.InputContract),
		OutputContract:      json.RawMessage(in.Node.OutputContract),
		AcceptanceCriteria:  in.AcceptanceCriteria,
		ContextRefs:         in.ContextRefs,
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

func (o *Orchestrator) notifyTaskQueued(ctx context.Context, task db.AgentTaskQueue) {
	if o.TaskSvc == nil {
		return
	}
	o.TaskSvc.broadcastTaskEvent(ctx, "task:queued", task)
	o.TaskSvc.NotifyTaskEnqueued(ctx, task)
}

func (o *Orchestrator) runInTx(ctx context.Context, fn func(*db.Queries) error) error {
	if o.TxStarter == nil {
		return fn(o.Queries)
	}
	tx, err := o.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(o.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}

func nodeDispatchedPayload(taskID, runID string, attemptCount, maxAttempts int32) map[string]any {
	return map[string]any{
		"task_id":       taskID,
		"run_id":        runID,
		"attempt_count": attemptCount,
		"max_attempts":  maxAttempts,
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
