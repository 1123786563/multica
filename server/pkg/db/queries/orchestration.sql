-- name: CreateOrchestrationPlan :one
INSERT INTO orchestration_plan (
    workspace_id, source_type, source_id, objective, status,
    policy, metadata, created_by_type, created_by_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, sqlc.narg('created_by_type'), sqlc.narg('created_by_id'))
RETURNING *;

-- name: GetOrchestrationPlan :one
SELECT * FROM orchestration_plan
WHERE id = $1;

-- name: ListOrchestrationPlansBySource :many
SELECT * FROM orchestration_plan
WHERE source_type = $1 AND source_id = $2
ORDER BY created_at DESC;

-- name: GetActiveOrchestrationPlanBySource :one
SELECT * FROM orchestration_plan
WHERE source_type = $1 AND source_id = $2
  AND status NOT IN ('completed', 'failed', 'cancelled')
ORDER BY created_at DESC
LIMIT 1;

-- name: UpdateOrchestrationPlanStatus :exec
UPDATE orchestration_plan SET status = $2, updated_at = now()
WHERE id = $1;

-- name: CreateOrchestrationNode :one
INSERT INTO orchestration_node (
    plan_id, type, title, description, status, assignee_agent_id,
    input_contract, output_contract, evaluator_policy, retry_policy, runtime_constraints,
    max_attempts
) VALUES ($1, $2, $3, sqlc.narg('description'), $4, sqlc.narg('assignee_agent_id'), $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetOrchestrationNode :one
SELECT * FROM orchestration_node
WHERE id = $1;

-- name: ListOrchestrationNodesByPlan :many
SELECT * FROM orchestration_node
WHERE plan_id = $1
ORDER BY created_at ASC;

-- name: CreateOrchestrationEdge :one
INSERT INTO orchestration_edge (plan_id, from_node_id, to_node_id, type, metadata)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListOrchestrationEdgesByPlan :many
SELECT * FROM orchestration_edge
WHERE plan_id = $1
ORDER BY created_at ASC;

-- name: MarkOrchestrationNodeDispatched :one
UPDATE orchestration_node
SET status = 'dispatched', attempt_count = attempt_count + 1, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkOrchestrationNodeRunning :exec
UPDATE orchestration_node
SET status = 'running', started_at = COALESCE(started_at, now()), updated_at = now()
WHERE id = $1;

-- name: MarkOrchestrationNodeEvaluating :exec
UPDATE orchestration_node
SET status = 'evaluating', updated_at = now()
WHERE id = $1;

-- name: CompleteOrchestrationNode :exec
UPDATE orchestration_node
SET status = 'completed', completed_at = now(), updated_at = now()
WHERE id = $1;

-- name: FailOrchestrationNode :exec
UPDATE orchestration_node
SET status = 'failed', completed_at = now(), updated_at = now()
WHERE id = $1;

-- name: ReadyOrchestrationNode :exec
UPDATE orchestration_node
SET status = 'ready', updated_at = now()
WHERE id = $1;

-- name: WaitOrchestrationNodeForHuman :exec
UPDATE orchestration_node
SET status = 'waiting_human', updated_at = now()
WHERE id = $1;

-- name: CreateOrchestrationEvent :one
INSERT INTO orchestration_event (plan_id, node_id, task_id, event_type, actor_type, actor_id, payload)
VALUES ($1, sqlc.narg('node_id'), sqlc.narg('task_id'), $2, $3, sqlc.narg('actor_id'), $4)
RETURNING *;

-- name: ListOrchestrationEventsByPlan :many
SELECT * FROM orchestration_event
WHERE plan_id = $1
ORDER BY created_at ASC;

-- name: CreateOrchestrationArtifact :one
INSERT INTO orchestration_artifact (plan_id, node_id, task_id, type, uri, content, metadata, content_hash)
VALUES ($1, sqlc.narg('node_id'), sqlc.narg('task_id'), $2, sqlc.narg('uri'), $3, $4, sqlc.narg('content_hash'))
RETURNING *;

-- name: ListOrchestrationArtifactsByPlan :many
SELECT * FROM orchestration_artifact
WHERE plan_id = $1
ORDER BY created_at ASC;

-- name: CreateOrchestrationNodeTask :one
INSERT INTO agent_task_queue (
    agent_id, runtime_id, issue_id, status, priority, context,
    orchestration_plan_id, orchestration_node_id, orchestration_run_id,
    force_fresh_session
)
VALUES ($1, $2, $3, 'queued', $4, $5, $6, $7, $8, COALESCE(sqlc.narg('force_fresh_session')::boolean, FALSE))
RETURNING id, agent_id, issue_id, status, priority, dispatched_at, started_at, completed_at, result, error, created_at, context, runtime_id, session_id, work_dir, trigger_comment_id, chat_session_id, autopilot_run_id, attempt, max_attempts, parent_task_id, failure_reason, trigger_summary, force_fresh_session;
