# Deterministic result validation and evidence-insufficient retry

Label: ready-for-agent
Type: AFK
Risk: High

## Parent

.scratch/ai-native-orchestration-kernel/temporal-mvp-prd-github-issue.md

## What to build

Build Result Schema v1 validation and bounded automatic retry for recoverable evidence failures. `validate_result` should deterministically inspect the Agent Task structured outcome and evidence, without running shell commands, daemon commands, tests, or LLM judgment.

Malformed schema and insufficient evidence may trigger one bounded retry attempt; semantic failures such as failed tests or risks must not auto-retry in this slice.

## Acceptance criteria

- [x] Result Schema v1 parser validates schema version, summary, changed files, artifacts, tests, risks, and evidence references.
- [x] Unknown schema versions and malformed payloads become evidence insufficient rather than successful completion.
- [x] `validate_result` is deterministic and does not call daemon, shell, CLI, Eino, HTTP, or filesystem side effects.
- [x] Evidence insufficient or malformed schema can auto-retry only while retry budget remains.
- [x] MVP node retry allows at most two attempts total.
- [x] Prior evidence summary is preserved across retry attempts.
- [x] Tests cover valid schema, malformed schema, unknown version, missing evidence, retry scheduling, retry budget exhaustion, and no external execution from validation.

## Blocked by

- 05-daemonbridge-dispatches-agent-task-and-waits-for-outcome-signal
