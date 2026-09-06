# 实际金额接口协议

本协议覆盖前台和后台 HTTP 金额接口，以及 Socket 对外事件。数据库、账本、内部事件仍使用最小单位整数，前端不再乘除币种精度。

请求金额支持 JSON 数字或十进制字符串：`100`、`1.5`、`"1.5"`。返回金额统一为十进制字符串，如 `"100.000"`、`"1.500"`。响应不再同时返回 `*_minor`。这属于不兼容接口升级，前后端需要同步发布；旧请求传 `stake_minor` 等字段会返回400，不能把旧字段改成小数继续发送。

例如投注100宝石：

```json
{
  "client_request_id": "每笔投注唯一，重试复用",
  "round_id": "当前可投注期的UUID",
  "account_id": 100009,
  "currency": "POINTS",
  "stake": 100,
  "selection": {"pick": "odd"},
  "game_room_id": "94000000-0000-4000-8000-000000000001",
  "play_mode": "road"
}
```

投注1.5宝石只需改成 `"stake":1.5`。`account_id` 必须是本人公开用户ID，不是邀请码；期号、房间必须先查询实际可用值。赔率、限额、余额和封盘时间仍由后端校验。

| 接口 | 请求金额字段 | 对应精度 |
| --- | --- | --- |
| POST /v1/bets | stake | currency |
| POST /v1/withdrawals | amount | currency，平台精度 |
| POST /v1/point-withdrawals | amount | POINTS |
| POST /v1/admin/credits、/v1/admin/wallet-adjustments | amount | currency |
| POST /v1/admin/agents/{agentID}/commissions | amount | currency |
| POST /v1/chat/rooms/{roomID}/red-packets | total | currency |
| POST /v1/stamina/consume | amount | STAMINA |
| PUT /v1/admin/tasks/bet-configs | items[].threshold、items[].reward | accumulation_currency、reward_currency，分别转换 |
| PUT /v1/admin/spins | items[].cost、items[].prizes[].amount | cost_currency、prizes[].currency，分别转换 |
| POST /v1/admin/spins；PUT /v1/admin/spins/{spinID} | cost、prizes[].amount | cost_currency、prizes[].currency，分别转换 |
| POST /v1/admin/tasks/bet-configs；PUT /v1/admin/tasks/bet-configs/{taskID} | threshold、reward | accumulation_currency、reward_currency，分别转换 |
| PUT /v1/admin/leaderboard-reward-rules | rules[].reward | 每档reward_currency，与排行币种独立 |
| PUT /v1/admin/hash/config | min_stake、guess_max_stake、dodge_max_stake、road_max_stake | 每行currency |
| POST /v1/admin/virtual-accounts | initial_balances中的值 | 映射键中的币种 |
| PUT /v1/admin/virtual-accounts/{userID}/automation | stake | currency |

任务领取、转盘抽奖、排行榜发奖不接受玩家自行指定奖励数量。玩家提交任务/转盘标识和必要幂等键，服务器读取已保存的配置计算到账金额。

任务配置示例：`{"items":[{"id":"","accumulation_currency":"USDT","threshold":100,"reward_currency":"POINTS","reward":1.5,"enabled":true}],"second_password":"后台全局二级操作密码"}`。表示累计100 USDT有效投注后，可手动领取1.5宝石；币种互不强绑定。

转盘配置中的 `cost:1.5` 表示消耗1.5个所选币种；奖项 `amount:100` 表示奖励100个奖项币种。奖项名称仅展示，不决定实际奖励。权重仍为正整数，所有权重不要求合计100。

金额响应主要字段：投注 `stake/payout/balance_after_bet/balance_after_settlement/balance_after_refund`；钱包 `available/frozen`；流水 `amount/balance_after/available_delta/frozen_delta/available_after/frozen_after`；任务 `threshold/progress/reward`；转盘 `cost/reward/cost_balance/reward_balance`；排行榜 `effective_stake/total_bet/total_payout/net_win/available`，奖励对象内为 `amount`。不存在的历史快照继续省略或为空，不用当前余额冒充历史值。

精度以 `currencies` 表为准：USDT默认6位，POINTS/JADE/ORIGIN_STONE默认3位，四种体力默认0位。超精度、负数、零金额、整数溢出、科学计数法、布尔值和重复JSON字段被拒绝，不四舍五入。大金额建议提交字符串，避免浏览器在发送前损失精度。

赔率的 multiplier/divisor、权重、名次、次数、期号、ID和版本仍为原有整数。链上签名回调属于机器协议，不修改签名原文或链上单位。Socket轮次结算事件不再暴露原先跨币种相加的payout总额；逐币种派奖请读取投注记录。

本次接口转换无需重写钱包、投注、账本或历史金额，也不需要金额数据迁移。此前修正默认转盘奖项的0053迁移仍需按部署迁移序列执行。
