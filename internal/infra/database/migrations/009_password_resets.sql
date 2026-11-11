CREATE TABLE sitetrack.password_resets (
    user_id uuid PRIMARY KEY REFERENCES sitetrack.users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL CHECK (octet_length(token_hash) = 32),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    used_at timestamptz,
    attempts smallint NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5)
);

CREATE INDEX password_reset_expiry_idx ON sitetrack.password_resets (expires_at);
CREATE INDEX password_reset_used_idx ON sitetrack.password_resets (used_at) WHERE used_at IS NOT NULL;

---- create above / drop below ----

DROP TABLE sitetrack.password_resets;
