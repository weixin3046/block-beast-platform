-- Each customer service conversation belongs to one player and one service type.
-- Legacy single customer service rooms become the player's deposit (top-up) room.
ALTER TABLE chat_rooms
    ADD COLUMN customer_user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    ADD COLUMN service_type TEXT;

UPDATE chat_rooms AS room
SET customer_user_id = member.user_id,
    service_type = 'deposit'
FROM chat_room_members AS member
WHERE member.room_id = room.id
  AND room.room_type = 'customer_service';

ALTER TABLE chat_rooms
    ADD CONSTRAINT chat_rooms_customer_service_details_check CHECK (
        (room_type = 'customer_service'
            AND customer_user_id IS NOT NULL
            AND service_type IN ('deposit', 'withdrawal'))
        OR
        (room_type <> 'customer_service'
            AND customer_user_id IS NULL
            AND service_type IS NULL)
    );

CREATE UNIQUE INDEX chat_rooms_customer_service_owner_type_key
    ON chat_rooms (customer_user_id, service_type)
    WHERE room_type = 'customer_service';
