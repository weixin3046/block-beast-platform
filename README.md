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

macOS / Linux：

```bash
./scripts/dev-up.sh
```

可选参数：`--skip-infra` 跳过基础设施容器，`--swagger` 同时启动
`http://localhost:8082` 的 Swagger UI。脚本会启动 API、Worker、Realtime，并在
按下 `Ctrl+C` 时停止这三个 Go 进程。

本地 PostgreSQL 容器映射到 `localhost:5433`，避免与 macOS/Linux
上已安装的 PostgreSQL 默认端口 `5432` 冲突。
脚本会自动把 `.env` 中供容器使用的 `postgres`、`nats` 主机名
替换为宿主机地址。如需自定义，可设置 `DEV_POSTGRES_DSN`、
`DEV_NATS_URL`。

Windows PowerShell：

```powershell
Copy-Item .env.example .env
.\scripts\dev-up.ps1
```

首次创建 PostgreSQL 数据卷时会按文件名顺序执行 `migrations/` 下的全部迁移。已有数据卷不会自动重新执行初始化脚本；macOS/Linux 使用 `scripts/dev-up.sh` 启动时会自动识别并补跑缺失迁移，也可单独运行 `scripts/dev-migrate.sh`。生产环境升级仍需在发布流程中按顺序执行新增迁移，不能通过删除数据卷完成升级。

## 生产部署

服务器使用独立的 `compose.production.yaml` 和 `.env.production`：

```bash
cp .env.production.example .env.production
# 填写生产数据库、NATS、JWT、PQPA、QuickNode 及域名配置
./scripts/deploy-production.sh
```

该脚本会先执行增量迁移，再更新 API、Worker 和 Realtime。完整的网络隔离、
HTTPS/WebSocket 代理、验证和运维说明见
[生产环境 Docker 部署](docs/deployment.md)。

API 健康检查：`http://localhost:8080/healthz`。

Realtime 网关健康检查：`http://localhost:8081/healthz`，认证 WebSocket 地址为 `ws://localhost:8081/v1/ws`。

## 当前接口

前端可直接使用的完整接口契约见 [OpenAPI 3.1 文档](docs/openapi.yaml)，接入顺序与 TypeScript 示例见 [前端接入说明](docs/frontend-api.md)。

| 方法与地址 | 用途 |
| --- | --- |
| `GET /healthz` | API 存活检查。 |
| `GET /readyz` | PostgreSQL 就绪检查。 |
| `GET /v1/platform` | 查询当前环境与领域列表。 |
| `POST /v1/auth/register` | 注册玩家账号（创建用户、player 角色和 USDT 零余额钱包），直接返回访问令牌。 |
| `POST /v1/auth/login` | 玩家端登录；拒绝 admin/operator 后台账号；连续失败达到阈值后返回 429。 |
| `POST /v1/admin/auth/login` | 管理后台登录；仅允许 admin/operator；与玩家端共用持久化失败锁定。 |
| `POST /v1/auth/refresh` | 轮换玩家端刷新令牌并复核 player 角色。 |
| `POST /v1/admin/auth/refresh` | 轮换管理后台刷新令牌并复核 admin/operator 角色。 |
| `POST /v1/auth/logout` | 撤销刷新令牌并退出当前会话。 |
| `POST /v1/chat/customer-service` | 获取或创建当前玩家的专属客服房间。 |
| `GET/POST /v1/chat/rooms/{room_id}/messages` | 查询或幂等发送有权限访问的聊天消息。 |
| `POST /v1/uploads/authorize` | 获取本地存储或 S3 上传授权。 |
| `PUT/GET /v1/uploads/{upload_id}/content` | 鉴权上传或下载本人的本地文件。 |
| `POST /v1/uploads/{upload_id}/confirm` | 幂等确认上传并校验大小和类型。 |
| `GET /v1/leaderboards/daily` | 查询 UTC 日榜，支持有效投注、净收益和胜场排序。 |
| `POST /v1/chat/rooms/{room_id}/red-packets` | 使用 USDT 或积分余额幂等创建聊天室红包。 |
| `POST /v1/red-packets/{packet_id}/claim` | 幂等领取红包，过期未领金额由 Worker 自动退款。 |
| `GET /v1/announcements` | 查询当前生效的玩家公告。 |
| `GET/POST /v1/admin/announcements` | 后台查询或创建公告。仅 operator/admin。 |
| `PUT /v1/admin/announcements/{announcement_id}` | 后台更新公告内容、启用状态和展示时间。仅 operator/admin。 |
| `GET /v1/admin/roles` | 查询标准后台角色。仅 admin。 |
| `PUT /v1/admin/users/{user_id}/roles` | 分配角色并撤销目标账号全部刷新会话。仅 admin。 |
| `GET /v1/configs/{key}` | 匿名读取标记为 public 的平台 JSON 配置。 |
| `GET /v1/admin/configs`、`PUT /v1/admin/configs/{key}` | 后台通过版本号安全管理平台配置。仅 admin。 |
| `GET /v1/admin/audit-logs` | 按操作或管理员筛选不可变审计日志。仅 admin。 |
| `GET /v1/game-rooms` | 查询启用的游戏房间及房内玩法。 |
| `GET/POST /v1/admin/game-rooms` | 管理房间数量、名称、分类、排序和状态；代码由后端生成。仅 operator/admin。 |
| `GET/POST /v1/admin/game-types` | 查询或创建房内玩法与结算规则。仅 operator/admin。 |
| `PUT /v1/admin/game-types/{game_type_id}` | 修改玩法规则或启停玩法。仅 operator/admin。 |
| `GET/POST /v1/admin/rounds` | 查询轮次或创建固定封盘时间的新轮次。仅 operator/admin。 |
| `GET /v1/rounds?game_type={code}&limit={1-100}` | 查询指定游戏类型的开放轮次。 |
| `GET /v1/rounds/{round_id}` | 查询单个轮次。 |
| `POST /v1/rounds/{round_id}/cancel` | 取消开放或已封盘轮次，并退款全部接受中的投注。仅 operator/admin。 |
| `POST /v1/bets` | 创建幂等投注，同时扣减余额、写入账本和 outbox。仅本人或 operator/admin。 |
| `GET /v1/bets/{bet_id}` | 查询投注记录与状态。仅本人或 operator/admin。 |
| `GET /v1/wallets/{account_id}?currency={code}` | 查询钱包可用与冻结余额。仅本人或 operator/admin。 |
| `POST /v1/webhooks/chain/deposits` | 链上充值回调（服务商）：HMAC 签名验签，按事件 ID 与交易哈希幂等入账。 |
| `POST /v1/withdrawals` | 创建提现申请：校验地址及单笔/每日限额，冻结金额，幂等键防重复。仅本人。 |
| `GET /v1/withdrawals/{withdrawal_id}` | 查询提现申请。仅本人或 operator/admin。 |

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

已实现轮次结算与 Worker 接入：房间按 `game_kind` 分为哈希和 K 线两类。房间和玩法代码均由后端自动生成。TRON 平均 3 秒出块，哈希 N 使用当前高度 H 的下一个 N 整倍数 `(floor(H/N)+1)×N` 作为目标区块，并把该高度直接保存为轮次号，不需要基准区块；K 线房间内可选择 BTC 或 ETH，每分钟使用刚闭合的上一根 1 分钟 K 线开奖。每个房内玩法独立配置赔率、投注限额和提前封盘秒数。Worker 为每个启用玩法自动保持三期未来轮次，并只在开奖时刻到达后结算已封盘（或中断在结算中）的轮次。玩法赔率使用百分整数，194 表示 1.94 倍。玩法规则定义在 `game_types.rules` 中，包括结果池 `outcomes`、派奖倍数 `payout_multiplier`、倍率除数 `payout_divisor`、可选的中奖字段 `match_field` 和开奖个数 `result_count`。`okx_kline` 使用 OKX 业务 WebSocket 的 `candle1m` 作为实时主通道；`tron_hash` 使用 QuickNode TRON JSON-RPC 查询区块高度与哈希。

玩法规则示例：

```json
{
	"outcomes": ["red", "black"],
	"payout_multiplier": 2,
	"match_field": "color",
	"result_count": 1
}
```

已实现 JetStream 消费端可靠性：Worker 以耐用消费者订阅 `game.>`、`wallet.>`、`chain.>` 主题，处理失败时按退避策略（1s/2s/5s/10s/30s）重投，最多投递 5 次后把原始消息连同失败原因、投递次数等上下文发布到 `BLOCK_BEAST_DEAD_LETTERS` 死信流（主题为 `deadletter.<原主题>`）并终止重投。消费侧内置监控计数器（接收/成功/重试/死信），变化时输出结构化日志；NATS 自带的监控接口在 `http://localhost:8222/jsz` 可查看流与消费者状态。提现审批事件已接入 PQPA 出金，其余事件当前主要由 Realtime 转发或记录日志。

后续实现顺序：

1. ~~将游戏规则、结果来源与结算任务接入 Worker，按玩法定义赔率和更精确的中奖判定。~~（已完成）
2. ~~NATS JetStream 消费者重试、死信与事件处理监控。~~（已完成）
3. ~~JWT/RBAC、业务接口鉴权和后台审计。~~（已完成）
4. ~~链上回调验签、充值确认与提现流程。~~（已完成：PQPA 资产自动同步、官方充值/提现协议、验签与幂等入账、审批后异步出金、Webhook 状态更新及主动对账补偿）
5. ~~实时 WebSocket 协议、订阅和通知。~~（已完成：v1 协议握手、全局游戏/指定轮次订阅、用户定向资金通知、心跳和慢连接背压保护）

不在仓库中保存私钥、数据库密码、第三方 API 密钥或生产环境配置。
