package orchestration

import (
	"context"
	"strings"
	"testing"

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
