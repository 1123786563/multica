ALTER TABLE orchestration_node
    DROP CONSTRAINT IF EXISTS orchestration_node_type_check;

ALTER TABLE orchestration_node
    ADD CONSTRAINT orchestration_node_type_check
    CHECK (type IN ('plan', 'execute', 'verify'));
