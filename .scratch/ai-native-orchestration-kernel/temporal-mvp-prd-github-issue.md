## Problem Statement

Multica already has Issue, Agent, Runtime, Skill, daemon, and Agent Task primitives, but the current execution model behaves like an AI coding task manager rather than an AI-native software delivery orchestration system. An agent-assigned Issue can be executed by a daemon-backed Agent Task, but Multica does not yet have a durable workflow owner for multi-step orchestration, retry policy, evidence validation, approval gates, recovery, and process observability.

From the user's perspective, this creates several gaps:

- It is hard to see the full orchestration process behind an Issue beyond top-level task state.
- Agent Task completion can be treated as success before structured evidence is validated.
- Retry, approval, cancellation, and recovery behavior are not governed by a single durable workflow source of truth.
- AI reasoning, runtime execution, product projection, and human approval can blur together without clear ownership.
- Temporal infrastructure should improve lifecycle correctness, but it must not create a second hidden task path or silently fall back to direct Agent Task execution.

## Solution

Build a Temporal-backed AI-native Orchestration Kernel MVP on top of the current Multica foundation.

The MVP introduces an Issue-scoped fixed workflow where Temporal owns orchestration lifecycle state, Eino performs reasoning activities, daemon-backed Agent Tasks execute code work, and Multica projects workflow progress into existing orchestration records for Issue Detail observability.

The first end-to-end loop is:

Issue -> Temporal Workflow -> Eino reasoning -> Agent Task execution -> Agent Task outcome Signal -> deterministic validation -> advisory review -> review handoff / approval / retry / failure -> Multica projection + Issue Detail observability.

The MVP deliberately optimizes for correctness and evidence over breadth. It does not add a workflow designer, DAG canvas, dynamic Eino topology, generic orchestration CRUD, or automatic Issue closure.

## User Stories

1. As a workspace member, I want an Issue to start an orchestration workflow, so that AI work is coordinated by a durable lifecycle owner.
2. As a workspace member, I want orchestration to fail closed when Temporal is unavailable, so that Multica never silently falls back to a weaker completion path.
3. As an Issue creator, I want to see whether an orchestration run is queued, running, waiting for human input, completed, failed, or cancelled, so that I understand the current delivery state.
4. As an Issue creator, I want only one active orchestration run per Issue, so that duplicate triggers do not create conflicting AI work.
5. As an Issue creator, I want later reruns to preserve historical runs, so that previous execution evidence remains inspectable.
6. As an operator, I want each run to have a distinct workflow identity, so that Temporal history and rerun behavior are clear.
7. As an operator, I want repeated start requests to return the existing active run, so that retries and UI double-clicks do not start duplicate workflows.
8. As an operator, I want Temporal history to be the source of truth when projection rows lag, so that recovery has one authoritative state source.
9. As a developer, I want existing orchestration projection records to be reused, so that API, UI, Agent Task links, and audit views evolve in place.
10. As a developer, I want all projection writes to happen through Activities, so that Workflow replay does not duplicate visible side effects.
11. As a developer, I want Workflow code to remain deterministic, so that replay, recovery, and tests are reliable.
12. As a developer, I want Eino to reason inside a fixed workflow, so that intelligent analysis is added without dynamic workflow topology risk.
13. As a developer, I want Eino to generate execution advice and a recommended coding prompt, so that daemon-backed coding agents receive better task context.
14. As a developer, I want Eino review to be advisory, so that LLM judgment cannot override deterministic evidence policy.
15. As a developer, I want Eino summary to produce review handoff context, so that humans can inspect what happened without treating orchestration as final acceptance.
16. As an agent runtime operator, I want daemon-backed Agent Tasks to remain the execution unit, so that existing runtime, workspace, shell, logging, and cleanup behavior are preserved.
17. As an operator, I want dispatch to create or reuse exactly one Agent Task per node attempt, so that retries and recovery do not duplicate code edits.
18. As an operator, I want long-running Agent Tasks to signal Temporal when they finish, so that Activities do not poll the database while waiting.
19. As an operator, I want Agent Task outcome signals to include plan, node, attempt, task, and outcome version data, so that stale or mismatched callbacks cannot advance the wrong workflow node.
20. As an operator, I want duplicate, stale, and mismatched signals to be recorded as low-noise audit events, so that callback races can be debugged without confusing normal users.
21. As a workspace member, I want malformed structured results to be treated as insufficient evidence, so that completed Agent Tasks are not automatically considered verified.
22. As a workspace member, I want validation to check schema and evidence deterministically, so that success does not depend on free-form prose.
23. As a workspace member, I want validation not to run a second daemon test step, so that result ownership stays clear in the MVP.
24. As a workspace member, I want failed tests or non-empty risks to pause for human judgment, so that risky work is not retried or accepted silently.
25. As an operator, I want automatic node retry to be bounded, so that malformed output can recover without creating infinite loops.
26. As an operator, I want automatic retry to cover only non-semantic recoverable failures, so that code-changing agent work is not repeated blindly.
27. As a human assignee, I want an Approval Gate for risky or uncertain outcomes, so that I can decide whether to approve, retry, or cancel.
28. As a human assignee, I want Approval Gate actions to be minimal in the MVP, so that approve, retry, and cancel have clear semantics.
29. As a workspace admin, I want agents to be unable to approve, retry, or cancel their own orchestration runs, so that human accountability is preserved.
30. As a workspace admin, I want every approval action audited with actor and reason metadata, so that risk acceptance and cancellation decisions are traceable.
31. As an Issue creator, I want successful orchestration to hand off reviewable work rather than close the Issue, so that final business acceptance remains human-owned.
32. As an Issue creator, I want attention comments only when human attention is needed, so that successful runs do not create comment noise.
33. As a human assignee, I want attention comments to mention Issue-relevant humans only, so that workspace-wide notification noise is avoided.
34. As an agent assignee, I do not want to be mentioned in attention comments, so that agent loops and irrelevant notifications are avoided.
35. As a workspace member, I want Issue Detail to show a linear orchestration panel, so that the fixed MVP workflow is easy to scan.
36. As a workspace member, I want each node row to show status, reason, attempts, summary, evidence count, and linked Agent Task information, so that I can understand process state without reading raw events first.
37. As a workspace member, I want to expand nodes for events, evidence, artifacts, transcript summaries, and signal audit events, so that detailed debugging data is available when needed.
38. As a frontend developer, I want orchestration server state to be owned by React Query, so that views stay aligned with Multica's server-state boundary.
39. As a frontend developer, I want Zustand used only for local UI state such as expanded panels, so that orchestration projection is not duplicated client-side.
40. As a product owner, I want DAG visualization deferred until branch, parallel, or loop workflows exist, so that MVP effort stays focused on correctness.
41. As a developer, I want an explicit orchestration worker process, so that Temporal workflow execution is not embedded in the API process.
42. As a developer, I want default local development to work without Temporal unless explicitly enabled, so that ordinary Multica development is not disrupted.
43. As a QA engineer, I want focused workflow and Activity tests before broad E2E tests, so that correctness failures are localized.
44. As a QA engineer, I want at least one happy path and one failure/retry/approval path validated end-to-end, so that the Temporal-to-daemon-to-UI loop is proven.

## Implementation Decisions

- Build a Temporal-backed Orchestration Kernel where Temporal Workflow Execution is the lifecycle source of truth.
- Keep Multica Issue, Agent, Runtime, Skill, daemon, Agent Task, permissions, comments, notifications, and UI ownership in Multica.
- Reuse existing orchestration projection concepts rather than adding parallel generic workflow projection tables.
- Introduce an explicit Temporal profile and a separate orchestration worker. Default development startup does not require Temporal.
- Orchestration entry points fail closed when Temporal is unavailable and do not create direct Agent Tasks as fallback.
- Start behavior is Active Run idempotent. An Issue can have at most one active orchestration run; repeated start requests return the existing active run.
- Each orchestration run has a distinct Temporal Workflow ID derived from workspace, Issue, and plan identity.
- Implement a fixed MVP workflow chain: load issue, analyze issue, dispatch coding Agent Task, wait for Agent Task outcome, validate result, review result, summarize result, and review handoff.
- Eino is a reasoning activity provider. It may analyze, review, summarize, and produce recommended prompts, but it may not create or mutate workflow topology in the MVP.
- Daemon-backed Agent Task remains the coding runtime. Runtime execution, workspace preparation, shell commands, logs, transcript collection, and cleanup remain outside Eino.
- Temporal Workflow code is deterministic. Projection writes, comments, notifications, WebSocket refreshes, Eino calls, daemon bridge calls, Agent Task creation, and Agent Task cancellation happen only through Activities.
- Add a projection Activity boundary with idempotent Activity keys based on run, node, attempt, event type, workflow identity, and task identity.
- Dispatching a daemon task is a short Activity. It creates or links an Agent Task and returns without waiting for completion.
- Agent Task completion, failure, and cancellation are sent back to the owning Workflow through Signals or Updates.
- Agent Task outcome Signals must include plan identity, node identity, attempt number, task identity, outcome version, and result reference or structured result payload.
- Workflow advancement requires the outcome Signal to match the current waiting node attempt and linked task.
- Duplicate, stale, and mismatched Signals do not advance the Workflow and are projected as low-noise audit events.
- Result Schema v1 is required for orchestration evidence. Unknown versions and malformed payloads become insufficient evidence rather than success.
- Validation is deterministic schema and evidence validation. It does not run shell commands, daemon commands, tests, or LLM judgment.
- Eino review is advisory. It cannot mark the run successful or override failed tests, risks, missing evidence, malformed evidence, or approval rules.
- Temporal Outcome Policy combines deterministic validation, Eino advisory review, risks, failed tests, missing evidence, approval state, and retry budget.
- Node retry is limited to at most two attempts total in the MVP.
- Automatic node retry is allowed only for recoverable non-semantic failures such as malformed schema, insufficient evidence, timeout, transient worker error, or signal delivery repair.
- Failed tests, non-empty risks, high-risk review concerns, unverifiable evidence, and destructive operations enter Approval Gate instead of auto-retrying.
- Approval Gate supports only approve, retry, and cancel.
- Approval actions require authorized human actors: workspace owner, workspace admin, Issue creator, or human Issue assignee.
- Agent assignees and agents that initiated or executed the run cannot approve, retry, or cancel their own orchestration.
- Every approval action writes an audit event with actor identity, actor type, action, reason, plan identity, and node identity.
- Complete issue means review handoff. It can write summary, trace, artifact projection, and move the Issue to review-like status, but it does not mark the Issue done.
- Attention comments are created only for exceptional states that require human attention: waiting human, run failed, retry exhausted, repair failed, or Temporal unavailable.
- Successful runs do not create default attention comments. Review handoff summaries are separate from attention comments.
- Attention comments notify only Issue-relevant humans: creator, human assignee, subscribers, and watchers. They do not mention agent assignees or broadcast to the workspace.
- The first UI surface is a Linear Orchestration Panel inside Issue Detail, not a DAG graph, workflow designer, or standalone orchestration page.
- The panel shows node status, reason, recommended action, latest summary, attempts, evidence count, linked Agent Task, and expandable events/evidence/artifacts.
- Signal audit events appear in expanded detail by default and do not become primary panel errors unless current waiting state or repair failure requires attention.
- Orchestration server state is managed through query invalidation after coarse refresh events. Client local state is limited to presentation state such as expanded nodes.

Major modules to build or modify:

- Temporal client, worker, workflow, and activity layer.
- Orchestration start service and Active Run idempotency layer.
- Projection writer and repair layer.
- Eino reasoning adapter.
- DaemonBridge adapter and Agent Task outcome signaler.
- Result schema parser and deterministic validation module.
- Temporal outcome policy and retry policy module.
- Approval permission and audit module.
- Attention comment and audience selection module.
- Issue-scoped orchestration API contract.
- Linear Orchestration Panel shared view and frontend data hooks.

Deep modules that should have stable testable interfaces:

- Workflow definition and deterministic policy module.
- Projection Activity writer.
- Signal correlation validator.
- Result Schema v1 validator.
- Outcome policy evaluator.
- Approval permission evaluator.
- Attention audience selector.
- Linear panel view model builder.

## Testing Decisions

Good tests should verify externally observable behavior and contracts rather than internal implementation details. Workflow tests should assert lifecycle transitions and scheduled Activities. Activity tests should assert idempotent side effects. API tests should assert contract, authorization, and projection behavior. Frontend tests should assert rendered user-facing state from API-shaped data.

Backend and workflow modules to test:

- Temporal unavailable fail-closed behavior.
- Active Run idempotent start.
- Concurrent start race behavior.
- Workflow ID generation per run.
- Workflow replay determinism.
- Projection Activity idempotency.
- Projection repair when Temporal history wins.
- Agent Task dispatch idempotency.
- Signal matching advancing Workflow.
- Duplicate Signal no-op behavior.
- Stale attempt rejection.
- Wrong task, wrong node, and wrong plan rejection.
- Low-noise Signal Audit Event projection.
- Result Schema v1 valid, malformed, unknown version, and missing evidence cases.
- Deterministic validation behavior.
- Outcome policy for complete, retry, approval, failed, and cancelled outcomes.
- Bounded node retry with maximum two attempts.
- Failed tests and risks routing to Approval Gate.
- Eino advisory review unable to override deterministic policy.
- Approval permission for allowed human actors.
- Denied approval for agent actors and unrelated members.
- Approval audit event payloads.
- Cancellation propagation to active Agent Task.
- Attention comment trigger conditions.
- Attention audience selection and agent assignee exclusion.

API and frontend modules to test:

- Issue-scoped orchestration read permission follows Issue visibility.
- Approval mutation permission is stricter than read permission.
- Unknown enum and malformed projection fallback behavior.
- Coarse orchestration refresh invalidates query data.
- Linear Orchestration Panel renders node summary.
- Reason and recommended action render from server-projected data.
- Expanded detail renders events, evidence, artifacts, transcript summaries, and Signal Audit Events.
- Approval buttons render only when server-projected permission and action allow them.
- Partial projection data does not white-screen.
- Server state remains query-owned; local UI state remains local.

End-to-end coverage:

- Happy path: start orchestration, analyze, dispatch Agent Task, signal completed, validate, review, summarize, review handoff.
- Fail-closed path: Temporal unavailable returns unavailable and creates no direct Agent Task.
- Failure path: malformed or insufficient evidence triggers retry or approval.

Prior art in the codebase includes existing Go service/API tests, existing Agent Task lifecycle tests, existing frontend view tests, and the prior orchestration kernel issue slices that validated plans, nodes, events, artifacts, approval gates, visibility, and attention comments.

## Out of Scope

- Dynamic workflow topology.
- DAG graph visualization.
- Workflow designer.
- Generic run/node/event/evidence CRUD APIs.
- Parallel, branch, and loop workflow execution.
- Eino graph execution.
- Verifier Agent as a separate runtime node.
- Workspace-wide automatic skill planning.
- Workspace-level orchestration policy tables.
- Structured risk taxonomy beyond MVP result schema.
- Automatic Issue done/close.
- Request changes prompt editing from Approval Gate.
- Success comment configuration.
- Chat, autopilot, and quick-create orchestration entry points.
- Enterprise RBAC, billing, marketplace, and governance features.

## Further Notes

This PRD reflects the accepted Temporal MVP boundary from the current orchestration design work. The implementation should proceed from contract baseline, then Temporal skeleton and fail-closed behavior, then projection and idempotent start, then daemon signal bridge, validation/retry/policy, Eino reasoning, approval/attention, UI, and final checkout validation.

The MVP cut line is strict: the feature is not complete if lifecycle state is DB-owned instead of Temporal-owned, orchestration silently falls back to direct Agent Task, Workflow code performs projection side effects directly, failed tests or risks auto-retry code work, agents can approve their own work, or the UI only shows top-line state without node/evidence process detail.
