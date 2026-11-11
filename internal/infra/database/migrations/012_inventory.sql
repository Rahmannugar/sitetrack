CREATE TYPE sitetrack.stock_status AS ENUM ('out_of_stock','low_stock','available');

CREATE TABLE sitetrack.company_stores (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 170)
);
CREATE UNIQUE INDEX company_store_singleton_idx ON sitetrack.company_stores((true));

CREATE TABLE sitetrack.company_inventory (
    company_store_id uuid NOT NULL REFERENCES sitetrack.company_stores(id),
    material_id uuid NOT NULL REFERENCES sitetrack.materials(id),
    quantity numeric(18,3) NOT NULL DEFAULT 0 CHECK (quantity BETWEEN 0 AND 999999999999999.999),
    reorder_point numeric(18,3) NOT NULL DEFAULT 0 CHECK (reorder_point BETWEEN 0 AND 999999999999999.999),
    in_transit_to_projects numeric(18,3) NOT NULL DEFAULT 0 CHECK (in_transit_to_projects BETWEEN 0 AND 999999999999999.999),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(company_store_id,material_id)
);
CREATE INDEX company_inventory_material_idx ON sitetrack.company_inventory(material_id);

CREATE TABLE sitetrack.project_inventory (
    project_store_id uuid NOT NULL REFERENCES sitetrack.project_stores(id),
    material_id uuid NOT NULL REFERENCES sitetrack.materials(id),
    quantity numeric(18,3) NOT NULL DEFAULT 0 CHECK (quantity BETWEEN 0 AND 999999999999999.999),
    in_transit numeric(18,3) NOT NULL DEFAULT 0 CHECK (in_transit BETWEEN 0 AND 999999999999999.999),
    last_delivery_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(project_store_id,material_id)
);
CREATE INDEX project_inventory_material_idx ON sitetrack.project_inventory(material_id);
CREATE INDEX project_inventory_quantity_idx ON sitetrack.project_inventory(project_store_id,quantity,material_id);
CREATE INDEX project_inventory_delivery_idx ON sitetrack.project_inventory(project_store_id,last_delivery_at,material_id);

CREATE VIEW sitetrack.company_stock AS
SELECT s.id AS store_id,m.id AS material_id,m.name,m.category_id,c.name AS category_name,m.unit,lower(m.name) AS sort_name,
    COALESCE(i.quantity,0)::numeric(18,3) AS quantity,COALESCE(i.reorder_point,0)::numeric(18,3) AS reorder_point,
    COALESCE(i.in_transit_to_projects,0)::numeric(18,3) AS in_transit_to_projects,
    (CASE WHEN COALESCE(i.quantity,0)=0 THEN 'out_of_stock' WHEN i.quantity<=i.reorder_point THEN 'low_stock' ELSE 'available' END)::sitetrack.stock_status AS status,
    COALESCE(i.updated_at,m.created_at) AS updated_at
FROM sitetrack.company_stores s CROSS JOIN sitetrack.materials m
JOIN sitetrack.material_categories c ON c.id=m.category_id
LEFT JOIN sitetrack.company_inventory i ON i.company_store_id=s.id AND i.material_id=m.id
WHERE m.deleted_at IS NULL;

CREATE VIEW sitetrack.project_stock AS
SELECT s.id AS store_id,s.project_id,m.id AS material_id,m.name,m.category_id,c.name AS category_name,m.unit,lower(m.name) AS sort_name,
    i.quantity,i.last_delivery_at,i.updated_at
FROM sitetrack.project_inventory i JOIN sitetrack.project_stores s ON s.id=i.project_store_id
JOIN sitetrack.projects p ON p.id=s.project_id JOIN sitetrack.materials m ON m.id=i.material_id
JOIN sitetrack.material_categories c ON c.id=m.category_id
WHERE p.deleted_at IS NULL AND m.deleted_at IS NULL;

CREATE FUNCTION sitetrack.guard_material_inventory() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER guard_material_inventory BEFORE UPDATE ON sitetrack.materials FOR EACH ROW EXECUTE FUNCTION sitetrack.guard_material_inventory();

CREATE FUNCTION sitetrack.guard_project_inventory() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER guard_project_inventory BEFORE UPDATE ON sitetrack.projects FOR EACH ROW EXECUTE FUNCTION sitetrack.guard_project_inventory();

---- create above / drop below ----

DROP TRIGGER guard_project_inventory ON sitetrack.projects;
DROP FUNCTION sitetrack.guard_project_inventory();
DROP TRIGGER guard_material_inventory ON sitetrack.materials;
DROP FUNCTION sitetrack.guard_material_inventory();
DROP VIEW sitetrack.project_stock;
DROP VIEW sitetrack.company_stock;
DROP TABLE sitetrack.project_inventory;
DROP TABLE sitetrack.company_inventory;
DROP TABLE sitetrack.company_stores;
DROP TYPE sitetrack.stock_status;
