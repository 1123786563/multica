package service

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestBuildNodeSummaryRunning(t *testing.T) {
	summary := BuildNodeSummary(NodeObservabilityInput{
		NodeStatus:   "running",
		AttemptCount: 1,
		MaxAttempts:  2,
	})
	if summary.Status != "running" {
		t.Fatalf("expected running, got %q", summary.Status)
	}
	if summary.ReasonCode != "running" {
		t.Fatalf("expected running reason code, got %q", summary.ReasonCode)
	}
	if summary.RecommendedAction != "none" {
		t.Fatalf("expected no action, got %q", summary.RecommendedAction)
	}
}

func TestBuildNodeSummaryWaitingHumanApproval(t *testing.T) {
	summary := BuildNodeSummary(NodeObservabilityInput{
		NodeStatus: "waiting_human",
		Evaluation: &EvaluationResult{
			Status:            "waiting_human",
			Reason:            "low_confidence",
			ReasonDetail:      "Kernel requires approval before completing the node.",
			RecommendedAction: "ask_human",
		},
	})
	if summary.ReasonCode != "waiting_for_approval" {
		t.Fatalf("expected waiting_for_approval, got %q", summary.ReasonCode)
	}
	if summary.RecommendedAction != "approve" {
		t.Fatalf("expected approve action, got %q", summary.RecommendedAction)
	}
	if !summary.ActionEnabled {
		t.Fatalf("expected approval action to be enabled")
	}
}

func TestBuildNodeSummaryRetryExhausted(t *testing.T) {
	summary := BuildNodeSummary(NodeObservabilityInput{
		NodeStatus:          "failed",
		AttemptCount:        2,
		MaxAttempts:         2,
		LatestFailureReason: "daemon disconnected",
	})
	if summary.ReasonCode != "retry_exhausted" {
		t.Fatalf("expected retry_exhausted, got %q", summary.ReasonCode)
	}
	if summary.RecommendedAction != "retry" {
		t.Fatalf("expected retry action, got %q", summary.RecommendedAction)
	}
}

func TestBuildNodeSummaryCompleted(t *testing.T) {
	summary := BuildNodeSummary(NodeObservabilityInput{
		NodeStatus: "completed",
		Evaluation: &EvaluationResult{
			Status:            "passed",
			Reason:            "hard_check_passed",
			ReasonDetail:      "Kernel hard checks passed for this node.",
			RecommendedAction: "complete",
		},
		LatestAgentSummary: "Implemented the requested changes.",
	})
	if summary.ReasonCode != "completed" {
		t.Fatalf("expected completed reason code, got %q", summary.ReasonCode)
	}
	if summary.LatestAgentSummary != "Implemented the requested changes." {
		t.Fatalf("expected latest agent summary to round-trip, got %q", summary.LatestAgentSummary)
	}
}

func TestBuildNodeSummaryFromRecordsUsesLatestEvents(t *testing.T) {
	nodeID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	node := db.OrchestrationNode{
		ID:           nodeID,
		Status:       "ready",
		AttemptCount: 1,
		MaxAttempts:  3,
		UpdatedAt:    pgtype.Timestamptz{Time: time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC), Valid: true},
	}
	events := []db.OrchestrationEvent{
		{
			NodeID:    nodeID,
			EventType: "task.completed",
			Payload:   []byte(`{"summary":"attempt failed"}`),
			CreatedAt: pgtype.Timestamptz{Time: time.Date(2026, 5, 11, 10, 1, 0, 0, time.UTC), Valid: true},
		},
		{
			NodeID:    nodeID,
			EventType: "task.failed",
			Payload:   []byte(`{"failure_reason":"timeout"}`),
			CreatedAt: pgtype.Timestamptz{Time: time.Date(2026, 5, 11, 10, 2, 0, 0, time.UTC), Valid: true},
		},
		{
			NodeID:    nodeID,
			EventType: "node.retry_scheduled",
			Payload:   []byte(`{"reason":"timeout"}`),
			CreatedAt: pgtype.Timestamptz{Time: time.Date(2026, 5, 11, 10, 3, 0, 0, time.UTC), Valid: true},
		},
	}

	summary := BuildNodeSummaryFromRecords(node, events)
	if summary.ReasonCode != "retry_scheduled" {
		t.Fatalf("expected retry_scheduled, got %q", summary.ReasonCode)
	}
	if summary.LatestAgentSummary != "attempt failed" {
		t.Fatalf("expected latest agent summary to be preserved, got %q", summary.LatestAgentSummary)
	}
	if summary.UpdatedAt != "2026-05-11T10:03:00Z" {
		t.Fatalf("expected updated_at from latest event, got %q", summary.UpdatedAt)
	}
}
