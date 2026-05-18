export interface IssueOrchestrationSummary {
  reason_code: string;
  recommended_action: string;
}

export interface IssueOrchestrationNode {
  id: string;
  node_key: string;
  workflow_node_key: string;
  title: string;
  status: string;
  reason_code: string;
  recommended_action: string;
  attempt: number;
}

export interface IssueOrchestrationEvent {
  id: string;
  node_id?: string;
  type: string;
  source: string;
  message: string;
  details: Record<string, unknown>;
}

export interface IssueOrchestrationArtifact {
  id: string;
  node_id?: string;
  type: string;
  source: string;
  label: string;
  uri?: string;
  data: Record<string, unknown>;
}

export interface IssueOrchestrationPlan {
  id: string;
  issue_id: string;
  status: string;
  temporal_workflow_id?: string;
  temporal_run_id?: string;
  workflow_type: string;
  projection_version: number;
  created_at: string;
  updated_at: string;
  summary: IssueOrchestrationSummary;
  nodes: IssueOrchestrationNode[];
  events: IssueOrchestrationEvent[];
  artifacts: IssueOrchestrationArtifact[];
}

export interface IssueOrchestration {
  plans: IssueOrchestrationPlan[];
}
