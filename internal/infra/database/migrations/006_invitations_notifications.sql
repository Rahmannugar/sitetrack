ALTER TABLE sitetrack.outbox_events ALTER COLUMN aggregate_id TYPE text USING aggregate_id::text;
ALTER TABLE sitetrack.outbox_events ALTER COLUMN expires_at DROP NOT NULL;

CREATE TABLE sitetrack.project_invitations (
    id uuid PRIMARY KEY,
    project_id text NOT NULL REFERENCES sitetrack.projects(id),
    inviter_id uuid NOT NULL REFERENCES sitetrack.users(id),
    invitee_id uuid NOT NULL REFERENCES sitetrack.users(id),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','rejected','withdrawn')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz,
    CHECK (inviter_id <> invitee_id),
    CHECK ((status = 'pending') = (completed_at IS NULL))
);
CREATE UNIQUE INDEX invitation_pending_idx ON sitetrack.project_invitations(project_id, invitee_id) WHERE status='pending';
CREATE INDEX invitation_project_list_idx ON sitetrack.project_invitations(project_id,created_at DESC,id DESC);
CREATE INDEX invitation_invitee_idx ON sitetrack.project_invitations(invitee_id,id);

CREATE TABLE sitetrack.outbox_recipients (
    event_id uuid NOT NULL REFERENCES sitetrack.outbox_events(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES sitetrack.users(id) ON DELETE CASCADE,
    delivered_at timestamptz,
    PRIMARY KEY(event_id,user_id)
);
CREATE INDEX outbox_recipients_pending_idx ON sitetrack.outbox_recipients(event_id,user_id) WHERE delivered_at IS NULL;

CREATE TABLE sitetrack.notifications (
    id uuid PRIMARY KEY,
    event_id uuid NOT NULL REFERENCES sitetrack.outbox_events(id),
    user_id uuid NOT NULL REFERENCES sitetrack.users(id) ON DELETE CASCADE,
    project_id text NOT NULL REFERENCES sitetrack.projects(id),
    invitation_id uuid REFERENCES sitetrack.project_invitations(id),
    type text NOT NULL,
    title text NOT NULL,
    message text NOT NULL,
    read_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(event_id,user_id)
);
CREATE INDEX notifications_user_list_idx ON sitetrack.notifications(user_id,created_at DESC,id DESC);
CREATE INDEX notifications_unread_idx ON sitetrack.notifications(user_id,project_id) WHERE read_at IS NULL;
CREATE INDEX notifications_invitation_idx ON sitetrack.notifications(invitation_id) WHERE invitation_id IS NOT NULL;

-- We serialize feed revisions per user so replay cannot skip a late commit.
CREATE TABLE sitetrack.notification_streams (
    user_id uuid PRIMARY KEY REFERENCES sitetrack.users(id) ON DELETE CASCADE,
    revision bigint NOT NULL DEFAULT 0 CHECK (revision>=0)
);
CREATE TABLE sitetrack.notification_changes (
    user_id uuid NOT NULL REFERENCES sitetrack.users(id) ON DELETE CASCADE,
    revision bigint NOT NULL,
    notification_id uuid NOT NULL REFERENCES sitetrack.notifications(id) ON DELETE CASCADE,
    PRIMARY KEY(user_id,revision)
);
CREATE INDEX notification_changes_notification_idx ON sitetrack.notification_changes(notification_id);

CREATE FUNCTION sitetrack.record_notification_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE next_revision bigint;
BEGIN
    INSERT INTO sitetrack.notification_streams(user_id,revision) VALUES(NEW.user_id,1)
    ON CONFLICT(user_id) DO UPDATE SET revision=notification_streams.revision+1
    RETURNING revision INTO next_revision;
    INSERT INTO sitetrack.notification_changes(user_id,revision,notification_id)
    VALUES(NEW.user_id,next_revision,NEW.id);
    PERFORM pg_notify('sitetrack_notifications',NEW.user_id::text);
    RETURN NULL;
END;
$$;
CREATE TRIGGER notification_change AFTER INSERT OR UPDATE ON sitetrack.notifications
FOR EACH ROW EXECUTE FUNCTION sitetrack.record_notification_change();

---- create above / drop below ----

DROP TABLE sitetrack.notification_changes;
DROP TABLE sitetrack.notification_streams;
DROP TRIGGER notification_change ON sitetrack.notifications;
DROP FUNCTION sitetrack.record_notification_change();
DROP TABLE sitetrack.notifications;
DROP TABLE sitetrack.outbox_recipients;
DROP TABLE sitetrack.project_invitations;
DELETE FROM sitetrack.outbox_events WHERE event_type='notification.project';
ALTER TABLE sitetrack.outbox_events ALTER COLUMN expires_at SET NOT NULL;
ALTER TABLE sitetrack.outbox_events ALTER COLUMN aggregate_id TYPE uuid USING aggregate_id::uuid;
