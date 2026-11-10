CREATE TABLE sitetrack.auth_sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL UNIQUE REFERENCES sitetrack.users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at)
);

CREATE TABLE sitetrack.refresh_tokens (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    session_id uuid NOT NULL REFERENCES sitetrack.auth_sessions(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    rotated_at timestamptz
);

CREATE INDEX refresh_tokens_session_idx ON sitetrack.refresh_tokens (session_id);

---- create above / drop below ----

DROP TABLE sitetrack.refresh_tokens;
DROP TABLE sitetrack.auth_sessions;
