-- 共享话术库；保留删除标记和创建幂等键，避免删除后的旧创建请求重放复活话术。
CREATE TABLE customer_phrases (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 100),
    content TEXT NOT NULL CHECK (char_length(content) BETWEEN 1 AND 2000),
    category TEXT NOT NULL DEFAULT '' CHECK (char_length(category) <= 50),
    sort BIGINT NOT NULL DEFAULT 0 CHECK (sort >= 0),
    enabled BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted BOOLEAN NOT NULL DEFAULT false,
    created_by UUID NOT NULL REFERENCES users(id),
    request_id UUID NOT NULL,
    create_input JSONB NOT NULL,
    UNIQUE(created_by, request_id)
);
CREATE INDEX customer_phrases_order_idx ON customer_phrases(sort, id DESC) WHERE NOT deleted;
CREATE INDEX customer_phrases_category_idx ON customer_phrases(category, sort, id DESC) WHERE NOT deleted;
CREATE INDEX customer_phrases_enabled_idx ON customer_phrases(enabled, sort, id DESC) WHERE NOT deleted;
