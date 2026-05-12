package service

import "testing"

func TestDefaultInitialOrchestrationNodesDefinesKernelShape(t *testing.T) {
	nodes := DefaultInitialOrchestrationNodes()

	if len(nodes) != 3 {
		t.Fatalf("expected 3 initial nodes, got %d: %+v", len(nodes), nodes)
	}

	tests := []struct {
		index        int
		key          OrchestrationNodeKey
		kind         OrchestrationNodeKind
		status       OrchestrationNodeStatus
		position     int32
		dependencies []OrchestrationNodeKey
	}{
		{index: 0, key: OrchestrationNodeKeyPlan, kind: OrchestrationNodeKindPlan, status: OrchestrationNodeStatusReady, position: 1, dependencies: []OrchestrationNodeKey{}},
		{index: 1, key: OrchestrationNodeKeyExecute, kind: OrchestrationNodeKindExecute, status: OrchestrationNodeStatusPending, position: 2, dependencies: []OrchestrationNodeKey{OrchestrationNodeKeyPlan}},
		{index: 2, key: OrchestrationNodeKeyVerify, kind: OrchestrationNodeKindVerify, status: OrchestrationNodeStatusPending, position: 3, dependencies: []OrchestrationNodeKey{OrchestrationNodeKeyExecute}},
	}

	for _, tt := range tests {
		node := nodes[tt.index]
		if node.Key != tt.key || node.Kind != tt.kind || node.Status != tt.status || node.Position != tt.position {
			t.Fatalf("node %d = %+v, want key=%q kind=%q status=%q position=%d", tt.index, node, tt.key, tt.kind, tt.status, tt.position)
		}
		if len(node.Dependencies) != len(tt.dependencies) {
			t.Fatalf("node %d dependencies = %+v, want %+v", tt.index, node.Dependencies, tt.dependencies)
		}
		for i, dep := range tt.dependencies {
			if node.Dependencies[i] != dep {
				t.Fatalf("node %d dependency %d = %q, want %q", tt.index, i, node.Dependencies[i], dep)
			}
		}
		if err := node.Validate(); err != nil {
			t.Fatalf("node %d should validate: %v", tt.index, err)
		}
	}
}

func TestOrchestrationNodeDefinitionValidationRejectsUnknownDomainValues(t *testing.T) {
	node := OrchestrationNodeDefinition{
		Key:      "execute",
		Kind:     "future-kind",
		Status:   OrchestrationNodeStatusReady,
		Position: 1,
	}
	if err := node.Validate(); err == nil {
		t.Fatalf("expected invalid kind to fail validation")
	}

	node.Kind = OrchestrationNodeKindExecute
	node.Status = "future-status"
	if err := node.Validate(); err == nil {
		t.Fatalf("expected invalid status to fail validation")
	}

	node.Status = OrchestrationNodeStatusReady
	node.Key = "future-key"
	if err := node.Validate(); err == nil {
		t.Fatalf("expected invalid key to fail validation")
	}

	node.Key = OrchestrationNodeKeyExecute
	node.Dependencies = []OrchestrationNodeKey{"future-dependency"}
	if err := node.Validate(); err == nil {
		t.Fatalf("expected invalid dependency key to fail validation")
	}
}

func TestParseOrchestrationTaskResultV1ExtractsEvidenceFields(t *testing.T) {
	result, err := ParseOrchestrationTaskResult([]byte(`{
		"schema_version": 1,
		"summary": "Implemented the orchestration kernel model",
		"changed_files": ["server/internal/service/orchestration_model.go"],
		"artifacts": [{"path": "docs/orchestration-kernel-v1.md", "kind": "doc"}],
		"tests": [{"name": "go test ./internal/service", "status": "passed"}],
		"risks": []
	}`))
	if err != nil {
		t.Fatalf("ParseOrchestrationTaskResult returned error: %v", err)
	}

	if result.SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", result.SchemaVersion)
	}
	if result.Summary != "Implemented the orchestration kernel model" {
		t.Fatalf("summary = %q", result.Summary)
	}
	if len(result.ChangedFiles) != 1 || result.ChangedFiles[0] != "server/internal/service/orchestration_model.go" {
		t.Fatalf("changed files = %+v", result.ChangedFiles)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].Path != "docs/orchestration-kernel-v1.md" {
		t.Fatalf("artifacts = %+v", result.Artifacts)
	}
	if len(result.Tests) != 1 || result.Tests[0].Status != OrchestrationTaskTestStatusPassed {
		t.Fatalf("tests = %+v", result.Tests)
	}
	if len(result.Risks) != 0 {
		t.Fatalf("risks = %+v, want none", result.Risks)
	}
}

func TestVerifyCompletedTaskEvidenceSucceedsForRiskFreeResult(t *testing.T) {
	result := OrchestrationTaskResult{
		SchemaVersion: OrchestrationTaskResultSchemaV1,
		Summary:       "Implemented the orchestration kernel model",
		ChangedFiles:  []string{"server/internal/service/orchestration_model.go"},
		Artifacts:     []OrchestrationTaskArtifact{},
		Tests: []OrchestrationTaskTest{
			{Name: "go test ./internal/service", Status: OrchestrationTaskTestStatusPassed},
		},
		Risks: []OrchestrationTaskRisk{},
	}

	verification := VerifyCompletedTaskEvidence(OrchestrationHardCheckInput{
		HasLinkedTask:    true,
		TaskCompleted:    true,
		EvidenceRecorded: true,
		Result:           result,
	})

	if verification.Status != OrchestrationHardCheckStatusSucceeded {
		t.Fatalf("verification status = %q, want %q; reason=%q", verification.Status, OrchestrationHardCheckStatusSucceeded, verification.Reason)
	}
}

func TestParseOrchestrationTaskResultRejectsInsufficientEvidence(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "unknown schema version",
			raw: `{
				"schema_version": 2,
				"summary": "Done",
				"changed_files": [],
				"artifacts": [],
				"tests": [],
				"risks": []
			}`,
		},
		{
			name: "missing required arrays",
			raw: `{
				"schema_version": 1,
				"summary": "Done"
			}`,
		},
		{
			name: "empty summary",
			raw: `{
				"schema_version": 1,
				"summary": "   ",
				"changed_files": [],
				"artifacts": [],
				"tests": [],
				"risks": []
			}`,
		},
		{
			name: "unknown test status",
			raw: `{
				"schema_version": 1,
				"summary": "Done",
				"changed_files": [],
				"artifacts": [],
				"tests": [{"name": "make check", "status": "future"}],
				"risks": []
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseOrchestrationTaskResult([]byte(tt.raw)); err == nil {
				t.Fatalf("expected parse error")
			}
		})
	}
}

func TestVerifyCompletedTaskEvidenceRequiresApprovalForRisksOrFailedTests(t *testing.T) {
	base := OrchestrationTaskResult{
		SchemaVersion: OrchestrationTaskResultSchemaV1,
		Summary:       "Implemented the orchestration kernel model",
		ChangedFiles:  []string{},
		Artifacts:     []OrchestrationTaskArtifact{},
		Tests: []OrchestrationTaskTest{
			{Name: "go test ./internal/service", Status: OrchestrationTaskTestStatusPassed},
		},
		Risks: []OrchestrationTaskRisk{},
	}

	withFailedTest := base
	withFailedTest.Tests = []OrchestrationTaskTest{{Name: "make check", Status: OrchestrationTaskTestStatusFailed}}
	failedTest := VerifyCompletedTaskEvidence(OrchestrationHardCheckInput{
		HasLinkedTask:    true,
		TaskCompleted:    true,
		EvidenceRecorded: true,
		Result:           withFailedTest,
	})
	if failedTest.Status != OrchestrationHardCheckStatusApprovalRequired || failedTest.Reason != "tests_failed" {
		t.Fatalf("failed-test verification = %+v", failedTest)
	}

	withRisk := base
	withRisk.Risks = []OrchestrationTaskRisk{{Summary: "Manual migration required", RequiresApproval: true}}
	risk := VerifyCompletedTaskEvidence(OrchestrationHardCheckInput{
		HasLinkedTask:    true,
		TaskCompleted:    true,
		EvidenceRecorded: true,
		Result:           withRisk,
	})
	if risk.Status != OrchestrationHardCheckStatusApprovalRequired || risk.Reason != "risks_present" {
		t.Fatalf("risk verification = %+v", risk)
	}
}
