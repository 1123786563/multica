# Start workflow with Active Run idempotency

Label: ready-for-agent
Type: AFK
Risk: High

## Parent

.scratch/ai-native-orchestration-kernel/temporal-mvp-prd-github-issue.md

## What to build

Build the happy-path orchestration start slice. A valid request should create or reuse one active Issue-scoped Orchestration Run, derive a per-run Temporal Workflow ID from the active projection plan, start the fixed MVP workflow, and expose the run through the issue orchestration read API.

This slice should prove that duplicate triggers and concurrent starts cannot create parallel active runs for the same Issue.

## Acceptance criteria

- [ ] Starting orchestration for an Issue creates one active orchestration plan projection and starts one Temporal Workflow.
- [ ] The Temporal Workflow ID is derived from workspace, Issue, and plan identity, not a fixed Issue-only ID.
- [ ] Repeating start while an active run exists returns the existing active run and does not call Temporal StartWorkflow again.
- [ ] Terminal historical runs remain inspectable, and a later rerun creates a new plan identity and new Workflow ID.
- [ ] Tests cover duplicate starts, concurrent start races, terminal rerun, and WorkflowAlreadyStarted projection repair.

## Blocked by

- 01-temporal-unavailable-fail-closed-start-path

