CREATE TYPE sitetrack.delivery_direction AS ENUM ('company_to_project','project_to_company');
CREATE TYPE sitetrack.delivery_status AS ENUM ('in_transit','delivered','rejected','cancelled');

ALTER TABLE sitetrack.company_inventory ADD CONSTRAINT company_inventory_capacity CHECK(quantity+in_transit_to_projects<=999999999999999.999);
ALTER TABLE sitetrack.project_inventory ADD CONSTRAINT project_inventory_capacity CHECK(quantity+in_transit<=999999999999999.999);

CREATE TABLE sitetrack.deliveries (
    id uuid PRIMARY KEY,
    project_id text NOT NULL REFERENCES sitetrack.projects(id),
    direction sitetrack.delivery_direction NOT NULL,
    status sitetrack.delivery_status NOT NULL DEFAULT 'in_transit',
    initiated_by_user_id uuid NOT NULL REFERENCES sitetrack.users(id),
    completed_by_user_id uuid REFERENCES sitetrack.users(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz,
    CHECK ((status='in_transit' AND completed_by_user_id IS NULL AND completed_at IS NULL) OR
           (status<>'in_transit' AND completed_by_user_id IS NOT NULL AND completed_at IS NOT NULL AND completed_at>=created_at))
);
CREATE INDEX deliveries_project_page_idx ON sitetrack.deliveries(project_id,created_at DESC,id DESC);
CREATE INDEX deliveries_company_page_idx ON sitetrack.deliveries(created_at DESC,id DESC);
CREATE INDEX deliveries_project_status_page_idx ON sitetrack.deliveries(project_id,status,created_at DESC,id DESC);
CREATE INDEX deliveries_status_page_idx ON sitetrack.deliveries(status,created_at DESC,id DESC);
CREATE INDEX deliveries_project_direction_page_idx ON sitetrack.deliveries(project_id,direction,created_at DESC,id DESC);
CREATE INDEX deliveries_direction_page_idx ON sitetrack.deliveries(direction,created_at DESC,id DESC);

CREATE TABLE sitetrack.delivery_items (
    delivery_id uuid NOT NULL REFERENCES sitetrack.deliveries(id),
    material_id uuid NOT NULL REFERENCES sitetrack.materials(id),
    quantity numeric(18,3) NOT NULL CHECK(quantity>0 AND quantity<=999999999999999.999),
    PRIMARY KEY(delivery_id,material_id)
);
CREATE INDEX delivery_items_material_idx ON sitetrack.delivery_items(material_id);

CREATE FUNCTION sitetrack.guard_delivery_changes() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.id,NEW.project_id,NEW.direction,NEW.initiated_by_user_id,NEW.created_at) IS DISTINCT FROM
       (OLD.id,OLD.project_id,OLD.direction,OLD.initiated_by_user_id,OLD.created_at) OR
       OLD.status<>'in_transit' OR NEW.status='in_transit' THEN
        RAISE EXCEPTION 'delivery cannot be edited' USING ERRCODE='23514',CONSTRAINT='delivery_transition';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER guard_delivery_changes BEFORE UPDATE ON sitetrack.deliveries FOR EACH ROW EXECUTE FUNCTION sitetrack.guard_delivery_changes();

CREATE FUNCTION sitetrack.guard_delivery_item_changes() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'delivery items cannot be edited' USING ERRCODE='23514',CONSTRAINT='delivery_items_immutable';
END;
$$;
CREATE TRIGGER guard_delivery_item_changes BEFORE UPDATE ON sitetrack.delivery_items FOR EACH ROW EXECUTE FUNCTION sitetrack.guard_delivery_item_changes();

CREATE OR REPLACE FUNCTION sitetrack.guard_project_inventory() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL AND (
        EXISTS(SELECT 1 FROM sitetrack.project_inventory i JOIN sitetrack.project_stores s ON s.id=i.project_store_id
               WHERE s.project_id=NEW.id AND (i.quantity>0 OR i.in_transit>0)) OR
        EXISTS(SELECT 1 FROM sitetrack.deliveries WHERE project_id=NEW.id AND status='in_transit')
    ) THEN
        RAISE EXCEPTION 'project store still has stock or goods in transit' USING ERRCODE='23514',CONSTRAINT='project_stock';
    END IF;
    RETURN NEW;
END;
$$;

---- create above / drop below ----

CREATE OR REPLACE FUNCTION sitetrack.guard_project_inventory() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL AND EXISTS(
        SELECT 1 FROM sitetrack.project_inventory i JOIN sitetrack.project_stores s ON s.id=i.project_store_id
        WHERE s.project_id=NEW.id AND (i.quantity>0 OR i.in_transit>0)
    ) THEN
        RAISE EXCEPTION 'project store still has stock or goods in transit' USING ERRCODE='23514',CONSTRAINT='project_stock';
    END IF;
    RETURN NEW;
END;
$$;
DROP TABLE sitetrack.delivery_items;
DROP FUNCTION sitetrack.guard_delivery_item_changes();
DROP TABLE sitetrack.deliveries;
DROP FUNCTION sitetrack.guard_delivery_changes();
ALTER TABLE sitetrack.company_inventory DROP CONSTRAINT company_inventory_capacity;
ALTER TABLE sitetrack.project_inventory DROP CONSTRAINT project_inventory_capacity;
DROP TYPE sitetrack.delivery_status;
DROP TYPE sitetrack.delivery_direction;
