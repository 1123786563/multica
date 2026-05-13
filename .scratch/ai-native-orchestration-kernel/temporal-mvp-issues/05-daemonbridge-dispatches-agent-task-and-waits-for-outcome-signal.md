# DaemonBridge dispatches Agent Task and waits for outcome Signal

Label: ready-for-agent
Type: AFK
Risk: High

## Parent

.scratch/ai-native-orchestration-kernel/temporal-mvp-prd-github-issue.md

## What to build

Build the first real execution bridge. The Temporal dispatch Activity should create or reuse one daemon-backed Agent Task for the current node attempt, link it to the orchestration projection, return immediately, and let the Workflow wait for an Agent Task outcome Signal.

This slice should prove the happy path where daemon task completion records outcome/projection and signals the waiting Workflow to continue.

## Acceptance criteria

- [ ] Dispatch Activity creates or reuses exactly one Agent Task for the current run, node, and attempt.
- [ ] The Agent Task is linked to orchestration plan, node, attempt, task identity, and Temporal Workflow identity.
- [ ] Dispatch Activity returns after task creation/linking and does not poll for completion.
- [ ] Existing task completion/failure/cancellation APIs can send the corresponding Temporal Signal or Update.
- [ ] A matching completed-task Signal advances the Workflow to the next node.
- [ ] Tests cover dispatch idempotency, happy-path completion Signal, projection update, and no polling wait inside the Activity.

## Blocked by

- 03-projection-activities-render-fixed-workflow-progress
- 04-eino-analyze-activity-produces-coding-guidance

