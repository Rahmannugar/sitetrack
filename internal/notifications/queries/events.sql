-- name: CaptureProjectRecipients :exec
INSERT INTO sitetrack.outbox_recipients(event_id,user_id)
SELECT sqlc.arg(event_id)::uuid,m.user_id FROM sitetrack.project_memberships m
WHERE m.project_id=sqlc.arg(project_id) AND m.user_id<>sqlc.arg(actor);

-- name: CaptureInvitee :exec
INSERT INTO sitetrack.outbox_recipients(event_id,user_id) VALUES($1,$2);

-- name: PendingRecipients :many
SELECT user_id FROM sitetrack.outbox_recipients WHERE event_id=$1 AND delivered_at IS NULL
ORDER BY user_id LIMIT 100;

-- name: LockNotificationJob :one
SELECT id FROM sitetrack.outbox_events WHERE id=$1 AND lease_token=$2 AND status='processing' FOR UPDATE;

-- name: InsertNotifications :exec
INSERT INTO sitetrack.notifications(id,event_id,user_id,project_id,invitation_id,type,title,message)
SELECT identifiers.id,sqlc.arg(event_id),recipients.user_id,sqlc.arg(project_id),sqlc.narg(invitation_id),sqlc.arg(type),sqlc.arg(title),sqlc.arg(message)
FROM unnest(sqlc.arg(ids)::uuid[]) WITH ORDINALITY AS identifiers(id,ordinal)
JOIN unnest(sqlc.arg(users)::uuid[]) WITH ORDINALITY AS recipients(user_id,ordinal) USING(ordinal)
JOIN sitetrack.projects p ON p.id=sqlc.arg(project_id) AND p.deleted_at IS NULL
JOIN sitetrack.users u ON u.id=recipients.user_id
LEFT JOIN sitetrack.project_memberships m ON m.project_id=p.id AND m.user_id=u.id
LEFT JOIN sitetrack.project_invitations i ON i.id=sqlc.narg(invitation_id) AND i.project_id=p.id AND i.invitee_id=u.id
WHERE i.id IS NOT NULL OR (sqlc.narg(invitation_id)::uuid IS NULL AND (m.user_id IS NOT NULL OR u.role='superadmin'))
ORDER BY recipients.user_id
ON CONFLICT(event_id,user_id) DO NOTHING;

-- name: CompleteRecipients :exec
UPDATE sitetrack.outbox_recipients SET delivered_at=clock_timestamp() WHERE event_id=sqlc.arg(event_id) AND user_id=ANY(sqlc.arg(users)::uuid[]);

-- name: TouchInvitationNotifications :exec
UPDATE sitetrack.notifications SET updated_at=clock_timestamp() WHERE invitation_id=$1;
