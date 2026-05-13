# Agent-assigned Issue creates an Active Orchestration Run

Status: done
Type: AFK
Risk: High

## What to build

Build the first thin path where an agent-assigned Issue creates or reuses an Active Orchestration Run. The slice should include the minimal persistence, service behavior, issue-scoped read API, and tests needed to verify that an Issue now has a server-owned orchestration lifecycle without changing the existing Agent Task execution path yet.

This slice should create the deterministic initial run shape with `plan`, `execute`, and `verify` nodes, record initial Kernel Events, and expose the run through the issue orchestration read API. Workspaces without orchestration enabled should be unaffected.

## Acceptance criteria

- [x] When an Issue is assigned to an Agent, the server creates an Active Orchestration Run with `plan`, `execute`, and `verify` nodes.
- [x] Re-triggering orchestration for the same Issue while the run is active reuses the existing Active Run and does not create a parallel run.
- [x] The issue-scoped orchestration read API returns the active/latest run, nodes, initial events, and enough metadata for clients to verify the run exists.
- [x] Historical `workspace.settings.orchestration_enabled` values no longer block the active orchestration path for new agent-assigned Issues.
- [x] Backend tests cover active-run idempotency, workspace flag behavior, issue read permissions, and initial event creation.

## Agent / human ownership

Suitable for agent implementation. Human review recommended because this establishes the first persistence and state-machine invariants.

## Blocked by

None - can start immediately
