-- Server-owned orchestration kernel domain model.
-- v1 is issue-scoped and sits above the existing agent_task_queue lifecycle.

CREATE TABLE orchestration_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('active', 'succeeded', 'failed', 'cancelled')),
    source TEXT NOT NULL DEFAULT 'issue_assignment'
        CHECK (source IN ('issue_assignment', 'manual_retry', 'recovery')),
    plan_version INT NOT NULL DEFAULT 1,
    created_by_type TEXT CHECK (created_by_type IN ('member', 'agent', 'system')),
    created_by_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_orchestration_run_active_issue
    ON orchestration_run(issue_id)
    WHERE status = 'active';

CREATE INDEX idx_orchestration_run_workspace_issue_created
    ON orchestration_run(workspace_id, issue_id, created_at DESC);

CREATE TABLE orchestration_node (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES orchestration_run(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('plan', 'execute', 'verify')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'ready', 'running', 'waiting', 'succeeded', 'failed', 'cancelled')),
    position INT NOT NULL,
    dependencies TEXT[] NOT NULL DEFAULT '{}',
    agent_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    attempt INT NOT NULL DEFAULT 0,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(run_id, key)
);

CREATE INDEX idx_orchestration_node_run_position
    ON orchestration_node(run_id, position);

CREATE TABLE orchestration_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES orchestration_run(id) ON DELETE CASCADE,
    node_id UUID REFERENCES orchestration_node(id) ON DELETE SET NULL,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    message TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orchestration_event_run_created
    ON orchestration_event(run_id, created_at ASC, id ASC);

CREATE TABLE orchestration_evidence (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES orchestration_run(id) ON DELETE CASCADE,
    node_id UUID NOT NULL REFERENCES orchestration_node(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    agent_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    kind TEXT NOT NULL,
    summary TEXT,
    data JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orchestration_evidence_node_created
    ON orchestration_evidence(node_id, created_at ASC, id ASC);
