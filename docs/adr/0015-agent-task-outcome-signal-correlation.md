# ADR 0015: Agent Task outcome signals are correlated to node attempts

Status: Accepted

## Context

Agent Task outcomes can arrive more than once. The daemon may retry callbacks, API handlers may retry Temporal signal delivery, and repair jobs may backfill missing signals from recorded task outcomes. Node retry also means an older Agent Task attempt can finish after a newer attempt is already waiting or completed.

If the Workflow advances on any signal addressed only to its Workflow ID, stale or duplicate outcomes can advance the wrong node attempt.

## Decision

Every Agent Task outcome signal must include enough correlation data to identify the exact waiting node attempt:

- `plan_id`
- `node_id`
- `attempt`
- `task_id`
- `outcome_version`
- a result reference or structured result payload

The Workflow advances only when the signal matches the current waiting `plan_id`, `node_id`, `attempt`, and linked `task_id`.

Duplicate signals for an already processed outcome are idempotent no-ops. Signals for stale attempts, wrong nodes, wrong tasks, wrong plans, or unsupported outcome versions are recorded or ignored without advancing the Workflow.

Ignored or rejected signals use low-noise audit event types such as `signal.duplicate_ignored`, `signal.stale_ignored`, and `signal.mismatched_rejected`.

Repair and reconciliation paths must use the same signal contract.

## Consequences

Workflow tests must cover matching signal advancement, duplicate signal no-op behavior, stale attempt rejection, wrong task rejection, wrong node rejection, repair-job signal replay, and projection of low-noise signal audit events.

The Signal contract becomes part of the orchestration evidence boundary and should be versioned through `outcome_version`.
