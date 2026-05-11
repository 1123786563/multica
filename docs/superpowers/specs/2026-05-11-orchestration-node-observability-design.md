# Orchestration Node Observability Contract 设计规格

日期：2026-05-11

状态：Draft for review

关联设计：
- `multica_ai_native_orchestration_kernel_design.md`
- `docs/superpowers/specs/2026-05-11-orchestration-kernel-correctness-evidence-design.md`

## 1. 目的

本文档定义 Multica orchestration kernel 的下一阶段可观测性设计。

当前代码已经具备 orchestration MVP：plan/node 创建、task dispatch、structured result hard check、artifact/event 写入、retry / waiting_human / cancel，以及 issue detail 中的最小 orchestration 展示。

下一阶段不应优先扩展 planner、DAG UI 或更复杂的调度抽象，而应先解决一个更直接的问题：

> 当 orchestration node 进入 running、waiting_human、failed 或 completed 时，普通使用者和维护者都应该能直接看懂系统为什么这样判定，以及接下来应该做什么。

本设计的核心产物是一个由服务端统一推导的 `node_summary` / `node observability contract`。它不是新的事实数据模型，而是对现有 node、event、artifact、evaluation 结果做稳定解释，供 Web / Desktop UI 直接消费。

## 2. 问题定义

当前 orchestration 读模型存在三个缺口：

1. **状态可见，但原因不稳定**
   - UI 可以知道 node 是 running、waiting_human、failed 或 completed。
   - 但“为什么处于这个状态”往往要靠 event payload 或 agent 文本间接判断。

2. **动作入口不清晰**
   - 用户很难快速知道当前应当 retry、approve、provide input，还是无需动作。
   - 同一个 `waiting_human` 状态可能对应完全不同的操作语义。

3. **调试信息组织维度不对**
   - event timeline 适合审计，不适合作为日常主视图。
   - 真正的工作单元是 node，因此维护者视图应该围绕 node 组织，再下钻 attempt 和 timeline。

## 3. 设计目标

### 3.1 统一解释层

为每个 orchestration node 提供统一的服务端摘要对象，回答：

- 现在处于什么状态
- 为什么处于这个状态
- 下一步最推荐的动作是什么
- 系统还有没有自动恢复空间

### 3.2 全覆盖而非只覆盖异常态

第一版不只解释失败态，而是统一覆盖：

- `pending` / `ready`
- `running`
- `waiting_human`
- `failed`
- `completed`

避免出现“失败态有解释、完成态仍是黑箱”的模型割裂。

### 3.3 双层可观测性

同一套 contract 同时服务两类视图：

- **Decision Panel**：给普通使用者与维护者第一眼判断
- **Node Debug Panel**：给维护者下钻状态机、evidence、attempt、audit

### 3.4 后端集中推导

状态解释逻辑必须集中在服务端，而不是分散到 Web / Desktop 前端。

前端消费摘要结果，不负责重建 kernel 状态机推理。

## 4. 非目标

本阶段不做：

- 新的 orchestration 独立页面
- 完整 DAG 可视化
- planner / scheduler 新抽象
- event sourcing 重构
- 新的持久化 summary 表
- LLM-based evaluator 解释

## 5. 核心设计

## 5.1 Node Observability Contract

在现有 orchestration detail 返回中的每个 node 上补充一个派生字段：

```json
{
  "summary": {
    "status": "waiting_human",
    "reason_code": "waiting_for_approval",
    "reason_title": "Approval required",
    "reason_detail": "Kernel evaluation requires human approval before marking this node complete.",
    "recommended_action": "approve",
    "action_enabled": true,
    "attempt_count": 1,
    "max_attempts": 2,
    "latest_evaluation_status": "waiting_human",
    "latest_agent_summary": "Implementation is ready; waiting for product sign-off.",
    "updated_at": "2026-05-11T12:34:56Z"
  }
}
```

字段职责固定如下：

- `status`
  - node 当前状态机状态
- `reason_code`
  - 面向展示和交互的稳定解释码
- `reason_title`
  - 简洁标题，适合卡片主文案
- `reason_detail`
  - 较完整解释，适合详情或 tooltip
- `recommended_action`
  - 当前最推荐的人类动作
- `action_enabled`
  - 当前动作是否可直接触发
- `attempt_count` / `max_attempts`
  - 是否还有自动恢复空间
- `latest_evaluation_status`
  - 最近一次 evaluator 结论
- `latest_agent_summary`
  - agent 补充语境，不参与最终判定
- `updated_at`
  - 最近摘要更新时间

## 5.2 状态、原因、动作三层分离

本设计要求严格区分三层语义：

- `status`
  - 回答“node 在状态机里处于哪一态”
- `reason_code`
  - 回答“为什么在这一态”
- `recommended_action`
  - 回答“现在最应该做什么”

示例：

- `status = waiting_human`
- `reason_code = waiting_for_human_input`
- `recommended_action = provide_input`

和

- `status = waiting_human`
- `reason_code = waiting_for_approval`
- `recommended_action = approve`

状态相同，但解释和动作不同。这正是该 contract 的价值。

## 5.3 顶层 Reason Code 集合

第一版推荐先收敛到以下顶层 reason code：

- `pending_dependencies`
- `ready_to_run`
- `running`
- `evaluation_in_progress`
- `waiting_for_human_input`
- `waiting_for_approval`
- `retry_scheduled`
- `runtime_failed`
- `invalid_result`
- `evidence_insufficient`
- `retry_exhausted`
- `completed`

说明：

- 顶层 code 只负责稳定解释，不直接暴露 evaluator 的全部细粒度内部原因。
- 像 `test_failed`、`review_failed`、`missing_artifact_or_changed_files` 这类更细粒度原因，优先落在 `reason_detail` 或 evaluator payload 中，而不是直接膨胀顶层 code 集合。

## 5.4 Recommended Action 集合

第一版动作集合应保持最小：

- `none`
- `retry`
- `approve`
- `provide_input`
- `inspect_evidence`

动作语义：

- `none`
  - 当前无需人工动作
- `retry`
  - 允许人工再次触发 node 执行
- `approve`
  - 允许人工完成审批
- `provide_input`
  - 需要用户补充上下文、需求或决策
- `inspect_evidence`
  - 当前最合适动作是先查看证据而非直接操作

## 5.5 Summary 推导优先级

服务端生成 `node_summary` 时，推导优先级固定如下：

1. 当前 node `status`
2. 最近 evaluator 结果
3. 最近 task/runtime failure
4. agent summary / risks / next actions

即：

> 系统结论优先，agent 自述补充。

这保证 UI 不会被自由文本带偏。

## 6. UI 设计

## 6.1 第一层：Decision Panel

Issue detail 中的 orchestration 第一层应作为决策面板，而不是日志面板。

固定展示：

- `Current node`
- `Current status`
- `Why this state`
- `Recommended action`
- `Latest agent summary`
- `Last updated`

该面板目标是让用户在 10 秒内回答：

- 现在执行到哪一步
- 系统为什么停在这里
- 是否需要我介入
- 如果要介入，我该做什么

这一层只消费 `node_summary`，不直接消费 raw event 做业务判断。

## 6.2 第二层：Node Debug Panel

维护者视图按 node 组织，而不是按 event 或 attempt 平铺。

每个 node 卡片默认展示：

- node name / type
- status
- reason code
- recommended action
- attempt count / max attempts
- latest evaluation status

展开后分为三个子区：

- `Evidence`
  - criteria coverage
  - artifacts
  - changed files
  - test / review result
- `Execution`
  - latest task id
  - runtime
  - agent
  - attempts list
- `Audit`
  - event timeline
  - raw structured result payload
  - evaluator detail

设计原则：

- node 是主组织单元
- attempt 是 node 内执行层
- timeline 是审计层，不抢主入口

## 7. 后端落地方式

## 7.1 不新增持久化表

第一版不建议为 `node_summary` 新增持久化表。

原因：

- 它是解释层，而非原始事实层
- 持久化会引入回填、迁移、一致性维护成本
- 当前更适合在服务端查询 / DTO 组装时集中推导

后续如该 contract 稳定，再考虑物化缓存或持久化。

## 7.2 服务端职责分布

- `server/internal/service/orchestration_evaluator.go`
  - 负责产出标准化 evaluator 结果
  - 应能够返回 `evaluation_status`、`reason_code`、`reason_detail`、`recommended_action`

- `server/internal/service/orchestrator.go`
  - 负责在 node / task / evaluation 状态变化后，汇总生成 `node_summary` 语义
  - 不要求持久化，但要求推导规则集中

- orchestration detail handler / response
  - 负责把 `summary` 连同 node 原始数据一并返回

## 7.3 前端职责分布

- `packages/core/api/schemas.ts`
  - 为 `summary` 加 defensive schema
  - 使用 optional + loose parsing，避免过渡期数据导致 UI 崩溃

- `packages/views/issues/components/issue-detail.tsx`
  - 第一层面板只读 summary
  - 第二层展开区才读取更细的 evidence / event 数据

## 8. 实施顺序

建议按以下顺序落地：

1. 在 evaluator 中统一产出 `evaluation_status / reason_code / reason_detail / recommended_action`
2. 在 orchestrator / orchestration read model 中生成 `node_summary`
3. 在 API schema 中加入 `summary`
4. 在 issue detail 中先落第一层 Decision Panel
5. 再补第二层 Node Debug Panel
6. 最后补完整 event timeline 与 attempt replay

## 9. 风险与约束

### 9.1 Reason Code 过细

若第一版顶层 reason code 设计过细，前端和后端 contract 会迅速失稳。

约束：

- 顶层 code 只保留稳定解释
- 更细粒度失败原因下沉到 detail / payload

### 9.2 前端重复推理

若 Web / Desktop 前端自行基于 event 推 reason，会重新复制一套状态机知识。

约束：

- 第一层显示逻辑只依赖 summary

### 9.3 Completed 被特殊处理

若 completed 不进入同一 contract，模型会重新分叉。

约束：

- completed 必须和 running / waiting_human / failed 一样，走统一 summary 模型

### 9.4 Agent 文本盖过系统结论

若 agent summary 优先于 kernel 判定，用户容易误解系统真实状态。

约束：

- 显示顺序固定为：kernel 结论在前，agent 补充在后

## 10. 成功标准

当本设计落地后，应满足以下结果：

1. 普通使用者在 issue detail 第一层就能理解当前 orchestration node 的状态、原因和下一步动作。
2. 维护者无需先翻 event timeline，就能按 node 快速定位卡点。
3. Web / Desktop 共享同一套后端解释语义，不需要各自重建状态机推理。
4. `completed` 不再是黑箱结果，而是同样具备标准原因和动作语义的终态。

