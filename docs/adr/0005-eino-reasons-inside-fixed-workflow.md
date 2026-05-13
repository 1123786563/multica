# ADR 0005: Eino reasons inside the fixed MVP workflow

## Status

Accepted

## Context

Temporal is the orchestration lifecycle source of truth, and the MVP must first prove the Issue-to-Temporal-to-Agent-Task-to-projection loop. Letting Eino generate or mutate workflow topology in the same slice would add dynamic workflow definitions, versioning, projection mapping, recovery compatibility, and UI complexity.

## Decision

The MVP uses a fixed Temporal workflow: load issue, analyze issue, dispatch coding Agent Task, wait for Agent Task outcome signal, validate result, review result, summarize result, and complete issue. Eino implements reasoning activities for analysis, review, and summarization. Eino may output a recommended coding prompt, risks, suspected files, execution advice, review decisions, and summaries, but it may not create, remove, reorder, branch, or loop workflow nodes in the MVP.

## Consequences

Dynamic workflow definitions, JSON workflow DSLs, Eino graph execution, SequentialAgent, LoopAgent, and ParallelAgent remain future extensions. Any Eino recommendation that implies different topology must be recorded as a recommendation or approval reason, not applied automatically.
