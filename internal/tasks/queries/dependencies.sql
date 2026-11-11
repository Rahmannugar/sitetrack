-- name: AddDependency :one
INSERT INTO sitetrack.task_dependencies(project_id,dependent_task_id,required_task_id,status)
VALUES($1,$2,$3,'waiting') RETURNING *;

-- name: RemoveDependency :execrows
DELETE FROM sitetrack.task_dependencies WHERE project_id=$1 AND dependent_task_id=$2 AND required_task_id=$3;

-- name: ListDependencies :many
SELECT d.required_task_id,d.status,t.name,t.status AS task_status
FROM sitetrack.task_dependencies d JOIN sitetrack.tasks t ON t.id=d.required_task_id
WHERE d.project_id=$1 AND d.dependent_task_id=$2 AND (sqlc.arg(after_id)::text='' OR d.required_task_id>sqlc.arg(after_id))
ORDER BY d.required_task_id LIMIT sqlc.arg(page_size);

-- name: ListRelationships :many
WITH RECURSIVE paths(id,steps) AS (
 SELECT d.required_task_id,1::integer FROM sitetrack.task_dependencies d
 WHERE d.project_id=sqlc.arg(project_id) AND d.dependent_task_id=sqlc.arg(task_id)
 UNION
 SELECT d.required_task_id,p.steps+1 FROM paths p
 JOIN sitetrack.task_dependencies d ON d.dependent_task_id=p.id
 WHERE d.project_id=sqlc.arg(project_id)
), distances AS (SELECT paths.id,min(paths.steps)::integer AS steps FROM paths GROUP BY paths.id)
SELECT t.id,t.name,t.status,d.steps FROM distances d JOIN sitetrack.tasks t ON t.id=d.id
WHERE (sqlc.arg(after_id)::text='' OR (d.steps,t.id)>(sqlc.arg(after_steps)::integer,sqlc.arg(after_id)))
ORDER BY d.steps,t.id LIMIT sqlc.arg(page_size);

-- name: ListDependents :many
WITH RECURSIVE paths(id,steps) AS (
 SELECT d.dependent_task_id,1::integer FROM sitetrack.task_dependencies d
 WHERE d.project_id=sqlc.arg(project_id) AND d.required_task_id=sqlc.arg(task_id)
 UNION
 SELECT d.dependent_task_id,p.steps+1 FROM paths p
 JOIN sitetrack.task_dependencies d ON d.required_task_id=p.id
 WHERE d.project_id=sqlc.arg(project_id)
), distances AS (SELECT paths.id,min(paths.steps)::integer AS steps FROM paths GROUP BY paths.id)
SELECT t.id,t.name,t.status,d.steps FROM distances d JOIN sitetrack.tasks t ON t.id=d.id
WHERE (sqlc.arg(after_id)::text='' OR (d.steps,t.id)>(sqlc.arg(after_steps)::integer,sqlc.arg(after_id)))
ORDER BY d.steps,t.id LIMIT sqlc.arg(page_size);
