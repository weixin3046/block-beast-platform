-- Correct only recognizable default prizes. Never rewrite wallets or past rewards.
UPDATE spin_prizes p
SET reward_minor = (v.amount * power(10::numeric,c.decimals))::bigint
FROM spin_configs s, currencies c,
 (VALUES ('points-8','8 宝石','POINTS',8::bigint),
         ('points-28','28 宝石','POINTS',28::bigint),
         ('points-68','68 宝石','POINTS',68::bigint),
         ('usdt-1','1 USDT','USDT',1::bigint),
         ('usdt-6','6 USDT','USDT',6::bigint),
         ('usdt-18','18 USDT','USDT',18::bigint)) v(code,label,currency,amount)
WHERE p.spin_id=s.id AND s.code='lucky-spin'
 AND p.code=v.code AND p.label=v.label AND p.reward_currency=v.currency
 AND p.reward_minor=v.amount AND c.code=v.currency;
