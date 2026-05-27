package orchestration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/multica-ai/multica/server/internal/service"
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

func TestNewWorkerEinoReasonerRejectsStaticWithoutExplicitAllow(t *testing.T) {
	_, err := newWorkerEinoReasoner(t.Context(), EinoReasoningConfig{
		Provider: EinoProviderStatic,
	})
	if err == nil {
		t.Fatal("newWorkerEinoReasoner should reject static provider unless explicitly allowed")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "static") || !strings.Contains(msg, "allow") {
		t.Fatalf("static provider guard error should mention static allow gate, got %q", msg)
	}
}

func TestNewWorkerEinoReasonerAllowsExplicitStatic(t *testing.T) {
	reasoner, err := newWorkerEinoReasoner(t.Context(), EinoReasoningConfig{
		Provider:    EinoProviderStatic,
		AllowStatic: true,
	})
	if err != nil {
		t.Fatalf("newWorkerEinoReasoner returned error: %v", err)
	}
	if _, ok := reasoner.(StaticEinoReasoner); !ok {
		t.Fatalf("expected StaticEinoReasoner, got %T", reasoner)
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

func TestParseEinoReviewOutcomeOutputAcceptsStrictJSON(t *testing.T) {
	result, err := parseEinoReviewOutcomeOutput(`{
		"summary": "Evidence looks good",
		"high_risk": false,
		"concern": "",
		"severity_label": "low",
		"evidence": ["tests passed"],
		"risks": [],
		"recommended_action": "accept"
	}`)
	if err != nil {
		t.Fatalf("parseEinoReviewOutcomeOutput returned error: %v", err)
	}
	if result.Summary != "Evidence looks good" || result.RecommendedAction != "accept" {
		t.Fatalf("unexpected parsed review result: %+v", result)
	}
	if len(result.Evidence) != 1 || result.Evidence[0] != "tests passed" {
		t.Fatalf("review evidence was not preserved: %+v", result.Evidence)
	}
}

func TestParseEinoReviewOutcomeOutputRejectsAuthoritativeFields(t *testing.T) {
	raw := `{
		"summary":"Evidence looks good",
		"high_risk":false,
		"concern":"",
		"severity_label":"low",
		"evidence":["tests passed"],
		"risks":[],
		"recommended_action":"accept",
		"is_success":true
	}`
	if _, err := parseEinoReviewOutcomeOutput(raw); err == nil {
		t.Fatal("review output with authoritative fields must be rejected")
	}
}

func TestParseEinoSummarizeOutcomeOutputAcceptsStrictJSON(t *testing.T) {
	result, err := parseEinoSummarizeOutcomeOutput(`{
		"summary": "Ready for review",
		"trace_ref": "plan/node/task"
	}`)
	if err != nil {
		t.Fatalf("parseEinoSummarizeOutcomeOutput returned error: %v", err)
	}
	if result.Summary != "Ready for review" || result.TraceRef != "plan/node/task" {
		t.Fatalf("unexpected parsed summary result: %+v", result)
	}
}

func TestParseEinoSummarizeOutcomeOutputRejectsWorkflowAction(t *testing.T) {
	raw := `{
		"summary":"Ready for review",
		"trace_ref":"plan/node/task",
		"terminal_plan_status":"completed"
	}`
	if _, err := parseEinoSummarizeOutcomeOutput(raw); err == nil {
		t.Fatal("summary output with workflow action must be rejected")
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

func TestEinoIssueAnalyzerParsesReviewAndSummarizeResponses(t *testing.T) {
	analyzer := EinoIssueAnalyzer{
		reviewModel: fakeEinoChatModel{content: `{
			"summary": "Review is clean",
			"high_risk": false,
			"concern": "",
			"severity_label": "low",
			"evidence": ["go test ./internal/orchestration"],
			"risks": [],
			"recommended_action": "accept"
		}`},
		summaryModel: fakeEinoChatModel{content: `{
			"summary": "Ready for review",
			"trace_ref": "plan-1/node-1/task-1"
		}`},
		maxOutputTokens: 1200,
	}
	review, err := analyzer.ReviewOutcome(t.Context(), ValidateOutcomeResult{}, AnalyzeIssueResult{}, IssueSnapshot{}, service.DispatchAgentTaskResult{})
	if err != nil {
		t.Fatalf("ReviewOutcome returned error: %v", err)
	}
	if review.Summary != "Review is clean" || review.RecommendedAction != "accept" {
		t.Fatalf("unexpected review result: %+v", review)
	}
	summary, err := analyzer.SummarizeOutcome(t.Context(), review, ValidateOutcomeResult{}, AnalyzeIssueResult{}, IssueSnapshot{}, service.DispatchAgentTaskResult{})
	if err != nil {
		t.Fatalf("SummarizeOutcome returned error: %v", err)
	}
	if summary.TraceRef != "plan-1/node-1/task-1" {
		t.Fatalf("unexpected summary result: %+v", summary)
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

func TestEinoReasonerLiveProviderSmoke(t *testing.T) {
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

	reasoner, err := NewEinoIssueAnalyzer(t.Context(), EinoReasoningConfig{
		Provider:            EinoProviderOpenAICompatible,
		APIKey:              apiKey,
		Model:               model,
		BaseURL:             strings.TrimSpace(os.Getenv("ORCHESTRATION_EINO_BASE_URL")),
		Timeout:             timeout,
		ReasoningProfileRef: DefaultReasoningProfileRef,
	})
	if err != nil {
		t.Fatalf("NewEinoIssueAnalyzer returned error: %v", err)
	}
	issue := IssueSnapshot{
		IssueID:        "live-smoke",
		Title:          "Verify Eino reasoner live provider wiring",
		Description:    "Return strict JSON for a narrow orchestration reasoner smoke test.",
		AcceptanceText: "AnalyzeIssue, ReviewOutcome, and SummarizeOutcome must satisfy strict structured output contracts.",
		Priority:       "medium",
		Status:         "todo",
		AssigneeType:   "agent",
	}
	input := IssueWorkflowInput{
		WorkspaceID:         "live-smoke-workspace",
		PlanID:              "live-smoke-plan",
		ReasoningProfileRef: DefaultReasoningProfileRef,
	}
	analysis, err := reasoner.AnalyzeIssue(t.Context(), issue, input)
	if err != nil {
		t.Fatalf("AnalyzeIssue live provider call returned error: %v", err)
	}
	if strings.TrimSpace(analysis.ProblemSummary) == "" ||
		strings.TrimSpace(analysis.ExecutionAdvice) == "" ||
		strings.TrimSpace(analysis.SuspectedContext) == "" ||
		strings.TrimSpace(analysis.RecommendedAgentPrompt) == "" ||
		strings.TrimSpace(analysis.ReasonCode) == "" ||
		strings.TrimSpace(analysis.RecommendedAction) == "" {
		t.Fatalf("AnalyzeIssue live provider returned incomplete result: %+v", analysis)
	}

	validation := ValidateOutcomeResult{
		Status:             "completed",
		RecommendedAction:  "none",
		TerminalPlanStatus: "completed",
		ProjectionDetail:   "synthetic result validated",
		ResultSummary:      "Synthetic implementation completed with tests.",
		ChangedFiles:       []string{"server/internal/orchestration/eino_analyzer.go"},
		Tests:              []ResultTestRef{{Name: "go test ./internal/orchestration", Status: "passed"}},
		Evidence:           []ResultEvidenceRef{{Type: "test", Ref: "go test ./internal/orchestration"}},
	}
	dispatch := service.DispatchAgentTaskResult{
		PlanID:  "live-smoke-plan",
		NodeID:  "live-smoke-node",
		TaskID:  "live-smoke-task",
		Attempt: 1,
	}
	review, err := reasoner.ReviewOutcome(t.Context(), validation, analysis, issue, dispatch)
	if err != nil {
		t.Fatalf("ReviewOutcome live provider call returned error: %v", err)
	}
	if strings.TrimSpace(review.Summary) == "" ||
		strings.TrimSpace(review.SeverityLabel) == "" ||
		strings.TrimSpace(review.RecommendedAction) == "" {
		t.Fatalf("ReviewOutcome live provider returned incomplete result: %+v", review)
	}

	summary, err := reasoner.SummarizeOutcome(t.Context(), review, validation, analysis, issue, dispatch)
	if err != nil {
		t.Fatalf("SummarizeOutcome live provider call returned error: %v", err)
	}
	if strings.TrimSpace(summary.Summary) == "" || strings.TrimSpace(summary.TraceRef) == "" {
		t.Fatalf("SummarizeOutcome live provider returned incomplete result: %+v", summary)
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
