# ADR 0002: Temporal owns orchestration lifecycle state

## Status

Accepted

## Context

Multica already had an Issue-scoped server-owned orchestration kernel backed by Postgres tables for plans, nodes, events, artifacts, evidence, and Agent Task links. The MVP direction now chooses Temporal as the durable workflow engine for orchestration lifecycle, retry, timeout, cancellation, replay, and recovery.

## Decision

Temporal Workflow Execution is the source of truth for Orchestration Run lifecycle state. Multica keeps Issue, Agent, Runtime, Skill, daemon, Agent Task, comments, notifications, permissions, and UI ownership, but its orchestration tables become a product projection of Temporal workflow progress rather than the authoritative workflow state machine.

Eino is introduced as a reasoning-node implementation for analysis, review, and summarization activities. It does not replace Temporal as workflow engine or the daemon-backed Agent Task lifecycle as the coding runtime.

## Consequences

ADR 0001's boundary still applies to the daemon and Agent Task execution boundary, but its DB-owned kernel-state assumption is superseded. Implementation must avoid dual state authority: when Temporal history and Multica projection disagree, Temporal wins and the projection must be repaired.
