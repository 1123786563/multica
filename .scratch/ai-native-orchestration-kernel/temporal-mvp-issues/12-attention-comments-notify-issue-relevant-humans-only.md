# Attention comments notify Issue-relevant humans only

Label: ready-for-agent
Type: AFK
Risk: Medium

## Parent

.scratch/ai-native-orchestration-kernel/temporal-mvp-prd-github-issue.md

## What to build

Build exceptional-state attention comments and audience selection. Orchestration should create Issue-visible attention comments only when humans need to act or investigate, and notify only Issue-relevant humans. Successful runs should not create default attention comments.

This slice should prove low-noise collaboration behavior for approval waits, failures, retry exhaustion, repair failure, and Temporal unavailable fail-closed states.

## Acceptance criteria

- [x] `waiting_human` creates an attention comment or equivalent Issue-visible activity.
- [x] Run failed, retry exhausted, repair failed, and Temporal unavailable fail-closed states create attention comments.
- [x] Successful orchestration does not create a default attention comment.
- [x] Attention audience includes Issue creator, human assignee, and subscribers/watchers.
- [x] Attention audience excludes agent assignee and unrelated workspace members.
- [x] Attention comments are deduplicated for repeated projection/repair events.
- [x] Tests cover each trigger condition, success no-comment behavior, audience selection, deduplication, and no agent mention loop.

## Blocked by

- 08-outcome-policy-routes-risks-and-failed-tests-to-approval-gate
- 09-human-only-approval-actions-with-audit-events
- 10-cancellation-propagates-to-temporal-and-active-agent-task
