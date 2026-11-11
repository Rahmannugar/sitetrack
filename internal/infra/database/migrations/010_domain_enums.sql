DROP TRIGGER sync_dependency_status ON sitetrack.tasks;
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
ALTER TABLE sitetrack.task_dependencies RENAME COLUMN prerequisite_task_id TO required_task_id;
CREATE OR REPLACE FUNCTION sitetrack.guard_dependency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE required_status sitetrack.task_status; dependent_status sitetrack.task_status;
BEGIN
    IF TG_OP='UPDATE' AND (NEW.project_id,NEW.dependent_task_id,NEW.required_task_id) IS DISTINCT FROM (OLD.project_id,OLD.dependent_task_id,OLD.required_task_id) THEN
        RAISE EXCEPTION 'dependency identity is immutable' USING ERRCODE='23514',CONSTRAINT='dependency_identity';
    END IF;
    PERFORM 1 FROM sitetrack.projects WHERE id=NEW.project_id AND deleted_at IS NULL FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'project unavailable' USING ERRCODE='23514',CONSTRAINT='dependency_endpoint'; END IF;
    SELECT status INTO required_status FROM sitetrack.tasks WHERE project_id=NEW.project_id AND id=NEW.required_task_id AND deleted_at IS NULL;
    SELECT status INTO dependent_status FROM sitetrack.tasks WHERE project_id=NEW.project_id AND id=NEW.dependent_task_id AND deleted_at IS NULL;
    IF required_status IS NULL OR dependent_status IS NULL THEN
        RAISE EXCEPTION 'dependency endpoint unavailable' USING ERRCODE='23514',CONSTRAINT='dependency_endpoint';
    END IF;
    IF TG_OP='INSERT' AND EXISTS (
        WITH RECURSIVE ancestors(id) AS (
            SELECT NEW.required_task_id
            UNION SELECT d.required_task_id FROM sitetrack.task_dependencies d JOIN ancestors a ON d.dependent_task_id=a.id
        ) SELECT 1 FROM ancestors WHERE id=NEW.dependent_task_id
    ) THEN
        RAISE EXCEPTION 'dependency cycle' USING ERRCODE='23514',CONSTRAINT='dependency_cycle';
    END IF;
    NEW.status:=(CASE WHEN required_status='done' THEN 'satisfied' ELSE 'waiting' END)::sitetrack.dependency_status;
    IF NEW.status='waiting' AND dependent_status<>'not_started' THEN
        RAISE EXCEPTION 'started task requires completed dependencies' USING ERRCODE='23514',CONSTRAINT='dependency_started';
    END IF;
    RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION sitetrack.guard_task() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='UPDATE' AND (NEW.id,NEW.project_id) IS DISTINCT FROM (OLD.id,OLD.project_id) THEN
        RAISE EXCEPTION 'task identity is immutable' USING ERRCODE='23514',CONSTRAINT='task_identity';
    END IF;
    PERFORM 1 FROM sitetrack.projects WHERE id=NEW.project_id AND deleted_at IS NULL FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'project unavailable' USING ERRCODE='23514',CONSTRAINT='task_project'; END IF;
    IF TG_OP='INSERT' THEN
        IF NEW.status<>'not_started' THEN RAISE EXCEPTION 'task must start not started' USING ERRCODE='23514',CONSTRAINT='task_transition'; END IF;
        RETURN NEW;
    END IF;
    IF NEW.deleted_at IS NOT NULL AND OLD.deleted_at IS NULL AND EXISTS(SELECT 1 FROM sitetrack.task_dependencies WHERE required_task_id=NEW.id) THEN
        RAISE EXCEPTION 'task still has dependents' USING ERRCODE='23514',CONSTRAINT='task_referenced';
    END IF;
    IF NEW.status=OLD.status THEN RETURN NEW; END IF;
    IF OLD.status='not_started' AND NEW.status='in_progress' THEN
        IF EXISTS(SELECT 1 FROM sitetrack.task_dependencies WHERE dependent_task_id=NEW.id AND status='waiting') THEN
            RAISE EXCEPTION 'task blocked' USING ERRCODE='23514',CONSTRAINT='task_blocked';
        END IF;
    ELSIF OLD.status='in_progress' AND NEW.status='done' THEN
        NULL;
    ELSIF OLD.status='done' AND NEW.status='in_progress' THEN
        IF EXISTS(
            WITH RECURSIVE dependents(id) AS (
                SELECT dependent_task_id FROM sitetrack.task_dependencies WHERE required_task_id=NEW.id
                UNION SELECT d.dependent_task_id FROM sitetrack.task_dependencies d JOIN dependents p ON d.required_task_id=p.id
            ) SELECT 1 FROM dependents d JOIN sitetrack.tasks t ON t.id=d.id WHERE t.status<>'not_started'
        ) THEN RAISE EXCEPTION 'dependent already started' USING ERRCODE='23514',CONSTRAINT='task_reopen'; END IF;
    ELSE
        RAISE EXCEPTION 'invalid task transition' USING ERRCODE='23514',CONSTRAINT='task_transition';
    END IF;
    RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION sitetrack.sync_dependency_status() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE sitetrack.task_dependencies SET status=(CASE WHEN NEW.status='done' THEN 'satisfied' ELSE 'waiting' END)::sitetrack.dependency_status
    WHERE required_task_id=NEW.id;
    RETURN NULL;
END;
$$;

CREATE TRIGGER sync_dependency_status AFTER UPDATE OF status ON sitetrack.tasks
FOR EACH ROW WHEN (OLD.status IS DISTINCT FROM NEW.status) EXECUTE FUNCTION sitetrack.sync_dependency_status();

---- create above / drop below ----

DROP TRIGGER sync_dependency_status ON sitetrack.tasks;
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

ALTER TABLE sitetrack.task_dependencies RENAME COLUMN required_task_id TO prerequisite_task_id;
CREATE OR REPLACE FUNCTION sitetrack.guard_dependency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prerequisite_status text; dependent_status text;
BEGIN
    IF TG_OP='UPDATE' AND (NEW.project_id,NEW.dependent_task_id,NEW.prerequisite_task_id) IS DISTINCT FROM (OLD.project_id,OLD.dependent_task_id,OLD.prerequisite_task_id) THEN
        RAISE EXCEPTION 'dependency identity is immutable' USING ERRCODE='23514',CONSTRAINT='dependency_identity';
    END IF;
    PERFORM 1 FROM sitetrack.projects WHERE id=NEW.project_id AND deleted_at IS NULL FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'project unavailable' USING ERRCODE='23514',CONSTRAINT='dependency_endpoint'; END IF;
    SELECT status INTO prerequisite_status FROM sitetrack.tasks WHERE project_id=NEW.project_id AND id=NEW.prerequisite_task_id AND deleted_at IS NULL;
    SELECT status INTO dependent_status FROM sitetrack.tasks WHERE project_id=NEW.project_id AND id=NEW.dependent_task_id AND deleted_at IS NULL;
    IF prerequisite_status IS NULL OR dependent_status IS NULL THEN
        RAISE EXCEPTION 'dependency endpoint unavailable' USING ERRCODE='23514',CONSTRAINT='dependency_endpoint';
    END IF;
    IF TG_OP='INSERT' AND EXISTS (
        WITH RECURSIVE ancestors(id) AS (
            SELECT NEW.prerequisite_task_id
            UNION SELECT d.prerequisite_task_id FROM sitetrack.task_dependencies d JOIN ancestors a ON d.dependent_task_id=a.id
        ) SELECT 1 FROM ancestors WHERE id=NEW.dependent_task_id
    ) THEN
        RAISE EXCEPTION 'dependency cycle' USING ERRCODE='23514',CONSTRAINT='dependency_cycle';
    END IF;
    NEW.status:=CASE WHEN prerequisite_status='done' THEN 'satisfied' ELSE 'waiting' END;
    IF NEW.status='waiting' AND dependent_status<>'not_started' THEN
        RAISE EXCEPTION 'started task requires completed prerequisites' USING ERRCODE='23514',CONSTRAINT='dependency_started';
    END IF;
    RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION sitetrack.guard_task() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='UPDATE' AND (NEW.id,NEW.project_id) IS DISTINCT FROM (OLD.id,OLD.project_id) THEN
        RAISE EXCEPTION 'task identity is immutable' USING ERRCODE='23514',CONSTRAINT='task_identity';
    END IF;
    PERFORM 1 FROM sitetrack.projects WHERE id=NEW.project_id AND deleted_at IS NULL FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'project unavailable' USING ERRCODE='23514',CONSTRAINT='task_project'; END IF;
    IF TG_OP='INSERT' THEN
        IF NEW.status<>'not_started' THEN RAISE EXCEPTION 'task must start not started' USING ERRCODE='23514',CONSTRAINT='task_transition'; END IF;
        RETURN NEW;
    END IF;
    IF NEW.deleted_at IS NOT NULL AND OLD.deleted_at IS NULL AND EXISTS(SELECT 1 FROM sitetrack.task_dependencies WHERE prerequisite_task_id=NEW.id) THEN
        RAISE EXCEPTION 'task still has dependents' USING ERRCODE='23514',CONSTRAINT='task_referenced';
    END IF;
    IF NEW.status=OLD.status THEN RETURN NEW; END IF;
    IF OLD.status='not_started' AND NEW.status='in_progress' THEN
        IF EXISTS(SELECT 1 FROM sitetrack.task_dependencies WHERE dependent_task_id=NEW.id AND status='waiting') THEN
            RAISE EXCEPTION 'task blocked' USING ERRCODE='23514',CONSTRAINT='task_blocked';
        END IF;
    ELSIF OLD.status='in_progress' AND NEW.status='done' THEN
        NULL;
    ELSIF OLD.status='done' AND NEW.status='in_progress' THEN
        IF EXISTS(
            WITH RECURSIVE dependents(id) AS (
                SELECT dependent_task_id FROM sitetrack.task_dependencies WHERE prerequisite_task_id=NEW.id
                UNION SELECT d.dependent_task_id FROM sitetrack.task_dependencies d JOIN dependents p ON d.prerequisite_task_id=p.id
            ) SELECT 1 FROM dependents d JOIN sitetrack.tasks t ON t.id=d.id WHERE t.status<>'not_started'
        ) THEN RAISE EXCEPTION 'dependent already started' USING ERRCODE='23514',CONSTRAINT='task_reopen'; END IF;
    ELSE
        RAISE EXCEPTION 'invalid task transition' USING ERRCODE='23514',CONSTRAINT='task_transition';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER sync_dependency_status AFTER UPDATE OF status ON sitetrack.tasks
FOR EACH ROW WHEN (OLD.status IS DISTINCT FROM NEW.status) EXECUTE FUNCTION sitetrack.sync_dependency_status();
