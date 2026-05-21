package orchestration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestNewEinoIssueAnalyzerRequiresConfiguredProvider(t *testing.T) {
	_, err := NewEinoIssueAnalyzer(t.Context(), EinoReasoningConfig{
		Provider: "openai-compatible",
	})
	if err == nil {
		t.Fatal("NewEinoIssueAnalyzer should reject missing provider credentials")
	}
	msg := err.Error()
	if !strings.Contains(msg, "api key") || !strings.Contains(msg, "model") {
		t.Fatalf("configuration error should mention api key and model, got %q", msg)
	}
}

func TestParseEinoAnalyzeIssueOutputAcceptsStrictJSON(t *testing.T) {
	result, err := parseEinoAnalyzeIssueOutput(`{
		"problem_summary": "Fix orchestration analyzer wiring",
		"execution_advice": "Keep the change scoped to the worker analyzer",
		"suspected_context": "server/internal/orchestration",
		"risks": ["provider output may drift"],
		"recommended_agent_prompt": "Implement the analyzer with tests",
		"reason_code": "eino_analysis_ready",
		"recommended_action": "none"
	}`)
	if err != nil {
		t.Fatalf("parseEinoAnalyzeIssueOutput returned error: %v", err)
	}
	if result.ProblemSummary != "Fix orchestration analyzer wiring" {
		t.Fatalf("unexpected parsed result: %+v", result)
	}
	if len(result.Risks) != 1 || result.Risks[0] != "provider output may drift" {
		t.Fatalf("risks were not preserved: %+v", result.Risks)
	}
}

func TestParseEinoAnalyzeIssueOutputRejectsProseAndTopologyFields(t *testing.T) {
	for _, raw := range []string{
		"Here is the analysis: fix the analyzer.",
		`{
			"problem_summary": "Fix it",
			"execution_advice": "Do it",
			"suspected_context": "server",
			"recommended_agent_prompt": "Patch code",
			"reason_code": "eino_analysis_ready",
			"recommended_action": "none"
		}`,
		`{
			"problem_summary": "Fix it",
			"execution_advice": "Do it",
			"suspected_context": "server",
			"risks": [],
			"recommended_agent_prompt": "Patch code",
			"reason_code": "eino_analysis_ready",
			"recommended_action": "none",
			"nodes": [{"id": "extra"}]
		}`,
		`{
			"problem_summary": "Fix it",
			"execution_advice": "Do it",
			"suspected_context": "server",
			"risks": [],
			"recommended_agent_prompt": "Patch code",
			"reason_code": "eino_analysis_ready",
			"recommended_action": "none",
			"final_success": true
		}`,
		`{
			"problem_summary": "Fix it",
			"execution_advice": "Do it",
			"suspected_context": "server",
			"risks": [],
			"recommended_agent_prompt": "Patch code",
			"reason_code": "eino_analysis_ready",
			"recommended_action": "none"
		} {"extra": true}`,
	} {
		if _, err := parseEinoAnalyzeIssueOutput(raw); err == nil {
			t.Fatalf("parseEinoAnalyzeIssueOutput should reject %s", raw)
		}
	}
}

func TestEinoIssueAnalyzerParsesChatModelResponse(t *testing.T) {
	analyzer := EinoIssueAnalyzer{
		model: fakeEinoChatModel{content: `{
			"problem_summary": "Use real Eino",
			"execution_advice": "Keep topology fixed",
			"suspected_context": "server/internal/orchestration",
			"risks": [],
			"recommended_agent_prompt": "Patch the worker analyzer",
			"reason_code": "eino_analysis_ready",
			"recommended_action": "none"
		}`},
		maxOutputTokens: 1200,
	}
	result, err := analyzer.AnalyzeIssue(t.Context(), IssueSnapshot{Title: "wire Eino"}, IssueWorkflowInput{})
	if err != nil {
		t.Fatalf("AnalyzeIssue returned error: %v", err)
	}
	if result.RecommendedAgentPrompt != "Patch the worker analyzer" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestEinoIssueAnalyzerRejectsMalformedChatModelResponse(t *testing.T) {
	analyzer := EinoIssueAnalyzer{
		model:           fakeEinoChatModel{content: "analysis: patch it"},
		maxOutputTokens: 1200,
	}
	if _, err := analyzer.AnalyzeIssue(t.Context(), IssueSnapshot{Title: "wire Eino"}, IssueWorkflowInput{}); err == nil {
		t.Fatal("AnalyzeIssue should reject malformed provider output")
	}
}

func TestEinoIssueAnalyzerLiveProviderSmoke(t *testing.T) {
	if os.Getenv("ORCHESTRATION_EINO_LIVE_TEST") != "1" {
		t.Skip("set ORCHESTRATION_EINO_LIVE_TEST=1 to run live Eino provider smoke test")
	}
	apiKey := strings.TrimSpace(os.Getenv("ORCHESTRATION_EINO_API_KEY"))
	model := strings.TrimSpace(os.Getenv("ORCHESTRATION_EINO_MODEL"))
	var missing []string
	if apiKey == "" {
		missing = append(missing, "ORCHESTRATION_EINO_API_KEY")
	}
	if model == "" {
		missing = append(missing, "ORCHESTRATION_EINO_MODEL")
	}
	if len(missing) > 0 {
		t.Fatalf("live Eino provider smoke test missing %s", strings.Join(missing, " and "))
	}
	timeout := 60 * time.Second
	if raw := strings.TrimSpace(os.Getenv("ORCHESTRATION_EINO_TIMEOUT")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			t.Fatalf("invalid ORCHESTRATION_EINO_TIMEOUT %q: %v", raw, err)
		}
		timeout = parsed
	}

	analyzer, err := NewEinoIssueAnalyzer(t.Context(), EinoReasoningConfig{
		Provider: EinoProviderOpenAICompatible,
		APIKey:   apiKey,
		Model:    model,
		BaseURL:  strings.TrimSpace(os.Getenv("ORCHESTRATION_EINO_BASE_URL")),
		Timeout:  timeout,
	})
	if err != nil {
		t.Fatalf("NewEinoIssueAnalyzer returned error: %v", err)
	}
	result, err := analyzer.AnalyzeIssue(t.Context(), IssueSnapshot{
		IssueID:        "live-smoke",
		Title:          "Verify AnalyzeIssue live provider wiring",
		Description:    "Return strict JSON for a narrow orchestration analyzer smoke test.",
		AcceptanceText: "The response must satisfy the AnalyzeIssue structured output contract.",
		Priority:       "medium",
		Status:         "todo",
		AssigneeType:   "agent",
	}, IssueWorkflowInput{
		WorkspaceID: "live-smoke-workspace",
		PlanID:      "live-smoke-plan",
	})
	if err != nil {
		t.Fatalf("AnalyzeIssue live provider call returned error: %v", err)
	}
	if strings.TrimSpace(result.ProblemSummary) == "" ||
		strings.TrimSpace(result.ExecutionAdvice) == "" ||
		strings.TrimSpace(result.SuspectedContext) == "" ||
		strings.TrimSpace(result.RecommendedAgentPrompt) == "" ||
		strings.TrimSpace(result.ReasonCode) == "" ||
		strings.TrimSpace(result.RecommendedAction) == "" {
		t.Fatalf("AnalyzeIssue live provider returned incomplete result: %+v", result)
	}
}

type fakeEinoChatModel struct {
	content string
	err     error
}

func (m fakeEinoChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	if m.err != nil {
		return nil, m.err
	}
	return schema.AssistantMessage(m.content, nil), nil
}

func (m fakeEinoChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}
