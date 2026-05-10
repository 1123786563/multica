package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

var allowedResultStatuses = map[string]bool{
	"completed":   true,
	"failed":      true,
	"blocked":     true,
	"needs_human": true,
}

var allowedArtifactTypes = map[string]bool{
	"diff":           true,
	"file":           true,
	"log":            true,
	"test_result":    true,
	"pr":             true,
	"decision":       true,
	"review_result":  true,
	"command_output": true,
	"summary":        true,
}

type ResultParseOptions struct {
	AllowLegacyCompatibility bool
}

type ValidationError struct {
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

type ResultValidation struct {
	Valid             bool                  `json:"valid"`
	Result            AgentStructuredResult `json:"-"`
	Errors            []ValidationError     `json:"errors,omitempty"`
	CompatibilityMode bool                  `json:"compatibility_mode"`
}

type AgentResultArtifact struct {
	Type     string          `json:"type"`
	URI      string          `json:"uri,omitempty"`
	Content  json.RawMessage `json:"content,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type AgentStructuredResult struct {
	Status           string                `json:"status"`
	Summary          string                `json:"summary"`
	Artifacts        []AgentResultArtifact `json:"artifacts"`
	ChangedFiles     []string              `json:"changed_files"`
	TestResult       json.RawMessage       `json:"test_result"`
	Claims           []string              `json:"claims"`
	CriteriaEvidence []CriteriaEvidence    `json:"criteria_evidence"`
	Risks            []string              `json:"risks"`
	NextActions      []string              `json:"next_actions"`
	Confidence       float64               `json:"confidence"`
}

type CriteriaEvidence struct {
	Criterion    string   `json:"criterion"`
	Evidence     string   `json:"evidence"`
	ArtifactRefs []string `json:"artifact_refs,omitempty"`
}

type legacyTaskPayload struct {
	Output string `json:"output"`
}

func ParseAgentResultPayload(raw []byte, opts ResultParseOptions) ResultValidation {
	var validation ResultValidation
	if len(raw) == 0 {
		validation.Errors = append(validation.Errors, validationError("empty_payload", "", "result payload is empty"))
		return validation
	}

	var result AgentStructuredResult
	if err := json.Unmarshal(raw, &result); err != nil {
		validation.Errors = append(validation.Errors, validationError("invalid_json", "", err.Error()))
		return validation
	}

	if result.Status == "" && opts.AllowLegacyCompatibility {
		var legacy legacyTaskPayload
		if err := json.Unmarshal(raw, &legacy); err == nil && strings.TrimSpace(legacy.Output) != "" {
			result.Status = "completed"
			result.Summary = strings.TrimSpace(legacy.Output)
			validation.CompatibilityMode = true
		}
	}
	result.Status = strings.TrimSpace(result.Status)

	validation.Result = result
	validation.Errors = validateAgentStructuredResult(result)
	validation.Valid = len(validation.Errors) == 0
	return validation
}

func validateAgentStructuredResult(result AgentStructuredResult) []ValidationError {
	var errs []ValidationError
	status := strings.TrimSpace(result.Status)
	if status == "" {
		errs = append(errs, validationError("missing_status", "status", "status is required"))
	} else if !allowedResultStatuses[status] {
		errs = append(errs, validationError("invalid_status", "status", fmt.Sprintf("unsupported status %q", status)))
	}
	if status == "completed" && strings.TrimSpace(result.Summary) == "" {
		errs = append(errs, validationError("missing_summary", "summary", "completed result requires summary"))
	}
	if result.Confidence < 0 || result.Confidence > 1 {
		errs = append(errs, validationError("invalid_confidence", "confidence", "confidence must be between 0 and 1"))
	}
	for i, artifact := range result.Artifacts {
		field := fmt.Sprintf("artifacts[%d].type", i)
		if strings.TrimSpace(artifact.Type) == "" {
			errs = append(errs, validationError("missing_artifact_type", field, "artifact type is required"))
			continue
		}
		if !allowedArtifactTypes[artifact.Type] {
			errs = append(errs, validationError("unknown_artifact_type", field, fmt.Sprintf("unsupported artifact type %q", artifact.Type)))
		}
	}
	if status == "failed" && strings.TrimSpace(result.Summary) == "" && len(result.Risks) == 0 {
		errs = append(errs, validationError("missing_failure_summary", "summary", "failed result requires summary or risks"))
	}
	if status == "blocked" && strings.TrimSpace(result.Summary) == "" && len(result.Risks) == 0 {
		errs = append(errs, validationError("missing_blocked_summary", "summary", "blocked result requires summary or risks"))
	}
	if status == "needs_human" && (strings.TrimSpace(result.Summary) == "" || (len(result.NextActions) == 0 && len(result.Risks) == 0)) {
		errs = append(errs, validationError("missing_human_followup", "next_actions", "needs_human result requires summary and next_actions or risks"))
	}
	return errs
}

func validationError(code, field, message string) ValidationError {
	return ValidationError{Code: code, Field: field, Message: message}
}

func NormalizeArtifacts(result AgentStructuredResult) []AgentResultArtifact {
	artifacts := append([]AgentResultArtifact{}, result.Artifacts...)
	if len(result.ChangedFiles) > 0 {
		content, _ := json.Marshal(map[string]any{"changed_files": result.ChangedFiles})
		artifacts = append(artifacts, AgentResultArtifact{Type: "diff", Content: content})
	}
	if len(result.TestResult) > 0 {
		artifacts = append(artifacts, AgentResultArtifact{Type: "test_result", Content: result.TestResult})
	}
	if strings.TrimSpace(result.Summary) != "" {
		content, _ := json.Marshal(map[string]string{"summary": result.Summary})
		artifacts = append(artifacts, AgentResultArtifact{Type: "summary", Content: content})
	}
	return artifacts
}

func hasArtifactType(artifacts []AgentResultArtifact, artifactType string) bool {
	for _, artifact := range artifacts {
		if artifact.Type == artifactType {
			return true
		}
	}
	return false
}

func testResultFailed(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var result struct {
		Status  string `json:"status"`
		Passed  *bool  `json:"passed"`
		Success *bool  `json:"success"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return false
	}
	if strings.EqualFold(result.Status, "failed") || strings.EqualFold(result.Status, "failure") {
		return true
	}
	if result.Passed != nil && !*result.Passed {
		return true
	}
	return result.Success != nil && !*result.Success
}

func criteriaEvidenceValid(items []CriteriaEvidence) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if strings.TrimSpace(item.Criterion) == "" || strings.TrimSpace(item.Evidence) == "" {
			return false
		}
	}
	return true
}
