export type OrchestrationPlanStatus =
  | "planning"
  | "ready"
  | "running"
  | "waiting_human"
  | "completed"
  | "failed"
  | "cancelled";

export type OrchestrationNodeStatus =
  | "pending"
  | "ready"
  | "dispatched"
  | "running"
  | "evaluating"
  | "completed"
  | "failed"
  | "blocked"
  | "waiting_human"
  | "skipped"
  | "cancelled";

export interface OrchestrationPlan {
  id: string;
  workspace_id: string;
  source_type: string;
  source_id: string;
  objective: string;
  status: OrchestrationPlanStatus | string;
  policy: Record<string, unknown>;
  metadata: Record<string, unknown>;
  created_by_type: string | null;
  created_by_id: string | null;
  created_at: string;
  updated_at: string;
}

export interface OrchestrationNode {
  id: string;
  plan_id: string;
  type: string;
  title: string;
  description: string | null;
  status: OrchestrationNodeStatus | string;
  assignee_agent_id: string | null;
  input_contract: Record<string, unknown>;
  output_contract: Record<string, unknown>;
  evaluator_policy: Record<string, unknown>;
  retry_policy: Record<string, unknown>;
  runtime_constraints: Record<string, unknown>;
  attempt_count: number;
  max_attempts: number;
  linked_task_id: string | null;
  artifact_count: number;
  summary?: OrchestrationNodeSummary | null;
  permissions?: OrchestrationNodePermissions | null;
  approval_history?: OrchestrationApprovalHistoryItem[];
  started_at: string | null;
  completed_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface OrchestrationNodeSummary {
  status: string;
  reason_code: string;
  reason_title: string;
  reason_detail: string;
  recommended_action: string;
  action_enabled: boolean;
  attempt_count: number;
  max_attempts: number;
  latest_evaluation_status?: string;
  latest_agent_summary?: string;
  prior_evidence_summary?: string;
  updated_at?: string;
}

export interface OrchestrationNodePermissions {
  can_approve: boolean;
  can_request_changes: boolean;
  can_retry: boolean;
}

export interface OrchestrationApprovalHistoryItem {
  action: string;
  actor_type: string;
  actor_id: string | null;
  created_at: string;
  change_request?: string;
}

export interface OrchestrationEvent {
  id: string;
  plan_id: string;
  node_id: string | null;
  task_id: string | null;
  event_type: string;
  actor_type: string;
  actor_id: string | null;
  payload: Record<string, unknown>;
  created_at: string;
}

export interface OrchestrationArtifact {
  id: string;
  plan_id: string;
  node_id: string | null;
  task_id: string | null;
  type: string;
  uri: string | null;
  content: Record<string, unknown>;
  metadata: Record<string, unknown>;
  content_hash: string | null;
  created_at: string;
}

export interface IssueOrchestration {
  plans: OrchestrationPlan[];
  nodes: OrchestrationNode[];
  events: OrchestrationEvent[];
  artifacts: OrchestrationArtifact[];
}
