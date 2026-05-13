# ADR 0003: Agent Task outcomes signal Temporal workflows

## Status

Accepted

## Context

Temporal is now the source of truth for Orchestration Run lifecycle state, while daemon-backed Agent Tasks remain the execution unit for coding work. A daemon task may run for minutes or hours, so holding a Temporal Activity open to poll Multica's database would blur lifecycle ownership and make retries risk duplicate code execution.

## Decision

Dispatching an Agent Task is a short Temporal Activity that creates or links the task and then returns. The Temporal Workflow waits for an Agent Task outcome signal. When the daemon calls Multica's existing completion, failure, or cancellation APIs, Multica records the Agent Task outcome, updates the projection, and sends an `AgentTaskCompleted`, `AgentTaskFailed`, or `AgentTaskCancelled` Signal or Update to the owning Temporal Workflow.

Outcome signals must carry `plan_id`, `node_id`, `attempt`, `task_id`, `outcome_version`, and a result reference or payload. The Workflow validates that the signal matches the current waiting node attempt before advancing.

## Consequences

The Agent Task outcome path must be idempotent: duplicate daemon callbacks, retrying API handlers, or repeated Temporal signals must not create duplicate node attempts or advance a workflow twice. Stale attempts, wrong tasks, and mismatched nodes are recorded or ignored but do not advance the Workflow. Polling can exist only as repair or reconciliation, not as the primary completion path.
