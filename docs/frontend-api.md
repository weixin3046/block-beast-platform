# 前端接口接入

完整机器可读的接口定义在 [openapi.yaml](./openapi.yaml)。可直接导入 Swagger UI、Postman、Apifox，或用 OpenAPI Generator 生成 TypeScript 客户端。

本地 API 地址为 `http://localhost:8080`。所有接口使用 JSON，错误统一为：

```json
{ "error": "原因描述" }
```

## 调用顺序

1. 调用 `POST /v1/auth/register` 注册账号（或 `POST /v1/auth/login` 登录），响应中的 `access_token` 用于后续所有业务接口；注册成功即拥有 player 角色和 USDT 零余额钱包。
2. 调用 `GET /v1/rounds?game_type={code}` 获取仍可下注的轮次。
3. 调用 `POST /v1/bets` 创建投注。浏览器应为每次用户确认操作生成稳定的 `client_request_id`；网络重试必须复用该值。`account_id` 必须与令牌主体一致（本人），否则返回 403。
4. 使用 `GET /v1/bets/{betID}` 轮询投注状态；当前状态有 `accepted`、`won`、`lost` 与 `refunded`。
5. 使用 `GET /v1/wallets/{accountID}?currency=USDT` 刷新余额，仅本人或 operator/admin 可查。

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

// 2. 携带令牌投注
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
    currency: "USDT",
    selection: { color: "red" },
    stake_minor: 2500,
  }),
});

if (!response.ok) {
  const { error } = await response.json();
  throw new Error(error);
}
const bet = await response.json();
```

## 当前限制

当前 API 还没有跨域（CORS）策略和令牌刷新机制：访问令牌有效期 15 分钟，过期后需重新登录。前端在本地开发时应通过同源代理访问 API；生产环境接入前必须配置明确的允许来源。
