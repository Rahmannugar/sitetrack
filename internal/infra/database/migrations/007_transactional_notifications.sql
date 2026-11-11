INSERT INTO sitetrack.notifications(id,event_id,user_id,project_id,invitation_id,type,title,message)
SELECT (lpad(to_hex(floor(extract(epoch FROM clock_timestamp())*1000)::bigint),12,'0') || '7' || substr(replace(gen_random_uuid()::text,'-',''),14,3) || substr(replace(gen_random_uuid()::text,'-',''),17,16))::uuid,e.id,r.user_id,p.id,i.id,
    content.payload->>'kind',content.payload->>'title',content.payload->>'message'
FROM sitetrack.outbox_events e
JOIN sitetrack.outbox_recipients r ON r.event_id=e.id AND r.delivered_at IS NULL
CROSS JOIN LATERAL (SELECT convert_from(e.payload,'UTF8')::jsonb AS payload) content
JOIN sitetrack.projects p ON p.id=content.payload->>'project_id' AND p.deleted_at IS NULL
JOIN sitetrack.users u ON u.id=r.user_id
LEFT JOIN sitetrack.project_memberships m ON m.project_id=p.id AND m.user_id=u.id
LEFT JOIN sitetrack.project_invitations i ON i.id=(content.payload->>'invitation_id')::uuid AND i.project_id=p.id AND i.invitee_id=u.id
WHERE e.event_type='notification.project' AND e.status IN ('pending','processing')
AND (i.id IS NOT NULL OR (content.payload->>'invitation_id' IS NULL AND (m.user_id IS NOT NULL OR u.role='superadmin')))
ORDER BY r.user_id,e.created_at,e.id
ON CONFLICT(event_id,user_id) DO NOTHING;

ALTER TABLE sitetrack.notifications DROP COLUMN event_id;
CREATE UNIQUE INDEX notifications_invitation_user_idx ON sitetrack.notifications(invitation_id,user_id) WHERE invitation_id IS NOT NULL;
DROP TABLE sitetrack.outbox_recipients;
DELETE FROM sitetrack.outbox_events WHERE event_type='notification.project';

---- create above / drop below ----

DROP INDEX sitetrack.notifications_invitation_user_idx;
ALTER TABLE sitetrack.notifications ADD COLUMN event_id uuid;
ALTER TABLE sitetrack.notifications DISABLE TRIGGER notification_change;
UPDATE sitetrack.notifications SET event_id=(lpad(to_hex(floor(extract(epoch FROM clock_timestamp())*1000)::bigint),12,'0') || '7' || substr(replace(gen_random_uuid()::text,'-',''),14,3) || substr(replace(gen_random_uuid()::text,'-',''),17,16))::uuid;
ALTER TABLE sitetrack.notifications ENABLE TRIGGER notification_change;
INSERT INTO sitetrack.outbox_events(id,aggregate_id,event_type,status,created_at,published_at)
SELECT event_id,project_id,'notification.project','published',created_at,created_at FROM sitetrack.notifications;
ALTER TABLE sitetrack.notifications ALTER COLUMN event_id SET NOT NULL;
ALTER TABLE sitetrack.notifications ADD FOREIGN KEY(event_id) REFERENCES sitetrack.outbox_events(id);
ALTER TABLE sitetrack.notifications ADD UNIQUE(event_id,user_id);
CREATE TABLE sitetrack.outbox_recipients (
    event_id uuid NOT NULL REFERENCES sitetrack.outbox_events(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES sitetrack.users(id) ON DELETE CASCADE,
    delivered_at timestamptz,
    PRIMARY KEY(event_id,user_id)
);
CREATE INDEX outbox_recipients_pending_idx ON sitetrack.outbox_recipients(event_id,user_id) WHERE delivered_at IS NULL;
INSERT INTO sitetrack.outbox_recipients(event_id,user_id,delivered_at)
SELECT event_id,user_id,created_at FROM sitetrack.notifications;
