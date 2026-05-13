# ADR 0011: Approval Gate uses minimal MVP actions

Status: Accepted

## Context

The Temporal-backed orchestration workflow needs a human intervention state for risk-bearing or uncertain outcomes. That state should be visible and actionable, but the MVP should not introduce a broad human prompt-editing flow before retry semantics, audit rules, and permissions are stable.

## Decision

The MVP Approval Gate supports only three actions: approve, retry, and cancel.

- `approve` accepts the current risk or uncertainty and lets the workflow continue to review handoff.
- `retry` creates the next Agent Task attempt using kernel-generated validation feedback and review concern context.
- `cancel` cancels the Temporal Workflow and propagates cancellation to the active Agent Task when one exists.

The MVP does not support `request changes`, arbitrary prompt patching, Issue description rewrites, manual node jumps, or workflow topology edits from the Approval Gate.

Approval Actions require an authorized human actor: workspace owner, workspace admin, Issue creator, or human Issue assignee. Agent assignees and the agent that initiated or executed the run cannot approve, retry, or cancel their own orchestration.

Every Approval Action writes an audit event containing `actor_id`, `actor_type=human`, `action`, `reason`, `plan_id`, and `node_id`.

## Consequences

The first implementation can keep approval API, permissions, Temporal signals, frontend controls, and audit records small and testable while preserving human accountability.

Human-written change requests remain a future extension. If introduced later, they must define explicit audit records, authorization, prompt assembly, retry behavior, and UI affordances without silently mutating the original Issue.
