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

1. 调用 `POST /v1/auth/register` 注册账号（或 `POST /v1/auth/login` 登录），保存响应中的 `access_token` 和 `refresh_token`；访问令牌用于业务接口，过期后调用 `POST /v1/auth/refresh` 原子轮换两个令牌。退出时调用 `POST /v1/auth/logout` 撤销刷新令牌。
2. 调用 `GET /v1/rounds?game_type={code}` 获取仍可下注的轮次。
3. 调用 `POST /v1/bets` 创建投注，`currency` 传 `USDT` 或 `POINTS`。浏览器应为每次用户确认操作生成稳定的 `client_request_id`；网络重试必须复用该值。`account_id` 必须与令牌主体一致（本人），否则返回 403。
4. 使用 `GET /v1/bets/{betID}` 轮询投注状态；当前状态有 `accepted`、`won`、`lost` 与 `refunded`。
5. 使用 `GET /v1/wallets/{accountID}?currency=USDT` 查询单币种余额，或 `GET /v1/wallets/{accountID}/all` 一次拉取全部币种。
6. 每日首次进入时调用 `POST /v1/tasks/checkin` 签到领取体力；`checked_in=false` 表示今日已签过，不要重复提示。
7. 参加活动时调用 `POST /v1/stamina/consume` 扣体力，`activity_id` 由活动方提供；体力不足返回 409。

## TypeScript 示例

```ts
const api = "http://localhost:8080";

// 1. 注册（已有账号则改为 /v1/auth/login）
const auth = await fetch(`${api}/v1/auth/register`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ login_name: "player-001", password: "至少12位密码" }),
}).then((r) => (r.ok ? r.json() : r.json().then(({ error }) => Promise.reject(new Error(error)))));

let { access_token, refresh_token, user_id } = auth;

// access_token 过期后轮换；旧 refresh_token 随即失效
const refreshed = await fetch(`${api}/v1/auth/refresh`, {
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

API 通过 `API_ALLOWED_ORIGINS` 配置玩家端和管理后台的跨域白名单。访问令牌有效期 15 分钟，当前尚未实现刷新令牌，过期后需重新登录。

## 实时连接

浏览器通过子协议连接：`new WebSocket("ws://localhost:8081/v1/ws", ["bearer." + accessToken])`。`game.*` 事件广播给所有已认证连接；`wallet.*` 和 `chain.*` 事件仅发送给事件 `user_id` 对应的玩家。生产环境必须使用 `wss://`，并通过 `REALTIME_ALLOWED_ORIGINS` 限制前端来源。
