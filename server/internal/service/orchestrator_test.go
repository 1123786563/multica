package service

import (
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestParseOrchestrationTaskContext(t *testing.T) {
	// Empty input
	_, ok := ParseOrchestrationTaskContext(nil)
	if ok {
		t.Fatal("nil input should return false")
	}
	_, ok = ParseOrchestrationTaskContext([]byte{})
	if ok {
		t.Fatal("empty input should return false")
	}

	// Invalid JSON
	_, ok = ParseOrchestrationTaskContext([]byte(`not json`))
	if ok {
		t.Fatal("invalid JSON should return false")
	}

	// Missing required fields
	_, ok = ParseOrchestrationTaskContext([]byte(`{"type":"orchestration_node"}`))
	if ok {
		t.Fatal("missing plan_id and node_id should return false")
	}

	// Wrong type
	_, ok = ParseOrchestrationTaskContext([]byte(`{"type":"other","orchestration_plan_id":"p1","orchestration_node_id":"n1"}`))
	if ok {
		t.Fatal("wrong type should return false")
	}

	// Valid context
	ctx, ok := ParseOrchestrationTaskContext([]byte(`{"type":"orchestration_node","orchestration_plan_id":"plan-1","orchestration_node_id":"node-1","node_type":"implement","objective":"fix bug"}`))
	if !ok {
		t.Fatal("valid context should return true")
	}
	if ctx.OrchestrationPlanID != "plan-1" || ctx.OrchestrationNodeID != "node-1" {
		t.Fatalf("unexpected context: %+v", ctx)
	}
	if ctx.NodeType != "implement" || ctx.Objective != "fix bug" {
		t.Fatalf("unexpected node_type/objective: %+v", ctx)
	}
}

func TestBuildPlanNodeSpecs(t *testing.T) {
	t.Run("simple plan for normal issue", func(t *testing.T) {
		issue := db.Issue{Title: "Fix typo", Priority: "medium"}
		specs := buildPlanNodeSpecs(issue, []byte(`{"required":["summary"]}`))
		if len(specs) != 1 {
			t.Fatalf("expected 1 node, got %d", len(specs))
		}
		if specs[0].Type != "implement" {
			t.Fatalf("expected implement, got %s", specs[0].Type)
		}
	})

	t.Run("medium plan for urgent issue", func(t *testing.T) {
		issue := db.Issue{Title: "Critical fix", Priority: "urgent"}
		specs := buildPlanNodeSpecs(issue, []byte(`{"required":["summary"]}`))
		if len(specs) != 3 {
			t.Fatalf("expected 3 nodes for urgent, got %d", len(specs))
		}
		if specs[0].Type != "inspect" || specs[1].Type != "implement" || specs[2].Type != "test" {
			t.Fatalf("expected inspect/implement/test, got %s/%s/%s", specs[0].Type, specs[1].Type, specs[2].Type)
		}
	})

	t.Run("medium plan for issue with acceptance criteria", func(t *testing.T) {
		issue := db.Issue{
			Title:              "Feature X",
			Priority:           "low",
			AcceptanceCriteria: []byte(`[{"criterion":"must pass"}]`),
		}
		specs := buildPlanNodeSpecs(issue, []byte(`{"required":["summary"]}`))
		if len(specs) != 3 {
			t.Fatalf("expected 3 nodes for acceptance criteria issue, got %d", len(specs))
		}
	})
}

func TestNormalizeArtifacts(t *testing.T) {
	result := AgentStructuredResult{
		Summary:      "done",
		ChangedFiles: []string{"a.go", "b.go"},
		TestResult:   json.RawMessage(`{"passed":true}`),
		Artifacts: []AgentResultArtifact{
			{Type: "diff", URI: "git://branch/x", Content: json.RawMessage(`{}`)},
		},
	}
	artifacts := NormalizeArtifacts(result)

	// Original artifact + changed_files → diff + test_result + summary
	hasOriginalDiff, hasGeneratedDiff, hasTestResult, hasSummary := false, false, false, false
	for _, a := range artifacts {
		switch a.Type {
		case "diff":
			if a.URI == "git://branch/x" {
				hasOriginalDiff = true
			} else {
				hasGeneratedDiff = true
			}
		case "test_result":
			hasTestResult = true
		case "summary":
			hasSummary = true
		}
	}
	if !hasOriginalDiff {
		t.Error("missing original diff artifact")
	}
	if !hasGeneratedDiff {
		t.Error("missing generated diff from changed_files")
	}
	if !hasTestResult {
		t.Error("missing test_result artifact")
	}
	if !hasSummary {
		t.Error("missing summary artifact")
	}
}

func TestEmptyArtifactTypeInvalidInStructuredResult(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{
		"status":"completed",
		"summary":"done",
		"artifacts":[{"content":{}}],
		"criteria_evidence":[{"criterion":"c","evidence":"e"}]
	}`), ResultParseOptions{AllowLegacyCompatibility: true})
	if validation.Valid {
		t.Fatal("empty artifact type should be invalid")
	}
}

func TestNodeDependenciesCompleted(t *testing.T) {
	node1 := db.OrchestrationNode{ID: util.MustParseUUID("00000000-0000-0000-0000-000000000001"), Status: "completed"}
	node2 := db.OrchestrationNode{ID: util.MustParseUUID("00000000-0000-0000-0000-000000000002"), Status: "pending"}
	node3 := db.OrchestrationNode{ID: util.MustParseUUID("00000000-0000-0000-0000-000000000003"), Status: "pending"}

	nodeByID := map[string]db.OrchestrationNode{
		util.UUIDToString(node1.ID): node1,
		util.UUIDToString(node2.ID): node2,
		util.UUIDToString(node3.ID): node3,
	}

	edge1to2 := db.OrchestrationEdge{FromNodeID: node1.ID, ToNodeID: node2.ID}
	edge2to3 := db.OrchestrationEdge{FromNodeID: node2.ID, ToNodeID: node3.ID}

	t.Run("node with no dependencies is ready", func(t *testing.T) {
		if !nodeDependenciesCompleted(node1, nil, nodeByID) {
			t.Fatal("node with no edges should be ready")
		}
	})

	t.Run("node with completed upstream is ready", func(t *testing.T) {
		if !nodeDependenciesCompleted(node2, []db.OrchestrationEdge{edge1to2}, nodeByID) {
			t.Fatal("node2 should be ready when node1 is completed")
		}
	})

	t.Run("node with pending upstream is not ready", func(t *testing.T) {
		if nodeDependenciesCompleted(node3, []db.OrchestrationEdge{edge1to2, edge2to3}, nodeByID) {
			t.Fatal("node3 should not be ready when node2 is pending")
		}
	})
}

func TestTestResultFailed(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"empty", "", false},
		{"passed status", `{"status":"passed"}`, false},
		{"failed status", `{"status":"failed"}`, true},
		{"failure status", `{"status":"failure"}`, true},
		{"passed bool", `{"passed":true}`, false},
		{"failed passed bool", `{"passed":false}`, true},
		{"success true", `{"success":true}`, false},
		{"success false", `{"success":false}`, true},
		{"invalid json", `{bad`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := testResultFailed(json.RawMessage(tt.input))
			if got != tt.expected {
				t.Fatalf("testResultFailed(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

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
		CriteriaEvidence: []CriteriaEvidence{{Criterion: "tests pass", Evidence: "failed"}},
	}); pass || reason != "test_result_failed" {
		t.Fatalf("failed test result should fail: pass=%v reason=%q", pass, reason)
	}

	pass, reason := hardCheckNodeResult(node, AgentStructuredResult{
		Summary:          "implemented",
		ChangedFiles:     []string{"server/internal/service/orchestrator.go"},
		CriteriaEvidence: []CriteriaEvidence{{Criterion: "builds", Evidence: "go test passed"}},
	})
	if !pass || reason != "hard_check_passed" {
		t.Fatalf("changed-files result should pass: pass=%v reason=%q", pass, reason)
	}

	testNode := db.OrchestrationNode{Type: "test"}
	if pass, reason := hardCheckNodeResult(testNode, AgentStructuredResult{
		Summary:          "verified",
		CriteriaEvidence: []CriteriaEvidence{{Criterion: "tests", Evidence: "missing"}},
	}); pass || reason != "missing_test_result" {
		t.Fatalf("test node without test result should fail: pass=%v reason=%q", pass, reason)
	}

	if pass, reason := hardCheckNodeResult(testNode, AgentStructuredResult{
		Summary: "verified",
		Artifacts: []AgentResultArtifact{
			{Type: "test_result", Content: json.RawMessage(`{"passed":false}`)},
		},
		CriteriaEvidence: []CriteriaEvidence{{Criterion: "tests", Evidence: "failed"}},
	}); pass || reason != "test_result_failed" {
		t.Fatalf("failed artifact-only test result should fail: pass=%v reason=%q", pass, reason)
	}

	statusValidation := ParseAgentResultPayload([]byte(`{
		"status":" failed ",
		"summary":"agent reported failure",
		"criteria_evidence":[{"criterion":"c","evidence":"e"}]
	}`), ResultParseOptions{AllowLegacyCompatibility: true})
	if !statusValidation.Valid {
		t.Fatalf("whitespace-padded failed status should be valid: %#v", statusValidation.Errors)
	}
	if statusValidation.Result.Status != "failed" {
		t.Fatalf("expected normalized status failed, got %q", statusValidation.Result.Status)
	}
	if pass, reason := hardCheckNodeResult(node, statusValidation.Result); pass || reason != "agent_reported_failed" {
		t.Fatalf("normalized failed status should report agent failure: pass=%v reason=%q", pass, reason)
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
	structuredValidation := ParseAgentResultPayload([]byte(`{"status":"completed","summary":"done","changed_files":["a.go"]}`), ResultParseOptions{AllowLegacyCompatibility: true})
	if !structuredValidation.Valid {
		t.Fatalf("structured result should be valid: %#v", structuredValidation.Errors)
	}
	structured := structuredValidation.Result
	if structured.Summary != "done" || len(structured.ChangedFiles) != 1 {
		t.Fatalf("structured result not parsed: %#v", structured)
	}

	legacyValidation := ParseAgentResultPayload([]byte(`{"output":"legacy summary"}`), ResultParseOptions{AllowLegacyCompatibility: true})
	if !legacyValidation.Valid {
		t.Fatalf("legacy result should be valid: %#v", legacyValidation.Errors)
	}
	legacy := legacyValidation.Result
	if legacy.Status != "completed" || legacy.Summary != "legacy summary" {
		t.Fatalf("legacy result not normalized: %#v", legacy)
	}
}
