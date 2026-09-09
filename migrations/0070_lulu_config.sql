-- Single database authority for public Lulu receiving account and channel state.
-- Start disabled; runtime credentials never enter this table.
CREATE TABLE lulu_config (
 singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK(singleton),
 receiver_uid TEXT NOT NULL DEFAULT '' CHECK(receiver_uid='' OR receiver_uid ~ '^[1-9][0-9]{2,19}$'),
 enabled BOOLEAN NOT NULL DEFAULT false,
 version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
 updated_by UUID REFERENCES users(id),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK(NOT enabled OR receiver_uid<>'')
);
INSERT INTO lulu_config(singleton) VALUES(true);
