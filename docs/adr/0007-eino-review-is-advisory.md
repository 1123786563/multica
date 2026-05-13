# ADR 0007: Eino review is advisory

## Status

Accepted

## Context

The MVP includes `review_result` after deterministic validation. If Eino review can directly mark a run successful, orchestration returns to the old failure mode where an agent or LLM decides completion from prose instead of policy and evidence.

## Decision

Eino `review_result` is advisory. It may summarize evidence, explain risks, identify concerns, and recommend a next action, but it cannot directly set the Orchestration Run outcome. Temporal Outcome Policy combines deterministic validation outcome, Eino advisory review, risks, failed tests, missing evidence, approval state, and retry budget to decide complete, approval, retry, or failure.

## Consequences

`ReviewResultOutput` must avoid authoritative fields such as `is_success` or final `next_action`. Non-empty risks, failed tests, missing evidence, or malformed results cannot be overridden by a positive Eino review.
