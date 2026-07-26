CREATE TABLE red_packets (
    id UUID PRIMARY KEY,
    room_id UUID NOT NULL REFERENCES chat_rooms(id),
    sender_user_id UUID NOT NULL REFERENCES users(id),
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    client_request_id TEXT NOT NULL,
    currency TEXT NOT NULL CHECK (currency IN ('USDT', 'POINTS')),
    greeting TEXT NOT NULL DEFAULT '',
    total_minor BIGINT NOT NULL CHECK (total_minor > 0),
    remaining_minor BIGINT NOT NULL CHECK (remaining_minor >= 0),
    packet_count INTEGER NOT NULL CHECK (packet_count > 0),
    claimed_count INTEGER NOT NULL DEFAULT 0 CHECK (claimed_count >= 0),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'exhausted', 'refunded')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (sender_user_id, client_request_id)
);

CREATE TABLE red_packet_claims (
    id UUID PRIMARY KEY,
    red_packet_id UUID NOT NULL REFERENCES red_packets(id),
    user_id UUID NOT NULL REFERENCES users(id),
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (red_packet_id, user_id)
);

CREATE INDEX red_packets_room_created_idx ON red_packets (room_id, created_at DESC);
CREATE INDEX red_packets_active_expiry_idx ON red_packets (expires_at) WHERE status = 'active';
CREATE INDEX red_packet_claims_packet_time_idx ON red_packet_claims (red_packet_id, claimed_at);
