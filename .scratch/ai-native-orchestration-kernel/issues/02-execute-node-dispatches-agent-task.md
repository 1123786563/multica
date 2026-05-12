# Execute node dispatches through existing Agent Task lifecycle

Status: ready-for-agent
Type: AFK
Risk: High

## What to build

Build the Runtime Adapter slice that lets an `execute` node dispatch work through the existing Agent Task lifecycle. The kernel should not run agent CLIs or introduce a new daemon protocol. It should create and link an existing Agent Task, pass small Orchestration Context into the task, and preserve compatibility with the current daemon claim/start/message/complete/fail APIs.

This slice should also record the selected Agent's currently bound skill context as part of the orchestration context or node metadata, without introducing workspace-wide automatic skill selection.

## Acceptance criteria

- [ ] A ready `execute` node creates exactly one linked Agent Task through the existing task queue.
- [ ] The linked node records the Agent Task id, node attempt, and dispatch event in one consistent state transition.
- [ ] Re-running advancement or recovery for the same node attempt reuses the existing linked task instead of creating a duplicate.
- [ ] Daemon claim responses for orchestration-created tasks include Orchestration Context such as run id, node id, node type, attempt, expected result schema, prior evidence summary, and change request when present.
- [ ] Agent-bound skill context is visible to the task path using the existing Agent skill model; no global skill planner is introduced.
- [ ] Tests cover dispatch idempotency, enabled-vs-disabled workspace behavior, task claim compatibility, and agent-bound skill context preservation.

## Agent / human ownership

Suitable for agent implementation. Human review recommended because this touches the task dispatch boundary.

## Blocked by

- 01-active-run-for-agent-assigned-issue
