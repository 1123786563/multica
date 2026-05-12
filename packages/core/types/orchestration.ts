export type OrchestrationRunStatus = "active" | "succeeded" | "failed" | "cancelled";

export type OrchestrationRunSource = "issue_assignment" | "manual_retry" | "recovery";

export type OrchestrationNodeKind = "plan" | "execute" | "verify";

export type OrchestrationNodeStatus =
  | "pending"
  | "ready"
  | "running"
  | "waiting"
  | "succeeded"
  | "failed"
  | "cancelled";

export interface OrchestrationRun {
  id: string;
  workspace_id: string;
  issue_id: string;
  status: OrchestrationRunStatus;
  source: OrchestrationRunSource;
  plan_version: number;
  created_by_type: "member" | "agent" | "system" | null;
  created_by_id: string | null;
  created_at: string;
  updated_at: string;
}

export interface OrchestrationNode {
  id: string;
  run_id: string;
  workspace_id: string;
  issue_id: string;
  key: string;
  kind: OrchestrationNodeKind;
  status: OrchestrationNodeStatus;
  position: number;
  dependencies: string[];
  agent_task_id: string | null;
  attempt: number;
  metadata: Record<string, unknown>;
  started_at: string | null;
  completed_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface OrchestrationEvent {
  id: string;
  run_id: string;
  node_id: string | null;
  workspace_id: string;
  issue_id: string;
  type: string;
  message: string | null;
  metadata: Record<string, unknown>;
  created_at: string;
}

export interface OrchestrationEvidence {
  id: string;
  run_id: string;
  node_id: string;
  workspace_id: string;
  issue_id: string;
  agent_task_id: string | null;
  kind: string;
  summary: string | null;
  data: Record<string, unknown>;
  created_at: string;
}

export interface IssueOrchestration {
  run: OrchestrationRun | null;
  nodes: OrchestrationNode[];
  events: OrchestrationEvent[];
  evidence: OrchestrationEvidence[];
}
