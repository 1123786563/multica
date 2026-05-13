# Cancellation propagates to Temporal and active Agent Task

Label: ready-for-agent
Type: AFK
Risk: High

## Parent

.scratch/ai-native-orchestration-kernel/temporal-mvp-prd-github-issue.md

## What to build

Build orchestration cancellation across Temporal and the active daemon-backed Agent Task. A cancel request should cancel the Temporal Workflow, propagate cancellation to any active linked Agent Task, preserve completed projection/evidence history, and show a cancelled state and audit trail in Issue Detail.

This slice should share the human approval permission and audit boundary where cancellation comes from Approval Gate.

## Acceptance criteria

- [ ] Cancel endpoint locates the active run and Temporal Workflow identity.
- [ ] Cancelling a run calls Temporal cancellation and updates projection after cancellation is accepted or observed.
- [ ] If an active linked Agent Task exists, cancellation propagates to that task.
- [ ] Completed node evidence, events, and artifacts remain inspectable after cancellation.
- [ ] Cancel audit/event trail is visible in expanded events.
- [ ] Tests cover cancel with active task, cancel with no active task, repeated cancel idempotency, projection status, and evidence preservation.

## Blocked by

- 05-daemonbridge-dispatches-agent-task-and-waits-for-outcome-signal
- 09-human-only-approval-actions-with-audit-events

