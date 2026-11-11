DROP VIEW sitetrack.company_stock;
ALTER TABLE sitetrack.company_inventory DROP CONSTRAINT company_inventory_pkey;
ALTER TABLE sitetrack.company_inventory DROP COLUMN company_store_id;
DROP INDEX sitetrack.company_inventory_material_idx;
ALTER TABLE sitetrack.company_inventory ADD PRIMARY KEY(material_id);
DROP TABLE sitetrack.company_stores;

CREATE VIEW sitetrack.company_stock AS
SELECT m.id AS material_id,m.name,m.category_id,c.name AS category_name,m.unit,lower(m.name) AS sort_name,
    COALESCE(i.quantity,0)::numeric(18,3) AS quantity,COALESCE(i.reorder_point,0)::numeric(18,3) AS reorder_point,
    COALESCE(i.in_transit_to_projects,0)::numeric(18,3) AS in_transit_to_projects,
    (CASE WHEN COALESCE(i.quantity,0)=0 THEN 'out_of_stock' WHEN i.quantity<=i.reorder_point THEN 'low_stock' ELSE 'available' END)::sitetrack.stock_status AS status,
    COALESCE(i.updated_at,m.created_at) AS updated_at
FROM sitetrack.materials m
JOIN sitetrack.material_categories c ON c.id=m.category_id
LEFT JOIN sitetrack.company_inventory i ON i.material_id=m.id
WHERE m.deleted_at IS NULL;

---- create above / drop below ----

DROP VIEW sitetrack.company_stock;
CREATE TABLE sitetrack.company_stores (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 170)
);
CREATE UNIQUE INDEX company_store_singleton_idx ON sitetrack.company_stores((true));
INSERT INTO sitetrack.company_stores(id,name) VALUES('019f2aa0-7000-7000-8000-000000000020','Company store');
ALTER TABLE sitetrack.company_inventory ADD COLUMN company_store_id uuid REFERENCES sitetrack.company_stores(id);
UPDATE sitetrack.company_inventory SET company_store_id=(SELECT id FROM sitetrack.company_stores);
ALTER TABLE sitetrack.company_inventory ALTER COLUMN company_store_id SET NOT NULL;
ALTER TABLE sitetrack.company_inventory DROP CONSTRAINT company_inventory_pkey;
ALTER TABLE sitetrack.company_inventory ADD PRIMARY KEY(company_store_id,material_id);
CREATE INDEX company_inventory_material_idx ON sitetrack.company_inventory(material_id);

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

