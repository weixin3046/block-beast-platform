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

1. 调用 `POST /v1/auth/register` 注册账号（或 `POST /v1/auth/login` 登录），响应中的 `access_token` 用于后续所有业务接口；注册成功即拥有 player 角色和三种零余额钱包。
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

const { access_token, user_id } = auth;

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

## 当前限制

当前 API 还没有跨域（CORS）策略和令牌刷新机制：访问令牌有效期 15 分钟，过期后需重新登录。前端在本地开发时应通过同源代理访问 API；生产环境接入前必须配置明确的允许来源。
