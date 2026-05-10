package service

import "testing"

func TestParseAgentResultPayloadStructuredValid(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{
		"status":"completed",
		"summary":"Implemented kernel evidence checks.",
		"changed_files":["server/internal/service/orchestrator.go"],
		"criteria_evidence":[{"criterion":"has evidence","evidence":"changed_files present"}],
		"confidence":0.8
	}`), ResultParseOptions{AllowLegacyCompatibility: true})

	if !validation.Valid {
		t.Fatalf("expected valid result, got errors: %#v", validation.Errors)
	}
	if validation.CompatibilityMode {
		t.Fatal("structured payload must not use compatibility mode")
	}
	if validation.Result.Status != "completed" || validation.Result.Summary == "" {
		t.Fatalf("unexpected result: %#v", validation.Result)
	}
}

func TestParseAgentResultPayloadRejectsUnknownArtifactType(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{
		"status":"completed",
		"summary":"done",
		"artifacts":[{"type":"mystery","content":{}}],
		"criteria_evidence":[{"criterion":"c","evidence":"e"}]
	}`), ResultParseOptions{AllowLegacyCompatibility: true})

	if validation.Valid {
		t.Fatal("unknown artifact type should be invalid")
	}
	if !hasValidationCode(validation.Errors, "unknown_artifact_type") {
		t.Fatalf("expected unknown_artifact_type, got %#v", validation.Errors)
	}
}

func TestParseAgentResultPayloadRejectsInvalidConfidence(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{
		"status":"completed",
		"summary":"done",
		"confidence":1.2,
		"criteria_evidence":[{"criterion":"c","evidence":"e"}]
	}`), ResultParseOptions{AllowLegacyCompatibility: true})

	if validation.Valid {
		t.Fatal("confidence above 1 should be invalid")
	}
	if !hasValidationCode(validation.Errors, "invalid_confidence") {
		t.Fatalf("expected invalid_confidence, got %#v", validation.Errors)
	}
}

func TestParseAgentResultPayloadRejectsCompletedWithoutSummary(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{
		"status":"completed",
		"changed_files":["a.go"],
		"criteria_evidence":[{"criterion":"c","evidence":"e"}]
	}`), ResultParseOptions{AllowLegacyCompatibility: true})

	if validation.Valid {
		t.Fatal("completed result without summary should be invalid")
	}
	if !hasValidationCode(validation.Errors, "missing_summary") {
		t.Fatalf("expected missing_summary, got %#v", validation.Errors)
	}
}

func TestParseAgentResultPayloadLegacyCompatibility(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{"output":"legacy summary"}`), ResultParseOptions{AllowLegacyCompatibility: true})

	if !validation.Valid {
		t.Fatalf("legacy output should parse in compatibility mode: %#v", validation.Errors)
	}
	if !validation.CompatibilityMode {
		t.Fatal("expected compatibility mode")
	}
	if validation.Result.Status != "completed" || validation.Result.Summary != "legacy summary" {
		t.Fatalf("unexpected legacy result: %#v", validation.Result)
	}
}

func TestParseAgentResultPayloadRejectsLegacyWhenDisabled(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{"output":"legacy summary"}`), ResultParseOptions{AllowLegacyCompatibility: false})

	if validation.Valid {
		t.Fatal("legacy output should be invalid when compatibility is disabled")
	}
	if !hasValidationCode(validation.Errors, "missing_status") {
		t.Fatalf("expected missing_status, got %#v", validation.Errors)
	}
}

func TestParseAgentResultPayloadNeedsHumanRequiresNextActionOrRisk(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{
		"status":"needs_human",
		"summary":"Need product confirmation."
	}`), ResultParseOptions{AllowLegacyCompatibility: true})

	if validation.Valid {
		t.Fatal("needs_human without next_actions or risks should be invalid")
	}
	if !hasValidationCode(validation.Errors, "missing_human_followup") {
		t.Fatalf("expected missing_human_followup, got %#v", validation.Errors)
	}
}

func TestNormalizeArtifactsRejectsEmptyTypeForStructuredPayload(t *testing.T) {
	validation := ParseAgentResultPayload([]byte(`{
		"status":"completed",
		"summary":"done",
		"artifacts":[{"content":{}}],
		"criteria_evidence":[{"criterion":"c","evidence":"e"}]
	}`), ResultParseOptions{AllowLegacyCompatibility: true})

	if validation.Valid {
		t.Fatal("empty artifact type should be invalid")
	}
	if !hasValidationCode(validation.Errors, "missing_artifact_type") {
		t.Fatalf("expected missing_artifact_type, got %#v", validation.Errors)
	}
}

func hasValidationCode(errors []ValidationError, code string) bool {
	for _, err := range errors {
		if err.Code == code {
			return true
		}
	}
	return false
}
