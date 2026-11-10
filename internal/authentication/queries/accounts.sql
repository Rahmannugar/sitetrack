-- name: CreateUser :one
INSERT INTO sitetrack.users (id, name, email, password_hash, role)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (email) DO NOTHING
RETURNING id;

-- name: LockUserByEmail :one
SELECT id, email, email_verified FROM sitetrack.users WHERE email = $1 FOR UPDATE;

-- name: GetVerification :one
SELECT user_id, token_hash, created_at, expires_at, attempts, used_at
FROM sitetrack.email_verifications WHERE user_id = $1;

-- name: IssueVerification :exec
INSERT INTO sitetrack.email_verifications (user_id, token_hash, created_at, expires_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO UPDATE SET token_hash = EXCLUDED.token_hash,
    created_at = EXCLUDED.created_at, expires_at = EXCLUDED.expires_at, attempts = 0, used_at = NULL;

-- name: CountVerificationAttempt :one
UPDATE sitetrack.email_verifications SET attempts = attempts + 1
WHERE user_id = $1 AND attempts < 5 AND expires_at > $2 AND used_at IS NULL
RETURNING user_id;

-- name: VerifyEmail :one
UPDATE sitetrack.users AS u SET email_verified = true
FROM sitetrack.email_verifications AS v
WHERE u.id = v.user_id AND v.token_hash = $1 AND v.expires_at > $2
    AND NOT u.email_verified AND v.used_at IS NULL
RETURNING u.id, u.email, u.email_verified;

-- name: MarkVerificationUsed :exec
UPDATE sitetrack.email_verifications SET used_at = now() WHERE user_id = $1;

-- name: CleanupVerifications :execrows
WITH obsolete AS (
    SELECT user_id FROM sitetrack.email_verifications
    WHERE expires_at <= now() OR used_at IS NOT NULL
    LIMIT 500 FOR UPDATE SKIP LOCKED
)
DELETE FROM sitetrack.email_verifications AS v USING obsolete WHERE v.user_id = obsolete.user_id;
