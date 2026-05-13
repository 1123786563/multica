# ADR 0014: Projection and side effects happen only through Activities

Status: Accepted

## Context

Temporal Workflow code is replayed. If Workflow code writes Multica tables, sends WebSocket notifications, writes comments, calls Eino, or cancels Agent Tasks directly, replay can duplicate side effects or make workflow history non-deterministic.

The orchestration MVP also reuses Multica's `orchestration_*` tables as a projection, so projection writes must stay reliable without becoming the lifecycle source of truth.

## Decision

Workflow code is deterministic and does not directly perform external side effects.

All of the following must happen in Activities:

- writes or repairs to `orchestration_plan`, `orchestration_node`, `orchestration_event`, and `orchestration_artifact`;
- creation, update, or cancellation of `agent_task_queue` rows;
- Eino, LLM, HTTP, filesystem, daemon bridge, or Multica API calls;
- Issue comments, Issue status nudges, attention comments, notifications, and WebSocket refreshes;
- cancellation propagation to an active Agent Task.

Activities must be idempotent using stable keys such as `plan_id`, `node_id`, `attempt`, `event_type`, `temporal_workflow_id`, and linked Agent Task IDs.

Workflow code may make deterministic policy decisions, wait for signals, schedule Activities, handle timers, and store local workflow state. When projection lags or conflicts with Temporal history, a repair or reconciliation Activity updates the projection from the authoritative workflow state.

## Consequences

Replay cannot duplicate Multica-visible side effects. Tests must cover Workflow replay determinism separately from Activity idempotency and projection repair.
