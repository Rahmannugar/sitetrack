-- name: GetPasswordReset :one
SELECT * FROM sitetrack.password_resets WHERE user_id = $1;

-- name: IssuePasswordReset :exec
INSERT INTO sitetrack.password_resets (user_id, token_hash, created_at, expires_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO UPDATE SET token_hash = EXCLUDED.token_hash,
    created_at = EXCLUDED.created_at, expires_at = EXCLUDED.expires_at, attempts = 0, used_at = NULL;

-- name: CountPasswordResetAttempt :exec
UPDATE sitetrack.password_resets SET attempts = attempts + 1 WHERE user_id = $1 AND attempts < 5;

-- name: UsePasswordReset :exec
UPDATE sitetrack.password_resets SET used_at = $2 WHERE user_id = $1;

-- name: ChangePassword :exec
UPDATE sitetrack.users SET password_hash = $2 WHERE id = $1;

-- name: CleanupPasswordResets :execrows
WITH obsolete AS (
    SELECT user_id FROM sitetrack.password_resets
    WHERE expires_at <= now() OR used_at IS NOT NULL
    LIMIT 500 FOR UPDATE SKIP LOCKED
)
DELETE FROM sitetrack.password_resets AS r USING obsolete WHERE r.user_id = obsolete.user_id;
