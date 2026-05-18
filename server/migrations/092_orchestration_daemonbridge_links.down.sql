DROP INDEX IF EXISTS idx_agent_task_queue_orchestration_attempt;

ALTER TABLE agent_task_queue
    DROP COLUMN IF EXISTS temporal_workflow_id,
    DROP COLUMN IF EXISTS orchestration_attempt,
    DROP COLUMN IF EXISTS orchestration_node_id,
    DROP COLUMN IF EXISTS orchestration_plan_id;

DROP INDEX IF EXISTS idx_orchestration_node_plan;
DROP INDEX IF EXISTS idx_orchestration_node_plan_key_attempt;

ALTER TABLE orchestration_node
    DROP COLUMN IF EXISTS completed_at,
    DROP COLUMN IF EXISTS started_at,
    DROP COLUMN IF EXISTS agent_task_id,
    DROP COLUMN IF EXISTS recommended_action,
    DROP COLUMN IF EXISTS reason_code,
    DROP COLUMN IF EXISTS plan_id;
