CREATE TABLE orchestration_plan (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    source_type TEXT NOT NULL CHECK (source_type IN ('issue', 'chat', 'autopilot', 'api', 'quick_create')),
    source_id UUID NOT NULL,
    objective TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'planning' CHECK (status IN ('planning', 'ready', 'running', 'waiting_human', 'completed', 'failed', 'cancelled')),
    policy JSONB NOT NULL DEFAULT '{}',
    metadata JSONB NOT NULL DEFAULT '{}',
    created_by_type TEXT CHECK (created_by_type IN ('member', 'agent', 'system')),
    created_by_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orchestration_plan_workspace ON orchestration_plan(workspace_id);
CREATE INDEX idx_orchestration_plan_source ON orchestration_plan(source_type, source_id);
CREATE INDEX idx_orchestration_plan_status ON orchestration_plan(workspace_id, status);

CREATE TABLE orchestration_node (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES orchestration_plan(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('clarify', 'inspect', 'design', 'implement', 'test', 'review', 'fix', 'deploy', 'approval', 'summarize')),
    title TEXT NOT NULL,
    description TEXT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready', 'dispatched', 'running', 'evaluating', 'completed', 'failed', 'blocked', 'waiting_human', 'skipped', 'cancelled')),
    assignee_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    input_contract JSONB NOT NULL DEFAULT '{}',
    output_contract JSONB NOT NULL DEFAULT '{}',
    evaluator_policy JSONB NOT NULL DEFAULT '{}',
    retry_policy JSONB NOT NULL DEFAULT '{}',
    runtime_constraints JSONB NOT NULL DEFAULT '{}',
    attempt_count INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 2,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orchestration_node_plan ON orchestration_node(plan_id);
CREATE INDEX idx_orchestration_node_status ON orchestration_node(plan_id, status);
CREATE INDEX idx_orchestration_node_agent ON orchestration_node(assignee_agent_id, status);

CREATE TABLE orchestration_edge (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES orchestration_plan(id) ON DELETE CASCADE,
    from_node_id UUID NOT NULL REFERENCES orchestration_node(id) ON DELETE CASCADE,
    to_node_id UUID NOT NULL REFERENCES orchestration_node(id) ON DELETE CASCADE,
    type TEXT NOT NULL DEFAULT 'blocks' CHECK (type IN ('blocks', 'data_dep', 'approval_dep', 'review_dep')),
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(from_node_id, to_node_id, type)
);

CREATE INDEX idx_orchestration_edge_plan ON orchestration_edge(plan_id);
CREATE INDEX idx_orchestration_edge_to ON orchestration_edge(to_node_id);

CREATE TABLE orchestration_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES orchestration_plan(id) ON DELETE CASCADE,
    node_id UUID REFERENCES orchestration_node(id) ON DELETE SET NULL,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    event_type TEXT NOT NULL,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('kernel', 'agent', 'member', 'system')),
    actor_id UUID,
    payload JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orchestration_event_plan ON orchestration_event(plan_id, created_at);
CREATE INDEX idx_orchestration_event_node ON orchestration_event(node_id, created_at);

CREATE TABLE orchestration_artifact (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES orchestration_plan(id) ON DELETE CASCADE,
    node_id UUID REFERENCES orchestration_node(id) ON DELETE SET NULL,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    type TEXT NOT NULL CHECK (type IN ('diff', 'file', 'log', 'test_result', 'pr', 'decision', 'review_result', 'command_output', 'summary')),
    uri TEXT,
    content JSONB NOT NULL DEFAULT '{}',
    metadata JSONB NOT NULL DEFAULT '{}',
    content_hash TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orchestration_artifact_plan ON orchestration_artifact(plan_id);
CREATE INDEX idx_orchestration_artifact_node ON orchestration_artifact(node_id);
CREATE INDEX idx_orchestration_artifact_task ON orchestration_artifact(task_id);

ALTER TABLE agent_task_queue
    ADD COLUMN orchestration_plan_id UUID REFERENCES orchestration_plan(id) ON DELETE SET NULL,
    ADD COLUMN orchestration_node_id UUID REFERENCES orchestration_node(id) ON DELETE SET NULL,
    ADD COLUMN orchestration_run_id UUID;

CREATE INDEX idx_agent_task_queue_orchestration_node ON agent_task_queue(orchestration_node_id, status);
CREATE INDEX idx_agent_task_queue_orchestration_plan ON agent_task_queue(orchestration_plan_id, status);

CREATE INDEX idx_agent_task_queue_context_orchestration_node
ON agent_task_queue((context->>'orchestration_node_id'), status)
WHERE context ? 'orchestration_node_id';

CREATE INDEX idx_agent_task_queue_context_orchestration_plan
ON agent_task_queue((context->>'orchestration_plan_id'), status)
WHERE context ? 'orchestration_plan_id';
