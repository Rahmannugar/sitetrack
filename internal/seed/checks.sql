-- check: verified accounts
SELECT NOT EXISTS(SELECT 1 FROM sitetrack.users WHERE NOT email_verified OR password_hash='');
-- check: project admin and team bounds
SELECT NOT EXISTS(SELECT p.id FROM sitetrack.projects p LEFT JOIN sitetrack.project_memberships m ON m.project_id=p.id
 GROUP BY p.id HAVING count(m.user_id)>50 OR count(m.user_id)=0 OR count(*) FILTER(WHERE m.is_admin)=0);
-- check: task numbering
SELECT NOT EXISTS(SELECT p.id FROM sitetrack.projects p LEFT JOIN sitetrack.tasks t ON t.project_id=p.id
 GROUP BY p.id HAVING count(t.id)>3000 OR p.task_number<>max(substring(t.id FROM char_length(p.id)+4)::bigint));
-- check: dependency order
SELECT NOT EXISTS(SELECT 1 FROM sitetrack.task_dependencies WHERE required_task_id>=dependent_task_id);
-- check: dependency stored state
SELECT NOT EXISTS(SELECT 1 FROM sitetrack.task_dependencies d JOIN sitetrack.tasks t ON t.id=d.required_task_id
 WHERE d.status::text<>CASE WHEN t.status='done' THEN 'satisfied' ELSE 'waiting' END);
-- check: started task dependencies
SELECT NOT EXISTS(SELECT 1 FROM sitetrack.task_dependencies d JOIN sitetrack.tasks t ON t.id=d.dependent_task_id
 WHERE t.status<>'not_started' AND d.status='waiting');
-- check: task guard restoration
SELECT count(*)=2 FROM pg_trigger WHERE tgrelid IN ('sitetrack.tasks'::regclass,'sitetrack.task_dependencies'::regclass)
 AND tgname IN ('guard_task','guard_dependency') AND tgenabled='O';
-- check: invitation membership
SELECT NOT EXISTS(SELECT 1 FROM sitetrack.project_invitations i LEFT JOIN sitetrack.project_memberships m
 ON m.project_id=i.project_id AND m.user_id=i.invitee_id WHERE (i.status='accepted' AND m.user_id IS NULL) OR (i.status='pending' AND m.user_id IS NOT NULL));
-- check: delivery line bounds
SELECT NOT EXISTS(SELECT d.id FROM sitetrack.deliveries d LEFT JOIN sitetrack.delivery_items i ON i.delivery_id=d.id
 GROUP BY d.id HAVING count(i.material_id)<1 OR count(i.material_id)>100);
-- check: delivery sender authority
SELECT NOT EXISTS(SELECT 1 FROM sitetrack.deliveries d JOIN sitetrack.users u ON u.id=d.initiated_by_user_id
 LEFT JOIN sitetrack.project_memberships m ON m.project_id=d.project_id AND m.user_id=u.id
 WHERE (d.direction='company_to_project' AND u.role NOT IN ('company_store_manager','superadmin')) OR
       (d.direction='project_to_company' AND NOT COALESCE(m.is_admin,false)));
-- check: delivery completion authority
SELECT NOT EXISTS(SELECT 1 FROM sitetrack.deliveries d JOIN sitetrack.users u ON u.id=d.completed_by_user_id
 LEFT JOIN sitetrack.project_memberships m ON m.project_id=d.project_id AND m.user_id=u.id
 WHERE CASE WHEN (d.direction='company_to_project')=(d.status='cancelled')
 THEN u.role NOT IN ('company_store_manager','superadmin') ELSE NOT COALESCE(m.is_admin,false) END);
-- check: company transit matches deliveries
WITH pending AS (SELECT i.material_id,sum(i.quantity) AS quantity FROM sitetrack.delivery_items i
 JOIN sitetrack.deliveries d ON d.id=i.delivery_id WHERE d.direction='company_to_project' AND d.status='in_transit' GROUP BY i.material_id)
SELECT NOT EXISTS(SELECT 1 FROM sitetrack.company_store c FULL JOIN pending p ON p.material_id=c.material_id
 WHERE COALESCE(c.in_transit_to_projects,0)<>COALESCE(p.quantity,0));
-- check: project transit matches deliveries
WITH pending AS (SELECT d.project_id,i.material_id,sum(i.quantity) AS quantity FROM sitetrack.delivery_items i
 JOIN sitetrack.deliveries d ON d.id=i.delivery_id WHERE d.direction='project_to_company' AND d.status='in_transit' GROUP BY d.project_id,i.material_id)
SELECT NOT EXISTS(SELECT 1 FROM sitetrack.project_store c FULL JOIN pending p ON p.project_id=c.project_id AND p.material_id=c.material_id
 WHERE COALESCE(c.in_transit,0)<>COALESCE(p.quantity,0));
-- check: quantities conserved per material
WITH projects AS (SELECT material_id,sum(quantity+in_transit) AS quantity FROM sitetrack.project_store GROUP BY material_id)
SELECT NOT EXISTS(SELECT 1 FROM seed_supplies s LEFT JOIN sitetrack.company_store c ON c.material_id=s.material_id
 LEFT JOIN projects p ON p.material_id=s.material_id
 WHERE c.material_id IS NULL OR s.quantity<>c.quantity+c.in_transit_to_projects+COALESCE(p.quantity,0));
-- check: project history never overspends
WITH movements AS (
 SELECT d.project_id,i.material_id,d.completed_at AS at,i.quantity AS delta FROM sitetrack.delivery_items i
 JOIN sitetrack.deliveries d ON d.id=i.delivery_id WHERE d.direction='company_to_project' AND d.status='delivered'
 UNION ALL
 SELECT d.project_id,i.material_id,d.created_at,-i.quantity FROM sitetrack.delivery_items i
 JOIN sitetrack.deliveries d ON d.id=i.delivery_id WHERE d.direction='project_to_company'
 UNION ALL
 SELECT d.project_id,i.material_id,d.completed_at,i.quantity FROM sitetrack.delivery_items i
 JOIN sitetrack.deliveries d ON d.id=i.delivery_id WHERE d.direction='project_to_company' AND d.status IN ('rejected','cancelled')
), balances AS (SELECT sum(delta) OVER(PARTITION BY project_id,material_id ORDER BY at ROWS UNBOUNDED PRECEDING) AS quantity FROM movements)
SELECT NOT EXISTS(SELECT 1 FROM balances WHERE quantity<0);
-- check: notification revisions
SELECT NOT EXISTS(SELECT 1 FROM sitetrack.notification_streams s LEFT JOIN
 (SELECT user_id,count(*) AS quantity,max(revision) AS latest FROM sitetrack.notification_changes GROUP BY user_id) c ON c.user_id=s.user_id
 WHERE s.revision<>COALESCE(c.quantity,0) OR s.revision<>COALESCE(c.latest,0));
-- check: no pending email effects
SELECT NOT EXISTS(SELECT 1 FROM sitetrack.outbox_events) AND NOT EXISTS(SELECT 1 FROM sitetrack.email_verifications);
-- check: catalogue stock variety
SELECT count(DISTINCT status)=3 FROM sitetrack.company_stock;
