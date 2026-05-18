# Projection Activities render fixed workflow progress

Label: ready-for-agent
Type: AFK
Risk: High

## Parent

.scratch/ai-native-orchestration-kernel/temporal-mvp-prd-github-issue.md

## What to build

Build projection Activities and the first visible fixed workflow progress path. Temporal Workflow code must remain deterministic and schedule Activities for all Multica-visible side effects. The issue orchestration read API and Issue Detail linear panel should show fixed-chain node progress from projection data.

This slice should prove projection table reuse, Activity-owned writes, coarse refresh invalidation, and a minimal UI process view.

## Acceptance criteria

- [x] Workflow code does not directly write projection tables, comments, notifications, WebSocket events, or Agent Task side effects.
- [x] Projection Activities idempotently write run, node, and event progress for the fixed MVP workflow chain.
- [x] The issue-scoped read API returns node summaries suitable for a Linear Orchestration Panel.
- [x] A coarse `orchestration:updated` refresh causes clients to reload issue-scoped orchestration data.
- [x] Tests cover Activity idempotency, workflow replay determinism, projection ordering, read API shape, and minimal panel rendering.

## Blocked by

- 02-start-workflow-with-active-run-idempotency
