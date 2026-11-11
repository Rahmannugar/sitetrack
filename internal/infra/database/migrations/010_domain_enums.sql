DROP INDEX sitetrack.outbox_pending_idx;
DROP INDEX sitetrack.outbox_leased_idx;
DROP INDEX sitetrack.outbox_aggregate_idx;
DROP INDEX sitetrack.invitation_pending_idx;
DROP INDEX sitetrack.task_dependencies_waiting_idx;
CREATE TYPE sitetrack.app_role AS ENUM ('user','company_store_manager','superadmin');
ALTER TABLE sitetrack.users DROP CONSTRAINT users_role_check;
ALTER TABLE sitetrack.users ALTER COLUMN role TYPE sitetrack.app_role USING role::sitetrack.app_role;
CREATE TYPE sitetrack.outbox_status AS ENUM ('pending','processing','published','failed','cancelled');
ALTER TABLE sitetrack.outbox_events DROP CONSTRAINT outbox_events_status_check;
ALTER TABLE sitetrack.outbox_events DROP CONSTRAINT outbox_events_check;
ALTER TABLE sitetrack.outbox_events DROP CONSTRAINT outbox_events_check1;
ALTER TABLE sitetrack.outbox_events ALTER COLUMN status DROP DEFAULT;
ALTER TABLE sitetrack.outbox_events ALTER COLUMN status TYPE sitetrack.outbox_status USING status::sitetrack.outbox_status;
ALTER TABLE sitetrack.outbox_events ALTER COLUMN status SET DEFAULT 'pending';
CREATE TYPE sitetrack.invitation_status AS ENUM ('pending','accepted','rejected','withdrawn');
ALTER TABLE sitetrack.project_invitations DROP CONSTRAINT project_invitations_status_check;
ALTER TABLE sitetrack.project_invitations DROP CONSTRAINT project_invitations_check1;
ALTER TABLE sitetrack.project_invitations ALTER COLUMN status DROP DEFAULT;
ALTER TABLE sitetrack.project_invitations ALTER COLUMN status TYPE sitetrack.invitation_status USING status::sitetrack.invitation_status;
ALTER TABLE sitetrack.project_invitations ALTER COLUMN status SET DEFAULT 'pending';
CREATE TYPE sitetrack.task_status AS ENUM ('not_started','in_progress','done');
ALTER TABLE sitetrack.tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE sitetrack.tasks ALTER COLUMN status DROP DEFAULT;
ALTER TABLE sitetrack.tasks ALTER COLUMN status TYPE sitetrack.task_status USING status::sitetrack.task_status;
ALTER TABLE sitetrack.tasks ALTER COLUMN status SET DEFAULT 'not_started';
CREATE TYPE sitetrack.dependency_status AS ENUM ('waiting','satisfied');
ALTER TABLE sitetrack.task_dependencies DROP CONSTRAINT task_dependencies_status_check;
ALTER TABLE sitetrack.task_dependencies ALTER COLUMN status TYPE sitetrack.dependency_status USING status::sitetrack.dependency_status;
ALTER TABLE sitetrack.outbox_events ADD CONSTRAINT outbox_events_check CHECK (status IN ('published','failed','cancelled') OR payload IS NOT NULL);
ALTER TABLE sitetrack.outbox_events ADD CONSTRAINT outbox_events_check1 CHECK (status<>'processing' OR (lease_token IS NOT NULL AND lease_until IS NOT NULL));
ALTER TABLE sitetrack.project_invitations ADD CONSTRAINT project_invitations_check1 CHECK ((status='pending')=(completed_at IS NULL));
CREATE INDEX outbox_pending_idx ON sitetrack.outbox_events (available_at,created_at) WHERE status='pending';
CREATE INDEX outbox_leased_idx ON sitetrack.outbox_events (lease_until) WHERE status='processing';
CREATE INDEX outbox_aggregate_idx ON sitetrack.outbox_events (aggregate_id,event_type) WHERE status IN ('pending','processing');
CREATE UNIQUE INDEX invitation_pending_idx ON sitetrack.project_invitations(project_id,invitee_id) WHERE status='pending';
CREATE INDEX task_dependencies_waiting_idx ON sitetrack.task_dependencies(dependent_task_id) WHERE status='waiting';
CREATE OR REPLACE FUNCTION sitetrack.sync_dependency_status() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE sitetrack.task_dependencies SET status=(CASE WHEN NEW.status='done' THEN 'satisfied' ELSE 'waiting' END)::sitetrack.dependency_status
    WHERE prerequisite_task_id=NEW.id;
    RETURN NULL;
END;
$$;

---- create above / drop below ----

DROP INDEX sitetrack.outbox_pending_idx;
DROP INDEX sitetrack.outbox_leased_idx;
DROP INDEX sitetrack.outbox_aggregate_idx;
DROP INDEX sitetrack.invitation_pending_idx;
DROP INDEX sitetrack.task_dependencies_waiting_idx;
ALTER TABLE sitetrack.outbox_events DROP CONSTRAINT outbox_events_check;
ALTER TABLE sitetrack.outbox_events DROP CONSTRAINT outbox_events_check1;
ALTER TABLE sitetrack.project_invitations DROP CONSTRAINT project_invitations_check1;
ALTER TABLE sitetrack.task_dependencies ALTER COLUMN status TYPE text USING status::text;
ALTER TABLE sitetrack.task_dependencies ADD CONSTRAINT task_dependencies_status_check CHECK (status IN ('waiting','satisfied'));
DROP TYPE sitetrack.dependency_status;
ALTER TABLE sitetrack.tasks ALTER COLUMN status DROP DEFAULT;
ALTER TABLE sitetrack.tasks ALTER COLUMN status TYPE text USING status::text;
ALTER TABLE sitetrack.tasks ALTER COLUMN status SET DEFAULT 'not_started';
ALTER TABLE sitetrack.tasks ADD CONSTRAINT tasks_status_check CHECK (status IN ('not_started','in_progress','done'));
DROP TYPE sitetrack.task_status;
ALTER TABLE sitetrack.project_invitations ALTER COLUMN status DROP DEFAULT;
ALTER TABLE sitetrack.project_invitations ALTER COLUMN status TYPE text USING status::text;
ALTER TABLE sitetrack.project_invitations ALTER COLUMN status SET DEFAULT 'pending';
ALTER TABLE sitetrack.project_invitations ADD CONSTRAINT project_invitations_status_check CHECK (status IN ('pending','accepted','rejected','withdrawn'));
DROP TYPE sitetrack.invitation_status;
ALTER TABLE sitetrack.outbox_events ALTER COLUMN status DROP DEFAULT;
ALTER TABLE sitetrack.outbox_events ALTER COLUMN status TYPE text USING status::text;
ALTER TABLE sitetrack.outbox_events ALTER COLUMN status SET DEFAULT 'pending';
ALTER TABLE sitetrack.outbox_events ADD CONSTRAINT outbox_events_status_check CHECK (status IN ('pending','processing','published','failed','cancelled'));
DROP TYPE sitetrack.outbox_status;
ALTER TABLE sitetrack.users ALTER COLUMN role TYPE text USING role::text;
ALTER TABLE sitetrack.users ADD CONSTRAINT users_role_check CHECK (role IN ('user','company_store_manager','superadmin'));
DROP TYPE sitetrack.app_role;
ALTER TABLE sitetrack.outbox_events ADD CONSTRAINT outbox_events_check CHECK (status IN ('published','failed','cancelled') OR payload IS NOT NULL);
ALTER TABLE sitetrack.outbox_events ADD CONSTRAINT outbox_events_check1 CHECK (status<>'processing' OR (lease_token IS NOT NULL AND lease_until IS NOT NULL));
ALTER TABLE sitetrack.project_invitations ADD CONSTRAINT project_invitations_check1 CHECK ((status='pending')=(completed_at IS NULL));
CREATE INDEX outbox_pending_idx ON sitetrack.outbox_events (available_at,created_at) WHERE status='pending';
CREATE INDEX outbox_leased_idx ON sitetrack.outbox_events (lease_until) WHERE status='processing';
CREATE INDEX outbox_aggregate_idx ON sitetrack.outbox_events (aggregate_id,event_type) WHERE status IN ('pending','processing');
CREATE UNIQUE INDEX invitation_pending_idx ON sitetrack.project_invitations(project_id,invitee_id) WHERE status='pending';
CREATE INDEX task_dependencies_waiting_idx ON sitetrack.task_dependencies(dependent_task_id) WHERE status='waiting';
CREATE OR REPLACE FUNCTION sitetrack.sync_dependency_status() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE sitetrack.task_dependencies SET status=CASE WHEN NEW.status='done' THEN 'satisfied' ELSE 'waiting' END
    WHERE prerequisite_task_id=NEW.id;
    RETURN NULL;
END;
$$;
