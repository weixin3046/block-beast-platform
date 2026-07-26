# 前端接口接入

完整机器可读的接口定义在 [openapi.yaml](./openapi.yaml)。可直接导入 Swagger UI、Postman、Apifox，或用 OpenAPI Generator 生成 TypeScript 客户端。

本地 API 地址为 `http://localhost:8080`。所有接口使用 JSON，错误统一为：

```json
{ "error": "原因描述" }
```

## 账号与币种

平台有三种账户币种，注册即自动创建三个零余额钱包：

| 币种 | 用途 | 来源 |
|---|---|---|
| `USDT` | 投注、提现 | 链上充值回调自动入账，或管理员后台充值 |
| `POINTS` | 投注 | 管理员后台充值 |
| `STAMINA` | 参加活动消耗 | 每日签到、投注任务奖励、管理员后台充值 |

## 调用顺序

1. 玩家端调用 `POST /v1/auth/register` 注册账号（或 `POST /v1/auth/login` 登录），并使用 `POST /v1/auth/refresh` 续期；管理后台必须调用 `POST /v1/admin/auth/login`，并使用 `POST /v1/admin/auth/refresh` 续期。两类刷新令牌绑定所属端，不能交叉使用，续期时还会重新检查当前角色。退出时调用 `POST /v1/auth/logout` 撤销刷新令牌。登录返回 `429` 表示该登录名因连续失败被临时锁定，前端应停止自动重试并遵循 `Retry-After`。
2. 调用 `GET /v1/rounds?game_type={code}` 获取仍可下注的轮次。
3. 调用 `POST /v1/bets` 创建投注，`currency` 传 `USDT` 或 `POINTS`。浏览器应为每次用户确认操作生成稳定的 `client_request_id`；网络重试必须复用该值。`account_id` 必须与令牌主体一致（本人），否则返回 403。
4. 使用 `GET /v1/bets/{betID}` 轮询投注状态；当前状态有 `accepted`、`won`、`lost` 与 `refunded`。
5. 使用 `GET /v1/wallets/{accountID}?currency=USDT` 查询单币种余额，或 `GET /v1/wallets/{accountID}/all` 一次拉取全部币种。
6. 每日首次进入时调用 `POST /v1/tasks/checkin` 签到领取体力；`checked_in=false` 表示今日已签过，不要重复提示。
7. 参加活动时调用 `POST /v1/stamina/consume` 扣体力，`activity_id` 由活动方提供；体力不足返回 409。
8. 大厅调用 `GET /v1/announcements` 获取当前时间窗口内启用的公告；该接口无需登录。

管理后台可用 `GET/POST /v1/admin/announcements` 和 `PUT /v1/admin/announcements/{id}` 管理公告。`operator` 与 `admin` 均可管理公告；不可变审计日志 `GET /v1/admin/audit-logs?action={action}&actor_user_id={id}` 仅 `admin` 可查看。

角色管理仅对 `admin` 开放：`GET /v1/admin/roles` 查询 `player`、`operator`、
`admin` 标准角色，`PUT /v1/admin/users/{userID}/roles` 使用
`{"roles":["operator"]}` 替换目标账号的完整角色集合。成功后目标账号所有刷新
会话立即撤销。接口禁止管理员移除自己的 `admin`，也禁止移除系统最后一个
`admin`；这两种情况返回 409。

游戏运营可通过 `GET/POST /v1/admin/game-types` 创建玩法规则，通过
`PUT /v1/admin/game-types/{id}` 修改或启停玩法；`POST /v1/admin/rounds`
创建固定封盘时间的新轮次，轮次序号由系统按玩法自动递增。

## TypeScript 示例

```ts
const api = "http://localhost:8080";

// 1. 注册（已有账号则改为 /v1/auth/login）
const auth = await fetch(`${api}/v1/auth/register`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ login_name: "player-001", password: "开发环境密码" }),
}).then((r) => (r.ok ? r.json() : r.json().then(({ error }) => Promise.reject(new Error(error)))));

let { access_token, refresh_token, user_id } = auth;

// access_token 过期后轮换；旧 refresh_token 随即失效
const refreshed = await fetch(`${api}/v1/auth/refresh`, { // 后台改用 /v1/admin/auth/refresh
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ refresh_token }),
}).then((r) => r.json());
({ access_token, refresh_token } = refreshed);

// 2. 携带令牌投注（currency 可为 USDT 或 POINTS）
const response = await fetch(`${api}/v1/bets`, {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    Authorization: `Bearer ${access_token}`,
  },
  body: JSON.stringify({
    client_request_id: crypto.randomUUID(),
    round_id: roundId,
    account_id: user_id,
    currency: "POINTS",
    selection: { pick: "7" },
    stake_minor: 2500,
  }),
});

if (!response.ok) {
  const { error } = await response.json();
  throw new Error(error);
}
const bet = await response.json();

// 3. 查询全部余额
const balances = await fetch(`${api}/v1/wallets/${user_id}/all`, {
  headers: { Authorization: `Bearer ${access_token}` },
}).then((r) => r.json());

// 4. 每日签到
const checkin = await fetch(`${api}/v1/tasks/checkin`, {
  method: "POST",
  headers: { Authorization: `Bearer ${access_token}` },
}).then((r) => r.json());
```

## 流水查询

- 积分流水：`GET /v1/points/{accountID}/ledger?limit=50&offset=0`
- 体力流水：`GET /v1/stamina/{accountID}/ledger?limit=50&offset=0`
- USDT 充值记录：`GET /v1/deposits`
- USDT 提现记录：`GET /v1/withdrawals`
- 投注与结算记录：`GET /v1/bets?status=won`

流水按时间倒序返回，`amount_minor` 正数为入账、负数为出账；`business_type` 区分来源：`admin_credit`（管理员充值）、`checkin_reward`（签到）、`bet_task_reward`（投注达标奖励）、`activity_consume`（活动消耗）。

## 链上充值

充值网络与币种由 PQPA 应用配置决定，前端不得硬编码 TRON 或其他网络：

1. 调用 `GET /v1/assets` 获取当前启用的 `chain_code`、`token_code`、精度和 `support_withdraw`。
2. 玩家选择资产后，调用 `GET /v1/deposit-addresses?chain_code=POLYGON&token_code=USDT` 查询既有地址。
3. 地址不存在时，调用 `POST /v1/deposit-addresses` 并传入相同的 `chain_code` 和 `token_code` 创建地址。
4. 切换网络或币种时必须清空之前展示的地址，避免跨链误充值。
5. 创建地址响应可能包含 `memo`；存在时必须与地址一起展示和复制。

## USDT 提现

提现网络同样来自 `GET /v1/assets`，只允许选择 `support_withdraw=true` 的资产。调用 `POST /v1/withdrawals` 时必须提交 `chain_code`、`currency`、目标地址、可选的 `destination_memo`、最小单位整数金额和客户端幂等键。服务端执行地址格式、单笔最小/最大金额和 UTC 每日累计限额检查；超出每日限额返回 409。后台审批通过后由 Worker 调用 PQPA 出金；最终状态由 PQPA Webhook 更新，回调丢失时由 Worker 主动对账补偿。

## 管理后台接口

管理员（operator/admin 角色）可调用 `POST /v1/admin/credits` 为用户充值任意币种：

```json
{
  "user_id": "用户ID",
  "currency": "POINTS",
  "amount_minor": 10000,
  "remark": "活动补偿",
  "request_id": "admin-20260723-0001"
}
```

`request_id` 是幂等键，重复请求返回首次结果（`credited=false`），不会重复入账。

用户管理：

- `GET /v1/admin/users?status=active&q=用户名`：查询和筛选用户。
- `PUT /v1/admin/users/{userID}/status`：设置 `active`、`disabled` 或 `bet_banned`。

`disabled` 用户不能登录或继续下注；`bet_banned` 用户可以登录和查看资产，但不能创建新投注。

## 代理返佣

- `GET /v1/agents/me/commissions`：查询本人佣金明细。
- `GET /v1/agents/me/team-summary`：按币种查询直属团队有效投注和已付佣金。
- `GET /v1/admin/commissions?status=paid`：后台查询返佣订单。
- `POST /v1/admin/commissions/{commissionID}/reverse`：撤销返佣并冲正代理余额。
- `POST /v1/admin/agents/{agentID}/commissions`：人工补发积分或 USDT 佣金。

自动返佣仅计算一级直属代理，按已结算且未退款的有效投注额计算。积分投注返到积分钱包，USDT 投注返到 USDT 钱包。
人工补发请求必须提供唯一 `request_id`，重复请求不会重复入账。

## 当前限制

API 通过 `API_ALLOWED_ORIGINS` 配置玩家端和管理后台的跨域白名单。访问令牌默认有效期 15 分钟；客户端应使用可轮换刷新令牌续期，退出时撤销当前刷新令牌。

## 实时连接

浏览器通过子协议连接：`new WebSocket("ws://localhost:8081/v1/ws", ["bearer." + accessToken])`。生产环境必须使用 `wss://`，并通过 `REALTIME_ALLOWED_ORIGINS` 限制前端来源。连接建立后服务端发送版本化握手：

```json
{"v":1,"type":"hello","topics":["game"],"occurred_at":"2026-07-26T12:00:00Z"}
```

连接默认订阅全部 `game.*` 事件。客户端可以取消全局游戏事件并仅订阅指定轮次：

```json
{"v":1,"type":"unsubscribe","topics":["game"],"request_id":"u-1"}
{"v":1,"type":"subscribe","topics":["round:f39ac19d-20a0-42d7-a876-87aa3618635e"],"request_id":"s-1"}
```

服务端以 `type=subscribed` 返回当前完整订阅列表。客户端也可以发送
`{"v":1,"type":"ping","request_id":"p-1"}`，服务端返回对应 `pong`。版本、消息类型或 topic 无效时返回 `type=error`。

事件统一为 `{"v":1,"type":"event","subject":"game.round.settled","payload":{...},"occurred_at":"..."}`。`game.*` 按 `game` 或 `round:{round_id}` 订阅投递；订阅 `chat` 可接收公共房间消息。客服消息、`wallet.*` 和 `chain.*` 无需客户端订阅，只发送给对应玩家。客户端消费过慢导致 128 条发送队列写满时，网关会主动断开连接，客户端应退避重连并重新建立订阅。

## 聊天与客服

- `POST /v1/chat/customer-service`：幂等获取玩家自己的客服房间。
- `GET /v1/chat/rooms`：玩家查询公共房间和自己的客服房间；后台角色可查询全部客服房间。
- `GET /v1/chat/rooms/{roomID}/messages`：查询可访问房间的最近消息。
- `POST /v1/chat/rooms/{roomID}/messages`：发送消息，必须提供稳定且唯一的 `client_request_id`，网络重试复用原值。

消息与 `chat.message.created` outbox 事件在同一个数据库事务中提交。客服房间只有所属玩家和后台角色可读写，公共消息通过 WebSocket 的 `chat` topic 广播。

## 文件上传

1. 调用 `POST /v1/uploads/authorize`，提交 `content_type` 和 `size_bytes`。
2. 使用响应中的 `method=PUT`、`url` 和完整 `headers` 直接上传到对象存储；不得修改签名要求的 `Content-Type`。
3. 调用 `POST /v1/uploads/{uploadID}/confirm`。后端通过已签名 HEAD 请求核对实际大小和类型，匹配后状态变为 `confirmed`。
4. `GET /v1/uploads/{uploadID}` 仅允许所有者查询状态。

允许 JPEG、PNG、WebP 和 PDF，默认最大 10 MiB、签名有效期 10 分钟。前端不能把 `pending` 对象当成可用文件；授权过期或对象元数据不匹配时确认接口返回 409。
Worker 会把超时未确认记录批量标记为 `expired`。对象存储桶还应配置生命周期规则，自动清理未被业务引用的过期对象。

## 每日排行榜

`GET /v1/leaderboards/daily?date=2026-07-26&currency=USDT&metric=profit&limit=50`
返回 UTC 自然日排行榜。`currency` 必须是 `USDT` 或 `POINTS`；`metric` 支持：

- `turnover`：已结算且未退款投注的有效投注额，默认值。
- `profit`：派奖减去有效投注额，与钱包当前余额无关。
- `wins`：中奖投注数量。

Worker 默认每分钟重建当日快照，因此结算完成到榜单变化可能存在短暂延迟。退款投注不进入榜单。

## 聊天室红包

- `POST /v1/chat/rooms/{roomID}/red-packets`：使用 `USDT` 或 `POINTS` 创建红包。请求包含 `client_request_id`、`currency`、`total_minor`、`packet_count` 和可选 `greeting`。
- `GET /v1/red-packets/{packetID}`：房间成员查询红包剩余份数和状态。
- `POST /v1/red-packets/{packetID}/claim`：领取红包；同一用户重复请求返回第一次领取记录，不会重复入账。

创建时总金额立即从发送者可用余额扣除并进入红包托管。金额必须至少等于份数，最多 100 份；发送者不能领取自己的红包。每次领取至少一个最小货币单位，最后一份获得全部剩余金额。默认 24 小时过期，Worker 将未领取余额退回发送者原币种钱包。创建、领取、退款均在同一事务中更新钱包、写不可变账本和 outbox 事件。

游戏开奖结果的数据源由后端玩法规则决定：K 线玩法使用 OKX `candle1m` WebSocket 实时数据并由 REST 补偿，TRON 哈希玩法使用 QuickNode JSON-RPC。前端不得自行计算或替代开奖结果。
