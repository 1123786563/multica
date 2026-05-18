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

ALTER TABLE orchestration_event
    ADD COLUMN IF NOT EXISTS plan_id UUID REFERENCES orchestration_plan(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS node_id UUID REFERENCES orchestration_node(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS type TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'server',
    ADD COLUMN IF NOT EXISTS message TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS details JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_name = 'orchestration_event'
            AND column_name = 'run_id'
    ) THEN
        ALTER TABLE orchestration_event ALTER COLUMN run_id DROP NOT NULL;
    END IF;
END $$;

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

ALTER TABLE orchestration_artifact
    ADD COLUMN IF NOT EXISTS plan_id UUID REFERENCES orchestration_plan(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS node_id UUID REFERENCES orchestration_node(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS type TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'server',
    ADD COLUMN IF NOT EXISTS label TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS uri TEXT,
    ADD COLUMN IF NOT EXISTS data JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_name = 'orchestration_artifact'
            AND column_name = 'run_id'
    ) THEN
        ALTER TABLE orchestration_artifact ALTER COLUMN run_id DROP NOT NULL;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_orchestration_artifact_plan
    ON orchestration_artifact(plan_id, created_at ASC);
