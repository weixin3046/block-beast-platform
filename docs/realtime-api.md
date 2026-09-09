# WebSocket v1 前端接口手册

金额字段已统一为实际币种金额字符串，详见[金额协议](./amount-contract.md)。API和Realtime进程需同步升级。

本文档描述当前后端已经实现的 WebSocket v1 协议。玩家端和管理后台可以按本文档直接完成连接、订阅、聊天、事件消费、断线恢复和 HTTP 状态对账。

0068 起聊天 `message.sender.is_staff` 为后端保存的发送时身份快照：admin/operator 为 true，可显示“系统管理员”；普通玩家及迁移前历史消息默认 false。HTTP 历史、发送响应、chat_message_sent 与 chat.message.created 均使用该字段，客户端不能指定它。后续修改角色不改变历史标识；没有 sender 的系统消息应单独处理。

> WebSocket 不属于 OpenAPI 的请求/响应模型，因此不会显示在 Swagger UI 中。HTTP 接口仍以 `docs/openapi.yaml` 为准。

本服务使用浏览器原生 WebSocket JSON 协议，不是 Socket.IO；前端不要引入 `socket.io-client`。

## 1. 服务地址

| 环境 | WebSocket 地址 | 健康检查 |
| --- | --- | --- |
| 本地开发 | `ws://localhost:8081/v1/ws` | `http://localhost:8081/healthz` |
| 当前服务器 | `ws://58.87.64.208/v1/ws` | 由反向代理暴露的 `/healthz` |
| 配置 HTTPS 后 | `wss://<实时域名>/v1/ws` | `https://<实时域名>/healthz` |

生产页面使用 HTTPS 时必须连接 `wss://`，否则浏览器会阻止混合内容。WebSocket 网关是独立的 `realtime` 进程，不是 API 进程。

## 2. 认证与握手

连接必须携带仍在有效期内且会话未撤销的 Access Token。0067 起同账号新登录会使旧会话失效；握手、每条命令及每秒定期检查均校验会话。已失效连接以 1008、“登录已失效，请重新登录”关闭，禁止使用旧令牌无限重连。刷新保留会话，访问令牌到期后需用新令牌重连。断线期间消息仍通过 HTTP 对账。浏览器不能为 WebSocket 握手设置自定义 `Authorization` 请求头，推荐通过子协议传递：

```ts
const socket = new WebSocket(
  "ws://58.87.64.208/v1/ws",
  [`bearer.${accessToken}`],
);
```

网关同时支持以下三种认证方式，优先级从高到低：

1. `Authorization: Bearer <access-token>`，适合非浏览器客户端。
2. `Sec-WebSocket-Protocol: bearer.<access-token>`，推荐浏览器使用。
3. 查询参数 `?access_token=<access-token>`，仅作为兼容方式；生产环境不推荐，因为 URL 可能进入访问日志。

认证失败时，WebSocket 升级不会成功，服务器返回 HTTP `401`，响应正文为 `missing or invalid access token`。来源域名不在 `REALTIME_ALLOWED_ORIGINS` 中时，握手也会被拒绝。

Token 只在建立连接时校验。客户端刷新 Access Token 后，应在适当时机用新 Token 重新连接；不要把 Token 写入日志、本地错误上报或埋点。

连接成功后，服务端首先发送 `hello`：

```json
{
  "v": 1,
  "type": "hello",
  "topics": ["game"],
  "occurred_at": "2026-08-29T10:00:00Z"
}
```

新连接默认订阅 `game`。收到 `hello` 后才应认为协议握手完成，并恢复业务订阅。

## 3. 通用消息格式

服务端只发送 UTF-8 JSON 文本消息：

```ts
type RealtimeSubject =
  | "game.bet.placed"
  | "game.bet.cancelled"
  | "game.round.closed"
  | "game.round.settling"
  | "game.round.settled"
  | "game.round.cancelled"
  | "chain.deposit.credited"
  | "wallet.withdrawal.requested"
  | "chat.message.created"
  | "chat.red_packet.created"
  | "chat.red_packet.claimed"
  | "chat.red_packet.refunded";

interface RealtimeMessage<T = unknown> {
  v: 1;
  type:
    | "hello"
    | "subscribed"
    | "pong"
    | "event"
    | "chat_message_sent"
    | "error";
  request_id?: string;
  subject?: RealtimeSubject | string;
  topics?: string[];
  payload?: T;
  error?: string;
  occurred_at: string; // UTC RFC 3339
}
```

字段说明：

| 字段 | 说明 |
| --- | --- |
| `v` | 协议版本，当前固定为 `1`。 |
| `type` | 消息类别。 |
| `request_id` | 对客户端命令的关联确认；事件推送通常没有该字段。 |
| `subject` | `type=event` 时的事件主题。 |
| `topics` | 当前连接的完整订阅列表。 |
| `payload` | 事件或命令结果。结构由 `type`/`subject` 决定。 |
| `error` | `type=error` 时的错误文本。 |
| `occurred_at` | 网关生成该消息的时间，不等同于业务记录的创建或结算时间。 |

客户端必须忽略未知字段，并允许未来出现未知 `subject`。收到不认识的协议版本时，应关闭连接并提示升级客户端。

## 4. 客户端命令

客户端命令统一包含：

```ts
interface ClientCommand {
  v: 1;
  type: "subscribe" | "unsubscribe" | "ping" | "chat.send";
  request_id?: string;
  topics?: string[];
  room_id?: string;
  body?: string;
  image_upload_id?: string;
}
```

### 4.1 订阅

当前允许的 topic：

| Topic | 含义 |
| --- | --- |
| `game` | 全部 `game.*` 事件；新连接默认订阅。 |
| `round:{round_id}` | 只接收 payload 中 `round_id` 等于指定 UUID 的 `game.*` 事件。 |
| `chat` | 全局房间和游戏房间的 `chat.*` 广播事件。 |

订阅一个或多个 topic：

```json
{
  "v": 1,
  "type": "subscribe",
  "topics": ["chat", "round:f39ac19d-20a0-42d7-a876-87aa3618635e"],
  "request_id": "sub-001"
}
```

取消默认的全局游戏订阅，只保留指定轮次：

```json
{
  "v": 1,
  "type": "unsubscribe",
  "topics": ["game"],
  "request_id": "unsub-001"
}
```

成功响应始终返回当前完整订阅列表，而不是本次增删的差量：

```json
{
  "v": 1,
  "type": "subscribed",
  "request_id": "sub-001",
  "topics": ["chat", "round:f39ac19d-20a0-42d7-a876-87aa3618635e"],
  "occurred_at": "2026-08-29T10:00:01Z"
}
```

订阅操作是幂等的。重复订阅或取消不存在的 topic 不会报错。

如果连接同时订阅了 `game` 和相应的 `round:{round_id}`，同一个游戏事件只会发送一次。

### 4.2 应用层心跳

```json
{"v":1,"type":"ping","request_id":"ping-001"}
```

响应：

```json
{
  "v": 1,
  "type": "pong",
  "request_id": "ping-001",
  "occurred_at": "2026-08-29T10:00:05Z"
}
```

网关自身每 25 秒还会发送 WebSocket 协议层 Ping，浏览器会自动处理，不会触发 JavaScript `message` 事件。应用层 `ping` 主要用于业务侧测量延迟或主动检查连接。

### 4.3 发送聊天消息

```json
{
  "v": 1,
  "type": "chat.send",
  "room_id": "f39ac19d-20a0-42d7-a876-87aa3618635e",
  "body": "我要上分",
  "request_id": "chat-001"
}
```

约束：

- `room_id` 必须是有效 UUID，并且当前用户有权访问房间。
- `body` 去除首尾空白后最多 2000 个 Unicode 字符；未附 image_upload_id 时必须至少有 1 个字符。
- `request_id` 必填，最长 128 个字符，在同一发送者和房间内用于幂等。
- 网络超时后重发同一条消息时必须复用原 `request_id`。

成功响应：

```json
{
  "v": 1,
  "type": "chat_message_sent",
  "request_id": "chat-001",
  "payload": {
    "message": {
      "id": "8ab4bb89-e89f-4a52-b71d-36741de6528f",
      "room_id": "f39ac19d-20a0-42d7-a876-87aa3618635e",
      "sender": {
        "user_id": 100009,
        "display_name": "玩家一号",
        "avatar_url": "/v1/avatars/100009?v=8f11a3b7",
        "is_staff": false
      },
      "body": "我要上分",
      "status": "visible",
      "client_request_id": "chat-001",
      "created_at": "2026-08-29T10:00:10Z"
    },
    "created": true
  },
  "occurred_at": "2026-08-29T10:00:10Z"
}
```

同一请求重复发送时，返回第一次创建的消息，并且 `created=false`。

当前连接可能先后收到 `chat_message_sent` 和 `chat.message.created`。两者表示同一条消息，聊天列表必须按 `message.id` 去重。

## 5. 事件投递规则

事件统一使用：

```json
{
  "v": 1,
  "type": "event",
  "subject": "game.round.settled",
  "payload": {},
  "occurred_at": "2026-08-29T10:01:00Z"
}
```

投递分为三类：

1. 所有 `game.*` 事件公开广播给订阅 `game` 的连接，并同时匹配 `round:{round_id}`。
2. 全局/游戏聊天室的 `chat.*` 事件发送给订阅 `chat` 的连接。
3. 客服/私聊和带 `user_id` 的资金事件按 Access Token 中的用户 ID 定向发送，不需要订阅 topic。

WebSocket 事件是状态变化通知，不是可回放日志：

- 断线期间的消息不会补发。
- 消息没有连续序号，前端不能用它恢复完整状态。
- 网络、Outbox 重试或重新发布可能产生重复消息。
- 重连后必须通过 HTTP 重新查询当前轮次、投注、钱包、提现和聊天历史。

## 6. 游戏事件

HTTP与Socket所有金额字符串去掉小数末尾多余的零，例如 `"1.500"` 显示为 `"1.5"`，`"0.000"` 为 `"0"`；下面旧示例的补零金额以此规则为准。此规则不改变币种精度和赔率字段。

### 6.1 `game.bet.placed`

触发：投注已创建，余额扣减、账本和 Outbox 已在同一事务中提交。

```json
{
  "v": 1,
  "type": "event",
  "subject": "game.bet.placed",
  "payload": {
    "bet": {
      "bet_id": "5a69a884-d231-4109-b306-8c98fbe28456",
      "player": {
        "user_id": 100009,
        "display_name": "玩家一号",
        "avatar_url": "/v1/avatars/100009",
        "is_virtual": false
      },
      "round_id": "f39ac19d-20a0-42d7-a876-87aa3618635e",
      "game_type": "hash_9",
      "game_name": "9区块",
      "round_sequence": 85767406,
      "currency": "POINTS",
      "game_room_id": "94000000-0000-4000-8000-000000000001",
      "game_room_code": "hash_rate_1940",
      "game_room_name": "高额返水 1.94 倍",
      "play_mode": "road",
      "selection": {"pick": "odd"},
      "stake": "0.100",
      "payout_multiplier": 1940,
      "payout_divisor": 1000,
      "payout_rate": "1.94",
      "status": "accepted",
      "payout": "0.000",
      "placed_at": "2026-08-29T10:00:20Z"
    }
  },
  "occurred_at": "2026-08-29T10:00:20Z"
}
```

这是广播事件，`payload.bet` 与 `GET /v1/bets/public-feed` 的单条记录结构一致。0064起，同条件追加投注保持同一 `bet_id`，`bet.stake` 为合计金额；前端必须按 `bet_id` 插入或更新，不能仅忽略重复ID。`bet.placement_count` 是递增版本，忽略次数小于或等于当前行的旧投注事件，且不得以迟到的投注事件覆盖已取消/已结算状态；断线后重新拉取接口。`bet.last_placed_at` 为最近追加时间，`placed_at` 保留首单时间。

0064新事件在payload内另有 `placement_id`（本次成功下单UUID）和 `added_stake`（实际币种金额字符串，例如 `"10.000"`）；展示最新投注动态时用added_stake，显示整单时用bet.stake。旧事件可能没有这两个字段。事件不包含余额、登录账号或内部用户 UUID。

### 6.1.1 `game.bet.cancelled`

触发：玩家在封盘前取消投注，退款、账本和 Outbox 已在同一事务中提交。

```json
{
  "v": 1,
  "type": "event",
  "subject": "game.bet.cancelled",
  "payload": {
    "bet": {
      "bet_id": "5a69a884-d231-4109-b306-8c98fbe28456",
      "player": {
        "user_id": 100009,
        "display_name": "玩家一号",
        "avatar_url": "/v1/avatars/100009",
        "is_virtual": false
      },
      "round_id": "f39ac19d-20a0-42d7-a876-87aa3618635e",
      "game_type": "hash_9",
      "game_name": "9区块",
      "round_sequence": 85767406,
      "currency": "POINTS",
      "game_room_code": "hash_rate_1940",
      "game_room_name": "高额返水 1.94 倍",
      "play_mode": "road",
      "selection": {"pick": "odd"},
      "stake": "0.100",
      "payout_multiplier": 1940,
      "payout_divisor": 1000,
      "payout_rate": "1.94",
      "status": "cancelled",
      "payout": "0.000",
      "placed_at": "2026-08-29T10:00:20Z",
      "settled_at": "2026-08-29T10:00:21Z"
    }
  },
  "occurred_at": "2026-08-29T10:00:21Z"
}
```

前端按 `payload.bet.bet_id` 替换公开投注列表中的原记录。广播载荷不包含退款后余额；发起取消的玩家从 HTTP 取消响应读取 `balance_after_refund`。

### 6.2 `game.round.closed`

触发：轮次到达封盘时间，不再接受投注。

```json
{
  "v": 1,
  "type": "event",
  "subject": "game.round.closed",
  "payload": {
    "round_id": "f39ac19d-20a0-42d7-a876-87aa3618635e"
  },
  "occurred_at": "2026-08-29T10:00:30Z"
}
```

收到后应立即禁用该轮次的投注按钮。最终状态仍以 HTTP 查询为准。

### 6.3 `game.round.settling`

触发：Worker 已取得该轮次结算权并开始结算。

```json
{
  "v": 1,
  "type": "event",
  "subject": "game.round.settling",
  "payload": {
    "round_id": "f39ac19d-20a0-42d7-a876-87aa3618635e"
  },
  "occurred_at": "2026-08-29T10:00:33Z"
}
```

### 6.4 `game.round.settled`

个人结算推送 `game.bet.settled`：每位玩家每个 round_id 只生成一条，包含本期正常结算的全部 won/lost 主单，跨房间和币种也合在这一条。定向发给本人在线连接，无需订阅。前端按 account_id+round_id 去重，再按 bets 内 bet_id 更新订单，重复事件覆盖而非累加。取消和退款沿用既有事件，不计入本通知；断线后通过 HTTP 本人投注列表对账，不自动重放。不同玩法即使显示期号相同，round_id 不同仍分别通知。

示例 payload：

```json
{"account_id":100006,"round_id":"轮次UUID","round_sequence":86036031,"game_type":"hash_9","settled_at":"2026-09-07T11:17:46Z","bets":[{"bet_id":"订单UUID","game_room_id":"房间UUID","play_mode":"road","selection":{"pick":"big"},"currency":"POINTS","status":"lost","stake":"10","payout":"0","net_win":"-10","placement_count":1,"is_simulated":false}],"totals":[{"currency":"POINTS","stake":"10","payout":"0","net_win":"-10","bet_count":1}]}
```

金额为真实数量字符串，net_win=派奖−投入，不含返水等额外奖励。bets 为单订单结果；totals 按币种独立汇总本期正常结算投入、派奖、净收益及主单数，绝不跨币种混加。虚拟投注也推送，is_simulated=true 不代表钱包变动。不得将本事件派奖再次累加到钱包；余额以钱包事件或查询为准。

部署必须先更新全部 Realtime，再更新 Worker，或停服整体更新。旧网关会将 game.* 广播，不允许新 Worker 与旧 Realtime 混跑。本功能无需迁移，不补发上线前已结算订单。

触发：轮次和所有接受中的投注已在同一事务中完成结算。

```json
{
  "v": 1,
  "type": "event",
  "subject": "game.round.settled",
  "payload": {
    "round_id": "f39ac19d-20a0-42d7-a876-87aa3618635e",
    "outcome": ["7", "big", "odd"],
    "won_bet_count": 12,
    "lost_bet_count": 18,
    "settled_at": "2026-08-29T10:00:34Z"
  },
  "occurred_at": "2026-08-29T10:00:34Z"
}
```

轮次结算事件不再包含跨币种总派奖。玩家收到后查询本人投注和钱包，按币种读取payout。

### 6.5 `game.round.cancelled`

触发：后台取消开放或已封盘轮次，接受中的投注已退款。

```json
{
  "v": 1,
  "type": "event",
  "subject": "game.round.cancelled",
  "payload": {
    "round_id": "f39ac19d-20a0-42d7-a876-87aa3618635e",
    "refunded_bet_count": 20
  },
  "occurred_at": "2026-08-29T10:00:35Z"
}
```

## 7. 钱包与链上事件

资金事件按 `payload.user_id` 定向投递。客户端不能订阅其他用户的资金事件。

### 7.1 `chain.deposit.credited`

触发：充值已确认并计入钱包可用余额。

```json
{
  "v": 1,
  "type": "event",
  "subject": "chain.deposit.credited",
  "payload": {
    "deposit_id": "b3b790b0-e8df-4c63-a3d7-d043864fb469",
    "user_id": "0ecdd037-e5a1-4831-ad20-ac30f05b6098",
    "token_code": "USDT",
    "tx_hash": "0x..."
  },
  "occurred_at": "2026-08-29T10:05:00Z"
}
```

payload 不含充值金额和最新余额。收到后重新查询充值记录和对应钱包。

### 7.2 `wallet.withdrawal.requested`

触发：提现申请已创建，金额已从可用余额转入冻结余额。

```json
{
  "v": 1,
  "type": "event",
  "subject": "wallet.withdrawal.requested",
  "payload": {
    "withdrawal_id": "adfefea0-d166-49a4-8f4b-12fc2ca7ec2b",
    "user_id": "0ecdd037-e5a1-4831-ad20-ac30f05b6098",
    "currency": "USDT"
  },
  "occurred_at": "2026-08-29T10:06:00Z"
}
```

### 7.3 当前不保证实时送达的提现状态

以下内部事件虽然已经定义或发布，但当前 payload 没有 `user_id`，实时网关不会发送给前端：

- `chain.withdrawal.approved`
- `chain.withdrawal.sent`

提现 `confirmed` 和 `failed` 终态当前也没有对应的 Outbox/WebSocket 事件。前端在提交提现后必须通过 HTTP 查询提现详情，直到进入终态。不要只依赖 Socket 更新提现状态。

`wallet.ledger.committed` 自数据库迁移 0045 起由统一账本触发器写入 outbox，再经 Worker 和实时网关发送给该钱包所属用户。投注扣款、结算入账、充值、奖励和提现产生新流水时均会发送；历史搬迁不补发旧事件。

payload 字段：`user_id`（内部路由 ID，不作玩家公开展示）、`currency`、`ledger_id`、`business_id`、`business_type`、`available_delta`、`frozen_delta`、`available_after`、`frozen_after`。金额均为实际金额十进制字符串；前端收到后建议重新查询全部钱包余额接口，直接使用其中的 `available/frozen` 字符串。

这是变更通知，不是另一次发奖指令。与原投注/充值等业务事件可能同时到达，按事件 ID 去重，并以 HTTP 查询为最终状态；不能对多个事件重复累加余额，重连后也必须重新查询。

## 8. 聊天与红包事件

### 8.1 `chat.message.created`

```json
{
  "v": 1,
  "type": "event",
  "subject": "chat.message.created",
  "payload": {
    "room_id": "f39ac19d-20a0-42d7-a876-87aa3618635e",
    "message": {
      "id": "8ab4bb89-e89f-4a52-b71d-36741de6528f",
      "room_id": "f39ac19d-20a0-42d7-a876-87aa3618635e",
      "sender": {
        "user_id": 100009,
        "display_name": "玩家一号",
        "avatar_url": "/v1/avatars/100009?v=8f11a3b7",
        "is_staff": false
      },
      "body": "你好",
      "status": "visible",
      "client_request_id": "chat-001",
      "created_at": "2026-08-29T10:10:00Z"
    },
    "broadcast": true
  },
  "occurred_at": "2026-08-29T10:10:00Z"
}
```

- 全局/游戏房间：`broadcast=true`，需要订阅 `chat`。
- 客服房间：`broadcast=false`，按房间成员及消息创建时拥有 `admin`、`operator` 角色的用户定向投递至在线连接；后台不需要加入房间。成员与角色重叠时不会重复投递。
- 私聊房间仍只向房间成员投递，不因后台角色扩大接收范围。以上定向事件无需订阅 `chat`，内部定向用户 ID 不会出现在前端载荷中；其他玩家即使订阅 `chat` 也不会收到客服消息。断线重连后须通过 HTTP 查询历史消息对账。
- 前端按 `message.id` 去重。
- `message.sender` 与 HTTP 历史消息完全一致：`user_id` 是公开数字 ID，名称读取 `display_name`，头像读取 `avatar_url`。空头像由前端显示默认图；系统消息可以没有 `sender`。

### 8.2 `chat.red_packet.created`

```json
{
  "v": 1,
  "type": "event",
  "subject": "chat.red_packet.created",
  "payload": {
    "room_id": "f39ac19d-20a0-42d7-a876-87aa3618635e",
    "red_packet": {
      "id": "92a42a95-f3c3-4468-a014-ae59e43ce3bb",
      "room_id": "f39ac19d-20a0-42d7-a876-87aa3618635e",
      "sender_user_id": "0ecdd037-e5a1-4831-ad20-ac30f05b6098",
      "client_request_id": "packet-001",
      "currency": "USDT",
      "greeting": "恭喜发财",
      "total": "10.000000",
      "remaining": "10.000000",
      "packet_count": 10,
      "claimed_count": 0,
      "status": "active",
      "expires_at": "2026-08-30T10:10:00Z",
      "created_at": "2026-08-29T10:10:00Z"
    },
    "broadcast": true
  },
  "occurred_at": "2026-08-29T10:10:00Z"
}
```

### 8.3 `chat.red_packet.claimed`

```json
{
  "v": 1,
  "type": "event",
  "subject": "chat.red_packet.claimed",
  "payload": {
    "room_id": "f39ac19d-20a0-42d7-a876-87aa3618635e",
    "red_packet_id": "92a42a95-f3c3-4468-a014-ae59e43ce3bb",
    "claim": {
      "id": "2336636b-a4d5-4600-a4b3-c7e7ebcc4acf",
      "red_packet_id": "92a42a95-f3c3-4468-a014-ae59e43ce3bb",
      "user_id": "4bf2af91-13c5-4774-a1fd-2b0767b4e82f",
      "currency": "USDT",
      "amount": "0.860000",
      "claimed_at": "2026-08-29T10:11:00Z"
    },
    "broadcast": true
  },
  "occurred_at": "2026-08-29T10:11:00Z"
}
```

### 8.4 `chat.red_packet.refunded`

```json
{
  "v": 1,
  "type": "event",
  "subject": "chat.red_packet.refunded",
  "payload": {
    "room_id": "f39ac19d-20a0-42d7-a876-87aa3618635e",
    "red_packet_id": "92a42a95-f3c3-4468-a014-ae59e43ce3bb",
    "refund": "3.200",
    "currency": "POINTS",
    "broadcast": true
  },
  "occurred_at": "2026-08-30T10:10:01Z"
}
```

红包的 HTTP 创建、领取和查询接口以 OpenAPI 为准；Socket 只负责房间内状态通知。

## 9. 错误消息

错误统一使用：

```json
{
  "v": 1,
  "type": "error",
  "request_id": "chat-001",
  "error": "无权访问该聊天室",
  "occurred_at": "2026-08-29T10:12:00Z"
}
```

当前可能出现的错误：

| `error` | 场景 | 建议处理 |
| --- | --- | --- |
| `实时消息指令格式不正确` | JSON 无效、版本/类型错误、topic 无效、缺少必要字段。 | 记录命令类型并修正客户端；不要原样无限重试。 |
| `请发送文本消息` | 发送了二进制帧。 | 改为 JSON 文本帧。 |
| `消息内容须为 1 至 2000 个字符` | 聊天内容为空或过长。 | 前端校验后提示用户。 |
| `请填写请求唯一标识 client_request_id` | 聊天幂等键为空或超过 128 字符。 | 生成并复用合法请求 ID。 |
| `无权访问该聊天室` | 当前用户无房间权限。 | 停止重试并刷新房间列表。 |
| `聊天室不存在` | 房间不存在。 | 刷新房间列表。 |
| `聊天服务暂不可用` | 网关未装配聊天服务。 | 退避后重试或降级提示。 |
| `发送聊天消息失败` | 服务端内部或依赖异常。 | 保留相同 `request_id`，退避后重试。 |

`error` 为展示用中文，`type/subject/request_id` 等协议字段不变，不要按中文文本比较业务类型。未知内部错误使用中文兜底，不直接透传数据库和服务商异常。网关主动关闭连接的说明为中文，关闭码不变；WebSocket 库自身产生的底层协议关闭错误不属于业务提示。

订阅、取消订阅和应用层 ping 的 `request_id` 不是强制字段，但推荐始终提供，便于关联请求与响应。`chat.send` 的 `request_id` 强制必填。

## 10. 连接关闭与重连

网关使用的主要关闭码：

| Close code | 原因 | 客户端行为 |
| --- | --- | --- |
| `1000` | 正常关闭。 | 用户主动退出时不重连；意外收到时可重新认证后连接。 |
| `1001` | 服务关闭、写入失败或协议层 Ping 失败。 | 指数退避重连。 |
| `1008` | 客户端消费过慢，128 条发送队列已满。 | 先修复消息消费阻塞，再退避重连并用 HTTP 对账。 |

推荐重连延迟：`1s → 2s → 4s → 8s → 15s → 30s`，每次增加 0～30% 随机抖动，连接稳定 60 秒后重置重试次数。

重连流程：

1. 确认网络恢复。
2. 获取有效 Access Token，必要时先调用刷新令牌接口。
3. 建立新连接并等待 `hello`。
4. 恢复 `chat` 和所有 `round:{id}` 订阅；如果不需要全部游戏事件，再取消默认 `game`。
5. 通过 HTTP 重新查询页面当前状态，覆盖断线前的本地缓存。

## 11. 倒计时与页面状态同步

当前 WebSocket 不会每秒推送倒计时。前端应按以下方式实现：

1. 通过 HTTP 获取轮次的 `bet_closes_at` 和 `result_at`。
2. 使用服务器 UTC 时间和本地校准后的时钟计算剩余秒数。
3. 收到 `game.round.closed` 后立即将倒计时归零并关闭投注。
4. 收到 `settled`/`cancelled` 后刷新当前轮次和下一轮次。
5. 页面重新显示、设备唤醒或 Socket 重连时再次通过 HTTP 校准，不能只依赖 `setInterval`。

`occurred_at` 是消息经过实时网关的时间，不能替代 `bet_closes_at` 或 `result_at`。

## 12. 可直接使用的 TypeScript 客户端

```ts
type Topic = "game" | "chat" | `round:${string}`;

type RealtimeEnvelope<T = unknown> = {
  v: 1;
  type: "hello" | "subscribed" | "pong" | "event" | "chat_message_sent" | "error";
  request_id?: string;
  subject?: string;
  topics?: Topic[];
  payload?: T;
  error?: string;
  occurred_at: string;
};

type RealtimeOptions = {
  url: string;
  getAccessToken: () => Promise<string>;
  onEvent: (message: RealtimeEnvelope) => void;
  onError?: (error: Error) => void;
};

export class RealtimeClient {
  private socket?: WebSocket;
  private stopped = false;
  private retry = 0;
  private topics = new Set<Topic>(["game"]);

  constructor(private readonly options: RealtimeOptions) {}

  async connect(): Promise<void> {
    this.stopped = false;
    const token = await this.options.getAccessToken();
    const socket = new WebSocket(this.options.url, [`bearer.${token}`]);
    this.socket = socket;

    socket.onopen = () => {
      this.retry = 0;
    };

    socket.onmessage = (event) => {
      try {
        const message = JSON.parse(String(event.data)) as RealtimeEnvelope;
        if (message.v !== 1) throw new Error(`不支持的实时协议版本：${message.v}`);

        if (message.type === "hello") {
          const extraTopics = [...this.topics].filter((topic) => topic !== "game");
          if (!this.topics.has("game")) {
            this.send({v: 1, type: "unsubscribe", topics: ["game"], request_id: crypto.randomUUID()});
          }
          if (extraTopics.length > 0) {
            this.send({v: 1, type: "subscribe", topics: extraTopics, request_id: crypto.randomUUID()});
          }
        }
        this.options.onEvent(message);
      } catch (error) {
        this.options.onError?.(error instanceof Error ? error : new Error(String(error)));
      }
    };

    socket.onerror = () => {
      this.options.onError?.(new Error("实时连接异常"));
    };

    socket.onclose = () => {
      if (!this.stopped) this.scheduleReconnect();
    };
  }

  subscribe(topic: Topic): void {
    this.topics.add(topic);
    if (this.socket?.readyState === WebSocket.OPEN) {
      this.send({v: 1, type: "subscribe", topics: [topic], request_id: crypto.randomUUID()});
    }
  }

  unsubscribe(topic: Topic): void {
    this.topics.delete(topic);
    if (this.socket?.readyState === WebSocket.OPEN) {
      this.send({v: 1, type: "unsubscribe", topics: [topic], request_id: crypto.randomUUID()});
    }
  }

  sendChat(roomID: string, body: string, requestID = crypto.randomUUID()): string {
    this.send({v: 1, type: "chat.send", room_id: roomID, body, request_id: requestID});
    return requestID;
  }

  ping(): void {
    this.send({v: 1, type: "ping", request_id: crypto.randomUUID()});
  }

  close(): void {
    this.stopped = true;
    this.socket?.close(1000, "客户端主动关闭");
  }

  private send(command: object): void {
    if (this.socket?.readyState !== WebSocket.OPEN) {
      throw new Error("实时连接尚未建立");
    }
    this.socket.send(JSON.stringify(command));
  }

  private scheduleReconnect(): void {
    const steps = [1000, 2000, 4000, 8000, 15000, 30000];
    const base = steps[Math.min(this.retry++, steps.length - 1)];
    const delay = base + Math.floor(Math.random() * base * 0.3);
    window.setTimeout(() => void this.connect().catch((error) => {
      this.options.onError?.(error instanceof Error ? error : new Error(String(error)));
      if (!this.stopped) this.scheduleReconnect();
    }), delay);
  }
}
```

使用示例：

```ts
const realtime = new RealtimeClient({
  url: "ws://58.87.64.208/v1/ws",
  getAccessToken: async () => authStore.accessToken,
  onEvent: (message) => {
    if (message.type !== "event") return;

    switch (message.subject) {
      case "game.round.closed":
        roundsStore.markClosed(message.payload);
        break;
      case "game.round.settled":
      case "game.round.cancelled":
        void roundsStore.reload();
        void walletStore.reload();
        break;
      case "chain.deposit.credited":
      case "wallet.withdrawal.requested":
        void walletStore.reload();
        break;
      case "chat.message.created":
        chatStore.upsertByID(message.payload);
        break;
    }
  },
});

await realtime.connect();
realtime.subscribe("chat");
```

## 13. 前端接入检查表

- [ ] 使用 Access Token 建立连接并等待 `hello`。
- [ ] 根据页面恢复 `chat` 和 `round:{id}` 订阅。
- [ ] 所有消息先判断 `v`、`type`，事件再判断 `subject`。
- [ ] 未知事件忽略并记录，不导致连接崩溃。
- [ ] 聊天按 `message.id` 去重，发送重试复用 `request_id`。
- [ ] 游戏、钱包和提现在重连后使用 HTTP 对账。
- [ ] 倒计时根据 HTTP 时间字段本地计算，不等待每秒 Socket 推送。
- [ ] 处理 `1001`、`1008` 关闭码并指数退避重连。
- [ ] HTTPS 页面使用 `wss://`。
- [ ] 不在日志、埋点和 URL 中泄露 Access Token。

## 14. 当前协议边界

当前 v1 尚不提供：

- 事件历史回放和断点续传。
- 每秒倒计时推送。
- Socket 内刷新或替换 Access Token。
- 动态订阅任意钱包或其他用户事件。
- 提现审批、广播、成功、失败的完整实时状态链。
- 全部历史钱包事件的离线回放（新流水已有统一钱包通知，但不是可补拉的离线消息队列）。
- 管理后台专用的全量玩家资金订阅。

需要上述能力时必须先扩展后端协议和事件 payload，再同步更新本文档，前端不得自行假设事件存在。


## 聊天图片消息

客服和公共聊天支持每条消息一张 JPEG、PNG 或 WebP 图片，可附带不超过 2000 字的文字。先使用现有 POST /v1/uploads/authorize，按返回的上传方式写入文件并完成确认，取得 upload.id；上传大小遵循上传接口限制。PDF 不能作为聊天图片。随后通过 WebSocket 发送：

```json
{"v":1,"type":"chat.send","room_id":"房间 UUID","request_id":"客户端生成的唯一标识","body":"可选说明","image_upload_id":"已确认的上传 UUID"}
```

纯图片允许 body 为空；无图片时仍必须有文字。图片必须属于发送者，禁止传外部图片 URL。重复请求复用 request_id，返回原消息，不替换原内容。发送确认、历史列表和 chat.message.created 均包含 image_upload_id、image_url，文字消息省略这两项。

image_url 指向 GET /v1/uploads/{uploadID}/content，需要携带平台 Bearer Token；浏览器使用 fetch 获取 Blob 后通过 URL.createObjectURL 展示并适时 revokeObjectURL，不把 Token 放进 URL。上传者可读取本人文件；其他人只可读取其有权查看房间内的可见图片消息，隐藏或删除消息不再授予附件访问权限。图片同时发布到公共房间后，其他已登录用户可读取。

部署前执行 0076_chat_images.sql，再更新 API、Realtime。后端接口已支持，前端需实现上传按钮和图片渲染。
