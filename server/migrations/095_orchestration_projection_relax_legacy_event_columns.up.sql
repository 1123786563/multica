DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_name = 'orchestration_event'
            AND column_name = 'event_key'
    ) THEN
        ALTER TABLE orchestration_event ALTER COLUMN event_key SET DEFAULT '';
        ALTER TABLE orchestration_event ALTER COLUMN event_key DROP NOT NULL;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_name = 'orchestration_event'
            AND column_name = 'event_type'
    ) THEN
        ALTER TABLE orchestration_event ALTER COLUMN event_type SET DEFAULT '';
        ALTER TABLE orchestration_event ALTER COLUMN event_type DROP NOT NULL;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_name = 'orchestration_artifact'
            AND column_name = 'issue_id'
    ) THEN
        ALTER TABLE orchestration_artifact ALTER COLUMN issue_id DROP NOT NULL;
    END IF;
END $$;
