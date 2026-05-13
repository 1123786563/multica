package service

import "encoding/json"

const orchestrationContextType = "orchestration_node"

type OrchestrationTaskContext struct {
	Type                 string          `json:"type"`
	OrchestrationPlanID  string          `json:"orchestration_plan_id"`
	OrchestrationNodeID  string          `json:"orchestration_node_id"`
	OrchestrationRunID   string          `json:"orchestration_run_id"`
	NodeType             string          `json:"node_type"`
	Attempt              int32           `json:"attempt"`
	Objective            string          `json:"objective"`
	NodeTitle            string          `json:"node_title"`
	NodeDescription      string          `json:"node_description,omitempty"`
	InputContract        json.RawMessage `json:"input_contract,omitempty"`
	OutputContract       json.RawMessage `json:"output_contract,omitempty"`
	ExpectedResultSchema json.RawMessage `json:"expected_result_schema,omitempty"`
	PriorEvidenceSummary string          `json:"prior_evidence_summary,omitempty"`
	ChangeRequest        string          `json:"change_request,omitempty"`
	AcceptanceCriteria   json.RawMessage `json:"acceptance_criteria,omitempty"`
	ContextRefs          json.RawMessage `json:"context_refs,omitempty"`
}

func ParseOrchestrationTaskContext(raw []byte) (OrchestrationTaskContext, bool) {
	if len(raw) == 0 {
		return OrchestrationTaskContext{}, false
	}
	var ctx OrchestrationTaskContext
	if err := json.Unmarshal(raw, &ctx); err != nil {
		return OrchestrationTaskContext{}, false
	}
	if ctx.Type != orchestrationContextType || ctx.OrchestrationPlanID == "" || ctx.OrchestrationNodeID == "" {
		return OrchestrationTaskContext{}, false
	}
	return ctx, true
}
