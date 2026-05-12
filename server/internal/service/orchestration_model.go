package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

type OrchestrationRunStatus string

const (
	OrchestrationRunStatusActive    OrchestrationRunStatus = "active"
	OrchestrationRunStatusSucceeded OrchestrationRunStatus = "succeeded"
	OrchestrationRunStatusFailed    OrchestrationRunStatus = "failed"
	OrchestrationRunStatusCancelled OrchestrationRunStatus = "cancelled"
)

type OrchestrationRunSource string

const (
	OrchestrationRunSourceIssueAssignment OrchestrationRunSource = "issue_assignment"
	OrchestrationRunSourceManualRetry     OrchestrationRunSource = "manual_retry"
	OrchestrationRunSourceRecovery        OrchestrationRunSource = "recovery"
)

type OrchestrationNodeKey string

const (
	OrchestrationNodeKeyPlan    OrchestrationNodeKey = "plan"
	OrchestrationNodeKeyExecute OrchestrationNodeKey = "execute"
	OrchestrationNodeKeyVerify  OrchestrationNodeKey = "verify"
)

type OrchestrationNodeKind string

const (
	OrchestrationNodeKindPlan    OrchestrationNodeKind = "plan"
	OrchestrationNodeKindExecute OrchestrationNodeKind = "execute"
	OrchestrationNodeKindVerify  OrchestrationNodeKind = "verify"
)

type OrchestrationNodeStatus string

const (
	OrchestrationNodeStatusPending   OrchestrationNodeStatus = "pending"
	OrchestrationNodeStatusReady     OrchestrationNodeStatus = "ready"
	OrchestrationNodeStatusRunning   OrchestrationNodeStatus = "running"
	OrchestrationNodeStatusWaiting   OrchestrationNodeStatus = "waiting"
	OrchestrationNodeStatusSucceeded OrchestrationNodeStatus = "succeeded"
	OrchestrationNodeStatusFailed    OrchestrationNodeStatus = "failed"
	OrchestrationNodeStatusCancelled OrchestrationNodeStatus = "cancelled"
)

type OrchestrationEventType string

const (
	OrchestrationEventTypeRunStarted  OrchestrationEventType = "run_started"
	OrchestrationEventTypeNodeCreated OrchestrationEventType = "node_created"
)

type OrchestrationEvidenceKind string

const (
	OrchestrationEvidenceKindResultSummary OrchestrationEvidenceKind = "result_summary"
	OrchestrationEvidenceKindHardCheck     OrchestrationEvidenceKind = "hard_check"
	OrchestrationEvidenceKindAgentOutput   OrchestrationEvidenceKind = "agent_output"
)

const OrchestrationTaskResultSchemaV1 = 1

type OrchestrationTaskTestStatus string

const (
	OrchestrationTaskTestStatusPassed  OrchestrationTaskTestStatus = "passed"
	OrchestrationTaskTestStatusFailed  OrchestrationTaskTestStatus = "failed"
	OrchestrationTaskTestStatusSkipped OrchestrationTaskTestStatus = "skipped"
)

type OrchestrationTaskResult struct {
	SchemaVersion int                         `json:"schema_version"`
	Summary       string                      `json:"summary"`
	ChangedFiles  []string                    `json:"changed_files"`
	Artifacts     []OrchestrationTaskArtifact `json:"artifacts"`
	Tests         []OrchestrationTaskTest     `json:"tests"`
	Risks         []OrchestrationTaskRisk     `json:"risks"`
}

type OrchestrationTaskArtifact struct {
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
	Kind string `json:"kind,omitempty"`
}

type OrchestrationTaskTest struct {
	Name   string                      `json:"name"`
	Status OrchestrationTaskTestStatus `json:"status"`
	Output string                      `json:"output,omitempty"`
}

type OrchestrationTaskRisk struct {
	Summary          string `json:"summary"`
	Severity         string `json:"severity,omitempty"`
	RequiresApproval bool   `json:"requires_approval,omitempty"`
}

type OrchestrationHardCheckStatus string

const (
	OrchestrationHardCheckStatusSucceeded            OrchestrationHardCheckStatus = "succeeded"
	OrchestrationHardCheckStatusEvidenceInsufficient OrchestrationHardCheckStatus = "evidence_insufficient"
	OrchestrationHardCheckStatusApprovalRequired     OrchestrationHardCheckStatus = "approval_required"
)

type OrchestrationHardCheckInput struct {
	HasLinkedTask    bool
	TaskCompleted    bool
	EvidenceRecorded bool
	Result           OrchestrationTaskResult
}

type OrchestrationHardCheckResult struct {
	Status OrchestrationHardCheckStatus
	Reason string
}

func ParseOrchestrationTaskResult(raw []byte) (OrchestrationTaskResult, error) {
	var envelope struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return OrchestrationTaskResult{}, fmt.Errorf("parse orchestration task result: %w", err)
	}
	if envelope.SchemaVersion != OrchestrationTaskResultSchemaV1 {
		return OrchestrationTaskResult{}, fmt.Errorf("unsupported orchestration task result schema version %d", envelope.SchemaVersion)
	}

	type requiredFields struct {
		ChangedFiles *json.RawMessage `json:"changed_files"`
		Artifacts    *json.RawMessage `json:"artifacts"`
		Tests        *json.RawMessage `json:"tests"`
		Risks        *json.RawMessage `json:"risks"`
	}
	var required requiredFields
	if err := json.Unmarshal(raw, &required); err != nil {
		return OrchestrationTaskResult{}, fmt.Errorf("parse orchestration task result required fields: %w", err)
	}
	if required.ChangedFiles == nil || required.Artifacts == nil || required.Tests == nil || required.Risks == nil {
		return OrchestrationTaskResult{}, fmt.Errorf("orchestration task result missing required evidence arrays")
	}

	var result OrchestrationTaskResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return OrchestrationTaskResult{}, fmt.Errorf("decode orchestration task result v1: %w", err)
	}
	result.Summary = strings.TrimSpace(result.Summary)
	if result.Summary == "" {
		return OrchestrationTaskResult{}, fmt.Errorf("orchestration task result summary is required")
	}
	for i, test := range result.Tests {
		if !validTaskTestStatus(test.Status) {
			return OrchestrationTaskResult{}, fmt.Errorf("invalid orchestration task test status %q at index %d", test.Status, i)
		}
	}
	return result, nil
}

func VerifyCompletedTaskEvidence(input OrchestrationHardCheckInput) OrchestrationHardCheckResult {
	if !input.HasLinkedTask {
		return OrchestrationHardCheckResult{Status: OrchestrationHardCheckStatusEvidenceInsufficient, Reason: "missing_linked_task"}
	}
	if !input.TaskCompleted {
		return OrchestrationHardCheckResult{Status: OrchestrationHardCheckStatusEvidenceInsufficient, Reason: "task_not_completed"}
	}
	if !input.EvidenceRecorded {
		return OrchestrationHardCheckResult{Status: OrchestrationHardCheckStatusEvidenceInsufficient, Reason: "evidence_not_recorded"}
	}
	if input.Result.SchemaVersion != OrchestrationTaskResultSchemaV1 {
		return OrchestrationHardCheckResult{Status: OrchestrationHardCheckStatusEvidenceInsufficient, Reason: "invalid_result_schema"}
	}
	if strings.TrimSpace(input.Result.Summary) == "" {
		return OrchestrationHardCheckResult{Status: OrchestrationHardCheckStatusEvidenceInsufficient, Reason: "missing_summary"}
	}
	if input.Result.ChangedFiles == nil || input.Result.Artifacts == nil || input.Result.Tests == nil || input.Result.Risks == nil {
		return OrchestrationHardCheckResult{Status: OrchestrationHardCheckStatusEvidenceInsufficient, Reason: "missing_required_evidence"}
	}
	for _, test := range input.Result.Tests {
		if test.Status == OrchestrationTaskTestStatusFailed {
			return OrchestrationHardCheckResult{Status: OrchestrationHardCheckStatusApprovalRequired, Reason: "tests_failed"}
		}
	}
	if len(input.Result.Risks) > 0 {
		return OrchestrationHardCheckResult{Status: OrchestrationHardCheckStatusApprovalRequired, Reason: "risks_present"}
	}
	return OrchestrationHardCheckResult{Status: OrchestrationHardCheckStatusSucceeded}
}

type OrchestrationNodeDefinition struct {
	Key          OrchestrationNodeKey
	Kind         OrchestrationNodeKind
	Status       OrchestrationNodeStatus
	Position     int32
	Dependencies []OrchestrationNodeKey
}

func (d OrchestrationNodeDefinition) Validate() error {
	if !validNodeKey(d.Key) {
		return fmt.Errorf("invalid orchestration node key %q", d.Key)
	}
	if !validNodeKind(d.Kind) {
		return fmt.Errorf("invalid orchestration node kind %q", d.Kind)
	}
	if !validNodeStatus(d.Status) {
		return fmt.Errorf("invalid orchestration node status %q", d.Status)
	}
	if d.Position < 1 {
		return fmt.Errorf("orchestration node position must be positive")
	}
	for _, dep := range d.Dependencies {
		if !validNodeKey(dep) {
			return fmt.Errorf("invalid orchestration node dependency %q", dep)
		}
	}
	return nil
}

func (d OrchestrationNodeDefinition) DependencyStrings() []string {
	deps := make([]string, len(d.Dependencies))
	for i, dep := range d.Dependencies {
		deps[i] = string(dep)
	}
	return deps
}

func DefaultInitialOrchestrationNodes() []OrchestrationNodeDefinition {
	nodes := []OrchestrationNodeDefinition{
		{
			Key:          OrchestrationNodeKeyPlan,
			Kind:         OrchestrationNodeKindPlan,
			Status:       OrchestrationNodeStatusReady,
			Position:     1,
			Dependencies: []OrchestrationNodeKey{},
		},
		{
			Key:          OrchestrationNodeKeyExecute,
			Kind:         OrchestrationNodeKindExecute,
			Status:       OrchestrationNodeStatusPending,
			Position:     2,
			Dependencies: []OrchestrationNodeKey{OrchestrationNodeKeyPlan},
		},
		{
			Key:          OrchestrationNodeKeyVerify,
			Kind:         OrchestrationNodeKindVerify,
			Status:       OrchestrationNodeStatusPending,
			Position:     3,
			Dependencies: []OrchestrationNodeKey{OrchestrationNodeKeyExecute},
		},
	}
	out := make([]OrchestrationNodeDefinition, len(nodes))
	copy(out, nodes)
	return out
}

func validNodeKey(key OrchestrationNodeKey) bool {
	switch key {
	case OrchestrationNodeKeyPlan, OrchestrationNodeKeyExecute, OrchestrationNodeKeyVerify:
		return true
	default:
		return false
	}
}

func validNodeKind(kind OrchestrationNodeKind) bool {
	switch kind {
	case OrchestrationNodeKindPlan, OrchestrationNodeKindExecute, OrchestrationNodeKindVerify:
		return true
	default:
		return false
	}
}

func validNodeStatus(status OrchestrationNodeStatus) bool {
	switch status {
	case OrchestrationNodeStatusPending,
		OrchestrationNodeStatusReady,
		OrchestrationNodeStatusRunning,
		OrchestrationNodeStatusWaiting,
		OrchestrationNodeStatusSucceeded,
		OrchestrationNodeStatusFailed,
		OrchestrationNodeStatusCancelled:
		return true
	default:
		return false
	}
}

func validTaskTestStatus(status OrchestrationTaskTestStatus) bool {
	switch status {
	case OrchestrationTaskTestStatusPassed,
		OrchestrationTaskTestStatusFailed,
		OrchestrationTaskTestStatusSkipped:
		return true
	default:
		return false
	}
}
