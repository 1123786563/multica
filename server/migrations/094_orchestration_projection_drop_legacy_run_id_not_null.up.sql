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

    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_name = 'orchestration_artifact'
            AND column_name = 'run_id'
    ) THEN
        ALTER TABLE orchestration_artifact ALTER COLUMN run_id DROP NOT NULL;
    END IF;
END $$;
