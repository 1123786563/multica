# Run cancellation and node recovery preserve audit history

Status: ready-for-agent
Type: AFK
Risk: High

## What to build

Build the cancellation and recovery slice that keeps orchestration safe across failures, Issue cancellation, and server restarts. Recovery should operate at node granularity, preserve completed nodes, Kernel Events, and Node Evidence, and use linked Agent Task state to repair interrupted advancement.

Cancellation should stop active execution without deleting history.

## Acceptance criteria

- [ ] Cancelling an Orchestration Run cancels active nodes and linked queued/dispatched/running Agent Tasks.
- [ ] Moving an Issue to `cancelled` cancels its Active Run and linked active execution.
- [ ] Completed tasks, Kernel Events, and Node Evidence remain available after cancellation.
- [ ] A lightweight recovery scan can detect a linked Agent Task that completed or failed while advancement was interrupted and safely advance or block the node.
- [ ] Recovery does not rerun completed nodes or create duplicate Agent Tasks for an existing node attempt.
- [ ] Tests cover run cancellation, Issue cancellation, task cancellation integration, completed-history preservation, recovery scan repair, and unrecoverable run failure.

## Agent / human ownership

Suitable for agent implementation. Human review recommended because cancellation touches existing task lifecycle behavior.

## Blocked by

- 02-execute-node-dispatches-agent-task
- 04-evidence-insufficient-retries-node
