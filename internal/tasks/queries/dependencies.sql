-- name: AddDependency :one
INSERT INTO sitetrack.task_dependencies(project_id,dependent_task_id,prerequisite_task_id,status)
VALUES($1,$2,$3,'waiting') RETURNING *;

-- name: RemoveDependency :execrows
DELETE FROM sitetrack.task_dependencies WHERE project_id=$1 AND dependent_task_id=$2 AND prerequisite_task_id=$3;

-- name: ListDependencies :many
SELECT d.prerequisite_task_id,d.status,t.name,t.status AS task_status
FROM sitetrack.task_dependencies d JOIN sitetrack.tasks t ON t.id=d.prerequisite_task_id
WHERE d.project_id=$1 AND d.dependent_task_id=$2 AND (sqlc.arg(after_id)::text='' OR d.prerequisite_task_id>sqlc.arg(after_id))
ORDER BY d.prerequisite_task_id LIMIT sqlc.arg(page_size);

-- We deduplicate node/depth pairs before calculating shortest distances.
-- name: ListRelationships :many
WITH RECURSIVE paths(id,steps) AS (
    SELECT CASE sqlc.arg(direction)::text WHEN 'prerequisites' THEN d.prerequisite_task_id ELSE d.dependent_task_id END AS id,1::integer AS steps
    FROM sitetrack.task_dependencies d WHERE d.project_id=sqlc.arg(project_id) AND
      ((sqlc.arg(direction)::text='prerequisites' AND d.dependent_task_id=sqlc.arg(task_id)) OR (sqlc.arg(direction)::text='dependents' AND d.prerequisite_task_id=sqlc.arg(task_id)))
    UNION
    SELECT CASE sqlc.arg(direction)::text WHEN 'prerequisites' THEN d.prerequisite_task_id ELSE d.dependent_task_id END,p.steps+1
    FROM paths p JOIN sitetrack.task_dependencies d ON
      ((sqlc.arg(direction)::text='prerequisites' AND d.dependent_task_id=p.id) OR (sqlc.arg(direction)::text='dependents' AND d.prerequisite_task_id=p.id))
    WHERE d.project_id=sqlc.arg(project_id)
), distances AS (SELECT paths.id,min(paths.steps)::integer AS steps FROM paths GROUP BY paths.id)
SELECT t.id,t.name,t.status,d.steps FROM distances d JOIN sitetrack.tasks t ON t.id=d.id
WHERE (sqlc.arg(after_id)::text='' OR (d.steps,t.id)>(sqlc.arg(after_steps)::integer,sqlc.arg(after_id)))
ORDER BY d.steps,t.id LIMIT sqlc.arg(page_size);
