-- name: LockProject :one
SELECT id FROM sitetrack.projects WHERE id=$1 AND deleted_at IS NULL FOR UPDATE;

-- name: ShareProject :one
SELECT id FROM sitetrack.projects WHERE id=$1 AND deleted_at IS NULL FOR SHARE;

-- name: ProjectAccess :one
SELECT COALESCE(m.is_admin,false)::boolean AS is_admin FROM sitetrack.users u LEFT JOIN sitetrack.project_memberships m ON m.user_id=u.id AND m.project_id=sqlc.arg(project_id)
WHERE u.id=sqlc.arg(actor) AND (u.role='superadmin' OR m.user_id IS NOT NULL);

-- name: NextTaskNumber :one
UPDATE sitetrack.projects SET task_number=task_number+1 WHERE id=$1 RETURNING task_number;

-- name: CreateTask :exec
INSERT INTO sitetrack.tasks(id,project_id,name,start_date,end_date) VALUES($1,$2,$3,$4,$5);

-- name: UpdateTask :exec
UPDATE sitetrack.tasks SET name=$3,start_date=$4,end_date=$5 WHERE project_id=$1 AND id=$2 AND deleted_at IS NULL;

-- name: TransitionTask :exec
UPDATE sitetrack.tasks SET status=$3 WHERE project_id=$1 AND id=$2 AND deleted_at IS NULL;

-- name: RemoveTask :exec
UPDATE sitetrack.tasks SET deleted_at=clock_timestamp() WHERE project_id=$1 AND id=$2 AND deleted_at IS NULL;

-- name: RemoveTaskDependencies :exec
DELETE FROM sitetrack.task_dependencies WHERE project_id=$1 AND dependent_task_id=$2;

-- We require completed dependencies on started tasks, so direct waiting dependencies determine Blocked.
-- name: GetTask :one
SELECT t.*, EXISTS(SELECT 1 FROM sitetrack.project_memberships m WHERE m.project_id=t.project_id AND m.user_id=sqlc.arg(actor) AND m.is_admin)::boolean AS is_admin, (t.status<>'done' AND (t.end_date<sqlc.arg(today)::date OR (t.status='not_started' AND t.start_date<sqlc.arg(today))))::boolean AS late,
    (t.status<>'done' AND EXISTS(SELECT 1 FROM sitetrack.task_dependencies d WHERE d.dependent_task_id=t.id AND d.status='waiting'))::boolean AS blocked,
    (SELECT count(*) FROM sitetrack.task_dependencies d WHERE d.dependent_task_id=t.id)::bigint AS dependency_count,
    (SELECT count(*) FROM sitetrack.task_dependencies d WHERE d.required_task_id=t.id)::bigint AS dependent_count,
    ARRAY(SELECT d.required_task_id FROM sitetrack.task_dependencies d WHERE d.dependent_task_id=t.id ORDER BY d.required_task_id LIMIT 20)::text[] AS depends_on,
    NOT EXISTS(SELECT 1 FROM sitetrack.task_dependencies d JOIN sitetrack.tasks successor ON successor.id=d.dependent_task_id WHERE d.required_task_id=t.id AND successor.status<>'not_started') AS can_reopen
FROM sitetrack.tasks t WHERE t.project_id=sqlc.arg(project_id) AND t.id=sqlc.arg(id) AND t.deleted_at IS NULL;

-- name: ListTasks :many
WITH candidates AS (
SELECT t.*, EXISTS(SELECT 1 FROM sitetrack.project_memberships m WHERE m.project_id=t.project_id AND m.user_id=sqlc.arg(actor) AND m.is_admin)::boolean AS is_admin, (t.status<>'done' AND (t.end_date<sqlc.arg(today)::date OR (t.status='not_started' AND t.start_date<sqlc.arg(today))))::boolean AS late,
    (t.status<>'done' AND EXISTS(SELECT 1 FROM sitetrack.task_dependencies d WHERE d.dependent_task_id=t.id AND d.status='waiting'))::boolean AS blocked,
    (SELECT count(*) FROM sitetrack.task_dependencies d WHERE d.dependent_task_id=t.id)::bigint AS dependency_count,
    (SELECT count(*) FROM sitetrack.task_dependencies d WHERE d.required_task_id=t.id)::bigint AS dependent_count,
    ARRAY(SELECT d.required_task_id FROM sitetrack.task_dependencies d WHERE d.dependent_task_id=t.id ORDER BY d.required_task_id LIMIT 20)::text[] AS depends_on,
    NOT EXISTS(SELECT 1 FROM sitetrack.task_dependencies d JOIN sitetrack.tasks successor ON successor.id=d.dependent_task_id WHERE d.required_task_id=t.id AND successor.status<>'not_started') AS can_reopen
FROM sitetrack.tasks t WHERE t.project_id=sqlc.arg(project_id) AND t.deleted_at IS NULL
)
SELECT * FROM candidates t
WHERE (sqlc.arg(status)::text='' OR t.status=NULLIF(sqlc.arg(status)::text,'')::sitetrack.task_status)
AND (sqlc.arg(search)::text='' OR position(lower(sqlc.arg(search)) in lower(t.name))>0 OR position(lower(sqlc.arg(search)) in lower(t.id))>0)
AND (sqlc.narg(late)::boolean IS NULL OR t.late=sqlc.narg(late))
AND (sqlc.narg(blocked)::boolean IS NULL OR t.blocked=sqlc.narg(blocked))
AND (sqlc.narg(from_date)::date IS NULL OR t.end_date>=sqlc.narg(from_date))
AND (sqlc.narg(to_date)::date IS NULL OR t.start_date<=sqlc.narg(to_date))
AND (sqlc.arg(after_id)::text='' OR
    (sqlc.arg(sort_by)::text='dependent_count' AND
        ((sqlc.arg(sort_order)::text='asc' AND (t.dependent_count,t.id)>(sqlc.arg(after_count)::bigint,sqlc.arg(after_id))) OR
         (sqlc.arg(sort_order)::text='desc' AND (t.dependent_count,t.id)<(sqlc.arg(after_count)::bigint,sqlc.arg(after_id))))) OR
    (sqlc.arg(sort_by)::text IN ('start_date','end_date') AND
        ((sqlc.arg(sort_order)::text='asc' AND (CASE sqlc.arg(sort_by)::text WHEN 'start_date' THEN t.start_date ELSE t.end_date END,t.id)>(sqlc.narg(after_date)::date,sqlc.arg(after_id))) OR
         (sqlc.arg(sort_order)::text='desc' AND (CASE sqlc.arg(sort_by)::text WHEN 'start_date' THEN t.start_date ELSE t.end_date END,t.id)<(sqlc.narg(after_date)::date,sqlc.arg(after_id))))) OR
    (sqlc.arg(sort_by)::text IN ('id','name') AND
        ((sqlc.arg(sort_order)::text='asc' AND (CASE sqlc.arg(sort_by)::text WHEN 'name' THEN t.name ELSE t.id END,t.id)>(sqlc.arg(after_value)::text,sqlc.arg(after_id))) OR
         (sqlc.arg(sort_order)::text='desc' AND (CASE sqlc.arg(sort_by)::text WHEN 'name' THEN t.name ELSE t.id END,t.id)<(sqlc.arg(after_value)::text,sqlc.arg(after_id))))))
ORDER BY
 CASE WHEN sqlc.arg(sort_order)::text='asc' AND sqlc.arg(sort_by)::text='dependent_count' THEN t.dependent_count END ASC,
 CASE WHEN sqlc.arg(sort_order)::text='desc' AND sqlc.arg(sort_by)::text='dependent_count' THEN t.dependent_count END DESC,
 CASE WHEN sqlc.arg(sort_order)::text='asc' THEN CASE sqlc.arg(sort_by)::text WHEN 'start_date' THEN t.start_date WHEN 'end_date' THEN t.end_date END END ASC,
 CASE WHEN sqlc.arg(sort_order)::text='desc' THEN CASE sqlc.arg(sort_by)::text WHEN 'start_date' THEN t.start_date WHEN 'end_date' THEN t.end_date END END DESC,
 CASE WHEN sqlc.arg(sort_order)::text='asc' THEN CASE sqlc.arg(sort_by)::text WHEN 'name' THEN t.name WHEN 'id' THEN t.id END END ASC,
 CASE WHEN sqlc.arg(sort_order)::text='desc' THEN CASE sqlc.arg(sort_by)::text WHEN 'name' THEN t.name WHEN 'id' THEN t.id END END DESC,
 CASE WHEN sqlc.arg(sort_order)::text='asc' THEN t.id END ASC,
 CASE WHEN sqlc.arg(sort_order)::text='desc' THEN t.id END DESC
LIMIT sqlc.arg(page_size);
