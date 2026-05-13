# ADR 0012: Node retry is limited to recoverable non-semantic failures

Status: Accepted

## Context

The Temporal-backed orchestration workflow needs retry behavior, but retry can mean different things:

- retrying a short Temporal Activity after a transient infrastructure error;
- creating a new Agent Task attempt for the same orchestration node;
- re-running code-modifying work after tests fail or risk is detected.

Automatically re-running code-modifying agent work can duplicate edits, hide risk, or obscure accountability.

## Decision

The MVP uses a conservative Node Retry Policy:

- A node allows at most two attempts total.
- Automatic Node retry is allowed only for recoverable, non-semantic failures: malformed schema, insufficient evidence, timeout, worker transient error, or signal delivery repair.
- Failed tests, non-empty risks, high-risk review concerns, unverifiable evidence, and destructive operations do not auto-retry. They enter an Approval Gate.
- A human `retry` action from the Approval Gate may create the next Agent Task attempt only while retry budget remains.
- Retry context comes from kernel-generated validation feedback and review concern summaries. The MVP does not accept human-authored `request changes` input.

Temporal Activity retries remain separate from Node retry. Retrying an Activity does not imply creating another Agent Task attempt unless the workflow's Node Retry Policy explicitly does so.

## Consequences

The first implementation avoids hidden duplicate code edits and keeps retry behavior testable. Workflow tests must distinguish Activity retry from Node retry and must assert that risk-bearing or semantically failed work pauses for human approval.

The configuration example and implementation should use `max_node_attempts = 2` for the MVP.
