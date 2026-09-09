-- Resolve historical matched orders with the previous application before upgrading.
-- Never guess their financial outcome or discard their receipt linkage.
DO $$
BEGIN
 IF EXISTS (SELECT 1 FROM lulu_orders WHERE status='matched') THEN
  RAISE EXCEPTION 'Resolve historical Lulu matched orders before applying migration 0073';
 END IF;
END $$;
ALTER TABLE lulu_orders DROP CONSTRAINT lulu_orders_status_check;
ALTER TABLE lulu_orders ADD CONSTRAINT lulu_orders_status_check
 CHECK(status IN ('requested','approved','sending','unknown','confirmed','rejected','failed','expired'));
DROP INDEX lulu_active_sender;
CREATE UNIQUE INDEX lulu_active_sender ON lulu_orders(receiver_uid,lulu_uid)
 WHERE kind='deposit' AND status='requested';
