-- Shared transactional provisioning for registration and admin-created players.
-- Existing rooms, IDs, messages and staff membership are preserved.
CREATE FUNCTION ensure_user_customer_service_rooms(player_id UUID)
RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id
        JOIN roles r ON r.id=ur.role_id
        WHERE u.id=player_id AND NOT u.is_virtual AND r.code='player') THEN
        RETURN;
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('customer-service:'||player_id::text,0));
    INSERT INTO chat_rooms(id,room_type,customer_user_id,service_type)
    SELECT gen_random_uuid(),'customer_service',player_id,kind
    FROM (VALUES ('deposit'),('withdrawal')) types(kind)
    ON CONFLICT (customer_user_id,service_type) WHERE room_type='customer_service' DO NOTHING;
    INSERT INTO chat_room_members(room_id,user_id,member_role)
    SELECT id,player_id,'owner' FROM chat_rooms
    WHERE room_type='customer_service' AND customer_user_id=player_id
    ON CONFLICT(room_id,user_id) DO NOTHING;
END $$;

-- Backfill only real players, including owners of a single existing room.
SELECT ensure_user_customer_service_rooms(u.id)
FROM users u WHERE NOT u.is_virtual AND EXISTS (
    SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id
    WHERE ur.user_id=u.id AND r.code='player'
);
