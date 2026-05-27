ALTER TABLE orchestration_plan
    ADD COLUMN IF NOT EXISTS reasoning_profile_ref TEXT NOT NULL DEFAULT 'legacy/default';

COMMENT ON COLUMN orchestration_plan.reasoning_profile_ref IS
    'Stable worker-bound reasoning profile reference used for orchestration provider selection and trace correlation.';
