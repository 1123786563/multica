ALTER TABLE orchestration_node
    ADD COLUMN IF NOT EXISTS plan_id UUID REFERENCES orchestration_plan(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS reason_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS recommended_action TEXT NOT NULL DEFAULT 'none',
    ADD COLUMN IF NOT EXISTS agent_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
            AND table_name = 'orchestration_node'
            AND column_name = 'run_id'
    ) THEN
        ALTER TABLE orchestration_node ALTER COLUMN run_id DROP NOT NULL;
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_orchestration_node_plan_key_attempt
    ON orchestration_node(plan_id, workflow_node_key, attempt)
    WHERE plan_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_orchestration_node_plan
    ON orchestration_node(plan_id, created_at ASC)
    WHERE plan_id IS NOT NULL;

ALTER TABLE agent_task_queue
    ADD COLUMN IF NOT EXISTS orchestration_plan_id UUID REFERENCES orchestration_plan(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS orchestration_node_id UUID REFERENCES orchestration_node(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS orchestration_attempt INT,
    ADD COLUMN IF NOT EXISTS temporal_workflow_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_task_queue_orchestration_attempt
    ON agent_task_queue(orchestration_plan_id, orchestration_node_id, orchestration_attempt)
    WHERE orchestration_plan_id IS NOT NULL
        AND orchestration_node_id IS NOT NULL
        AND orchestration_attempt IS NOT NULL;
