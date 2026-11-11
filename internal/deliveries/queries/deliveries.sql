-- name: AccountRole :one
SELECT role IN ('company_store_manager','superadmin') AS company FROM sitetrack.users WHERE id=$1;

-- name: LockProject :one
SELECT id FROM sitetrack.projects WHERE id=$1 FOR UPDATE;

-- name: ShareProject :one
SELECT id FROM sitetrack.projects WHERE id=$1 FOR SHARE;

-- name: ProjectAccess :one
SELECT (p.deleted_at IS NOT NULL)::boolean AS removed,
 u.role IN ('company_store_manager','superadmin') AS company,
 (p.deleted_at IS NULL AND (u.role='superadmin' OR m.user_id IS NOT NULL))::boolean AS readable,
 COALESCE(m.is_admin,false)::boolean AS admin
FROM sitetrack.projects p
JOIN sitetrack.users u ON u.id=sqlc.arg(actor)
LEFT JOIN sitetrack.project_memberships m ON m.project_id=p.id AND m.user_id=u.id
WHERE p.id=sqlc.arg(project_id);

-- name: DeliveryProject :one
SELECT project_id FROM sitetrack.deliveries WHERE id=$1;

-- name: LockDelivery :one
SELECT * FROM sitetrack.deliveries WHERE id=$1 FOR UPDATE;

-- name: LockMaterials :many
SELECT id FROM sitetrack.materials WHERE id=ANY(sqlc.arg(material_ids)::uuid[]) AND deleted_at IS NULL ORDER BY id FOR UPDATE;

-- name: EnsureCompanyInventory :exec
INSERT INTO sitetrack.company_inventory(material_id) SELECT unnest(sqlc.arg(material_ids)::uuid[]) ON CONFLICT DO NOTHING;

-- name: EnsureProjectInventory :exec
INSERT INTO sitetrack.project_inventory(project_id,material_id)
SELECT sqlc.arg(project_id),unnest(sqlc.arg(material_ids)::uuid[]) ON CONFLICT DO NOTHING;

-- name: SendCompanyStock :execrows
UPDATE sitetrack.company_inventory i SET quantity=i.quantity-line.amount::numeric,in_transit_to_projects=i.in_transit_to_projects+line.amount::numeric,updated_at=clock_timestamp()
FROM (SELECT unnest(sqlc.arg(material_ids)::uuid[]) AS material_id,unnest(sqlc.arg(quantities)::text[]) AS amount) line
WHERE i.material_id=line.material_id AND i.quantity>=line.amount::numeric;

-- name: ReleaseCompanyStock :exec
UPDATE sitetrack.company_inventory i SET quantity=i.quantity+CASE WHEN sqlc.arg(return_stock)::boolean THEN line.amount::numeric ELSE 0 END,
in_transit_to_projects=i.in_transit_to_projects-line.amount::numeric,updated_at=clock_timestamp()
FROM (SELECT unnest(sqlc.arg(material_ids)::uuid[]) AS material_id,unnest(sqlc.arg(quantities)::text[]) AS amount) line
WHERE i.material_id=line.material_id;

-- name: ReceiveCompanyStock :exec
UPDATE sitetrack.company_inventory i SET quantity=i.quantity+line.amount::numeric,updated_at=clock_timestamp()
FROM (SELECT unnest(sqlc.arg(material_ids)::uuid[]) AS material_id,unnest(sqlc.arg(quantities)::text[]) AS amount) line
WHERE i.material_id=line.material_id;

-- name: SendProjectStock :execrows
UPDATE sitetrack.project_inventory i SET quantity=i.quantity-line.amount::numeric,in_transit=i.in_transit+line.amount::numeric,updated_at=clock_timestamp(),last_delivery_at=clock_timestamp()
FROM (SELECT unnest(sqlc.arg(material_ids)::uuid[]) AS material_id,unnest(sqlc.arg(quantities)::text[]) AS amount) line
WHERE i.project_id=sqlc.arg(project_id) AND i.material_id=line.material_id AND i.quantity>=line.amount::numeric;

-- name: ReleaseProjectStock :exec
UPDATE sitetrack.project_inventory i SET quantity=i.quantity+CASE WHEN sqlc.arg(return_stock)::boolean THEN line.amount::numeric ELSE 0 END,
in_transit=i.in_transit-line.amount::numeric,updated_at=clock_timestamp(),last_delivery_at=CASE WHEN sqlc.arg(return_stock)::boolean THEN clock_timestamp() ELSE i.last_delivery_at END
FROM (SELECT unnest(sqlc.arg(material_ids)::uuid[]) AS material_id,unnest(sqlc.arg(quantities)::text[]) AS amount) line
WHERE i.project_id=sqlc.arg(project_id) AND i.material_id=line.material_id;

-- name: ReceiveProjectStock :exec
UPDATE sitetrack.project_inventory i SET quantity=i.quantity+line.amount::numeric,updated_at=clock_timestamp(),last_delivery_at=clock_timestamp()
FROM (SELECT unnest(sqlc.arg(material_ids)::uuid[]) AS material_id,unnest(sqlc.arg(quantities)::text[]) AS amount) line
WHERE i.project_id=sqlc.arg(project_id) AND i.material_id=line.material_id;

-- name: CreateDelivery :exec
INSERT INTO sitetrack.deliveries(id,project_id,direction,initiated_by_user_id) VALUES($1,$2,$3,$4);

-- name: CreateItems :exec
INSERT INTO sitetrack.delivery_items(delivery_id,material_id,quantity)
SELECT sqlc.arg(delivery_id),unnest(sqlc.arg(material_ids)::uuid[]),unnest(sqlc.arg(quantities)::text[])::numeric;

-- name: CompleteDelivery :exec
UPDATE sitetrack.deliveries SET status=$2,completed_by_user_id=$3,completed_at=clock_timestamp() WHERE id=$1;

-- name: GetDelivery :one
SELECT d.*,p.name AS project_name,s.name AS initiated_by_name,r.name AS completed_by_name
FROM sitetrack.deliveries d JOIN sitetrack.projects p ON p.id=d.project_id
JOIN sitetrack.users s ON s.id=d.initiated_by_user_id LEFT JOIN sitetrack.users r ON r.id=d.completed_by_user_id
WHERE d.id=$1;

-- name: DeliveryItems :many
SELECT i.delivery_id,i.material_id,m.name,m.unit,i.quantity::text AS quantity
FROM sitetrack.delivery_items i JOIN sitetrack.materials m ON m.id=i.material_id
WHERE i.delivery_id=ANY(sqlc.arg(delivery_ids)::uuid[])
ORDER BY i.delivery_id,i.material_id;

-- name: ListProjectDeliveries :many
SELECT d.*,p.name AS project_name,s.name AS initiated_by_name,r.name AS completed_by_name
FROM sitetrack.deliveries d JOIN sitetrack.projects p ON p.id=d.project_id
JOIN sitetrack.users s ON s.id=d.initiated_by_user_id LEFT JOIN sitetrack.users r ON r.id=d.completed_by_user_id
WHERE d.project_id=sqlc.arg(project_id)
AND (sqlc.arg(direction)::text='' OR d.direction=NULLIF(sqlc.arg(direction)::text,'')::sitetrack.delivery_direction)
AND (sqlc.arg(status)::text='' OR d.status=NULLIF(sqlc.arg(status)::text,'')::sitetrack.delivery_status)
AND (sqlc.narg(from_date)::timestamptz IS NULL OR d.created_at>=sqlc.narg(from_date))
AND (sqlc.narg(to_date)::timestamptz IS NULL OR d.created_at<sqlc.narg(to_date))
AND (sqlc.narg(after_id)::uuid IS NULL OR (d.created_at,d.id)<(sqlc.narg(after_time)::timestamptz,sqlc.narg(after_id)))
ORDER BY d.created_at DESC,d.id DESC LIMIT sqlc.arg(page_size);

-- name: ListCompanyDeliveries :many
SELECT d.*,p.name AS project_name,s.name AS initiated_by_name,r.name AS completed_by_name
FROM sitetrack.deliveries d JOIN sitetrack.projects p ON p.id=d.project_id
JOIN sitetrack.users s ON s.id=d.initiated_by_user_id LEFT JOIN sitetrack.users r ON r.id=d.completed_by_user_id
WHERE (sqlc.arg(direction)::text='' OR d.direction=NULLIF(sqlc.arg(direction)::text,'')::sitetrack.delivery_direction)
AND (sqlc.arg(status)::text='' OR d.status=NULLIF(sqlc.arg(status)::text,'')::sitetrack.delivery_status)
AND (sqlc.narg(from_date)::timestamptz IS NULL OR d.created_at>=sqlc.narg(from_date))
AND (sqlc.narg(to_date)::timestamptz IS NULL OR d.created_at<sqlc.narg(to_date))
AND (sqlc.narg(after_id)::uuid IS NULL OR (d.created_at,d.id)<(sqlc.narg(after_time)::timestamptz,sqlc.narg(after_id)))
ORDER BY d.created_at DESC,d.id DESC LIMIT sqlc.arg(page_size);
