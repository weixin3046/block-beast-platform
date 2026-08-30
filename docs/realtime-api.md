# WebSocket v1 前端接口手册

本文档描述当前后端已经实现的 WebSocket v1 协议。玩家端和管理后台可以按本文档直接完成连接、订阅、聊天、事件消费、断线恢复和 HTTP 状态对账。

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

连接必须携带仍在有效期内的 Access Token。浏览器不能为 WebSocket 握手设置自定义 `Authorization` 请求头，推荐通过子协议传递：

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
- `body` 去除首尾空白后必须为 1～2000 个 Unicode 字符。
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
      "sender_user_id": "0ecdd037-e5a1-4831-ad20-ac30f05b6098",
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

### 6.1 `game.bet.placed`

触发：投注已创建，余额扣减、账本和 Outbox 已在同一事务中提交。

```json
{
  "v": 1,
  "type": "event",
  "subject": "game.bet.placed",
  "payload": {
    "bet_id": "5a69a884-d231-4109-b306-8c98fbe28456",
    "round_id": "f39ac19d-20a0-42d7-a876-87aa3618635e",
    "user_id": "0ecdd037-e5a1-4831-ad20-ac30f05b6098"
  },
  "occurred_at": "2026-08-29T10:00:20Z"
}
```

这是广播事件，当前 payload 只包含标识字段，不包含币种、金额或选择。需要完整投注数据时，使用有权限的 HTTP 投注查询接口。

### 6.1.1 `game.bet.cancelled`

触发：玩家在封盘前取消投注，退款、账本和 Outbox 已在同一事务中提交。

```json
{
  "v": 1,
  "type": "event",
  "subject": "game.bet.cancelled",
  "payload": {
    "bet_id": "5a69a884-d231-4109-b306-8c98fbe28456",
    "round_id": "f39ac19d-20a0-42d7-a876-87aa3618635e",
    "user_id": "0ecdd037-e5a1-4831-ad20-ac30f05b6098"
  },
  "occurred_at": "2026-08-29T10:00:21Z"
}
```

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
    "payout_minor": 245000,
    "settled_at": "2026-08-29T10:00:34Z"
  },
  "occurred_at": "2026-08-29T10:00:34Z"
}
```

`payout_minor` 是该轮次所有中奖投注的总派奖，不是当前用户的派奖。玩家端收到后应重新查询自己的投注和钱包；管理端可刷新轮次统计。

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

`wallet.ledger.committed` 目前只有事件常量，没有业务发布方，因此前端不能依赖该事件刷新所有钱包变动。

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
      "sender_user_id": "0ecdd037-e5a1-4831-ad20-ac30f05b6098",
      "body": "你好",
      "status": "visible",
      "client_request_id": "chat-001",
      "created_at": "2026-08-29T10:10:00Z"
    },
    "user_ids": [],
    "broadcast": true
  },
  "occurred_at": "2026-08-29T10:10:00Z"
}
```

- 全局/游戏房间：`broadcast=true`，需要订阅 `chat`。
- 客服/私聊房间：`broadcast=false`，`user_ids` 是房间成员；成员无需订阅 `chat` 也会定向收到。
- 后台角色虽然可以通过 HTTP 读写客服房间，但当前不会仅因拥有后台角色就自动加入 `user_ids`。未成为房间成员的后台客服必须通过 HTTP 刷新消息，不能依赖 Socket 收到玩家的新消息。
- 前端按 `message.id` 去重。

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
      "total_minor": 10000,
      "remaining_minor": 10000,
      "packet_count": 10,
      "claimed_count": 0,
      "status": "active",
      "expires_at": "2026-08-30T10:10:00Z",
      "created_at": "2026-08-29T10:10:00Z"
    },
    "user_ids": [],
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
      "amount_minor": 860,
      "claimed_at": "2026-08-29T10:11:00Z"
    },
    "user_ids": [],
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
    "refund_minor": 3200,
    "user_ids": [],
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
  "error": "chat room access denied",
  "occurred_at": "2026-08-29T10:12:00Z"
}
```

当前可能出现的错误：

| `error` | 场景 | 建议处理 |
| --- | --- | --- |
| `invalid realtime command` | JSON 无效、版本/类型错误、topic 无效、缺少必要字段。 | 记录命令类型并修正客户端；不要原样无限重试。 |
| `text messages are required` | 发送了二进制帧。 | 改为 JSON 文本帧。 |
| `message must contain 1-2000 characters` | 聊天内容为空或过长。 | 前端校验后提示用户。 |
| `client_request_id is required` | 聊天幂等键为空或超过 128 字符。 | 生成并复用合法请求 ID。 |
| `chat room access denied` | 当前用户无房间权限。 | 停止重试并刷新房间列表。 |
| `chat room not found` | 房间不存在。 | 刷新房间列表。 |
| `chat is unavailable` | 网关未装配聊天服务。 | 退避后重试或降级提示。 |
| `unable to send chat message` | 服务端内部或依赖异常。 | 保留相同 `request_id`，退避后重试。 |

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
        if (message.v !== 1) throw new Error(`unsupported realtime version: ${message.v}`);

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
      this.options.onError?.(new Error("realtime connection error"));
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
    this.socket?.close(1000, "client closed");
  }

  private send(command: object): void {
    if (this.socket?.readyState !== WebSocket.OPEN) {
      throw new Error("realtime connection is not open");
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
- 每一笔账本变化的统一钱包事件。
- 管理后台专用的全量玩家资金订阅。
- 管理后台客服角色自动接收全部客服房间消息。

需要上述能力时必须先扩展后端协议和事件 payload，再同步更新本文档，前端不得自行假设事件存在。
