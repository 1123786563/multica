# Review handoff completes orchestration without auto-closing Issue

Label: ready-for-agent
Type: AFK
Risk: Medium

## Parent

.scratch/ai-native-orchestration-kernel/temporal-mvp-prd-github-issue.md

## What to build

Build advisory review, result summarization, and review handoff completion. A successful orchestration run should produce reviewable evidence, summary, trace, and artifacts, and may move the Issue into a review-like status, but it must not mark the Issue done or treat orchestration completion as business acceptance.

This slice should prove Eino review remains advisory and `complete_issue` is a handoff, not final closure.

## Acceptance criteria

- [x] Eino review summarizes evidence, concerns, risks, and recommended policy action without authoritative success fields.
- [x] Positive Eino review cannot override deterministic validation, failed tests, risks, missing evidence, or approval rules.
- [x] Summary output creates a review handoff summary with trace and artifact references.
- [x] `complete_issue` writes summary/projection and may move the Issue to a review-like state.
- [x] `complete_issue` does not mark the Issue done, close it, or hide orchestration evidence.
- [x] Tests cover advisory review limits, summary projection, review handoff state, and no auto-done behavior.

## Blocked by

- 06-signal-correlation-rejects-stale-and-mismatched-outcomes
- 07-deterministic-result-validation-and-evidence-insufficient-retry
