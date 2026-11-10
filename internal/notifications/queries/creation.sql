-- name: ProjectRecipients :many
SELECT m.user_id FROM sitetrack.project_memberships m
WHERE m.project_id=sqlc.arg(project_id) AND m.user_id<>sqlc.arg(actor)
AND (sqlc.narg(after_id)::uuid IS NULL OR m.user_id>sqlc.narg(after_id))
ORDER BY m.user_id LIMIT 100;

-- name: InsertNotifications :exec
INSERT INTO sitetrack.notifications(id,user_id,project_id,invitation_id,type,title,message)
SELECT identifiers.id,recipients.user_id,sqlc.arg(project_id),sqlc.narg(invitation_id),sqlc.arg(type),sqlc.arg(title),sqlc.arg(message)
FROM unnest(sqlc.arg(ids)::uuid[]) WITH ORDINALITY AS identifiers(id,ordinal)
JOIN unnest(sqlc.arg(users)::uuid[]) WITH ORDINALITY AS recipients(user_id,ordinal) USING(ordinal)
JOIN sitetrack.projects p ON p.id=sqlc.arg(project_id) AND p.deleted_at IS NULL
JOIN sitetrack.users u ON u.id=recipients.user_id
LEFT JOIN sitetrack.project_memberships m ON m.project_id=p.id AND m.user_id=u.id
LEFT JOIN sitetrack.project_invitations i ON i.id=sqlc.narg(invitation_id) AND i.project_id=p.id AND i.invitee_id=u.id
WHERE i.id IS NOT NULL OR (sqlc.narg(invitation_id)::uuid IS NULL AND (m.user_id IS NOT NULL OR u.role='superadmin'))
ORDER BY recipients.user_id
ON CONFLICT(invitation_id,user_id) WHERE invitation_id IS NOT NULL DO NOTHING;

-- name: TouchInvitationNotifications :exec
UPDATE sitetrack.notifications SET updated_at=clock_timestamp() WHERE invitation_id=$1;

-- name: LockStreams :exec
INSERT INTO sitetrack.notification_streams(user_id,revision)
SELECT users.user_id,0 FROM unnest(sqlc.arg(users)::uuid[]) AS users(user_id) ORDER BY users.user_id
ON CONFLICT(user_id) DO UPDATE SET revision=notification_streams.revision;

-- name: LockProjectStreams :exec
INSERT INTO sitetrack.notification_streams(user_id,revision)
SELECT m.user_id,0 FROM sitetrack.project_memberships m WHERE m.project_id=$1 ORDER BY m.user_id
ON CONFLICT(user_id) DO UPDATE SET revision=notification_streams.revision;

-- name: LockInvitationStream :exec
INSERT INTO sitetrack.notification_streams(user_id,revision)
SELECT invitee_id,0 FROM sitetrack.project_invitations WHERE id=$1
ON CONFLICT(user_id) DO UPDATE SET revision=notification_streams.revision;
