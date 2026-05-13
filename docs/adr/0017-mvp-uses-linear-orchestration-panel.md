# ADR 0017: MVP uses a linear orchestration panel

Status: Accepted

## Context

The MVP workflow is a fixed linear chain: load issue, analyze issue, dispatch coding Agent Task, wait for outcome, validate result, review result, summarize result, and review handoff. Rendering a DAG or workflow designer before branch, parallel, or loop semantics exist would add frontend complexity without improving kernel correctness.

Users still need process visibility: node status, reason, attempts, evidence, Agent Task links, artifacts, and audit events should be visible from Issue Detail.

## Decision

The MVP Issue Detail orchestration UI is a linear Orchestration Run Panel.

It shows a fixed-order node list with:

- node key or display name;
- status;
- reason code;
- recommended action;
- attempt count;
- latest summary;
- evidence count;
- linked Agent Task and runtime information.

Nodes expose expandable Kernel Events, Agent Task transcript summaries, structured evidence, Signal Audit Events, and artifacts.

The MVP does not include a DAG graph, graph canvas, workflow designer, or standalone orchestration page. DAG visualization is deferred until workflow topology introduces condition, parallel, or loop behavior.

## Consequences

The first UI slice can focus on correctness evidence and operator comprehension rather than topology rendering. Frontend tests should verify the list view, expandable event/evidence detail, approval actions, and Signal Audit Event visibility.
