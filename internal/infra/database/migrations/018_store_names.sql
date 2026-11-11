ALTER TABLE sitetrack.company_inventory RENAME TO company_store;
ALTER TABLE sitetrack.project_inventory RENAME TO project_store;

CREATE OR REPLACE FUNCTION sitetrack.guard_material_inventory() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.unit IS DISTINCT FROM OLD.unit AND
        (EXISTS(SELECT 1 FROM sitetrack.company_store WHERE material_id=NEW.id) OR
         EXISTS(SELECT 1 FROM sitetrack.project_store WHERE material_id=NEW.id)) THEN
        RAISE EXCEPTION 'material unit is in use' USING ERRCODE='23514',CONSTRAINT='material_unit_in_use';
    END IF;
    IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL AND
        (EXISTS(SELECT 1 FROM sitetrack.company_store WHERE material_id=NEW.id AND (quantity>0 OR in_transit_to_projects>0)) OR
         EXISTS(SELECT 1 FROM sitetrack.project_store WHERE material_id=NEW.id AND (quantity>0 OR in_transit>0))) THEN
        RAISE EXCEPTION 'material still has stock or goods in transit' USING ERRCODE='23514',CONSTRAINT='material_stock';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION sitetrack.guard_project_inventory() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL AND (
        EXISTS(SELECT 1 FROM sitetrack.project_store i
               WHERE i.project_id=NEW.id AND (i.quantity>0 OR i.in_transit>0)) OR
        EXISTS(SELECT 1 FROM sitetrack.deliveries WHERE project_id=NEW.id AND status='in_transit')
    ) THEN
        RAISE EXCEPTION 'project store still has stock or goods in transit' USING ERRCODE='23514',CONSTRAINT='project_stock';
    END IF;
    RETURN NEW;
END;
$$;


---- create above / drop below ----

ALTER TABLE sitetrack.company_store RENAME TO company_inventory;
ALTER TABLE sitetrack.project_store RENAME TO project_inventory;

CREATE OR REPLACE FUNCTION sitetrack.guard_material_inventory() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.unit IS DISTINCT FROM OLD.unit AND
        (EXISTS(SELECT 1 FROM sitetrack.company_inventory WHERE material_id=NEW.id) OR
         EXISTS(SELECT 1 FROM sitetrack.project_inventory WHERE material_id=NEW.id)) THEN
        RAISE EXCEPTION 'material unit is in use' USING ERRCODE='23514',CONSTRAINT='material_unit_in_use';
    END IF;
    IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL AND
        (EXISTS(SELECT 1 FROM sitetrack.company_inventory WHERE material_id=NEW.id AND (quantity>0 OR in_transit_to_projects>0)) OR
         EXISTS(SELECT 1 FROM sitetrack.project_inventory WHERE material_id=NEW.id AND (quantity>0 OR in_transit>0))) THEN
        RAISE EXCEPTION 'material still has stock or goods in transit' USING ERRCODE='23514',CONSTRAINT='material_stock';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION sitetrack.guard_project_inventory() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL AND (
        EXISTS(SELECT 1 FROM sitetrack.project_inventory i
               WHERE i.project_id=NEW.id AND (i.quantity>0 OR i.in_transit>0)) OR
        EXISTS(SELECT 1 FROM sitetrack.deliveries WHERE project_id=NEW.id AND status='in_transit')
    ) THEN
        RAISE EXCEPTION 'project store still has stock or goods in transit' USING ERRCODE='23514',CONSTRAINT='project_stock';
    END IF;
    RETURN NEW;
END;
$$;

