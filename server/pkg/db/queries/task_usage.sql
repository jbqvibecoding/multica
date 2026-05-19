-- name: UpsertTaskUsage :exec
-- Bumps `updated_at` on INSERT and on conflict so the daily-rollup worker
-- (migration 073) detects the row as dirty and re-aggregates its bucket.
-- Without the conflict-side bump, a correction to historical token counts
-- would never propagate to the rollup.
INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (task_id, provider, model)
DO UPDATE SET
    input_tokens = EXCLUDED.input_tokens,
    output_tokens = EXCLUDED.output_tokens,
    cache_read_tokens = EXCLUDED.cache_read_tokens,
    cache_write_tokens = EXCLUDED.cache_write_tokens,
    updated_at = now();

-- name: GetTaskUsage :many
SELECT * FROM task_usage
WHERE task_id = $1
ORDER BY model;

-- name: GetWorkspaceUsageByDay :many
-- Bucket by tu.created_at (usage report time, ~= task completion time), not
-- atq.created_at (task enqueue time), so tasks that queue one day and execute
-- the next are attributed to the day tokens were actually produced. The since
-- cutoff is truncated to start-of-day so `days=N` yields full calendar days.
SELECT
    DATE(tu.created_at) AS date,
    tu.model,
    SUM(tu.input_tokens)::bigint AS total_input_tokens,
    SUM(tu.output_tokens)::bigint AS total_output_tokens,
    SUM(tu.cache_read_tokens)::bigint AS total_cache_read_tokens,
    SUM(tu.cache_write_tokens)::bigint AS total_cache_write_tokens,
    COUNT(DISTINCT tu.task_id)::int AS task_count
FROM task_usage tu
JOIN agent_task_queue atq ON atq.id = tu.task_id
JOIN agent a ON a.id = atq.agent_id
WHERE a.workspace_id = $1
  AND tu.created_at >= DATE_TRUNC('day', @since::timestamptz)
GROUP BY DATE(tu.created_at), tu.model
ORDER BY DATE(tu.created_at) DESC, tu.model;

-- name: GetWorkspaceUsageSummary :many
-- Filter by tu.created_at (usage report time), aligned to start-of-day, so
-- `days=N` is interpreted as N full calendar days like the other usage queries.
SELECT
    tu.model,
    SUM(tu.input_tokens)::bigint AS total_input_tokens,
    SUM(tu.output_tokens)::bigint AS total_output_tokens,
    SUM(tu.cache_read_tokens)::bigint AS total_cache_read_tokens,
    SUM(tu.cache_write_tokens)::bigint AS total_cache_write_tokens,
    COUNT(DISTINCT tu.task_id)::int AS task_count
FROM task_usage tu
JOIN agent_task_queue atq ON atq.id = tu.task_id
JOIN agent a ON a.id = atq.agent_id
WHERE a.workspace_id = $1
  AND tu.created_at >= DATE_TRUNC('day', @since::timestamptz)
GROUP BY tu.model
ORDER BY (SUM(tu.input_tokens) + SUM(tu.output_tokens)) DESC;

-- name: GetIssueUsageSummary :one
SELECT
    COALESCE(SUM(tu.input_tokens), 0)::bigint AS total_input_tokens,
    COALESCE(SUM(tu.output_tokens), 0)::bigint AS total_output_tokens,
    COALESCE(SUM(tu.cache_read_tokens), 0)::bigint AS total_cache_read_tokens,
    COALESCE(SUM(tu.cache_write_tokens), 0)::bigint AS total_cache_write_tokens,
    COUNT(DISTINCT tu.task_id)::int AS task_count
FROM task_usage tu
JOIN agent_task_queue atq ON atq.id = tu.task_id
WHERE atq.issue_id = $1;

-- name: ListDashboardUsageDaily :many
-- Daily per-(date, model) token aggregates for the workspace, optionally
-- scoped to a single project AND/OR a single squad. Bucketed by
-- tu.created_at (token-production time) to match GetWorkspaceUsageByDay,
-- so a task that queues one day and finishes the next is attributed to
-- the day the tokens actually landed. Powers the workspace dashboard's
-- daily cost chart.
--
-- The squad predicate is two-layered: (1) the issue must be assigned to
-- the squad (`issue.assignee_type='squad' AND issue.assignee_id = :squad_id`),
-- AND (2) the task's agent must be a member of that squad — either a
-- `squad_member` row of type 'agent', or the squad's leader_id. A task
-- run by a non-member agent on a squad-assigned issue is NOT counted in
-- that squad's usage. The rollup variant below does NOT carry a squad
-- dimension; the handler forces this raw-stream query whenever a squad
-- filter is requested.
SELECT
    DATE(tu.created_at) AS date,
    tu.model,
    SUM(tu.input_tokens)::bigint AS input_tokens,
    SUM(tu.output_tokens)::bigint AS output_tokens,
    SUM(tu.cache_read_tokens)::bigint AS cache_read_tokens,
    SUM(tu.cache_write_tokens)::bigint AS cache_write_tokens,
    COUNT(DISTINCT tu.task_id)::int AS task_count
FROM task_usage tu
JOIN agent_task_queue atq ON atq.id = tu.task_id
JOIN agent a ON a.id = atq.agent_id
LEFT JOIN issue i ON i.id = atq.issue_id
WHERE a.workspace_id = $1
  AND tu.created_at >= DATE_TRUNC('day', @since::timestamptz)
  AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('squad_id')::uuid IS NULL
       OR (i.assignee_type = 'squad' AND i.assignee_id = sqlc.narg('squad_id')
           AND atq.agent_id IN (
               SELECT sm.member_id FROM squad_member sm
               WHERE sm.squad_id = sqlc.narg('squad_id') AND sm.member_type = 'agent'
               UNION
               SELECT s.leader_id FROM squad s WHERE s.id = sqlc.narg('squad_id')
           )))
GROUP BY DATE(tu.created_at), tu.model
ORDER BY DATE(tu.created_at) DESC, tu.model;

-- name: ListDashboardUsageByAgent :many
-- Per-(agent, model) token aggregates for the workspace, optionally scoped
-- to a single project AND/OR a single squad (see ListDashboardUsageDaily
-- for the squad-predicate rationale). Model dimension is preserved so the
-- client can compute cost from its per-model pricing table; the client
-- folds rows by agent for the "by agent" list on the dashboard.
SELECT
    atq.agent_id,
    tu.model,
    SUM(tu.input_tokens)::bigint AS input_tokens,
    SUM(tu.output_tokens)::bigint AS output_tokens,
    SUM(tu.cache_read_tokens)::bigint AS cache_read_tokens,
    SUM(tu.cache_write_tokens)::bigint AS cache_write_tokens,
    COUNT(DISTINCT tu.task_id)::int AS task_count
FROM task_usage tu
JOIN agent_task_queue atq ON atq.id = tu.task_id
JOIN agent a ON a.id = atq.agent_id
LEFT JOIN issue i ON i.id = atq.issue_id
WHERE a.workspace_id = $1
  AND tu.created_at >= DATE_TRUNC('day', @since::timestamptz)
  AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('squad_id')::uuid IS NULL
       OR (i.assignee_type = 'squad' AND i.assignee_id = sqlc.narg('squad_id')
           AND atq.agent_id IN (
               SELECT sm.member_id FROM squad_member sm
               WHERE sm.squad_id = sqlc.narg('squad_id') AND sm.member_type = 'agent'
               UNION
               SELECT s.leader_id FROM squad s WHERE s.id = sqlc.narg('squad_id')
           )))
GROUP BY atq.agent_id, tu.model
ORDER BY atq.agent_id, tu.model;

-- name: ListDashboardUsageDailyRollup :many
-- Daily token rollup, served from `task_usage_dashboard_daily` (migration
-- 084). Same wire shape as ListDashboardUsageDaily so the handler can
-- swap them on the `UseDailyRollupForDashboard` flag with no other
-- changes. The rollup is up to ~10 min stale (5 min cron + 5 min lag),
-- which is fine for a dashboard read path.
SELECT
    bucket_date AS date,
    model,
    SUM(input_tokens)::bigint        AS input_tokens,
    SUM(output_tokens)::bigint       AS output_tokens,
    SUM(cache_read_tokens)::bigint   AS cache_read_tokens,
    SUM(cache_write_tokens)::bigint  AS cache_write_tokens,
    SUM(task_count)::int             AS task_count
FROM task_usage_dashboard_daily
WHERE workspace_id = $1
  AND bucket_date >= DATE_TRUNC('day', @since::timestamptz)::date
  AND (sqlc.narg('project_id')::uuid IS NULL OR project_id = sqlc.narg('project_id'))
GROUP BY bucket_date, model
ORDER BY bucket_date DESC, model;

-- name: ListDashboardUsageByAgentRollup :many
-- Per-(agent, model) token rollup from `task_usage_dashboard_daily`.
-- task_count here is the SUM of per-bucket distinct counts; one task that
-- spans multiple days lands in multiple buckets, so this can over-count
-- by date. The frontend prefers `ListDashboardAgentRunTime`'s per-agent
-- distinct figure for the user-facing "tasks" column, so this value is
-- informational only.
SELECT
    agent_id,
    model,
    SUM(input_tokens)::bigint        AS input_tokens,
    SUM(output_tokens)::bigint       AS output_tokens,
    SUM(cache_read_tokens)::bigint   AS cache_read_tokens,
    SUM(cache_write_tokens)::bigint  AS cache_write_tokens,
    SUM(task_count)::int             AS task_count
FROM task_usage_dashboard_daily
WHERE workspace_id = $1
  AND bucket_date >= DATE_TRUNC('day', @since::timestamptz)::date
  AND (sqlc.narg('project_id')::uuid IS NULL OR project_id = sqlc.narg('project_id'))
GROUP BY agent_id, model
ORDER BY agent_id, model;

-- name: ListDashboardRunTimeDaily :many
-- Daily per-date run time + task counts for the workspace, optionally
-- scoped to a single project AND/OR a single squad. Powers the workspace
-- dashboard's "Time" and "Tasks" metrics on the same toggle as Tokens /
-- Cost. Bucketed by completed_at (terminal time) — same anchor as
-- ListDashboardAgentRunTime so the day boundaries line up with the
-- per-agent run-time card. Only terminal tasks (completed or failed)
-- with both started_at and completed_at populated contribute.
SELECT
    DATE(atq.completed_at) AS date,
    COALESCE(
        SUM(EXTRACT(EPOCH FROM (atq.completed_at - atq.started_at)))::bigint,
        0
    )::bigint AS total_seconds,
    COUNT(*)::int AS task_count,
    COUNT(*) FILTER (WHERE atq.status = 'failed')::int AS failed_count
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
LEFT JOIN issue i ON i.id = atq.issue_id
WHERE a.workspace_id = $1
  AND atq.status IN ('completed', 'failed')
  AND atq.started_at IS NOT NULL
  AND atq.completed_at IS NOT NULL
  AND atq.completed_at >= DATE_TRUNC('day', @since::timestamptz)
  AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('squad_id')::uuid IS NULL
       OR (i.assignee_type = 'squad' AND i.assignee_id = sqlc.narg('squad_id')
           AND atq.agent_id IN (
               SELECT sm.member_id FROM squad_member sm
               WHERE sm.squad_id = sqlc.narg('squad_id') AND sm.member_type = 'agent'
               UNION
               SELECT s.leader_id FROM squad s WHERE s.id = sqlc.narg('squad_id')
           )))
GROUP BY DATE(atq.completed_at)
ORDER BY DATE(atq.completed_at) DESC;

-- name: ListDashboardAgentRunTime :many
-- Per-agent total task run time and task count for the workspace, optionally
-- scoped to a single project AND/OR a single squad. Counts only terminal
-- runs (completed or failed) with both started_at and completed_at
-- populated — queued/running tasks have no finite duration. Anchored on
-- completed_at so the window matches the token cost window (which is
-- anchored on tu.created_at, ~= completion time).
SELECT
    atq.agent_id,
    COALESCE(
        SUM(EXTRACT(EPOCH FROM (atq.completed_at - atq.started_at)))::bigint,
        0
    )::bigint AS total_seconds,
    COUNT(*)::int AS task_count,
    COUNT(*) FILTER (WHERE atq.status = 'failed')::int AS failed_count
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
LEFT JOIN issue i ON i.id = atq.issue_id
WHERE a.workspace_id = $1
  AND atq.status IN ('completed', 'failed')
  AND atq.started_at IS NOT NULL
  AND atq.completed_at IS NOT NULL
  AND atq.completed_at >= DATE_TRUNC('day', @since::timestamptz)
  AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('squad_id')::uuid IS NULL
       OR (i.assignee_type = 'squad' AND i.assignee_id = sqlc.narg('squad_id')
           AND atq.agent_id IN (
               SELECT sm.member_id FROM squad_member sm
               WHERE sm.squad_id = sqlc.narg('squad_id') AND sm.member_type = 'agent'
               UNION
               SELECT s.leader_id FROM squad s WHERE s.id = sqlc.narg('squad_id')
           )))
GROUP BY atq.agent_id
ORDER BY total_seconds DESC;
