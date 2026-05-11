# Orchestration Kernel Correctness and Evidence Contract 设计规格

日期：2026-05-11

状态：Implementation planned

关联设计：`multica_ai_native_orchestration_kernel_design.md`
实施计划：`docs/superpowers/plans/2026-05-11-orchestration-kernel-correctness-evidence-plan.md`

## 1. 目的

本文档定义 Multica AI-native orchestration kernel 的下一阶段可靠性设计。

当前代码已经具备第一版闭环：创建 plan/node、调度 agent task、在 task 完成后进行 hard check、写入 artifact/event、支持 retry / waiting_human / cancel，并在 issue detail 展示最小 orchestration 状态。

下一步不应该优先做更复杂的 plan graph UI，也不应该马上引入 LLM planner。下一步应该先把当前闭环做扎实，让 kernel 的状态推进、证据要求和兼容边界都可验证。

本规格合并两个方向：

1. **Kernel Correctness First**：先保证状态机一致性、attempt 计数、event 完整性、feature flag 行为、legacy task 兼容、失败/重试/人工介入边界测试。
2. **Evidence Contract First**：把 structured result 从“agent 尽量输出 JSON”升级为正式协议，包括 `multica task complete --result`、schema validation、artifact 类型规范、`criteria_evidence` 强校验、invalid result 明确失败。

最终目标是：当 Multica 显示某个 node 或 issue 已完成时，服务端 kernel 能证明它为什么完成。

## 2. 当前基线

当前实现已经覆盖 orchestration MVP 的主要骨架：

- `server/migrations/081_orchestration_kernel.up.sql` 定义了 `orchestration_plan`、`orchestration_node`、`orchestration_edge`、`orchestration_event`、`orchestration_artifact`，并给 `agent_task_queue` 增加 orchestration 关联字段。
- `server/internal/service/orchestrator.go` 负责 plan 创建、node 创建、node dispatch、task completion callback、hard check evaluation、artifact/event 写入、retry、waiting_human、cancel，以及最终 issue status 推进。
- `server/internal/daemon/prompt.go` 在 task 带 orchestration context 时走 node-oriented prompt。
- `packages/core/api/client.ts` 已暴露 orchestration 查询、approve、retry、cancel API。
- `packages/views/issues/components/issue-detail.tsx` 已有最小 orchestration section。

当前缺口不是“没有 orchestration”，而是以下边界仍然偏隐式：

- agent result parsing 仍偏宽松，主要依赖普通 task output 中的 JSON。
- 状态转移由 service 流程隐式表达，缺少显式不变量和测试矩阵。
- attempt 计数和 retry eligibility 需要精确定义。
- event 完整性目前更像约定，缺少每个状态变化必须落 event 的强约束。
- feature flag 行为必须成为硬兼容边界。
- legacy completion payload 需要继续兼容，但不能绕过 evidence 模型。

## 3. 设计目标

### 3.1 Kernel Correctness

Kernel 必须满足以下不变量：

1. `task completed` 不等于 `node completed`。
2. node 只能在 evaluator 通过或人工审批通过后进入 `completed`。
3. plan 只能在所有必要 node 均为 `completed` 或 `skipped` 后进入 `completed`。
4. issue 只能在 plan 完成后由 kernel 推进到 `done`。
5. evaluator 失败后只能进入三类结果：retry、waiting_human、failed。
6. 每个关键状态变化必须写入 `orchestration_event`。
7. workspace 未开启 `orchestration_enabled` 时，原 `EnqueueTaskForIssue` 路径行为不变。
8. legacy task completion 对非 orchestration task 继续兼容；对 orchestration task 必须进入 evidence evaluation，不能直接视为完成。

### 3.2 Evidence Contract

Kernel 必须把 agent 输出视为不可信输入：

1. orchestration task completion 接收结构化 result payload，并在服务端做 schema validation。
2. invalid structured result 必须产生明确 evaluation failure 和 event。
3. artifact 必须归一化为受控类型集合。
4. issue 有 acceptance criteria 时，`criteria_evidence` 必须覆盖每条 criterion。
5. 不同 node 类型有不同证据要求：
   - `implement`：必须有 summary，且必须有 diff / changed_files / file artifact。
   - `test`：必须有 summary 和 test_result。
   - `review`：必须有 summary 和 review_result。
   - `design`：必须有 summary，以及 decision / design summary 类证据。
6. failed test result 不能通过 evaluation。
7. low confidence 或需要人工判断的结果必须由 evaluator 显式进入 `waiting_human`，不能由 parser 偶然触发。

## 4. 非目标

本阶段不做：

- LLM planner。
- LLM evaluator。
- multi-agent assignment strategy。
- 完整 DAG plan graph UI。
- workflow DSL 或 policy DSL。
- deploy node 自动化。
- 把 daemon 改造成 orchestrator。
- 重写现有 task queue。

## 5. 总体设计

本阶段把职责边界拆清楚：

```text
TaskService
  负责 task lifecycle 和 daemon-facing execution。

Orchestrator
  负责 plan/node 状态、dispatch、evaluation decision、event、artifact、retry、issue status。

Result Protocol
  负责 structured result shape、schema validation、artifact normalization、legacy compatibility。
```

核心变化是：orchestration completion 不再等价于“从 agent 输出里尽量解析一个 JSON”。完成路径必须先生成一个经过验证的 `AgentStructuredResult`，再把这个 typed result 交给 evaluator。

```text
daemon / CLI completion
  -> raw payload
  -> result protocol parser
  -> validation outcome
  -> artifact normalization
  -> hard-check evaluator
  -> kernel state transition
  -> event log
```

## 6. 状态机不变量

### 6.1 Plan 状态

计划状态仍为：

```text
planning -> ready -> running -> completed
                         |-> waiting_human -> running
                         |-> failed
                         |-> cancelled
```

当前实现可以在一个事务中创建 plan、node 并 dispatch 第一个 task，因此 plan 直接进入 `running` 是可接受的。但 event 必须让生命周期可审计：

- `plan.created`
- `node.created`
- `node.dispatched`

Plan 不变量：

- `completed`、`failed`、`cancelled` 是终态。
- `waiting_human` 表示至少一个活跃 node 需要人工输入或审批。
- `running` 表示至少一个 node 可调度、已调度、运行中或正在 evaluation。
- plan 不允许在仍有 `pending` / `ready` / `dispatched` / `running` / `evaluating` node 时完成。

### 6.2 Node 状态

节点状态仍为：

```text
pending -> ready -> dispatched -> running -> evaluating -> completed
                                             |              |-> ready
                                             |              |-> waiting_human
                                             |              |-> failed
                                             |-> failed
                                             |-> cancelled
```

Node 不变量：

- `pending` node 必须等待所有 incoming dependency node 完成或跳过。
- `ready` node 只有在 policy、agent availability、capacity 满足时才能 dispatch。
- `dispatched` 表示已经存在对应 `agent_task_queue` row。
- `running` 表示 daemon 已 start task。
- `evaluating` 必须出现在 task completion 之后、evaluator decision 之前。
- `completed` 只能由 kernel 写入。
- `waiting_human` 必须有原因 event。
- `failed` 必须有 evaluation 或 runtime failure reason。

### 6.3 Task 状态

Task 状态继续由现有 task queue 管理。

Kernel 不变量：

```text
task completed != node completed
```

task completion callback 可以写 artifact 和 event，但不能直接推进 issue status。issue status 只能在 plan completion invariant 成立后推进。

## 7. Attempt 计数和 Retry 语义

attempt 计数属于 node，因为一个 node 可以产生多次 task attempt。

### 7.1 定义

- `attempt_count`：该 node 已 dispatch 的 task attempt 数。
- `max_attempts`：该 node 允许的最大 attempt 数。
- 当 dispatch 下一次 attempt 之前满足 `attempt_count < max_attempts` 时，允许 retry。

### 7.2 Dispatch 行为

dispatch node task 必须在同一个事务中完成：

1. `orchestration_node.attempt_count += 1`。
2. 创建新的 `orchestration_run_id`。
3. 创建 `agent_task_queue` row。
4. 设置 node status 为 `dispatched`。
5. 写入 `node.dispatched` event，payload 包含 `task_id`、`run_id`、`attempt_count`、`max_attempts`。

### 7.3 Task Failed 行为

当 task 在未产出 result 前失败：

1. 写入 `task.failed`。
2. 如果允许 retry：node 回到 `ready`，写入 `node.retry_scheduled`，dispatch 下一次 task。
3. 如果 retry 耗尽：node 进入 `failed`，plan 进入 `failed`，写入 `node.failed` 和 `plan.failed`。

### 7.4 Evaluator Failed 行为

当 task completed 但 evaluator 不通过：

1. 写入 `task.completed`。
2. 写入 `evaluation.failed`。
3. 如果允许 retry：node 回到 `ready`，写入 `node.retry_scheduled`，dispatch 下一次 task。
4. 如果需要人工介入：node 和 plan 进入 `waiting_human`。
5. 如果 retry 耗尽且不需要人工介入：node 和 plan 进入 `failed`。

### 7.5 边界测试

必须覆盖：

- 首次 dispatch 后 `attempt_count = 1`。
- 一次 retry 后 `attempt_count = 2`。
- `max_attempts = 2` 时不能创建第三次 task。
- UI manual retry 默认也遵守 `max_attempts`。
- task runtime failure 和 evaluator failure 都消耗 attempt。

## 8. Event 完整性

Event 是 kernel 的审计账本。本阶段需要标准化 event 名称和 payload。

### 8.1 必须事件

Plan-level：

- `plan.created`
- `plan.running`
- `plan.waiting_human`
- `plan.completed`
- `plan.failed`
- `plan.cancelled`

Node-level：

- `node.created`
- `node.ready`
- `node.dispatched`
- `node.running`
- `node.evaluating`
- `node.completed`
- `node.retry_scheduled`
- `node.waiting_human`
- `node.failed`
- `node.cancelled`

Task-level：

- `task.started`
- `task.completed`
- `task.failed`
- `task.cancelled`

Evaluation-level：

- `evaluation.started`
- `evaluation.passed`
- `evaluation.failed`
- `evaluation.invalid_result`
- `evaluation.waiting_human`

Artifact-level：

- `artifact.recorded`

### 8.2 Event Payload Contract

每个 event payload 应尽量包含：

- `plan_id`
- `node_id`，如适用
- `task_id`，如适用
- `run_id`，如适用
- `attempt_count`，如适用
- `max_attempts`，如适用
- `previous_status`
- `next_status`
- `reason`
- `evaluator_mode`，如适用
- `artifact_ids`，如适用

event payload 不应该存大日志或完整命令输出。大内容应进入 artifact。

### 8.3 Event 写入规则

状态更新和对应 event 必须在同一个数据库事务内完成。任何关键状态变化没有 event，视为 correctness bug。

## 9. Feature Flag 行为

`workspace.settings.orchestration_enabled` 是兼容边界。

### 9.1 Flag Off

当 `orchestration_enabled` 不存在或为 false：

- agent-assigned issue 继续走 legacy task path。
- 不创建 orchestration plan。
- 不创建 orchestration node。
- task context 不要求 orchestration 字段。
- daemon prompt 继续走 issue-oriented prompt。
- legacy task completion 行为保持兼容。

### 9.2 Flag On

当 `orchestration_enabled` 为 true：

- agent-assigned issue 创建 orchestration plan。
- simple planner 创建 node。
- dispatcher 创建带 orchestration context 的 agent task。
- daemon claim response 带 orchestration context。
- daemon 使用 node-oriented prompt。
- task completion 必须经过 orchestrator evaluator 后才能影响 node / plan / issue。

### 9.3 测试要求

必须证明：

- flag off：创建 legacy task，不创建 plan。
- flag on：创建 plan + node + orchestration task，不创建直接执行整个 issue 的 legacy task。
- flag 切换只影响新 assignment；既有 task 按创建时模式继续执行。

## 10. Structured Result Protocol

### 10.1 Result Shape

Canonical orchestration result：

```json
{
  "status": "completed",
  "summary": "Implemented the requested behavior and verified it.",
  "artifacts": [
    {
      "type": "diff",
      "uri": "",
      "content": {
        "changed_files": ["server/internal/service/orchestrator.go"]
      },
      "metadata": {}
    }
  ],
  "changed_files": ["server/internal/service/orchestrator.go"],
  "test_result": {
    "command": "go test ./server/internal/service",
    "passed": true,
    "summary": "All tests passed"
  },
  "claims": ["Node state transitions are evented."],
  "criteria_evidence": [
    {
      "criterion": "Task completion does not directly complete the issue.",
      "evidence": "Issue status update happens only after plan completion.",
      "artifact_refs": []
    }
  ],
  "risks": [],
  "next_actions": [],
  "confidence": 0.86
}
```

### 10.2 Status Values

允许值：

- `completed`
- `failed`
- `blocked`
- `needs_human`

语义：

- `completed`：agent 认为当前 node 完成，并提交证据。
- `failed`：agent 尝试执行但失败。
- `blocked`：agent 因外部条件或缺失依赖无法继续。
- `needs_human`：agent 需要人工输入或审批。

即使 agent 提交 `completed`，evaluator 仍可拒绝。

### 10.3 Artifact Types

允许 artifact 类型：

- `diff`
- `file`
- `log`
- `test_result`
- `pr`
- `decision`
- `review_result`
- `command_output`
- `summary`

归一化规则：

- `changed_files` 生成或补充 `diff` artifact。
- `test_result` 生成或补充 `test_result` artifact。
- `summary` 生成 `summary` artifact。
- structured result 中空 artifact type 非法。
- unknown artifact type 对 orchestration task 非法。

### 10.4 Criteria Evidence

`criteria_evidence` 必须是对象数组：

```json
{
  "criterion": "The exact or normalized criterion text.",
  "evidence": "Concrete evidence that satisfies the criterion.",
  "artifact_refs": ["artifact-id-or-local-ref"]
}
```

规则：

- source issue 有 acceptance criteria 时，每条 criterion 都必须有 evidence。
- source issue 没有 acceptance criteria，但 node output contract 要求 `criteria_evidence` 时，至少需要一条 evidence 连接 node objective 和执行结果。
- 空 evidence string 失败。

## 11. CLI Completion Protocol

### 11.1 命令

新增或强化：

```bash
multica task complete --task-id <task_id> --result <result.json>
```

行为：

1. 读取 `result.json`。
2. 作为 structured result payload 提交。
3. 如果 task 携带 orchestration context，服务端按 orchestration result schema 校验。
4. 服务端将 normalized artifact 和 evaluation event 入库。

### 11.2 权限

该命令必须复用 daemon/task completion 的权限边界。普通 workspace member 不能仅凭 task ID 完成任意 agent task。

### 11.3 Legacy Completion

非 orchestration task 继续支持 legacy completion。

对 orchestration task：

- 如果 daemon 提交 legacy `{ "output": "..." }`，result protocol parser 可以在 compatibility mode 下转换为最小 structured result。
- compatibility mode 仍必须经过 evaluator。只有 summary 的 legacy payload 不能通过需要 artifact 或 criteria evidence 的 implement node。
- event stream 应记录 `evaluation.invalid_result` 或 `evaluation.failed`，reason 可为 `legacy_payload_missing_evidence`。

这能兼容旧 daemon，又不会让旧 payload 绕过 kernel。

## 12. Schema Validation

### 12.1 服务端 Validation

structured result 必须在服务端 evaluation 前校验。

建议结果结构：

```go
type ResultValidation struct {
    Valid bool
    Result AgentStructuredResult
    Errors []ValidationError
    CompatibilityMode bool
}
```

invalid payload 处理：

1. 写入 `evaluation.invalid_result`。
2. 记录 compact validation errors。
3. 将 evaluation 视为 failed。
4. 按 retry / waiting_human / failed 规则推进。

### 12.2 Validation 规则

通用必填：

- `status`
- `status = completed` 时必须有 `summary`
- `confidence` 如存在必须在 0 到 1 之间

证据规则：

- `completed` 且 summary 为空，invalid。
- unknown artifact type，invalid。
- failed test result 是 valid payload，但 evaluation failed。
- `failed` 可以没有 artifact，但必须有 summary 或 risks。
- `blocked` 必须有 summary 或 risks。
- `needs_human` 必须有 summary，并有 next action 或 risk。

## 13. HardCheckEvaluator

HardCheckEvaluator 应从松散 helper function 提升为显式组件。

建议接口：

```go
type Evaluator interface {
    Evaluate(ctx context.Context, input EvaluationInput) (EvaluationResult, error)
}

type EvaluationInput struct {
    Plan db.OrchestrationPlan
    Node db.OrchestrationNode
    Task db.AgentTaskQueue
    Result AgentStructuredResult
    Validation ResultValidation
    AcceptanceCriteria []AcceptanceCriterion
}

type EvaluationResult struct {
    Pass bool
    Score float64
    Reason string
    FailedCriteria []string
    MissingArtifacts []string
    Risks []string
    RecommendedAction string
}
```

初始 evaluator mode：

- `hard_check`
- `hard_check_with_human_review_on_low_confidence`

Hard check 规则：

- validation invalid 必然 failed。
- `status = failed` failed，可 retry。
- `status = blocked` 进入 waiting_human 或 blocked。
- `status = needs_human` 进入 waiting_human。
- missing summary failed。
- implement node 需要 `diff`、`file` 或 `changed_files`。
- test node 需要 `test_result`。
- review node 需要 `review_result`。
- design node 需要 `decision` 或带 evidence 的 design summary。
- failed tests failed。
- acceptance criteria 缺 evidence failed。

## 14. Orchestrator 事务边界

关键状态变化必须保持 transactional。

### 14.1 Dispatch Transaction

同一个事务内完成：

1. 增加 attempt count。
2. 创建 task。
3. 设置 node `dispatched`。
4. 写 `node.dispatched`。

### 14.2 Completion Evaluation Transaction

同一个事务内完成：

1. 设置 node `evaluating`。
2. 写 `node.evaluating`。
3. 存储 normalized artifacts。
4. 写 `artifact.recorded`。
5. 写 `task.completed`。
6. 写 evaluation event。
7. 应用 evaluator decision。
8. 写最终 node / plan event。

如果 node completed 后马上 dispatch downstream node，当前可以保留在同一事务内。若后续 dispatch 逻辑变复杂，应改为 outbox-style follow-up，但 event ordering 必须保持可审计。

## 15. API 与 UI 影响

### 15.1 API

现有 API 可保持：

- `GET /api/issues/:id/orchestration`
- `POST /api/orchestration/nodes/:node_id/approve`
- `POST /api/orchestration/nodes/:node_id/retry`
- `POST /api/orchestration/plans/:plan_id/cancel`

潜在补充：

- `GET /api/orchestration/plans/:plan_id/events`
- `GET /api/orchestration/plans/:plan_id/artifacts`

本阶段 API 变更应保持最小，除非 CLI structured result submission 或测试需要。

### 15.2 UI

现有 issue-detail orchestration section 足够支撑本阶段。

允许做的小改动仅限暴露 correctness 信息：

- latest evaluator reason。
- current attempt count。
- invalid result reason。
- waiting-human reason。

不做完整 plan graph UI。

## 16. 测试计划

### 16.1 Backend Unit Tests

新增或补齐：

- valid structured result 通过 validation。
- unknown artifact type validation failed。
- invalid confidence validation failed。
- completed result 空 summary validation failed。
- legacy payload 只在 compatibility mode 转换。
- implement node 无 artifact / changed_files 失败。
- test node failed test result 失败。
- source issue 有 acceptance criteria 时必须有 criteria evidence。
- `needs_human` result 进入 waiting_human。
- `blocked` result 不能完成 node。

### 16.2 Orchestrator Integration Tests

新增或补齐：

- feature flag off 走 legacy task path。
- feature flag on 创建 plan、node、orchestration task。
- task start 将 node 置为 running 并写 event。
- task completion 先进入 evaluating，再应用 evaluator decision。
- evaluator success 完成 node 和 plan。
- plan completed 后 issue 进入 done。
- evaluator failure 在 attempts 未耗尽时 schedule retry。
- retry 增加 attempt count。
- retry exhausted 后 node 和 plan failed。
- manual approve 完成 waiting-human node 并 dispatch downstream node。
- cancel plan 写 event 且阻止后续 dispatch。

### 16.3 Frontend/Core Tests

新增或调整：

- API response parsing 能容忍缺失 orchestration 字段。
- issue detail 能展示 failed validation reason。
- retry / approve / cancel button 只在合法状态出现。
- mutation 后 query invalidation 能刷新 orchestration view。

### 16.4 CLI Tests

新增：

- `multica task complete --result result.json` 能读取合法 JSON。
- result file 缺失时本地失败。
- invalid JSON 本地失败。
- orchestration result payload 原样到达服务端。
- non-orchestration task legacy completion 仍可用。

## 17. 实施阶段

### Phase 1：Document Invariants and Add Tests

- 补状态机 invariant tests。
- 补 feature flag tests。
- 补 attempt / retry boundary tests。
- 补 event completeness assertions。

### Phase 2：Extract Result Protocol

- 新增 result validation / normalization 模块。
- 将当前宽松 parser 放入 compatibility mode。
- 覆盖 schema failure 和 legacy payload tests。

### Phase 3：Harden Evaluator

- 抽出 HardCheckEvaluator。
- 强化 node-type evidence rules。
- 强化 criteria evidence rules。
- 补 invalid-result 和 waiting-human event 覆盖。

### Phase 4：CLI Structured Result

- 新增或强化 `multica task complete --result`。
- orchestration task completion 走 structured result validation。
- non-orchestration task legacy completion 保持兼容。

### Phase 5：Minimal UI Evidence Feedback

- 展示 validation / evaluator reason。
- 展示准确 attempt count。
- 保持 UI 紧凑，不做 graph view。

## 18. 验收标准

完成标准：

1. feature flag off 时 agent-assigned issue 保持 legacy 行为。
2. feature flag on 时 agent-assigned issue 进入 plan/node/task path。
3. task completion 不会绕过 evaluator 直接完成 node。
4. plan 只有在所有必要 node completed/skipped 后才能 completed。
5. issue `done` 只在 plan completed 后由 kernel 写入。
6. 每次 dispatch node task，attempt count 精确加一。
7. retry 在 `max_attempts` 处停止。
8. 主流程每个状态变化都有同事务 event。
9. 服务端存在 structured result schema validation。
10. invalid orchestration result 产生 evaluation failure event。
11. implement / test / review / design node 强制各自证据要求。
12. source issue 有 acceptance criteria 时必须有 evidence。
13. non-orchestration task legacy completion 继续可用。
14. legacy orchestration payload 不能绕过 evidence checks。
15. backend tests 覆盖 success、failure、retry、waiting human、cancel、compatibility。
16. frontend 继续 defensive parse orchestration API response。
17. `make test` 和 `pnpm typecheck` 通过；如有无关失败，必须记录精确原因。

## 19. Open Decisions

### 19.1 Manual Retry Override

推荐默认：manual retry 遵守 `max_attempts`。

理由：保持 attempt 语义简单。若后续需要 operator override，再新增带审计事件的特权 endpoint，例如 `node.retry_override_requested`。

### 19.2 Blocked Node Status

推荐默认：第一阶段把 agent-reported `blocked` 映射为 `waiting_human`，除非 kernel 能观察到明确的自动 unblock 条件。

理由：多数初期 blocked 状态本质是缺少人工输入，而不是系统可自动等待的外部条件。

### 19.3 Raw Result 存储

推荐默认：小型 raw result 可放在 `task.completed` 或 `evaluation.*` event payload；大型输出应存为 artifact。

理由：event 用于 timeline 和调试，不应该变成无界日志存储。

## 20. 实现注意事项

保持现有架构边界：

- shared frontend code 仍放在 `packages/core` 和 `packages/views`。
- shared views 不引入 `next/*`。
- 不把 server state 复制到 Zustand。
- `packages/core/api/client.ts` 继续使用 parse-with-fallback 风格处理 API response，不裸 cast。
- Go 注释如需新增，使用英文。
- 除非 correctness 被现有耦合阻塞，否则不做大范围目录重构。

最重要的约束是：kernel 始终拥有 workflow truth。Agent 可以报告执行结果、证据、置信度和风险，但不能决定 plan 是否完成。
