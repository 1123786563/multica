# AI-native Orchestration Kernel v1 设计与实施计划

## 状态

已确认需求，待实施。

相关文档：

- `CONTEXT.md`：领域术语和边界
- `docs/adr/0001-ai-native-orchestration-kernel-v1-boundary.md`：v1 架构取舍

## 目标

在 Multica 现有 Issue、Agent、Runtime、Skill、daemon、Agent Task 模型之上，增加一个服务端拥有的 AI-native orchestration kernel。

v1 的核心目标不是重写执行层，而是补齐：

- Issue 级编排生命周期
- 节点状态机
- 持久 Kernel Event
- 结构化 Node Evidence
- node 级恢复和重试
- 条件式 human approval
- Issue Detail 内的 Decision Panel

## 非目标

v1 明确不做：

- 不替换 `agent_task_queue`
- 不新增 kernel-to-daemon 协议
- 不直接运行 agent CLI
- 不做完整并行 DAG scheduler
- 不做 LLM planner
- 不做 workspace-wide 自动 skill selection
- 不默认派发 verifier agent
- 不新增独立 Orchestration 页面
- 不提供 run/node/event/evidence 通用 CRUD API
- 不自动把 Issue 标记为 `done`

## v1 边界

Orchestration Kernel 是 Issue 上方的服务端决策层。它决定下一步应该发生什么、记录为什么发生、校验证据是否足够，然后通过现有 Agent Task 执行。

首版入口只绑定 Issue：

- 一个 Issue 同一时间最多一个 Active Run
- Chat、Autopilot、Quick Create 暂不进入 kernel
- workspace 未开启 orchestration 时，继续走现有 direct issue-to-task enqueue 路径
- workspace 开启 orchestration 后，agent-assigned issue 先进入 kernel，再由 kernel 派发 Agent Task

## 核心模型

### Orchestration Run

Issue-scoped 编排实例。

建议状态：

- `running`
- `waiting_for_approval`
- `succeeded`
- `failed`
- `cancelled`

约束：

- 同一 Issue 同一时间最多一个非终态 run
- 重复触发必须复用现有 Active Run
- 终态 run 保留历史，可由手动 rerun 创建新 run

### Orchestration Node

Run 内的编排节点。Node State 独立于 Agent Task status。

v1 node type：

- `plan`
- `execute`
- `verify`

建议 node state：

- `pending`
- `ready`
- `dispatched`
- `running`
- `waiting_for_approval`
- `succeeded`
- `failed`
- `skipped`
- `cancelled`

v1 schedule 是最小图模型，但运行语义是线性主链：

```text
plan -> execute -> verify
```

`approval` 和 `retry` 不是 node type，而是 node state / Kernel Event / user action。

### Agent Task

现有执行单元保持不变。

execute node 通过 Runtime Adapter 派发 Agent Task：

- node 记录 linked `task_id`
- daemon 继续使用现有 claim/start/message/complete/fail API
- kernel 读取 Agent Task outcome 后推进 node/run

同一个 `run_id + node_id + attempt` 最多绑定一个 Agent Task。重复触发或 recovery scan 必须复用已有 task。

### Kernel Event

持久审计事实流，是 recovery、observability、audit 的 source of truth。

WebSocket 只做提交后通知，不是状态来源。

v1 event type 使用稳定枚举，建议包括：

- `run_created`
- `run_started`
- `run_waiting_for_approval`
- `run_succeeded`
- `run_failed`
- `run_cancelled`
- `node_created`
- `node_ready`
- `node_dispatched`
- `node_started`
- `node_succeeded`
- `node_failed`
- `node_waiting_for_approval`
- `node_retried`
- `task_linked`
- `evidence_recorded`
- `approval_action_recorded`

每次 run/node 状态变化必须和对应 Kernel Event 在同一个 DB 事务提交。

### Node Evidence

结构化证据独立于 `agent_task_queue.result` 持久化。

建议字段：

- `run_id`
- `node_id`
- `task_id`
- `kind`
- `payload`
- `created_at`

verify、recovery、Decision Panel 读取 Node Evidence，而不是反复解析原始 task output。

## Result Schema

Agent Task 用于 orchestration 时必须提供版本化结构化结果。

v1 schema：

```json
{
  "schema_version": 1,
  "summary": "",
  "changed_files": [],
  "artifacts": [],
  "tests": [],
  "risks": []
}
```

规则：

- 缺失或 malformed schema 不代表 task failed
- task 可以保持 `completed`
- 对应 node 不能直接 `succeeded`
- verify 应输出 `evidence_insufficient`
- 未知 `schema_version` 降级为 evidence insufficient

## Verification

verify node v1 使用 server-owned hard checks，不默认派发 verifier agent。

最低校验：

- execute node 有 linked `task_id`
- linked Agent Task 已完成
- result schema 有效
- `summary` 非空
- 必要 `changed_files`、`artifacts`、`tests` 字段存在
- 没有未处理失败状态
- Node Evidence 已记录
- `risks` 为空或已进入 approval

成功后：

- verify node `succeeded`
- run `succeeded`
- Issue 可推进到 `in_review`
- 不自动标记 Issue `done`

## Retry 与 Recovery

### Node Retry Policy

v1 默认最多 2 次 node attempt。

可自动重试：

- runtime recovery
- timeout
- 结构化 result 缺失
- result schema malformed
- evidence insufficient 且属于输出格式问题

不应自动重试：

- 风险非空
- 测试失败
- 结果不可验证
- 破坏性操作
- retry exhausted

这些情况进入 Approval Gate 或 failed。

### Node Recovery

恢复粒度是 node，不是整个 run。

规则：

- completed node 不重跑
- Kernel Events 和 Node Evidence 保留
- server restart 后根据 run/node/event/task_id 和 Agent Task 状态补齐推进
- 只有 run 结构损坏或无法判定安全状态时，run 才进入 failed

### Run Advancement

不引入长期 scheduler worker。

这些入口同步调用 `AdvanceRun()`，推进到下一个阻塞点：

- run 创建
- Agent Task completed
- Agent Task failed
- approval action
- manual retry

另加轻量 recovery scan，修复 task 已完成但 node 未推进等断点。

`AdvanceRun()` 必须持有 run 级 DB lock，例如对 `orchestration_run` 行加 `FOR UPDATE`。

## Human Approval

Approval Gate 是条件式暂停点，不默认插入每个 run。

进入 approval 的原因：

- risks 非空
- tests failed
- unverifiable result
- destructive operation
- retry exhausted
- explicit policy

v1 Approval Action：

- `approve`
- `retry`
- `request_changes`
- `cancel`

`request_changes` 记录为 Kernel Event，并作为下一次 node attempt 的 Orchestration Context，不自动改写 Issue 描述。

### 权限

读取 orchestration 跟随 Issue read permission。

approval action 只允许：

- workspace owner
- workspace admin
- Issue creator
- Issue human assignee

agent assignee 不能批准自己的 orchestration。

## Runtime Adapter 与 Orchestration Context

Runtime Adapter 只做生命周期桥接：

```text
node -> Agent Task -> node outcome
```

不新增 daemon 协议，不直接运行 CLI。

派发 Agent Task 时，kernel 只追加小型 Orchestration Context：

- `run_id`
- `node_id`
- `node_type`
- `attempt`
- `expected_result_schema`
- `prior_evidence_summary`
- `change_request`

现有 daemon prompt、Issue 获取、repo checkout、comment workflow、skill 注入继续保留。

## Skill Discovery

v1 只使用已绑定到 Agent 的 skills。

kernel 可以记录 node 当时可用的 skill context，但不做：

- workspace-wide skill search
- runtime local skill 自动选择
- 动态 skill planner

## Issue 状态与通知

Issue status 与 Run State 独立。

有限联动：

- run started 时，可将 agent-assigned Issue 从 `todo/backlog` 推到 `in_progress`
- verify succeeded 后，可将 Issue 推到 `in_review`
- 不自动 `done`

取消联动：

- 取消 run 会取消 active nodes 和 linked active Agent Tasks
- Issue 变为 `cancelled` 时取消 Active Run
- completed task、Kernel Events、Node Evidence 保留

评论策略：

- 成功不默认评论
- `waiting_for_approval`、`retry_exhausted`、`run_failed` 等需要人类注意时创建 Attention Comment

通知目标：

- Issue creator
- human assignee
- subscribers

不 workspace-wide 广播，不自动 @mention agent assignee。

## API Surface

v1 只提供 Issue-scoped 查询和 action。

建议：

```text
GET  /api/issues/{id}/orchestration
POST /api/issues/{id}/orchestration/actions
```

`GET` 返回：

- latest/active run
- nodes
- node summaries
- events
- evidence summary
- linked task ids
- reason codes
- recommended actions
- permission flags

`POST actions` 支持：

- `approve`
- `retry`
- `request_changes`
- `cancel`

不暴露通用 run/node/event/evidence CRUD。

## WebSocket 与前端状态

v1 只新增粗粒度 refresh event：

```text
orchestration:updated
```

payload 建议：

- `issue_id`
- `run_id`
- `changed_at`

前端收到后 invalidate issue orchestration query。

前端数据所有权：

- run/node/event/evidence 是 server state，走 React Query
- Zustand 只保存 UI state，例如 expanded nodes、selected panel
- `packages/views` 不放 store
- web/desktop 不各自复制 orchestration 逻辑

## Decision Panel

首版 UI 落在现有 Issue Detail。

默认展示 node-centered summary：

- node status
- reason_code
- recommended_action
- latest summary
- attempts
- evidence count
- linked task status

raw Kernel Events、Node Evidence、task messages 作为展开详情。

reason/action 由服务端派生，前端只负责展示和本地化。

建议 reason code：

- `pending_dependencies`
- `ready_to_run`
- `running`
- `waiting_for_approval`
- `evidence_insufficient`
- `retry_scheduled`
- `runtime_failed`
- `verification_failed`
- `risk_requires_approval`
- `retry_exhausted`
- `completed`
- `cancelled`

建议 recommended action：

- `none`
- `wait`
- `inspect_evidence`
- `retry`
- `approve`
- `request_changes`
- `cancel`
- `update_runtime`

## Persistence Plan

v1 四张核心表：

- `orchestration_run`
- `orchestration_node`
- `orchestration_event`
- `orchestration_evidence`

继续复用：

- `workspace.settings`：orchestration feature flag
- `agent_task_queue`：执行生命周期
- `task_message`：agent live output
- Issue/comment/activity/subscriber：产品上下文和通知

## Implementation Plan

### Phase 1: Persistence and generated DB layer

- 增加四张 orchestration migration
- 添加 active run 约束
- 添加 node task link 和 attempt/idempotency 约束
- 添加 event type、node type、node state、run state check constraints
- 添加 sqlc queries
- 运行 sqlc 和 Go 编译检查

### Phase 2: Kernel service state machine

- 新增 server-owned orchestration service
- 实现 create/reuse active run
- 实现 deterministic plan generation
- 实现 `AdvanceRun()` 和 run lock
- 实现 node dispatch idempotency
- 实现 task completion/failure 回写
- 实现 hard-check verification
- 实现 node retry policy
- 实现 recovery scan

### Phase 3: Integration with existing task lifecycle

- workspace orchestration flag 生效
- agent-assigned Issue 走 kernel path
- 未启用 workspace 继续 direct enqueue
- `CompleteTask` / `FailTask` 后触发 run advancement
- Issue cancel 级联 run cancellation
- run success 推进 Issue 到 `in_review`

### Phase 4: API and client contracts

- 添加 issue-scoped orchestration read endpoint
- 添加 approval action endpoint
- 添加 core API client method 和 zod schema
- 添加 malformed response fallback tests
- 新增 `orchestration:updated` WS event 和 query invalidation

### Phase 5: Issue Detail Decision Panel

- 在 `packages/views` 的 Issue Detail 内渲染 Decision Panel
- 展示 node summaries、reason、recommended action、attempt、evidence count
- 展开显示 events/evidence/task link
- approval action buttons 根据服务端 permission/action 渲染
- web/desktop 复用同一 shared view

### Phase 6: Notifications and attention comments

- waiting approval / retry exhausted / failed 生成 Attention Comment
- 通知 Issue creator、human assignee、subscribers
- 避免 agent mention loop
- 成功态不默认评论

## Test Plan

优先 Go service/state-machine tests。

必须覆盖：

- active run 幂等创建
- deterministic plan/execute/verify node 创建
- run lock 防并发推进
- execute node dispatch 只创建一个 Agent Task
- task completed 后 node/run 推进
- malformed Result Schema 进入 evidence insufficient
- evidence insufficient 自动 retry 一次
- risks 非空进入 approval
- approval approve/retry/request_changes/cancel
- retry exhausted
- server restart/recovery scan 补齐状态
- Issue cancel 级联 run/task cancel
- Kernel Event 与状态同事务一致
- Node Evidence 保留跨 retry

API tests：

- read endpoint 权限跟随 Issue
- approval action 权限更严格
- malformed payload fallback
- unknown enum fallback

Frontend tests：

- Decision Panel 渲染 node summary
- reason/recommended action 本地展示
- missing fields 不 white-screen
- approval buttons 按权限显示
- `orchestration:updated` invalidate query

E2E 最小覆盖：

- happy path：Issue assigned -> run -> execute task -> verify -> in_review
- failure recovery path：malformed result -> retry -> approval 或 success

## Open Follow-ups

这些不阻塞 v1，但后续需要单独决策：

- verifier agent 何时引入
- LLM planner 何时替换 Default Plan Node
- parallel DAG 何时启用
- workspace-level orchestration policy 是否需要独立表
- success comment 是否做 workspace 配置
- risk taxonomy 是否从字符串升级为结构化 severity/category
