-- Stop API/Worker/Realtime before upgrading. UUID relationships and balances
-- stay unchanged. Keep a permanent old/new mapping for historical audit lookup.
LOCK TABLE users IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF (SELECT count(*) FROM users)>89999 THEN
  RAISE EXCEPTION 'five-digit user ID capacity exceeded (10001..99999)';
 END IF;
END $$;
CREATE TABLE user_public_id_history (
 user_id UUID PRIMARY KEY,
 old_public_id BIGINT NOT NULL UNIQUE,
 new_public_id BIGINT NOT NULL UNIQUE,
 changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO user_public_id_history(user_id,old_public_id,new_public_id)
SELECT id,public_id,10000+row_number() OVER(ORDER BY public_id) FROM users;
ALTER TABLE users DROP CONSTRAINT users_public_id_minimum;
-- All existing IDs are >=100000, so the new range is disjoint and no temporary
-- negative IDs are necessary. The 0078 trigger also sets invitation_code.
UPDATE users u SET public_id=h.new_public_id FROM user_public_id_history h WHERE h.user_id=u.id;
ALTER TABLE users ADD CONSTRAINT users_public_id_five_digits CHECK(public_id BETWEEN 10001 AND 99999);
ALTER SEQUENCE users_public_id_seq RESTART WITH 10001;
ALTER SEQUENCE users_public_id_seq START WITH 10001 MINVALUE 10001 MAXVALUE 99999 NO CYCLE;
SELECT setval('users_public_id_seq',COALESCE((SELECT max(public_id) FROM users),10001),EXISTS(SELECT 1 FROM users));

UPDATE leaderboard_entries e SET public_user_id=u.public_id FROM users u WHERE u.id=e.user_id;

-- Translate live/replay payload identifiers, but preserve historical audit
-- records and already-published events. Internal UUID strings are untouched.
CREATE FUNCTION remap_public_ids_0081(data JSONB) RETURNS JSONB LANGUAGE plpgsql AS $$
DECLARE k TEXT; v JSONB; result JSONB; replacement BIGINT;
BEGIN
 IF jsonb_typeof(data)='array' THEN
  SELECT COALESCE(jsonb_agg(remap_public_ids_0081(value) ORDER BY ord),'[]'::jsonb)
   INTO result FROM jsonb_array_elements(data) WITH ORDINALITY a(value,ord);
  RETURN result;
 ELSIF jsonb_typeof(data)='object' THEN
  result := '{}'::jsonb;
  FOR k,v IN SELECT * FROM jsonb_each(data) LOOP
   replacement := NULL;
   IF k IN ('user_id','account_id','public_id','public_user_id','agent_id','parent_user_id','operator_user_id','beneficiary_user_id','sender_user_id','invitation_code','parent_invitation_code') THEN
    SELECT new_public_id INTO replacement FROM user_public_id_history WHERE old_public_id::text=(v#>>'{}');
   END IF;
   IF replacement IS NOT NULL THEN
    v := CASE WHEN jsonb_typeof(v)='string' THEN to_jsonb(replacement::text) ELSE to_jsonb(replacement) END;
   ELSE v := remap_public_ids_0081(v);
   END IF;
   result := result || jsonb_build_object(k,v);
  END LOOP;
  RETURN result;
 END IF;
 RETURN data;
END $$;
UPDATE robot_plans SET create_input=remap_public_ids_0081(create_input);
UPDATE admin_bet_voids SET result=remap_public_ids_0081(result);
UPDATE outbox_events SET payload=remap_public_ids_0081(payload) WHERE published_at IS NULL;
DROP FUNCTION remap_public_ids_0081(JSONB);
