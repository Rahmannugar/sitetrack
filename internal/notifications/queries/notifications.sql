-- name: ListNotifications :many
SELECT n.id,n.project_id,n.invitation_id,n.type,n.title,n.message,n.read_at,n.created_at,i.status AS invitation_status
FROM sitetrack.notifications n
JOIN sitetrack.projects p ON p.id=n.project_id
JOIN sitetrack.users u ON u.id=sqlc.arg(actor)
LEFT JOIN sitetrack.project_memberships m ON m.project_id=p.id AND m.user_id=u.id
LEFT JOIN sitetrack.project_invitations i ON i.id=n.invitation_id
WHERE n.user_id=sqlc.arg(actor) AND p.deleted_at IS NULL AND (i.invitee_id=u.id OR m.user_id IS NOT NULL OR u.role='superadmin')
AND (sqlc.narg(after_time)::timestamptz IS NULL OR (n.created_at,n.id)<(sqlc.narg(after_time),sqlc.narg(after_id)::uuid))
ORDER BY n.created_at DESC,n.id DESC LIMIT sqlc.arg(page_size);

-- name: UnreadCount :one
SELECT count(*)
FROM sitetrack.notifications n
JOIN sitetrack.projects p ON p.id=n.project_id
JOIN sitetrack.users u ON u.id=sqlc.arg(actor)
LEFT JOIN sitetrack.project_memberships m ON m.project_id=p.id AND m.user_id=u.id
LEFT JOIN sitetrack.project_invitations i ON i.id=n.invitation_id
WHERE n.user_id=sqlc.arg(actor) AND n.read_at IS NULL AND p.deleted_at IS NULL AND (i.invitee_id=u.id OR m.user_id IS NOT NULL OR u.role='superadmin');

-- name: ReadNotification :execrows
UPDATE sitetrack.notifications SET read_at=COALESCE(read_at,clock_timestamp()),updated_at=clock_timestamp()
WHERE id IN (SELECT n.id
FROM sitetrack.notifications n
JOIN sitetrack.projects p ON p.id=n.project_id
JOIN sitetrack.users u ON u.id=sqlc.arg(actor)
LEFT JOIN sitetrack.project_memberships m ON m.project_id=p.id AND m.user_id=u.id
LEFT JOIN sitetrack.project_invitations i ON i.id=n.invitation_id
WHERE n.id=sqlc.arg(id) AND n.user_id=sqlc.arg(actor) AND p.deleted_at IS NULL AND (i.invitee_id=u.id OR m.user_id IS NOT NULL OR u.role='superadmin'));

-- name: ReadAllNotifications :exec
UPDATE sitetrack.notifications SET read_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE id IN (SELECT n.id
FROM sitetrack.notifications n
JOIN sitetrack.projects p ON p.id=n.project_id
JOIN sitetrack.users u ON u.id=sqlc.arg(actor)
LEFT JOIN sitetrack.project_memberships m ON m.project_id=p.id AND m.user_id=u.id
LEFT JOIN sitetrack.project_invitations i ON i.id=n.invitation_id
WHERE n.user_id=sqlc.arg(actor) AND n.read_at IS NULL AND p.deleted_at IS NULL AND (i.invitee_id=u.id OR m.user_id IS NOT NULL OR u.role='superadmin'));

-- name: StreamHead :one
SELECT COALESCE((SELECT revision FROM sitetrack.notification_streams WHERE user_id=$1),0)::bigint;

-- name: NotificationChanges :many
SELECT c.revision,n.id,n.project_id,n.invitation_id,n.type,n.title,n.message,n.read_at,n.created_at,i.status AS invitation_status, (p.deleted_at IS NULL AND (i.invitee_id=u.id OR m.user_id IS NOT NULL OR u.role='superadmin'))::boolean AS visible
FROM sitetrack.notification_changes c
JOIN sitetrack.notifications n ON n.id=c.notification_id
JOIN sitetrack.projects p ON p.id=n.project_id
JOIN sitetrack.users u ON u.id=sqlc.arg(actor)
LEFT JOIN sitetrack.project_memberships m ON m.project_id=p.id AND m.user_id=u.id
LEFT JOIN sitetrack.project_invitations i ON i.id=n.invitation_id
WHERE c.user_id=sqlc.arg(actor) AND c.revision>sqlc.arg(after_revision)
ORDER BY c.revision LIMIT 20;
