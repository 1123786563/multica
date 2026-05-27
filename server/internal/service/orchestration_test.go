package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestStartIssueBindsWorkerDefaultReasoningProfile(t *testing.T) {
	workspaceID := testUUID(10)
	issueID := testUUID(11)
	planID := testUUID(12)
	tx := &startIssueTx{planID: planID}
	db := &startIssueDB{planID: planID, issueID: issueID}
	starter := &captureIssueWorkflowStarter{}
	svc := NewOrchestrationService(db, &startIssueTxStarter{tx: tx}, starter)

	result, err := svc.StartIssue(t.Context(), workspaceID, issueID)
	if err != nil {
		t.Fatalf("StartIssue returned error: %v", err)
	}

	if !strings.Contains(tx.insertSQL, "reasoning_profile_ref") {
		t.Fatalf("StartIssue insert must persist reasoning_profile_ref, sql=%s", tx.insertSQL)
	}
	if !containsAnyArg(tx.insertArgs, "worker-default") {
		t.Fatalf("StartIssue insert args must include worker-default reasoning profile, args=%v", tx.insertArgs)
	}
	if starter.input.ReasoningProfileRef != "worker-default" {
		t.Fatalf("Temporal input reasoning profile mismatch: got %q", starter.input.ReasoningProfileRef)
	}
	if result.Plan.ReasoningProfileRef != "worker-default" {
		t.Fatalf("returned plan reasoning profile mismatch: got %q", result.Plan.ReasoningProfileRef)
	}
}

type captureIssueWorkflowStarter struct {
	input IssueWorkflowStartInput
}

func (s *captureIssueWorkflowStarter) StartIssueWorkflow(ctx context.Context, input IssueWorkflowStartInput) (TemporalWorkflowStart, error) {
	s.input = input
	return TemporalWorkflowStart{WorkflowID: input.WorkflowID, RunID: "run-1"}, nil
}

type startIssueTxStarter struct {
	tx *startIssueTx
}

func (s *startIssueTxStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	return s.tx, nil
}

type startIssueTx struct {
	planID     pgtype.UUID
	insertSQL  string
	insertArgs []any
	committed  bool
}

func (tx *startIssueTx) Begin(ctx context.Context) (pgx.Tx, error) { return tx, nil }
func (tx *startIssueTx) Commit(ctx context.Context) error {
	tx.committed = true
	return nil
}
func (tx *startIssueTx) Rollback(ctx context.Context) error { return nil }
func (tx *startIssueTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	return 0, fmt.Errorf("CopyFrom not implemented")
}
func (tx *startIssueTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults { return nil }
func (tx *startIssueTx) LargeObjects() pgx.LargeObjects                               { return pgx.LargeObjects{} }
func (tx *startIssueTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	return nil, fmt.Errorf("Prepare not implemented")
}
func (tx *startIssueTx) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("UPDATE 1"), nil
}
func (tx *startIssueTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return emptyRows{}, nil
}
func (tx *startIssueTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "WHERE issue_id = $1 AND status IN") {
		return staticRow{err: pgx.ErrNoRows}
	}
	if strings.Contains(sql, "INSERT INTO orchestration_plan") {
		tx.insertSQL = sql
		tx.insertArgs = args
		return staticRow{values: []any{tx.planID}}
	}
	return staticRow{err: fmt.Errorf("unexpected tx QueryRow: %s", sql)}
}
func (tx *startIssueTx) Conn() *pgx.Conn { return nil }

type startIssueDB struct {
	planID     pgtype.UUID
	issueID    pgtype.UUID
	workflowID string
	runID      string
}

func (db *startIssueDB) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "SET status = 'running'") {
		if workflowID, ok := arguments[1].(string); ok {
			db.workflowID = workflowID
		}
		if runID, ok := arguments[2].(string); ok {
			db.runID = runID
		}
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}
func (db *startIssueDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return emptyRows{}, nil
}
func (db *startIssueDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "FROM orchestration_plan") && strings.Contains(sql, "WHERE id = $1") {
		now := time.Unix(1700000000, 0)
		return staticRow{values: []any{
			db.planID,
			db.issueID,
			"running",
			"",
			"none",
			db.workflowID,
			db.runID,
			"worker-default",
			"issue_mvp",
			1,
			now,
			now,
		}}
	}
	return staticRow{err: fmt.Errorf("unexpected db QueryRow: %s", sql)}
}

type staticRow struct {
	values []any
	err    error
}

func (r staticRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan destination count %d does not match value count %d", len(dest), len(r.values))
	}
	for i := range dest {
		switch d := dest[i].(type) {
		case *pgtype.UUID:
			v, ok := r.values[i].(pgtype.UUID)
			if !ok {
				return fmt.Errorf("value %d is %T, not pgtype.UUID", i, r.values[i])
			}
			*d = v
		case *string:
			v, ok := r.values[i].(string)
			if !ok {
				return fmt.Errorf("value %d is %T, not string", i, r.values[i])
			}
			*d = v
		case *int:
			v, ok := r.values[i].(int)
			if !ok {
				return fmt.Errorf("value %d is %T, not int", i, r.values[i])
			}
			*d = v
		case *time.Time:
			v, ok := r.values[i].(time.Time)
			if !ok {
				return fmt.Errorf("value %d is %T, not time.Time", i, r.values[i])
			}
			*d = v
		default:
			return fmt.Errorf("unsupported scan destination %T", dest[i])
		}
	}
	return nil
}

type emptyRows struct{}

func (emptyRows) Close()                                       {}
func (emptyRows) Err() error                                   { return nil }
func (emptyRows) CommandTag() pgconn.CommandTag                { return pgconn.NewCommandTag("SELECT 0") }
func (emptyRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (emptyRows) Next() bool                                   { return false }
func (emptyRows) Scan(dest ...any) error                       { return fmt.Errorf("empty rows") }
func (emptyRows) Values() ([]any, error)                       { return nil, fmt.Errorf("empty rows") }
func (emptyRows) RawValues() [][]byte                          { return nil }
func (emptyRows) Conn() *pgx.Conn                              { return nil }

func containsAnyArg(args []any, want string) bool {
	for _, arg := range args {
		if s, ok := arg.(string); ok && s == want {
			return true
		}
	}
	return false
}
