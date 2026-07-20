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

| 方法与地址 | 用途 |
| --- | --- |
| `GET /healthz` | API 存活检查。 |
| `GET /readyz` | PostgreSQL 就绪检查。 |
| `GET /v1/platform` | 查询当前环境与领域列表。 |
| `GET /v1/rounds?game_type={code}&limit={1-100}` | 查询指定游戏类型的开放轮次。 |
| `GET /v1/rounds/{round_id}` | 查询单个轮次。 |
| `POST /v1/rounds/{round_id}/cancel` | 取消开放或已封盘轮次，并退款全部接受中的投注。 |
| `POST /v1/bets` | 创建幂等投注，同时扣减余额、写入账本和 outbox。 |
| `GET /v1/bets/{bet_id}` | 查询投注记录与状态。 |
| `GET /v1/wallets/{account_id}?currency={code}` | 查询钱包可用与冻结余额。 |

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

已实现基础轮次结算：关闭或结算中的轮次可按结果结算，中奖投注会在同一事务中完成派奖、账本记录、轮次状态更新和 outbox 事件写入。当前基础规则将投注 JSON 中任一字符串值与结果值匹配，中奖金额由调用方提供的正整数倍数决定；具体玩法赔率仍应在游戏规则层实现。

后续实现顺序：

1. 将游戏规则、结果来源与结算任务接入 Worker，按玩法定义赔率和更精确的中奖判定。
2. NATS JetStream 消费者重试、死信与事件处理监控。
3. JWT/RBAC、业务接口鉴权和后台审计。
4. 链上回调验签、充值确认与提现流程。
5. 实时 WebSocket 协议、订阅和通知。

不在仓库中保存私钥、数据库密码、第三方 API 密钥或生产环境配置。
