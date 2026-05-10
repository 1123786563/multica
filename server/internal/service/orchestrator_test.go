package service

import (
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestHardCheckNodeResult(t *testing.T) {
	node := db.OrchestrationNode{Type: "implement"}

	if pass, reason := hardCheckNodeResult(node, AgentStructuredResult{}); pass || reason != "missing_summary" {
		t.Fatalf("empty result: pass=%v reason=%q", pass, reason)
	}

	if pass, reason := hardCheckNodeResult(node, AgentStructuredResult{Summary: "done"}); pass || reason != "missing_artifact_or_changed_files" {
		t.Fatalf("missing evidence: pass=%v reason=%q", pass, reason)
	}

	if pass, reason := hardCheckNodeResult(node, AgentStructuredResult{
		Summary:      "implemented",
		ChangedFiles: []string{"server/internal/service/orchestrator.go"},
	}); pass || reason != "missing_criteria_evidence" {
		t.Fatalf("missing criteria evidence should fail: pass=%v reason=%q", pass, reason)
	}

	if pass, reason := hardCheckNodeResult(node, AgentStructuredResult{
		Summary:          "implemented",
		ChangedFiles:     []string{"server/internal/service/orchestrator.go"},
		TestResult:       []byte(`{"status":"failed"}`),
		CriteriaEvidence: []any{map[string]any{"criterion": "tests pass", "evidence": "failed"}},
	}); pass || reason != "test_result_failed" {
		t.Fatalf("failed test result should fail: pass=%v reason=%q", pass, reason)
	}

	pass, reason := hardCheckNodeResult(node, AgentStructuredResult{
		Summary:          "implemented",
		ChangedFiles:     []string{"server/internal/service/orchestrator.go"},
		CriteriaEvidence: []any{map[string]any{"criterion": "builds", "evidence": "go test passed"}},
	})
	if !pass || reason != "hard_check_passed" {
		t.Fatalf("changed-files result should pass: pass=%v reason=%q", pass, reason)
	}

	testNode := db.OrchestrationNode{Type: "test"}
	if pass, reason := hardCheckNodeResult(testNode, AgentStructuredResult{
		Summary:          "verified",
		CriteriaEvidence: []any{map[string]any{"criterion": "tests", "evidence": "missing"}},
	}); pass || reason != "missing_test_result" {
		t.Fatalf("test node without test result should fail: pass=%v reason=%q", pass, reason)
	}
}

func TestShouldWaitForHuman(t *testing.T) {
	if !shouldWaitForHuman(AgentStructuredResult{Confidence: 0.3}) {
		t.Fatal("low non-zero confidence should wait for human")
	}
	if shouldWaitForHuman(AgentStructuredResult{Confidence: 0}) {
		t.Fatal("missing confidence should not force human wait")
	}
	if shouldWaitForHuman(AgentStructuredResult{Confidence: 0.8}) {
		t.Fatal("high confidence should not wait for human")
	}
}

func TestParseAgentResultLegacyAndStructured(t *testing.T) {
	structured := parseAgentResult([]byte(`{"status":"completed","summary":"done","changed_files":["a.go"]}`))
	if structured.Summary != "done" || len(structured.ChangedFiles) != 1 {
		t.Fatalf("structured result not parsed: %#v", structured)
	}

	legacy := parseAgentResult([]byte(`{"output":"legacy summary"}`))
	if legacy.Status != "completed" || legacy.Summary != "legacy summary" {
		t.Fatalf("legacy result not normalized: %#v", legacy)
	}
}
