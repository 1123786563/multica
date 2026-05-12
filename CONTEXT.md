# Multica Orchestration

This context captures the domain language for Multica's AI-native orchestration layer. It describes how orchestration relates to existing issues, agents, runtimes, skills, and task execution.

## Language

**Orchestration Kernel**:
A server-owned planning and decision layer above issues that coordinates work through nodes, events, evidence, and task dispatch.
_Avoid_: replacement task queue, runtime, agent CLI wrapper

**Agent Task**:
The existing executable unit claimed by a runtime and completed by an agent through the daemon.
_Avoid_: orchestration node, kernel task

**Kernel Execution Boundary**:
The Orchestration Kernel decides and records what should happen next, then dispatches existing Agent Tasks for execution. It does not run agent CLIs, replace the daemon, or replace the task queue.
_Avoid_: kernel runtime, direct CLI executor

**Orchestration Run**:
An issue-scoped instance of kernel coordination. In the first implementation, an Issue is the primary entry point for orchestration; chat, autopilot, and quick-create tasks remain outside the kernel until explicitly expanded.
_Avoid_: chat run, autopilot run, global workflow

**Kernel Compatibility Mode**:
A workspace-scoped rollout mode where agent-assigned issues enter the Orchestration Kernel before dispatching Agent Tasks, while workspaces without orchestration enabled keep the existing direct issue-to-task enqueue path.
_Avoid_: global replacement, dual execution path

**Kernel Schedule**:
The first orchestration scheduling model is a minimal graph that runs as a linear main chain with optional verification nodes. Nodes may record dependencies, but the first runtime behavior advances only when the prior required node has completed.
_Avoid_: full DAG scheduler, multi-agent parallel planner

**Kernel Event**:
A persisted orchestration fact used for audit, recovery, and process visibility. Kernel Events are distinct from WebSocket notifications: persisted events are the source of truth, while WebSocket messages only notify clients to refresh or stream live updates.
_Avoid_: transient UI event, direct Zustand write

**Runtime Adapter**:
The bridge that maps an executable orchestration node to an existing Agent Task and later maps the Agent Task outcome back to the node. It does not introduce a new daemon protocol or run agent CLIs directly.
_Avoid_: kernel daemon protocol, direct runtime executor

**Skill Discovery Boundary**:
The first orchestration implementation uses skills already bound to the selected Agent. The kernel may record the skill context available to a node, but it does not perform workspace-wide skill search or dynamic skill selection.
_Avoid_: global skill planner, automatic runtime skill selection

**Approval Gate**:
A conditional human decision point used when orchestration cannot safely continue automatically, such as high-risk work, insufficient evidence, failed verification, or an explicit issue or node policy. It is not inserted into every run by default.
_Avoid_: mandatory human checkpoint, manual-only orchestration

**Node Recovery**:
The first failure recovery model retries or resumes individual orchestration nodes instead of rerunning the whole Orchestration Run. Completed nodes, Kernel Events, and evidence are preserved; the whole run fails only when its structure is invalid or recovery cannot determine a safe next node state.
_Avoid_: restart whole run, erase evidence

**Decision Panel**:
The primary observability surface for an Orchestration Run. It summarizes each node's status, reason, recommended action, latest summary, attempts, and evidence count before exposing raw Kernel Events as detail.
_Avoid_: event-dump-first UI, opaque top-line status

**Kernel Test Strategy**:
The first testing strategy prioritizes Go service and state-machine tests for orchestration correctness and recovery, then adds focused API, frontend contract, and minimal end-to-end coverage.
_Avoid_: E2E-first validation, UI-only confidence

**Node State**:
The orchestration-specific status of a node, independent from the linked Agent Task status. Node State expresses kernel concepts such as pending, ready, dispatched, running, waiting for approval, succeeded, failed, skipped, and cancelled.
_Avoid_: task status alias, daemon status

**Node Type**:
The role of a node in the initial orchestration schedule. The first implementation uses only plan, execute, and verify node types. Approval and retry are modeled as node states or events, not as separate node types.
_Avoid_: approval node, retry node, arbitrary plugin node

**Default Plan Node**:
The first plan node is generated deterministically by the server from issue and workspace context. It records the default schedule and does not call an Agent or LLM in the first implementation.
_Avoid_: LLM planner, agent-generated schedule

**Hard Check Verification**:
The first verify node uses deterministic server-owned checks against Agent Task outcome and recorded evidence. It does not dispatch a verifier Agent by default.
_Avoid_: verifier agent by default, trust final prose

**Evidence Contract**:
The minimum structured outcome an Agent Task must provide for kernel verification: summary, changed files, artifacts, tests, and risks. The kernel may parse this from Agent Task result JSON and attach it to node evidence.
_Avoid_: prose-only completion, unverifiable output

**Node Evidence**:
Structured evidence persisted for an orchestration node, separate from the raw Agent Task result. Node Evidence is queried by verification, recovery, and observability surfaces and remains available across retries.
_Avoid_: task-result-only evidence, reparse-only verification

**Active Run**:
The single non-terminal Orchestration Run for an Issue. Repeated triggers for the same Issue must return or reuse the existing Active Run instead of creating a parallel run.
_Avoid_: parallel issue runs, duplicate orchestration

**Run State**:
The lifecycle status of an Orchestration Run, independent from the Issue workflow status. Run State expresses orchestration lifecycle such as running, waiting for approval, succeeded, failed, and cancelled.
_Avoid_: issue status alias, board status

**Issue Status Coordination**:
The limited relationship between orchestration lifecycle and the Issue workflow. The kernel may perform deterministic status nudges, such as moving a ready agent-assigned Issue into progress, but it does not automatically mark business work done.
_Avoid_: full board automation, automatic done

**Run Cancellation**:
The cancellation behavior for orchestration. Cancelling an Orchestration Run cancels active nodes and their active linked Agent Tasks; cancelling an Issue cancels its Active Run. Completed tasks, Kernel Events, and Node Evidence remain as history.
_Avoid_: delete run history, leave linked task running

**Kernel Transaction Boundary**:
The consistency rule that every orchestration state change and its corresponding Kernel Event are committed in the same database transaction. WebSocket notifications happen after commit and are not the source of truth.
_Avoid_: event-only state, post-commit audit repair

**Run Advancement**:
The first scheduling trigger model for orchestration. Run advancement is invoked synchronously from creation, Agent Task completion or failure, approval actions, and manual retry, then proceeds until the next blocking state. A lightweight recovery scan may repair interrupted advancement.
_Avoid_: long-running scheduler worker, polling-first orchestration

**Run Lock**:
The per-run database lock used while advancing an Orchestration Run. Every Run Advancement path must hold this lock before changing run or node state, dispatching Agent Tasks, or writing Kernel Events.
_Avoid_: global scheduler lock, unlocked advancement

**Node Dispatch Idempotency**:
The rule that one orchestration node attempt can bind to at most one Agent Task. Repeated dispatch attempts or recovery scans must reuse the existing linked task for that node attempt instead of creating another execution.
_Avoid_: duplicate node task, parallel same-attempt execution

**Result Schema**:
The versioned structured result shape expected from Agent Tasks used by orchestration. The first version carries schema_version, summary, changed files, artifacts, tests, and risks; unknown versions are treated as insufficient evidence rather than successful completion.
_Avoid_: unversioned result, implicit prose parsing

**Evidence Insufficient**:
The verification outcome used when an Agent Task completes but does not provide a valid Result Schema or enough Node Evidence. The Agent Task may remain completed, but the orchestration node does not automatically succeed.
_Avoid_: completed equals verified, malformed success

**Node Retry Policy**:
The retry policy applied to an orchestration node. The first implementation defaults to at most two node attempts and retries only recoverable conditions such as runtime recovery, timeout, and insufficient evidence.
_Avoid_: infinite retry, whole-run retry by default

**Approval Action**:
The small set of human decisions available when an Orchestration Run is waiting for approval: approve, retry, request changes, or cancel. Approval actions are audited and do not allow arbitrary node jumps in the first implementation.
_Avoid_: arbitrary state edit, jump to node

**Change Request**:
A human instruction captured from request changes during an Approval Gate. The kernel records it as a Kernel Event and passes it into the next node attempt as execution context; it does not automatically rewrite the Issue description.
_Avoid_: hidden issue rewrite, unaudited prompt patch

**Orchestration Context**:
The small kernel-provided execution context appended to an Agent Task, such as run id, node id, node type, attempt, expected result schema, prior evidence summary, and change request. It complements the existing daemon prompt and CLI workflow instead of replacing them.
_Avoid_: replacement daemon prompt, bespoke agent workflow

**Kernel Persistence Model**:
The first persistence model for orchestration: orchestration run, orchestration node, orchestration event, and orchestration evidence. Existing workspace settings and Agent Task queue remain the surrounding integration points.
_Avoid_: policy table explosion, planner template registry

**Kernel API Surface**:
The minimal external API for orchestration: issue-scoped read access to run progress and a small approval-action endpoint. Run, node, event, and evidence records are not exposed as generic CRUD resources in the first implementation.
_Avoid_: public node editor, generic orchestration CRUD

**Issue Detail Observability**:
The first user-facing orchestration surface lives inside the existing Issue Detail experience. It shows the Decision Panel and expandable event or evidence detail without introducing a standalone orchestration page.
_Avoid_: separate orchestration app, route-first observability

**Orchestration Client State Boundary**:
The frontend ownership rule for orchestration data. Run, node, event, and evidence data are server state managed by React Query; Zustand may only hold local UI state such as expanded nodes or selected panels.
_Avoid_: persisted orchestration store, duplicate server state

**Orchestration Refresh Event**:
The coarse WebSocket notification that orchestration data changed for an Issue. The first implementation uses one refresh event rather than streaming every Kernel Event over WebSocket.
_Avoid_: mirrored event stream, WS source of truth

**Approval Permission**:
The human authorization rule for Approval Actions. In the first implementation, workspace owners, workspace admins, the Issue creator, and a human Issue assignee may approve or direct a paused run; agent assignees cannot approve their own orchestration.
_Avoid_: self-approving agent, unrestricted approval

**Orchestration Read Permission**:
The authorization rule for viewing orchestration data. Reading an Orchestration Run follows the same access boundary as reading its Issue; approval and mutation actions remain separately restricted.
_Avoid_: separate read ACL, evidence side channel

**Kernel Event Type**:
The stable event-type vocabulary for persisted Kernel Events. The first implementation uses a constrained enum for run, node, task-link, evidence, retry, and approval facts instead of arbitrary event strings.
_Avoid_: free-form audit event, ungoverned event name

**Node Reason Code**:
The server-derived explanation for why a node is in its current state. The frontend displays and localizes the code but does not infer orchestration reasons from raw events.
_Avoid_: frontend state inference, event-order guessing

**Recommended Action**:
The server-derived next action suggested for a node or run, based on state, reason, permissions, and retry budget. The frontend renders the recommendation but does not choose orchestration actions independently.
_Avoid_: client-side action inference, UI-owned state machine

**Evidence Retry Boundary**:
The rule for automatic retry after insufficient evidence. Missing or malformed structured result fields may auto-retry within the Node Retry Policy, while risk-bearing or untrustworthy evidence enters an Approval Gate instead.
_Avoid_: retry risky work automatically, block all malformed output on humans

**Risk Approval Boundary**:
The rule that risk-bearing evidence requires human approval in the first orchestration implementation. Non-empty risks, failed tests, unverifiable results, or destructive operations pause the run instead of auto-succeeding.
_Avoid_: agent self-approves risk, silent risk acceptance

**Verification Completion**:
The outcome when hard-check verification succeeds. The Orchestration Run succeeds and the Issue may move to in review, but the kernel does not automatically mark the Issue done.
_Avoid_: verified equals done, auto-close issue

**Attention Comment**:
An orchestration-generated Issue comment used only when human attention is required, such as waiting for approval, retry exhaustion, or run failure. Successful orchestration does not create a comment by default.
_Avoid_: success-comment noise, hidden failure

**Attention Audience**:
The human audience for orchestration states that need attention. The first implementation notifies Issue-relevant people such as the creator, human assignee, and subscribers rather than broadcasting to the whole workspace.
_Avoid_: workspace-wide alert, agent mention loop

## Relationships

- An **Orchestration Kernel** coordinates one or more **Agent Tasks**
- An **Agent Task** remains the execution unit for an agent runtime
- An **Orchestration Kernel** does not replace the existing issue, agent, runtime, skill, or task queue models
- The **Kernel Execution Boundary** keeps orchestration decisions in the server while runtime execution stays in the daemon-backed Agent Task lifecycle
- An **Orchestration Run** belongs to an Issue in the initial kernel scope
- **Kernel Compatibility Mode** preserves the existing issue, agent, runtime, skill, and task queue contracts while changing only the enabled workspace's assignment trigger path
- A **Kernel Schedule** can describe node dependencies, but the initial scheduler executes a simple ordered chain such as plan, execute, verify
- A **Kernel Event** records orchestration facts; WebSocket events remain a delivery mechanism for client refresh and live display
- A **Runtime Adapter** dispatches executable nodes through Agent Tasks and feeds their outcomes back into the Orchestration Run
- The **Skill Discovery Boundary** keeps initial orchestration aligned with the existing Agent skill model
- An **Approval Gate** pauses an Orchestration Run only when policy or evidence requires a human decision
- **Node Recovery** uses persisted node state, Kernel Events, and linked Agent Task status to resume orchestration after task failures or server restarts
- A **Decision Panel** explains orchestration progress from the node's perspective; Kernel Events remain available as supporting detail
- The **Kernel Test Strategy** treats node progression, Agent Task bridging, recovery, approval, event ordering, and evidence preservation as core correctness requirements
- **Node State** is linked to, but not derived solely from, Agent Task status
- **Node Type** stays deliberately small in the first implementation: plan, execute, verify
- A **Default Plan Node** produces the initial plan, execute, verify schedule without invoking a runtime
- **Hard Check Verification** determines whether an execute node produced enough structured outcome and evidence for orchestration to continue
- An **Evidence Contract** gives Hard Check Verification and the Decision Panel stable data instead of relying only on free-form agent output
- **Node Evidence** records the evidence extracted from Agent Task outcomes for a specific Orchestration Run and node
- An Issue can have at most one **Active Run** at a time
- **Run State** describes orchestration lifecycle while Issue status continues to describe product workflow
- **Issue Status Coordination** keeps orchestration progress visible without replacing human workflow judgment
- **Run Cancellation** stops active execution while preserving completed evidence and audit history
- The **Kernel Transaction Boundary** keeps Run State, Node State, Node Evidence, and Kernel Events consistent for recovery and audit
- **Run Advancement** moves an Orchestration Run forward from explicit lifecycle triggers and uses recovery scanning only as a safety net
- A **Run Lock** prevents concurrent triggers from advancing the same Orchestration Run twice
- **Node Dispatch Idempotency** ensures a repeated dispatch trigger cannot create two Agent Tasks for the same node attempt
- **Result Schema** gives the Evidence Contract an explicit version so verification can downgrade unknown or malformed results safely
- **Evidence Insufficient** routes malformed or incomplete Agent Task output into retry or Approval Gate instead of marking the node successful
- **Node Retry Policy** creates a new Agent Task attempt for recoverable node failures while preserving prior Kernel Events and Node Evidence
- An **Approval Action** resumes, retries, changes, or cancels orchestration from an Approval Gate
- A **Change Request** becomes audited context for the next node attempt while preserving the original Issue as written
- **Orchestration Context** lets Agent Tasks participate in kernel workflows while keeping existing runtime and skill behavior intact
- The **Kernel Persistence Model** stores orchestration lifecycle, node state, audit facts, and evidence while linking executable work through Agent Tasks
- The **Kernel API Surface** exposes orchestration through Issue detail and Approval Actions while preserving kernel invariants inside the server
- **Issue Detail Observability** keeps orchestration process visibility attached to the Issue being coordinated
- The **Orchestration Client State Boundary** follows Multica's server-state split and keeps orchestration facts out of Zustand
- An **Orchestration Refresh Event** tells clients to reload issue-scoped orchestration data from the API
- **Approval Permission** keeps human accountability attached to approval decisions
- **Orchestration Read Permission** prevents orchestration evidence from bypassing Issue visibility
- **Kernel Event Type** keeps orchestration audit records queryable, testable, and suitable for Decision Panel derivation
- **Node Reason Code** makes the Decision Panel consistent across web and desktop clients
- **Recommended Action** keeps user-facing controls aligned with the server-owned orchestration state machine
- **Evidence Retry Boundary** separates recoverable output-format failures from evidence that requires human judgment
- **Risk Approval Boundary** makes risk acceptance an explicit human decision
- **Verification Completion** means orchestration produced reviewable work, not that human acceptance is complete
- An **Attention Comment** brings exceptional orchestration states to humans without duplicating successful Decision Panel summaries
- The **Attention Audience** keeps orchestration notifications scoped to people already connected to the Issue

## Example Dialogue

> **Dev:** "Should the **Orchestration Kernel** run Codex directly?"
> **Domain expert:** "No. It decides which work should run and records evidence; the existing **Agent Task** and runtime adapter still execute the agent."

## Flagged Ambiguities

- "kernel" could mean a replacement for the task queue or a layer above it; resolved: **Orchestration Kernel** is a server-owned orchestration and decision layer above the existing task queue.
