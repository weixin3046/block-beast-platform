# Block Beast Platform

面向实时游戏、资金账本、链上充提和运营后台的 Go 后端基础工程。它采用“领域清晰、可独立部署、按负载逐步拆分”的演进方式：初期是模块化单体加独立 Worker 与实时网关，后续可平滑拆分为钱包、结算、链服务和实时服务。

## 当前进程

- `cmd/api`：HTTP API、鉴权入口和管理端入口。
- `cmd/worker`：结算、返水、排行榜、充值确认与通知等异步任务的运行入口。
- `cmd/realtime`：实时连接网关的运行入口。

## 领域边界

| 模块 | 负责内容 |
| --- | --- |
| `identity` | 登录、用户、角色与权限 |
| `wallet` | 余额、冻结、不可变账本、充值和提现 |
| `game` | 玩法、下注、轮次、封盘和结算 |
| `agent` | 推荐关系、返佣和返水 |
| `realtime` | WebSocket、聊天室、通知和在线状态 |
| `chain` | 链上地址、充值确认、提现与第三方回调 |
| `operations` | 后台配置、公告、风控、报表与审计 |

## 本地启动

```powershell
Copy-Item .env.example .env
docker compose up --build
```

API 健康检查：`http://localhost:8080/healthz`。

Realtime 网关健康检查：`http://localhost:8081/healthz`。当前仅提供健康检查，尚未实现根页面或 WebSocket 路由，因此访问 `http://localhost:8081/` 会返回 404。

## 停止服务

```powershell
docker compose down
```

该命令会停止并移除容器与网络，但会保留 PostgreSQL 数据卷。若也需要清空本地数据库数据：

```powershell
docker compose down --volumes
```

## 下一步实现顺序

1. PostgreSQL migration：账户、不可变账本、投注、结算和 outbox。
2. 钱包事务：幂等扣款、派奖、退款、充值、提现。
3. 游戏轮次状态机与结算 Worker。
4. NATS JetStream 事件发布、消费者重试与死信。
5. JWT/RBAC、后台审计、链上回调验签和实时 WebSocket 协议。

不在仓库中保存私钥、数据库密码、第三方 API 密钥或生产环境配置。