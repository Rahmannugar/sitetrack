ALTER TABLE sitetrack.project_invitations ADD COLUMN expires_at timestamptz;
UPDATE sitetrack.project_invitations SET expires_at=created_at+interval '168 hours';
ALTER TABLE sitetrack.project_invitations ALTER COLUMN expires_at SET NOT NULL;
ALTER TABLE sitetrack.project_invitations ADD CONSTRAINT invitation_lifetime CHECK(expires_at=created_at+interval '168 hours');
CREATE FUNCTION sitetrack.set_invitation_expiry() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.expires_at := NEW.created_at+interval '168 hours';
    RETURN NEW;
END;
$$;
CREATE TRIGGER invitation_expiry BEFORE INSERT ON sitetrack.project_invitations FOR EACH ROW EXECUTE FUNCTION sitetrack.set_invitation_expiry();

CREATE VIEW sitetrack.invitation_states AS
SELECT id,project_id,inviter_id,invitee_id,
 (CASE WHEN status='pending' AND expires_at<=statement_timestamp() THEN 'expired'::sitetrack.invitation_status ELSE status END)::sitetrack.invitation_status AS status,
 created_at,
 (CASE WHEN status='pending' AND expires_at<=statement_timestamp() THEN expires_at ELSE completed_at END)::timestamptz AS completed_at,
 expires_at
FROM sitetrack.project_invitations;

---- create above / drop below ----

DROP VIEW sitetrack.invitation_states;
DROP TRIGGER invitation_expiry ON sitetrack.project_invitations;
DROP FUNCTION sitetrack.set_invitation_expiry();
ALTER TABLE sitetrack.project_invitations DROP COLUMN expires_at;
