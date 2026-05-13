# Signal correlation rejects stale and mismatched outcomes

Label: ready-for-agent
Type: AFK
Risk: High

## Parent

.scratch/ai-native-orchestration-kernel/temporal-mvp-prd-github-issue.md

## What to build

Build strict Agent Task outcome Signal correlation. A Workflow must only advance when an outcome Signal matches the current waiting node attempt and linked Agent Task. Duplicate, stale, and mismatched outcomes should be recorded as low-noise audit events or ignored without advancing workflow state.

This slice should make callback races, repair-job replays, and old attempt completions safe.

## Acceptance criteria

- [ ] Agent Task outcome Signal payload includes plan identity, node identity, attempt, task identity, outcome version, and result reference or result payload.
- [ ] Workflow advancement requires payload correlation with the current waiting node attempt and linked Agent Task.
- [ ] Duplicate Signals do not advance the Workflow twice and are treated as idempotent no-ops or `signal.duplicate_ignored`.
- [ ] Stale attempt Signals do not advance the Workflow and project `signal.stale_ignored`.
- [ ] Wrong plan, node, or task Signals do not advance the Workflow and project `signal.mismatched_rejected`.
- [ ] Expanded event detail shows Signal Audit Events without making isolated ignored Signals primary panel errors.
- [ ] Tests cover matching, duplicate, stale, wrong task, wrong node, wrong plan, and repair-job replay scenarios.

## Blocked by

- 05-daemonbridge-dispatches-agent-task-and-waits-for-outcome-signal

