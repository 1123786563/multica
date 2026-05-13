# ADR 0010: Orchestration fails closed without Temporal

## Status

Accepted

## Context

Temporal is the source of truth for orchestration lifecycle state. If an orchestration entry point falls back to the old direct Agent Task path when Temporal is unavailable, Multica would again have two completion semantics: one with workflow policy, evidence, projection, and review handoff, and one where an agent task completes directly.

## Decision

Orchestration entry points fail closed when Temporal is not configured, unreachable, or the orchestration worker is unavailable. They return an explicit unavailable or failed state and create visible projection or attention records where appropriate. They do not create direct Agent Tasks as a fallback for orchestration work.

## Consequences

Legacy or non-orchestration direct Agent Task flows may continue only where they are explicitly outside orchestration. Agent assignment, manual orchestration start, retry, and rerun paths must not silently bypass Temporal.
