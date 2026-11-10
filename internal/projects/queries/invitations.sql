-- name: CreateInvitation :one
INSERT INTO sitetrack.project_invitations(id,project_id,inviter_id,invitee_id)
VALUES($1,$2,$3,$4) RETURNING *;

-- name: RegisteredInvitee :one
SELECT id FROM sitetrack.users WHERE id=$1;

-- name: GetInvitation :one
SELECT * FROM sitetrack.project_invitations WHERE id=$1 AND project_id=$2;

-- name: InvitationProject :one
SELECT project_id FROM sitetrack.project_invitations WHERE id=$1 AND invitee_id=$2;

-- name: FinishInvitation :one
UPDATE sitetrack.project_invitations SET status=$3,completed_at=clock_timestamp()
WHERE id=$1 AND project_id=$2 AND status='pending' RETURNING *;

-- name: ListInvitations :many
SELECT * FROM sitetrack.project_invitations WHERE project_id=sqlc.arg(project_id)
AND (sqlc.arg(status)::text='' OR status=sqlc.arg(status))
AND (sqlc.narg(after_time)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(after_time),sqlc.narg(after_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_size);

-- name: CancelProjectInvitations :exec
UPDATE sitetrack.project_invitations SET status='withdrawn',completed_at=clock_timestamp()
WHERE project_id=$1 AND status='pending';
