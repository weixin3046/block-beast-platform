-- Configuration only: external IP/device risk classification is not enabled.
CREATE TABLE login_risk_whitelist (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id UUID REFERENCES users(id),
 ip INET,
 remark TEXT NOT NULL DEFAULT '' CHECK(char_length(remark)<=200),
 updated_by UUID NOT NULL REFERENCES users(id),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK((user_id IS NULL) <> (ip IS NULL)),
 CHECK(ip IS NULL OR masklen(ip)=CASE family(ip) WHEN 4 THEN 32 ELSE 128 END),
 UNIQUE(user_id), UNIQUE(ip)
);
