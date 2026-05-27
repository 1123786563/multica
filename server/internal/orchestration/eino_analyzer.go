package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/multica-ai/multica/server/internal/service"
	orderedmap "github.com/wk8/go-ordered-map/v2"
	"go.temporal.io/sdk/temporal"
)

const (
	EinoProviderOpenAICompatible = "openai-compatible"
	EinoProviderStatic           = "static"
	DefaultReasoningProfileRef   = "worker-default"

	EinoReasonProviderIncompatible = "provider_incompatible"
	EinoReasonProviderAuthFailed   = "provider_auth_failed"
	EinoReasonProviderTimeout      = "provider_timeout"
	EinoReasonProviderRateLimited  = "provider_rate_limited"
	EinoReasonProviderUnavailable  = "provider_unavailable"
	EinoReasonOutputMalformed      = "eino_output_malformed"
)

const einoFailureErrorTypePrefix = "EinoFailure:"

type EinoReasoningConfig struct {
	Provider            string
	APIKey              string
	Model               string
	BaseURL             string
	Timeout             time.Duration
	MaxOutputTokens     int
	AllowStatic         bool
	ReasoningProfileRef string
}

type EinoIssueAnalyzer struct {
	model               einomodel.BaseChatModel
	reviewModel         einomodel.BaseChatModel
	summaryModel        einomodel.BaseChatModel
	maxOutputTokens     int
	reasoningProfileRef string
	providerLabel       string
	modelName           string
}

type EinoTrace struct {
	ReasoningProfileRef string
	PromptProfileRef    string
	SchemaName          string
	SchemaVersion       string
	ProviderLabel       string
	Model               string
	CapabilityMode      string
	LatencyMS           int64
	InputTokens         int
	OutputTokens        int
	TotalTokens         int
}

type einoAnalyzeIssueJSON struct {
	ProblemSummary         string    `json:"problem_summary"`
	ExecutionAdvice        string    `json:"execution_advice"`
	SuspectedContext       string    `json:"suspected_context"`
	Risks                  *[]string `json:"risks"`
	RecommendedAgentPrompt string    `json:"recommended_agent_prompt"`
	ReasonCode             string    `json:"reason_code"`
	RecommendedAction      string    `json:"recommended_action"`
}

type einoReviewOutcomeJSON struct {
	Summary           string    `json:"summary"`
	HighRisk          bool      `json:"high_risk"`
	Concern           string    `json:"concern"`
	SeverityLabel     string    `json:"severity_label"`
	Evidence          *[]string `json:"evidence"`
	Risks             *[]string `json:"risks"`
	RecommendedAction string    `json:"recommended_action"`
}

type einoSummarizeOutcomeJSON struct {
	Summary  string `json:"summary"`
	TraceRef string `json:"trace_ref"`
}

type EinoFailureDetails struct {
	ReasonCode string `json:"reason_code"`
	Message    string `json:"message"`
}

func NewEinoIssueAnalyzer(ctx context.Context, cfg EinoReasoningConfig) (EinoIssueAnalyzer, error) {
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	if cfg.Provider == "" {
		cfg.Provider = EinoProviderOpenAICompatible
	}
	cfg.ReasoningProfileRef = normalizeReasoningProfileRef(cfg.ReasoningProfileRef)
	if cfg.Provider != EinoProviderOpenAICompatible && cfg.Provider != "openai" {
		return EinoIssueAnalyzer{}, fmt.Errorf("unsupported Eino reasoning provider %q", cfg.Provider)
	}

	var missing []string
	if strings.TrimSpace(cfg.APIKey) == "" {
		missing = append(missing, "api key")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		missing = append(missing, "model")
	}
	if len(missing) > 0 {
		return EinoIssueAnalyzer{}, fmt.Errorf("Eino reasoning provider missing %s", strings.Join(missing, " and "))
	}

	maxTokens := cfg.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	cm, err := newEinoChatModel(ctx, cfg, maxTokens, einoAnalyzeIssueResponseFormat())
	if err != nil {
		return EinoIssueAnalyzer{}, fmt.Errorf("initialize Eino OpenAI-compatible ChatModel: %w", err)
	}
	reviewModel, err := newEinoChatModel(ctx, cfg, maxTokens, einoReviewOutcomeResponseFormat())
	if err != nil {
		return EinoIssueAnalyzer{}, fmt.Errorf("initialize Eino OpenAI-compatible review ChatModel: %w", err)
	}
	summaryModel, err := newEinoChatModel(ctx, cfg, maxTokens, einoSummarizeOutcomeResponseFormat())
	if err != nil {
		return EinoIssueAnalyzer{}, fmt.Errorf("initialize Eino OpenAI-compatible summarize ChatModel: %w", err)
	}
	return EinoIssueAnalyzer{
		model:               cm,
		reviewModel:         reviewModel,
		summaryModel:        summaryModel,
		maxOutputTokens:     maxTokens,
		reasoningProfileRef: cfg.ReasoningProfileRef,
		providerLabel:       EinoProviderOpenAICompatible,
		modelName:           strings.TrimSpace(cfg.Model),
	}, nil
}

func normalizeReasoningProfileRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return DefaultReasoningProfileRef
	}
	return ref
}

func newEinoFailure(reasonCode, message string) error {
	reasonCode = strings.TrimSpace(reasonCode)
	if reasonCode == "" {
		reasonCode = EinoReasonProviderUnavailable
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Eino provider failed"
	}
	return temporal.NewApplicationError(message, einoFailureErrorTypePrefix+reasonCode, EinoFailureDetails{
		ReasonCode: reasonCode,
		Message:    message,
	})
}

func classifyEinoProviderError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(err.Error())
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "context deadline"):
		return newEinoFailure(EinoReasonProviderTimeout, msg)
	case strings.Contains(lower, "rate limit") || strings.Contains(lower, "rate_limit") || strings.Contains(lower, "429"):
		return newEinoFailure(EinoReasonProviderRateLimited, msg)
	case strings.Contains(lower, "unauthorized") || strings.Contains(lower, "forbidden") || strings.Contains(lower, "401") || strings.Contains(lower, "403") || strings.Contains(lower, "api key") || strings.Contains(lower, "authentication"):
		return newEinoFailure(EinoReasonProviderAuthFailed, msg)
	case strings.Contains(lower, "unsupported") || strings.Contains(lower, "incompatible"):
		return newEinoFailure(EinoReasonProviderIncompatible, msg)
	default:
		return newEinoFailure(EinoReasonProviderUnavailable, msg)
	}
}

func recommendedActionForEinoFailure(reason string) string {
	switch reason {
	case EinoReasonProviderIncompatible, EinoReasonProviderAuthFailed:
		return "configure_provider"
	case EinoReasonProviderTimeout, EinoReasonProviderRateLimited, EinoReasonProviderUnavailable:
		return "retry_later"
	case EinoReasonOutputMalformed:
		return "configure_provider"
	default:
		return "configure_provider"
	}
}

func newEinoChatModel(ctx context.Context, cfg EinoReasoningConfig, maxTokens int, responseFormat *einoopenai.ChatCompletionResponseFormat) (einomodel.BaseChatModel, error) {
	temperature := float32(0)
	return einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		APIKey:              strings.TrimSpace(cfg.APIKey),
		Model:               strings.TrimSpace(cfg.Model),
		BaseURL:             strings.TrimSpace(cfg.BaseURL),
		Timeout:             cfg.Timeout,
		Temperature:         &temperature,
		MaxCompletionTokens: &maxTokens,
		ResponseFormat:      responseFormat,
	})
}

func einoAnalyzeIssueResponseFormat() *einoopenai.ChatCompletionResponseFormat {
	return &einoopenai.ChatCompletionResponseFormat{
		Type: einoopenai.ChatCompletionResponseFormatTypeJSONSchema,
		JSONSchema: &einoopenai.ChatCompletionResponseFormatJSONSchema{
			Name:        "multica_analyze_issue",
			Description: "Structured analysis and coding guidance for a fixed Multica orchestration AnalyzeIssue activity.",
			Strict:      true,
			JSONSchema:  einoAnalyzeIssueJSONSchema(),
		},
	}
}

func einoReviewOutcomeResponseFormat() *einoopenai.ChatCompletionResponseFormat {
	return &einoopenai.ChatCompletionResponseFormat{
		Type: einoopenai.ChatCompletionResponseFormatTypeJSONSchema,
		JSONSchema: &einoopenai.ChatCompletionResponseFormatJSONSchema{
			Name:        "multica_review_outcome",
			Description: "Structured advisory review for a fixed Multica orchestration ReviewOutcome activity.",
			Strict:      true,
			JSONSchema:  einoReviewOutcomeJSONSchema(),
		},
	}
}

func einoSummarizeOutcomeResponseFormat() *einoopenai.ChatCompletionResponseFormat {
	return &einoopenai.ChatCompletionResponseFormat{
		Type: einoopenai.ChatCompletionResponseFormatTypeJSONSchema,
		JSONSchema: &einoopenai.ChatCompletionResponseFormatJSONSchema{
			Name:        "multica_summarize_outcome",
			Description: "Structured handoff summary for a fixed Multica orchestration SummarizeOutcome activity.",
			Strict:      true,
			JSONSchema:  einoSummarizeOutcomeJSONSchema(),
		},
	}
}

func einoAnalyzeIssueJSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: string(schema.Object),
		Properties: orderedmap.New[string, *jsonschema.Schema](
			orderedmap.WithInitialData[string, *jsonschema.Schema](
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key:   "problem_summary",
					Value: einoStringSchema("Concise summary of the issue being analyzed."),
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key:   "execution_advice",
					Value: einoStringSchema("Practical execution guidance for the daemon-backed coding task."),
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key:   "suspected_context",
					Value: einoStringSchema("Likely code or product context the coding agent should inspect."),
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key:   "risks",
					Value: einoStringArraySchema("Risk notes for the coding agent and reviewer."),
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key:   "recommended_agent_prompt",
					Value: einoStringSchema("Prompt to send to the daemon-backed agent task."),
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key:   "reason_code",
					Value: einoStringSchema("Machine-readable reason code for the analysis node."),
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key:   "recommended_action",
					Value: einoStringSchema("Recommended next action for the orchestration node."),
				},
			),
		),
		Required: []string{
			"problem_summary",
			"execution_advice",
			"suspected_context",
			"risks",
			"recommended_agent_prompt",
			"reason_code",
			"recommended_action",
		},
		AdditionalProperties: jsonschema.FalseSchema,
	}
}

func einoReviewOutcomeJSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: string(schema.Object),
		Properties: orderedmap.New[string, *jsonschema.Schema](
			orderedmap.WithInitialData[string, *jsonschema.Schema](
				orderedmap.Pair[string, *jsonschema.Schema]{Key: "summary", Value: einoStringSchema("Concise advisory review summary.")},
				orderedmap.Pair[string, *jsonschema.Schema]{Key: "high_risk", Value: &jsonschema.Schema{
					Type:        string(schema.Boolean),
					Description: "Whether the reviewed outcome appears high risk.",
				}},
				orderedmap.Pair[string, *jsonschema.Schema]{Key: "concern", Value: einoStringSchema("Primary review concern, or an empty string when none.")},
				orderedmap.Pair[string, *jsonschema.Schema]{Key: "severity_label", Value: einoStringSchema("Advisory severity label such as low, medium, or high.")},
				orderedmap.Pair[string, *jsonschema.Schema]{Key: "evidence", Value: einoStringArraySchema("Evidence references considered by the review.")},
				orderedmap.Pair[string, *jsonschema.Schema]{Key: "risks", Value: einoStringArraySchema("Advisory risks found by the review.")},
				orderedmap.Pair[string, *jsonschema.Schema]{Key: "recommended_action", Value: einoStringSchema("Advisory recommendation such as accept or review.")},
			),
		),
		Required: []string{
			"summary",
			"high_risk",
			"concern",
			"severity_label",
			"evidence",
			"risks",
			"recommended_action",
		},
		AdditionalProperties: jsonschema.FalseSchema,
	}
}

func einoSummarizeOutcomeJSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: string(schema.Object),
		Properties: orderedmap.New[string, *jsonschema.Schema](
			orderedmap.WithInitialData[string, *jsonschema.Schema](
				orderedmap.Pair[string, *jsonschema.Schema]{Key: "summary", Value: einoStringSchema("Concise handoff summary for a human reviewer.")},
				orderedmap.Pair[string, *jsonschema.Schema]{Key: "trace_ref", Value: einoStringSchema("Safe trace reference in plan/node/task format.")},
			),
		),
		Required:             []string{"summary", "trace_ref"},
		AdditionalProperties: jsonschema.FalseSchema,
	}
}

func einoStringSchema(description string) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        string(schema.String),
		Description: description,
	}
}

func einoStringArraySchema(description string) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        string(schema.Array),
		Description: description,
		Items: &jsonschema.Schema{
			Type: string(schema.String),
		},
	}
}

func (a EinoIssueAnalyzer) AnalyzeIssue(ctx context.Context, issue IssueSnapshot, input IssueWorkflowInput) (AnalyzeIssueResult, error) {
	if a.model == nil {
		return AnalyzeIssueResult{}, newEinoFailure(EinoReasonProviderIncompatible, "Eino reasoning provider is not configured")
	}
	started := time.Now()
	resp, err := a.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(einoAnalyzeIssueSystemPrompt()),
		schema.UserMessage(einoAnalyzeIssueUserPrompt(issue, input)),
	}, einomodel.WithMaxTokens(a.maxOutputTokens), einomodel.WithTemperature(0))
	if err != nil {
		return AnalyzeIssueResult{}, classifyEinoProviderError(err)
	}
	if resp == nil {
		return AnalyzeIssueResult{}, newEinoFailure(EinoReasonProviderUnavailable, "Eino analyze issue provider returned no response")
	}
	result, err := parseEinoAnalyzeIssueOutput(resp.Content)
	if err != nil {
		return AnalyzeIssueResult{}, newEinoFailure(EinoReasonOutputMalformed, err.Error())
	}
	result.Trace = a.trace("analyze/v1", "multica_analyze_issue", time.Since(started))
	return result, nil
}

func (a EinoIssueAnalyzer) ReviewOutcome(ctx context.Context, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (ReviewOutcomeResult, error) {
	model := a.reviewModel
	if model == nil {
		model = a.model
	}
	if model == nil {
		return ReviewOutcomeResult{}, newEinoFailure(EinoReasonProviderIncompatible, "Eino reasoning provider is not configured")
	}
	started := time.Now()
	resp, err := model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(einoReviewOutcomeSystemPrompt()),
		schema.UserMessage(einoReviewOutcomeUserPrompt(validation, analysis, issue, dispatch)),
	}, einomodel.WithMaxTokens(a.maxOutputTokens), einomodel.WithTemperature(0))
	if err != nil {
		return ReviewOutcomeResult{}, classifyEinoProviderError(err)
	}
	if resp == nil {
		return ReviewOutcomeResult{}, newEinoFailure(EinoReasonProviderUnavailable, "Eino review outcome provider returned no response")
	}
	result, err := parseEinoReviewOutcomeOutput(resp.Content)
	if err != nil {
		return ReviewOutcomeResult{}, newEinoFailure(EinoReasonOutputMalformed, err.Error())
	}
	result.Trace = a.trace("review/v1", "multica_review_outcome", time.Since(started))
	return result, nil
}

func (a EinoIssueAnalyzer) SummarizeOutcome(ctx context.Context, review ReviewOutcomeResult, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) (SummarizeOutcomeResult, error) {
	model := a.summaryModel
	if model == nil {
		model = a.model
	}
	if model == nil {
		return SummarizeOutcomeResult{}, newEinoFailure(EinoReasonProviderIncompatible, "Eino reasoning provider is not configured")
	}
	started := time.Now()
	resp, err := model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(einoSummarizeOutcomeSystemPrompt()),
		schema.UserMessage(einoSummarizeOutcomeUserPrompt(review, validation, analysis, issue, dispatch)),
	}, einomodel.WithMaxTokens(a.maxOutputTokens), einomodel.WithTemperature(0))
	if err != nil {
		return SummarizeOutcomeResult{}, classifyEinoProviderError(err)
	}
	if resp == nil {
		return SummarizeOutcomeResult{}, newEinoFailure(EinoReasonProviderUnavailable, "Eino summarize outcome provider returned no response")
	}
	result, err := parseEinoSummarizeOutcomeOutput(resp.Content)
	if err != nil {
		return SummarizeOutcomeResult{}, newEinoFailure(EinoReasonOutputMalformed, err.Error())
	}
	result.Trace = a.trace("summary/v1", "multica_summarize_outcome", time.Since(started))
	return result, nil
}

func (a EinoIssueAnalyzer) trace(promptProfileRef, schemaName string, latency time.Duration) EinoTrace {
	profileRef := normalizeReasoningProfileRef(a.reasoningProfileRef)
	providerLabel := strings.TrimSpace(a.providerLabel)
	if providerLabel == "" {
		providerLabel = EinoProviderOpenAICompatible
	}
	return EinoTrace{
		ReasoningProfileRef: profileRef,
		PromptProfileRef:    promptProfileRef,
		SchemaName:          schemaName,
		SchemaVersion:       "1",
		ProviderLabel:       providerLabel,
		Model:               strings.TrimSpace(a.modelName),
		CapabilityMode:      "json_schema",
		LatencyMS:           latency.Milliseconds(),
	}
}

func parseEinoAnalyzeIssueOutput(raw string) (AnalyzeIssueResult, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return AnalyzeIssueResult{}, errors.New("malformed Eino analyze issue output: empty response")
	}

	var payload einoAnalyzeIssueJSON
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		return AnalyzeIssueResult{}, fmt.Errorf("malformed Eino analyze issue output: %w", err)
	}
	if _, err := dec.Token(); err == nil {
		return AnalyzeIssueResult{}, errors.New("malformed Eino analyze issue output: trailing data")
	}

	result := AnalyzeIssueResult{
		ProblemSummary:         strings.TrimSpace(payload.ProblemSummary),
		ExecutionAdvice:        strings.TrimSpace(payload.ExecutionAdvice),
		SuspectedContext:       strings.TrimSpace(payload.SuspectedContext),
		RecommendedAgentPrompt: strings.TrimSpace(payload.RecommendedAgentPrompt),
		ReasonCode:             strings.TrimSpace(payload.ReasonCode),
		RecommendedAction:      strings.TrimSpace(payload.RecommendedAction),
	}
	var missing []string
	if result.ProblemSummary == "" {
		missing = append(missing, "problem_summary")
	}
	if result.ExecutionAdvice == "" {
		missing = append(missing, "execution_advice")
	}
	if result.SuspectedContext == "" {
		missing = append(missing, "suspected_context")
	}
	if payload.Risks == nil {
		missing = append(missing, "risks")
	} else {
		result.Risks = nonEmptyStrings(*payload.Risks)
	}
	if result.RecommendedAgentPrompt == "" {
		missing = append(missing, "recommended_agent_prompt")
	}
	if result.ReasonCode == "" {
		missing = append(missing, "reason_code")
	}
	if result.RecommendedAction == "" {
		missing = append(missing, "recommended_action")
	}
	if len(missing) > 0 {
		return AnalyzeIssueResult{}, fmt.Errorf("malformed Eino analyze issue output: missing %s", strings.Join(missing, ", "))
	}
	return result, nil
}

func parseEinoReviewOutcomeOutput(raw string) (ReviewOutcomeResult, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ReviewOutcomeResult{}, errors.New("malformed Eino review outcome output: empty response")
	}
	keys, err := strictJSONKeys(raw, &einoReviewOutcomeJSON{})
	if err != nil {
		return ReviewOutcomeResult{}, fmt.Errorf("malformed Eino review outcome output: %w", err)
	}
	required := []string{"summary", "high_risk", "concern", "severity_label", "evidence", "risks", "recommended_action"}
	if missing := missingJSONFields(keys, required...); len(missing) > 0 {
		return ReviewOutcomeResult{}, fmt.Errorf("malformed Eino review outcome output: missing %s", strings.Join(missing, ", "))
	}

	var payload einoReviewOutcomeJSON
	if err := decodeStrictJSON(raw, &payload); err != nil {
		return ReviewOutcomeResult{}, fmt.Errorf("malformed Eino review outcome output: %w", err)
	}
	result := ReviewOutcomeResult{
		Summary:           strings.TrimSpace(payload.Summary),
		HighRisk:          payload.HighRisk,
		Concern:           strings.TrimSpace(payload.Concern),
		SeverityLabel:     strings.TrimSpace(payload.SeverityLabel),
		RecommendedAction: strings.TrimSpace(payload.RecommendedAction),
	}
	if payload.Evidence != nil {
		result.Evidence = nonEmptyStrings(*payload.Evidence)
	}
	if payload.Risks != nil {
		result.Risks = nonEmptyStrings(*payload.Risks)
	}
	var missing []string
	if result.Summary == "" {
		missing = append(missing, "summary")
	}
	if result.SeverityLabel == "" {
		missing = append(missing, "severity_label")
	}
	if payload.Evidence == nil {
		missing = append(missing, "evidence")
	}
	if payload.Risks == nil {
		missing = append(missing, "risks")
	}
	if result.RecommendedAction == "" {
		missing = append(missing, "recommended_action")
	}
	if len(missing) > 0 {
		return ReviewOutcomeResult{}, fmt.Errorf("malformed Eino review outcome output: missing %s", strings.Join(missing, ", "))
	}
	return result, nil
}

func parseEinoSummarizeOutcomeOutput(raw string) (SummarizeOutcomeResult, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return SummarizeOutcomeResult{}, errors.New("malformed Eino summarize outcome output: empty response")
	}
	keys, err := strictJSONKeys(raw, &einoSummarizeOutcomeJSON{})
	if err != nil {
		return SummarizeOutcomeResult{}, fmt.Errorf("malformed Eino summarize outcome output: %w", err)
	}
	if missing := missingJSONFields(keys, "summary", "trace_ref"); len(missing) > 0 {
		return SummarizeOutcomeResult{}, fmt.Errorf("malformed Eino summarize outcome output: missing %s", strings.Join(missing, ", "))
	}

	var payload einoSummarizeOutcomeJSON
	if err := decodeStrictJSON(raw, &payload); err != nil {
		return SummarizeOutcomeResult{}, fmt.Errorf("malformed Eino summarize outcome output: %w", err)
	}
	result := SummarizeOutcomeResult{
		Summary:  strings.TrimSpace(payload.Summary),
		TraceRef: strings.TrimSpace(payload.TraceRef),
	}
	var missing []string
	if result.Summary == "" {
		missing = append(missing, "summary")
	}
	if result.TraceRef == "" {
		missing = append(missing, "trace_ref")
	}
	if len(missing) > 0 {
		return SummarizeOutcomeResult{}, fmt.Errorf("malformed Eino summarize outcome output: missing %s", strings.Join(missing, ", "))
	}
	return result, nil
}

func strictJSONKeys(raw string, target any) (map[string]struct{}, error) {
	var keys map[string]json.RawMessage
	if err := decodeStrictJSON(raw, &keys); err != nil {
		return nil, err
	}
	if err := decodeStrictJSON(raw, target); err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(keys))
	for key := range keys {
		result[key] = struct{}{}
	}
	return result, nil
}

func decodeStrictJSON(raw string, target any) error {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	if _, err := dec.Token(); err == nil {
		return errors.New("trailing data")
	}
	return nil
}

func missingJSONFields(keys map[string]struct{}, fields ...string) []string {
	missing := make([]string, 0)
	for _, field := range fields {
		if _, ok := keys[field]; !ok {
			missing = append(missing, field)
		}
	}
	return missing
}

func einoAnalyzeIssueSystemPrompt() string {
	return strings.TrimSpace(`You are Multica's orchestration reasoning node.

Return exactly one JSON object and no Markdown or prose.
Your first character must be { and your last character must be }.
Do not wrap the JSON in markdown code fences.
Allowed fields are:
- problem_summary: string
- execution_advice: string
- suspected_context: string
- risks: string[]
- recommended_agent_prompt: string
- reason_code: string
- recommended_action: string

Do not create, remove, reorder, branch, loop, or rename workflow nodes.
Do not include topology, nodes, workflow, final_success, is_success, or verdict fields.
The coding work will be executed by a daemon-backed Agent Task; your role is only analysis and prompt guidance.`)
}

func einoAnalyzeIssueUserPrompt(issue IssueSnapshot, input IssueWorkflowInput) string {
	contextParts := []string{
		"Workspace ID: " + input.WorkspaceID,
		"Issue ID: " + issue.IssueID,
		"Plan ID: " + input.PlanID,
		"Title: " + issue.Title,
		"Description:\n" + issue.Description,
		"Acceptance criteria:\n" + issue.AcceptanceText,
		"Priority: " + issue.Priority,
		"Status: " + issue.Status,
		"Assignee type: " + issue.AssigneeType,
	}
	return strings.TrimSpace(strings.Join(contextParts, "\n\n"))
}

func einoReviewOutcomeSystemPrompt() string {
	return strings.TrimSpace(`You are Multica's advisory review reasoning node.

Return exactly one JSON object and no Markdown or prose.
Your first character must be { and your last character must be }.
Do not wrap the JSON in markdown code fences.
Allowed fields are:
- summary: string
- high_risk: boolean
- concern: string
- severity_label: string
- evidence: string[]
- risks: string[]
- recommended_action: string

Your review is advisory only. Do not decide final workflow status.
Do not include is_success, final_status, terminal_plan_status, should_retry, or any workflow action fields.
Ground the review in the provided validation summary, evidence refs, risks, and issue context.`)
}

func einoReviewOutcomeUserPrompt(validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) string {
	return safeJSONPrompt(map[string]any{
		"issue": map[string]any{
			"id":          issue.IssueID,
			"title":       issue.Title,
			"priority":    issue.Priority,
			"status":      issue.Status,
			"assignee":    issue.AssigneeType,
			"acceptance":  issue.AcceptanceText,
			"description": issue.Description,
		},
		"analysis": map[string]any{
			"problem_summary":   analysis.ProblemSummary,
			"execution_advice":  analysis.ExecutionAdvice,
			"suspected_context": analysis.SuspectedContext,
			"risks":             analysis.Risks,
			"reason_code":       analysis.ReasonCode,
		},
		"validation": map[string]any{
			"status":            validation.Status,
			"reason_code":       validation.ReasonCode,
			"projection_detail": validation.ProjectionDetail,
			"failed_tests":      validation.FailedTests,
			"risks":             validation.Risks,
			"result_summary":    validation.ResultSummary,
			"changed_files":     validation.ChangedFiles,
			"artifacts":         validation.Artifacts,
			"tests":             validation.Tests,
			"evidence":          validation.Evidence,
		},
		"dispatch": map[string]any{
			"plan_id": dispatch.PlanID,
			"node_id": dispatch.NodeID,
			"task_id": dispatch.TaskID,
			"attempt": dispatch.Attempt,
		},
	})
}

func einoSummarizeOutcomeSystemPrompt() string {
	return strings.TrimSpace(`You are Multica's outcome summarization reasoning node.

Return exactly one JSON object and no Markdown or prose.
Your first character must be { and your last character must be }.
Do not wrap the JSON in markdown code fences.
Allowed fields are:
- summary: string
- trace_ref: string

Summarize the validated handoff for a human reviewer using safe evidence and trace references.
Do not include terminal_plan_status, final_status, should_retry, is_success, or any workflow action fields.`)
}

func einoSummarizeOutcomeUserPrompt(review ReviewOutcomeResult, validation ValidateOutcomeResult, analysis AnalyzeIssueResult, issue IssueSnapshot, dispatch service.DispatchAgentTaskResult) string {
	traceRef := strings.Join([]string{dispatch.PlanID, dispatch.NodeID, dispatch.TaskID}, "/")
	return safeJSONPrompt(map[string]any{
		"trace_ref": traceRef,
		"issue": map[string]any{
			"id":    issue.IssueID,
			"title": issue.Title,
		},
		"analysis": map[string]any{
			"problem_summary":  analysis.ProblemSummary,
			"execution_advice": analysis.ExecutionAdvice,
		},
		"validation": map[string]any{
			"status":            validation.Status,
			"reason_code":       validation.ReasonCode,
			"projection_detail": validation.ProjectionDetail,
			"result_summary":    validation.ResultSummary,
			"changed_files":     validation.ChangedFiles,
			"artifacts":         validation.Artifacts,
			"tests":             validation.Tests,
			"evidence":          validation.Evidence,
		},
		"review": map[string]any{
			"summary":            review.Summary,
			"high_risk":          review.HighRisk,
			"concern":            review.Concern,
			"severity_label":     review.SeverityLabel,
			"evidence":           review.Evidence,
			"risks":              review.Risks,
			"recommended_action": review.RecommendedAction,
		},
	})
}

func safeJSONPrompt(v any) string {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(raw)
}
