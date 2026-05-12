package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type OrchestrationService struct {
	DB TxStarter
}

func NewOrchestrationService(db TxStarter) *OrchestrationService {
	return &OrchestrationService{DB: db}
}

type OrchestrationRun struct {
	ID            pgtype.UUID
	WorkspaceID   pgtype.UUID
	IssueID       pgtype.UUID
	Status        string
	Source        string
	PlanVersion   int32
	CreatedByType pgtype.Text
	CreatedByID   pgtype.UUID
	CreatedAt     pgtype.Timestamptz
	UpdatedAt     pgtype.Timestamptz
}

type OrchestrationNode struct {
	ID           pgtype.UUID
	RunID        pgtype.UUID
	WorkspaceID  pgtype.UUID
	IssueID      pgtype.UUID
	Key          string
	Kind         string
	Status       string
	Position     int32
	Dependencies []string
	AgentTaskID  pgtype.UUID
	Attempt      int32
	Metadata     json.RawMessage
	StartedAt    pgtype.Timestamptz
	CompletedAt  pgtype.Timestamptz
	CreatedAt    pgtype.Timestamptz
	UpdatedAt    pgtype.Timestamptz
}

type OrchestrationEvent struct {
	ID          pgtype.UUID
	RunID       pgtype.UUID
	NodeID      pgtype.UUID
	WorkspaceID pgtype.UUID
	IssueID     pgtype.UUID
	Type        string
	Message     pgtype.Text
	Metadata    json.RawMessage
	CreatedAt   pgtype.Timestamptz
}

type OrchestrationEvidence struct {
	ID          pgtype.UUID
	RunID       pgtype.UUID
	NodeID      pgtype.UUID
	WorkspaceID pgtype.UUID
	IssueID     pgtype.UUID
	AgentTaskID pgtype.UUID
	Kind        string
	Summary     pgtype.Text
	Data        json.RawMessage
	CreatedAt   pgtype.Timestamptz
}

type OrchestrationSnapshot struct {
	Run      *OrchestrationRun
	Nodes    []OrchestrationNode
	Events   []OrchestrationEvent
	Evidence []OrchestrationEvidence
}

func (s *OrchestrationService) EnsureActiveRunForIssue(ctx context.Context, issue db.Issue, createdByType, createdByID string) (*OrchestrationSnapshot, error) {
	if s == nil || s.DB == nil {
		return nil, nil
	}
	if !issue.AssigneeType.Valid || issue.AssigneeType.String != "agent" || !issue.AssigneeID.Valid {
		return nil, nil
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var enabled bool
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(settings->>'orchestration_enabled', 'false') = 'true'
		FROM workspace
		WHERE id = $1
	`, issue.WorkspaceID).Scan(&enabled); err != nil {
		return nil, fmt.Errorf("read orchestration flag: %w", err)
	}
	if !enabled {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	}

	run, inserted, err := insertOrSelectActiveRun(ctx, tx, issue, createdByType, createdByID)
	if err != nil {
		return nil, err
	}
	if inserted {
		if err := insertInitialRunShape(ctx, tx, run); err != nil {
			return nil, err
		}
	}

	snapshot, err := selectSnapshotForRun(ctx, tx, run.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *OrchestrationService) GetLatestForIssue(ctx context.Context, issueID pgtype.UUID) (*OrchestrationSnapshot, error) {
	if s == nil || s.DB == nil {
		return &OrchestrationSnapshot{Nodes: []OrchestrationNode{}, Events: []OrchestrationEvent{}, Evidence: []OrchestrationEvidence{}}, nil
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var runID pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM orchestration_run
		WHERE issue_id = $1
		ORDER BY CASE WHEN status = 'active' THEN 0 ELSE 1 END, created_at DESC
		LIMIT 1
	`, issueID).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &OrchestrationSnapshot{Nodes: []OrchestrationNode{}, Events: []OrchestrationEvent{}, Evidence: []OrchestrationEvidence{}}, nil
	}
	if err != nil {
		return nil, err
	}

	snapshot, err := selectSnapshotForRun(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func insertOrSelectActiveRun(ctx context.Context, tx pgx.Tx, issue db.Issue, createdByType, createdByID string) (OrchestrationRun, bool, error) {
	creatorType := pgtype.Text{}
	if createdByType != "" {
		creatorType = pgtype.Text{String: createdByType, Valid: true}
	}
	creatorID := pgtype.UUID{}
	if createdByID != "" {
		if parsed, err := util.ParseUUID(createdByID); err == nil {
			creatorID = parsed
		}
	}

	run, err := scanRun(tx.QueryRow(ctx, `
		INSERT INTO orchestration_run (
			workspace_id, issue_id, status, source, plan_version, created_by_type, created_by_id
		)
		VALUES ($1, $2, 'active', 'issue_assignment', 1, $3, $4)
		ON CONFLICT (issue_id) WHERE status = 'active' DO NOTHING
		RETURNING id, workspace_id, issue_id, status, source, plan_version,
		          created_by_type, created_by_id, created_at, updated_at
	`, issue.WorkspaceID, issue.ID, creatorType, creatorID))
	if err == nil {
		return run, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return OrchestrationRun{}, false, fmt.Errorf("insert orchestration run: %w", err)
	}

	run, err = scanRun(tx.QueryRow(ctx, `
		SELECT id, workspace_id, issue_id, status, source, plan_version,
		       created_by_type, created_by_id, created_at, updated_at
		FROM orchestration_run
		WHERE issue_id = $1 AND status = 'active'
		ORDER BY created_at DESC
		LIMIT 1
		FOR UPDATE
	`, issue.ID))
	if err != nil {
		return OrchestrationRun{}, false, fmt.Errorf("select active orchestration run: %w", err)
	}
	return run, false, nil
}

func insertInitialRunShape(ctx context.Context, tx pgx.Tx, run OrchestrationRun) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO orchestration_event (run_id, workspace_id, issue_id, type, message, metadata)
		VALUES ($1, $2, $3, $4, 'Active orchestration run started', '{}'::jsonb)
	`, run.ID, run.WorkspaceID, run.IssueID, string(OrchestrationEventTypeRunStarted)); err != nil {
		return fmt.Errorf("insert run_started event: %w", err)
	}

	for _, n := range DefaultInitialOrchestrationNodes() {
		if err := n.Validate(); err != nil {
			return err
		}
		node, err := scanNode(tx.QueryRow(ctx, `
			INSERT INTO orchestration_node (
				run_id, workspace_id, issue_id, key, kind, status, position, dependencies
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id, run_id, workspace_id, issue_id, key, kind, status, position,
			          dependencies, agent_task_id, attempt, metadata, started_at,
			          completed_at, created_at, updated_at
		`, run.ID, run.WorkspaceID, run.IssueID, string(n.Key), string(n.Kind), string(n.Status), n.Position, n.DependencyStrings()))
		if err != nil {
			return fmt.Errorf("insert %s node: %w", n.Key, err)
		}
		metadata, _ := json.Marshal(map[string]string{"node_key": string(n.Key)})
		if _, err := tx.Exec(ctx, `
			INSERT INTO orchestration_event (run_id, node_id, workspace_id, issue_id, type, message, metadata)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, run.ID, node.ID, run.WorkspaceID, run.IssueID, string(OrchestrationEventTypeNodeCreated), "Initial "+string(n.Key)+" node created", metadata); err != nil {
			return fmt.Errorf("insert %s node_created event: %w", n.Key, err)
		}
	}

	return nil
}

func selectSnapshotForRun(ctx context.Context, tx pgx.Tx, runID pgtype.UUID) (*OrchestrationSnapshot, error) {
	run, err := scanRun(tx.QueryRow(ctx, `
		SELECT id, workspace_id, issue_id, status, source, plan_version,
		       created_by_type, created_by_id, created_at, updated_at
		FROM orchestration_run
		WHERE id = $1
	`, runID))
	if err != nil {
		return nil, fmt.Errorf("select orchestration run: %w", err)
	}

	nodes, err := selectNodesForRun(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	events, err := selectEventsForRun(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	evidence, err := selectEvidenceForRun(ctx, tx, runID)
	if err != nil {
		return nil, err
	}

	return &OrchestrationSnapshot{
		Run:      &run,
		Nodes:    nodes,
		Events:   events,
		Evidence: evidence,
	}, nil
}

func selectNodesForRun(ctx context.Context, tx pgx.Tx, runID pgtype.UUID) ([]OrchestrationNode, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, run_id, workspace_id, issue_id, key, kind, status, position,
		       dependencies, agent_task_id, attempt, metadata, started_at,
		       completed_at, created_at, updated_at
		FROM orchestration_node
		WHERE run_id = $1
		ORDER BY position ASC
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("select orchestration nodes: %w", err)
	}
	defer rows.Close()

	nodes := []OrchestrationNode{}
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func selectEventsForRun(ctx context.Context, tx pgx.Tx, runID pgtype.UUID) ([]OrchestrationEvent, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, run_id, node_id, workspace_id, issue_id, type, message, metadata, created_at
		FROM orchestration_event
		WHERE run_id = $1
		ORDER BY created_at ASC, id ASC
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("select orchestration events: %w", err)
	}
	defer rows.Close()

	events := []OrchestrationEvent{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func selectEvidenceForRun(ctx context.Context, tx pgx.Tx, runID pgtype.UUID) ([]OrchestrationEvidence, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, run_id, node_id, workspace_id, issue_id, agent_task_id,
		       kind, summary, data, created_at
		FROM orchestration_evidence
		WHERE run_id = $1
		ORDER BY created_at ASC, id ASC
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("select orchestration evidence: %w", err)
	}
	defer rows.Close()

	evidence := []OrchestrationEvidence{}
	for rows.Next() {
		var item OrchestrationEvidence
		if err := rows.Scan(
			&item.ID,
			&item.RunID,
			&item.NodeID,
			&item.WorkspaceID,
			&item.IssueID,
			&item.AgentTaskID,
			&item.Kind,
			&item.Summary,
			&item.Data,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		evidence = append(evidence, item)
	}
	return evidence, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRun(row scanner) (OrchestrationRun, error) {
	var run OrchestrationRun
	err := row.Scan(
		&run.ID,
		&run.WorkspaceID,
		&run.IssueID,
		&run.Status,
		&run.Source,
		&run.PlanVersion,
		&run.CreatedByType,
		&run.CreatedByID,
		&run.CreatedAt,
		&run.UpdatedAt,
	)
	return run, err
}

func scanNode(row scanner) (OrchestrationNode, error) {
	var node OrchestrationNode
	err := row.Scan(
		&node.ID,
		&node.RunID,
		&node.WorkspaceID,
		&node.IssueID,
		&node.Key,
		&node.Kind,
		&node.Status,
		&node.Position,
		&node.Dependencies,
		&node.AgentTaskID,
		&node.Attempt,
		&node.Metadata,
		&node.StartedAt,
		&node.CompletedAt,
		&node.CreatedAt,
		&node.UpdatedAt,
	)
	return node, err
}

func scanEvent(row scanner) (OrchestrationEvent, error) {
	var event OrchestrationEvent
	err := row.Scan(
		&event.ID,
		&event.RunID,
		&event.NodeID,
		&event.WorkspaceID,
		&event.IssueID,
		&event.Type,
		&event.Message,
		&event.Metadata,
		&event.CreatedAt,
	)
	return event, err
}
