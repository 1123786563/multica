# Temporal unavailable fail-closed start path

Label: ready-for-agent
Type: AFK
Risk: High

## Parent

.scratch/ai-native-orchestration-kernel/temporal-mvp-prd-github-issue.md

## What to build

Build the first vertical slice for explicit Temporal orchestration startup failure. When orchestration is requested but Temporal is not configured, unreachable, or the orchestration worker is unavailable, Multica must return a clear unavailable result, project enough issue-visible state for users to understand the failure, and must not create a direct Agent Task fallback.

This slice should prove the new fail-closed boundary across config, API behavior, projection, issue detail read data, and focused tests.

## Acceptance criteria

- [ ] Orchestration startup checks explicit Temporal configuration before creating or dispatching executable work.
- [ ] When Temporal is unavailable, the start path returns an unavailable/fail-closed response instead of creating a direct Agent Task.
- [ ] The issue-scoped orchestration read model exposes enough status/reason data for Issue Detail to show unavailable/fail-closed state.
- [ ] The failure path may record a projection event and attention state, but does not enqueue daemon-backed execution.
- [ ] Tests cover missing config, unreachable Temporal client, unavailable worker behavior if detectable, and no direct Agent Task fallback.

## Blocked by

None - can start immediately

