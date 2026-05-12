# Issue Detail orchestration API returns Decision Panel summaries

Status: ready-for-agent
Type: AFK
Risk: Medium

## What to build

Build the issue-scoped read contract that powers the Decision Panel. The API should return server-derived node summaries rather than forcing clients to infer orchestration state from raw Kernel Events.

The response should include run state, node state, reason codes, recommended actions, evidence counts, latest summaries, linked task ids, permissions, and expandable event/evidence detail. The core client schema should degrade safely when the backend adds fields or returns malformed optional data.

## Acceptance criteria

- [ ] The issue orchestration read API returns Decision Panel-ready summaries for run and nodes.
- [ ] `reason_code` and `recommended_action` are derived server-side.
- [ ] The response includes linked task ids, attempts, evidence counts, latest summaries, events, evidence detail, and permission flags.
- [ ] Read authorization follows Issue read permission and does not introduce a separate evidence side channel.
- [ ] Core API client schemas parse the response with fallbacks for missing fields, unknown enum values, and malformed optional arrays.
- [ ] Backend and core tests cover read permissions, derived reason/action values, malformed response fallback, and unknown enum downgrade behavior.

## Agent / human ownership

Suitable for agent implementation.

## Blocked by

- 03-completed-task-records-evidence-and-verifies
- 05-risk-and-verification-failure-approval
