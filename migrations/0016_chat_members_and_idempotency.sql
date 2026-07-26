ALTER TABLE chat_messages
    ADD COLUMN client_request_id TEXT;

CREATE UNIQUE INDEX chat_messages_sender_request_idx
    ON chat_messages (room_id, sender_user_id, client_request_id)
    WHERE sender_user_id IS NOT NULL AND client_request_id IS NOT NULL;

CREATE TABLE chat_room_members (
    room_id UUID NOT NULL REFERENCES chat_rooms(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    member_role TEXT NOT NULL DEFAULT 'member' CHECK (member_role IN ('member', 'owner')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, user_id)
);

CREATE INDEX chat_room_members_user_idx ON chat_room_members (user_id, joined_at DESC);

INSERT INTO chat_rooms (id, room_type)
VALUES ('00000000-0000-0000-0000-000000000001', 'global')
ON CONFLICT (id) DO NOTHING;
