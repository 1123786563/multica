# Multica AI-native Orchestration Kernel 详细设计文档

## 1. 文档目的

本文档用于描述如何在 Multica 现有架构基础上，演进出一个 **AI-native orchestration kernel**。

目标不是重写 Multica，而是在保留现有 issue、agent、daemon、task queue、workspace、UI 的基础上，新增一个服务端编排内核，使 Multica 从“AI teammate 协作平台”升级为“AI 工作执行内核”。

核心变化是：

> 将工作推进权从 Agent Prompt 中收回到服务端 Kernel，由 Kernel 负责计划、调度、状态推进、验收、重试、阻塞和人工审批。

---

## 2. 背景与现状

### 2.0 当前代码证据

本文档基于当前仓库状态设计，关键依据如下：

| 能力 | 当前证据 |
|---|---|
| issue 承载任务目标 | `server/migrations/001_init.up.sql` 中的 `issue` 表，包含 status、priority、assignee、acceptance_criteria、context_refs |
| issue dependency | `issue_dependency` 支持 `blocks` / `blocked_by` / `related` |
| agent task queue | `agent_task_queue` 已有 queued / dispatched / running / completed / failed / cancelled 生命周期 |
| runtime claim | `server/internal/handler/daemon.go` 中 `ClaimTaskByRuntime` 负责 runtime 领取任务并组装 claim payload |
| task lifecycle | `StartTask` / `CompleteTask` / `FailTask` / `CancelTask` 由 daemon 调用 |
| daemon prompt | `server/internal/daemon/prompt.go` 中 `BuildPrompt` 仍以 issue / chat / comment / autopilot / quick-create 为入口 |
| task context | `server/migrations/003_task_context.up.sql` 已给 `agent_task_queue` 增加 nullable `context JSONB` |
| nullable issue task | `server/migrations/033_chat.up.sql` 已允许 `agent_task_queue.issue_id` 为空，支持 chat / quick-create / autopilot 类任务 |
| retry / attempt | `server/migrations/055_task_lease_and_retry.up.sql` 已有 attempt、max_attempts、parent_task_id、failure_reason |

因此 orchestration 第一版不需要重建 task queue，只需要在现有 task lifecycle 上增加 plan / node / artifact / event 四类内核对象，并把 task 从“整个 issue 的执行”变成“某个 node 的一次执行尝试”。

### 2.1 Multica 当前形态

从当前代码结构来看，Multica 已经具备以下基础能力：

1. Workspace / Member / Agent 管理。
2. Issue 作为任务承载对象。
3. Issue 支持 status、priority、assignee、parent issue、acceptance criteria、context refs。
4. Issue dependency 支持 blocks / blocked_by / related 关系。
5. Agent task queue 支持 queued、dispatched、running、completed、failed、cancelled 等执行状态。
6. Daemon 负责连接本地 AI CLI runtime，并领取 task 执行。
7. Prompt 负责把 issue/chat/autopilot/quick-create 转换成 agent 可执行指令。
8. TaskService 负责创建、领取、开始、完成、失败和取消任务。

这些能力已经可以支撑“AI agent 领取任务并执行”。

### 2.2 当前问题

当前 Multica 的核心问题是：

> Agent 既是执行者，也是事实上的流程推进者。

例如：

1. Agent 通过 prompt 获取 issue。
2. Agent 自己理解任务。
3. Agent 自己决定是否完成。
4. Agent 自己评论或更新 issue。
5. 系统更多负责 queue 和 runtime，而不是流程判断。

这种模式适合早期 AI teammate 产品，但存在几个明显问题：

| 问题 | 影响 |
|---|---|
| 状态推进依赖 agent 自觉 | agent 可能误判完成 |
| Prompt 中隐藏流程逻辑 | 难以审计、难以复现 |
| 缺少计划图 | 复杂任务无法稳定拆解和追踪 |
| 缺少结构化验收 | acceptance criteria 无法被强制执行 |
| 多 agent 协作靠自然语言触发 | 容易循环、遗漏、重复执行 |
| task completed 等于执行结束 | 但不等于任务真的完成 |
| 缺少 evidence/artifact 模型 | 无法证明 agent 做了什么 |

### 2.3 目标架构转变

当前模式：

```text
Issue
  ↓
TaskService 创建 task
  ↓
Daemon 领取 task
  ↓
Agent 执行整个 issue
  ↓
Agent 自己更新状态 / 评论
```

目标模式：

```text
Issue / Chat / Autopilot / API
  ↓
Orchestrator 创建 Plan
  ↓
Planner 拆分 Node
  ↓
Scheduler 判断可执行节点
  ↓
Dispatcher 创建 Agent Task
  ↓
Daemon 执行当前 Node
  ↓
Agent 提交结构化结果和 Artifacts
  ↓
Evaluator 验收
  ↓
Kernel 决定完成 / 重试 / 阻塞 / 审批 / 下一个节点
```

---

## 3. 设计目标与非目标

### 3.1 设计目标

#### 目标 1：Kernel-owned Workflow

工作流状态必须由 Kernel 决定，而不是由 Agent 自己决定。

Agent 可以：

- 执行任务。
- 调用工具。
- 生成代码。
- 提交结果。
- 提出下一步建议。

Agent 不应该直接：

- 判断整个 issue 是否最终完成。
- 自行推进 plan 状态。
- 自行创建下游任务。
- 绕过 evaluator。
- 绕过人工审批策略。

#### 目标 2：Plan Graph

复杂任务需要被表示为有向图，而不是单个 issue。

一个 issue 可以对应一个 plan。

一个 plan 可以包含多个 node。

node 之间通过 edge 表示依赖关系。

#### 目标 3：Structured Execution

Agent 执行结果必须结构化。

结果应该包含：

- summary
- artifacts
- changed files
- test result
- claims
- risks
- next actions
- confidence

#### 目标 4：Evidence-based Evaluation

Kernel 不接受“我完成了”这种自由文本判断。

必须根据：

- acceptance criteria
- output contract
- artifacts
- test result
- reviewer result
- policy

共同判断 node 是否完成。

#### 目标 5：兼容现有 Multica

第一阶段不破坏现有 UI 和 daemon。

应保留：

- issue 表
- agent 表
- agent_task_queue 表
- daemon claim/start/complete 机制
- 现有 CLI agent runtime

新增 orchestration 层，并逐步替换 prompt-driven workflow。

---

### 3.2 非目标

第一版不做以下事情：

1. 不设计复杂工作流 DSL。
2. 不做通用 BPMN 引擎。
3. 不做完全自动多 agent swarm。
4. 不要求所有 agent CLI 原生支持结构化协议。
5. 不重写 Multica 前端。
6. 不强制所有历史 issue 迁移到 orchestration plan。
7. 不把 daemon 改造成 orchestrator。

---

## 4. 核心概念模型

### 4.1 Issue

Issue 是用户可见的工作目标。

它描述“要解决什么问题”。

Issue 不再直接等于一次 agent execution。

### 4.2 Plan

Plan 是 Kernel 为一个目标生成的执行计划。

一个 Plan 通常来源于：

- issue
- chat session
- autopilot run
- API trigger
- quick-create

Plan 负责表示“如何完成这个目标”。

### 4.3 Node

Node 是 Plan 中的最小可调度工作单元。

常见 node 类型：

| 类型 | 说明 |
|---|---|
| clarify | 澄清需求 |
| inspect | 阅读代码 / 分析现状 |
| design | 输出设计方案 |
| implement | 实现代码 |
| test | 运行测试 |
| review | 审查结果 |
| fix | 修复问题 |
| deploy | 发布部署 |
| approval | 人工审批 |
| summarize | 汇总结果 |

### 4.4 Edge

Edge 表示 node 之间的依赖关系。

常见 edge 类型：

| 类型 | 说明 |
|---|---|
| blocks | A 完成后 B 才能执行 |
| data_dep | B 需要 A 的输出作为输入 |
| approval_dep | B 需要 A 的审批结果 |
| review_dep | B 需要 A 的 review 结果 |

### 4.5 Task

Task 是一次实际的 agent 执行尝试。

一个 node 可以产生多次 task，例如：

- 第一次执行失败。
- evaluator 不通过后重试。
- review 后返工。

所以关系是：

```text
Plan 1 - N Node
Node 1 - N Task Attempt
```

### 4.6 Artifact

Artifact 是 agent 执行过程中产生的证据。

常见 artifact 类型：

| 类型 | 示例 |
|---|---|
| diff | 代码变更 |
| file | 生成文件 |
| log | 执行日志 |
| test_result | 测试结果 |
| pr | Pull Request |
| decision | 设计决策 |
| review_result | 审查结果 |
| command_output | 命令输出 |

### 4.7 Event

Event 是 orchestration 过程中的不可变事件记录。

它用于：

- 审计
- 回放
- 调试
- UI 时间线
- 状态重建
- 训练和优化

### 4.8 Evaluator

Evaluator 是验收器。

它根据 node contract、agent result、artifacts、acceptance criteria、policy 判断节点是否通过。

---

## 5. 总体架构

### 5.1 分层架构

```text
┌─────────────────────────────────────────┐
│ Multica UI                              │
│ Issue Board / Plan View / Evidence View │
└─────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────┐
│ API Layer                               │
│ Issue API / Task API / Orchestration API│
└─────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────┐
│ AI-native Orchestration Kernel          │
│ Planner / Scheduler / Evaluator / Policy│
└─────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────┐
│ Existing TaskService                    │
│ Queue / Claim / Start / Complete / Fail │
└─────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────┐
│ Daemon Executor                         │
│ Local Agent CLI / Workspace / Runtime   │
└─────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────┐
│ External Tools                          │
│ Git / Tests / CLI / IDE / Browser / APIs│
└─────────────────────────────────────────┘
```

### 5.2 目录结构建议

```text
server/internal/orchestrator/
  orchestrator.go
  planner.go
  scheduler.go
  dispatcher.go
  evaluator.go
  policy.go
  state_machine.go
  artifact_store.go
  event_store.go
  contracts.go
  errors.go

server/internal/orchestrator/planner/
  simple.go
  llm_planner.go
  templates.go

server/internal/orchestrator/evaluator/
  hard_checks.go
  llm_judge.go
  test_result.go
  acceptance.go

server/internal/orchestrator/scheduler/
  dependency.go
  capacity.go
  retry.go

server/internal/orchestrator/protocol/
  node_input.go
  agent_result.go
  evaluator_result.go
```

---

## 6. 数据库设计

### 6.1 orchestration_plan

```sql
CREATE TABLE orchestration_plan (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,

    source_type TEXT NOT NULL CHECK (
        source_type IN ('issue', 'chat', 'autopilot', 'api', 'quick_create')
    ),
    source_id UUID NOT NULL,

    objective TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'planning' CHECK (
        status IN (
            'planning',
            'ready',
            'running',
            'waiting_human',
            'completed',
            'failed',
            'cancelled'
        )
    ),

    policy JSONB NOT NULL DEFAULT '{}',
    metadata JSONB NOT NULL DEFAULT '{}',

    created_by_type TEXT CHECK (created_by_type IN ('member', 'agent', 'system')),
    created_by_id UUID,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orchestration_plan_workspace
ON orchestration_plan(workspace_id);

CREATE INDEX idx_orchestration_plan_source
ON orchestration_plan(source_type, source_id);

CREATE INDEX idx_orchestration_plan_status
ON orchestration_plan(workspace_id, status);
```

### 6.2 orchestration_node

```sql
CREATE TABLE orchestration_node (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES orchestration_plan(id) ON DELETE CASCADE,

    type TEXT NOT NULL CHECK (
        type IN (
            'clarify',
            'inspect',
            'design',
            'implement',
            'test',
            'review',
            'fix',
            'deploy',
            'approval',
            'summarize'
        )
    ),

    title TEXT NOT NULL,
    description TEXT,

    status TEXT NOT NULL DEFAULT 'pending' CHECK (
        status IN (
            'pending',
            'ready',
            'dispatched',
            'running',
            'evaluating',
            'completed',
            'failed',
            'blocked',
            'waiting_human',
            'skipped',
            'cancelled'
        )
    ),

    assignee_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,

    input_contract JSONB NOT NULL DEFAULT '{}',
    output_contract JSONB NOT NULL DEFAULT '{}',
    evaluator_policy JSONB NOT NULL DEFAULT '{}',
    retry_policy JSONB NOT NULL DEFAULT '{}',
    runtime_constraints JSONB NOT NULL DEFAULT '{}',

    attempt_count INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 2,

    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orchestration_node_plan
ON orchestration_node(plan_id);

CREATE INDEX idx_orchestration_node_status
ON orchestration_node(plan_id, status);

CREATE INDEX idx_orchestration_node_agent
ON orchestration_node(assignee_agent_id, status);
```

### 6.3 orchestration_edge

```sql
CREATE TABLE orchestration_edge (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES orchestration_plan(id) ON DELETE CASCADE,
    from_node_id UUID NOT NULL REFERENCES orchestration_node(id) ON DELETE CASCADE,
    to_node_id UUID NOT NULL REFERENCES orchestration_node(id) ON DELETE CASCADE,

    type TEXT NOT NULL DEFAULT 'blocks' CHECK (
        type IN ('blocks', 'data_dep', 'approval_dep', 'review_dep')
    ),

    metadata JSONB NOT NULL DEFAULT '{}',

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE(from_node_id, to_node_id, type)
);

CREATE INDEX idx_orchestration_edge_plan
ON orchestration_edge(plan_id);

CREATE INDEX idx_orchestration_edge_to
ON orchestration_edge(to_node_id);
```

### 6.4 orchestration_event

```sql
CREATE TABLE orchestration_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES orchestration_plan(id) ON DELETE CASCADE,
    node_id UUID REFERENCES orchestration_node(id) ON DELETE SET NULL,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,

    event_type TEXT NOT NULL,

    actor_type TEXT NOT NULL CHECK (
        actor_type IN ('kernel', 'agent', 'member', 'system')
    ),
    actor_id UUID,

    payload JSONB NOT NULL DEFAULT '{}',

    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orchestration_event_plan
ON orchestration_event(plan_id, created_at);

CREATE INDEX idx_orchestration_event_node
ON orchestration_event(node_id, created_at);
```

### 6.5 orchestration_artifact

```sql
CREATE TABLE orchestration_artifact (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES orchestration_plan(id) ON DELETE CASCADE,
    node_id UUID REFERENCES orchestration_node(id) ON DELETE SET NULL,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,

    type TEXT NOT NULL CHECK (
        type IN (
            'diff',
            'file',
            'log',
            'test_result',
            'pr',
            'decision',
            'review_result',
            'command_output',
            'summary'
        )
    ),

    uri TEXT,
    content JSONB NOT NULL DEFAULT '{}',
    metadata JSONB NOT NULL DEFAULT '{}',
    content_hash TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orchestration_artifact_plan
ON orchestration_artifact(plan_id);

CREATE INDEX idx_orchestration_artifact_node
ON orchestration_artifact(node_id);

CREATE INDEX idx_orchestration_artifact_task
ON orchestration_artifact(task_id);
```

### 6.6 agent_task_queue 扩展

当前仓库已经有：

- `runtime_id`
- nullable `issue_id`
- nullable `context JSONB`
- `attempt`
- `max_attempts`
- `parent_task_id`
- `failure_reason`
- `session_id`
- `work_dir`
- `chat_session_id`
- `autopilot_run_id`
- `trigger_comment_id`

因此 orchestration 迁移只新增 plan / node 关联字段，不重复新增 `context`，也不假设 `issue_id` 必填。

```sql
ALTER TABLE agent_task_queue
ADD COLUMN orchestration_plan_id UUID REFERENCES orchestration_plan(id) ON DELETE SET NULL,
ADD COLUMN orchestration_node_id UUID REFERENCES orchestration_node(id) ON DELETE SET NULL,
ADD COLUMN orchestration_run_id UUID;

CREATE INDEX idx_agent_task_queue_orchestration_node
ON agent_task_queue(orchestration_node_id, status);

CREATE INDEX idx_agent_task_queue_orchestration_plan
ON agent_task_queue(orchestration_plan_id, status);
```

---

## 7. 状态机设计

### 7.1 Plan 状态机

```text
planning
  ↓
ready
  ↓
running
  ├── waiting_human
  │       ↓
  │    running
  ├── completed
  ├── failed
  └── cancelled
```

#### 状态说明

| 状态 | 说明 |
|---|---|
| planning | 正在生成 plan 和 node |
| ready | plan 已生成，等待调度 |
| running | 至少一个 node 正在执行或可执行 |
| waiting_human | plan 等待人工输入或审批 |
| completed | 所有必要 node 完成 |
| failed | 无法继续执行 |
| cancelled | 被用户或系统取消 |

### 7.2 Node 状态机

```text
pending
  ↓
ready
  ↓
dispatched
  ↓
running
  ↓
evaluating
  ├── completed
  ├── ready        -- retry
  ├── waiting_human
  ├── blocked
  └── failed
```

#### 状态说明

| 状态 | 说明 |
|---|---|
| pending | 等待依赖完成 |
| ready | 可被调度 |
| dispatched | 已创建 agent task，但尚未开始 |
| running | agent 正在执行 |
| evaluating | agent 已提交结果，正在验收 |
| completed | evaluator 通过 |
| ready | evaluator 不通过但允许重试 |
| waiting_human | 需要人工输入或审批 |
| blocked | 外部条件不满足 |
| failed | 重试耗尽或不可恢复失败 |
| cancelled | 被取消 |

### 7.3 Task 状态机

复用现有状态：

```text
queued
  ↓
dispatched
  ↓
running
  ├── completed
  ├── failed
  └── cancelled
```

关键区别：

```text
Task completed 不等于 Node completed。
Node completed 必须经过 Evaluator 验收。
```

---

## 8. 核心流程设计

### 8.1 Issue Assigned 触发 Orchestration

#### 触发条件

当 issue 被分配给 agent 时触发。

可选策略：

1. 所有 agent-assigned issue 都触发 orchestration。
2. 只有 workspace 开启 orchestration feature flag 时触发。
3. 只有 issue label 包含 `orchestrated` 时触发。

第一版建议使用 workspace feature flag。

#### 流程

```text
Issue assigned to agent
  ↓
Orchestrator.OnIssueAssigned(issueID)
  ↓
Create Plan
  ↓
Create Initial Node
  ↓
Mark Plan ready
  ↓
Scheduler.DispatchReadyNodes(planID)
```

#### 伪代码

```go
func (o *Orchestrator) OnIssueAssigned(ctx context.Context, issueID uuid.UUID) error {
    issue, err := o.Issues.Get(ctx, issueID)
    if err != nil {
        return err
    }

    existing, err := o.Plans.FindBySource(ctx, "issue", issue.ID)
    if err == nil && existing.IsActive() {
        return nil
    }

    plan, nodes, edges, err := o.Planner.CreatePlanFromIssue(ctx, issue)
    if err != nil {
        return err
    }

    err = o.Tx(ctx, func(tx Tx) error {
        tx.Plans.Create(plan)
        tx.Nodes.CreateMany(nodes)
        tx.Edges.CreateMany(edges)
        tx.Events.Append(plan.ID, "plan.created", ...)
        tx.Plans.MarkReady(plan.ID)
        return nil
    })
    if err != nil {
        return err
    }

    return o.DispatchReadyNodes(ctx, plan.ID)
}
```

---

### 8.2 Dispatch Ready Nodes

#### 规则

Node 可调度必须满足：

1. node.status = pending 或 ready。
2. 所有 blocks/data_dep/review_dep 依赖都 completed。
3. policy 允许自动执行。
4. assignee agent 存在且未 archived。
5. agent 有 runtime。
6. agent 当前 running task 数未超过 max_concurrent_tasks。

#### 伪代码

```go
func (o *Orchestrator) DispatchReadyNodes(ctx context.Context, planID uuid.UUID) error {
    readyNodes, err := o.Scheduler.ReadyNodes(ctx, planID)
    if err != nil {
        return err
    }

    for _, node := range readyNodes {
        if o.Policy.RequiresHumanApproval(node) {
            o.Nodes.MarkWaitingHuman(ctx, node.ID)
            o.Events.Append(ctx, planID, node.ID, "node.waiting_human", ...)
            continue
        }

        err := o.Dispatcher.DispatchNode(ctx, node)
        if err != nil {
            o.Events.Append(ctx, planID, node.ID, "node.dispatch_failed", ...)
            continue
        }
    }

    return nil
}
```

---

### 8.3 Dispatcher 创建 Agent Task

#### 输入

```go
type DispatchNodeInput struct {
    PlanID uuid.UUID
    NodeID uuid.UUID
    AgentID uuid.UUID
    IssueID uuid.UUID
    Priority int
    Context NodeExecutionContext
}
```

#### NodeExecutionContext

```go
type NodeExecutionContext struct {
    PlanID string `json:"plan_id"`
    NodeID string `json:"node_id"`
    NodeType string `json:"node_type"`
    Objective string `json:"objective"`
    NodeTitle string `json:"node_title"`
    NodeDescription string `json:"node_description"`
    InputContract map[string]any `json:"input_contract"`
    OutputContract map[string]any `json:"output_contract"`
    AcceptanceCriteria []AcceptanceCriterion `json:"acceptance_criteria"`
    ContextRefs []ContextRef `json:"context_refs"`
    PriorArtifacts []ArtifactRef `json:"prior_artifacts"`
    Constraints RuntimeConstraints `json:"constraints"`
}
```

#### 伪代码

```go
func (d *Dispatcher) DispatchNode(ctx context.Context, node Node) error {
    context := d.ContextBuilder.BuildNodeExecutionContext(ctx, node)

    task, err := d.TaskService.EnqueueTaskForNode(ctx, EnqueueNodeTaskParams{
        PlanID: node.PlanID,
        NodeID: node.ID,
        AgentID: node.AssigneeAgentID,
        IssueID: node.SourceIssueID,
        Context: context,
    })
    if err != nil {
        return err
    }

    d.Nodes.MarkDispatched(ctx, node.ID, task.ID)
    d.Events.Append(ctx, node.PlanID, node.ID, "node.dispatched", map[string]any{
        "task_id": task.ID,
    })

    return nil
}
```

---

### 8.4 Agent 执行 Node

#### Prompt 改造

当前 prompt 是 issue-oriented：

```text
Your assigned issue ID is: xxx
Start by running multica issue get xxx --output json
```

目标 prompt 是 node-oriented：

```text
You are executing one orchestration node in a Multica workspace.

Plan ID: <plan_id>
Node ID: <node_id>
Node Type: implement

Objective:
<overall objective>

Current Node:
<title and description>

Input Contract:
<structured input>

Output Contract:
<required outputs>

Acceptance Criteria:
<criteria>

Rules:
- Execute only this node.
- Do not mark the issue done.
- Do not create downstream tasks.
- Do not bypass acceptance criteria.
- Submit a structured result.
```

#### Agent Result 格式

```json
{
  "status": "completed",
  "summary": "Implemented login API and added tests.",
  "artifacts": [
    {
      "type": "diff",
      "uri": "git://branch/login-api",
      "metadata": {
        "changed_files": [
          "server/internal/auth/login.go",
          "server/internal/auth/login_test.go"
        ]
      }
    },
    {
      "type": "test_result",
      "content": {
        "command": "go test ./...",
        "passed": true,
        "summary": "All tests passed"
      }
    }
  ],
  "claims": [
    "Added login endpoint",
    "Added password validation",
    "Added JWT generation",
    "Added unit tests"
  ],
  "criteria_evidence": [
    {
      "criterion": "User can login with email and password",
      "evidence": "Added POST /auth/login and unit tests"
    }
  ],
  "risks": [
    "Frontend integration not verified"
  ],
  "next_actions": [
    "Run frontend integration test"
  ],
  "confidence": 0.82
}
```

---

### 8.5 Task Completed 后触发 Evaluation

#### 流程

```text
Daemon reports task completed
  ↓
TaskService.CompleteTask
  ↓
Orchestrator.OnTaskCompleted
  ↓
Node status = evaluating
  ↓
Store artifacts
  ↓
Evaluator evaluates result
  ↓
Pass?
  ├── yes: node completed
  │       ↓
  │    Dispatch downstream nodes
  │
  └── no:
          ├── retry allowed: node ready
          ├── needs human: waiting_human
          └── retry exhausted: failed
```

#### 伪代码

```go
func (o *Orchestrator) OnTaskCompleted(
    ctx context.Context,
    task AgentTask,
    result AgentResult,
) error {
    if task.OrchestrationNodeID == nil {
        return nil
    }

    node, err := o.Nodes.Get(ctx, *task.OrchestrationNodeID)
    if err != nil {
        return err
    }

    err = o.Tx(ctx, func(tx Tx) error {
        tx.Nodes.MarkEvaluating(node.ID)
        tx.Artifacts.StoreFromAgentResult(task, result)
        tx.Events.Append(node.PlanID, node.ID, "task.completed", map[string]any{
            "task_id": task.ID,
            "result": result,
        })
        return nil
    })
    if err != nil {
        return err
    }

    eval, err := o.Evaluator.Evaluate(ctx, node, result)
    if err != nil {
        return err
    }

    return o.ApplyEvaluationResult(ctx, node, task, eval)
}
```

---

### 8.6 Apply Evaluation Result

```go
func (o *Orchestrator) ApplyEvaluationResult(
    ctx context.Context,
    node Node,
    task AgentTask,
    eval EvalResult,
) error {
    if eval.Pass {
        err := o.Tx(ctx, func(tx Tx) error {
            tx.Nodes.MarkCompleted(node.ID)
            tx.Events.Append(node.PlanID, node.ID, "node.completed", eval)
            return nil
        })
        if err != nil {
            return err
        }

        if err := o.MaybeCompletePlan(ctx, node.PlanID); err != nil {
            return err
        }

        return o.DispatchReadyNodes(ctx, node.PlanID)
    }

    if o.RetryPolicy.CanRetry(node, eval) {
        return o.RetryNode(ctx, node, eval)
    }

    if eval.RecommendedAction == "ask_human" || eval.RecommendedAction == "review" {
        return o.MarkNodeWaitingHuman(ctx, node, eval)
    }

    return o.MarkNodeFailed(ctx, node, eval)
}
```

---

## 9. Planner 设计

### 9.1 Planner 分级策略

第一版 Planner 不需要过度智能。

建议分三档：

| 任务复杂度 | Plan 结构 |
|---|---|
| simple | implement |
| medium | inspect → implement → test |
| complex | clarify/design → implement → test → review → approval |

### 9.2 简单 Planner

第一阶段可以使用 rule-based planner。

```go
func (p *SimplePlanner) CreatePlanFromIssue(issue Issue) PlanSpec {
    if issue.HasAcceptanceCriteria() || issue.Priority == "urgent" {
        return p.CreateMediumPlan(issue)
    }

    return PlanSpec{
        Nodes: []NodeSpec{
            {
                Type: "implement",
                Title: "Implement issue: " + issue.Title,
                AssigneeAgentID: issue.AssigneeID,
                OutputContract: map[string]any{
                    "required": []string{"summary", "artifacts"},
                },
            },
        },
    }
}
```

### 9.3 LLM Planner

第二阶段可以引入 LLM Planner。

输入：

```json
{
  "issue": {
    "title": "实现登录功能",
    "description": "...",
    "acceptance_criteria": [...],
    "context_refs": [...]
  },
  "workspace_policy": {...},
  "available_agents": [...]
}
```

输出：

```json
{
  "complexity": "medium",
  "nodes": [
    {
      "key": "inspect_auth",
      "type": "inspect",
      "title": "Inspect existing auth implementation",
      "output_contract": {
        "required": ["summary", "relevant_files"]
      }
    },
    {
      "key": "implement_login",
      "type": "implement",
      "title": "Implement login API",
      "depends_on": ["inspect_auth"],
      "output_contract": {
        "required": ["diff", "test_result", "criteria_evidence"]
      }
    },
    {
      "key": "review_login",
      "type": "review",
      "title": "Review login implementation",
      "depends_on": ["implement_login"],
      "output_contract": {
        "required": ["review_result", "risks"]
      }
    }
  ]
}
```

### 9.4 Planner 约束

Planner 不能无限拆分。

建议限制：

```text
simple: 1 node
medium: 3 nodes以内
complex: 7 nodes以内
```

Planner 生成结果必须经过 schema validation。

---

## 10. Scheduler 设计

### 10.1 Scheduler 输入

```go
type SchedulerInput struct {
    PlanID uuid.UUID
    Nodes []Node
    Edges []Edge
    AgentCapacity map[uuid.UUID]AgentCapacity
    Policy PlanPolicy
}
```

### 10.2 Scheduler 输出

```go
type SchedulingDecision struct {
    ReadyNodes []uuid.UUID
    BlockedNodes []BlockedReason
    WaitingHumanNodes []uuid.UUID
}
```

### 10.3 Ready 判断

```go
func IsNodeReady(node Node, graph Graph) bool {
    if node.Status != "pending" && node.Status != "ready" {
        return false
    }

    deps := graph.IncomingEdges(node.ID)
    for _, dep := range deps {
        upstream := graph.Node(dep.FromNodeID)
        if upstream.Status != "completed" {
            return false
        }
    }

    return true
}
```

### 10.4 Capacity 判断

```go
func HasAgentCapacity(agent Agent) bool {
    running := CountRunningTasks(agent.ID)
    return running < agent.MaxConcurrentTasks
}
```

### 10.5 调度优先级

优先级建议：

```text
urgent issue > high priority issue > unblocked old node > normal node
```

可实现为：

```go
score = issuePriorityScore + waitingTimeScore + dependencyScore
```

---

## 11. Evaluator 设计

### 11.1 Evaluator 分层

Evaluator 建议分为三层：

```text
Hard Check
  ↓
Contract Check
  ↓
LLM Judge
```

### 11.2 Hard Check

硬规则不需要 LLM。

示例：

| Node 类型 | 硬规则 |
|---|---|
| implement | 必须有 diff 或 changed_files |
| test | 必须有 test_result |
| review | 必须有 review_result |
| design | 必须有 decision / design summary |
| deploy | 必须有 deployment result |

伪代码：

```go
func HardCheck(node Node, result AgentResult) []EvalIssue {
    issues := []EvalIssue{}

    if result.Summary == "" {
        issues = append(issues, EvalIssue{"missing_summary"})
    }

    if node.Type == "implement" && !result.HasArtifactType("diff") {
        issues = append(issues, EvalIssue{"missing_diff"})
    }

    if RequiresTestResult(node) && !result.HasArtifactType("test_result") {
        issues = append(issues, EvalIssue{"missing_test_result"})
    }

    if result.HasFailedTest() {
        issues = append(issues, EvalIssue{"test_failed"})
    }

    return issues
}
```

### 11.3 Contract Check

根据 node.output_contract 判断 required 字段是否满足。

```json
{
  "required": ["summary", "diff", "test_result", "criteria_evidence"],
  "min_confidence": 0.7,
  "require_all_criteria_evidence": true
}
```

### 11.4 Acceptance Criteria Check

Issue 的 acceptance criteria 必须被映射到 evidence。

示例：

```json
{
  "criterion": "用户可以使用邮箱和密码登录",
  "evidence": "Added POST /auth/login and unit tests in login_test.go",
  "artifact_ids": ["artifact_xxx"]
}
```

如果某条 criterion 没有 evidence，则 evaluator 不通过。

### 11.5 LLM Judge

LLM Judge 用于判断语义完成度。

输入：

```json
{
  "objective": "...",
  "node": {...},
  "acceptance_criteria": [...],
  "output_contract": {...},
  "agent_result": {...},
  "artifacts": [...]
}
```

输出：

```json
{
  "pass": true,
  "score": 0.86,
  "failed_criteria": [],
  "missing_artifacts": [],
  "risks": ["Frontend integration not verified"],
  "reason": "The implementation satisfies backend login criteria and includes tests.",
  "recommended_next_action": "create_test_node"
}
```

### 11.6 EvalResult

```go
type EvalResult struct {
    Pass bool `json:"pass"`
    Score float64 `json:"score"`
    Reason string `json:"reason"`
    FailedCriteria []string `json:"failed_criteria"`
    MissingArtifacts []string `json:"missing_artifacts"`
    Risks []string `json:"risks"`
    RecommendedAction string `json:"recommended_action"`
}
```

---

## 12. Policy 设计

### 12.1 Plan Policy 示例

```json
{
  "auto_execute": true,
  "require_human_approval_before": ["deploy"],
  "require_review_for": ["implement"],
  "max_auto_retries": 2,
  "allow_agent_to_create_issues": false,
  "allow_agent_to_update_issue_status": false,
  "allowed_tools": ["git", "go test", "npm test", "multica issue get"],
  "blocked_tools": ["rm -rf", "production deploy"]
}
```

### 12.2 Workspace Policy

Workspace 可以定义默认策略：

```json
{
  "orchestration_enabled": true,
  "default_planner": "simple",
  "default_evaluator": "hard_check_plus_llm",
  "require_human_approval_for_urgent": true,
  "max_nodes_per_plan": 7,
  "max_retries_per_node": 2
}
```

### 12.3 Policy 判定点

Policy 应该参与这些决策：

1. 是否允许创建 plan。
2. 是否允许自动拆解。
3. 是否允许自动执行 node。
4. 是否需要人工审批。
5. 是否允许重试。
6. 是否允许 agent 修改 issue。
7. 是否允许创建下游 node。
8. 是否允许触发 deploy。

---

## 13. Agent / Daemon 协议设计

### 13.1 Daemon 不应该成为 Orchestrator

Daemon 只负责：

1. 连接服务端。
2. 注册 runtime。
3. claim task。
4. 准备 workspace。
5. 启动 agent CLI。
6. 收集输出。
7. 上报 task start / complete / fail。

Daemon 不负责：

1. 拆分任务。
2. 判断 node 是否完成。
3. 调度下一个 node。
4. 执行 policy。
5. 决定 retry。

### 13.2 Task Claim 响应扩展

Task claim 返回结构中增加 orchestration context。

```json
{
  "task_id": "task_xxx",
  "agent_id": "agent_xxx",
  "issue_id": "issue_xxx",
  "orchestration": {
    "plan_id": "plan_xxx",
    "node_id": "node_xxx",
    "node_type": "implement",
    "objective": "实现登录功能",
    "input_contract": {...},
    "output_contract": {...},
    "acceptance_criteria": [...],
    "prior_artifacts": [...]
  }
}
```

### 13.3 Result Submit 方式

有两种实现路径。

#### 路径 A：新增 CLI 命令

```bash
multica task complete --task-id <task_id> --result result.json
```

优点：

- 结构化稳定。
- 容易验证。
- 不依赖解析 stdout。

缺点：

- 需要 agent 遵守命令。

#### 路径 B：Daemon 解析输出

Agent 输出 fenced JSON：

```text
MULTICA_RESULT_JSON_START
{
  "status": "completed",
  "summary": "..."
}
MULTICA_RESULT_JSON_END
```

优点：

- 不需要 agent 调 CLI。

缺点：

- 解析脆弱。
- Agent 容易输出不合规。

第一版建议优先做路径 A，路径 B 作为 fallback。

---

## 14. Prompt Protocol 设计

### 14.1 Node Prompt 模板

```text
You are executing one orchestration node in a Multica workspace.

You are NOT responsible for the entire issue.
You are responsible ONLY for the current node.

Plan ID: {{plan_id}}
Node ID: {{node_id}}
Node Type: {{node_type}}

Overall Objective:
{{objective}}

Current Node:
{{node_title}}
{{node_description}}

Input Contract:
{{input_contract_json}}

Output Contract:
{{output_contract_json}}

Acceptance Criteria:
{{acceptance_criteria_json}}

Prior Artifacts:
{{prior_artifacts_json}}

Execution Rules:
1. Execute only this node.
2. Do not mark the issue as done.
3. Do not create downstream tasks.
4. Do not bypass acceptance criteria.
5. Do not claim completion without evidence.
6. Submit your final result using:

   multica task complete --task-id {{task_id}} --result <path-to-result-json>

Required Result JSON Schema:
{{result_schema_json}}
```

### 14.2 禁止事项

Prompt 中必须明确：

```text
Do NOT:
- update issue status to done
- create unrelated issues
- trigger other agents by mention
- skip tests if output contract requires tests
- claim success without artifacts
- modify orchestration state directly
```

---

## 15. API 设计

### 15.1 创建 Plan

```http
POST /api/orchestration/plans
```

请求：

```json
{
  "source_type": "issue",
  "source_id": "issue_xxx",
  "planner": "simple"
}
```

响应：

```json
{
  "plan_id": "plan_xxx",
  "status": "ready"
}
```

### 15.2 查询 Plan

```http
GET /api/orchestration/plans/:plan_id
```

响应：

```json
{
  "id": "plan_xxx",
  "status": "running",
  "objective": "...",
  "nodes": [...],
  "edges": [...]
}
```

### 15.3 查询 Plan Timeline

```http
GET /api/orchestration/plans/:plan_id/events
```

### 15.4 查询 Artifacts

```http
GET /api/orchestration/plans/:plan_id/artifacts
```

### 15.5 人工审批 Node

```http
POST /api/orchestration/nodes/:node_id/approve
```

请求：

```json
{
  "decision": "approved",
  "comment": "Looks good. Continue."
}
```

### 15.6 Retry Node

```http
POST /api/orchestration/nodes/:node_id/retry
```

### 15.7 Cancel Plan

```http
POST /api/orchestration/plans/:plan_id/cancel
```

---

## 16. UI 设计建议

### 16.1 第一版 UI 最小改动

在 issue detail 页面新增一个区域：

```text
Orchestration
- Plan status
- Current node
- Node list
- Latest evaluator result
- Artifacts
- Retry / Approve / Cancel buttons
```

### 16.2 Plan View

展示 DAG：

```text
[Inspect] → [Implement] → [Test] → [Review]
```

每个 node 展示：

- status
- assignee agent
- attempt count
- latest task
- evaluator score
- artifacts

### 16.3 Evidence View

展示：

- changed files
- test command
- test result
- agent claims
- risks
- criteria evidence
- evaluator reason

### 16.4 Timeline View

按时间展示事件：

```text
10:01 plan.created
10:02 node.dispatched inspect
10:05 task.completed
10:06 node.completed inspect
10:07 node.dispatched implement
10:16 evaluator.failed missing test_result
10:17 node.retry_scheduled
```

---

## 17. 与现有 Multica 模块的集成点

### 17.1 IssueService

当前 issue 分配触发点在 `server/internal/handler/issue.go`，主要通过 `shouldEnqueueAgentTask` 判断是否调用 `TaskService.EnqueueTaskForIssue`。

当 issue assigned to agent 时：

```go
if workspace.OrchestrationEnabled {
    orchestrator.OnIssueAssigned(ctx, issue.ID)
} else {
    taskService.EnqueueTaskForIssue(ctx, issue)
}
```

### 17.2 TaskService

新增：

```go
EnqueueTaskForNode(ctx, node, context)
```

并在 task completed / failed 后通知 orchestrator：

```go
if task.OrchestrationNodeID.Valid {
    orchestrator.OnTaskCompleted(ctx, task, result)
}
```

注意：当前 `CompleteTask` 会为 issue task 自动创建 agent comment，并广播 `task:completed`。Orchestration 接入后，需要避免 evaluator 失败时让用户误以为 issue 已完成。第一阶段建议：

1. task 仍可记录 raw result。
2. issue comment 改为“node attempt result”，不要自动表达最终完成。
3. issue status 只能由 Kernel 在 node / plan 验收后推进。

### 17.3 Daemon

BuildPrompt 增加 orchestration path：

```go
func BuildPrompt(task Task) string {
    if task.OrchestrationNodeID != "" {
        return buildNodePrompt(task)
    }

    // fallback to existing issue/chat/autopilot/quick-create prompts
}
```

### 17.4 CLI

新增：

```bash
multica task complete --task-id <id> --result result.json
multica task artifact add --task-id <id> --type test_result --file result.json
multica plan get <plan_id> --output json
multica node get <node_id> --output json
```

CLI 的 `task complete --result` 应该走 daemon 已有 task completion 权限模型，而不是普通用户权限模型。服务端需要能区分 legacy completion payload 和 orchestration structured result。

---

## 18. 第一阶段 MVP 范围

### 18.1 MVP 目标

实现最小闭环：

```text
Issue assigned
  → Create Plan
  → Create one implement Node
  → Enqueue Task
  → Agent executes
  → Submit structured result
  → Evaluator checks
  → Complete / Retry / Waiting Human
```

### 18.2 MVP 包含

1. orchestration_plan 表。
2. orchestration_node 表。
3. orchestration_event 表。
4. orchestration_artifact 表。
5. agent_task_queue 增加 orchestration 字段。
6. SimplePlanner。
7. Scheduler。
8. Dispatcher。
9. HardCheckEvaluator。
10. Node Prompt。
11. `multica task complete --result`。
12. Issue detail 页面展示 plan/node 状态。

### 18.3 MVP 不包含

1. 多 node DAG 自动拆解。
2. LLM Planner。
3. 复杂图形化 Plan View。
4. 多 agent 自动协作。
5. 自动 deploy。
6. 高级 policy DSL。

---

## 19. 演进路线

### Phase 0：代码准备

- 梳理 TaskService 生命周期。
- 梳理 daemon prompt 和 claim payload。
- 梳理 issue assigned 触发点。
- 增加 feature flag。

### Phase 1：Single Node Kernel

目标：控制权收回 Kernel。

```text
issue → plan → implement node → task → evaluator → issue status
```

完成标准：

- agent 不再直接决定 issue done。
- task completed 后必须经过 evaluator。
- evaluator 失败可 retry。

### Phase 2：Structured Result + Artifacts

目标：让 agent 结果可验证。

完成标准：

- agent 提交 JSON result。
- artifacts 入库。
- acceptance criteria 有 evidence 映射。

### Phase 3：Multi-node Plan

目标：支持 inspect → implement → test → review。

完成标准：

- node dependency 可调度。
- downstream node 自动解锁。
- plan 可展示完整 timeline。

### Phase 4：LLM Planner + LLM Evaluator

目标：让 Kernel 能智能拆分和智能验收。

完成标准：

- LLM Planner 输出 schema-valid plan。
- LLM Evaluator 输出 schema-valid eval result。
- 所有 LLM 判断有 event log。

### Phase 5：Multi-agent Orchestration

目标：支持不同 agent 执行不同 node。

完成标准：

- implementer / reviewer / tester 可分工。
- 多 agent 不通过 @ mention 触发，而通过 graph 调度。

### Phase 6：Policy-driven Autonomy

目标：按 workspace/project 策略控制自动化程度。

完成标准：

- 高风险 node 需要人工审批。
- deploy 默认人工审批。
- 可配置自动 retry 次数。

---

## 20. 风险与应对

### 风险 1：Agent 不按结构化协议输出

应对：

- 新增 `multica task complete --result` 命令。
- daemon fallback 解析 stdout。
- evaluator 对 invalid result 直接失败。

### 风险 2：Planner 拆太复杂

应对：

- 第一版只做 single node。
- LLM Planner 加 max_nodes 限制。
- 所有 plan 输出必须 schema validation。

### 风险 3：Evaluator 误判

应对：

- hard check 优先。
- LLM judge 只做语义补充。
- 低置信度进入 human review。
- evaluator 结果写入 event log。

### 风险 4：与现有 TaskService 耦合过深

应对：

- TaskService 只做 execution queue。
- Orchestrator 通过 callback 接入。
- 不把 planner/scheduler/evaluator 写进 TaskService。

### 风险 5：UI 复杂度上升

应对：

- MVP 只在 issue detail 增加 orchestration section。
- Plan graph 后续再做。

### 风险 6：多 agent 循环触发

应对：

- 禁止 agent 自由 @mention 触发下游 agent。
- 多 agent 必须通过 node graph。
- 每个 node 有 max_attempts。
- event log 检测重复循环。

---

## 21. 关键设计原则

### 21.1 Agent 是 Worker，不是 Kernel

Agent 做执行。

Kernel 做决策。

### 21.2 Prompt 不是 Workflow

Workflow 必须显式建模为 Plan / Node / Edge / Event。

Prompt 只负责指导当前 node 执行。

### 21.3 Completed 必须有 Evidence

没有 artifact、test result、criteria evidence 的 completed 不可信。

### 21.4 Task Completion 不等于 Work Completion

Task 只是一次执行尝试。

Node 通过 evaluator 才算完成。

Plan 所有必要 node 完成才算完成。

### 21.5 一切状态变化都应该事件化

每个关键状态变化都写入 orchestration_event。

---

## 22. 推荐第一版实现顺序

建议按下面顺序开发：

1. 新增数据库表。
2. 新增 orchestrator 包。
3. 实现 SimplePlanner。
4. 实现 EnqueueTaskForNode。
5. 扩展 agent_task_queue。
6. 扩展 daemon claim payload。
7. 实现 buildNodePrompt。
8. 新增 `multica task complete --result`。
9. 实现 HardCheckEvaluator。
10. 接入 TaskService complete callback。
11. 实现 retry / waiting_human。
12. Issue detail 展示 orchestration 状态。
13. 加 feature flag 灰度。

### 22.1 实现验收清单

后续实现完成时，至少需要逐项验证：

| 项目 | 验收证据 |
|---|---|
| 数据库迁移 | 新增 orchestration_plan / node / edge / event / artifact 表，agent_task_queue 仅新增 orchestration_plan_id / orchestration_node_id / orchestration_run_id |
| sqlc 查询 | 有 plan、node、edge、event、artifact 的 Create / Get / List / UpdateStatus 查询，并已重新生成代码 |
| feature flag | workspace 未启用 orchestration 时，原 `EnqueueTaskForIssue` 路径行为不变 |
| issue 分配入口 | agent-assigned issue 在 flag 开启时创建 plan + initial node，而不是直接把整个 issue 交给 agent |
| single-node planner | 简单 issue 生成一个 implement node，保留 issue acceptance_criteria 和 context_refs |
| task enqueue | node dispatch 后创建 agent_task_queue 记录，并写入 orchestration_plan_id / orchestration_node_id |
| claim payload | daemon claim response 带 orchestration context，legacy task response 仍兼容 |
| prompt | orchestration task 使用 node prompt，明确禁止 agent 自行 mark issue done / 创建下游任务 |
| structured result | `multica task complete --result` 能提交 JSON result，并由服务端 schema validation |
| artifact store | result 中 artifacts 入库，能按 plan / node / task 查询 |
| evaluator | HardCheckEvaluator 能拒绝缺 summary、缺 required artifact、失败测试、缺 criteria_evidence 的结果 |
| retry | evaluator 不通过且未超过 max_attempts 时，node 回到 ready 并创建下一次 task attempt |
| waiting human | 低置信度或需要审批时 node / plan 进入 waiting_human |
| issue status | issue done 只能由 Kernel 在 plan 完成后推进，task completed 不能直接代表 issue 完成 |
| event log | plan.created、node.dispatched、task.completed、node.completed、node.failed、node.retry_scheduled 等关键状态变化都有 event |
| UI | issue detail 能看到 plan status、current node、latest evaluator result、artifacts、retry / approve / cancel 操作 |
| 回归验证 | `make test`、`pnpm typecheck`、相关 Vitest 通过；新增 orchestration 单测覆盖 planner / scheduler / evaluator / task callback |

---

## 23. 最终目标形态

最终 Multica 的 AI-native orchestration kernel 应该具备以下能力：

```text
用户提出目标
  ↓
Kernel 理解目标
  ↓
Kernel 拆分计划
  ↓
Kernel 调度 agent
  ↓
Agent 执行具体节点
  ↓
Kernel 收集证据
  ↓
Kernel 验收结果
  ↓
Kernel 决定下一步
  ↓
人类只在关键节点介入
```

最终产品定位可以是：

> AI-native orchestration kernel for autonomous software work.

或者更直接：

> 一个能计划、调度、验证和审计 AI agent 工作的执行内核。

---

## 24. 总结

在 Multica 基础上做 orchestration，最重要的不是增加更多 agent，而是增加一个真正的 Kernel。

这个 Kernel 应该拥有：

1. Plan Graph。
2. Node State Machine。
3. Scheduler。
4. Dispatcher。
5. Structured Agent Result。
6. Artifact Store。
7. Evaluator。
8. Policy Engine。
9. Event Log。
10. Human Approval Gate。

第一版不要贪大。

最小闭环就是：

```text
Issue → Plan → Node → Task → Result → Evaluator → Next Decision
```

只要这个闭环跑通，Multica 就从“让 agent 接任务”升级成了“由 kernel 编排 agent 工作”。
