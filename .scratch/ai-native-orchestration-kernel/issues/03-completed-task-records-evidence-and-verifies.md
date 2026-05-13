# Completed Agent Task records Node Evidence and verifies successfully

Status: done
Type: AFK
Risk: High

## What to build

Build the successful completion path from a linked Agent Task back into the Orchestration Run. When the Agent Task completes with a valid versioned Result Schema, the kernel should extract structured Node Evidence, run deterministic hard-check verification, mark the verify path successful, and move the Issue to review without marking it done.

This slice should make `summary`, `changed_files`, `artifacts`, `tests`, and `risks` usable by the kernel and queryable through the issue orchestration API.

## Acceptance criteria

- [x] A completed linked Agent Task with `schema_version: 1` and valid evidence fields creates persisted Node Evidence.
- [x] Hard Check Verification succeeds when the result schema is valid, required fields are present, and risks do not require approval.
- [x] The execute/verify nodes and run reach successful terminal states with corresponding Kernel Events.
- [x] The Issue can move to `in_review`, but is not automatically moved to `done`.
- [x] The issue-scoped read API exposes evidence summary, latest summary, tests, artifacts, changed files, linked task id, and terminal run state.
- [x] Tests cover valid result parsing, evidence persistence, verification success, transaction consistency between state and Kernel Events, and Issue status coordination.

## Agent / human ownership

Suitable for agent implementation. Human review recommended because this defines the first evidence contract.

## Blocked by

- 02-execute-node-dispatches-agent-task
