-- PostgreSQL delivers NOTIFY only on commit; rollback emits no wakeup.
-- Payload carries no account details. The worker reads the durable outbox.
CREATE FUNCTION notify_wallet_outbox_ready() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('wallet_outbox_ready', '');
    RETURN NEW;
END $$;
CREATE TRIGGER wallet_outbox_ready AFTER INSERT ON outbox_events
FOR EACH ROW WHEN (NEW.event_type = 'wallet.ledger.committed')
EXECUTE FUNCTION notify_wallet_outbox_ready();
