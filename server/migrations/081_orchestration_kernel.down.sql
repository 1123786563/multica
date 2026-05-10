DROP INDEX IF EXISTS idx_agent_task_queue_context_orchestration_plan;
DROP INDEX IF EXISTS idx_agent_task_queue_context_orchestration_node;
DROP INDEX IF EXISTS idx_agent_task_queue_orchestration_plan;
DROP INDEX IF EXISTS idx_agent_task_queue_orchestration_node;

ALTER TABLE agent_task_queue
    DROP COLUMN IF EXISTS orchestration_run_id,
    DROP COLUMN IF EXISTS orchestration_node_id,
    DROP COLUMN IF EXISTS orchestration_plan_id;

DROP TABLE IF EXISTS orchestration_artifact;
DROP TABLE IF EXISTS orchestration_event;
DROP TABLE IF EXISTS orchestration_edge;
DROP TABLE IF EXISTS orchestration_node;
DROP TABLE IF EXISTS orchestration_plan;
