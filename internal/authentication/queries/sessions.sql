-- name: GetLoginUser :one
SELECT id, name, email, password_hash, role, email_verified
FROM sitetrack.users WHERE email = $1;

-- name: LockLoginUser :one
SELECT id, name, email, password_hash, role, email_verified
FROM sitetrack.users WHERE id = $1 FOR UPDATE;

-- name: DeleteUserSessions :exec
DELETE FROM sitetrack.auth_sessions WHERE user_id = $1;

-- name: CreateSession :exec
INSERT INTO sitetrack.auth_sessions (id, user_id, created_at, expires_at)
VALUES ($1, $2, $3, $4);

-- name: GetCurrentSession :one
SELECT s.id, s.user_id, s.created_at, s.expires_at,
    u.name, u.email, u.role, u.email_verified
FROM sitetrack.auth_sessions AS s JOIN sitetrack.users AS u ON u.id = s.user_id
WHERE s.id = $1 AND s.expires_at > $2 AND u.email_verified;

-- name: LockRefreshUser :one
SELECT u.id FROM sitetrack.users AS u
JOIN sitetrack.auth_sessions AS s ON s.user_id = u.id
JOIN sitetrack.refresh_tokens AS r ON r.session_id = s.id
WHERE r.token_hash = $1 FOR UPDATE OF u;

-- name: LockSession :one
SELECT id, user_id, created_at, expires_at
FROM sitetrack.auth_sessions WHERE id = $1 FOR UPDATE;

-- name: InsertRefreshToken :execrows
INSERT INTO sitetrack.refresh_tokens (token_hash, session_id, created_at, expires_at)
VALUES ($1, $2, $3, $4) ON CONFLICT (token_hash) DO NOTHING;

-- name: FindRefreshToken :one
SELECT token_hash, session_id, created_at, expires_at, rotated_at
FROM sitetrack.refresh_tokens WHERE token_hash = $1;

-- name: LockRefreshToken :one
SELECT token_hash, session_id, created_at, expires_at, rotated_at
FROM sitetrack.refresh_tokens WHERE token_hash = $1 FOR UPDATE;

-- name: MarkRefreshRotated :exec
UPDATE sitetrack.refresh_tokens SET rotated_at = $2 WHERE token_hash = $1;

-- name: DeleteSession :exec
DELETE FROM sitetrack.auth_sessions WHERE id = $1;

-- name: LogoutSession :exec
DELETE FROM sitetrack.auth_sessions WHERE id = $1 AND user_id = $2;

-- name: SetSessionExpiry :exec
UPDATE sitetrack.auth_sessions SET expires_at = $2 WHERE id = $1;

-- name: ExtendRefreshToken :exec
UPDATE sitetrack.refresh_tokens SET expires_at = $3
WHERE token_hash = $1 AND session_id = $2 AND rotated_at IS NULL;
