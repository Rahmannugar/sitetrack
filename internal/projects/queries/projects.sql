-- name: NextProjectNumber :one
SELECT nextval('sitetrack.project_number_seq')::bigint;

-- name: CreateProject :one
INSERT INTO sitetrack.projects (id, name, start_date, end_date)
VALUES ($1, $2, $3, $4) RETURNING *;

-- name: CreateProjectStore :exec
INSERT INTO sitetrack.project_stores (id, project_id, name) VALUES ($1, $2, $3);

-- name: CreateMembership :exec
INSERT INTO sitetrack.project_memberships (project_id, user_id, is_admin) VALUES ($1, $2, $3);

-- name: LockProject :one
SELECT id FROM sitetrack.projects WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;

-- name: ShareProject :one
SELECT id FROM sitetrack.projects WHERE id = $1 AND deleted_at IS NULL FOR SHARE;

-- name: GetProject :one
SELECT p.*, s.id AS store_id, COALESCE(m.is_admin, false)::boolean AS is_admin,
    (SELECT count(*) FROM sitetrack.project_memberships team WHERE team.project_id = p.id)::bigint AS member_count
FROM sitetrack.projects p
JOIN sitetrack.project_stores s ON s.project_id = p.id
JOIN sitetrack.users u ON u.id = sqlc.arg(actor)
LEFT JOIN sitetrack.project_memberships m ON m.project_id = p.id AND m.user_id = u.id
WHERE p.id = sqlc.arg(id) AND p.deleted_at IS NULL AND (u.role = 'superadmin' OR m.user_id IS NOT NULL);

-- name: ListProjects :many
SELECT p.*, s.id AS store_id, COALESCE(m.is_admin, false)::boolean AS is_admin,
    (SELECT count(*) FROM sitetrack.project_memberships team WHERE team.project_id = p.id)::bigint AS member_count
FROM sitetrack.projects p
JOIN sitetrack.project_stores s ON s.project_id = p.id
JOIN sitetrack.users u ON u.id = sqlc.arg(actor)
LEFT JOIN sitetrack.project_memberships m ON m.project_id = p.id AND m.user_id = u.id
WHERE p.deleted_at IS NULL AND (u.role = 'superadmin' OR m.user_id IS NOT NULL)
AND (sqlc.arg(search)::text = '' OR position(lower(sqlc.arg(search)) in lower(p.name)) > 0 OR position(lower(sqlc.arg(search)) in lower(p.id)) > 0)
AND (sqlc.arg(after_id)::text = '' OR
    (CASE sqlc.arg(sort_by)::text WHEN 'name' THEN p.name WHEN 'start_date' THEN to_char(p.start_date, 'YYYY-MM-DD') ELSE p.id END, p.id) > (sqlc.arg(after_value)::text, sqlc.arg(after_id)::text))
ORDER BY CASE sqlc.arg(sort_by)::text WHEN 'name' THEN p.name WHEN 'start_date' THEN to_char(p.start_date, 'YYYY-MM-DD') ELSE p.id END, p.id
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
