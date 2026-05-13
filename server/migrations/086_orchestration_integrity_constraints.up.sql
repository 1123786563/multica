-- B1: Enforce max 1 active orchestration plan per source (issue).
-- Prevents concurrent OnIssueAssigned calls from creating duplicate plans.
CREATE UNIQUE INDEX idx_orchestration_plan_unique_active_source
    ON orchestration_plan (source_type, source_id)
    WHERE status NOT IN ('completed', 'failed', 'cancelled');

-- B3: Enforce idempotent node dispatch — one task per (node, run) pair.
-- Prevents concurrent dispatchNodeTask calls from creating duplicate tasks.
CREATE UNIQUE INDEX idx_agent_task_unique_orchestration_dispatch
    ON agent_task_queue (orchestration_node_id, orchestration_run_id)
    WHERE orchestration_node_id IS NOT NULL;
