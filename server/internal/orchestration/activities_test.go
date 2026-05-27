package orchestration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/service"
)

type captureArtifactDB struct {
	onArtifact func(sql string, args ...any)
	sqls       []string
	args       [][]any
}

func (m *captureArtifactDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	m.sqls = append(m.sqls, sql)
	m.args = append(m.args, args)
	if m.onArtifact != nil {
		m.onArtifact(sql, args...)
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (m *captureArtifactDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}

func (m *captureArtifactDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return nil
}

func TestFinalizeWorkflowMovesCompletedHandoffToReviewState(t *testing.T) {
	var executedSQL []string
	var executedArgs [][]any
	activity := ActivitySet{
		DB: &captureArtifactDB{onArtifact: func(sql string, args ...any) {
			executedSQL = append(executedSQL, sql)
			executedArgs = append(executedArgs, args)
		}},
		Queries: nil,
	}
	err := activity.FinalizeWorkflow(t.Context(), ValidateOutcomeResult{
		Status:             "completed",
		TerminalPlanStatus: "completed",
	}, ReviewOutcomeResult{
		Summary:           "review clean",
		RecommendedAction: "accept",
	}, SummarizeOutcomeResult{
		Summary:  "done",
		TraceRef: "plan-1/node-1/task-1",
	}, IssueWorkflowInput{
		PlanID:     "00000000-0000-0000-0000-000000000001",
		WorkflowID: "wf-1",
	}, IssueSnapshot{
		IssueID: "00000000-0000-0000-0000-000000000004",
	}, AnalyzeIssueResult{
		ProblemSummary: "Fix the bug",
	}, service.DispatchAgentTaskResult{
		PlanID: "00000000-0000-0000-0000-000000000001",
		NodeID: "00000000-0000-0000-0000-000000000002",
		TaskID: "00000000-0000-0000-0000-000000000003",
	}, service.AgentTaskOutcomeSignalInput{
		WorkflowID: "wf-1",
		Status:     "completed",
	})
	if err != nil {
		t.Fatalf("FinalizeWorkflow returned error: %v", err)
	}
	for i, sql := range executedSQL {
		lower := strings.ToLower(sql)
		if !strings.Contains(lower, "update issue") {
			continue
		}
		for _, arg := range executedArgs[i] {
			if status, ok := arg.(string); ok && status == "in_review" {
				return
			}
		}
		t.Fatalf("completed handoff updated issue without setting in_review: sql=%s args=%v", sql, executedArgs[i])
	}
	t.Fatal("completed handoff must move the issue to in_review")
}

func TestFinalizeWorkflowNeverAutoClosesIssue(t *testing.T) {
	var executedSQL []string
	var executedArgs [][]any
	activity := ActivitySet{
		DB: &captureArtifactDB{onArtifact: func(sql string, args ...any) {
			executedSQL = append(executedSQL, sql)
			executedArgs = append(executedArgs, args)
		}},
		Queries: nil,
	}
	err := activity.FinalizeWorkflow(t.Context(), ValidateOutcomeResult{
		Status:             "completed",
		TerminalPlanStatus: "completed",
	}, ReviewOutcomeResult{
		Summary:           "review clean",
		RecommendedAction: "accept",
	}, SummarizeOutcomeResult{
		Summary:  "done",
		TraceRef: "plan-1/node-1/task-1",
	}, IssueWorkflowInput{
		PlanID:     "00000000-0000-0000-0000-000000000001",
		WorkflowID: "wf-1",
	}, IssueSnapshot{
		IssueID: "00000000-0000-0000-0000-000000000004",
	}, AnalyzeIssueResult{
		ProblemSummary: "Fix the bug",
	}, service.DispatchAgentTaskResult{
		PlanID: "00000000-0000-0000-0000-000000000001",
		NodeID: "00000000-0000-0000-0000-000000000002",
		TaskID: "00000000-0000-0000-0000-000000000003",
	}, service.AgentTaskOutcomeSignalInput{
		WorkflowID: "wf-1",
		Status:     "completed",
	})
	if err != nil {
		t.Fatalf("FinalizeWorkflow returned error: %v", err)
	}
	for i, args := range executedArgs {
		if !strings.Contains(strings.ToLower(executedSQL[i]), "update issue") {
			continue
		}
		for _, arg := range args {
			status, ok := arg.(string)
			if !ok {
				continue
			}
			switch status {
			case "done", "cancelled", "closed":
				t.Fatalf("FinalizeWorkflow must not auto-close issue with status %q", status)
			}
		}
	}
}

func TestProjectEinoFailureRecordsVisibleSafeTrace(t *testing.T) {
	var executedSQL []string
	var executedArgs [][]any
	activity := ActivitySet{
		DB: &captureArtifactDB{onArtifact: func(sql string, args ...any) {
			executedSQL = append(executedSQL, sql)
			executedArgs = append(executedArgs, args)
		}},
	}

	err := activity.ProjectEinoFailure(t.Context(), EinoFailureProjectionInput{
		PlanID:              "00000000-0000-0000-0000-000000000001",
		WorkflowNodeKey:     "review",
		Attempt:             2,
		ReasonCode:          EinoReasonProviderTimeout,
		RecommendedAction:   "retry_later",
		Message:             "provider timed out",
		ReasoningProfileRef: "worker-default",
		SchemaName:          "multica_review_outcome",
		SchemaVersion:       "1",
		ProviderLabel:       "openai-compatible",
		Model:               "configured-model",
		CapabilityMode:      "json_schema",
		LatencyMS:           1500,
	})
	if err != nil {
		t.Fatalf("ProjectEinoFailure returned error: %v", err)
	}

	var sawNode, sawPlan, sawEvent, sawTrace bool
	for i, sql := range executedSQL {
		lower := strings.ToLower(sql)
		args := executedArgs[i]
		if strings.Contains(lower, "orchestration_node") && containsArg(args, "review") && containsArg(args, "failed") && containsIntArg(args, 2) {
			sawNode = true
		}
		if strings.Contains(lower, "update orchestration_plan") &&
			containsArg(args, EinoReasonProviderTimeout) &&
			containsArg(args, "retry_later") {
			sawPlan = true
		}
		if strings.Contains(lower, "orchestration_event") && containsArg(args, "eino.provider_failed") {
			sawEvent = true
		}
		if strings.Contains(lower, "orchestration_artifact") && containsArg(args, "provider_failure_trace") {
			for _, arg := range args {
				raw, ok := arg.([]byte)
				if !ok {
					continue
				}
				var trace map[string]any
				if err := json.Unmarshal(raw, &trace); err != nil {
					t.Fatalf("trace artifact data is not json: %v", err)
				}
				failure, _ := trace["failure"].(map[string]any)
				if trace["reasoning_profile_ref"] == "worker-default" &&
					trace["schema_name"] == "multica_review_outcome" &&
					trace["provider_label"] == "openai-compatible" &&
					failure["reason_code"] == EinoReasonProviderTimeout &&
					trace["raw_prompt"] == nil &&
					trace["raw_response"] == nil {
					sawTrace = true
				}
			}
		}
	}
	if !sawNode || !sawPlan || !sawEvent || !sawTrace {
		t.Fatalf("ProjectEinoFailure missing projection parts: node=%v plan=%v event=%v trace=%v sql=%v args=%v", sawNode, sawPlan, sawEvent, sawTrace, executedSQL, executedArgs)
	}
}

func TestAnalyzeIssueProjectsSafeProviderTrace(t *testing.T) {
	activity := ActivitySet{
		DB: &captureArtifactDB{},
		Reasoner: stubEinoReasoner{
			trace: testEinoTrace("multica_analyze_issue"),
			analyzeResult: AnalyzeIssueResult{
				ProblemSummary:         "Fix issue",
				ExecutionAdvice:        "Inspect focused files",
				SuspectedContext:       "server/internal/orchestration",
				RecommendedAgentPrompt: "Fix the issue",
				ReasonCode:             "eino_analysis_ready",
				RecommendedAction:      "none",
			},
		},
	}
	result, err := activity.AnalyzeIssue(t.Context(), IssueSnapshot{Title: "Fix issue"}, IssueWorkflowInput{
		PlanID:              "00000000-0000-0000-0000-000000000001",
		ReasoningProfileRef: "worker-default",
	})
	if err != nil {
		t.Fatalf("AnalyzeIssue returned error: %v", err)
	}
	if result.Trace.SchemaName != "multica_analyze_issue" {
		t.Fatalf("AnalyzeIssue result missing trace: %+v", result.Trace)
	}
	trace := traceArtifactData(t, activity.DB.(*captureArtifactDB), "analysis_trace")
	assertSafeTrace(t, trace, "multica_analyze_issue")
	parsed, _ := trace["parsed_output"].(map[string]any)
	if parsed["problem_summary"] != "Fix issue" {
		t.Fatalf("analysis trace parsed output missing safe summary: %+v", parsed)
	}
}

func TestReviewOutcomeProjectsSafeProviderTrace(t *testing.T) {
	activity := ActivitySet{
		DB: &captureArtifactDB{},
		Reasoner: stubEinoReasoner{
			trace: testEinoTrace("multica_review_outcome"),
			reviewResult: ReviewOutcomeResult{
				Summary:           "Review is clean",
				SeverityLabel:     "low",
				RecommendedAction: "accept",
			},
		},
	}
	result, err := activity.ReviewOutcome(t.Context(), ValidateOutcomeResult{
		Status:             "completed",
		TerminalPlanStatus: "completed",
	}, AnalyzeIssueResult{ProblemSummary: "Fix issue"}, IssueSnapshot{}, service.DispatchAgentTaskResult{
		PlanID:  "00000000-0000-0000-0000-000000000001",
		NodeID:  "00000000-0000-0000-0000-000000000002",
		TaskID:  "00000000-0000-0000-0000-000000000003",
		Attempt: 1,
	})
	if err != nil {
		t.Fatalf("ReviewOutcome returned error: %v", err)
	}
	if result.Trace.SchemaName != "multica_review_outcome" {
		t.Fatalf("ReviewOutcome result missing trace: %+v", result.Trace)
	}
	trace := traceArtifactData(t, activity.DB.(*captureArtifactDB), "review_trace")
	assertSafeTrace(t, trace, "multica_review_outcome")
	parsed, _ := trace["parsed_output"].(map[string]any)
	if parsed["summary"] != "Review is clean" {
		t.Fatalf("review trace parsed output missing safe summary: %+v", parsed)
	}
}

func TestSummarizeOutcomeProjectsSafeProviderTrace(t *testing.T) {
	activity := ActivitySet{
		DB: &captureArtifactDB{},
		Reasoner: stubEinoReasoner{
			trace: testEinoTrace("multica_summarize_outcome"),
			summaryResult: SummarizeOutcomeResult{
				Summary:  "Ready for review",
				TraceRef: "plan/node/task",
			},
		},
	}
	result, err := activity.SummarizeOutcome(t.Context(), ReviewOutcomeResult{}, ValidateOutcomeResult{}, AnalyzeIssueResult{}, IssueSnapshot{}, service.DispatchAgentTaskResult{
		PlanID:  "00000000-0000-0000-0000-000000000001",
		NodeID:  "00000000-0000-0000-0000-000000000002",
		TaskID:  "00000000-0000-0000-0000-000000000003",
		Attempt: 1,
	})
	if err != nil {
		t.Fatalf("SummarizeOutcome returned error: %v", err)
	}
	if result.Trace.SchemaName != "multica_summarize_outcome" {
		t.Fatalf("SummarizeOutcome result missing trace: %+v", result.Trace)
	}
	trace := traceArtifactData(t, activity.DB.(*captureArtifactDB), "summary_trace")
	assertSafeTrace(t, trace, "multica_summarize_outcome")
	parsed, _ := trace["parsed_output"].(map[string]any)
	if parsed["summary"] != "Ready for review" || parsed["trace_ref"] != "plan/node/task" {
		t.Fatalf("summary trace parsed output missing safe values: %+v", parsed)
	}
}

func testEinoTrace(schemaName string) EinoTrace {
	return EinoTrace{
		ReasoningProfileRef: "worker-default",
		PromptProfileRef:    strings.TrimPrefix(schemaName, "multica_") + "/v1",
		SchemaName:          schemaName,
		SchemaVersion:       "1",
		ProviderLabel:       "openai-compatible",
		Model:               "test-model",
		CapabilityMode:      "json_schema",
		LatencyMS:           12,
		InputTokens:         10,
		OutputTokens:        20,
		TotalTokens:         30,
	}
}

func traceArtifactData(t *testing.T, db *captureArtifactDB, artifactType string) map[string]any {
	t.Helper()
	var data map[string]any
	for _, args := range db.args {
		if !containsArg(args, artifactType) {
			continue
		}
		for _, arg := range args {
			raw, ok := arg.([]byte)
			if !ok {
				continue
			}
			var decoded map[string]any
			if json.Unmarshal(raw, &decoded) == nil {
				data = decoded
			}
		}
	}
	if data == nil {
		t.Fatalf("missing %s artifact data", artifactType)
	}
	return data
}

func assertSafeTrace(t *testing.T, trace map[string]any, schemaName string) {
	t.Helper()
	if trace["reasoning_profile_ref"] != "worker-default" ||
		trace["schema_name"] != schemaName ||
		trace["provider_label"] != "openai-compatible" ||
		trace["model"] != "test-model" ||
		trace["capability_mode"] != "json_schema" {
		t.Fatalf("trace metadata mismatch: %+v", trace)
	}
	if trace["raw_prompt"] != nil || trace["raw_response"] != nil || trace["api_key"] != nil || trace["authorization"] != nil {
		t.Fatalf("trace contains unsafe raw/provider secret fields: %+v", trace)
	}
}

type stubEinoReasoner struct {
	analyzeResult AnalyzeIssueResult
	analyzeErr    error
	reviewResult  ReviewOutcomeResult
	reviewErr     error
	summaryResult SummarizeOutcomeResult
	summaryErr    error
	trace         EinoTrace
}

func (r stubEinoReasoner) AnalyzeIssue(ctx context.Context, issue IssueSnapshot, input IssueWorkflowInput) (AnalyzeIssueResult, error) {
	result := r.analyzeResult
	result.Trace = r.trace
	return result, r.analyzeErr
}

func (r stubEinoReasoner) ReviewOutcome(ctx context.Context, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (ReviewOutcomeResult, error) {
	result := r.reviewResult
	result.Trace = r.trace
	return result, r.reviewErr
}

func (r stubEinoReasoner) SummarizeOutcome(ctx context.Context, review ReviewOutcomeResult, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (SummarizeOutcomeResult, error) {
	result := r.summaryResult
	result.Trace = r.trace
	return result, r.summaryErr
}

func TestAnalyzeIssueUsesInjectedAnalyzerAdapter(t *testing.T) {
	var projected bool
	var projectedAnalyzeNode bool
	var projectedRisks bool
	activity := ActivitySet{
		DB: &captureArtifactDB{onArtifact: func(sql string, args ...any) {
			lower := strings.ToLower(sql)
			if strings.Contains(lower, "orchestration_event") {
				projected = true
				for _, arg := range args {
					if raw, ok := arg.([]byte); ok && strings.Contains(string(raw), `"adapter risk"`) {
						projectedRisks = true
					}
				}
			}
			if strings.Contains(lower, "orchestration_node") && containsArg(args, "analyze") && containsArg(args, "completed") {
				projectedAnalyzeNode = true
			}
		}},
		Reasoner: stubEinoReasoner{analyzeResult: AnalyzeIssueResult{
			ProblemSummary:         "Adapter summary",
			ExecutionAdvice:        "Adapter advice",
			SuspectedContext:       "Adapter context",
			Risks:                  []string{"adapter risk"},
			RecommendedAgentPrompt: "Adapter prompt",
			ReasonCode:             "adapter_ready",
			RecommendedAction:      "none",
		}},
	}
	result, err := activity.AnalyzeIssue(t.Context(), IssueSnapshot{
		IssueID: "00000000-0000-0000-0000-000000000004",
		Title:   "Use adapter",
	}, IssueWorkflowInput{
		PlanID: "00000000-0000-0000-0000-000000000001",
	})
	if err != nil {
		t.Fatalf("AnalyzeIssue returned error: %v", err)
	}
	if result.ProblemSummary != "Adapter summary" || !strings.Contains(result.RecommendedAgentPrompt, "Adapter prompt") {
		t.Fatalf("AnalyzeIssue did not use injected analyzer adapter: %+v", result)
	}
	if !strings.Contains(result.RecommendedAgentPrompt, "Authoritative Result Schema v1 contract:") ||
		!strings.Contains(result.RecommendedAgentPrompt, `"evidence":[{"type":"test","ref":"npm test"}]`) ||
		!strings.Contains(result.RecommendedAgentPrompt, "tests must be an array") {
		t.Fatalf("AnalyzeIssue should append the canonical Result Schema v1 contract: %s", result.RecommendedAgentPrompt)
	}
	if !projected {
		t.Fatal("AnalyzeIssue should project adapter output")
	}
	if !projectedRisks {
		t.Fatal("AnalyzeIssue should project analyzer risks for visible advisory policy")
	}
	if !projectedAnalyzeNode {
		t.Fatal("AnalyzeIssue should project the analyze node as completed")
	}
}

func TestValidateOutcomeProjectsValidateNodeState(t *testing.T) {
	var projectedValidateNode bool
	activity := ActivitySet{
		DB: &captureArtifactDB{onArtifact: func(sql string, args ...any) {
			if strings.Contains(strings.ToLower(sql), "orchestration_node") && containsArg(args, "validate") && containsArg(args, "completed") {
				projectedValidateNode = true
			}
		}},
	}
	result, err := activity.ValidateOutcome(t.Context(), ValidateOutcomeInput{
		Outcome: service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "wf-1",
			PlanID:         "00000000-0000-0000-0000-000000000001",
			NodeID:         "00000000-0000-0000-0000-000000000002",
			TaskID:         "00000000-0000-0000-0000-000000000003",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"schema_version":"1","summary":"done","changed_files":["server/internal/orchestration/activities.go"],"artifacts":[],"tests":[{"name":"go test ./internal/orchestration","status":"passed"}],"risks":[],"evidence":[{"type":"test","ref":"go test ./internal/orchestration"}]}`),
		},
		Analysis: AnalyzeIssueResult{ProblemSummary: "Fix issue"},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  "00000000-0000-0000-0000-000000000001",
			NodeID:  "00000000-0000-0000-0000-000000000002",
			TaskID:  "00000000-0000-0000-0000-000000000003",
			Attempt: 1,
		},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("validation status = %q, want completed", result.Status)
	}
	if !projectedValidateNode {
		t.Fatal("ValidateOutcome should project the validate node as completed")
	}
}

func TestReviewOutcomeProjectsReviewNodeState(t *testing.T) {
	var projectedReviewNode bool
	activity := ActivitySet{
		DB: &captureArtifactDB{onArtifact: func(sql string, args ...any) {
			if strings.Contains(strings.ToLower(sql), "orchestration_node") && containsArg(args, "review") && containsArg(args, "completed") {
				projectedReviewNode = true
			}
		}},
	}
	_, err := activity.ReviewOutcome(t.Context(), ValidateOutcomeResult{
		Status:             "completed",
		TerminalPlanStatus: "completed",
	}, AnalyzeIssueResult{ProblemSummary: "Fix issue"}, IssueSnapshot{}, service.DispatchAgentTaskResult{
		PlanID:  "00000000-0000-0000-0000-000000000001",
		NodeID:  "00000000-0000-0000-0000-000000000002",
		TaskID:  "00000000-0000-0000-0000-000000000003",
		Attempt: 1,
	})
	if err != nil {
		t.Fatalf("ReviewOutcome returned error: %v", err)
	}
	if !projectedReviewNode {
		t.Fatal("ReviewOutcome should project the review node as completed")
	}
}

func TestReviewOutcomeUsesInjectedReasoner(t *testing.T) {
	activity := ActivitySet{
		DB: &captureArtifactDB{},
		Reasoner: stubEinoReasoner{reviewResult: ReviewOutcomeResult{
			Summary:           "provider review",
			SeverityLabel:     "low",
			RecommendedAction: "accept",
		}},
	}
	result, err := activity.ReviewOutcome(t.Context(), ValidateOutcomeResult{
		Status: "completed",
	}, AnalyzeIssueResult{ProblemSummary: "Fix issue"}, IssueSnapshot{}, service.DispatchAgentTaskResult{
		PlanID:  "00000000-0000-0000-0000-000000000001",
		NodeID:  "00000000-0000-0000-0000-000000000002",
		TaskID:  "00000000-0000-0000-0000-000000000003",
		Attempt: 1,
	})
	if err != nil {
		t.Fatalf("ReviewOutcome returned error: %v", err)
	}
	if result.Summary != "provider review" || result.RecommendedAction != "accept" {
		t.Fatalf("ReviewOutcome did not use injected reasoner: %+v", result)
	}
}

func TestReviewOutcomeDoesNotProjectAcceptWhenValidationNeedsHumanReview(t *testing.T) {
	var projectedAction string
	activity := ActivitySet{
		DB: &captureArtifactDB{onArtifact: func(sql string, args ...any) {
			if strings.Contains(strings.ToLower(sql), "orchestration_node") && containsArg(args, "review") {
				if len(args) >= 6 {
					projectedAction, _ = args[5].(string)
				}
			}
		}},
		Reasoner: stubEinoReasoner{reviewResult: ReviewOutcomeResult{
			Summary:           "provider says clean",
			SeverityLabel:     "low",
			RecommendedAction: "accept",
		}},
	}
	_, err := activity.ReviewOutcome(t.Context(), ValidateOutcomeResult{
		Status:             "waiting_human",
		ReasonCode:         "tests_failed",
		RecommendedAction:  "review",
		NeedsHumanReview:   true,
		TerminalPlanStatus: "waiting_human",
	}, AnalyzeIssueResult{ProblemSummary: "Fix issue"}, IssueSnapshot{}, service.DispatchAgentTaskResult{
		PlanID:  "00000000-0000-0000-0000-000000000001",
		NodeID:  "00000000-0000-0000-0000-000000000002",
		TaskID:  "00000000-0000-0000-0000-000000000003",
		Attempt: 1,
	})
	if err != nil {
		t.Fatalf("ReviewOutcome returned error: %v", err)
	}
	if projectedAction != "review" {
		t.Fatalf("review projection action = %q, want review", projectedAction)
	}
}

func TestSummarizeOutcomeUsesInjectedReasoner(t *testing.T) {
	activity := ActivitySet{
		Reasoner: stubEinoReasoner{summaryResult: SummarizeOutcomeResult{
			Summary:  "provider summary",
			TraceRef: "provider/trace/ref",
		}},
	}
	result, err := activity.SummarizeOutcome(t.Context(), ReviewOutcomeResult{}, ValidateOutcomeResult{}, AnalyzeIssueResult{}, IssueSnapshot{}, service.DispatchAgentTaskResult{})
	if err != nil {
		t.Fatalf("SummarizeOutcome returned error: %v", err)
	}
	if result.Summary != "provider summary" || result.TraceRef != "provider/trace/ref" {
		t.Fatalf("SummarizeOutcome did not use injected reasoner: %+v", result)
	}
}

func containsArg(args []any, want string) bool {
	for _, arg := range args {
		if s, ok := arg.(string); ok && s == want {
			return true
		}
	}
	return false
}

func containsIntArg(args []any, want int) bool {
	for _, arg := range args {
		if v, ok := arg.(int); ok && v == want {
			return true
		}
	}
	return false
}

func TestNewWorkerActivitySetInjectsProductionAnalyzer(t *testing.T) {
	activity, err := NewWorkerActivitySet(t.Context(), nil, nil, nil, EinoReasoningConfig{
		Provider: EinoProviderOpenAICompatible,
		APIKey:   "test-key",
		Model:    "test-model",
	})
	if err != nil {
		t.Fatalf("NewWorkerActivitySet returned error: %v", err)
	}
	if activity.Reasoner == nil {
		t.Fatal("worker activity set must inject the production Eino reasoner")
	}
	if _, ok := activity.Reasoner.(StaticEinoReasoner); ok {
		t.Fatal("worker activity set must not use StaticEinoReasoner in the production path")
	}
}

func TestAnalyzeIssueRejectsMalformedAnalyzerOutput(t *testing.T) {
	activity := ActivitySet{
		DB: &captureArtifactDB{},
		Reasoner: stubEinoReasoner{analyzeResult: AnalyzeIssueResult{
			ProblemSummary: "Adapter summary",
		}},
	}
	if _, err := activity.AnalyzeIssue(t.Context(), IssueSnapshot{
		IssueID: "00000000-0000-0000-0000-000000000004",
		Title:   "Use adapter",
	}, IssueWorkflowInput{
		PlanID: "00000000-0000-0000-0000-000000000001",
	}); err == nil {
		t.Fatal("AnalyzeIssue should reject malformed analyzer output")
	}
}

func TestProjectionWritesUseIdempotentInserts(t *testing.T) {
	var eventSQL, artifactSQL string
	activity := ActivitySet{
		DB: &captureArtifactDB{onArtifact: func(sql string, args ...any) {
			lower := strings.ToLower(sql)
			if strings.Contains(lower, "orchestration_event") {
				eventSQL = sql
			}
			if strings.Contains(lower, "orchestration_artifact") {
				artifactSQL = sql
			}
		}},
		Reasoner: stubEinoReasoner{analyzeResult: AnalyzeIssueResult{
			ProblemSummary:         "Adapter summary",
			ExecutionAdvice:        "Adapter advice",
			SuspectedContext:       "Adapter context",
			RecommendedAgentPrompt: "Adapter prompt",
			ReasonCode:             "adapter_ready",
			RecommendedAction:      "none",
		}},
	}
	if _, err := activity.AnalyzeIssue(t.Context(), IssueSnapshot{
		IssueID: "00000000-0000-0000-0000-000000000004",
		Title:   "Use adapter",
	}, IssueWorkflowInput{
		PlanID: "00000000-0000-0000-0000-000000000001",
	}); err != nil {
		t.Fatalf("AnalyzeIssue returned error: %v", err)
	}
	if !strings.Contains(strings.ToLower(eventSQL), "where not exists") {
		t.Fatalf("event projection insert must be idempotent under Temporal activity retry, sql=%s", eventSQL)
	}
	if !strings.Contains(strings.ToLower(artifactSQL), "where not exists") {
		t.Fatalf("artifact projection insert must be idempotent under Temporal activity retry, sql=%s", artifactSQL)
	}
}

func TestFinalizeWorkflowWritesReviewHandoffArtifact(t *testing.T) {
	var artifactSQL []string
	var artifactArgs [][]any
	activity := ActivitySet{
		DB: &captureArtifactDB{onArtifact: func(sql string, args ...any) {
			artifactSQL = append(artifactSQL, sql)
			artifactArgs = append(artifactArgs, args)
		}},
		Queries: nil,
	}
	err := activity.FinalizeWorkflow(t.Context(), ValidateOutcomeResult{
		Status:             "completed",
		TerminalPlanStatus: "completed",
	}, ReviewOutcomeResult{
		Summary:           "review clean",
		RecommendedAction: "accept",
	}, SummarizeOutcomeResult{
		Summary:  "all done",
		TraceRef: "plan-1/node-1/task-1",
	}, IssueWorkflowInput{
		PlanID:     "00000000-0000-0000-0000-000000000001",
		WorkflowID: "wf-1",
	}, IssueSnapshot{
		IssueID: "00000000-0000-0000-0000-000000000004",
	}, AnalyzeIssueResult{
		ProblemSummary: "Fix the bug",
	}, service.DispatchAgentTaskResult{
		PlanID: "00000000-0000-0000-0000-000000000001",
		NodeID: "00000000-0000-0000-0000-000000000002",
		TaskID: "00000000-0000-0000-0000-000000000003",
	}, service.AgentTaskOutcomeSignalInput{
		WorkflowID: "wf-1",
		Status:     "completed",
	})
	if err != nil {
		t.Fatalf("FinalizeWorkflow returned error: %v", err)
	}
	found := false
	for i, sql := range artifactSQL {
		if strings.Contains(sql, "orchestration_artifact") && strings.Contains(sql, "INSERT") {
			found = true
			for _, arg := range artifactArgs[i] {
				s, ok := arg.(string)
				if ok && s == "review_handoff" {
					return
				}
			}
		}
	}
	if !found {
		t.Fatal("FinalizeWorkflow must record an artifact via INSERT INTO orchestration_artifact")
	}
	t.Fatal("FinalizeWorkflow must record a review_handoff artifact type")
}

func TestSummarizeOutcomeIncludesTraceAndArtifactReferences(t *testing.T) {
	result, err := (ActivitySet{}).SummarizeOutcome(t.Context(), ReviewOutcomeResult{
		Summary:           "review flagged billing risk",
		Evidence:          []string{"TestAuth"},
		Risks:             []string{"touches billing"},
		RecommendedAction: "review",
	}, ValidateOutcomeResult{
		Status:             "completed",
		TerminalPlanStatus: "completed",
		ProjectionDetail:   "structured result validated",
	}, AnalyzeIssueResult{
		ProblemSummary: "Fix the billing bug",
	}, IssueSnapshot{
		IssueID: "issue-1",
	}, service.DispatchAgentTaskResult{
		PlanID: "plan-1", TaskID: "task-1", NodeID: "node-1", Attempt: 1,
	})
	if err != nil {
		t.Fatalf("SummarizeOutcome returned error: %v", err)
	}
	if result.TraceRef == "" {
		t.Fatal("handoff summary must include trace reference")
	}
	if result.TraceRef != "plan-1/node-1/task-1" {
		t.Fatalf("trace ref = %q, want plan-1/node-1/task-1", result.TraceRef)
	}
}

func TestPositiveReviewCannotOverrideDeterministicValidation(t *testing.T) {
	cases := []struct {
		name       string
		validation ValidateOutcomeResult
		review     ReviewOutcomeResult
		assert     func(t *testing.T, result ValidateOutcomeResult)
	}{
		{
			name: "failed tests stay in human review despite clean review",
			validation: ValidateOutcomeResult{
				Status:             "waiting_human",
				ReasonCode:         "tests_failed",
				RecommendedAction:  "review",
				NeedsHumanReview:   true,
				TerminalPlanStatus: "waiting_human",
				FailedTests:        []string{"TestAuth"},
			},
			review: ReviewOutcomeResult{
				Summary:           "all clean",
				RecommendedAction: "accept",
				HighRisk:          false,
			},
			assert: func(t *testing.T, result ValidateOutcomeResult) {
				if !result.NeedsHumanReview {
					t.Fatal("positive review must not override deterministic NeedsHumanReview")
				}
			},
		},
		{
			name: "risks stay in human review despite clean review",
			validation: ValidateOutcomeResult{
				Status:             "waiting_human",
				ReasonCode:         "risks_present",
				RecommendedAction:  "review",
				NeedsHumanReview:   true,
				TerminalPlanStatus: "waiting_human",
				Risks:              []string{"touches billing"},
			},
			review: ReviewOutcomeResult{
				Summary:           "no concerns",
				RecommendedAction: "accept",
				HighRisk:          false,
			},
			assert: func(t *testing.T, result ValidateOutcomeResult) {
				if !result.NeedsHumanReview {
					t.Fatal("positive review must not override deterministic NeedsHumanReview")
				}
			},
		},
		{
			name: "evidence insufficient stays retryable despite clean review",
			validation: ValidateOutcomeResult{
				Status:             "waiting_human",
				ReasonCode:         "evidence_insufficient",
				RecommendedAction:  "retry",
				ShouldRetry:        true,
				TerminalPlanStatus: "waiting_human",
			},
			review: ReviewOutcomeResult{
				Summary:           "looks good",
				RecommendedAction: "accept",
				HighRisk:          false,
			},
			assert: func(t *testing.T, result ValidateOutcomeResult) {
				if !result.ShouldRetry {
					t.Fatal("positive review must not override deterministic ShouldRetry")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := applyOutcomePolicy(tc.validation, tc.review)
			if result.TerminalPlanStatus != "waiting_human" {
				t.Fatalf("plan status = %q, want waiting_human", result.TerminalPlanStatus)
			}
			if result.ReasonCode != tc.validation.ReasonCode {
				t.Fatalf("reason code changed from %q to %q", tc.validation.ReasonCode, result.ReasonCode)
			}
			tc.assert(t, result)
		})
	}
}

func TestReviewOutcomeIsAdvisoryWithEvidenceRisksAndPolicyAction(t *testing.T) {
	result, err := (ActivitySet{}).ReviewOutcome(t.Context(), ValidateOutcomeResult{
		Status:             "completed",
		TerminalPlanStatus: "completed",
		ProjectionDetail:   "structured result validated",
		FailedTests:        []string{"TestAuth", "TestBilling"},
		Risks:              []string{"touches billing"},
	}, AnalyzeIssueResult{
		ProblemSummary: "Fix the billing bug",
		Risks:          []string{"review the issue description"},
	}, IssueSnapshot{
		Title: "Fix billing",
	}, service.DispatchAgentTaskResult{
		PlanID: "plan-1", TaskID: "task-1", NodeID: "node-1", Attempt: 1,
	})
	if err != nil {
		t.Fatalf("ReviewOutcome returned error: %v", err)
	}
	hasTestAuth := false
	hasTestBilling := false
	for _, e := range result.Evidence {
		if e == "TestAuth" {
			hasTestAuth = true
		}
		if e == "TestBilling" {
			hasTestBilling = true
		}
	}
	if !hasTestAuth || !hasTestBilling {
		t.Fatal("advisory review should include failed tests as evidence")
	}
	hasBillingRisk := false
	hasAnalysisRisk := false
	for _, r := range result.Risks {
		if r == "touches billing" {
			hasBillingRisk = true
		}
		if r == "review the issue description" {
			hasAnalysisRisk = true
		}
	}
	if !hasBillingRisk || !hasAnalysisRisk {
		t.Fatal("advisory review should include risks from validation and analysis")
	}
	if result.RecommendedAction == "" {
		t.Fatal("advisory review should recommend a policy action")
	}
}

func TestValidateOutcomeRejectsArbitraryJSONAsEvidenceInsufficient(t *testing.T) {
	result, err := (ActivitySet{}).ValidateOutcome(t.Context(), ValidateOutcomeInput{
		Outcome: service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "wf-1",
			PlanID:         "plan-1",
			NodeID:         "node-1",
			TaskID:         "task-1",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"ok":true}`),
		},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  "plan-1",
			NodeID:  "node-1",
			TaskID:  "task-1",
			Attempt: 1,
		},
		Analysis: AnalyzeIssueResult{ProblemSummary: "Fix issue"},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if result.ReasonCode != "evidence_insufficient" {
		t.Fatalf("reason = %q, want evidence_insufficient", result.ReasonCode)
	}
	if !result.ShouldRetry {
		t.Fatal("malformed schema should be retryable while budget remains")
	}
}

func TestValidateOutcomeRejectsUnknownSchemaVersion(t *testing.T) {
	result, err := (ActivitySet{}).ValidateOutcome(t.Context(), ValidateOutcomeInput{
		Outcome: service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "wf-1",
			PlanID:         "plan-1",
			NodeID:         "node-1",
			TaskID:         "task-1",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"schema_version":"2","summary":"done","changed_files":["server/a.go"],"artifacts":[],"tests":[{"name":"go test","status":"passed"}],"risks":[],"evidence":[{"type":"test","ref":"go test ./..."}]}`),
		},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  "plan-1",
			NodeID:  "node-1",
			TaskID:  "task-1",
			Attempt: 1,
		},
		Analysis: AnalyzeIssueResult{ProblemSummary: "Fix issue"},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if result.ReasonCode != "evidence_insufficient" {
		t.Fatalf("reason = %q, want evidence_insufficient", result.ReasonCode)
	}
	if !result.ShouldRetry {
		t.Fatal("unknown schema version should be retryable while budget remains")
	}
}

func TestValidateOutcomeAcceptsResultSchemaV1WithEvidence(t *testing.T) {
	result, err := (ActivitySet{}).ValidateOutcome(t.Context(), ValidateOutcomeInput{
		Outcome: service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "wf-1",
			PlanID:         "plan-1",
			NodeID:         "node-1",
			TaskID:         "task-1",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"schema_version":"1","summary":"done","changed_files":["server/a.go"],"artifacts":[{"label":"diff","ref":"artifact://diff"}],"tests":[{"name":"go test ./...","status":"passed"}],"risks":[],"evidence":[{"type":"test","ref":"go test ./..."}]}`),
		},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  "plan-1",
			NodeID:  "node-1",
			TaskID:  "task-1",
			Attempt: 1,
		},
		Analysis: AnalyzeIssueResult{ProblemSummary: "Fix issue"},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if result.Status != "completed" || result.TerminalPlanStatus != "completed" {
		t.Fatalf("validation result = %+v, want completed", result)
	}
	if result.ResultSummary != "done" || len(result.ChangedFiles) != 1 || result.ChangedFiles[0] != "server/a.go" {
		t.Fatalf("validation did not preserve result summary/changed files: %+v", result)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].Label != "diff" || result.Artifacts[0].Ref != "artifact://diff" {
		t.Fatalf("validation did not preserve result artifacts: %+v", result.Artifacts)
	}
	if len(result.Evidence) != 1 || result.Evidence[0].Type != "test" || result.Evidence[0].Ref != "go test ./..." {
		t.Fatalf("validation did not preserve result evidence: %+v", result.Evidence)
	}
}

func TestFinalizeWorkflowProjectsResultEvidenceArtifact(t *testing.T) {
	var sawResultEvidence bool
	activity := ActivitySet{
		DB: &captureArtifactDB{onArtifact: func(sql string, args ...any) {
			if !strings.Contains(strings.ToLower(sql), "orchestration_artifact") {
				return
			}
			if len(args) >= 5 && args[1] == "result_evidence" {
				sawResultEvidence = true
				raw, ok := args[4].([]byte)
				if !ok {
					t.Fatalf("result evidence payload type = %T, want []byte", args[4])
				}
				var payload map[string]any
				if err := json.Unmarshal(raw, &payload); err != nil {
					t.Fatalf("decode result evidence payload: %v", err)
				}
				if payload["summary"] != "done" {
					t.Fatalf("result evidence summary = %v, want done", payload["summary"])
				}
			}
		}},
	}

	err := activity.FinalizeWorkflow(t.Context(), ValidateOutcomeResult{
		Status:             "completed",
		TerminalPlanStatus: "completed",
		ResultSummary:      "done",
		ChangedFiles:       []string{"server/a.go"},
		Artifacts:          []ResultArtifactRef{{Label: "diff", Ref: "artifact://diff"}},
		Evidence:           []ResultEvidenceRef{{Type: "test", Ref: "go test ./..."}},
		Tests:              []ResultTestRef{{Name: "go test ./...", Status: "passed"}},
	}, ReviewOutcomeResult{Summary: "review clean"}, SummarizeOutcomeResult{
		Summary:  "done",
		TraceRef: "plan/node/task",
	}, IssueWorkflowInput{
		PlanID:  "00000000-0000-0000-0000-000000000001",
		IssueID: "00000000-0000-0000-0000-000000000004",
	}, IssueSnapshot{
		IssueID: "00000000-0000-0000-0000-000000000004",
	}, AnalyzeIssueResult{ProblemSummary: "Fix issue"}, service.DispatchAgentTaskResult{
		PlanID: "00000000-0000-0000-0000-000000000001",
		NodeID: "00000000-0000-0000-0000-000000000002",
		TaskID: "00000000-0000-0000-0000-000000000003",
	}, service.AgentTaskOutcomeSignalInput{})
	if err != nil {
		t.Fatalf("FinalizeWorkflow returned error: %v", err)
	}
	if !sawResultEvidence {
		t.Fatal("FinalizeWorkflow must project Result Schema v1 evidence as an artifact")
	}
}

func TestValidateOutcomeRejectsMissingChangedFiles(t *testing.T) {
	result, err := (ActivitySet{}).ValidateOutcome(t.Context(), ValidateOutcomeInput{
		Outcome: service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "wf-1",
			PlanID:         "plan-1",
			NodeID:         "node-1",
			TaskID:         "task-1",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"schema_version":"1","summary":"done","artifacts":[],"tests":[{"name":"go test ./...","status":"passed"}],"risks":[],"evidence":[{"type":"test","ref":"go test ./..."}]}`),
		},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  "plan-1",
			NodeID:  "node-1",
			TaskID:  "task-1",
			Attempt: 1,
		},
		Analysis: AnalyzeIssueResult{ProblemSummary: "Fix issue"},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if result.ReasonCode != "evidence_insufficient" || !result.ShouldRetry {
		t.Fatalf("missing changed_files should be evidence_insufficient retryable result: %+v", result)
	}
}

func TestValidateOutcomeRejectsEmptyChangedFiles(t *testing.T) {
	result, err := (ActivitySet{}).ValidateOutcome(t.Context(), ValidateOutcomeInput{
		Outcome: service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "wf-1",
			PlanID:         "plan-1",
			NodeID:         "node-1",
			TaskID:         "task-1",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"schema_version":"1","summary":"done","changed_files":[],"artifacts":[],"tests":[{"name":"go test ./...","status":"passed"}],"risks":[],"evidence":[{"type":"test","ref":"go test ./..."}]}`),
		},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  "plan-1",
			NodeID:  "node-1",
			TaskID:  "task-1",
			Attempt: 1,
		},
		Analysis: AnalyzeIssueResult{ProblemSummary: "Fix issue"},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if result.ReasonCode != "evidence_insufficient" || !result.ShouldRetry {
		t.Fatalf("empty changed_files should be evidence_insufficient retryable result: %+v", result)
	}
}

func TestValidateOutcomeRejectsIncompleteArtifacts(t *testing.T) {
	result, err := (ActivitySet{}).ValidateOutcome(t.Context(), ValidateOutcomeInput{
		Outcome: service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "wf-1",
			PlanID:         "plan-1",
			NodeID:         "node-1",
			TaskID:         "task-1",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"schema_version":"1","summary":"done","changed_files":["server/a.go"],"artifacts":[{"label":"diff","ref":""}],"tests":[{"name":"go test ./...","status":"passed"}],"risks":[],"evidence":[{"type":"test","ref":"go test ./..."}]}`),
		},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  "plan-1",
			NodeID:  "node-1",
			TaskID:  "task-1",
			Attempt: 1,
		},
		Analysis: AnalyzeIssueResult{ProblemSummary: "Fix issue"},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if result.ReasonCode != "evidence_insufficient" || !result.ShouldRetry {
		t.Fatalf("incomplete artifacts should be evidence_insufficient retryable result: %+v", result)
	}
}

func TestValidateOutcomeRejectsIncompleteTests(t *testing.T) {
	result, err := (ActivitySet{}).ValidateOutcome(t.Context(), ValidateOutcomeInput{
		Outcome: service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "wf-1",
			PlanID:         "plan-1",
			NodeID:         "node-1",
			TaskID:         "task-1",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"schema_version":"1","summary":"done","changed_files":["server/a.go"],"artifacts":[],"tests":[{"name":"","status":""}],"risks":[],"evidence":[{"type":"test","ref":"go test ./..."}]}`),
		},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  "plan-1",
			NodeID:  "node-1",
			TaskID:  "task-1",
			Attempt: 1,
		},
		Analysis: AnalyzeIssueResult{ProblemSummary: "Fix issue"},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if result.ReasonCode != "evidence_insufficient" || !result.ShouldRetry {
		t.Fatalf("incomplete tests should be evidence_insufficient retryable result: %+v", result)
	}
}

func TestValidateOutcomeRoutesFailedTestsToHumanReview(t *testing.T) {
	result, err := (ActivitySet{}).ValidateOutcome(t.Context(), ValidateOutcomeInput{
		Outcome: service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "wf-1",
			PlanID:         "plan-1",
			NodeID:         "node-1",
			TaskID:         "task-1",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"schema_version":"1","summary":"done","changed_files":["server/a.go"],"artifacts":[],"tests":[{"name":"go test ./...","status":"failed"}],"risks":[],"evidence":[{"type":"test","ref":"go test ./..."}]}`),
		},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  "plan-1",
			NodeID:  "node-1",
			TaskID:  "task-1",
			Attempt: 1,
		},
		Analysis: AnalyzeIssueResult{ProblemSummary: "Fix issue"},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if !result.NeedsHumanReview || result.ShouldRetry {
		t.Fatalf("failed tests should require human review without auto retry: %+v", result)
	}
	if result.ReasonCode != "tests_failed" {
		t.Fatalf("reason = %q, want tests_failed", result.ReasonCode)
	}
}

func TestValidateOutcomeRoutesRisksToHumanReview(t *testing.T) {
	result, err := (ActivitySet{}).ValidateOutcome(t.Context(), ValidateOutcomeInput{
		Outcome: service.AgentTaskOutcomeSignalInput{
			WorkflowID:     "wf-1",
			PlanID:         "plan-1",
			NodeID:         "node-1",
			TaskID:         "task-1",
			Attempt:        1,
			OutcomeVersion: 1,
			Status:         "completed",
			Result:         json.RawMessage(`{"schema_version":"1","summary":"done","changed_files":["server/a.go"],"artifacts":[],"tests":[{"name":"go test ./...","status":"passed"}],"risks":["touches billing"],"evidence":[{"type":"test","ref":"go test ./..."}]}`),
		},
		Dispatch: service.DispatchAgentTaskResult{
			PlanID:  "plan-1",
			NodeID:  "node-1",
			TaskID:  "task-1",
			Attempt: 1,
		},
		Analysis: AnalyzeIssueResult{ProblemSummary: "Fix issue"},
	})
	if err != nil {
		t.Fatalf("ValidateOutcome returned error: %v", err)
	}
	if !result.NeedsHumanReview || result.ShouldRetry {
		t.Fatalf("risks should require human review without auto retry: %+v", result)
	}
	if result.ReasonCode != "risks_present" {
		t.Fatalf("reason = %q, want risks_present", result.ReasonCode)
	}
}
