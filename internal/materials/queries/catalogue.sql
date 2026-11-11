-- name: AccountAccess :one
SELECT role IN ('company_store_manager','superadmin') AS can_manage FROM sitetrack.users WHERE id=$1;

-- name: CreateCategory :one
INSERT INTO sitetrack.material_categories(id,name) VALUES($1,$2) RETURNING *;

-- name: GetCategory :one
SELECT * FROM sitetrack.material_categories WHERE id=$1 AND deleted_at IS NULL;

-- name: LockCategory :one
SELECT * FROM sitetrack.material_categories WHERE id=$1 AND deleted_at IS NULL FOR UPDATE;

-- name: UpdateCategory :one
UPDATE sitetrack.material_categories SET name=$2,updated_at=clock_timestamp() WHERE id=$1 AND deleted_at IS NULL RETURNING *;

-- name: CategoryInUse :one
SELECT EXISTS(SELECT 1 FROM sitetrack.materials WHERE category_id=$1 AND deleted_at IS NULL);

-- name: RemoveCategory :execrows
UPDATE sitetrack.material_categories SET deleted_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND deleted_at IS NULL;

-- name: ListCategories :many
SELECT *,lower(name) AS sort_name FROM sitetrack.material_categories WHERE deleted_at IS NULL
AND (sqlc.arg(search)::text='' OR lower(name) LIKE lower(sqlc.arg(search)))
AND (sqlc.narg(after_id)::uuid IS NULL OR (lower(name),id)>(sqlc.arg(after_name)::text,sqlc.narg(after_id)))
ORDER BY lower(name),id LIMIT sqlc.arg(page_size);

-- name: CreateMaterial :one
INSERT INTO sitetrack.materials(id,name,category_id,unit) VALUES($1,$2,$3,$4) RETURNING *;

-- name: GetMaterial :one
SELECT m.*,c.name AS category_name,lower(m.name) AS sort_name FROM sitetrack.materials m JOIN sitetrack.material_categories c ON c.id=m.category_id
WHERE m.id=$1 AND m.deleted_at IS NULL;

-- name: LockMaterial :one
SELECT * FROM sitetrack.materials WHERE id=$1 AND deleted_at IS NULL FOR UPDATE;

-- name: UpdateMaterial :exec
UPDATE sitetrack.materials SET name=$2,category_id=$3,unit=$4,updated_at=clock_timestamp() WHERE id=$1 AND deleted_at IS NULL;

-- name: RemoveMaterial :execrows
UPDATE sitetrack.materials SET deleted_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND deleted_at IS NULL;

-- name: ListMaterials :many
SELECT m.*,c.name AS category_name,lower(m.name) AS sort_name FROM sitetrack.materials m JOIN sitetrack.material_categories c ON c.id=m.category_id
WHERE m.deleted_at IS NULL
AND (sqlc.arg(search)::text='' OR lower(m.name) LIKE lower(sqlc.arg(search)))
AND (sqlc.narg(category_id)::uuid IS NULL OR m.category_id=sqlc.narg(category_id))
AND (sqlc.narg(after_id)::uuid IS NULL OR (lower(m.name),m.id)>(sqlc.arg(after_name)::text,sqlc.narg(after_id)))
ORDER BY lower(m.name),m.id LIMIT sqlc.arg(page_size);
