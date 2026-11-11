-- name: CompanyAccess :one
SELECT role IN ('company_store_manager','superadmin') AS allowed FROM sitetrack.users WHERE id=$1;

-- name: LockProject :one
SELECT id FROM sitetrack.projects WHERE id=$1 AND deleted_at IS NULL FOR SHARE;

-- name: ProjectAccess :one
SELECT p.id
FROM sitetrack.projects p
JOIN sitetrack.users u ON u.id=sqlc.arg(actor)
LEFT JOIN sitetrack.project_memberships m ON m.project_id=p.id AND m.user_id=u.id
WHERE p.id=sqlc.arg(project_id) AND p.deleted_at IS NULL AND (u.role='superadmin' OR m.user_id IS NOT NULL);

-- name: LockMaterial :one
SELECT id FROM sitetrack.materials WHERE id=$1 AND deleted_at IS NULL FOR UPDATE;

-- name: AddStock :exec
INSERT INTO sitetrack.company_store(material_id,quantity)
VALUES(sqlc.arg(material_id),sqlc.arg(quantity)::text::numeric)
ON CONFLICT(material_id) DO UPDATE SET quantity=company_store.quantity+EXCLUDED.quantity,updated_at=clock_timestamp();

-- name: SetReorderPoint :exec
INSERT INTO sitetrack.company_store(material_id,reorder_point)
VALUES(sqlc.arg(material_id),sqlc.arg(reorder_point)::text::numeric)
ON CONFLICT(material_id) DO UPDATE SET reorder_point=EXCLUDED.reorder_point,updated_at=clock_timestamp();

-- name: CompanyItem :one
SELECT material_id,name,category_id,category_name,unit,quantity::text,reorder_point::text,in_transit_to_projects::text,status,updated_at
FROM sitetrack.company_stock WHERE material_id=$1;

-- name: ProjectItem :one
SELECT material_id,name,category_id,category_name,unit,quantity::text,last_delivery_at,updated_at
FROM sitetrack.project_stock WHERE project_id=$1 AND material_id=$2;

-- name: ListCompanyStock :many
SELECT material_id,name,category_id,category_name,unit,quantity::text,reorder_point::text,in_transit_to_projects::text,status,updated_at,sort_name
FROM sitetrack.company_stock
WHERE (sqlc.arg(search)::text='' OR lower(name) LIKE lower(sqlc.arg(search)))
AND (sqlc.narg(category_id)::uuid IS NULL OR category_id=sqlc.narg(category_id))
AND (sqlc.arg(status)::text='' OR status=NULLIF(sqlc.arg(status)::text,'')::sitetrack.stock_status)
AND (sqlc.narg(after_id)::uuid IS NULL OR
 (sqlc.arg(sort_by)::text='name' AND
  ((sqlc.arg(sort_order)::text='asc' AND (sort_name,material_id)>(sqlc.arg(after_name)::text,sqlc.narg(after_id))) OR
   (sqlc.arg(sort_order)::text='desc' AND (sort_name,material_id)<(sqlc.arg(after_name)::text,sqlc.narg(after_id))))) OR
 (sqlc.arg(sort_by)::text='quantity' AND
  ((sqlc.arg(sort_order)::text='asc' AND (quantity,material_id)>(sqlc.arg(after_quantity)::text::numeric,sqlc.narg(after_id))) OR
   (sqlc.arg(sort_order)::text='desc' AND (quantity,material_id)<(sqlc.arg(after_quantity)::text::numeric,sqlc.narg(after_id))))) OR
 (sqlc.arg(sort_by)::text='status' AND
  ((sqlc.arg(sort_order)::text='asc' AND (status,material_id)>(sqlc.arg(after_status)::text::sitetrack.stock_status,sqlc.narg(after_id))) OR
   (sqlc.arg(sort_order)::text='desc' AND (status,material_id)<(sqlc.arg(after_status)::text::sitetrack.stock_status,sqlc.narg(after_id))))))
ORDER BY
 CASE WHEN sqlc.arg(sort_by)::text='name' AND sqlc.arg(sort_order)::text='asc' THEN sort_name END ASC,
 CASE WHEN sqlc.arg(sort_by)::text='name' AND sqlc.arg(sort_order)::text='desc' THEN sort_name END DESC,
 CASE WHEN sqlc.arg(sort_by)::text='quantity' AND sqlc.arg(sort_order)::text='asc' THEN quantity END ASC,
 CASE WHEN sqlc.arg(sort_by)::text='quantity' AND sqlc.arg(sort_order)::text='desc' THEN quantity END DESC,
 CASE WHEN sqlc.arg(sort_by)::text='status' AND sqlc.arg(sort_order)::text='asc' THEN status END ASC,
 CASE WHEN sqlc.arg(sort_by)::text='status' AND sqlc.arg(sort_order)::text='desc' THEN status END DESC,
 CASE WHEN sqlc.arg(sort_order)::text='asc' THEN material_id END ASC,
 CASE WHEN sqlc.arg(sort_order)::text='desc' THEN material_id END DESC
LIMIT sqlc.arg(page_size);

-- name: ListProjectStock :many
SELECT material_id,name,category_id,category_name,unit,quantity::text,last_delivery_at,updated_at,sort_name
FROM sitetrack.project_stock
WHERE project_id=sqlc.arg(project_id)
AND (sqlc.arg(search)::text='' OR lower(name) LIKE lower(sqlc.arg(search)))
AND (sqlc.narg(category_id)::uuid IS NULL OR category_id=sqlc.narg(category_id))
AND (sqlc.narg(after_id)::uuid IS NULL OR
 (sqlc.arg(sort_by)::text='name' AND
  ((sqlc.arg(sort_order)::text='asc' AND (sort_name,material_id)>(sqlc.arg(after_name)::text,sqlc.narg(after_id))) OR
   (sqlc.arg(sort_order)::text='desc' AND (sort_name,material_id)<(sqlc.arg(after_name)::text,sqlc.narg(after_id))))) OR
 (sqlc.arg(sort_by)::text='quantity' AND
  ((sqlc.arg(sort_order)::text='asc' AND (quantity,material_id)>(sqlc.arg(after_quantity)::text::numeric,sqlc.narg(after_id))) OR
   (sqlc.arg(sort_order)::text='desc' AND (quantity,material_id)<(sqlc.arg(after_quantity)::text::numeric,sqlc.narg(after_id))))) OR
 (sqlc.arg(sort_by)::text='last_delivery' AND
  ((sqlc.narg(after_delivery)::timestamptz IS NULL AND last_delivery_at IS NULL AND
    ((sqlc.arg(sort_order)::text='asc' AND material_id>sqlc.narg(after_id)) OR (sqlc.arg(sort_order)::text='desc' AND material_id<sqlc.narg(after_id)))) OR
   (sqlc.narg(after_delivery)::timestamptz IS NOT NULL AND (last_delivery_at IS NULL OR
    (sqlc.arg(sort_order)::text='asc' AND (last_delivery_at,material_id)>(sqlc.narg(after_delivery),sqlc.narg(after_id))) OR
    (sqlc.arg(sort_order)::text='desc' AND (last_delivery_at,material_id)<(sqlc.narg(after_delivery),sqlc.narg(after_id))))))))
ORDER BY
 CASE WHEN sqlc.arg(sort_by)::text='name' AND sqlc.arg(sort_order)::text='asc' THEN sort_name END ASC,
 CASE WHEN sqlc.arg(sort_by)::text='name' AND sqlc.arg(sort_order)::text='desc' THEN sort_name END DESC,
 CASE WHEN sqlc.arg(sort_by)::text='quantity' AND sqlc.arg(sort_order)::text='asc' THEN quantity END ASC,
 CASE WHEN sqlc.arg(sort_by)::text='quantity' AND sqlc.arg(sort_order)::text='desc' THEN quantity END DESC,
 CASE WHEN sqlc.arg(sort_by)::text='last_delivery' AND sqlc.arg(sort_order)::text='asc' THEN last_delivery_at END ASC NULLS LAST,
 CASE WHEN sqlc.arg(sort_by)::text='last_delivery' AND sqlc.arg(sort_order)::text='desc' THEN last_delivery_at END DESC NULLS LAST,
 CASE WHEN sqlc.arg(sort_order)::text='asc' THEN material_id END ASC,
 CASE WHEN sqlc.arg(sort_order)::text='desc' THEN material_id END DESC
LIMIT sqlc.arg(page_size);
