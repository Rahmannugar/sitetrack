CREATE TEMP TABLE seed_project_balances ON COMMIT DROP AS
SELECT d.project_id,i.material_id,
 sum(CASE WHEN d.direction='company_to_project' AND d.status='delivered' THEN i.quantity
          WHEN d.direction='project_to_company' AND d.status IN ('delivered','in_transit') THEN -i.quantity ELSE 0 END) AS quantity,
 sum(CASE WHEN d.direction='project_to_company' AND d.status='in_transit' THEN i.quantity ELSE 0 END) AS in_transit,
 max(CASE WHEN d.direction='company_to_project' AND d.status='delivered' THEN d.completed_at
          WHEN d.direction='project_to_company' AND d.status IN ('cancelled','rejected') THEN d.completed_at
          WHEN d.direction='project_to_company' THEN d.created_at END) AS last_delivery_at,
 min(d.created_at) AS created_at,max(COALESCE(d.completed_at,d.created_at)) AS updated_at
FROM sitetrack.delivery_items i JOIN sitetrack.deliveries d ON d.id=i.delivery_id
GROUP BY d.project_id,i.material_id;

INSERT INTO sitetrack.project_store(project_id,material_id,quantity,in_transit,last_delivery_at,created_at,updated_at)
SELECT project_id,material_id,quantity,in_transit,last_delivery_at,created_at,updated_at
FROM seed_project_balances ORDER BY project_id,material_id;

CREATE TEMP TABLE seed_company_flows ON COMMIT DROP AS
SELECT i.material_id,
 sum(CASE WHEN d.direction='company_to_project' THEN i.quantity ELSE 0 END) AS sent,
 sum(CASE WHEN d.direction='company_to_project' AND d.status IN ('delivered','in_transit') THEN -i.quantity
          WHEN d.direction='project_to_company' AND d.status='delivered' THEN i.quantity ELSE 0 END) AS delta,
 sum(CASE WHEN d.direction='company_to_project' AND d.status='in_transit' THEN i.quantity ELSE 0 END) AS in_transit,
 min(d.created_at) AS created_at,max(COALESCE(d.completed_at,d.created_at)) AS updated_at
FROM sitetrack.delivery_items i JOIN sitetrack.deliveries d ON d.id=i.delivery_id GROUP BY i.material_id;

CREATE TEMP TABLE seed_supplies ON COMMIT DROP AS
SELECT m.id AS material_id,
 (COALESCE(c.sent,0)+CASE WHEN COALESCE(c.sent,0)>0 THEN 100
                        WHEN m.ordinal%3=0 THEN 0 WHEN m.ordinal%3=1 THEN 20 ELSE 100 END)::numeric(18,3) AS quantity,
 m.ordinal
FROM (SELECT id,row_number() OVER(ORDER BY id) AS ordinal FROM sitetrack.materials) m
LEFT JOIN seed_company_flows c ON c.material_id=m.id;

INSERT INTO sitetrack.company_store(material_id,quantity,reorder_point,in_transit_to_projects,created_at,updated_at)
SELECT s.material_id,s.quantity+COALESCE(c.delta,0),
 CASE WHEN s.ordinal%3=1 THEN 1000000000 ELSE 50 END,COALESCE(c.in_transit,0),
 COALESCE(c.created_at-interval '1 day',$1::timestamptz-interval '1 year'),COALESCE(c.updated_at,$1::timestamptz)
FROM seed_supplies s LEFT JOIN seed_company_flows c ON c.material_id=s.material_id ORDER BY s.material_id;
