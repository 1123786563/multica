# End-to-end orchestration acceptance paths

Status: done
Type: AFK
Risk: High

## What to build

Build the minimum end-to-end acceptance coverage for AI-native orchestration kernel v1. The tests should prove the complete vertical behavior across issue creation/assignment, kernel run creation, Agent Task execution, verification, Issue Detail trace visibility, and orchestration-by-default behavior even when historical rollout flags remain on the workspace row.

This slice should focus on stable acceptance paths, not exhaustive state-machine coverage already handled by service/API tests.

## Acceptance criteria

- [x] E2E covers happy path: agent-assigned Issue creates run, dispatches task, completes with valid result, verifies, moves Issue to `in_review`, and shows trace in Issue Detail.
- [x] E2E covers recovery/blocked path: malformed result causes retry or approval, and the Decision Panel exposes reason/action/evidence.
- [x] E2E verifies historical rollout-flag values do not prevent the active orchestration path for new agent-assigned Issues.
- [x] E2E verifies `orchestration:updated` or equivalent refresh makes trace changes visible without manual reload.
- [x] Tests are deterministic enough for CI and avoid depending on live external agent providers.
- [x] The final suite documents any required fixtures or fake daemon behavior used for orchestration acceptance.

## Implementation notes

- Added `e2e/orchestration.spec.ts` with deterministic fake runtime/agent fixtures.
- Added fixture helpers in `e2e/fixtures.ts` to create an online agent/runtime and drive daemon task claim/start/complete through HTTP APIs.
- Historical rollout-flag values are still writable in fixtures for regression coverage, but handler/E2E assertions now expect new agent-assigned Issues to enter the orchestration plan/node/task path even when `settings.orchestration_enabled` is false.

## Agent / human ownership

Suitable for agent implementation once prior slices are complete. Human review recommended for test stability.

## Blocked by

- 01-active-run-for-agent-assigned-issue
- 02-execute-node-dispatches-agent-task
- 03-completed-task-records-evidence-and-verifies
- 04-evidence-insufficient-retries-node
- 05-risk-and-verification-failure-approval
- 06-run-cancellation-and-recovery
- 07-decision-panel-read-api
- 08-orchestration-refresh-ws
- 09-issue-detail-decision-panel
- 10-attention-comments
