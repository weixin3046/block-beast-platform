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

## 当前接口

前端可直接使用的完整接口契约见 [OpenAPI 3.1 文档](docs/openapi.yaml)，接入顺序与 TypeScript 示例见 [前端接入说明](docs/frontend-api.md)。

| 方法与地址 | 用途 |
| --- | --- |
| `GET /healthz` | API 存活检查。 |
| `GET /readyz` | PostgreSQL 就绪检查。 |
| `GET /v1/platform` | 查询当前环境与领域列表。 |
| `POST /v1/auth/register` | 注册玩家账号（创建用户、player 角色和 USDT 零余额钱包），直接返回访问令牌。 |
| `POST /v1/auth/login` | 密码登录，签发携带角色的短期 JWT（默认 15 分钟）。 |
| `GET /v1/rounds?game_type={code}&limit={1-100}` | 查询指定游戏类型的开放轮次。 |
| `GET /v1/rounds/{round_id}` | 查询单个轮次。 |
| `POST /v1/rounds/{round_id}/cancel` | 取消开放或已封盘轮次，并退款全部接受中的投注。仅 operator/admin。 |
| `POST /v1/bets` | 创建幂等投注，同时扣减余额、写入账本和 outbox。仅本人或 operator/admin。 |
| `GET /v1/bets/{bet_id}` | 查询投注记录与状态。仅本人或 operator/admin。 |
| `GET /v1/wallets/{account_id}?currency={code}` | 查询钱包可用与冻结余额。仅本人或 operator/admin。 |

除健康检查、平台信息和登录外，业务接口均需 `Authorization: Bearer <access_token>`。登录成功与失败、轮次取消等敏感操作会写入 `audit_logs` 审计表。未设置 `AUTH_TOKEN_SECRET` 时鉴权自动关闭（仅限本地开发，启动日志会给出警告）。

`POST /v1/bets` 请求示例：

```json
{
	"client_request_id": "request-001",
	"round_id": "轮次 UUID",
	"account_id": "用户 UUID",
	"currency": "USDT",
	"selection": { "color": "red" },
	"stake_minor": 2500
}
```

除健康检查和平台信息外，业务接口尚未接入身份认证，仅适用于本地开发。

## 本地代码格式化

团队统一使用 `gofmt`。`.editorconfig` 统一 IDE 的缩进、UTF-8、LF 换行符和文件末尾换行；`.gitattributes` 确保 Git 提交时采用 LF。

提交前在项目根目录执行：

```powershell
go fmt ./...
```

在 VS Code 中安装 Go 扩展后，使用“格式化文档”或启用保存时格式化即可遵循相同规范。GitHub Actions 会在每次 push 和 pull request 中执行 `gofmt` 校验；格式不符合规范时检查会失败。

## 停止服务

```powershell
docker compose down
```

该命令会停止并移除容器与网络，但会保留 PostgreSQL 数据卷。若也需要清空本地数据库数据：

```powershell
docker compose down --volumes
```

## 下一步实现顺序

已实现轮次结算与 Worker 接入：Worker 每个调度周期依次封盘到期轮次、结算已封盘（或中断在结算中）的轮次、发布 outbox 事件。玩法规则定义在 `game_types.rules` 中，包括结果池 `outcomes`、派奖倍数 `payout_multiplier`、可选的中奖字段 `match_field`（支持点路径，限定只比较 selection 中该字段的值）和开奖个数 `result_count`。结算时按玩法加载规则：开奖结果必须落在结果池内，中奖投注在同一事务中完成派奖、账本记录、轮次状态更新和 outbox 事件写入；单轮结算失败会回滚并保持原状态，等待下个周期重试。当前结果来源为确定性的哈希实现（同一轮次重复开奖结果一致），生产环境应替换为 TRON 区块哈希或 K 线等外部可验证来源。

玩法规则示例：

```json
{
	"outcomes": ["red", "black"],
	"payout_multiplier": 2,
	"match_field": "color",
	"result_count": 1
}
```

已实现 JetStream 消费端可靠性：Worker 以耐用消费者订阅 `game.>`、`wallet.>`、`chain.>` 主题，处理失败时按退避策略（1s/2s/5s/10s/30s）重投，最多投递 5 次后把原始消息连同失败原因、投递次数等上下文发布到 `BLOCK_BEAST_DEAD_LETTERS` 死信流（主题为 `deadletter.<原主题>`）并终止重投。消费侧内置监控计数器（接收/成功/重试/死信），变化时输出结构化日志；NATS 自带的监控接口在 `http://localhost:8222/jsz` 可查看流与消费者状态。当前处理器为占位日志实现，返佣、通知等业务消费者将在此基础上接入。

后续实现顺序：

1. ~~将游戏规则、结果来源与结算任务接入 Worker，按玩法定义赔率和更精确的中奖判定。~~（已完成）
2. ~~NATS JetStream 消费者重试、死信与事件处理监控。~~（已完成）
3. ~~JWT/RBAC、业务接口鉴权和后台审计。~~（已完成）
4. 链上回调验签、充值确认与提现流程。
5. 实时 WebSocket 协议、订阅和通知。

不在仓库中保存私钥、数据库密码、第三方 API 密钥或生产环境配置。
