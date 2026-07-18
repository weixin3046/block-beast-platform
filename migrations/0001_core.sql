CREATE EXTENSION IF NOT EXISTS ltree;

CREATE TABLE users (
    id UUID PRIMARY KEY,
    login_name TEXT UNIQUE,
    display_name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'bet_banned')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE roles (
    id UUID PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL
);

CREATE TABLE user_roles (
    user_id UUID NOT NULL REFERENCES users(id),
    role_id UUID NOT NULL REFERENCES roles(id),
    PRIMARY KEY (user_id, role_id)
);

CREATE TABLE auth_identities (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    provider TEXT NOT NULL,
    subject TEXT NOT NULL,
    password_hash TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, subject)
);

CREATE TABLE sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE wallets (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    currency TEXT NOT NULL,
    available_minor BIGINT NOT NULL DEFAULT 0,
    frozen_minor BIGINT NOT NULL DEFAULT 0,
    version BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, currency),
    CHECK (available_minor >= 0),
    CHECK (frozen_minor >= 0)
);

CREATE TABLE ledger_entries (
    id UUID PRIMARY KEY,
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    business_type TEXT NOT NULL,
    business_id TEXT NOT NULL,
    entry_type TEXT NOT NULL,
    amount_minor BIGINT NOT NULL,
    balance_after_minor BIGINT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (wallet_id, business_type, business_id, entry_type)
);

CREATE TABLE game_types (
    id UUID PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    rules JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE rounds (
    id UUID PRIMARY KEY,
    game_type_id UUID NOT NULL REFERENCES game_types(id),
    sequence BIGINT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('open', 'closed', 'settling', 'settled', 'cancelled')),
    bet_closes_at TIMESTAMPTZ NOT NULL,
    outcome JSONB,
    settled_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 0,
    UNIQUE (game_type_id, sequence)
);

CREATE TABLE bets (
    id UUID PRIMARY KEY,
    client_request_id TEXT NOT NULL,
    round_id UUID NOT NULL REFERENCES rounds(id),
    user_id UUID NOT NULL REFERENCES users(id),
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    selection JSONB NOT NULL,
    stake_minor BIGINT NOT NULL CHECK (stake_minor > 0),
    status TEXT NOT NULL CHECK (status IN ('accepted', 'cancelled', 'lost', 'won', 'refunded')),
    payout_minor BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    settled_at TIMESTAMPTZ,
    UNIQUE (user_id, client_request_id)
);

CREATE TABLE agent_relations (
    user_id UUID PRIMARY KEY REFERENCES users(id),
    parent_user_id UUID REFERENCES users(id),
    path LTREE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE commission_entries (
    id UUID PRIMARY KEY,
    source_bet_id UUID NOT NULL REFERENCES bets(id),
    beneficiary_user_id UUID NOT NULL REFERENCES users(id),
    amount_minor BIGINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'reversed')),
    UNIQUE (source_bet_id, beneficiary_user_id)
);

CREATE TABLE chat_rooms (
    id UUID PRIMARY KEY,
    room_type TEXT NOT NULL CHECK (room_type IN ('global', 'game', 'customer_service', 'direct')),
    game_type_id UUID REFERENCES game_types(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE chat_messages (
    id UUID PRIMARY KEY,
    room_id UUID NOT NULL REFERENCES chat_rooms(id),
    sender_user_id UUID REFERENCES users(id),
    body TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'visible' CHECK (status IN ('visible', 'hidden', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE chain_addresses (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    chain_code TEXT NOT NULL,
    token_code TEXT NOT NULL,
    address TEXT NOT NULL,
    UNIQUE (chain_code, token_code, address)
);

CREATE TABLE deposits (
    id UUID PRIMARY KEY,
    chain_address_id UUID NOT NULL REFERENCES chain_addresses(id),
    provider_event_id TEXT NOT NULL,
    tx_hash TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    status TEXT NOT NULL CHECK (status IN ('detected', 'confirmed', 'credited', 'reversed')),
    confirmed_at TIMESTAMPTZ,
    UNIQUE (provider_event_id),
    UNIQUE (tx_hash)
);

CREATE TABLE withdrawals (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    client_request_id TEXT NOT NULL,
    destination_address TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    status TEXT NOT NULL CHECK (status IN ('requested', 'approved', 'broadcasted', 'confirmed', 'failed', 'cancelled')),
    provider_order_id TEXT UNIQUE,
    tx_hash TEXT UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, client_request_id)
);

CREATE TABLE platform_configs (
    key TEXT PRIMARY KEY,
    value JSONB NOT NULL,
    updated_by UUID REFERENCES users(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE announcements (
    id UUID PRIMARY KEY,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    starts_at TIMESTAMPTZ,
    ends_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE uploads (
    id UUID PRIMARY KEY,
    owner_user_id UUID REFERENCES users(id),
    storage_key TEXT NOT NULL UNIQUE,
    content_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_logs (
    id UUID PRIMARY KEY,
    actor_user_id UUID REFERENCES users(id),
    action TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id TEXT NOT NULL,
    request_id TEXT,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE outbox_events (
    id UUID PRIMARY KEY,
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX bets_round_status_idx ON bets(round_id, status);
CREATE INDEX ledger_entries_wallet_occurred_idx ON ledger_entries(wallet_id, occurred_at DESC);
CREATE INDEX chat_messages_room_created_idx ON chat_messages(room_id, created_at DESC);
CREATE INDEX outbox_events_unpublished_idx ON outbox_events(occurred_at) WHERE published_at IS NULL;