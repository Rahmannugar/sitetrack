DROP VIEW sitetrack.project_stock;
ALTER TABLE sitetrack.project_inventory ADD COLUMN project_id text REFERENCES sitetrack.projects(id);
UPDATE sitetrack.project_inventory i SET project_id=s.project_id
FROM sitetrack.project_stores s WHERE s.id=i.project_store_id;
ALTER TABLE sitetrack.project_inventory ALTER COLUMN project_id SET NOT NULL;
ALTER TABLE sitetrack.project_inventory DROP CONSTRAINT project_inventory_pkey;
ALTER TABLE sitetrack.project_inventory DROP COLUMN project_store_id;
ALTER TABLE sitetrack.project_inventory ADD PRIMARY KEY(project_id,material_id);
CREATE INDEX project_inventory_quantity_idx ON sitetrack.project_inventory(project_id,quantity,material_id);
CREATE INDEX project_inventory_delivery_idx ON sitetrack.project_inventory(project_id,last_delivery_at,material_id);
DROP TABLE sitetrack.project_stores;

CREATE VIEW sitetrack.project_stock AS
SELECT i.project_id,m.id AS material_id,m.name,m.category_id,c.name AS category_name,m.unit,lower(m.name) AS sort_name,
    i.quantity,i.last_delivery_at,i.updated_at
FROM sitetrack.project_inventory i JOIN sitetrack.projects p ON p.id=i.project_id JOIN sitetrack.materials m ON m.id=i.material_id
JOIN sitetrack.material_categories c ON c.id=m.category_id
WHERE p.deleted_at IS NULL AND m.deleted_at IS NULL;

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

---- create above / drop below ----

DROP VIEW sitetrack.project_stock;
CREATE TABLE sitetrack.project_stores (
    id uuid PRIMARY KEY,
    project_id text NOT NULL UNIQUE REFERENCES sitetrack.projects(id),
    name text NOT NULL CHECK(char_length(name) BETWEEN 2 AND 170)
);
-- We recreate obsolete store identities only when restoring the previous schema.
INSERT INTO sitetrack.project_stores(id,project_id,name)
SELECT uuidv7(),id,name || ' store' FROM sitetrack.projects;
ALTER TABLE sitetrack.project_inventory ADD COLUMN project_store_id uuid REFERENCES sitetrack.project_stores(id);
UPDATE sitetrack.project_inventory i SET project_store_id=s.id
FROM sitetrack.project_stores s WHERE s.project_id=i.project_id;
ALTER TABLE sitetrack.project_inventory ALTER COLUMN project_store_id SET NOT NULL;
ALTER TABLE sitetrack.project_inventory DROP CONSTRAINT project_inventory_pkey;
ALTER TABLE sitetrack.project_inventory DROP COLUMN project_id;
ALTER TABLE sitetrack.project_inventory ADD PRIMARY KEY(project_store_id,material_id);
CREATE INDEX project_inventory_quantity_idx ON sitetrack.project_inventory(project_store_id,quantity,material_id);
CREATE INDEX project_inventory_delivery_idx ON sitetrack.project_inventory(project_store_id,last_delivery_at,material_id);

CREATE VIEW sitetrack.project_stock AS
SELECT s.id AS store_id,s.project_id,m.id AS material_id,m.name,m.category_id,c.name AS category_name,m.unit,lower(m.name) AS sort_name,
    i.quantity,i.last_delivery_at,i.updated_at
FROM sitetrack.project_inventory i JOIN sitetrack.project_stores s ON s.id=i.project_store_id
JOIN sitetrack.projects p ON p.id=s.project_id JOIN sitetrack.materials m ON m.id=i.material_id
JOIN sitetrack.material_categories c ON c.id=m.category_id
WHERE p.deleted_at IS NULL AND m.deleted_at IS NULL;

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

