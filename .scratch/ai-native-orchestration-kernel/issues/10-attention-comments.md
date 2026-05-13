# Attention comments notify issue-relevant humans only

Status: done
Type: AFK
Risk: Medium

## What to build

Build the notification slice for orchestration states that need human attention. The kernel should create Issue comments or activity when a run waits for approval, exhausts retries, or fails. Successful orchestration should not create a comment by default.

The audience should stay scoped to Issue-relevant humans: creator, human assignee, and subscribers. The system should not broadcast to the whole workspace or mention the agent assignee.

## Acceptance criteria

- [x] `waiting_for_approval`, retry exhaustion, and run failure create an Attention Comment or equivalent Issue-visible activity.
- [x] Successful verification does not create a success comment by default.
- [x] Notifications target Issue creator, human assignee, and subscribers, with deduplication.
- [x] Agent assignees are not mentioned or notified as if they were approval owners.
- [x] The comment content includes the reason, recommended action, and a concise evidence summary.
- [x] Tests cover each attention state, success no-comment behavior, audience selection, deduplication, and no agent mention loop.

## Agent / human ownership

Suitable for agent implementation.

## Blocked by

- 05-risk-and-verification-failure-approval
- 06-run-cancellation-and-recovery
