# ADR 0019: Attention comments target Issue-relevant humans

Status: Accepted

## Context

Orchestration can produce states that need human attention, such as approval waits, retry exhaustion, run failure, repair failure, or fail-closed entry points when Temporal is unavailable. Those states should be visible from the Issue without broadcasting noisy updates to the whole workspace.

Successful orchestration already has the Decision Panel and review handoff summary. Treating every successful run as an attention event would create comment noise.

## Decision

The MVP creates an attention comment only for exceptional states that require human attention:

- `waiting_human`
- run failed
- retry exhausted
- repair or reconciliation failed
- orchestration entrypoint fail closed, such as Temporal unavailable

Successful runs do not create default attention comments. Review handoff summaries remain separate from attention comments.

Attention comments notify only Issue-relevant humans:

- Issue creator
- human assignee
- subscribers or watchers

Attention comments do not mention agent assignees and do not broadcast to the workspace.

## Consequences

Humans connected to the Issue see states that require action, while routine success and internal callback noise stay in the Decision Panel or expanded event detail. Tests should verify trigger conditions, no success-comment noise, audience selection, and exclusion of agent assignees.
