# Evidence-insufficient output retries the execute node safely

Status: ready-for-agent
Type: AFK
Risk: Medium

## What to build

Build the recoverable malformed-output path. If an Agent Task completes operationally but does not provide a valid Result Schema or required structured evidence, the Agent Task may remain completed, but the orchestration node must not succeed. The kernel should classify the result as Evidence Insufficient, record events/evidence, and retry the node within the Node Retry Policy.

This slice should prove that output-format failures are recoverable without losing prior events or creating duplicate task attempts.

## Acceptance criteria

- [ ] Missing, malformed, or unknown-version Result Schema produces `evidence_insufficient` instead of node success.
- [ ] Recoverable evidence-insufficient output automatically schedules a new node attempt up to the configured max attempts.
- [ ] Each retry creates a new linked Agent Task attempt while preserving prior Kernel Events and Node Evidence.
- [ ] Retry exhaustion moves the run/node to the appropriate blocked state for human attention instead of looping indefinitely.
- [ ] The read API shows attempts, reason code, recommended action, and prior evidence summary.
- [ ] Tests cover malformed result parsing, unknown schema version, retry scheduling, max-attempt handling, and no duplicate task dispatch for the same attempt.

## Agent / human ownership

Suitable for agent implementation.

## Blocked by

- 03-completed-task-records-evidence-and-verifies
