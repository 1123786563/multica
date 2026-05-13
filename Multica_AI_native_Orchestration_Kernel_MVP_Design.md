# Multica AI-native Orchestration Kernel

## MVP 设计方案

**在现有 Multica 基础上增加 Temporal + Eino，实现智能体编排系统**

**版本：** v1.0  
**日期：** 2026-05-13  
**范围：** 第一阶段 MVP，不包含 Coze Studio 类可视化设计器

# 目录

1.  背景与目标

2.  MVP 边界

3.  总体架构与核心分层

4.  Eino 与 Daemon 的职责边界（新增重点）

5.  MVP Workflow 设计

6.  模块设计

7.  Temporal 设计

8.  Eino Kernel 设计

9.  DaemonBridge 设计

10.  数据模型设计

11.  API 设计

12.  前端最小改造

13.  配置设计

14.  开发计划与验收标准

15.  风险与后续演进

16.  参考资料

# 1. 背景与目标

现有 Multica 的核心能力可以理解为：Issue -> Agent -> Runtime / Daemon -> CLI Agent 执行 -> 回写进度。它已经具备 issue 管理、agent 分配、runtime/daemon 执行、任务状态和日志回传等基础。

本方案的目标是在现有 Multica 上增加一个 AI-native orchestration kernel，使 Multica 从“AI coding agent task manager”升级为“AI-native software delivery orchestration system”。

```text
Issue
  -> Orchestration Workflow
  -> Temporal durable execution
  -> Eino reasoning activity
  -> Multica daemon / CLI agent 执行代码任务
  -> 节点状态、日志、产物、错误全部回写 Multica
```

第一阶段重点不是做大而全的平台，而是跑通一个真实的端到端闭环：issue 可以启动 workflow，Temporal 负责可靠执行，Eino 负责智能体分析/审查/总结，Multica daemon 负责真实代码执行，最终执行轨迹回写到 Multica。

# 2. MVP 边界

## 2.1 第一阶段要做

- Multica issue 可以启动一个 orchestration workflow。

- Temporal 负责 workflow 生命周期、重试、超时、恢复、取消。

- Eino 负责固定 workflow 内的 reasoning activity，包括分析、审查、总结和 coding prompt 生成。

- Multica daemon 继续负责 CLI agent 执行，例如 Claude Code、Codex、OpenCode、Gemini CLI 等。

- 每个节点有状态、日志、输入、输出、错误和产物记录。

- workflow 结果回写 issue。

- 支持失败、重试、取消，为后续多 agent、approval、skill 和 graph 扩展预留接口。

## 2.2 第一阶段不做

- 不做 Coze Studio 类可视化 Workflow Designer。

- 不做复杂低代码 DSL 和 marketplace。

- 不做复杂 RBAC、多租户计费和企业级治理。

- 不做完整 skill 自动沉淀。

- 不重写 Multica daemon 的核心执行模型。

- 不把 Eino、Daemon、Temporal 都做成“总状态源”。

- Temporal 不可用时，不回落到旧 direct Agent Task 路径。

# 3. 总体架构与核心分层

```text
┌──────────────────────────────────────────────┐
│                Multica Web                   │
│  Issue Board / Execution Panel / Event Log   │
└──────────────────────┬───────────────────────┘
                       │ REST / WebSocket
                       ▼
┌──────────────────────────────────────────────┐
│              Multica Go API                  │
│ Issue / Agent / Runtime / Orchestration API  │
│ Trace Service / Daemon Task Bridge           │
└──────────────┬───────────────────────────────┘
               │ StartWorkflow / CancelWorkflow
               ▼
┌──────────────────────────────────────────────┐
│              Temporal Cluster                │
│ Workflow History / Task Queue / Retry        │
└──────────────┬───────────────────────────────┘
               │ Poll Task Queue
               ▼
┌──────────────────────────────────────────────┐
│        Multica Orchestrator Worker           │
│ Temporal Workflows / Activities              │
│ Eino Kernel Adapter / Daemon Bridge          │
└──────────────┬───────────────────────────────┘
       ┌───────┴────────┐
       ▼                ▼
┌──────────────┐   ┌───────────────────────────┐
│ Eino Kernel  │   │      Multica Daemon        │
│ Reasoning    │   │ CLI Agent / Workspace      │
│ Activity     │   │ Shell / Stream / Cleanup   │
└──────────────┘   └───────────────────────────┘
```

| 层级 | 组件 | 核心职责 |
| --- | --- | --- |
| 产品协作层 | Multica Web / Go API | issue、agent、runtime、trace、UI、权限与工作区上下文。 |
| 编排持久化层 | Temporal | workflow 生命周期、事件历史、重试、超时、恢复、取消。 |
| 智能体逻辑层 | Eino | analyze、review、summary、recommended_agent_prompt；首版不生成动态 workflow graph。 |
| 执行运行时层 | Multica Daemon | CLI agent、本地/云端环境、workspace、shell、代码修改和日志回传。 |

最小正确边界是：Temporal 管流程，Eino 管智能，Daemon 管执行，Multica 管产品协作和可观测。

# 4. Eino 与 Daemon 的职责边界（新增重点）

Eino 和 Daemon 会有表面重叠，但不应该承担同一层职责。正确设计里，Eino 是“智能体逻辑编排层”，Daemon 是“执行环境/运行时层”。

```text
Eino   = Agent Brain / Workflow Logic
Daemon = Agent Runtime / Execution Worker
Temporal = Durable Orchestrator
Multica  = Product Control Plane
```

## 4.1 可能重复的地方

| 能力 | Eino | Multica Daemon | 边界建议 |
| --- | --- | --- | --- |
| 调用模型 | 可以直接调用 ChatModel。 | 可通过 Claude/Codex 等 CLI 间接调用。 | 模型推理和结构化分析优先由 Eino 做；代码执行型 CLI 调用由 Daemon 做。 |
| 执行工具 | 可以封装 Tool。 | 可以执行 shell、CLI 和本地命令。 | 轻量、只读、结构化工具可放 Eino；涉及仓库、文件、shell 的执行放 Daemon。 |
| Agent 执行 | 可以运行 Eino Agent / WorkflowAgent。 | 可以运行外部 CLI Agent。 | MVP 中 Eino 只做固定 workflow 内的 reasoning activity；Daemon 做 coding agent runtime。 |
| 流式日志 | 可通过 callback/stream 输出。 | 可回传 CLI stdout/stderr/transcript。 | 统一写入 orchestration_event，由 Multica 作为展示层。 |
| 多步骤任务 | 未来可以用 graph/workflow 表达。 | CLI agent 内部也可能多步执行。 | MVP 跨节点流程由固定 Temporal Workflow 管；单个 CLI 内部步骤由 Daemon 记录为 transcript。 |

## 4.2 正确边界：Eino 负责“想”，Daemon 负责“做”

Eino 应该负责 reasoning、review、summary 和 recommended_agent_prompt；Daemon 应该负责 runtime execution、workspace、shell、CLI agent、文件修改、日志和清理。

| Eino 负责 | Daemon 负责 |
| --- | --- |
| 分析 issue，判断问题类型、风险和上下文需求。 | 在本地或云端创建隔离 workspace。 |
| 生成 recommended_agent_prompt 和执行建议。 | checkout 代码、准备运行目录和环境变量。 |
| 建议是否需要人工关注或额外 review。 | 启动 Claude Code / Codex / OpenCode / Gemini CLI。 |
| 审查 git diff、test output、agent transcript。 | 执行 shell 命令、收集 stdout/stderr/transcript。 |
| 总结结果，生成 issue 评论和下一步建议。 | 管理 runtime heartbeat、任务取消、workspace 清理。 |

## 4.3 典型执行例子

用户创建 issue：登录接口在用户名为空时报 500，请修复。

```text
Eino:
  1. 读取 issue 内容。
  2. 判断这是 bug fix 类型。
  3. 生成修复计划。
  4. 生成给 coding agent 的 prompt。

Daemon:
  1. 创建 workspace。
  2. checkout repo。
  3. 启动 Claude Code / Codex CLI。
  4. 把 Eino 生成的 prompt 传给 CLI agent。
  5. 收集 diff、日志、测试结果。

Eino:
  1. 分析 git diff 和 test output。
  2. 判断修复是否合理。
  3. 生成 issue 评论和总结。
```

## 4.4 避免重复的关键规则

- Eino 不直接当 coding runtime：不要让 Eino 自己 git checkout、写文件、跑 mvn test、管理工作目录。

- Eino 不生成 MVP workflow topology：不要让 Eino 在首版新增、删除、重排、分支或循环 Temporal nodes。

- Daemon 不做高级规划：不要让 Daemon 决定 issue 拆解、agent 路由、失败后策略、是否需要人工介入。

- Temporal 是总流程状态源：不要让 Eino 或 Daemon 成为 workflow 总状态源。

- Multica 是产品状态和可观测入口：所有节点状态、日志和 artifact 统一回写到 Multica trace。

```text
Temporal Workflow Execution = 总生命周期状态源
orchestration_plan = Multica 产品侧 run 投影
orchestration_node = Multica 产品侧节点投影
orchestration_event = Multica 产品侧事件投影
orchestration_artifact = Multica 产品侧 artifact / evidence 投影
Daemon task = 某个节点的 runtime 执行状态
Eino run = 某个智能节点的内部执行过程
```

## 4.5 第一阶段推荐使用方式

MVP 阶段，Eino 只做 3 件事，Daemon 只做 1 件事，这样边界最清晰，重复最少。

```text
Eino:
  1. AnalyzeIssue
  2. ReviewResult
  3. SummarizeResult

Daemon:
  1. RunCodingAgent

MVP flow:
  load_issue
    -> eino_analyze_issue
    -> daemon_run_coding_agent
    -> validate_result
    -> eino_review_result
    -> eino_summarize_result
    -> complete_issue
```

# 5. MVP Workflow 设计

第一阶段固定实现一个 bug_fix_mvp workflow，先不做可视化和复杂 JSON DSL。

```text
load_issue
  -> analyze_issue
  -> run_coding_agent
  -> validate_result
  -> review_result
  -> summarize_result
  -> complete_issue
```

| 节点 | 执行方式 | 说明 |
| --- | --- | --- |
| load_issue | Temporal Activity | 读取 issue、repo、agent、runtime 信息。 |
| analyze_issue | Eino Activity | 分析 issue，生成执行计划和给 coding agent 的 prompt。 |
| run_coding_agent | Daemon Activity | 创建 daemon task，执行 Claude/Codex/OpenCode 等 CLI agent。 |
| validate_result | Temporal Activity | 校验 Agent Task structured result schema 和 evidence 字段；不额外执行 daemon 命令。 |
| review_result | Eino Activity | 生成 evidence/risk 的 advisory review；不直接决定 workflow 成功。 |
| summarize_result | Eino Activity | 生成 issue 评论和结果总结。 |
| complete_issue | Multica Activity | 写入总结、trace、artifact，并把 Issue 交接到 review；不自动 done。 |

# 6. 模块设计

建议在 Multica Go backend 中新增 orchestration 相关模块，尽量用 adapter 模式隔离 Eino、Temporal 和现有 daemon task 系统。

```text
server/internal/orchestration/
  service.go
  types.go
  config.go

server/internal/orchestration/temporal/
  client.go
  worker.go
  workflows.go
  activities.go
  registry.go

server/internal/orchestration/eino/
  kernel.go
  issue_analyzer.go
  result_reviewer.go
  summarizer.go
  tools.go

server/internal/orchestration/daemonbridge/
  dispatcher.go
  signaler.go
  cancel.go

server/internal/orchestration/trace/
  recorder.go
  events.go

server/internal/orchestration/api/
  handlers.go
  routes.go
```

## 6.1 Orchestration Service

```go
type OrchestrationService interface {
    StartIssueWorkflow(ctx context.Context, input StartIssueWorkflowInput) (*OrchestrationPlanProjection, error)
    GetIssueOrchestration(ctx context.Context, issueID uuid.UUID) (*IssueOrchestrationProjection, error)
    ListEvents(ctx context.Context, planID uuid.UUID) ([]OrchestrationEventProjection, error)
    CancelPlan(ctx context.Context, planID uuid.UUID) error
}
```

# 7. Temporal 设计

Temporal Workflow 只做流程控制和 deterministic decision，不直接调用数据库、LLM、HTTP、文件系统或 daemon。所有非确定性外部交互都放到 Activity 中。

```text
Temporal Workflow:
  -> ExecuteActivity(LoadIssue)
  -> ExecuteActivity(EinoAnalyzeIssue)
  -> ExecuteActivity(DispatchDaemonAgent)
  -> WaitSignal(AgentTaskCompleted / AgentTaskFailed / AgentTaskCancelled)
  -> ExecuteActivity(ValidateResult)
  -> ExecuteActivity(EinoReviewResult)
  -> ExecuteActivity(EinoSummarizeResult)
  -> ExecuteActivity(CompleteIssue)
```

## 7.1 Workflow determinism 与 Projection Activity Boundary

Workflow 代码必须可 replay，不能直接产生外部副作用。以下操作只能在 Activity 中执行，并且 Activity 必须按 `plan_id`、`node_id`、`attempt`、`event_type` 等 key 做幂等：

- 写入或修复 `orchestration_plan`、`orchestration_node`、`orchestration_event`、`orchestration_artifact`。
- 创建、取消或更新 `agent_task_queue`。
- 调用 Eino / LLM、Multica API、HTTP、文件系统、daemon bridge。
- 写 Issue comment、推进 Issue status、创建 attention comment。
- 发送 WebSocket / notification refresh。
- 传播 cancel 到 active Agent Task。

Workflow 可以保存 deterministic 局部变量和调用 `ExecuteActivity` / `WaitSignal` / `ContinueAsNew` 等 Temporal 原语，但不能为了“顺手更新 UI”直接写 projection。projection lag 时，以 Temporal history 为准，由 repair/reconciliation Activity 补齐。

## 7.2 Workflow 输入输出

```go
type IssueWorkflowInput struct {
    ExecutionID  uuid.UUID
    WorkspaceID  uuid.UUID
    IssueID      uuid.UUID
    AgentID      uuid.UUID
    RuntimeID    uuid.UUID
    WorkflowType string
}

type IssueWorkflowResult struct {
    Status    string
    Summary   string
    Artifacts []WorkflowArtifactRef
}
```

## 7.3 Activity 超时与重试建议

| Activity | Timeout | Retry 建议 |
| --- | --- | --- |
| LoadIssueActivity | 30s | 可重试，处理数据库/API 临时失败。 |
| AnalyzeIssueActivity | 5min | 可重试，处理 LLM transient error。 |
| DispatchDaemonAgentActivity | 30s | 只创建或复用 daemon task；必须幂等，不能等待 task 完成。 |
| ValidateResultActivity | 30s | 可重试，只做 schema/evidence deterministic validation，不运行测试命令。 |
| ReviewResultActivity | 5min | 可重试。 |
| SummarizeResultActivity | 5min | 可重试。 |
| CompleteIssueActivity | 30s | 可重试，处理 issue 回写临时失败。 |

## 7.4 validate_result 边界

MVP 的 `ValidateResultActivity` 是 deterministic evidence/schema validation，不是第二个 daemon test runner。

它只检查：

- Agent Task outcome 已通过 Signal/Update 回到 Temporal。
- structured result `schema_version` 支持且 JSON 结构合法。
- `summary`、`changed_files`、`artifacts`、`tests`、`risks` 字段存在且类型正确。
- schema malformed、缺失 evidence、failed tests、非空 risks 会输出明确的 validation outcome；只有格式/证据不足等 non-semantic failure 可进入自动 retry，failed tests / risks 进入 approval 策略。

真实 lint/test 可以由 coding agent 在 `run_coding_agent` 内执行，并把结果写入 structured result；`validate_result` 不直接调用 daemon、shell、CLI 或 Eino。

## 7.5 Node Retry Policy 边界

MVP 区分 Temporal Activity retry 和 Node retry：

- Temporal Activity retry 只处理数据库/API/LLM/worker 的 transient error，不代表重新执行代码修改。
- Node retry 会创建新的 Agent Task attempt，必须写入新的 node attempt / Kernel Event / prior evidence summary。
- 自动 Node retry 只允许 recoverable non-semantic failure：schema malformed、evidence insufficient、timeout、worker transient error、Signal delivery repair。
- `failed tests`、非空 `risks`、high-risk review concern、unverifiable evidence、destructive operation 不自动重试，进入 Approval Gate。
- MVP 统一 `max_node_attempts = 2`，即首次 attempt + 最多一次 retry。

人工在 Approval Gate 点击 `Retry` 时，也只能在 retry budget 未耗尽时创建下一次 Agent Task attempt；retry context 来自 kernel 生成的 validation feedback / review concern，不接受人工 `request changes` 输入。

## 7.6 Outcome Policy 边界

`review_result` 不拥有最终裁决权。Temporal Workflow 在收到 validation outcome 和 Eino advisory review 后，由 Temporal Outcome Policy 决定下一步：

| 条件 | 推荐 outcome |
| --- | --- |
| schema / evidence 合法、tests 通过、risks 为空、review 无阻断 concern | complete |
| schema 缺失、malformed、evidence insufficient、timeout、transient recovery 且 retry budget 未耗尽 | retry |
| risks 非空、failed tests、review 存在高风险 concern、retry budget 耗尽且仍需要人工判断 | approval |
| Signal/task 状态不可恢复、投影无法修复、人工取消或明确不可继续 | failed / cancelled |

Eino 的 `recommended_policy_action` 只能作为 policy 输入，不能覆盖 deterministic validation、risk、failed tests 或 approval 规则。

## 7.7 Approval Gate 边界

MVP 的 Approval Gate 是 workflow 内置等待态，不是完整人工编辑流。它只支持三类显式动作：

| 动作 | 语义 |
| --- | --- |
| approve | 人工接受当前风险或不完整性，允许 workflow 继续进入 review handoff。 |
| retry | 在 retry budget 未耗尽时，基于 kernel 生成的 validation feedback / review concern 创建下一次 Agent Task attempt。 |
| cancel | 取消 Temporal Workflow，并传播取消到活跃 Agent Task。 |

MVP 不支持 `request changes`，也不允许审批动作直接改写 Issue 描述、注入任意 prompt、跳转到任意节点或修改 workflow topology。后续如果引入 change request，必须作为独立的显式能力重新设计审计、权限、prompt 拼接和重试语义。

Approval Action 权限：

- 允许：workspace owner、workspace admin、Issue creator、human assignee。
- 禁止：agent assignee、发起该 run 的 agent、执行该 run 的 agent、非 Issue 可见成员。
- 所有 Approval Action 都要求 `actor_type = human`。

每次 Approval Action 必须写入 approval audit event：

```json
{
  "event_type": "approval.approved | approval.retry_requested | approval.cancelled",
  "actor_id": "uuid",
  "actor_type": "human",
  "action": "approve | retry | cancel",
  "reason": "human provided reason",
  "plan_id": "uuid",
  "node_id": "uuid"
}
```

## 7.8 complete_issue 边界

`CompleteIssueActivity` 是 review handoff，不是自动验收关闭：

- 写入 summarize_result 生成的 issue comment / trace / artifact 投影。
- 可把 Issue 从执行中状态推进到 review 类状态，例如 `in_review`。
- 不自动标记 `done`，不 close issue，不删除或隐藏 orchestration evidence。
- 如果未来需要自动 done，必须通过单独 policy 和权限决策明确引入。

# 8. Eino Kernel 设计

第一阶段不要直接暴露复杂 graph，也不要让 Eino 生成或修改 workflow topology。先封装成固定 workflow 内的业务接口；后续再逐步扩展到 SequentialAgent、LoopAgent、ParallelAgent 或完整 Graph。

```go
type EinoKernel interface {
    AnalyzeIssue(ctx context.Context, input AnalyzeIssueInput) (*AnalyzeIssueOutput, error)
    ReviewResult(ctx context.Context, input ReviewResultInput) (*ReviewResultOutput, error)
    SummarizeResult(ctx context.Context, input SummarizeResultInput) (*SummarizeResultOutput, error)
}
```

## 8.1 AnalyzeIssue 输出示例

```json
{
  "problem_summary": "登录接口缺少空用户名参数校验",
  "risk_level": "medium",
  "suspected_files": [
    "internal/auth/handler.go",
    "internal/auth/service.go"
  ],
  "execution_advice": [
    "检查登录参数校验逻辑",
    "修复空用户名导致的异常",
    "补充单元测试",
    "运行测试"
  ],
  "recommended_agent_prompt": "请根据 issue 修复登录接口空用户名导致 500 的问题..."
}
```

## 8.2 ReviewResult 输出示例

```json
{
  "review_summary": "结构化证据显示登录空用户名校验已补充，并报告了相关测试。",
  "concerns": [],
  "risk_level": "low",
  "recommended_policy_action": "complete"
}
```

`recommended_policy_action` 只是建议输入，最终 `complete / approval / retry / failed` 由 Temporal Outcome Policy 根据 validation outcome、risk、failed tests、missing evidence、approval state 和 retry budget 决定。

## 8.3 SummarizeResult 输出示例

```json
{
  "issue_comment": "已修复登录接口空参数导致的 500 问题...",
  "status_recommendation": "in_review",
  "artifacts": ["git_diff", "test_report"]
}
```

# 9. DaemonBridge 设计

DaemonBridge 用来把 Temporal Workflow 和 Multica 当前 daemon task 系统连接起来。第一阶段复用现有 agent task 创建、daemon claim/start/complete/fail/cancel 和日志流机制，但 Agent Task 完成结果必须通过 Temporal Signal/Update 回到 Workflow。

```go
type DaemonBridge interface {
    CreateAgentTask(ctx context.Context, input CreateAgentTaskInput) (*AgentTask, error)
    SignalAgentTaskCompleted(ctx context.Context, input AgentTaskCompletedInput) error
    SignalAgentTaskFailed(ctx context.Context, input AgentTaskFailedInput) error
    SignalAgentTaskCancelled(ctx context.Context, input AgentTaskCancelledInput) error
    CancelAgentTask(ctx context.Context, taskID uuid.UUID) error
}
```

## 9.1 第一阶段实现方式

1.  DispatchDaemonAgentActivity 创建 agent task。

2.  task 绑定 orchestration_plan_id、orchestration_node_id、node attempt、task_id 和 temporal_workflow_id。

3.  daemon 按现有机制 pick task。

4.  daemon stream 日志回传 server。

5.  server 写入 orchestration_event / orchestration_node 投影。

6.  DispatchDaemonAgentActivity 返回 task_id，Temporal Workflow 进入等待 Signal 状态。

7.  daemon 调用现有 CompleteTask / FailTask / CancelTask API 时，Multica API 先记录 Agent Task outcome 和投影，再向对应 Temporal Workflow 发送 AgentTaskCompleted / AgentTaskFailed / AgentTaskCancelled Signal 或 Update。

8.  Temporal Workflow 收到 Signal 后先校验 signal payload 是否匹配当前 waiting node attempt；匹配后才继续进入 validate_result / review_result / summarize_result。

9.  取消 workflow 时，CancelExecution 需要先取消 Temporal Workflow，再传播到当前 active daemon task。

Polling 只能作为 repair / reconciliation 机制，例如 API 发 Signal 失败后由后台 job 根据 task outcome 补发；不能作为第一阶段主完成路径。

## 9.2 Agent Task Outcome Signal Contract

Agent Task outcome Signal 不能只依赖 `temporal_workflow_id`。MVP 的 Signal payload 至少包含：

```go
type AgentTaskOutcomeSignal struct {
    PlanID         uuid.UUID
    NodeID         uuid.UUID
    Attempt        int
    TaskID         uuid.UUID
    OutcomeVersion int
    OutcomeType    string // completed, failed, cancelled
    ResultRef      *WorkflowArtifactRef
    ResultJSON     json.RawMessage
}
```

Workflow 内部必须保存当前 waiting node attempt 的 `plan_id`、`node_id`、`attempt`、`task_id`。收到 Signal 后：

- payload 全部匹配当前 waiting node attempt，才允许推进 workflow。
- 重复 Signal 命中同一个已处理 outcome，应写入 `signal.duplicate_ignored` 或作为 idempotent duplicate 忽略，不重复推进。
- 旧 attempt 的 Signal 必须写入 `signal.stale_ignored`，但不能推进 workflow。
- 错误 `node_id`、错误 `task_id`、错误 `plan_id` 的 Signal 必须写入 `signal.mismatched_rejected`，但不能推进 workflow。
- repair / reconciliation job 补发 Signal 时也必须使用同一 contract。
- `OutcomeVersion` 用于未来演进结果结构；未知版本不能直接成功，只能进入 evidence insufficient / projection repair 路径。

这些 Signal audit events 默认只出现在展开事件流中，不作为 Decision Panel 主错误展示。只有当前 waiting node 长时间没有有效 Signal、repair/reconciliation 失败、或 Workflow 进入 failed / waiting_human 时，Decision Panel 才显示需要关注的主状态。

# 10. 数据模型设计

第一阶段不新增并行的 `workflow_executions`、`workflow_node_executions`、`workflow_events`、`workflow_artifacts` 表。现有 `orchestration_*` 表继续作为 Multica Projection，Temporal history 才是 workflow lifecycle source of truth。

## 10.1 orchestration_plan 扩展

`orchestration_plan` 表示 Issue-scoped Orchestration Run 的产品侧投影。建议补充：

```sql
ALTER TABLE orchestration_plan
    ADD COLUMN temporal_workflow_id TEXT,
    ADD COLUMN temporal_run_id TEXT,
    ADD COLUMN workflow_type TEXT NOT NULL DEFAULT 'bug_fix_mvp',
    ADD COLUMN projection_version INT NOT NULL DEFAULT 1,
    ADD COLUMN last_synced_at TIMESTAMPTZ,
    ADD COLUMN sync_error JSONB;

CREATE UNIQUE INDEX idx_orchestration_plan_temporal_workflow
    ON orchestration_plan(temporal_workflow_id)
    WHERE temporal_workflow_id IS NOT NULL;
```

约束：

- 同一 Issue 同一时间最多一个 active `orchestration_plan`。
- 启动 workflow 前先创建或读取 active `orchestration_plan`；重复触发返回已有 active plan，不启动第二个 Temporal Workflow。
- `temporal_workflow_id` 由本次 run 的 `plan_id` 派生，例如 `multica/{workspace_id}/issue/{issue_id}/run/{plan_id}`。
- 每次新的非并发 run 使用新的 `plan_id` 和新的 `temporal_workflow_id`，避免固定 Issue Workflow ID 带来的 Temporal reuse / retention 语义歧义。
- `status` 是 Temporal lifecycle 的投影，不是最终状态源。
- 如果 `orchestration_plan.status` 和 Temporal history 冲突，以 Temporal 为准并修复投影。

## 10.2 orchestration_node 扩展

`orchestration_node` 表示 workflow node 的产品侧投影。建议补充：

```sql
ALTER TABLE orchestration_node
    ADD COLUMN workflow_node_key TEXT,
    ADD COLUMN temporal_activity_id TEXT,
    ADD COLUMN signal_name TEXT,
    ADD COLUMN projection_version INT NOT NULL DEFAULT 1,
    ADD COLUMN last_synced_at TIMESTAMPTZ,
    ADD COLUMN sync_error JSONB;
```

约束：

- `workflow_node_key` 对应 `load_issue`、`analyze_issue`、`run_coding_agent`、`validate_result`、`review_result`、`summarize_result`、`complete_issue`。
- `status` 是 Decision Panel 使用的投影状态。
- Agent Task 外键继续使用现有 `agent_task_queue.orchestration_plan_id` 和 `agent_task_queue.orchestration_node_id`。

## 10.3 orchestration_event / orchestration_artifact

`orchestration_event` 和 `orchestration_artifact` 继续承载审计、日志、artifact 和 evidence 投影。建议补充统一事件来源：

```sql
ALTER TABLE orchestration_event
    ADD COLUMN source TEXT NOT NULL DEFAULT 'multica',
    ADD COLUMN temporal_event_id TEXT,
    ADD COLUMN projection_version INT NOT NULL DEFAULT 1;

ALTER TABLE orchestration_artifact
    ADD COLUMN source TEXT NOT NULL DEFAULT 'multica',
    ADD COLUMN projection_version INT NOT NULL DEFAULT 1;
```

事件来源建议：

| source | 说明 |
| --- | --- |
| temporal | workflow/activity/signal/cancel/timeout 投影事件 |
| daemon | CLI agent stdout/stderr/transcript/task outcome |
| eino | analyze/review/summarize reasoning 输出 |
| multica | issue/comment/status/projection repair 等产品侧事件 |

## 10.4 状态投影

| 投影对象 | 状态值 |
| --- | --- |
| orchestration_plan.status | queued, running, waiting_human, completed, failed, cancelled |
| orchestration_node.status | pending, running, completed, failed, skipped, cancelled, waiting_human |
| orchestration_event.event_type | workflow.started, node.started, eino.requested, daemon.stream, task.completed, task.failed, signal.sent, signal.duplicate_ignored, signal.stale_ignored, signal.mismatched_rejected, approval.approved, approval.retry_requested, approval.cancelled, projection.repaired 等 |

# 11. API 设计

## 11.1 启动编排

```http
POST /api/issues/{issueId}/orchestration/start

Request:
{
  "workflow_type": "bug_fix_mvp",
  "agent_id": "uuid",
  "runtime_id": "uuid"
}

Response:
{
  "plan_id": "uuid",
  "temporal_workflow_id": "multica/{workspace_id}/issue/{issue_id}/run/{plan_id}",
  "status": "queued"
}
```

启动幂等规则：

1. 在数据库事务内按 Issue 查询 active `orchestration_plan`。
2. 如果存在 active plan，直接返回该 plan，不调用 Temporal StartWorkflow。
3. 如果不存在 active plan，创建新的 `orchestration_plan`，用 `plan_id` 生成 `temporal_workflow_id`。
4. 调用 Temporal StartWorkflow；如果 StartWorkflow 返回 WorkflowAlreadyStarted，按 `temporal_workflow_id` 查询并修复 projection 后返回同一个 run。
5. 同一 Issue 的历史 completed / failed / cancelled run 保留；新 run 创建新的 `plan_id` 和 Workflow ID。

## 11.2 查询执行详情

```http
GET /api/issues/{issueId}/orchestration

Response:
{
  "id": "uuid",
  "issue_id": "uuid",
  "workflow_type": "bug_fix_mvp",
  "status": "running",
  "nodes": [
    {"node_key": "load_issue", "status": "succeeded"},
    {"node_key": "analyze_issue", "status": "succeeded"},
    {"node_key": "run_coding_agent", "status": "running"}
  ]
}
```

## 11.3 查询事件与取消执行

```http
GET  /api/issues/{issueId}/orchestration
POST /api/orchestration/nodes/{nodeId}/approve
POST /api/orchestration/nodes/{nodeId}/retry
POST /api/orchestration/plans/{planId}/cancel
```

Approval Action request:

```json
{
  "reason": "human provided reason"
}
```

Approval Action 处理逻辑：

1. 从当前 auth session 解析 `actor_id` 和 `actor_type`。
2. 校验 actor 是 human，并且是 workspace owner/admin、Issue creator 或 human assignee。
3. 拒绝 agent assignee、发起该 run 的 agent、执行该 run 的 agent。
4. 写入 `approval.approved` / `approval.retry_requested` / `approval.cancelled` audit event，包含 actor、action、reason、plan_id、node_id。
5. 将 approval action 转换成 Temporal Signal/Update 或 cancellation request。

MVP 不提供 `request-changes` endpoint。审批动作会转换成 Temporal Signal/Update，由 Workflow 自己决定后续状态。

Cancel 处理逻辑：
1. 查询 temporal_workflow_id。
2. 调用 Temporal CancelWorkflow。
3. 如果当前存在 daemon task，则调用 DaemonBridge.CancelAgentTask。
4. Temporal cancellation 完成后投影 orchestration_plan.status = cancelled。
5. 写入 orchestration_event。

# 12. 前端最小改造

第一阶段不做流程图、DAG canvas、独立 orchestration 页面或 workflow designer，只在 issue 页面增加一个线性的 Orchestration Run Panel。MVP workflow 本身是固定线性链，所以前端重点是把过程证据展示完整，而不是提前做拓扑可视化。

```text
Orchestration

Status: running

Nodes:
✓ load_issue
✓ analyze_issue
▶ run_coding_agent
○ validate_result
○ review_result
○ summarize_result
○ complete_issue

Events:
[10:01:02] Workflow started
[10:01:05] Issue loaded
[10:01:20] Eino generated coding guidance
[10:01:30] Daemon task created
[10:01:35] Agent: reading repository...
[10:02:10] Agent: editing internal/auth/service.go
```

节点列表每一行至少展示：

- node key / display name。
- status。
- reason code。
- recommended action。
- attempt count。
- latest summary。
- evidence count。
- linked Agent Task / runtime 信息。

每个节点可以展开查看相关 Kernel Events、Agent Task transcript 摘要、structured evidence、Signal audit events 和 artifacts。未来只有在 workflow 引入 condition / parallel / loop 后，才升级为 DAG / graph visualization。

当 run 或 node 处于 `waiting_human` 时，面板只展示 `Approve`、`Retry`、`Cancel` 三个动作，并展示 server-projected reason、risk、failed tests、retry budget 和推荐动作。MVP 不展示 `Request changes` 输入框。

Signal duplicate / stale / mismatch 默认只在 expanded Events 中展示；Decision Panel 不因单个 ignored Signal 改为 error。只有当前 waiting node 没有有效 Signal、repair 失败或 Workflow policy 进入 failed / waiting_human 时，才在主面板显示 attention reason。

Attention comment 只在需要人工注意时创建：

- `waiting_human`。
- run failed。
- retry exhausted。
- repair / reconciliation failed。
- orchestration entrypoint fail closed，例如 Temporal unavailable。

成功 run 不创建默认 attention comment；review handoff 的总结评论由 `complete_issue` 负责，不承担告警语义。

Attention audience 只包含 Issue 相关人：

- Issue creator。
- human assignee。
- subscribers / watchers。

不 mention agent assignee，不 workspace-wide broadcast，不把无关 workspace 成员拉进通知。

前端数据来源复用现有 Issue Detail orchestration 查询：GET /api/issues/{issueId}/orchestration。WebSocket 使用 coarse refresh 事件 `orchestration:updated`，客户端通过 React Query invalidate 后重新读取投影，不把 Temporal history 直接塞进 Zustand。

# 13. 配置设计

Temporal 是显式外部依赖，不嵌入 Multica API，也不默认进入 `make dev`。本地开发可以通过 `temporal server start-dev` 或 docker compose profile 启动 Temporal Server，然后单独启动 orchestration worker。

```yaml
orchestration:
  enabled: true

  temporal:
    address: localhost:7233
    namespace: default
    task_queue: multica-orchestration

  eino:
    provider: openai_compatible
    model: gpt-4o
    base_url: ${OPENAI_BASE_URL}
    api_key: ${OPENAI_API_KEY}

  workflow:
    default_type: bug_fix_mvp
    daemon_task_timeout: 2h
    max_node_attempts: 2
```

对应环境变量建议：

- `ORCHESTRATION_ENABLED`
- `TEMPORAL_ADDRESS`
- `TEMPORAL_NAMESPACE`
- `TEMPORAL_TASK_QUEUE`
- `EINO_PROVIDER`
- `EINO_MODEL`
- `EINO_BASE_URL`
- `EINO_API_KEY`

建议命令边界：

```bash
make orchestration-worker   # 单独启动 Temporal workflow/activity worker
```

未配置 Temporal 或 worker 不可用时，启动 orchestration 应返回明确的 unavailable 错误，不静默回落旧 DB-owned kernel path，也不创建 direct Agent Task。

适用入口：

- 手动 StartIssueWorkflow。
- agent assignment 自动触发。
- manual retry / rerun。
- comment-triggered orchestration 入口。

这些入口在 Temporal unavailable 时 fail closed，可记录 projection event / attention comment / UI error，但不能绕过 Temporal 直接派发 Agent Task。attention comment 只通知 Issue creator、human assignee、subscribers / watchers。

# 14. 开发计划与验收标准

| 阶段 | 目标 | 验收标准 |
| --- | --- | --- |
| Phase 1.1 Temporal 接入 | 引入 Temporal SDK，新增 client/worker/workflow/mock activities，API 能通过显式 Temporal 配置启动 workflow。 | Temporal 配置存在且 worker 独立运行时，issue 页面点击按钮后 workflow 从 queued -> running -> succeeded；Temporal 未配置时返回 unavailable 且不创建 direct Agent Task。 |
| Phase 1.2 Projection 字段和事件流 | 扩展现有 orchestration_plan/node/event/artifact 表，Activity 开始/结束写 projection event。 | Workflow 代码不直接写 DB/WS/comment；Activity 能幂等投影 load_issue/analyze_issue/run_coding_agent 等节点状态。 |
| Phase 1.3 Eino 接入 | 封装 EinoKernel，实现 AnalyzeIssue、ReviewResult、SummarizeResult。 | 输入 issue 后，Eino 能生成 problem_summary、execution_advice、recommended_agent_prompt。 |
| Phase 1.4 DaemonBridge 接入 | Temporal Activity 能创建 daemon task，daemon 日志进入 orchestration_event，task outcome 通过 Signal/Update 回 Temporal。 | 真实 issue 可以触发 CLI agent 执行，执行日志可查看。 |
| Phase 1.5 取消、失败、重试 | 实现 cancel、timeout、失败 trace、有限 retry。 | workflow 可取消；自动 retry 只覆盖 recoverable non-semantic failures；failed tests / risks 进入 Approval Gate；worker 重启后 workflow 不丢。 |

## 14.1 MVP 最终验收标准

- issue 可以启动 orchestration workflow。

- Temporal 能持久化 workflow execution。

- orchestration worker 能作为独立进程运行，默认 `make dev` 不强制启动 Temporal。

- Temporal unavailable 时 orchestration 入口 fail closed，不 fallback 到旧 direct task。

- Node retry 最多 2 次 attempt，且 failed tests / risks / high-risk review concern 不自动重新执行代码修改。

- Workflow determinism 测试能证明 Workflow replay 不会重复写 projection、comment、notification 或 Agent Task side effects。

- Eino 能分析 issue 并生成 execution_advice 和 recommended_agent_prompt。

- daemon 能被 workflow 调度执行 CLI agent。

- validate_result 能 deterministic 校验 Agent Task structured result 和 evidence。

- 节点状态、日志、输出、错误能落库。

- issue 页面能看到执行过程。

- workflow 能成功、失败、取消。

- complete_issue 不自动 done，只生成 review handoff 总结并可把 Issue 推到 review 状态。

- worker 重启后 workflow 可以恢复。

```text
Issue
  -> Start Orchestration
  -> Temporal Workflow
  -> Eino Analyze
  -> Daemon CLI Agent
  -> Validate
  -> Eino Review
  -> Eino Summary
  -> Review Handoff / Issue Comment / Status Nudge
  -> Trace View
```

# 15. 风险与后续演进

| 风险 | 说明 | 缓解措施 |
| --- | --- | --- |
| 职责混乱 | Eino、Daemon、Temporal 都可能被误用成“总编排器”。 | 明确：Temporal 管流程，Eino 管智能，Daemon 管执行，Multica 管产品。 |
| 重复执行代码修改 | daemon 节点自动 retry 可能导致重复修改。 | run_coding_agent 默认不自动重试，失败后人工确认或重新创建节点执行。 |
| Workflow 里出现非确定性调用 | 直接在 Temporal Workflow 中调用 LLM/DB/API 会破坏 replay。 | 所有外部调用必须放在 Activity。 |
| CLI agent 内部不可恢复 | Claude/Codex/OpenCode 内部中断后未必能精确恢复。 | 恢复粒度放在 workflow node 层，而不是 CLI agent 内部步骤层。 |
| 前端复杂度失控 | 一开始做可视化设计器或 DAG canvas 会拖慢内核验证。 | 第一阶段只做线性节点列表、事件流和 evidence 展开。 |

## 15.1 后续演进方向

- 支持多个 workflow_type。

- 支持 JSON workflow definition。

- 支持 condition、parallel、loop、approval node。

- 用 Eino SequentialAgent / LoopAgent / ParallelAgent 支持更复杂的多 agent 协作。

- 支持 skill extraction、workflow template 和后续可视化设计器。

| Eino 能力 | 未来映射 |
| --- | --- |
| SequentialAgent | 顺序开发流程，例如分析 -> 编码 -> 验证 -> 总结。 |
| LoopAgent | fix -> test -> review -> fix 循环。 |
| ParallelAgent | 并行 code review、安全审查、性能审查，或多个模块并行开发。 |
| Graph / Workflow | 更复杂的条件分支、工具调用、状态传递和节点复用。 |

# 16. 结论

这套方案的核心不是把 Multica 改造成通用 workflow 平台，而是把 Multica 的 agent task/runtime 基础升级为 AI-native software delivery orchestration system。

```text
Multica 负责产品协作和 runtime 管理
Temporal 负责 durable workflow execution
Eino 负责固定 workflow 内的 reasoning activity
Daemon 负责真实代码执行
PostgreSQL 负责 trace 和状态沉淀
```

第一阶段最关键的交付物只有一个：一个 issue 能通过 Temporal 启动固定编排流程，Eino 生成分析、审查、总结和 recommended_agent_prompt，daemon 执行 coding agent，最终结果和完整 trace 回写到 Multica。

# 17. 参考资料

- Multica GitHub: https://github.com/multica-ai/multica

- Multica CLI_AND_DAEMON.md: https://github.com/multica-ai/multica/blob/main/CLI_AND_DAEMON.md

- CloudWeGo Eino GitHub: https://github.com/cloudwego/eino

- Eino 文档: https://www.cloudwego.io/docs/eino/

- Temporal Workflow Definition: https://docs.temporal.io/workflow-definition

- Temporal Go SDK: https://pkg.go.dev/go.temporal.io/sdk/workflow
