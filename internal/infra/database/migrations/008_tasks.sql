ALTER TABLE sitetrack.projects ADD COLUMN task_number bigint NOT NULL DEFAULT 0 CHECK (task_number>=0);
CREATE TABLE sitetrack.tasks (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES sitetrack.projects(id),
    name text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 2 AND 150),
    start_date date NOT NULL,
    end_date date NOT NULL CHECK (end_date>=start_date),
    status text NOT NULL DEFAULT 'not_started' CHECK (status IN ('not_started','in_progress','done')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    deleted_at timestamptz,
    UNIQUE(project_id,id),
    CHECK (left(id,char_length(project_id)+3)=project_id||'-T-' AND substring(id FROM char_length(project_id)+4) ~ '^[0-9]{4,}$')
);
CREATE INDEX tasks_project_status_idx ON sitetrack.tasks(project_id,status,id) WHERE deleted_at IS NULL;
CREATE INDEX tasks_project_name_idx ON sitetrack.tasks(project_id,name,id) WHERE deleted_at IS NULL;
CREATE INDEX tasks_project_start_idx ON sitetrack.tasks(project_id,start_date,id) WHERE deleted_at IS NULL;
CREATE INDEX tasks_project_end_idx ON sitetrack.tasks(project_id,end_date,id) WHERE deleted_at IS NULL;
CREATE TABLE sitetrack.task_dependencies (
    project_id text NOT NULL,
    dependent_task_id text NOT NULL,
    prerequisite_task_id text NOT NULL,
    status text NOT NULL CHECK (status IN ('waiting','satisfied')),
    PRIMARY KEY(dependent_task_id,prerequisite_task_id),
    FOREIGN KEY(project_id,dependent_task_id) REFERENCES sitetrack.tasks(project_id,id),
    FOREIGN KEY(project_id,prerequisite_task_id) REFERENCES sitetrack.tasks(project_id,id),
    CHECK (dependent_task_id<>prerequisite_task_id)
);
CREATE INDEX task_dependencies_reverse_idx ON sitetrack.task_dependencies(prerequisite_task_id,dependent_task_id);
CREATE INDEX task_dependencies_waiting_idx ON sitetrack.task_dependencies(dependent_task_id) WHERE status='waiting';
CREATE INDEX task_dependencies_project_idx ON sitetrack.task_dependencies(project_id,dependent_task_id,prerequisite_task_id);

CREATE FUNCTION sitetrack.guard_dependency() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER guard_dependency BEFORE INSERT OR UPDATE ON sitetrack.task_dependencies
FOR EACH ROW EXECUTE FUNCTION sitetrack.guard_dependency();

CREATE FUNCTION sitetrack.guard_task() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER guard_task BEFORE INSERT OR UPDATE ON sitetrack.tasks FOR EACH ROW EXECUTE FUNCTION sitetrack.guard_task();
CREATE FUNCTION sitetrack.sync_dependency_status() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE sitetrack.task_dependencies SET status=CASE WHEN NEW.status='done' THEN 'satisfied' ELSE 'waiting' END
    WHERE prerequisite_task_id=NEW.id;
    RETURN NULL;
END;
$$;
CREATE TRIGGER sync_dependency_status AFTER UPDATE OF status ON sitetrack.tasks
FOR EACH ROW WHEN (OLD.status IS DISTINCT FROM NEW.status) EXECUTE FUNCTION sitetrack.sync_dependency_status();

---- create above / drop below ----

DROP TRIGGER sync_dependency_status ON sitetrack.tasks;
DROP FUNCTION sitetrack.sync_dependency_status();
DROP TRIGGER guard_task ON sitetrack.tasks;
DROP FUNCTION sitetrack.guard_task();
DROP TABLE sitetrack.task_dependencies;
DROP FUNCTION sitetrack.guard_dependency();
DROP TABLE sitetrack.tasks;
ALTER TABLE sitetrack.projects DROP COLUMN task_number;
