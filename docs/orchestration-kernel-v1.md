# AI-native Orchestration Kernel v1 Temporal MVP 实施计划

## 状态

已完成 grilling，进入实施计划阶段。

日期：2026-05-14

权威设计输入：

- `CONTEXT.md`
- `Multica_AI_native_Orchestration_Kernel_MVP_Design.md`
- `docs/adr/0001-ai-native-orchestration-kernel-v1-boundary.md`
- `docs/adr/0002-temporal-orchestration-source-of-truth.md`
- `docs/adr/0003-agent-task-outcomes-signal-temporal.md`
- `docs/adr/0004-reuse-orchestration-tables-as-temporal-projection.md`
- `docs/adr/0005-eino-reasons-inside-fixed-workflow.md`
- `docs/adr/0006-validate-result-is-deterministic-evidence-check.md`
- `docs/adr/0007-eino-review-is-advisory.md`
- `docs/adr/0008-complete-issue-hands-off-review.md`
- `docs/adr/0009-temporal-runs-as-explicit-profile.md`
- `docs/adr/0010-orchestration-fails-closed-without-temporal.md`
- `docs/adr/0011-approval-gate-minimal-actions.md`
- `docs/adr/0012-node-retry-policy-mvp.md`
- `docs/adr/0013-run-workflow-identity-and-active-run-idempotency.md`
- `docs/adr/0014-projection-side-effects-through-activities.md`
- `docs/adr/0015-agent-task-outcome-signal-correlation.md`
- `docs/adr/0016-signal-mismatch-events-are-low-noise-audit.md`
- `docs/adr/0017-mvp-uses-linear-orchestration-panel.md`
- `docs/adr/0018-approval-actions-require-human-authority.md`
- `docs/adr/0019-attention-comments-target-issue-relevant-humans.md`
- `docs/temporal-orchestration-mvp-checkout.md`

## MVP 目标

在现有 Multica Issue、Agent、Runtime、Skill、daemon、Agent Task 模型之上，增加 Temporal-backed AI-native orchestration kernel。

MVP 只证明一条端到端闭环：

```text
Issue -> Temporal Workflow -> Eino reasoning -> Agent Task execution
      -> Agent Task outcome Signal -> deterministic validation
      -> advisory review -> review handoff / approval / retry / failure
      -> Multica projection + Issue Detail observability
```

Temporal 是 orchestration lifecycle source of truth。Multica 的 `orchestration_*` 表是产品侧 projection。Eino 做固定 workflow 内 reasoning。daemon-backed Agent Task 仍是代码执行单元。

## 非目标

MVP 明确不做：

- 不回落旧 direct Agent Task orchestration path。
- 不新增 `workflow_*` 并行 projection 表。
- 不让 Eino 生成、修改、重排、分支或循环 workflow topology。
- 不在 Temporal Workflow 代码里直接写 DB、WS、comment、Agent Task side effects。
- 不把 `validate_result` 做成第二个 daemon test runner。
- 不让 Eino review 直接决定 workflow success。
- 不自动把 Issue 标记为 done。
- 不支持 `request changes`。
- 不允许 agent 自批、自 retry、自 cancel。
- 不做 DAG canvas、workflow designer、standalone orchestration page。
- 不广播 workspace，不 mention agent assignee。

## 核心实施约束

### Temporal Boundary

- Temporal Workflow Execution 拥有 run lifecycle、retry、timeout、cancellation、replay、recovery。
- API 和 worker 通过显式 Temporal 配置连接。
- `make dev` 不默认启动 Temporal。
- Temporal unavailable 时 orchestration entry points fail closed，不创建 direct Agent Task。

### Projection Boundary

- 复用并扩展 `orchestration_plan`、`orchestration_node`、`orchestration_event`、`orchestration_artifact`。
- projection lag 或冲突时，以 Temporal history 为准，由 repair / reconciliation Activity 修复。
- Workflow 代码只做 deterministic decision；projection writes、comment、WS、notification、Agent Task side effects 全部在 Activity 中执行。

### Active Run and Workflow Identity

- 一个 Issue 同一时间最多一个 active `orchestration_plan`。
- 重复 start 返回已有 active plan，不启动第二个 Temporal Workflow。
- 每个 run 使用独立 Workflow ID：`multica/{workspace_id}/issue/{issue_id}/run/{plan_id}`。
- completed / failed / cancelled 历史 run 保留，新 run 使用新 `plan_id`。

### Agent Task Signal Contract

Agent Task outcome Signal 必须携带：

- `plan_id`
- `node_id`
- `attempt`
- `task_id`
- `outcome_version`
- `result_ref` 或 `result_json`

Workflow 只在 Signal 匹配当前 waiting node attempt 和 linked Agent Task 时推进。

重复、过期、mismatched Signal 写入低噪声 audit event 或忽略，不推进 workflow：

- `signal.duplicate_ignored`
- `signal.stale_ignored`
- `signal.mismatched_rejected`

### Outcome Policy

- `validate_result` 只做 schema / evidence deterministic validation。
- Eino `review_result` 是 advisory，不拥有 final verdict。
- `complete_issue` 是 review handoff，不 auto done。
- Node retry 最多 2 次 attempt。
- 自动 Node retry 只覆盖 recoverable non-semantic failures。
- failed tests、risks、high-risk review concern、unverifiable evidence 进入 Approval Gate。

### Approval and Attention

- Approval Gate 只支持 `approve`、`retry`、`cancel`。
- Approval Action 只允许 authorized human actor：workspace owner/admin、Issue creator、human assignee。
- agent assignee、发起 run 的 agent、执行 run 的 agent 禁止 approval action。
- 每次 Approval Action 写 `approval.*` audit event，包含 `actor_id`、`actor_type=human`、`action`、`reason`、`plan_id`、`node_id`。
- Attention comment 只在 `waiting_human`、run failed、retry exhausted、repair failed、Temporal unavailable 等异常状态创建。
- Attention audience 只包含 Issue creator、human assignee、subscribers/watchers。

## 实施阶段

### Phase 0: Contract Baseline

目标：把已接受设计转成可测试的内部 contract，避免后续实现偏航。

交付物：

- Go package 边界草案：workflow、activities、projection、daemon bridge、eino adapter、api。
- Temporal config struct 和 disabled/unavailable error contract。
- Projection DTO / API response contract。
- Result Schema v1、Signal payload、Approval Action payload、Attention payload。

验收标准：

- 所有新增 contract 都能映射回 ADR 0002-0019。
- 无 `workflow_*` 表设计。
- 无 Workflow-side DB/HTTP/LLM/daemon direct call。

测试重点：

- contract unit tests / enum validation。
- unknown version / unknown enum fallback。

### Phase 1: Temporal Skeleton and Explicit Profile

目标：接入 Temporal client / worker / fixed workflow skeleton，但不触发真实 Agent Task。

交付物：

- Temporal client factory。
- `make orchestration-worker` 或等价 worker 启动命令。
- `IssueWorkflow` fixed chain skeleton。
- mock Activities：load/analyze/dispatch/wait-signal/validate/review/summarize/complete。
- API start path 在 Temporal configured 时启动 workflow。
- Temporal unavailable 时 fail closed。

验收标准：

- 显式配置 Temporal + worker 后，Issue 可以启动 workflow。
- 未配置或不可达时返回 unavailable，不创建 direct Agent Task。
- 默认 `make dev` 不要求 Temporal。

测试重点：

- workflow unit test。
- start fail-closed test。
- no fallback Agent Task test。

### Phase 2: Projection Migration and Idempotent Start

目标：把现有 `orchestration_*` 表扩展成 Temporal projection，并实现 Active Run 幂等 start。

交付物：

- `orchestration_plan` 增加 `temporal_workflow_id`、`temporal_run_id`、`workflow_type`、`projection_version`、`last_synced_at`、`sync_error`。
- `orchestration_node` 增加 `workflow_node_key`、`temporal_activity_id`、`signal_name`、projection metadata。
- `orchestration_event` / `orchestration_artifact` 增加 source / temporal event metadata。
- Temporal Workflow ID 生成：`multica/{workspace_id}/issue/{issue_id}/run/{plan_id}`。
- duplicate start 返回 existing active plan。
- `WorkflowAlreadyStarted` projection repair。

验收标准：

- 一个 Issue 同时只能有一个 active run。
- terminal run 后可以创建新 run。
- projection status 不作为 lifecycle source of truth。

测试重点：

- concurrent start race。
- duplicate start。
- terminal rerun。
- WorkflowAlreadyStarted repair。

### Phase 3: Projection Activities and Event Stream

目标：所有 Multica-visible side effects 通过 Activities 幂等写入。

交付物：

- `ProjectWorkflowStartedActivity`。
- `ProjectNodeStarted/Completed/FailedActivity`。
- `ProjectEventActivity`。
- `ProjectArtifactActivity`。
- `RepairProjectionActivity`。
- coarse `orchestration:updated` WS refresh。

验收标准：

- Workflow replay 不重复写 projection、comment、notification、Agent Task side effects。
- projection events 能展示 fixed chain 节点状态。
- `signal.*_ignored` 和 `approval.*` event type 可写入。

测试重点：

- Activity idempotency。
- Workflow replay determinism。
- event ordering。
- projection repair。

### Phase 4: Eino Reasoning Activities

目标：接入 Eino，但只作为固定 workflow 内 reasoning activity。

交付物：

- `EinoKernel` interface。
- `AnalyzeIssueActivity`。
- `ReviewResultActivity`。
- `SummarizeResultActivity`。
- output schema：`execution_advice`、`recommended_agent_prompt`、risk/review/summary。

验收标准：

- AnalyzeIssue 能生成 coding prompt 和 execution advice。
- ReviewResult 只输出 advisory review，不输出 authoritative `is_success`。
- SummarizeResult 生成 review handoff summary。
- Eino 不创建/删除/重排 workflow node。

测试重点：

- mocked Eino output parsing。
- advisory review cannot override failed tests / risks。
- malformed Eino response handling。

### Phase 5: DaemonBridge and Agent Task Outcome Signal

目标：Temporal dispatch 现有 Agent Task，daemon completion 通过 Signal 回到 Workflow。

交付物：

- `DispatchDaemonAgentActivity`。
- task link：`orchestration_plan_id`、`orchestration_node_id`、`attempt`、`task_id`、`temporal_workflow_id`。
- completion/failure/cancellation API hook。
- `AgentTaskCompleted/Failed/Cancelled` Signal sender。
- Signal payload validation。
- repair job 补发 Signal。

验收标准：

- Dispatch Activity 只创建/复用 Agent Task，不等待 task 完成。
- Workflow wait Signal。
- matching Signal 推进 workflow。
- duplicate/stale/mismatched Signal 不推进 workflow，并写低噪声 audit event。

测试重点：

- dispatch idempotency。
- completion Signal happy path。
- duplicate Signal no-op。
- stale attempt rejected。
- wrong task / wrong node / wrong plan rejected。
- repair-job Signal replay。

### Phase 6: Validation, Retry, Outcome Policy

目标：实现 deterministic validation、bounded retry、approval routing 和 final policy。

交付物：

- Result Schema v1 parser。
- `ValidateResultActivity`。
- `TemporalOutcomePolicy`。
- Node retry attempt tracking。
- evidence insufficient handling。
- risks / failed tests / high-risk review -> Approval Gate。

验收标准：

- `validate_result` 不运行 shell、daemon、LLM。
- automatic retry 只覆盖 schema/evidence/transient failures。
- `max_node_attempts = 2`。
- failed tests / risks 不自动 retry。
- retry exhausted 进入 attention / approval 或 failed policy。

测试重点：

- schema malformed -> retry。
- evidence insufficient -> retry。
- retry exhausted。
- failed tests -> approval。
- risks non-empty -> approval。
- positive Eino review cannot force success。

### Phase 7: Approval, Cancellation, Attention

目标：实现 MVP human gate 和 exceptional notification。

交付物：

- `POST /api/orchestration/nodes/{nodeId}/approve`。
- `POST /api/orchestration/nodes/{nodeId}/retry`。
- `POST /api/orchestration/plans/{planId}/cancel`。
- Approval permission checker。
- approval audit events。
- cancellation propagation to active Agent Task。
- attention comment generation and audience selection。

验收标准：

- 只允许 authorized human actor approval action。
- agent assignee / executing agent / initiating agent 禁止 action。
- approval action writes audit event before Temporal Signal/Update or cancellation request。
- attention comment 只在 exceptional states 创建。
- successful run 不创建 default attention comment。
- attention audience 不包含 agent assignee，不 workspace broadcast。

测试重点：

- allowed owner/admin/creator/human assignee。
- denied agent actors。
- denied unrelated member。
- approval audit payload。
- cancel active task propagation。
- attention audience selection。

### Phase 8: API and Linear Orchestration Panel

目标：把 orchestration projection 暴露到 Issue Detail，先做线性节点列表。

交付物：

- `GET /api/issues/{issueId}/orchestration` contract。
- core API client / schema。
- `orchestration:updated` query invalidation。
- Issue Detail Linear Orchestration Panel。
- node list fields：status、reason、recommended action、attempts、summary、evidence count、Agent Task link。
- expanded events / evidence / artifacts / Signal Audit Events。
- Approval buttons by server-projected permission/action。

验收标准：

- 不做 DAG canvas、workflow designer、standalone page。
- server state 走 React Query。
- Zustand 只保存 local expanded/selected UI state。
- malformed or partial projection 不 white-screen。

测试重点：

- API read permission follows Issue visibility。
- approval mutation permission stricter than read。
- node summary render。
- expanded events/evidence render。
- Signal Audit Events only in expanded detail by default。
- approval buttons by permission。

### Phase 9: End-to-End Checkout Validation

目标：用最小真实路径验证 Temporal -> Eino -> daemon -> projection -> UI 闭环。

交付物：

- explicit local Temporal setup doc / command。
- checkout-specific smoke test script or E2E notes。
- happy path fixture。
- failure/retry/approval fixture。

验收标准：

- issue 可以启动 orchestration workflow。
- worker 独立运行。
- daemon 能执行 linked Agent Task。
- task outcome Signal 推进 workflow。
- issue detail 能看到 full trace。
- fail-closed、retry、approval、attention 至少有 focused tests。

测试重点：

- Go workflow/activity tests。
- backend API tests。
- frontend component/contract tests。
- one minimal E2E happy path。
- one failure path：malformed result -> retry -> approval or success。

## Implementation Order

推荐按以下顺序落地，避免大爆炸：

1. Phase 0 contract baseline。
2. Phase 1 Temporal skeleton + fail-closed。
3. Phase 2 projection migration + idempotent start。
4. Phase 3 projection activities。
5. Phase 5 DaemonBridge Signal happy path。
6. Phase 6 validation/retry/policy。
7. Phase 4 Eino reasoning activities。
8. Phase 7 approval/cancel/attention。
9. Phase 8 UI。
10. Phase 9 E2E checkout validation。

说明：Phase 4 可以和 Phase 5 局部并行，但真实 Eino 接入不应阻塞 Temporal/Signal skeleton。先用 mocked Eino output 打通 lifecycle，再接真实 provider。

## Required Test Matrix

Backend / workflow:

- Temporal unavailable fail closed。
- start idempotency。
- concurrent start race。
- workflow replay determinism。
- projection Activity idempotency。
- Signal matching advances workflow。
- duplicate/stale/mismatched Signal does not advance workflow。
- validate_result schema malformed / evidence insufficient。
- failed tests / risks route Approval Gate。
- retry max 2 attempts。
- approval permissions and audit events。
- cancellation propagation。
- attention audience selection。

Frontend / contracts:

- read endpoint follows Issue visibility。
- approval action endpoints require stricter human permission。
- Linear Orchestration Panel renders node summary。
- expanded detail renders events/evidence/artifacts。
- Signal Audit Events are expanded detail by default。
- malformed projection does not white-screen。
- React Query owns server state; Zustand only local UI state。

E2E:

- happy path：start -> analyze -> dispatch -> signal completed -> validate -> review -> summarize -> review handoff。
- fail closed：Temporal unavailable -> no direct Agent Task。
- failure path：malformed/evidence insufficient -> retry or approval。

## Cut Line

MVP complete means:

- Temporal-backed fixed workflow can orchestrate one Issue.
- Existing daemon Agent Task executes coding work.
- Agent Task outcome reaches Workflow through strict Signal contract.
- `orchestration_*` projection powers API and Issue Detail panel.
- deterministic validation, bounded retry, advisory review, approval, cancellation, and attention comments work under accepted constraints.

MVP is not complete if:

- lifecycle state is DB-owned instead of Temporal-owned;
- orchestration silently falls back to direct Agent Task;
- Workflow code performs projection side effects directly;
- failed tests or risks auto-retry code work;
- agents can approve their own work;
- UI only shows top-line status without node/evidence process detail.

## Deferred Work

- DAG / graph visualization。
- dynamic workflow topology。
- Eino graph execution。
- verifier agent。
- workspace-level policy table。
- structured risk taxonomy。
- success comment workspace setting。
- generalized orchestration CRUD。
- chat/autopilot/quick-create orchestration entry points。
