# ADR 0008: Complete issue hands off reviewable work

## Status

Accepted

## Context

Temporal Outcome Policy can decide that orchestration completed successfully, but that only proves the workflow produced reviewable evidence and a summary. Treating this as business acceptance would hide human ownership of final Issue closure.

## Decision

MVP `complete_issue` performs a review handoff. It writes the orchestration summary, trace, artifacts, and projected completion state, and it may move the Issue to an in-review status. It does not automatically mark the Issue done, close it, or treat successful orchestration as final human acceptance.

## Consequences

Workflow outcome `complete` means orchestration completed, not that the Issue is closed. Final done/close remains a human action unless a future explicit policy introduces automatic closure.
