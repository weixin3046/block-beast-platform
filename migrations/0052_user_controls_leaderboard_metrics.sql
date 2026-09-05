ALTER TABLE users ADD COLUMN chat_muted BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE leaderboard_entries ADD COLUMN total_payout_minor BIGINT;
ALTER TABLE leaderboard_entries ADD COLUMN available_minor BIGINT;
-- 历史冻结榜单无法还原当时余额，保持NULL，不用当前余额伪装历史余额。
