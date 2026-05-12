# ADR-0001: Orchestration replaces legacy task path

**Status:** Accepted (2026-05-11)

## Context

Multica has two paths for agent-assigned issue execution:

1. **Legacy path**: `EnqueueTaskForIssue` → daemon claim → agent executes entire issue → `CompleteTask` → issue comment → done. The agent decides when the issue is complete.

2. **Orchestration path**: `OnIssueAssigned` → create Plan → create Nodes → dispatch tasks per node → agent executes one node → submit structured result → evaluator checks → complete/retry/wait for human. The Orchestrator decides when the issue is complete.

A workspace-level feature flag (`settings.orchestration_enabled`) originally selected between them during rollout.

## Decision

Orchestration will fully replace the legacy path. The feature flag is a rollout switch, not a permanent coexistence mechanism.

Once orchestration is stable for all workspace sizes and issue complexities, the legacy path code will be removed.

## Rationale

- **Single source of truth for completion.** The legacy path lets agents decide when an issue is done. The orchestration path forces evaluation before completion. Maintaining both means maintaining two different completion semantics.

- **Even single-node plans provide value.** A one-node `implement` plan with HardCheck evaluation is strictly better than the legacy path — it adds an evaluation gate without adding complexity to the agent's prompt.

- **Dual maintenance cost is high.** Two task creation paths, two prompt templates, two completion handlers, two WS event patterns. Every future feature (LLM Judge, multi-agent, policy engine) needs to work with both or one becomes neglected.

- **Migration is low-risk.** In-flight legacy tasks complete naturally (ADR-0001, decision 17a). New issues created after the switch go through orchestration. No data migration needed.

## Consequences

- All agent-assigned issues will go through the Plan/Node/Evaluation pipeline, even trivial ones.
- The daemon must support the structured result submission protocol (`multica task complete --result`).
- Legacy `EnqueueTaskForIssue`, issue-oriented `BuildPrompt`, and direct `CompleteTask` without evaluation will be deprecated and eventually removed.
- New issues, new assignments, comment-triggered runs, and reruns now always create orchestration plans; the flag remains only as historical rollout residue until fully removed.
