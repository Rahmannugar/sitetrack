CREATE SEQUENCE sitetrack.project_number_seq AS bigint NO CYCLE;

CREATE TABLE sitetrack.projects (
    id text PRIMARY KEY CHECK (id ~ '^PRJ-[0-9]{4,}$'),
    name text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 2 AND 150),
    start_date date NOT NULL,
    end_date date NOT NULL CHECK (end_date >= start_date),
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE INDEX projects_active_name_idx ON sitetrack.projects (name, id) WHERE deleted_at IS NULL;
CREATE INDEX projects_active_start_idx ON sitetrack.projects (start_date, id) WHERE deleted_at IS NULL;

CREATE TABLE sitetrack.project_stores (
    id uuid PRIMARY KEY,
    project_id text NOT NULL UNIQUE REFERENCES sitetrack.projects(id),
    name text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 170)
);

CREATE TABLE sitetrack.project_memberships (
    project_id text NOT NULL REFERENCES sitetrack.projects(id),
    user_id uuid NOT NULL REFERENCES sitetrack.users(id),
    is_admin boolean NOT NULL DEFAULT false,
    joined_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX project_memberships_user_idx ON sitetrack.project_memberships (user_id, project_id);
CREATE INDEX project_memberships_admin_idx ON sitetrack.project_memberships (project_id) WHERE is_admin;

CREATE FUNCTION sitetrack.lock_membership_project() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND (NEW.project_id <> OLD.project_id OR NEW.user_id <> OLD.user_id) THEN
        RAISE EXCEPTION 'membership identity is immutable' USING ERRCODE = '23514';
    END IF;
    PERFORM 1 FROM sitetrack.projects WHERE id = COALESCE(NEW.project_id, OLD.project_id) FOR UPDATE;
    RETURN COALESCE(NEW, OLD);
END;
$$;
CREATE TRIGGER membership_project_lock BEFORE INSERT OR UPDATE OR DELETE ON sitetrack.project_memberships
FOR EACH ROW EXECUTE FUNCTION sitetrack.lock_membership_project();

CREATE FUNCTION sitetrack.check_project_admin() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE project_code text;
BEGIN
    IF TG_TABLE_NAME = 'projects' THEN
        project_code := NEW.id;
    ELSE
        project_code := COALESCE(NEW.project_id, OLD.project_id);
    END IF;
    IF EXISTS (SELECT 1 FROM sitetrack.projects WHERE id = project_code AND deleted_at IS NULL)
       AND NOT EXISTS (SELECT 1 FROM sitetrack.project_memberships WHERE project_id = project_code AND is_admin) THEN
        RAISE EXCEPTION 'project requires an admin' USING ERRCODE = '23514', CONSTRAINT = 'project_requires_admin';
    END IF;
    RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER project_requires_admin AFTER INSERT OR UPDATE ON sitetrack.projects
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION sitetrack.check_project_admin();
CREATE CONSTRAINT TRIGGER membership_requires_admin AFTER INSERT OR UPDATE OR DELETE ON sitetrack.project_memberships
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION sitetrack.check_project_admin();

---- create above / drop below ----

DROP TABLE sitetrack.project_memberships;
DROP FUNCTION sitetrack.lock_membership_project();
DROP TABLE sitetrack.project_stores;
DROP TABLE sitetrack.projects;
DROP FUNCTION sitetrack.check_project_admin();
DROP SEQUENCE sitetrack.project_number_seq;
