CREATE TABLE sitetrack.users (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 100),
    email text NOT NULL UNIQUE CHECK (email = lower(btrim(email))),
    password_hash text NOT NULL,
    role text NOT NULL CHECK (role IN ('user', 'company_store_manager', 'superadmin')),
    email_verified boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sitetrack.email_verifications (
    user_id uuid PRIMARY KEY REFERENCES sitetrack.users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    used_at timestamptz,
    attempts smallint NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5)
);

CREATE INDEX email_verification_expiry_idx ON sitetrack.email_verifications (expires_at);
CREATE INDEX email_verification_used_idx ON sitetrack.email_verifications (used_at)
    WHERE used_at IS NOT NULL;

CREATE TABLE sitetrack.worker_maintenance (
    name text PRIMARY KEY,
    completed_on date,
    lease_token uuid NOT NULL,
    lease_until timestamptz NOT NULL
);

CREATE TABLE sitetrack.outbox_events (
    id uuid PRIMARY KEY,
    aggregate_id uuid NOT NULL,
    event_type text NOT NULL,
    payload bytea,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'published', 'failed', 'cancelled')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    lease_token uuid,
    lease_until timestamptz,
    provider_message_id text,
    error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    CHECK (status IN ('published', 'failed', 'cancelled') OR payload IS NOT NULL),
    CHECK (status <> 'processing' OR (lease_token IS NOT NULL AND lease_until IS NOT NULL))
);

CREATE INDEX outbox_pending_idx ON sitetrack.outbox_events (available_at, created_at)
    WHERE status = 'pending';
CREATE INDEX outbox_leased_idx ON sitetrack.outbox_events (lease_until)
    WHERE status = 'processing';
CREATE INDEX outbox_aggregate_idx ON sitetrack.outbox_events (aggregate_id, event_type)
    WHERE status IN ('pending', 'processing');

---- create above / drop below ----

DROP TABLE sitetrack.outbox_events;
DROP TABLE sitetrack.worker_maintenance;
DROP TABLE sitetrack.email_verifications;
DROP TABLE sitetrack.users;
