# 测试环境

2026-09-19 起使用新测试服务器；目标由 `scripts/deploy.sh staging` 固定选择。
旧测试服务器已经退役，不再访问。

## 与正式环境对齐

- Ubuntu 24.04.2、x86_64，安装宝塔面板。
- PostgreSQL 17.6、Nginx 1.26.3、NATS 2.10.7、Supervisor 4.2.4。
- Supervisor 管理 `block-beast-api`、`block-beast-worker`、
  `block-beast-realtime`、`block-beast-lulu-worker`、`block-beast-nats`。
- 应用以 `blockbeast` 用户运行，NATS 以 `nats` 用户运行。
- 发布目录 `/opt/block-beast/releases/<version>`，当前版本由 `current` 链接指定。
- 应用配置 `/etc/block-beast/block-beast.env`；启动脚本 `/opt/block-beast/run`。
- 两个环境共用 `scripts/deploy-baota.sh` 与 `scripts/deploy-baota-remote.sh`。

## 测试环境隔离与差异

数据库、JWT、NATS 密码和 Lulu 配置加密密钥独立生成，不复制正式业务数据。
测试环境的 PQPA 认证及对象存储认证留空，使用本地上传目录
`/var/lib/block-beast/uploads`。第三方资金通道应配置测试专用凭据后再验证。

当前通过 IP 提供 HTTP API，尚未配置测试域名和 HTTPS 证书。
本次准备后端运行环境，不包含玩家端或管理端前端构建发布。
宝塔安装日志（含初始登录信息）在服务器 `/root/block-beast-panel-install.log`，
仅 root 可读取；也可在服务器执行 `bt default` 获取面板登录信息。

PostgreSQL 由 `block-beast-postgres.service` 管理，数据目录为
`/www/server/pgsql/data`。Supervisor、Nginx 和 PostgreSQL 均启用开机启动。
PostgreSQL、NATS、API、Realtime 只监听回环地址。
Nginx 配置为 `/www/server/panel/vhost/nginx/block-beast-staging.conf`。

## 发布和验证

```sh
./scripts/deploy.sh staging
```

服务器上执行：

```sh
/www/server/panel/pyenv/bin/supervisorctl status
systemctl status block-beast-postgres nginx supervisord
curl --fail http://127.0.0.1/healthz
curl --fail http://127.0.0.1/readyz
curl --fail http://127.0.0.1/realtime/healthz
```

本地 `.env.staging` 已对应新机器；迁移前配置备份为
`.env.staging.before-server-migration`。两者均不应提交版本库。

## LuluAll 独立采集服务

2026-09-19 从用户提供的 `Downloads/LuluAll` 源码编译部署 .NET 10 Linux 版本。
Supervisor 进程名 `luluall`，程序目录 `/opt/luluall/current`，SQLite 和采集文件
位于 `/var/lib/luluall/data`，登录凭据路径 `/etc/luluall/token.json`。
按用户选择不上传旧凭据，需用测试专用账号重新登录后才会开始有效采集。

管理页面仅监听 `127.0.0.1:9082`。本机建立隧道：

```sh
ssh -N -L 19082:127.0.0.1:9082 root@120.26.41.254
```

然后浏览器访问 `http://127.0.0.1:19082`，使用测试专用账号完成验证码登录。

正式服务器可 GET `http://120.26.41.254/luluall/api/v1/games`，以及：

- `/luluall/api/v1/games/cock/history?limit=200`：怒翎破阵，对应平台 `lh`。
- `/luluall/api/v1/games/steal/history?limit=200`：星海逃杀，对应平台 `xdy`。
- `/luluall/api/v1/games/race/history?limit=200`：绿茵疾冲，对应平台 `race`。
- `/luluall/api/v1/games/{game}/rounds/{id}`：按期号查询。

Nginx 仅允许正式服务器 IP 和回环地址读取上述 API；其他来源返回 403。
登录接口不向公网代理。已验证正式服务器 GET 成功、其他来源被拒绝。

这只是数据源部署，未修改正式 Worker 或启用自动补期。原程序 API 仅查询
最近 500 期内存历史，重启仅恢复最近 200 期；缺少准确开奖时间的对账记录
不落 SQLite。正式补期接入需处理游戏编码映射、完整性和冲突校验、幂等及
持久历史查询，不能把来源的接收时间当作准确开奖时间。
