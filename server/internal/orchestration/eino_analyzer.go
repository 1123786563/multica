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
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

const (
	EinoProviderOpenAICompatible = "openai-compatible"
	EinoProviderStatic           = "static"
)

type EinoReasoningConfig struct {
	Provider        string
	APIKey          string
	Model           string
	BaseURL         string
	Timeout         time.Duration
	MaxOutputTokens int
}

type EinoIssueAnalyzer struct {
	model           einomodel.BaseChatModel
	maxOutputTokens int
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

func NewEinoIssueAnalyzer(ctx context.Context, cfg EinoReasoningConfig) (EinoIssueAnalyzer, error) {
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	if cfg.Provider == "" {
		cfg.Provider = EinoProviderOpenAICompatible
	}
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
		maxTokens = 1200
	}
	temperature := float32(0)
	cm, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		APIKey:              strings.TrimSpace(cfg.APIKey),
		Model:               strings.TrimSpace(cfg.Model),
		BaseURL:             strings.TrimSpace(cfg.BaseURL),
		Timeout:             cfg.Timeout,
		Temperature:         &temperature,
		MaxCompletionTokens: &maxTokens,
		ResponseFormat:      einoAnalyzeIssueResponseFormat(),
	})
	if err != nil {
		return EinoIssueAnalyzer{}, fmt.Errorf("initialize Eino OpenAI-compatible ChatModel: %w", err)
	}
	return EinoIssueAnalyzer{model: cm, maxOutputTokens: maxTokens}, nil
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

func einoAnalyzeIssueJSONSchema() *jsonschema.Schema {
	stringSchema := func(description string) *jsonschema.Schema {
		return &jsonschema.Schema{
			Type:        string(schema.String),
			Description: description,
		}
	}
	return &jsonschema.Schema{
		Type: string(schema.Object),
		Properties: orderedmap.New[string, *jsonschema.Schema](
			orderedmap.WithInitialData[string, *jsonschema.Schema](
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key:   "problem_summary",
					Value: stringSchema("Concise summary of the issue being analyzed."),
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key:   "execution_advice",
					Value: stringSchema("Practical execution guidance for the daemon-backed coding task."),
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key:   "suspected_context",
					Value: stringSchema("Likely code or product context the coding agent should inspect."),
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key: "risks",
					Value: &jsonschema.Schema{
						Type:        string(schema.Array),
						Description: "Risk notes for the coding agent and reviewer.",
						Items: &jsonschema.Schema{
							Type: string(schema.String),
						},
					},
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key:   "recommended_agent_prompt",
					Value: stringSchema("Prompt to send to the daemon-backed agent task."),
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key:   "reason_code",
					Value: stringSchema("Machine-readable reason code for the analysis node."),
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key:   "recommended_action",
					Value: stringSchema("Recommended next action for the orchestration node."),
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

func (a EinoIssueAnalyzer) AnalyzeIssue(ctx context.Context, issue IssueSnapshot, input IssueWorkflowInput) (AnalyzeIssueResult, error) {
	if a.model == nil {
		return AnalyzeIssueResult{}, errors.New("Eino reasoning provider is not configured")
	}
	resp, err := a.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(einoAnalyzeIssueSystemPrompt()),
		schema.UserMessage(einoAnalyzeIssueUserPrompt(issue, input)),
	}, einomodel.WithMaxTokens(a.maxOutputTokens), einomodel.WithTemperature(0))
	if err != nil {
		return AnalyzeIssueResult{}, fmt.Errorf("Eino analyze issue provider call failed: %w", err)
	}
	if resp == nil {
		return AnalyzeIssueResult{}, errors.New("Eino analyze issue provider returned no response")
	}
	result, err := parseEinoAnalyzeIssueOutput(resp.Content)
	if err != nil {
		return AnalyzeIssueResult{}, err
	}
	return result, nil
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

func einoAnalyzeIssueSystemPrompt() string {
	return strings.TrimSpace(`You are Multica's orchestration reasoning node.

Return exactly one JSON object and no Markdown or prose.
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
