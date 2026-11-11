-- name: Enqueue :exec
INSERT INTO sitetrack.outbox_events (id, aggregate_id, event_type, payload, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: CancelVerificationEmails :exec
UPDATE sitetrack.outbox_events SET status = 'cancelled', payload = NULL,
    lease_token = NULL, lease_until = NULL
WHERE aggregate_id = $1 AND event_type = 'email.verification'
    AND status IN ('pending', 'processing');

-- name: ExpireJobs :exec
WITH expired AS (
    SELECT id FROM sitetrack.outbox_events
    WHERE (status = 'pending' OR (status = 'processing' AND lease_until <= now()))
        AND ((expires_at IS NOT NULL AND expires_at <= now()) OR attempts >= 8)
    ORDER BY created_at LIMIT 100 FOR UPDATE SKIP LOCKED
)
UPDATE sitetrack.outbox_events AS e SET status = 'failed', payload = NULL,
    error_code = 'expired_or_exhausted', lease_token = NULL, lease_until = NULL
FROM expired WHERE e.id = expired.id;

-- name: CancelPasswordResetEmails :exec
UPDATE sitetrack.outbox_events SET status = 'cancelled', payload = NULL,
    lease_token = NULL, lease_until = NULL
WHERE aggregate_id = $1 AND event_type = 'email.password_reset'
    AND status IN ('pending', 'processing');

-- name: ClaimJobs :many
WITH candidate AS (
    SELECT id FROM sitetrack.outbox_events
    WHERE event_type=ANY(sqlc.arg(event_types)::text[]) AND (expires_at IS NULL OR expires_at > now()) AND attempts < 8
        AND ((status = 'pending' AND available_at <= now())
            OR (status = 'processing' AND lease_until <= now()))
    ORDER BY created_at, id LIMIT 20 FOR UPDATE SKIP LOCKED
)
UPDATE sitetrack.outbox_events AS e SET status = 'processing', attempts = attempts + 1,
    lease_token = sqlc.arg(lease_token), lease_until = now() + interval '30 seconds'
FROM candidate WHERE e.id = candidate.id
RETURNING e.id, e.event_type, e.payload, e.attempts, e.expires_at, e.lease_token, e.lease_until;

-- name: PublishJob :execrows
UPDATE sitetrack.outbox_events SET status = 'published', payload = NULL,
    published_at = now(), provider_message_id = $3, lease_token = NULL, lease_until = NULL
WHERE id = $1 AND lease_token = $2 AND status = 'processing';

-- name: ClaimDailyCleanup :one
INSERT INTO sitetrack.worker_maintenance (name, lease_token, lease_until)
VALUES ('otp_cleanup', $1, now() + interval '2 minutes')
ON CONFLICT (name) DO UPDATE SET lease_token = EXCLUDED.lease_token,
    lease_until = EXCLUDED.lease_until
WHERE (worker_maintenance.completed_on IS NULL OR worker_maintenance.completed_on < (now() AT TIME ZONE 'UTC')::date)
    AND worker_maintenance.lease_until <= now()
RETURNING name;

-- name: CompleteDailyCleanup :execrows
UPDATE sitetrack.worker_maintenance
SET completed_on = (now() AT TIME ZONE 'UTC')::date, lease_until = now()
WHERE name = 'otp_cleanup' AND lease_token = $1;

-- name: RetryJob :execrows
UPDATE sitetrack.outbox_events SET status = sqlc.arg(status)::sitetrack.outbox_status,
    available_at = sqlc.arg(available_at), error_code = sqlc.narg(error_code),
    lease_token = NULL, lease_until = NULL,
    payload = CASE WHEN sqlc.arg(status)::sitetrack.outbox_status = 'failed' THEN NULL ELSE payload END
WHERE id = sqlc.arg(id) AND lease_token = sqlc.arg(lease_token) AND status = 'processing';
