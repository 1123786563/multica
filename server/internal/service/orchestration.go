package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	ErrTemporalUnavailable = errors.New("temporal unavailable")
	ErrForbidden           = errors.New("forbidden")
	ErrInvalidState        = errors.New("invalid orchestration state")
)

const maxOrchestrationNodeAttempts = 2

type WorkflowAlreadyStartedError struct {
	WorkflowID string
	RunID      string
}

func (e WorkflowAlreadyStartedError) Error() string {
	if e.WorkflowID == "" {
		return "workflow already started"
	}
	return fmt.Sprintf("workflow already started: %s", e.WorkflowID)
}

type OrchestrationDB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type TemporalWorkflowStarter interface {
	StartIssueWorkflow(ctx context.Context, input IssueWorkflowStartInput) (TemporalWorkflowStart, error)
}

type AgentTaskOutcomeSignaler interface {
	SignalAgentTaskOutcome(ctx context.Context, input AgentTaskOutcomeSignalInput) error
}

type ApprovalActionSignaler interface {
	SignalApprovalAction(ctx context.Context, input ApprovalActionSignalInput) error
}

type WorkflowCanceler interface {
	CancelWorkflow(ctx context.Context, workflowID string) error
}

type TaskCanceler interface {
	CancelTask(ctx context.Context, taskID pgtype.UUID) (*db.AgentTaskQueue, error)
}

type TaskEnqueueNotifier interface {
	NotifyTaskEnqueued(ctx context.Context, task db.AgentTaskQueue)
}

type IssueWorkflowStartInput struct {
	WorkspaceID string
	IssueID     string
	PlanID      string
	WorkflowID  string
}

type TemporalWorkflowStart struct {
	WorkflowID string
	RunID      string
}

type AgentTaskOutcomeSignalInput struct {
	WorkflowID      string          `json:"workflow_id"`
	PlanID          string          `json:"plan_id"`
	NodeID          string          `json:"node_id"`
	TaskID          string          `json:"task_id"`
	Attempt         int             `json:"attempt"`
	OutcomeVersion  int             `json:"outcome_version"`
	Status          string          `json:"status"`
	Result          json.RawMessage `json:"result,omitempty"`
	ResultReference string          `json:"result_reference,omitempty"`
	Error           string          `json:"error,omitempty"`
}

type ApprovalActionSignalInput struct {
	WorkflowID string `json:"workflow_id"`
	PlanID     string `json:"plan_id"`
	NodeID     string `json:"node_id"`
	ActorID    string `json:"actor_id"`
	ActorType  string `json:"actor_type"`
	Action     string `json:"action"`
	Reason     string `json:"reason"`
}

type OrchestrationService struct {
	DB               OrchestrationDB
	Tx               TxStarter
	Starter          TemporalWorkflowStarter
	ApprovalSignaler ApprovalActionSignaler
	WorkflowCanceler WorkflowCanceler
	TaskCanceler     TaskCanceler
	TaskNotifier     TaskEnqueueNotifier
}

func NewOrchestrationService(db OrchestrationDB, tx TxStarter, starter TemporalWorkflowStarter) *OrchestrationService {
	return &OrchestrationService{DB: db, Tx: tx, Starter: starter}
}

type OrchestrationPlan struct {
	ID                 string                  `json:"id"`
	IssueID            string                  `json:"issue_id"`
	Status             string                  `json:"status"`
	TemporalWorkflowID string                  `json:"temporal_workflow_id,omitempty"`
	TemporalRunID      string                  `json:"temporal_run_id,omitempty"`
	WorkflowType       string                  `json:"workflow_type"`
	ProjectionVersion  int                     `json:"projection_version"`
	CreatedAt          time.Time               `json:"created_at"`
	UpdatedAt          time.Time               `json:"updated_at"`
	Summary            OrchestrationSummary    `json:"summary"`
	AvailableActions   []string                `json:"available_actions"`
	Nodes              []OrchestrationNode     `json:"nodes"`
	Events             []OrchestrationEvent    `json:"events"`
	Artifacts          []OrchestrationArtifact `json:"artifacts"`
}

type OrchestrationSummary struct {
	ReasonCode        string `json:"reason_code"`
	RecommendedAction string `json:"recommended_action"`
}

type OrchestrationNode struct {
	ID                string   `json:"id"`
	NodeKey           string   `json:"node_key"`
	WorkflowNodeKey   string   `json:"workflow_node_key"`
	Title             string   `json:"title"`
	Status            string   `json:"status"`
	ReasonCode        string   `json:"reason_code"`
	RecommendedAction string   `json:"recommended_action"`
	AvailableActions  []string `json:"available_actions"`
	Attempt           int      `json:"attempt"`
}

type OrchestrationEvent struct {
	ID      string         `json:"id"`
	NodeID  string         `json:"node_id,omitempty"`
	Type    string         `json:"type"`
	Source  string         `json:"source"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

type OrchestrationArtifact struct {
	ID     string         `json:"id"`
	NodeID string         `json:"node_id,omitempty"`
	Type   string         `json:"type"`
	Source string         `json:"source"`
	Label  string         `json:"label"`
	URI    string         `json:"uri,omitempty"`
	Data   map[string]any `json:"data"`
}

type OrchestrationSnapshot struct {
	Plans []OrchestrationPlan `json:"plans"`
}

type StartOrchestrationResult struct {
	Plan      OrchestrationPlan `json:"plan"`
	Reused    bool              `json:"reused"`
	Available bool              `json:"available"`
}

type DispatchAgentTaskInput struct {
	PlanID             string
	WorkflowNodeKey    string
	Attempt            int
	TemporalWorkflowID string
	AgentPrompt        string
}

type DispatchAgentTaskResult struct {
	PlanID  string
	TaskID  string
	NodeID  string
	Attempt int
	Reused  bool
}

const OrchestrationTaskContextType = "orchestration_dispatch"

type OrchestrationTaskContext struct {
	Type   string `json:"type"`
	Prompt string `json:"prompt"`
}

type ApprovalActionInput struct {
	NodeID  pgtype.UUID
	ActorID pgtype.UUID
	Action  string
	Reason  string
}

type ApprovalActionResult struct {
	PlanID      string `json:"plan_id"`
	NodeID      string `json:"node_id"`
	Action      string `json:"action"`
	WorkflowID  string `json:"workflow_id"`
	WorkspaceID string `json:"workspace_id"`
}

type PlanApprovalActionInput struct {
	PlanID  pgtype.UUID
	ActorID pgtype.UUID
	Action  string
	Reason  string
}

func (s *OrchestrationService) StartIssue(ctx context.Context, workspaceID, issueID pgtype.UUID) (StartOrchestrationResult, error) {
	if s.Tx == nil || s.DB == nil {
		return StartOrchestrationResult{}, ErrTemporalUnavailable
	}

	workspaceIDStr := util.UUIDToString(workspaceID)
	issueIDStr := util.UUIDToString(issueID)

	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return StartOrchestrationResult{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, issueIDStr); err != nil {
		return StartOrchestrationResult{}, err
	}

	var activeID pgtype.UUID
	var activeStatus, activeWorkflowID string
	var activeUpdatedAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT id, status, COALESCE(temporal_workflow_id, ''), updated_at
		FROM orchestration_plan
		WHERE issue_id = $1 AND status IN ('starting', 'running', 'waiting_human')
		ORDER BY created_at DESC
		LIMIT 1
	`, issueID).Scan(&activeID, &activeStatus, &activeWorkflowID, &activeUpdatedAt); err == nil {
		recentStartingWithWorkflowID := activeStatus == "starting" &&
			activeWorkflowID != "" &&
			activeUpdatedAt.After(time.Now().Add(-30*time.Second))
		if activeStatus != "starting" || recentStartingWithWorkflowID {
			if err := tx.Commit(ctx); err != nil {
				return StartOrchestrationResult{}, err
			}
			plan, err := s.getPlan(ctx, activeID)
			if err != nil {
				return StartOrchestrationResult{}, err
			}
			return StartOrchestrationResult{Plan: plan, Reused: true, Available: true}, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return StartOrchestrationResult{}, err
	}

	planID := activeID
	reusedExisting := activeID.Valid
	if !reusedExisting {
		if err := tx.QueryRow(ctx, `
			INSERT INTO orchestration_plan (
				workspace_id, issue_id, status, reason_code, recommended_action,
				workflow_type, projection_version, last_synced_at, sync_error,
				completed_at
			)
			VALUES ($1, $2, 'starting', '', 'none',
				'issue_mvp', 1, now(), NULL, NULL)
			RETURNING id
		`, workspaceID, issueID).Scan(&planID); err != nil {
			return StartOrchestrationResult{}, err
		}
	}

	workflowID := fmt.Sprintf("multica/%s/issue/%s/run/%s", workspaceIDStr, issueIDStr, util.UUIDToString(planID))
	if s.Starter != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE orchestration_plan
			SET temporal_workflow_id = $2,
				updated_at = now()
			WHERE id = $1
		`, planID, workflowID); err != nil {
			return StartOrchestrationResult{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return StartOrchestrationResult{}, err
	}

	if s.Starter == nil {
		plan, err := s.markPlanFailed(ctx, planID, "Temporal is not configured")
		if err != nil {
			return StartOrchestrationResult{}, err
		}
		return StartOrchestrationResult{Plan: plan, Reused: reusedExisting, Available: false}, nil
	}

	start, err := s.Starter.StartIssueWorkflow(ctx, IssueWorkflowStartInput{
		WorkspaceID: workspaceIDStr,
		IssueID:     issueIDStr,
		PlanID:      util.UUIDToString(planID),
		WorkflowID:  workflowID,
	})
	if err != nil {
		var alreadyStarted WorkflowAlreadyStartedError
		if errors.As(err, &alreadyStarted) {
			repairedWorkflowID := alreadyStarted.WorkflowID
			if repairedWorkflowID == "" {
				repairedWorkflowID = workflowID
			}
			plan, repairErr := s.markPlanRunning(ctx, planID, repairedWorkflowID, alreadyStarted.RunID)
			if repairErr != nil {
				return StartOrchestrationResult{}, repairErr
			}
			return StartOrchestrationResult{Plan: plan, Reused: reusedExisting, Available: true}, nil
		}
		plan, updateErr := s.markPlanFailed(ctx, planID, err.Error())
		if updateErr != nil {
			return StartOrchestrationResult{}, updateErr
		}
		return StartOrchestrationResult{Plan: plan, Reused: reusedExisting, Available: false}, nil
	}

	plan, err := s.markPlanRunning(ctx, planID, start.WorkflowID, start.RunID)
	if err != nil {
		return StartOrchestrationResult{}, err
	}
	return StartOrchestrationResult{Plan: plan, Reused: reusedExisting, Available: true}, nil
}

func (s *OrchestrationService) ApplyApprovalAction(ctx context.Context, input ApprovalActionInput) (ApprovalActionResult, error) {
	if s.Tx == nil || s.DB == nil || s.ApprovalSignaler == nil {
		return ApprovalActionResult{}, ErrTemporalUnavailable
	}
	if input.Action != "approve" && input.Action != "retry" {
		return ApprovalActionResult{}, fmt.Errorf("unsupported approval action: %s", input.Action)
	}

	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return ApprovalActionResult{}, err
	}
	defer tx.Rollback(ctx)

	var planID, issueID, workspaceID pgtype.UUID
	var workflowID, nodeStatus string
	var nodeAttempt int
	if err := tx.QueryRow(ctx, `
		SELECT p.id, p.issue_id, p.workspace_id, COALESCE(p.temporal_workflow_id, ''), n.status, n.attempt
		FROM orchestration_node n
		JOIN orchestration_plan p ON p.id = n.plan_id
		WHERE n.id = $1
		FOR UPDATE OF n, p
	`, input.NodeID).Scan(&planID, &issueID, &workspaceID, &workflowID, &nodeStatus, &nodeAttempt); err != nil {
		return ApprovalActionResult{}, fmt.Errorf("load approval node: %w", err)
	}
	if workflowID == "" {
		return ApprovalActionResult{}, ErrTemporalUnavailable
	}
	if nodeStatus != "waiting_human" {
		return ApprovalActionResult{}, ErrInvalidState
	}
	if input.Action == "retry" && nodeAttempt >= maxOrchestrationNodeAttempts {
		return ApprovalActionResult{}, ErrInvalidState
	}

	allowed, err := s.authorizedHumanApprovalActor(ctx, tx, workspaceID, issueID, input.ActorID)
	if err != nil {
		return ApprovalActionResult{}, err
	}
	if !allowed {
		return ApprovalActionResult{}, ErrForbidden
	}

	details, err := json.Marshal(map[string]any{
		"actor_id":   util.UUIDToString(input.ActorID),
		"actor_type": "human",
		"action":     input.Action,
		"reason":     input.Reason,
		"plan_id":    util.UUIDToString(planID),
		"node_id":    util.UUIDToString(input.NodeID),
	})
	if err != nil {
		return ApprovalActionResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO orchestration_event (plan_id, node_id, type, source, message, details)
		VALUES ($1, $2, $3, 'server', $4, $5::jsonb)
	`, planID, input.NodeID, "approval."+input.Action, "Approval action recorded", details); err != nil {
		return ApprovalActionResult{}, fmt.Errorf("write approval audit event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ApprovalActionResult{}, err
	}

	result := ApprovalActionResult{
		PlanID:      util.UUIDToString(planID),
		NodeID:      util.UUIDToString(input.NodeID),
		Action:      input.Action,
		WorkflowID:  workflowID,
		WorkspaceID: util.UUIDToString(workspaceID),
	}
	if err := s.ApprovalSignaler.SignalApprovalAction(ctx, ApprovalActionSignalInput{
		WorkflowID: result.WorkflowID,
		PlanID:     result.PlanID,
		NodeID:     result.NodeID,
		ActorID:    util.UUIDToString(input.ActorID),
		ActorType:  "human",
		Action:     input.Action,
		Reason:     input.Reason,
	}); err != nil {
		return ApprovalActionResult{}, err
	}
	return result, nil
}

func (s *OrchestrationService) ApplyPlanApprovalAction(ctx context.Context, input PlanApprovalActionInput) (ApprovalActionResult, error) {
	if s.Tx == nil || s.DB == nil || s.WorkflowCanceler == nil {
		return ApprovalActionResult{}, ErrTemporalUnavailable
	}
	if input.Action != "cancel" {
		return ApprovalActionResult{}, fmt.Errorf("unsupported plan approval action: %s", input.Action)
	}

	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return ApprovalActionResult{}, err
	}
	defer tx.Rollback(ctx)

	var issueID, workspaceID pgtype.UUID
	var workflowID, planStatus string
	if err := tx.QueryRow(ctx, `
		SELECT issue_id, workspace_id, status, COALESCE(temporal_workflow_id, '')
		FROM orchestration_plan
		WHERE id = $1
		FOR UPDATE
	`, input.PlanID).Scan(&issueID, &workspaceID, &planStatus, &workflowID); err != nil {
		return ApprovalActionResult{}, fmt.Errorf("load approval plan: %w", err)
	}
	if workflowID == "" {
		return ApprovalActionResult{}, ErrTemporalUnavailable
	}

	allowed, err := s.authorizedHumanApprovalActor(ctx, tx, workspaceID, issueID, input.ActorID)
	if err != nil {
		return ApprovalActionResult{}, err
	}
	if !allowed {
		return ApprovalActionResult{}, ErrForbidden
	}
	alreadyCancelled := planStatus == "cancelled"
	if !alreadyCancelled && planStatus != "starting" && planStatus != "running" && planStatus != "waiting_human" {
		return ApprovalActionResult{}, ErrInvalidState
	}

	if !alreadyCancelled {
		details, err := json.Marshal(map[string]any{
			"actor_id":   util.UUIDToString(input.ActorID),
			"actor_type": "human",
			"action":     input.Action,
			"reason":     input.Reason,
			"plan_id":    util.UUIDToString(input.PlanID),
		})
		if err != nil {
			return ApprovalActionResult{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO orchestration_event (plan_id, type, source, message, details)
			SELECT $1, 'approval.cancel', 'server', $2, $3::jsonb
			WHERE NOT EXISTS (
				SELECT 1
				FROM orchestration_event
				WHERE plan_id = $1 AND type = 'approval.cancel'
			)
		`, input.PlanID, "Approval action recorded", details); err != nil {
			return ApprovalActionResult{}, fmt.Errorf("write approval audit event: %w", err)
		}
	}
	taskRows, err := tx.Query(ctx, `
		SELECT id
		FROM agent_task_queue
		WHERE orchestration_plan_id = $1
			AND status IN ('queued', 'dispatched', 'running')
	`, input.PlanID)
	if err != nil {
		return ApprovalActionResult{}, fmt.Errorf("list orchestration tasks to cancel: %w", err)
	}
	var taskIDs []pgtype.UUID
	for taskRows.Next() {
		var taskID pgtype.UUID
		if err := taskRows.Scan(&taskID); err != nil {
			taskRows.Close()
			return ApprovalActionResult{}, err
		}
		taskIDs = append(taskIDs, taskID)
	}
	if err := taskRows.Err(); err != nil {
		taskRows.Close()
		return ApprovalActionResult{}, err
	}
	taskRows.Close()

	if err := tx.Commit(ctx); err != nil {
		return ApprovalActionResult{}, err
	}

	result := ApprovalActionResult{
		PlanID:      util.UUIDToString(input.PlanID),
		Action:      input.Action,
		WorkflowID:  workflowID,
		WorkspaceID: util.UUIDToString(workspaceID),
	}
	if !alreadyCancelled {
		if err := s.WorkflowCanceler.CancelWorkflow(ctx, result.WorkflowID); err != nil {
			return ApprovalActionResult{}, err
		}
	}

	if s.TaskCanceler != nil {
		for _, taskID := range taskIDs {
			if _, err := s.TaskCanceler.CancelTask(ctx, taskID); err != nil {
				return ApprovalActionResult{}, fmt.Errorf("cancel orchestration task: %w", err)
			}
		}
	}
	if !alreadyCancelled {
		if _, err := s.DB.Exec(ctx, `
			UPDATE orchestration_plan
			SET status = 'cancelled',
				reason_code = 'human_cancelled',
				recommended_action = 'none',
				completed_at = COALESCE(completed_at, now()),
				updated_at = now()
			WHERE id = $1
				AND status IN ('starting', 'running', 'waiting_human')
		`, input.PlanID); err != nil {
			return ApprovalActionResult{}, fmt.Errorf("cancel orchestration plan: %w", err)
		}
		if _, err := s.DB.Exec(ctx, `
			UPDATE orchestration_node
			SET status = 'cancelled',
				completed_at = COALESCE(completed_at, now()),
				updated_at = now()
			WHERE plan_id = $1 AND status IN ('pending', 'running', 'waiting_human')
		`, input.PlanID); err != nil {
			return ApprovalActionResult{}, fmt.Errorf("cancel orchestration nodes: %w", err)
		}
	}
	return result, nil
}

func (s *OrchestrationService) authorizedHumanApprovalActor(ctx context.Context, tx OrchestrationDB, workspaceID, issueID, actorID pgtype.UUID) (bool, error) {
	var role string
	if err := tx.QueryRow(ctx, `
		SELECT role
		FROM member
		WHERE workspace_id = $1 AND user_id = $2
	`, workspaceID, actorID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if role == "owner" || role == "admin" {
		return true, nil
	}

	var creatorType string
	var creatorID pgtype.UUID
	var assigneeType pgtype.Text
	var assigneeID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT creator_type, creator_id, assignee_type, assignee_id
		FROM issue
		WHERE id = $1
	`, issueID).Scan(&creatorType, &creatorID, &assigneeType, &assigneeID); err != nil {
		return false, err
	}
	if creatorType == "member" && creatorID == actorID {
		return true, nil
	}
	return assigneeType.Valid && assigneeType.String == "member" && assigneeID == actorID, nil
}

func (s *OrchestrationService) DispatchAgentTask(ctx context.Context, input DispatchAgentTaskInput) (DispatchAgentTaskResult, error) {
	if s.Tx == nil || s.DB == nil {
		return DispatchAgentTaskResult{}, ErrTemporalUnavailable
	}
	if input.Attempt <= 0 {
		input.Attempt = 1
	}
	planID, err := util.ParseUUID(input.PlanID)
	if err != nil {
		return DispatchAgentTaskResult{}, fmt.Errorf("invalid plan id: %w", err)
	}
	nodeKey := input.WorkflowNodeKey
	if nodeKey == "" {
		nodeKey = "dispatch"
	}

	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return DispatchAgentTaskResult{}, err
	}
	defer tx.Rollback(ctx)

	var issueID pgtype.UUID
	var planWorkflowID string
	if err := tx.QueryRow(ctx, `
		SELECT issue_id, COALESCE(temporal_workflow_id, '')
		FROM orchestration_plan
		WHERE id = $1
		FOR UPDATE
	`, planID).Scan(&issueID, &planWorkflowID); err != nil {
		return DispatchAgentTaskResult{}, fmt.Errorf("load orchestration plan: %w", err)
	}
	if input.TemporalWorkflowID != "" && planWorkflowID != "" && input.TemporalWorkflowID != planWorkflowID {
		return DispatchAgentTaskResult{}, fmt.Errorf("workflow id mismatch")
	}
	if input.TemporalWorkflowID == "" {
		input.TemporalWorkflowID = planWorkflowID
	}

	var nodeID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orchestration_node (
			plan_id, workflow_node_key, title, status, attempt, started_at
		)
		VALUES ($1, $2, $3, 'running', $4, now())
		ON CONFLICT (plan_id, workflow_node_key, attempt) WHERE plan_id IS NOT NULL
		DO UPDATE SET
			status = CASE
				WHEN orchestration_node.status IN ('pending', 'running') THEN 'running'
				ELSE orchestration_node.status
			END,
			started_at = COALESCE(orchestration_node.started_at, now()),
			updated_at = now()
		RETURNING id
	`, planID, nodeKey, orchestrationNodeTitle(nodeKey), input.Attempt).Scan(&nodeID); err != nil {
		return DispatchAgentTaskResult{}, fmt.Errorf("upsert orchestration node: %w", err)
	}

	var existingTaskID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id
		FROM agent_task_queue
		WHERE orchestration_plan_id = $1
			AND orchestration_node_id = $2
			AND orchestration_attempt = $3
		ORDER BY created_at ASC
		LIMIT 1
	`, planID, nodeID, input.Attempt).Scan(&existingTaskID); err == nil {
		if err := tx.Commit(ctx); err != nil {
			return DispatchAgentTaskResult{}, err
		}
		return DispatchAgentTaskResult{
			PlanID:  util.UUIDToString(planID),
			TaskID:  util.UUIDToString(existingTaskID),
			NodeID:  util.UUIDToString(nodeID),
			Attempt: input.Attempt,
			Reused:  true,
		}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return DispatchAgentTaskResult{}, fmt.Errorf("find existing orchestration task: %w", err)
	}

	var agentID, runtimeID pgtype.UUID
	var priority string
	if err := tx.QueryRow(ctx, `
		SELECT i.assignee_id, i.priority, a.runtime_id
		FROM issue i
		JOIN agent a ON a.id = i.assignee_id
		WHERE i.id = $1
			AND i.assignee_type = 'agent'
			AND i.assignee_id IS NOT NULL
			AND a.archived_at IS NULL
			AND a.runtime_id IS NOT NULL
	`, issueID).Scan(&agentID, &priority, &runtimeID); err != nil {
		return DispatchAgentTaskResult{}, fmt.Errorf("load dispatch agent: %w", err)
	}

	var taskContext []byte
	if prompt := strings.TrimSpace(input.AgentPrompt); prompt != "" {
		taskContext, err = json.Marshal(OrchestrationTaskContext{
			Type:   OrchestrationTaskContextType,
			Prompt: prompt,
		})
		if err != nil {
			return DispatchAgentTaskResult{}, err
		}
	}

	var taskID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			orchestration_plan_id, orchestration_node_id, orchestration_attempt,
			temporal_workflow_id, context
		)
		VALUES ($1, $2, $3, 'queued', $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, agentID, runtimeID, issueID, priorityToInt(priority), planID, nodeID, input.Attempt, input.TemporalWorkflowID, taskContext).Scan(&taskID); err != nil {
		return DispatchAgentTaskResult{}, fmt.Errorf("create orchestration task: %w", err)
	}

	createdTask, err := db.New(tx).GetAgentTask(ctx, taskID)
	if err != nil {
		return DispatchAgentTaskResult{}, fmt.Errorf("load created orchestration task: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE orchestration_node
		SET agent_task_id = $2,
			status = 'running',
			started_at = COALESCE(started_at, now()),
			updated_at = now()
		WHERE id = $1
	`, nodeID, taskID); err != nil {
		return DispatchAgentTaskResult{}, fmt.Errorf("link orchestration node task: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return DispatchAgentTaskResult{}, err
	}
	if s.TaskNotifier != nil {
		s.TaskNotifier.NotifyTaskEnqueued(ctx, createdTask)
	}
	return DispatchAgentTaskResult{
		PlanID:  util.UUIDToString(planID),
		TaskID:  util.UUIDToString(taskID),
		NodeID:  util.UUIDToString(nodeID),
		Attempt: input.Attempt,
		Reused:  false,
	}, nil
}

func (s *OrchestrationService) markPlanRunning(ctx context.Context, planID pgtype.UUID, workflowID, runID string) (OrchestrationPlan, error) {
	if _, err := s.DB.Exec(ctx, `
		UPDATE orchestration_plan
		SET status = 'running',
			reason_code = '',
			recommended_action = 'none',
			temporal_workflow_id = $2,
			temporal_run_id = $3,
			started_at = COALESCE(started_at, now()),
			completed_at = NULL,
			sync_error = NULL,
			updated_at = now()
		WHERE id = $1
	`, planID, workflowID, runID); err != nil {
		return OrchestrationPlan{}, err
	}
	return s.getPlan(ctx, planID)
}

func (s *OrchestrationService) markPlanFailed(ctx context.Context, planID pgtype.UUID, syncError string) (OrchestrationPlan, error) {
	if _, err := s.DB.Exec(ctx, `
		UPDATE orchestration_plan
		SET status = 'failed',
			reason_code = 'temporal_unavailable',
			recommended_action = 'configure_temporal',
			last_synced_at = now(),
			sync_error = $2,
			completed_at = now(),
			updated_at = now()
		WHERE id = $1
	`, planID, syncError); err != nil {
		return OrchestrationPlan{}, err
	}
	if err := CreateOrchestrationAttention(ctx, s.DB, planID, "temporal_unavailable", "Temporal unavailable: configure Temporal before retrying orchestration."); err != nil {
		return OrchestrationPlan{}, err
	}
	plan, err := s.getPlan(ctx, planID)
	if err != nil {
		return OrchestrationPlan{}, err
	}
	return plan, nil
}

func CreateOrchestrationAttention(ctx context.Context, store OrchestrationDB, planID pgtype.UUID, dedupeKey, message string) error {
	if store == nil {
		return nil
	}
	dedupe := fmt.Sprintf("orchestration_attention:%s:%s", util.UUIDToString(planID), dedupeKey)
	details, err := json.Marshal(map[string]any{
		"dedupe_key": dedupe,
		"plan_id":    util.UUIDToString(planID),
		"reason":     dedupeKey,
	})
	if err != nil {
		return err
	}

	commentMessage := fmt.Sprintf("%s\n\nPlan: %s", message, util.UUIDToString(planID))
	if _, err := store.Exec(ctx, `
		WITH plan_issue AS (
			SELECT p.workspace_id, p.issue_id, i.title, i.creator_type, i.creator_id, i.assignee_type, i.assignee_id
			FROM orchestration_plan p
			JOIN issue i ON i.id = p.issue_id
			WHERE p.id = $1
		),
		author AS (
			SELECT creator_id AS id
			FROM plan_issue
			WHERE creator_type = 'member'
			UNION ALL
			SELECT assignee_id AS id
			FROM plan_issue
			WHERE assignee_type = 'member' AND assignee_id IS NOT NULL
			UNION ALL
			SELECT s.user_id AS id
			FROM issue_subscriber s
			JOIN plan_issue pi ON pi.issue_id = s.issue_id
			WHERE s.user_type = 'member'
			LIMIT 1
		)
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		SELECT pi.issue_id, pi.workspace_id, 'member', author.id, $2, 'system'
		FROM plan_issue pi
		JOIN author ON TRUE
		WHERE NOT EXISTS (
			SELECT 1
			FROM comment c
			WHERE c.issue_id = pi.issue_id
				AND c.type = 'system'
				AND c.content = $2
		)
	`, planID, commentMessage); err != nil {
		return err
	}

	if _, err := store.Exec(ctx, `
		WITH plan_issue AS (
			SELECT p.workspace_id, p.issue_id, i.title, i.creator_type, i.creator_id, i.assignee_type, i.assignee_id
			FROM orchestration_plan p
			JOIN issue i ON i.id = p.issue_id
			WHERE p.id = $1
		),
		audience AS (
			SELECT creator_id AS user_id
			FROM plan_issue
			WHERE creator_type = 'member'
			UNION
			SELECT assignee_id AS user_id
			FROM plan_issue
			WHERE assignee_type = 'member' AND assignee_id IS NOT NULL
			UNION
			SELECT s.user_id
			FROM issue_subscriber s
			JOIN plan_issue pi ON pi.issue_id = s.issue_id
			WHERE s.user_type = 'member'
		)
		INSERT INTO inbox_item (
			workspace_id, recipient_type, recipient_id,
			type, severity, issue_id, title, body,
			actor_type, actor_id, details
		)
		SELECT
			pi.workspace_id, 'member', audience.user_id,
			'orchestration_attention', 'attention', pi.issue_id,
			'Orchestration needs attention', $2,
			'system', NULL, $3::jsonb
		FROM plan_issue pi
		JOIN audience ON audience.user_id IS NOT NULL
		WHERE NOT EXISTS (
			SELECT 1
			FROM inbox_item existing
			WHERE existing.issue_id = pi.issue_id
				AND existing.recipient_type = 'member'
				AND existing.recipient_id = audience.user_id
				AND existing.type = 'orchestration_attention'
				AND existing.details->>'dedupe_key' = $4
		)
	`, planID, message, details, dedupe); err != nil {
		return err
	}
	return nil
}

func (s *OrchestrationService) Snapshot(ctx context.Context, issueID pgtype.UUID) (OrchestrationSnapshot, error) {
	return s.snapshot(ctx, issueID, false)
}

func (s *OrchestrationService) SnapshotForActor(ctx context.Context, issueID pgtype.UUID, actorType string, actorID pgtype.UUID) (OrchestrationSnapshot, error) {
	canAct := false
	if actorType == "member" && actorID.Valid {
		var workspaceID pgtype.UUID
		if err := s.DB.QueryRow(ctx, `SELECT workspace_id FROM issue WHERE id = $1`, issueID).Scan(&workspaceID); err != nil {
			return OrchestrationSnapshot{}, err
		}
		allowed, err := s.authorizedHumanApprovalActor(ctx, s.DB, workspaceID, issueID, actorID)
		if err != nil {
			return OrchestrationSnapshot{}, err
		}
		canAct = allowed
	}
	return s.snapshot(ctx, issueID, canAct)
}

func (s *OrchestrationService) snapshot(ctx context.Context, issueID pgtype.UUID, canAct bool) (OrchestrationSnapshot, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id
		FROM orchestration_plan
		WHERE issue_id = $1
		ORDER BY created_at DESC
	`, issueID)
	if err != nil {
		return OrchestrationSnapshot{}, err
	}
	defer rows.Close()

	plans := make([]OrchestrationPlan, 0)
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return OrchestrationSnapshot{}, err
		}
		plan, err := s.getPlanWithActions(ctx, id, canAct)
		if err != nil {
			return OrchestrationSnapshot{}, err
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return OrchestrationSnapshot{}, err
	}
	return OrchestrationSnapshot{Plans: plans}, nil
}

func (s *OrchestrationService) getPlan(ctx context.Context, planID pgtype.UUID) (OrchestrationPlan, error) {
	return s.getPlanWithActions(ctx, planID, false)
}

func (s *OrchestrationService) getPlanWithActions(ctx context.Context, planID pgtype.UUID, canAct bool) (OrchestrationPlan, error) {
	var plan OrchestrationPlan
	var id, issueID pgtype.UUID
	if err := s.DB.QueryRow(ctx, `
		SELECT id, issue_id, status, reason_code, recommended_action,
			COALESCE(temporal_workflow_id, ''), COALESCE(temporal_run_id, ''),
			workflow_type, projection_version, created_at, updated_at
		FROM orchestration_plan
		WHERE id = $1
	`, planID).Scan(
		&id,
		&issueID,
		&plan.Status,
		&plan.Summary.ReasonCode,
		&plan.Summary.RecommendedAction,
		&plan.TemporalWorkflowID,
		&plan.TemporalRunID,
		&plan.WorkflowType,
		&plan.ProjectionVersion,
		&plan.CreatedAt,
		&plan.UpdatedAt,
	); err != nil {
		return OrchestrationPlan{}, err
	}
	plan.ID = util.UUIDToString(id)
	plan.IssueID = util.UUIDToString(issueID)
	plan.AvailableActions = availablePlanActions(plan, canAct)
	plan.Nodes = defaultIssueWorkflowNodes(plan.Status)
	if nodes, err := s.projectedNodes(ctx, planID, plan.Nodes, canAct); err == nil {
		plan.Nodes = nodes
	} else {
		return OrchestrationPlan{}, err
	}
	if events, err := s.projectedEvents(ctx, planID); err == nil {
		plan.Events = events
	} else {
		return OrchestrationPlan{}, err
	}
	if artifacts, err := s.projectedArtifacts(ctx, planID); err == nil {
		plan.Artifacts = artifacts
	} else {
		return OrchestrationPlan{}, err
	}
	return plan, nil
}

func (s *OrchestrationService) projectedEvents(ctx context.Context, planID pgtype.UUID) ([]OrchestrationEvent, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, node_id, type, source, message, details
		FROM orchestration_event
		WHERE plan_id = $1
		ORDER BY created_at ASC
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []OrchestrationEvent{}
	for rows.Next() {
		var event OrchestrationEvent
		var id pgtype.UUID
		var nodeID pgtype.UUID
		var details []byte
		if err := rows.Scan(&id, &nodeID, &event.Type, &event.Source, &event.Message, &details); err != nil {
			return nil, err
		}
		event.ID = util.UUIDToString(id)
		if nodeID.Valid {
			event.NodeID = util.UUIDToString(nodeID)
		}
		if len(details) > 0 {
			if err := json.Unmarshal(details, &event.Details); err != nil {
				return nil, err
			}
		}
		if event.Details == nil {
			event.Details = map[string]any{}
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *OrchestrationService) projectedArtifacts(ctx context.Context, planID pgtype.UUID) ([]OrchestrationArtifact, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, node_id, type, source, label, uri, data
		FROM orchestration_artifact
		WHERE plan_id = $1
		ORDER BY created_at ASC
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	artifacts := []OrchestrationArtifact{}
	for rows.Next() {
		var artifact OrchestrationArtifact
		var id pgtype.UUID
		var nodeID pgtype.UUID
		var uri pgtype.Text
		var data []byte
		if err := rows.Scan(&id, &nodeID, &artifact.Type, &artifact.Source, &artifact.Label, &uri, &data); err != nil {
			return nil, err
		}
		artifact.ID = util.UUIDToString(id)
		if nodeID.Valid {
			artifact.NodeID = util.UUIDToString(nodeID)
		}
		if uri.Valid {
			artifact.URI = uri.String
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &artifact.Data); err != nil {
				return nil, err
			}
		}
		if artifact.Data == nil {
			artifact.Data = map[string]any{}
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return artifacts, nil
}

func (s *OrchestrationService) projectedNodes(ctx context.Context, planID pgtype.UUID, defaults []OrchestrationNode, canAct bool) ([]OrchestrationNode, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, workflow_node_key, title, status, reason_code, recommended_action, attempt
		FROM orchestration_node
		WHERE plan_id = $1
		ORDER BY created_at ASC
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	nodes := make([]OrchestrationNode, len(defaults))
	copy(nodes, defaults)
	byKey := make(map[string]int, len(nodes))
	replaced := make(map[string]bool, len(nodes))
	for i := range nodes {
		byKey[nodes[i].WorkflowNodeKey] = i
	}
	sawProjectedNode := false
	for rows.Next() {
		var node OrchestrationNode
		var id pgtype.UUID
		if err := rows.Scan(
			&id,
			&node.WorkflowNodeKey,
			&node.Title,
			&node.Status,
			&node.ReasonCode,
			&node.RecommendedAction,
			&node.Attempt,
		); err != nil {
			return nil, err
		}
		node.ID = util.UUIDToString(id)
		node.NodeKey = node.WorkflowNodeKey
		sawProjectedNode = true
		if idx, ok := byKey[node.WorkflowNodeKey]; ok && !replaced[node.WorkflowNodeKey] {
			if node.Title == "" {
				node.Title = nodes[idx].Title
			}
			node.AvailableActions = availableNodeActions(node, canAct)
			nodes[idx] = node
			replaced[node.WorkflowNodeKey] = true
			continue
		}
		if node.Title == "" {
			node.Title = orchestrationNodeTitle(node.WorkflowNodeKey)
		}
		node.AvailableActions = availableNodeActions(node, canAct)
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if sawProjectedNode && len(nodes) > 0 && nodes[0].ID == "analyze" && nodes[0].ReasonCode == "temporal_unavailable" {
		nodes[0].Status = "pending"
		nodes[0].ReasonCode = ""
		nodes[0].RecommendedAction = "none"
	}
	return nodes, nil
}

func availablePlanActions(plan OrchestrationPlan, canAct bool) []string {
	if !canAct {
		return []string{}
	}
	switch plan.Status {
	case "starting", "running", "waiting_human":
		return []string{"cancel"}
	default:
		return []string{}
	}
}

func availableNodeActions(node OrchestrationNode, canAct bool) []string {
	if !canAct || node.Status != "waiting_human" {
		return []string{}
	}
	actions := []string{"approve"}
	if node.Attempt < maxOrchestrationNodeAttempts {
		actions = append(actions, "retry")
	}
	return actions
}

func defaultIssueWorkflowNodes(planStatus string) []OrchestrationNode {
	nodes := []OrchestrationNode{
		{ID: "analyze", NodeKey: "analyze", WorkflowNodeKey: "analyze", Title: "Analyze", Status: "pending", RecommendedAction: "none", Attempt: 1},
		{ID: "dispatch", NodeKey: "dispatch", WorkflowNodeKey: "dispatch", Title: "Dispatch agent task", Status: "pending", RecommendedAction: "none", Attempt: 1},
		{ID: "validate", NodeKey: "validate", WorkflowNodeKey: "validate", Title: "Validate result", Status: "pending", RecommendedAction: "none", Attempt: 1},
		{ID: "review", NodeKey: "review", WorkflowNodeKey: "review", Title: "Review handoff", Status: "pending", RecommendedAction: "none", Attempt: 1},
	}
	switch planStatus {
	case "completed":
		for i := range nodes {
			nodes[i].Status = "completed"
		}
	case "failed":
		nodes[0].Status = "failed"
		nodes[0].ReasonCode = "temporal_unavailable"
		nodes[0].RecommendedAction = "configure_temporal"
	case "cancelled":
		nodes[0].Status = "cancelled"
	}
	return nodes
}

func orchestrationNodeTitle(nodeKey string) string {
	switch nodeKey {
	case "analyze":
		return "Analyze"
	case "dispatch":
		return "Dispatch agent task"
	case "validate":
		return "Validate result"
	case "review":
		return "Review handoff"
	default:
		return nodeKey
	}
}
