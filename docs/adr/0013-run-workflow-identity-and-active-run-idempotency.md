# ADR 0013: Workflow identity is per run and start is Active Run idempotent

Status: Accepted

## Context

Temporal Workflow IDs are durable identifiers with reuse and retention semantics. Multica also needs an Issue-level rule: one Issue should not have two active orchestration runs at the same time, while completed historical runs should remain inspectable.

Using one fixed Workflow ID per Issue would make reruns and Temporal reuse policy harder to reason about. Starting a new Workflow ID for every trigger without an Active Run guard would allow parallel orchestration for the same Issue.

## Decision

The MVP uses both boundaries:

- Multica enforces at most one active `orchestration_plan` per Issue before starting Temporal.
- Repeated start triggers for an Issue with an active plan return that existing plan and do not call Temporal StartWorkflow again.
- Each Orchestration Run gets its own Temporal Workflow ID derived from the projection `plan_id`, for example `multica/{workspace_id}/issue/{issue_id}/run/{plan_id}`.
- Completed, failed, and cancelled historical runs are preserved. A later run for the same Issue gets a new `plan_id` and a new Workflow ID.

If Temporal returns WorkflowAlreadyStarted for the same `temporal_workflow_id`, Multica treats it as an idempotent start result, repairs projection if needed, and returns the same run.

## Consequences

Start behavior is idempotent at the Issue Active Run boundary while Temporal history remains cleanly separated per run. Tests must cover duplicate start requests, concurrent start races, projection repair after WorkflowAlreadyStarted, and starting a new run after a prior terminal run.
