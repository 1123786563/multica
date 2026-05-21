# Multica Orchestration

This context captures the domain language for Multica's AI-native orchestration layer. It describes how orchestration relates to existing issues, agents, runtimes, skills, and task execution.

## Language

**Orchestration Kernel**:
A Temporal-backed planning and decision layer above issues that coordinates work through workflows, nodes, events, evidence, Eino reasoning, and Agent Task dispatch.
_Avoid_: replacement task queue, runtime, agent CLI wrapper

**Agent Task**:
The existing executable unit claimed by a runtime and completed by an agent through the daemon.
_Avoid_: orchestration node, kernel task

**Kernel Execution Boundary**:
Temporal owns orchestration lifecycle state, while Multica starts, cancels, signals, projects, and observes workflows and still dispatches existing Agent Tasks for execution.
_Avoid_: DB-owned workflow state machine, direct CLI executor

**Orchestration Run**:
An issue-scoped Temporal Workflow Execution coordinated by the Orchestration Kernel and projected into Multica for product visibility.
_Avoid_: chat run, autopilot run, global workflow

**Orchestration Fail-Closed**:
The rule that orchestration entry points return an explicit unavailable or failed state when Temporal is not configured or reachable, instead of falling back to direct Agent Task execution.
_Avoid_: legacy fallback, dual completion semantics

**Kernel Schedule**:
The first orchestration scheduling model is a fixed Temporal workflow chain, with graph-shaped node records used as the Multica projection and UI model.
_Avoid_: full DAG scheduler, multi-agent parallel planner

**Kernel Event**:
A projected orchestration fact written from Temporal workflow progress, activity outcomes, signals, and Agent Task results for audit and process visibility in Multica.
_Avoid_: transient UI event, direct Zustand write

**Temporal Workflow Execution**:
The durable source of truth for an Orchestration Run's lifecycle, retry, timeout, cancellation, and recovery behavior.
_Avoid_: background job, read-model mirror

**Explicit Temporal Profile**:
The deployment boundary where Temporal Server is an explicitly configured external dependency and the orchestration worker is a separate process, not part of the default Multica API or default dev startup.
_Avoid_: embedded Temporal server, default make dev dependency

**Orchestration Worker**:
The dedicated worker process that hosts Temporal workflow and activity implementations for orchestration.
_Avoid_: API goroutine, daemon process

**Multica Projection**:
The product-side read model derived from Temporal workflow state and orchestration events for Issue Detail, API responses, comments, notifications, and audit views.
_Avoid_: source of truth, independent scheduler

**Projection Table Reuse**:
The decision to keep the existing `orchestration_plan`, `orchestration_node`, `orchestration_event`, and `orchestration_artifact` tables as Multica's orchestration projection instead of adding parallel `workflow_*` tables.
_Avoid_: parallel workflow read model, duplicate projection schema

**Temporal Activity**:
A non-deterministic orchestration step invoked by a Temporal Workflow, such as loading issue context, calling Eino, dispatching an Agent Task, validating results, or writing projections.
_Avoid_: workflow state owner, daemon task

**Projection Activity Boundary**:
The rule that Temporal Workflow code never writes Multica projection tables, comments, WebSocket notifications, or Agent Task side effects directly. Workflow code makes deterministic decisions; Activities perform projection writes and other external side effects idempotently.
_Avoid_: workflow-side DB write, replay side effect, non-idempotent projection

**Eino Reasoning Node**:
A reasoning-oriented activity implementation used for analysis, review, or summarization; it does not own workflow lifecycle or runtime execution.
_Avoid_: coding runtime, workflow engine

**Eino Reasoning Provider**:
The worker-scoped LLM provider used by Eino reasoning activities through Eino ChatModel implementations. It is configured independently from Multica Agent Runtime providers and is used only for orchestration analysis, advisory review, and summarization. The MVP implementation starts with one OpenAI-compatible ChatModel provider configured from worker environment variables; multi-provider UI, database-backed provider selection, and workspace-scoped provider policy are later extensions.
_Avoid_: daemon runtime provider, coding agent provider, per-Agent execution backend, workspace provider selector in MVP

**Eino Fail-Closed**:
The rule that production Eino reasoning activities do not silently fall back to static heuristics when the Eino Reasoning Provider is missing, misconfigured, unavailable, or returns malformed output. Static analysis is allowed only for tests or explicit development/mock modes; real provider failures surface as Activity failures for Temporal retry, projection, or visible orchestration failure handling.
_Avoid_: hidden static fallback, best-effort prompt generation, pretending provider reasoning happened

**Eino Structured Output Contract**:
The strict JSON contract for Eino reasoning outputs. Analyze-issue output must parse as JSON and contain only allowed analysis fields such as problem summary, execution advice, suspected context, risks, recommended agent prompt, reason code, and recommended action. Missing required fields, empty required values, natural-language-only responses, topology instructions, node mutations, workflow decisions, or authoritative success fields are malformed provider output.
_Avoid_: prose scraping, best-effort extraction, LLM topology patch, final success flag

**Eino Risk Signal**:
A risk surfaced by Eino reasoning that informs advisory review and Temporal Outcome Policy. High-risk, destructive, migration, or similarly unsafe concerns can route an Orchestration Run to an Approval Gate, but an Eino Risk Signal is not a final success or failure verdict.
_Avoid_: hidden blocker, LLM-owned failure, raw provider warning

**Advisory Review**:
An Eino-generated review of evidence, risks, and suggested next action that informs policy but cannot by itself mark an Orchestration Run successful.
_Avoid_: final verdict, LLM-owned completion

**Temporal Outcome Policy**:
The deterministic workflow policy that combines validation outcomes, Eino advisory review, risk rules, approval state, and retry budget to decide complete, approval, retry, or failure.
_Avoid_: Eino final decision, client-side workflow outcome

**Fixed Workflow Reasoning**:
The MVP rule that Eino reasons inside a fixed Temporal workflow and may produce prompts, risk summaries, reviews, and final summaries, but may not create, remove, reorder, or branch workflow nodes.
_Avoid_: dynamic planner, LLM-generated workflow graph

**Runtime Adapter**:
The bridge that maps an executable orchestration node to an existing Agent Task and later maps the Agent Task outcome back to the node. It does not introduce a new daemon protocol or run agent CLIs directly.
_Avoid_: kernel daemon protocol, direct runtime executor

**Agent Task Signal Bridge**:
The Multica API path that turns Agent Task completion, failure, or cancellation into a Temporal Signal or Update for the owning Orchestration Run.
_Avoid_: long-polling activity, DB polling loop

**Agent Task Outcome Signal Contract**:
The required correlation contract for Agent Task outcome signals. Every completion, failure, or cancellation signal carries `plan_id`, `node_id`, `attempt`, `task_id`, `outcome_version`, and a result reference or payload. The Workflow only advances when the signal matches the current waiting node attempt and linked Agent Task.
_Avoid_: workflow-id-only signal, stale attempt advance, duplicate completion advance

**Skill Discovery Boundary**:
The first orchestration implementation uses skills already bound to the selected Agent. The kernel may record the skill context available to a node, but it does not perform workspace-wide skill search or dynamic skill selection.
_Avoid_: global skill planner, automatic runtime skill selection

**Approval Gate**:
A conditional human decision point used when orchestration cannot safely continue automatically, such as high-risk work, insufficient evidence, failed verification, or an explicit issue or node policy. It is not inserted into every run by default.
_Avoid_: mandatory human checkpoint, manual-only orchestration

**Node Recovery**:
Temporal retry and replay recover workflow progress, while Multica preserves projected node history, Agent Task links, Kernel Events, and evidence across attempts.
_Avoid_: restart whole run, erase evidence

**Decision Panel**:
The primary observability surface for an Orchestration Run. The MVP renders a linear node list with status, reason, recommended action, latest summary, attempts, evidence count, Agent Task link, and expandable events or evidence detail.
_Avoid_: event-dump-first UI, opaque top-line status, DAG-first UI

**Linear Orchestration Panel**:
The MVP Issue Detail panel shape for orchestration: a fixed-order node list plus expandable events and evidence, aligned with the fixed workflow chain. It does not render a DAG, graph canvas, standalone orchestration page, or workflow designer until branch, parallel, or loop workflows are introduced.
_Avoid_: graph canvas in MVP, workflow designer, premature DAG visualization

**Kernel Test Strategy**:
The first testing strategy prioritizes Temporal workflow/activity tests, projection consistency tests, Agent Task bridge tests, and frontend contract tests before adding minimal end-to-end coverage.
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

**Deterministic Result Validation**:
The MVP validation activity that checks Agent Task outcome schema and evidence fields without running additional daemon commands or asking Eino to judge success.
_Avoid_: daemon test runner, LLM validation

**Evidence Contract**:
The minimum structured outcome an Agent Task must provide for kernel verification: summary, changed files, artifacts, tests, and risks. The kernel may parse this from Agent Task result JSON and attach it to node evidence.
_Avoid_: prose-only completion, unverifiable output

**Node Evidence**:
Structured evidence persisted for an orchestration node, separate from the raw Agent Task result. Node Evidence is queried by verification, recovery, and observability surfaces and remains available across retries.
_Avoid_: task-result-only evidence, reparse-only verification

**Active Run**:
The single non-terminal Orchestration Run for an Issue. Multica enforces Active Run uniqueness in the projection before starting Temporal; repeated triggers for the same Issue return the existing Active Run instead of creating a parallel run.
_Avoid_: parallel issue runs, duplicate orchestration, duplicate workflow start

**Run Workflow Identity**:
The deterministic Temporal Workflow ID for a specific Orchestration Run. The MVP derives it from workspace id, issue id, and plan id, such as `multica/{workspace_id}/issue/{issue_id}/run/{plan_id}`. Each new run gets a distinct Workflow ID; Active Run uniqueness prevents concurrent runs for the same Issue.
_Avoid_: fixed issue workflow id, workflow id reuse ambiguity, history overwrite

**Run State**:
The Temporal-owned lifecycle status of an Orchestration Run, projected into Multica independently from the Issue workflow status.
_Avoid_: issue status alias, board status

**Issue Status Coordination**:
The limited relationship between orchestration lifecycle and the Issue workflow. The kernel may perform deterministic status nudges, such as moving a ready agent-assigned Issue into progress, but it does not automatically mark business work done.
_Avoid_: full board automation, automatic done

**Run Cancellation**:
The cancellation behavior for orchestration. Cancelling an Orchestration Run cancels the Temporal Workflow Execution and propagates cancellation to active linked Agent Tasks; projected history and Node Evidence remain available.
_Avoid_: delete run history, leave linked task running

**Kernel Transaction Boundary**:
The consistency rule that Temporal workflow progress and Multica projections must be reconciled explicitly; Temporal is authoritative when projection rows lag or conflict.
_Avoid_: DB-only state authority, projection as workflow owner

**Run Advancement**:
The scheduling trigger model for orchestration. Temporal advances the workflow after workflow start, activity completion, Agent Task completion signals, approval actions, manual retry, timeout, or cancellation.
_Avoid_: DB-triggered state machine, polling-first orchestration

**Run Lock**:
Multica's database uniqueness and transaction boundary used while creating an Active Run and starting its Temporal Workflow. Temporal workflow identity prevents duplicate execution for a specific run, while the Active Run constraint prevents duplicate concurrent runs for the Issue.
_Avoid_: in-memory lock, parallel issue workflows, fixed issue workflow id

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
The retry policy applied to an orchestration node. The MVP allows at most two node attempts total. Automatic retry is limited to recoverable, non-semantic failures such as malformed schema, insufficient evidence, timeout, worker transient error, or signal delivery repair. Failed tests, non-empty risks, high-risk review concerns, unverifiable evidence, and destructive changes enter an Approval Gate instead of auto-retrying.
_Avoid_: infinite retry, whole-run retry by default, auto-retry risky code changes

**Approval Action**:
The small set of human decisions available when an Orchestration Run is waiting for approval in the MVP: approve, retry while retry budget remains, or cancel. Approval actions are audited and do not allow arbitrary node jumps or prompt edits in the first implementation.
_Avoid_: arbitrary state edit, jump to node

**Approval Audit Event**:
The Kernel Event written for every Approval Action. It records `actor_id`, `actor_type=human`, `action`, `reason`, `plan_id`, `node_id`, and the resulting Temporal signal or cancellation request.
_Avoid_: unaudited approval, agent actor approval, implicit risk acceptance

**Deferred Change Request**:
A future Approval Gate extension where a human can provide additional instructions for a later node attempt. It is not part of the MVP Approval Action set; MVP retry uses kernel-generated validation and review context rather than a human-authored prompt patch.
_Avoid_: hidden issue rewrite, unaudited prompt patch, MVP prompt editing

**Orchestration Context**:
The small kernel-provided execution context appended to an Agent Task, such as run id, node id, node type, attempt, expected result schema, prior evidence summary, and validation feedback. It complements the existing daemon prompt and CLI workflow instead of replacing them.
_Avoid_: replacement daemon prompt, bespoke agent workflow

**Kernel Persistence Model**:
The first persistence model for orchestration stores Temporal workflow identifiers, projected nodes, projected events, artifacts, evidence, and Agent Task links. Temporal history owns lifecycle state.
_Avoid_: DB-owned lifecycle state, policy table explosion

**Kernel API Surface**:
The minimal external API for orchestration: issue-scoped read access to run progress and a small approval-action endpoint. Run, node, event, and evidence records are not exposed as generic CRUD resources in the first implementation.
_Avoid_: public node editor, generic orchestration CRUD

**Issue Detail Observability**:
The first user-facing orchestration surface lives inside the existing Issue Detail experience. It shows the Linear Orchestration Panel and expandable event or evidence detail without introducing a standalone orchestration page.
_Avoid_: separate orchestration app, route-first observability

**Orchestration Client State Boundary**:
The frontend ownership rule for orchestration data. Run, node, event, and evidence data are server state managed by React Query; Zustand may only hold local UI state such as expanded nodes or selected panels.
_Avoid_: persisted orchestration store, duplicate server state

**Orchestration Refresh Event**:
The coarse WebSocket notification that orchestration data changed for an Issue. The first implementation uses one refresh event rather than streaming every Kernel Event over WebSocket.
_Avoid_: mirrored event stream, WS source of truth

**Approval Permission**:
The human authorization rule for Approval Actions. In the first implementation, workspace owners, workspace admins, the Issue creator, and a human Issue assignee may approve, retry, or cancel a paused run. Agent assignees and the agent that initiated or executed the run cannot perform Approval Actions.
_Avoid_: self-approving agent, agent self-cancel, unrestricted approval

**Orchestration Read Permission**:
The authorization rule for viewing orchestration data. Reading an Orchestration Run follows the same access boundary as reading its Issue; approval and mutation actions remain separately restricted.
_Avoid_: separate read ACL, evidence side channel

**Kernel Event Type**:
The stable event-type vocabulary for projected Kernel Events. Event types describe workflow, node, task-link, evidence, retry, Eino, Temporal, and approval facts instead of arbitrary event strings.
_Avoid_: free-form audit event, ungoverned event name

**Signal Audit Event**:
A low-noise Kernel Event that records ignored or rejected Agent Task outcome signals, such as `signal.duplicate_ignored`, `signal.stale_ignored`, and `signal.mismatched_rejected`. Signal Audit Events are shown in expanded event detail by default and do not become the Decision Panel's primary error unless the current waiting node needs attention because no valid signal arrives or repair fails.
_Avoid_: hidden signal mismatch, noisy primary error, callback spam

**Node Reason Code**:
The server-derived explanation for why a node is in its current state. The frontend displays and localizes the code but does not infer orchestration reasons from raw events.
_Avoid_: frontend state inference, event-order guessing

**Recommended Action**:
The Multica-projected next action suggested for a node or run, based on Temporal lifecycle state, reason, permissions, and retry budget. The frontend renders the recommendation but does not choose orchestration actions independently.
_Avoid_: client-side action inference, UI-owned state machine

**Evidence Retry Boundary**:
The rule for automatic retry after insufficient evidence. Missing or malformed structured result fields may auto-retry within the Node Retry Policy while retry budget remains; if attempts are exhausted, the run enters an Approval Gate with retry unavailable. Risk-bearing or untrustworthy evidence enters an Approval Gate immediately.
_Avoid_: retry risky work automatically, block all malformed output on humans

**Risk Approval Boundary**:
The rule that risk-bearing evidence requires human approval in the first orchestration implementation. Non-empty risks, failed tests, high-risk review concerns, unverifiable results, or destructive operations pause the run instead of auto-succeeding or auto-retrying.
_Avoid_: agent self-approves risk, silent risk acceptance

**Verification Completion**:
The outcome when hard-check verification succeeds. The Orchestration Run succeeds and the Issue may move to in review, but the kernel does not automatically mark the Issue done.
_Avoid_: verified equals done, auto-close issue

**Review Handoff**:
The product handoff after a successful Orchestration Run where Multica writes summary and evidence and may move the Issue to review, leaving final done/close to a human or future explicit policy.
_Avoid_: automatic done, hidden acceptance

**Attention Comment**:
An orchestration-generated Issue comment used only when human attention is required: `waiting_human`, run failed, retry exhausted, repair failed, or Temporal unavailable at an orchestration entry point. Successful orchestration does not create a default attention comment.
_Avoid_: success-comment noise, hidden failure, routine progress comment

**Attention Audience**:
The human audience for orchestration states that need attention. The MVP notifies only Issue-relevant humans: Issue creator, human assignee, and subscribers or watchers. It does not mention agent assignees and does not broadcast to the workspace.
_Avoid_: workspace-wide alert, agent mention loop, unrelated member mention

## Relationships

- An **Orchestration Kernel** coordinates one or more **Agent Tasks**
- An **Agent Task** remains the execution unit for an agent runtime
- An **Orchestration Kernel** does not replace the existing issue, agent, runtime, skill, or task queue models
- The **Temporal Workflow Execution** is the source of truth for an **Orchestration Run**
- The **Explicit Temporal Profile** keeps Temporal infrastructure and the **Orchestration Worker** outside default API/dev startup
- The **Multica Projection** makes Temporal workflow progress visible through Issue Detail, API responses, comments, and notifications
- **Projection Table Reuse** keeps existing orchestration API, Agent Task links, and Decision Panel contracts aligned while Temporal replaces lifecycle ownership
- The **Kernel Execution Boundary** keeps orchestration lifecycle in Temporal while runtime execution stays in the daemon-backed Agent Task lifecycle
- An **Orchestration Run** belongs to an Issue in the initial kernel scope
- **Orchestration Fail-Closed** preserves a single orchestration completion model when Temporal is unavailable
- A **Kernel Schedule** can describe node dependencies, but the initial scheduler executes a simple ordered chain such as plan, execute, verify
- A **Kernel Event** projects orchestration facts from Temporal workflow progress; WebSocket events remain a delivery mechanism for client refresh and live display
- A **Runtime Adapter** dispatches executable nodes through Agent Tasks and feeds their outcomes back into the Orchestration Run
- The **Agent Task Signal Bridge** is the primary path for Agent Task outcomes to resume the waiting **Temporal Workflow Execution**
- The **Agent Task Outcome Signal Contract** prevents stale, duplicate, or mismatched task outcomes from advancing the wrong node attempt
- A **Temporal Activity** may call an **Eino Reasoning Node** or dispatch an **Agent Task**, but it does not own workflow lifecycle
- The **Projection Activity Boundary** keeps all Multica projection writes and other side effects out of deterministic Workflow code
- **Fixed Workflow Reasoning** keeps Eino output inside the predefined workflow topology for the MVP
- The **Skill Discovery Boundary** keeps initial orchestration aligned with the existing Agent skill model
- An **Approval Gate** pauses an Orchestration Run only when policy or evidence requires a human decision
- **Node Recovery** uses Temporal retry/replay plus projected node history, Kernel Events, and linked Agent Task status to resume orchestration after task failures or server restarts
- A **Decision Panel** explains orchestration progress from the node's perspective; Kernel Events remain available as supporting detail
- The **Linear Orchestration Panel** keeps MVP observability aligned with the fixed workflow chain and defers DAG visualization until topology actually branches
- The **Kernel Test Strategy** treats node progression, Agent Task bridging, recovery, approval, event ordering, and evidence preservation as core correctness requirements
- **Node State** is linked to, but not derived solely from, Agent Task status
- **Node Type** stays deliberately small in the first implementation: plan, execute, verify
- A **Default Plan Node** produces the initial plan, execute, verify schedule without invoking a runtime
- **Hard Check Verification** determines whether an execute node produced enough structured outcome and evidence for orchestration to continue
- **Deterministic Result Validation** validates the structured Agent Task result but does not execute lint, test, or shell commands
- **Advisory Review** may recommend a next action, but **Temporal Outcome Policy** decides the Orchestration Run outcome
- An **Evidence Contract** gives Hard Check Verification and the Decision Panel stable data instead of relying only on free-form agent output
- **Node Evidence** records the evidence extracted from Agent Task outcomes for a specific Orchestration Run and node
- An Issue can have at most one **Active Run** at a time
- **Run Workflow Identity** gives every run a stable Temporal Workflow ID derived from its projection `plan_id`
- **Run State** describes orchestration lifecycle while Issue status continues to describe product workflow
- **Issue Status Coordination** keeps orchestration progress visible without replacing human workflow judgment
- **Run Cancellation** stops active execution while preserving completed evidence and audit history
- The **Kernel Transaction Boundary** treats Temporal as authoritative when projected Run State, Node State, Node Evidence, or Kernel Events lag
- **Run Advancement** moves an Orchestration Run forward through Temporal workflow execution and signals
- A **Run Lock** combines Multica projection uniqueness and Temporal workflow identity to make orchestration start idempotent
- **Node Dispatch Idempotency** ensures a repeated dispatch trigger cannot create two Agent Tasks for the same node attempt
- **Result Schema** gives the Evidence Contract an explicit version so verification can downgrade unknown or malformed results safely
- **Evidence Insufficient** routes malformed or incomplete Agent Task output into retry or Approval Gate instead of marking the node successful
- **Node Retry Policy** limits MVP execution to two node attempts and creates a new Agent Task attempt only for recoverable non-semantic failures or explicit human retry with budget remaining
- An **Approval Action** approves, retries, or cancels orchestration from an Approval Gate
- An **Approval Audit Event** attaches human accountability to every approve, retry, or cancel decision
- A **Deferred Change Request** stays outside the MVP Approval Action set; if introduced later, it must be explicit, audited, and preserve the original Issue as written
- **Orchestration Context** lets Agent Tasks participate in kernel workflows while keeping existing runtime and skill behavior intact
- The **Kernel Persistence Model** stores orchestration lifecycle, node state, audit facts, and evidence while linking executable work through Agent Tasks
- The **Kernel API Surface** exposes orchestration through Issue detail and Approval Actions while preserving kernel invariants inside the server
- **Issue Detail Observability** keeps orchestration process visibility attached to the Issue being coordinated
- The **Orchestration Client State Boundary** follows Multica's server-state split and keeps orchestration facts out of Zustand
- An **Orchestration Refresh Event** tells clients to reload issue-scoped orchestration data from the API
- **Approval Permission** keeps human accountability attached to approval decisions and blocks agent self-approval
- **Orchestration Read Permission** prevents orchestration evidence from bypassing Issue visibility
- **Kernel Event Type** keeps orchestration audit records queryable, testable, and suitable for Decision Panel derivation
- **Signal Audit Event** preserves duplicate, stale, and mismatched signal evidence without making callback noise the primary user-facing state
- **Node Reason Code** makes the Decision Panel consistent across web and desktop clients
- **Recommended Action** keeps user-facing controls aligned with Temporal-owned orchestration lifecycle state
- **Evidence Retry Boundary** separates recoverable output-format failures from evidence that requires human judgment
- **Risk Approval Boundary** makes risk acceptance an explicit human decision
- **Verification Completion** means orchestration produced reviewable work, not that human acceptance is complete
- **Review Handoff** separates successful orchestration from final Issue closure
- An **Attention Comment** brings exceptional orchestration states to humans without duplicating successful Decision Panel summaries
- The **Attention Audience** keeps orchestration notifications scoped to Issue-relevant humans and excludes agent assignees

## Example Dialogue

> **Dev:** "Should the **Orchestration Kernel** run Codex directly?"
> **Domain expert:** "No. It decides which work should run and records evidence; the existing **Agent Task** and runtime adapter still execute the agent."

> **Dev:** "If Temporal history and Multica's projected node table disagree, which one wins?"
> **Domain expert:** "Temporal wins. Multica's tables are the product projection and must be repaired from workflow state."

> **Dev:** "Should the daemon-dispatch activity poll the database until the Agent Task finishes?"
> **Domain expert:** "No. It creates or links the Agent Task, then the existing task completion API signals the waiting Temporal Workflow."

> **Dev:** "Should Temporal introduce new `workflow_executions` tables?"
> **Domain expert:** "No. The existing `orchestration_*` tables remain Multica's projection; Temporal history owns lifecycle state."

> **Dev:** "Can Eino add a new review node when it thinks risk is high?"
> **Domain expert:** "Not in the MVP. Eino can recommend review or human attention, but Temporal owns the fixed workflow topology."

> **Dev:** "Should `validate_result` run tests through the daemon?"
> **Domain expert:** "No. The coding Agent Task may report test results, but MVP validation only checks structured evidence."

> **Dev:** "Can Eino review mark the workflow successful?"
> **Domain expert:** "No. Eino review is advisory; Temporal Outcome Policy makes the final lifecycle decision."

> **Dev:** "Does `complete_issue` mark the Issue done?"
> **Domain expert:** "No. It hands off reviewable work and evidence; humans or a later explicit policy close the Issue."

> **Dev:** "Should `make dev` start Temporal by default?"
> **Domain expert:** "No. Temporal runs through an explicit profile and worker; the API reports orchestration unavailable when it is not configured."

> **Dev:** "If Temporal is down, should agent assignment create a direct Agent Task?"
> **Domain expert:** "No. Orchestration fails closed so completion semantics stay observable and consistent."

> **Dev:** "Can a reviewer type request changes from the MVP Approval Gate?"
> **Domain expert:** "No. The MVP gate supports approve, retry, and cancel only; human-authored change requests are a future extension."

> **Dev:** "Should failed tests or non-empty risks automatically retry the coding agent?"
> **Domain expert:** "No. Automatic retry is only for recoverable non-semantic failures; risky or semantically failed work pauses for human approval."

> **Dev:** "Should an Issue always reuse the same Temporal Workflow ID?"
> **Domain expert:** "No. Each Orchestration Run gets its own Workflow ID derived from `plan_id`; duplicate triggers return the existing Active Run."

> **Dev:** "Can Temporal Workflow code write `orchestration_event` rows directly?"
> **Domain expert:** "No. Workflow code only decides; projection writes, WebSocket refreshes, comments, and Agent Task cancellation happen through Activities."

> **Dev:** "Can an Agent Task completion signal advance a workflow by `workflow_id` alone?"
> **Domain expert:** "No. It must match the current `plan_id`, `node_id`, `attempt`, and `task_id`; stale or duplicate signals are ignored or recorded without advancing."

> **Dev:** "Should a stale signal make the Issue Detail panel look failed?"
> **Domain expert:** "No. It is an expanded audit event by default; the main panel only escalates when the current waiting node lacks a valid signal or repair fails."

> **Dev:** "Should the MVP render orchestration as a DAG graph?"
> **Domain expert:** "No. It renders a linear node list with expandable events and evidence; DAG visualization waits for branch, parallel, or loop workflows."

> **Dev:** "Can the assigned agent approve or cancel its own orchestration run?"
> **Domain expert:** "No. Approval Actions require an authorized human actor and always write an Approval Audit Event."

> **Dev:** "Should orchestration mention the whole workspace when it needs approval?"
> **Domain expert:** "No. Attention comments mention only Issue creator, human assignee, and subscribers/watchers; successful runs do not create attention comments."

## Flagged Ambiguities

- "kernel" could mean a replacement for the task queue or a layer above it; resolved: **Orchestration Kernel** is a Temporal-backed orchestration and decision layer above the existing task queue.
- "source of truth" previously meant Multica's persisted Kernel Events and node state; resolved: **Temporal Workflow Execution** is the authoritative lifecycle state, while Multica persists a projection for product collaboration and observability.
- "waiting for agent work" could mean a long-running polling Activity or a Workflow waiting on Signal; resolved: **Agent Task Signal Bridge** is the first-stage completion path.
- "workflow tables" could mean new `workflow_*` tables or the existing `orchestration_*` tables; resolved: use **Projection Table Reuse** and extend existing orchestration tables.
- "Eino plans" could mean dynamic workflow generation or reasoning inside a fixed workflow; resolved: MVP uses **Fixed Workflow Reasoning** only.
- "Eino provider" could mean the same provider as Multica's daemon-backed Agent Runtime provider; resolved: **Eino Reasoning Provider** is a separate worker-scoped ChatModel provider for reasoning activities only.
- "real Eino provider" could mean a full provider marketplace or the first concrete SDK provider; resolved: MVP uses one OpenAI-compatible **Eino Reasoning Provider** through Eino ChatModel and leaves multi-provider configuration for later.
- "Eino unavailable" could mean use the old static analyzer to keep the run moving; resolved: **Eino Fail-Closed** prohibits hidden static fallback in production.
- "Eino output" could mean parse whatever prose the model returns; resolved: **Eino Structured Output Contract** requires strict JSON and treats prose-only or topology-changing output as malformed.
- "Eino risk" could mean a final workflow decision or only hidden prompt context; resolved: **Eino Risk Signal** is visible advisory input that can route high-risk work to an Approval Gate through deterministic policy.
- "`validate_result`" could mean running tests or validating evidence; resolved: MVP uses **Deterministic Result Validation** only.
- "`review_result` success" could mean Eino's recommendation or final workflow outcome; resolved: **Advisory Review** informs **Temporal Outcome Policy**.
- "`complete_issue`" could mean close the Issue or hand off reviewable work; resolved: MVP uses **Review Handoff** and does not auto-done.
- "Temporal deployment" could mean embedded API dependency or explicit infrastructure profile; resolved: use **Explicit Temporal Profile** with a separate **Orchestration Worker**.
- "Temporal unavailable" could mean direct Agent Task fallback or visible orchestration failure; resolved: use **Orchestration Fail-Closed**.
- "approval action" could mean a full human edit flow or a minimal workflow decision; resolved: MVP **Approval Action** supports approve, retry, and cancel only.
- "retry" could mean retrying a transient activity failure or re-running code-modifying agent work; resolved: MVP **Node Retry Policy** auto-retries only recoverable non-semantic failures and caps node attempts at two.
- "workflow id" could mean one fixed ID per Issue or one ID per run; resolved: use **Run Workflow Identity** derived from `plan_id`, with **Active Run** uniqueness preventing concurrent issue runs.
- "projection write" could mean direct Workflow DB writes or Activity side effects; resolved: **Projection Activity Boundary** requires all projection and notification side effects to run in Activities.
- "task outcome signal" could mean any signal to the Workflow ID or a strongly correlated node-attempt outcome; resolved: **Agent Task Outcome Signal Contract** requires `plan_id`, `node_id`, `attempt`, and `task_id` match the current waiting node attempt.
- "signal mismatch visibility" could mean hidden debug logs or prominent user-facing failure; resolved: **Signal Audit Event** records it in expanded event detail unless the current run needs attention.
- "orchestration UI" could mean a graph/DAG designer or the existing Issue Detail surface; resolved: MVP uses a **Linear Orchestration Panel** inside Issue Detail.
- "approval permission" could mean Issue read/write access or explicit human approval authority; resolved: **Approval Permission** allows only workspace owners/admins, Issue creator, and human assignee, and **Approval Audit Event** records every decision.
- "attention audience" could mean everyone in the workspace or only Issue-relevant humans; resolved: **Attention Audience** is Issue creator, human assignee, and subscribers/watchers, excluding agent assignees.
