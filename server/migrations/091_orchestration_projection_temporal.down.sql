DROP TABLE IF EXISTS orchestration_artifact;
DROP TABLE IF EXISTS orchestration_event;
DROP INDEX IF EXISTS idx_agent_task_queue_orchestration_attempt;
ALTER TABLE agent_task_queue
    DROP COLUMN IF EXISTS temporal_workflow_id,
    DROP COLUMN IF EXISTS orchestration_attempt,
    DROP COLUMN IF EXISTS orchestration_node_id,
    DROP COLUMN IF EXISTS orchestration_plan_id;
DROP TABLE IF EXISTS orchestration_node;
DROP INDEX IF EXISTS idx_orchestration_plan_issue_created;
DROP INDEX IF EXISTS idx_orchestration_plan_one_active;
DROP TABLE IF EXISTS orchestration_plan;
