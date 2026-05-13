# Outcome policy routes risks and failed tests to Approval Gate

Label: ready-for-agent
Type: AFK
Risk: High

## Parent

.scratch/ai-native-orchestration-kernel/temporal-mvp-prd-github-issue.md

## What to build

Build the deterministic Temporal Outcome Policy for semantic failures and human approval routing. Failed tests, non-empty risks, high-risk Eino review concerns, unverifiable evidence, destructive operations, or retry exhaustion should pause the run in an Approval Gate instead of auto-succeeding or auto-retrying code work.

This slice should make `waiting_human` visible through the read API and Linear Orchestration Panel.

## Acceptance criteria

- [ ] Outcome Policy combines validation outcome, advisory Eino review, risks, failed tests, missing evidence, approval state, and retry budget.
- [ ] Failed tests route to Approval Gate instead of automatic retry.
- [ ] Non-empty risks route to Approval Gate instead of automatic retry.
- [ ] High-risk advisory review concerns route to Approval Gate without letting Eino directly decide final outcome.
- [ ] Approval Gate projection includes reason, recommended action, failed tests or risks summary, and retry budget.
- [ ] Linear Orchestration Panel shows `waiting_human` state and available server-projected actions.
- [ ] Tests cover failed tests, risks, high-risk review, retry exhaustion, and positive Eino review not overriding policy.

## Blocked by

- 07-deterministic-result-validation-and-evidence-insufficient-retry

