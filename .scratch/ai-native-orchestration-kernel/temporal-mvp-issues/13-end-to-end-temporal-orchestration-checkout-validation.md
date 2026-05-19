# End-to-end Temporal orchestration checkout validation

Label: ready-for-agent
Type: AFK
Risk: High

## Parent

.scratch/ai-native-orchestration-kernel/temporal-mvp-prd-github-issue.md

## What to build

Build and document the final checkout validation for the Temporal-backed orchestration MVP. The validation should prove the happy path, fail-closed path, and failure/retry/approval path across Temporal, Eino, daemon-backed Agent Task, projection, API, and Issue Detail.

This slice is complete only when it gives maintainers repeatable commands and evidence for the whole MVP cut line.

## Acceptance criteria

- [x] Documentation explains explicit local Temporal setup and orchestration worker startup.
- [x] Happy path validates start, analyze, dispatch, completed Signal, validation, review, summary, and review handoff.
- [x] Fail-closed path validates Temporal unavailable behavior and no direct Agent Task fallback.
- [x] Failure path validates malformed or insufficient evidence leading to retry or Approval Gate.
- [x] Issue Detail shows the full trace through Linear Orchestration Panel, expanded events, evidence, artifacts, and relevant audit events.
- [x] Validation commands include focused Go workflow/activity tests, backend API tests, frontend contract/component tests, and minimal E2E coverage.
- [x] Final evidence states which commands passed and any residual manual setup requirements.

## Blocked by

- 01-temporal-unavailable-fail-closed-start-path
- 02-start-workflow-with-active-run-idempotency
- 03-projection-activities-render-fixed-workflow-progress
- 04-eino-analyze-activity-produces-coding-guidance
- 05-daemonbridge-dispatches-agent-task-and-waits-for-outcome-signal
- 06-signal-correlation-rejects-stale-and-mismatched-outcomes
- 07-deterministic-result-validation-and-evidence-insufficient-retry
- 08-outcome-policy-routes-risks-and-failed-tests-to-approval-gate
- 09-human-only-approval-actions-with-audit-events
- 10-cancellation-propagates-to-temporal-and-active-agent-task
- 11-review-handoff-completes-orchestration-without-auto-closing-issue
- 12-attention-comments-notify-issue-relevant-humans-only
