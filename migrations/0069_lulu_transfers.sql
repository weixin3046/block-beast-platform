-- Lulu 彩石 uses integer units, 1:1 with LULU. No existing balances are rescaled.
INSERT INTO currencies(code,name,decimals,category,create_on_registration,sort_order)
VALUES('LULU','噜噜币',0,'custom',true,45);
INSERT INTO wallets(id,user_id,currency)
SELECT gen_random_uuid(),id,'LULU' FROM users WHERE NOT is_virtual
ON CONFLICT(user_id,currency) DO NOTHING;

CREATE TABLE lulu_orders (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id UUID NOT NULL REFERENCES users(id),
 kind TEXT NOT NULL CHECK(kind IN ('deposit','withdrawal')),
 request_id TEXT NOT NULL CHECK(length(request_id) BETWEEN 1 AND 128),
 lulu_uid TEXT NOT NULL CHECK(lulu_uid ~ '^[1-9][0-9]{2,19}$'),
 receiver_uid TEXT NOT NULL CHECK(receiver_uid ~ '^[1-9][0-9]{2,19}$'),
 amount BIGINT NOT NULL CHECK(amount>0),
 status TEXT NOT NULL CHECK(status IN ('requested','matched','approved','sending','unknown','confirmed','rejected','failed','expired')),
 receipt_id TEXT,
 evidence TEXT NOT NULL DEFAULT '',
 reviewed_by UUID REFERENCES users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 expires_at TIMESTAMPTZ NOT NULL DEFAULT (now()+interval '30 minutes'),
 UNIQUE(user_id,kind,request_id),
 CHECK(kind='withdrawal' OR status NOT IN ('approved','sending','unknown','failed'))
);
CREATE UNIQUE INDEX lulu_active_sender ON lulu_orders(receiver_uid,lulu_uid)
 WHERE kind='deposit' AND status IN ('requested','matched');
CREATE INDEX lulu_orders_user_time ON lulu_orders(user_id,created_at DESC,id DESC);
CREATE INDEX lulu_orders_pending ON lulu_orders(kind,status,created_at);

-- Provider receipts are independent of player claims; never accept a client-reported receipt.
CREATE TABLE lulu_receipts (
 id TEXT PRIMARY KEY,
 receiver_uid TEXT NOT NULL,
 sender_uid TEXT NOT NULL,
 amount BIGINT NOT NULL CHECK(amount>0),
 occurred_at TIMESTAMPTZ NOT NULL,
 order_id UUID UNIQUE REFERENCES lulu_orders(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE lulu_orders ADD CONSTRAINT lulu_order_receipt_fk FOREIGN KEY(receipt_id) REFERENCES lulu_receipts(id);
CREATE UNIQUE INDEX lulu_claimed_receipt ON lulu_orders(receipt_id) WHERE receipt_id IS NOT NULL;
CREATE INDEX lulu_receipts_unmatched ON lulu_receipts(receiver_uid,sender_uid,occurred_at) WHERE order_id IS NULL;

CREATE TABLE lulu_collector_state (
 receiver_uid TEXT PRIMARY KEY,
 scanned_at TIMESTAMPTZ,
 last_success_at TIMESTAMPTZ,
 last_error TEXT NOT NULL DEFAULT ''
);
