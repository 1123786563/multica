CREATE TABLE IF NOT EXISTS orchestration_plan (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('starting', 'running', 'waiting_human', 'completed', 'failed', 'cancelled')),
    reason_code TEXT NOT NULL DEFAULT '',
    recommended_action TEXT NOT NULL DEFAULT 'none',
    temporal_workflow_id TEXT,
    temporal_run_id TEXT,
    workflow_type TEXT NOT NULL DEFAULT 'issue_mvp',
    projection_version INT NOT NULL DEFAULT 1,
    last_synced_at TIMESTAMPTZ,
    sync_error TEXT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_orchestration_plan_one_active
    ON orchestration_plan(issue_id)
    WHERE status IN ('starting', 'running', 'waiting_human');

CREATE INDEX IF NOT EXISTS idx_orchestration_plan_issue_created
    ON orchestration_plan(issue_id, created_at DESC);

CREATE TABLE IF NOT EXISTS orchestration_node (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES orchestration_plan(id) ON DELETE CASCADE,
    workflow_node_key TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'completed', 'waiting_human', 'failed', 'cancelled')),
    reason_code TEXT NOT NULL DEFAULT '',
    recommended_action TEXT NOT NULL DEFAULT 'none',
    attempt INT NOT NULL DEFAULT 1,
    agent_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (plan_id, workflow_node_key, attempt)
);

CREATE INDEX IF NOT EXISTS idx_orchestration_node_plan
    ON orchestration_node(plan_id, created_at ASC);

CREATE TABLE IF NOT EXISTS orchestration_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES orchestration_plan(id) ON DELETE CASCADE,
    node_id UUID REFERENCES orchestration_node(id) ON DELETE SET NULL,
    type TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'server',
    message TEXT NOT NULL DEFAULT '',
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_orchestration_event_plan
    ON orchestration_event(plan_id, created_at ASC);

CREATE TABLE IF NOT EXISTS orchestration_artifact (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES orchestration_plan(id) ON DELETE CASCADE,
    node_id UUID REFERENCES orchestration_node(id) ON DELETE SET NULL,
    type TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'server',
    label TEXT NOT NULL DEFAULT '',
    uri TEXT,
    data JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_orchestration_artifact_plan
    ON orchestration_artifact(plan_id, created_at ASC);

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
