# 前端接口接入

完整机器可读的接口定义在 [openapi.yaml](./openapi.yaml)。可直接导入 Swagger UI、Postman、Apifox，或用 OpenAPI Generator 生成 TypeScript 客户端。

本地 API 地址为 `http://localhost:8080`。所有接口使用 JSON，错误统一为：

```json
{ "error": "原因描述" }
```

## 调用顺序

1. 调用 `GET /v1/rounds?game_type={code}` 获取仍可下注的轮次。
2. 调用 `POST /v1/bets` 创建投注。浏览器应为每次用户确认操作生成稳定的 `client_request_id`；网络重试必须复用该值。
3. 使用 `GET /v1/bets/{betID}` 轮询投注状态；当前状态有 `accepted`、`won`、`lost` 与 `refunded`。
4. 使用 `GET /v1/wallets/{accountID}?currency=USDT` 刷新余额。

## TypeScript 示例

```ts
const api = "http://localhost:8080";

const response = await fetch(`${api}/v1/bets`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({
    client_request_id: crypto.randomUUID(),
    round_id: roundId,
    account_id: accountId,
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

当前 API 还没有登录、JWT 鉴权或跨域策略。前端在本地开发时应通过同源代理访问 API；生产环境接入前必须完成鉴权并配置明确的允许来源，不能把 `account_id` 作为可信身份依据。
