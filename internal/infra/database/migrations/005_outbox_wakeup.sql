CREATE FUNCTION sitetrack.notify_outbox() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('sitetrack_outbox', '');
    RETURN NULL;
END;
$$;

CREATE TRIGGER outbox_wakeup AFTER INSERT ON sitetrack.outbox_events
FOR EACH STATEMENT EXECUTE FUNCTION sitetrack.notify_outbox();

---- create above / drop below ----

DROP TRIGGER outbox_wakeup ON sitetrack.outbox_events;
DROP FUNCTION sitetrack.notify_outbox();
