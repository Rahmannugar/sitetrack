ALTER TYPE sitetrack.invitation_status ADD VALUE 'expired';

---- create above / drop below ----

DROP INDEX sitetrack.invitation_pending_idx;
DO $$ DECLARE rule record;
BEGIN
 FOR rule IN SELECT conname FROM pg_constraint WHERE conrelid='sitetrack.project_invitations'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%status%' LOOP
  EXECUTE format('ALTER TABLE sitetrack.project_invitations DROP CONSTRAINT %I',rule.conname);
 END LOOP;
END $$;
ALTER TABLE sitetrack.project_invitations ALTER COLUMN status DROP DEFAULT;
UPDATE sitetrack.project_invitations SET status='withdrawn' WHERE status='expired';
CREATE TYPE sitetrack.invitation_status_previous AS ENUM ('pending','accepted','rejected','withdrawn');
ALTER TABLE sitetrack.project_invitations ALTER COLUMN status TYPE sitetrack.invitation_status_previous USING status::text::sitetrack.invitation_status_previous;
DROP TYPE sitetrack.invitation_status;
ALTER TYPE sitetrack.invitation_status_previous RENAME TO invitation_status;
ALTER TABLE sitetrack.project_invitations ALTER COLUMN status SET DEFAULT 'pending';
ALTER TABLE sitetrack.project_invitations ADD CONSTRAINT project_invitations_check1 CHECK((status='pending')=(completed_at IS NULL));
CREATE UNIQUE INDEX invitation_pending_idx ON sitetrack.project_invitations(project_id,invitee_id) WHERE status='pending';
