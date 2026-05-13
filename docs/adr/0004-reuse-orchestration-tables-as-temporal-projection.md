# ADR 0004: Reuse orchestration tables as Temporal projection

## Status

Accepted

## Context

The existing Multica codebase already has orchestration projection tables, Agent Task foreign keys, issue-scoped read APIs, and an Issue Detail Decision Panel built around `orchestration_plan`, `orchestration_node`, `orchestration_event`, and `orchestration_artifact`. The Temporal-first MVP draft proposed adding parallel `workflow_executions`, `workflow_node_executions`, `workflow_events`, and `workflow_artifacts` tables.

## Decision

Multica will reuse and extend the existing `orchestration_*` tables as the product projection for Temporal workflow progress. These rows store Temporal workflow identifiers, projected node state, events, artifacts, evidence, Agent Task links, projection version, and sync metadata. They do not become the lifecycle source of truth; Temporal history does.

## Consequences

Implementation must migrate semantics rather than fork the read model. Existing API, UI, tests, and Agent Task links can evolve in place, while new generic `workflow_*` CRUD surfaces are avoided.
