# Orchestration — Domain Context

## Glossary

| Term | Definition |
|------|-----------|
| **Orchestrator** | The server-side Go service (`service.Orchestrator`) that owns plan lifecycle, node dispatch, evaluation, and retry decisions. The product-level name is "AI-native Orchestration Kernel" but the code and API consistently use **Orchestrator**. |
| **Plan** | A directed execution graph created for a work goal. Sources: issue, chat session, autopilot run, API trigger, quick-create. Status machine: `planning → ready → running → completed \| failed \| cancelled \| waiting_human`. `planning` and `ready` are reserved for LLM Planner (Phase 4); rule-based planner skips directly to `running`. |
| **Node** | A single schedulable work step within a Plan. 10 types defined: clarify, inspect, design, implement, test, review, fix, deploy, approval, summarize. Phase 1 planner uses: inspect, implement, test. |
| **Edge** | A dependency between two nodes. Only type `blocks` (upstream must complete before downstream dispatches). Additional types may be added when differentiated scheduling behavior is needed. |
| **Task** | An execution attempt of a node by an agent. One node → many tasks (retry, rework). Reuses `agent_task_queue` with orchestration foreign keys. |
| **Run** | A unique identifier (`orchestration_run_id`, UUIDv7) generated per task dispatch. Purpose: trace correlation — group all events and artifacts from a single dispatch attempt. |
| **Artifact** | A structured evidence record produced during task execution. Types: diff, file, log, test_result, pr, decision, review_result, command_output, summary. |
| **Artifact Evidence** | Structured artifacts required by node type (e.g., implement needs diff, test needs test_result). Validated by HardCheck per node type rules. |
| **Criteria Evidence** | Agent-submitted text mappings of acceptance criteria to proof (`criterion → evidence`). Required when the issue has acceptance criteria; optional otherwise. |
| **Evaluation** | The process by which the Orchestrator decides whether a task result satisfies the node's requirements. Multi-layer: Hard Check (Phase 1) → LLM Judge (Phase 4). Status `evaluating` means "waiting for evaluator result"; currently instantaneous with HardCheck, will have duration with LLM Judge. |
| **Hard Check** | Deterministic, binary pass/fail evaluation. Checks: summary present, no failed tests, criteria evidence complete, node-type-specific artifacts. Does NOT use agent-reported confidence. |
| **Acceptance Criteria** | Verifiable conditions for work completion, sourced from issue's `acceptance_criteria`. |
| **Output Contract** | JSON spec on each node declaring required outputs. Phase 1: descriptive hint passed to agent prompt. Phase 2+: declarative constraint enforced by evaluator. |
| **Policy** | Configuration cascade: node-level → plan-level → workspace-level (most specific wins). All fields currently write-only; consumption planned for Phase 6. |
| **Legacy Path** | Pre-orchestration task flow. Being replaced — see [ADR-0001](../adr/0001-orchestration-replaces-legacy-task-path.md). |

## Key Relationships

```
Issue 1──* Plan 1──* Node 1──* Task (attempt)
                         │
                    Edge (blocks only)
Node 1──* Artifact    Task 1──* Artifact
Plan 1──* Event       Node 1──* Event
```

## Event Actor Types

| Actor | When | actor_id |
|-------|------|----------|
| `kernel` | Orchestrator autonomous decisions (evaluate, auto-retry, dispatch, fail) | empty |
| `agent` | Agent actions (task completed, task failed) | agent UUID |
| `member` | Human actions (approve node, cancel plan, retry node) | member UUID |
| `system` | Automated triggers (future: cron, webhook) | TBD |

## Node Status Usage

| Status | Setter | When |
|--------|--------|------|
| `pending` | `CreateOrchestrationNode` | Created, waiting on upstream edge |
| `ready` | `ReadyOrchestrationNode` | Retry or manual retry |
| `dispatched` | `MarkOrchestrationNodeDispatched` | Task created for node |
| `running` | `MarkOrchestrationNodeRunning` | Agent started task |
| `evaluating` | `MarkOrchestrationNodeEvaluating` | Task completed, awaiting evaluator |
| `completed` | `CompleteOrchestrationNode` | Eval passed or manual approve |
| `failed` | `FailOrchestrationNode` | Retries exhausted |
| `waiting_human` | `WaitOrchestrationNodeForHuman` | Eval says ask_human or agent unavailable |
| `blocked` | *(reserved)* External condition unsatisfied, needs human intervention |
| `skipped` | *(reserved)* Plan cancelled while node was pending |
| `cancelled` | *(reserved)* Node was started then cancelled |

## ADRs

- [ADR-0001: Orchestration replaces legacy task path](../adr/0001-orchestration-replaces-legacy-task-path.md)
