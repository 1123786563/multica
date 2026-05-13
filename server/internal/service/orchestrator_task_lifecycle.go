package service

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

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

func (o *Orchestrator) OnTaskFailed(ctx context.Context, task db.AgentTaskQueue, failureReason string) error {
	var queuedTasks []db.AgentTaskQueue
	if err := o.runInTx(ctx, func(qtx *db.Queries) error {
		var err error
		queuedTasks, err = o.OnTaskFailedTx(ctx, qtx, task, failureReason)
		return err
	}); err != nil {
		return err
	}
	for _, task := range queuedTasks {
		o.notifyTaskQueued(ctx, task)
	}
	return nil
}

func (o *Orchestrator) OnTaskFailedTx(ctx context.Context, qtx *db.Queries, task db.AgentTaskQueue, failureReason string) ([]db.AgentTaskQueue, error) {
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
	plan, err := qtx.GetOrchestrationPlan(ctx, planID)
	if err != nil {
		return nil, err
	}
	node, err := qtx.GetOrchestrationNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	payload := mustJSON(map[string]any{
		"reason":         failureReason,
		"task_id":        util.UUIDToString(task.ID),
		"attempt":        node.AttemptCount,
		"max_attempts":   node.MaxAttempts,
		"failure_reason": failureReason,
	})
	if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
		PlanID:    planID,
		NodeID:    nodeID,
		TaskID:    task.ID,
		EventType: "task.failed",
		ActorType: "agent",
		ActorID:   task.AgentID,
		Payload:   payload,
	}); err != nil {
		return nil, err
	}

	o.Logger.Warn("orchestration: task failed",
		"task_id", util.UUIDToString(task.ID),
		"plan_id", util.UUIDToString(planID),
		"node_id", util.UUIDToString(nodeID),
		"attempt", node.AttemptCount,
		"max_attempts", node.MaxAttempts,
		"failure_reason", failureReason,
		"component", "orchestrator",
	)

	if node.AttemptCount < node.MaxAttempts {
		if err := qtx.ReadyOrchestrationNode(ctx, nodeID); err != nil {
			return nil, err
		}
		agent := db.Agent{ID: task.AgentID, RuntimeID: task.RuntimeID}
		next, err := o.dispatchNodeTask(ctx, qtx, dispatchNodeInput{
			Plan:               plan,
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
		if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
			PlanID:    planID,
			NodeID:    nodeID,
			TaskID:    next.ID,
			EventType: "node.retry_scheduled",
			ActorType: "kernel",
			Payload: mustJSON(map[string]any{
				"reason":           failureReason,
				"previous_task_id": util.UUIDToString(task.ID),
				"next_task_id":     util.UUIDToString(next.ID),
			}),
		}); err != nil {
			return nil, err
		}
		return []db.AgentTaskQueue{next}, nil
	}

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
	if _, err := qtx.CreateOrchestrationEvent(ctx, db.CreateOrchestrationEventParams{
		PlanID:    planID,
		NodeID:    nodeID,
		TaskID:    task.ID,
		EventType: "plan.failed",
		ActorType: "kernel",
		Payload:   payload,
	}); err != nil {
		return nil, err
	}
	return nil, nil
}

func (o *Orchestrator) RecoverPlan(ctx context.Context, planID pgtype.UUID) error {
	plan, err := o.Queries.GetOrchestrationPlan(ctx, planID)
	if err != nil {
		return err
	}
	nodes, err := o.Queries.ListOrchestrationNodesByPlan(ctx, planID)
	if err != nil {
		return err
	}
	nodeByID := make(map[string]db.OrchestrationNode, len(nodes))
	for _, node := range nodes {
		nodeByID[util.UUIDToString(node.ID)] = node
	}
	tasks, err := o.Queries.ListOrchestrationTasksByPlan(ctx, planID)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		taskCtx, ok := ParseOrchestrationTaskContext(task.Context)
		if !ok {
			continue
		}
		node := nodeByID[taskCtx.OrchestrationNodeID]
		if orchestrationNodeTerminal(node.Status) {
			continue
		}
		switch task.Status {
		case "completed":
			if err := o.OnTaskCompleted(ctx, task, task.Result); err != nil {
				return err
			}
		case "failed":
			failureReason := task.FailureReason.String
			if failureReason == "" {
				failureReason = "recovery_failed_task"
			}
			if err := o.OnTaskFailed(ctx, task, failureReason); err != nil {
				return err
			}
		}
	}
	o.publishOrchestrationUpdated(ctx, plan)
	return nil
}

func orchestrationNodeTerminal(status string) bool {
	switch status {
	case "completed", "failed", "cancelled", "skipped", "waiting_human":
		return true
	default:
		return false
	}
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
	if task.IssueID.Valid {
		o.publishOrchestrationUpdatedFromIssue(ctx, task.IssueID)
	}
	o.createAttentionCommentIfNeeded(ctx, task)
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
	validation := ParseAgentResultPayload(rawResult, ResultParseOptions{})
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
		next, err := o.dispatchReadyNodes(ctx, qtx, plan, task.IssueID, task.Priority, taskCtx.AcceptanceCriteria, taskCtx.ContextRefs, false)
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
			if _, err := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: task.IssueID, Status: "in_review"}); err != nil {
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
			Plan:                 db.OrchestrationPlan{ID: planID, Objective: taskCtx.Objective},
			Node:                 node,
			Agent:                agent,
			IssueID:              task.IssueID,
			Priority:             task.Priority,
			AcceptanceCriteria:   taskCtx.AcceptanceCriteria,
			ContextRefs:          taskCtx.ContextRefs,
			PriorEvidenceSummary: buildRetryEvidenceSummary(result, validation, eval),
			ChangeRequest:        buildRetryChangeRequest(eval),
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
				"previous_task_id":       util.UUIDToString(task.ID),
				"next_task_id":           util.UUIDToString(next.ID),
				"reason":                 reason,
				"prior_evidence_summary": buildRetryEvidenceSummary(result, validation, eval),
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
