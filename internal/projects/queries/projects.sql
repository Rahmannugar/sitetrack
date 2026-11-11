-- name: NextProjectNumber :one
SELECT nextval('sitetrack.project_number_seq')::bigint;

-- name: CreateProject :one
INSERT INTO sitetrack.projects (id, name, start_date, end_date)
VALUES ($1, $2, $3, $4) RETURNING *;

-- name: CreateMembership :exec
INSERT INTO sitetrack.project_memberships (project_id, user_id, is_admin) VALUES ($1, $2, $3);

-- name: LockProject :one
SELECT id FROM sitetrack.projects WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;

-- name: ShareProject :one
SELECT id FROM sitetrack.projects WHERE id = $1 AND deleted_at IS NULL FOR SHARE;

-- name: GetProject :one
SELECT p.*, COALESCE(m.is_admin, false)::boolean AS is_admin,
    (SELECT count(*) FROM sitetrack.project_memberships team WHERE team.project_id = p.id)::bigint AS member_count,summary.task_count,summary.not_started_count,summary.in_progress_count,summary.done_count,summary.late_count,summary.blocked_count,summary.completion_percentage
FROM sitetrack.projects p
JOIN sitetrack.users u ON u.id = sqlc.arg(actor)
LEFT JOIN sitetrack.project_memberships m ON m.project_id = p.id AND m.user_id = u.id
CROSS JOIN LATERAL (
 SELECT count(*)::bigint AS task_count,
 count(*) FILTER(WHERE t.status='not_started')::bigint AS not_started_count,
 count(*) FILTER(WHERE t.status='in_progress')::bigint AS in_progress_count,
 count(*) FILTER(WHERE t.status='done')::bigint AS done_count,
 count(*) FILTER(WHERE t.status<>'done' AND (t.end_date<(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date OR (t.status='not_started' AND t.start_date<(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date)))::bigint AS late_count,
 count(*) FILTER(WHERE t.status<>'done' AND EXISTS(SELECT 1 FROM sitetrack.task_dependencies d WHERE d.dependent_task_id=t.id AND d.status='waiting'))::bigint AS blocked_count,
 COALESCE(round(100.0*(count(*) FILTER(WHERE t.status='done'))/NULLIF(count(*),0),2),0)::double precision AS completion_percentage
 FROM sitetrack.tasks t WHERE t.project_id=p.id AND t.deleted_at IS NULL
) summary
WHERE p.id = sqlc.arg(id) AND p.deleted_at IS NULL AND (u.role = 'superadmin' OR m.user_id IS NOT NULL);

-- name: ListProjects :many
SELECT p.*, COALESCE(m.is_admin, false)::boolean AS is_admin,
    (SELECT count(*) FROM sitetrack.project_memberships team WHERE team.project_id = p.id)::bigint AS member_count,summary.task_count,summary.not_started_count,summary.in_progress_count,summary.done_count,summary.late_count,(SELECT count(*) FROM sitetrack.tasks blocked_task WHERE blocked_task.project_id=p.id AND blocked_task.deleted_at IS NULL AND blocked_task.status<>'done' AND EXISTS(SELECT 1 FROM sitetrack.task_dependencies edge WHERE edge.dependent_task_id=blocked_task.id AND edge.status='waiting'))::bigint AS blocked_count,summary.completion_percentage
FROM sitetrack.projects p
JOIN sitetrack.users u ON u.id = sqlc.arg(actor)
LEFT JOIN sitetrack.project_memberships m ON m.project_id = p.id AND m.user_id = u.id
CROSS JOIN LATERAL (
 SELECT count(*)::bigint AS task_count,
 count(*) FILTER(WHERE t.status='not_started')::bigint AS not_started_count,
 count(*) FILTER(WHERE t.status='in_progress')::bigint AS in_progress_count,
 count(*) FILTER(WHERE t.status='done')::bigint AS done_count,
 count(*) FILTER(WHERE t.status<>'done' AND (t.end_date<(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date OR (t.status='not_started' AND t.start_date<(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date)))::bigint AS late_count,
 COALESCE(round(100.0*(count(*) FILTER(WHERE t.status='done'))/NULLIF(count(*),0),2),0)::double precision AS completion_percentage
 FROM sitetrack.tasks t WHERE t.project_id=p.id AND t.deleted_at IS NULL
) summary
WHERE p.deleted_at IS NULL AND (u.role = 'superadmin' OR m.user_id IS NOT NULL)
AND (sqlc.arg(search)::text = '' OR position(lower(sqlc.arg(search)) in lower(p.name)) > 0 OR position(lower(sqlc.arg(search)) in lower(p.id)) > 0)
AND (NOT sqlc.arg(late_only)::boolean OR summary.late_count>0)
AND (sqlc.arg(after_id)::text='' OR
 (sqlc.arg(sort_by)::text IN ('id','name') AND
  (CASE sqlc.arg(sort_by)::text WHEN 'name' THEN p.name ELSE p.id END,p.id)>(sqlc.arg(after_value)::text,sqlc.arg(after_id))) OR
 (sqlc.arg(sort_by)::text='start_date' AND (p.start_date,p.id)>(sqlc.narg(after_date)::date,sqlc.arg(after_id))) OR
 (sqlc.arg(sort_by)::text='completion_percentage' AND (summary.completion_percentage,p.id)>(sqlc.arg(after_percentage)::double precision,sqlc.arg(after_id))) OR
 (sqlc.arg(sort_by)::text='late_count' AND (summary.late_count,p.id)>(sqlc.arg(after_count)::bigint,sqlc.arg(after_id))))
ORDER BY
 CASE WHEN sqlc.arg(sort_by)::text='name' THEN p.name END,
 CASE WHEN sqlc.arg(sort_by)::text='start_date' THEN p.start_date END,
 CASE WHEN sqlc.arg(sort_by)::text='completion_percentage' THEN summary.completion_percentage END,
 CASE WHEN sqlc.arg(sort_by)::text='late_count' THEN summary.late_count END,p.id
LIMIT sqlc.arg(page_size);

-- name: ListProjectsBySummary :many
WITH visible AS MATERIALIZED (
 SELECT p.*,COALESCE(m.is_admin,false)::boolean AS is_admin
 FROM sitetrack.projects p JOIN sitetrack.users u ON u.id=sqlc.arg(actor)
 LEFT JOIN sitetrack.project_memberships m ON m.project_id=p.id AND m.user_id=u.id
 WHERE p.deleted_at IS NULL AND (u.role='superadmin' OR m.user_id IS NOT NULL)
 AND (sqlc.arg(search)::text='' OR position(lower(sqlc.arg(search)) in lower(p.name))>0 OR position(lower(sqlc.arg(search)) in lower(p.id))>0)
), totals AS MATERIALIZED (
 SELECT t.project_id,count(*)::bigint AS task_count,
 count(*) FILTER(WHERE t.status='not_started')::bigint AS not_started_count,
 count(*) FILTER(WHERE t.status='in_progress')::bigint AS in_progress_count,
 count(*) FILTER(WHERE t.status='done')::bigint AS done_count,
 count(*) FILTER(WHERE t.status<>'done' AND (t.end_date<(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date OR (t.status='not_started' AND t.start_date<(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date)))::bigint AS late_count,
 COALESCE(round(100.0*(count(*) FILTER(WHERE t.status='done'))/NULLIF(count(*),0),2),0)::double precision AS completion_percentage
 FROM sitetrack.tasks t JOIN visible scope ON scope.id=t.project_id
 WHERE t.deleted_at IS NULL GROUP BY t.project_id
)
SELECT p.id,p.name,p.start_date,p.end_date,p.created_at,p.deleted_at,p.task_number,p.is_admin,
    (SELECT count(*) FROM sitetrack.project_memberships team WHERE team.project_id = p.id)::bigint AS member_count,COALESCE(summary.task_count,0)::bigint AS task_count,COALESCE(summary.not_started_count,0)::bigint AS not_started_count,COALESCE(summary.in_progress_count,0)::bigint AS in_progress_count,COALESCE(summary.done_count,0)::bigint AS done_count,COALESCE(summary.late_count,0)::bigint AS late_count,(SELECT count(*) FROM sitetrack.tasks blocked_task WHERE blocked_task.project_id=p.id AND blocked_task.deleted_at IS NULL AND blocked_task.status<>'done' AND EXISTS(SELECT 1 FROM sitetrack.task_dependencies edge WHERE edge.dependent_task_id=blocked_task.id AND edge.status='waiting'))::bigint AS blocked_count,COALESCE(summary.completion_percentage,0)::double precision AS completion_percentage
FROM visible p
LEFT JOIN totals summary ON summary.project_id=p.id

WHERE (NOT sqlc.arg(late_only)::boolean OR COALESCE(summary.late_count,0)>0)
AND (sqlc.arg(after_id)::text='' OR
 (sqlc.arg(sort_by)::text IN ('id','name') AND
  (CASE sqlc.arg(sort_by)::text WHEN 'name' THEN p.name ELSE p.id END,p.id)>(sqlc.arg(after_value)::text,sqlc.arg(after_id))) OR
 (sqlc.arg(sort_by)::text='start_date' AND (p.start_date,p.id)>(sqlc.narg(after_date)::date,sqlc.arg(after_id))) OR
 (sqlc.arg(sort_by)::text='completion_percentage' AND (COALESCE(summary.completion_percentage,0),p.id)>(sqlc.arg(after_percentage)::double precision,sqlc.arg(after_id))) OR
 (sqlc.arg(sort_by)::text='late_count' AND (COALESCE(summary.late_count,0),p.id)>(sqlc.arg(after_count)::bigint,sqlc.arg(after_id))))
ORDER BY
 CASE WHEN sqlc.arg(sort_by)::text='name' THEN p.name END,
 CASE WHEN sqlc.arg(sort_by)::text='start_date' THEN p.start_date END,
 CASE WHEN sqlc.arg(sort_by)::text='completion_percentage' THEN COALESCE(summary.completion_percentage,0) END,
 CASE WHEN sqlc.arg(sort_by)::text='late_count' THEN COALESCE(summary.late_count,0) END,p.id
LIMIT sqlc.arg(page_size);


-- name: UpdateProject :exec
UPDATE sitetrack.projects SET name = $2, start_date = $3, end_date = $4 WHERE id = $1;

-- name: RemoveProject :exec
UPDATE sitetrack.projects SET deleted_at = now() WHERE id = $1;

-- name: ListMembers :many
SELECT m.user_id, u.name, m.is_admin, m.joined_at
FROM sitetrack.project_memberships m JOIN sitetrack.users u ON u.id = m.user_id
WHERE m.project_id = $1 AND (sqlc.narg(after_id)::uuid IS NULL OR m.user_id > sqlc.narg(after_id))
ORDER BY m.user_id LIMIT sqlc.arg(page_size);

-- name: GetMember :one
SELECT user_id, is_admin FROM sitetrack.project_memberships WHERE project_id = $1 AND user_id = $2;

-- name: CountAdmins :one
SELECT count(*) FROM sitetrack.project_memberships WHERE project_id = $1 AND is_admin;

-- name: UpdateMember :exec
UPDATE sitetrack.project_memberships SET is_admin = $3 WHERE project_id = $1 AND user_id = $2;

-- name: RemoveMember :exec
DELETE FROM sitetrack.project_memberships WHERE project_id = $1 AND user_id = $2;

-- name: ProjectAccess :one
SELECT COALESCE(m.is_admin, false)::boolean AS is_admin
FROM sitetrack.users u
LEFT JOIN sitetrack.project_memberships m ON m.project_id = sqlc.arg(project_id) AND m.user_id = u.id
WHERE u.id = sqlc.arg(actor) AND (u.role = 'superadmin' OR m.user_id IS NOT NULL);
