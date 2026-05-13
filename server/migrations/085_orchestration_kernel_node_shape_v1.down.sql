ALTER TABLE orchestration_node
    DROP CONSTRAINT IF EXISTS orchestration_node_type_check;

ALTER TABLE orchestration_node
    ADD CONSTRAINT orchestration_node_type_check
    CHECK (type IN ('clarify', 'inspect', 'design', 'implement', 'test', 'review', 'fix', 'deploy', 'approval', 'summarize'));
