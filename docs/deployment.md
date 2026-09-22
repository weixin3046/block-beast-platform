# 服务器部署（宝塔 / Supervisor）

测试与正式环境使用宝塔 Nginx、Supervisor 和本地 Linux 二进制，统一入口为
`scripts/deploy.sh`。本文件替代旧生产 Docker 部署说明。

`compose.yaml`、`Dockerfile` 和 Docker 迁移回退流程仍供本地开发使用。
仓库保留的 `compose.production.yaml`、`scripts/deploy-production.sh` 属于旧方案，
不用于当前测试或正式服务器发布。

## 环境与配置

- 环境目标由 `scripts/deploy.sh staging|production` 固定选择；只操作
  [AGENTS.md](../AGENTS.md) 授权的服务器。
- 本地分别准备 `.env.staging`、`.env.production`，其中 `APP_ENV` 必须与环境一致。
  配置包含数据库、NATS、认证及第三方凭据，不得提交版本库。
- 服务器应用配置为 `/etc/block-beast/block-beast.env`。
- 发布目录为 `/opt/block-beast/releases/<version>`，`/opt/block-beast/current`
  指向当前版本，二进制位于发布目录的 `bin/`。
- Supervisor 管理 `block-beast-api`、`block-beast-worker`、
  `block-beast-realtime`、`block-beast-lulu-worker`。
- API 与 Realtime 的本机端口分别为 8080、8081；通过 Nginx 提供对外入口，
  数据库和 NATS 不应直接暴露到公网。

环境差异与测试服务器准备见 [测试环境](staging-environment.md)。
现有发布脚本面向已完成基础设施和 Supervisor 配置的服务器，不负责首次安装。

## 发布

在开发机仓库根目录执行所需环境命令：

```sh
./scripts/deploy.sh staging
# 正式环境发布时使用：
./scripts/deploy.sh production
```

统一入口运行 `go test ./...`，构建 Linux amd64 二进制并上传发布包。
远端先检查发布内容，再停止四个业务进程、备份并更新环境配置、执行缺失迁移、
切换 `current` 链接、启动进程并检查 API 和 Realtime 健康状态。
这是有停机窗口的发布，实时连接会断开并需要重连，不是滚动发布。

上传的环境文件会覆盖服务器配置，因此发布前必须核对服务器上手动调整的配置，
尤其是域名白名单，避免旧的本地配置覆盖线上修改。通过域名工具维护的来源独立保存，部署会校验并保留其引用，见 [域名管理](domain-management.md)。

底层 `scripts/deploy-baota.sh` 也支持显式 `DEPLOY_HOST`，目标必须符合
AGENTS.md；未提供 `DEPLOY_ENV_FILE` 时沿用服务器配置。日常优先使用统一入口。

## Nginx 与证书

宝塔站点配置位于 `/www/server/panel/vhost/nginx/`。修改时先查看目标环境现有
站点，不直接覆盖其他站点或把仓库示例当成当前线上配置。

- API 反向代理至 `127.0.0.1:8080`。
- `/v1/ws` 转发至 `127.0.0.1:8081`，配置 WebSocket Upgrade/Connection 头和长连接超时。
- 转发正确的 Host、客户端地址和请求协议。
- HTTPS 使用覆盖实际域名的有效证书和匹配私钥；新增 server_name 不会自动扩展证书。
- API 的 `API_ALLOWED_ORIGINS` 填写完整浏览器 Origin；Realtime 的
  `REALTIME_ALLOWED_ORIGINS` 使用当前实现支持的主机及端口模式。
  后端域名和浏览器页面来源不是同一概念。

在服务器检查并加载 Nginx 配置：

```sh
/www/server/nginx/sbin/nginx -t -c /www/server/nginx/conf/nginx.conf
# 只有检查成功后才执行：
/www/server/nginx/sbin/nginx -s reload -c /www/server/nginx/conf/nginx.conf
```

证书私钥限制读取权限，不写入日志或版本库。证书续期后检查并重载 Nginx。

## 验证与故障恢复

在目标服务器执行：

```sh
/www/server/panel/pyenv/bin/supervisorctl status
curl --fail --silent --show-error --max-time 15 http://127.0.0.1:8080/healthz
curl --fail --silent --show-error --max-time 15 http://127.0.0.1:8080/readyz
curl --fail --silent --show-error --max-time 15 http://127.0.0.1:8081/healthz
```

本机健康检查通过后，还应检查 Nginx 入口及经过鉴权的 WebSocket 连接。
日志位置以当前 Supervisor 配置为准，不在排障输出中泄露凭据。

迁移等启动前步骤失败时，远端脚本会尝试恢复旧环境配置、旧发布链接并启动服务。
启动后的健康检查失败也会尝试恢复旧配置及发布链接，并重新启动旧服务。
失败时必须检查实际进程、环境配置、发布链接和数据库状态，不能把命令结束视为服务正常。
数据库已经提交的迁移也不会因切换旧二进制自动撤销；回退前必须核实版本兼容性。

定期备份 PostgreSQL、实际上传目录及必要的加密主密钥，并验证恢复流程。
上传目录以 `LOCAL_UPLOAD_ROOT` 为准，不沿用旧 Docker 命名卷备份命令。
备份目标同样必须遵守仓库远程服务器限制。

## 0080 单期投注升级

0080 将未来轮次预创建为 `scheduled`，只开放最早未完成轮次。旧 Worker 会创建
多期 open，因此必须先停旧 API/Worker，再应用迁移及新版进程。
当前宝塔统一发布脚本已在迁移前停止四个业务进程，使用上述统一发布入口即可。
迁移保留投注和钱包数据；失败时排查原因，不启动旧 Worker 继续写入。

0081 五位用户 ID 升级同样需要停写并协调更新 API、Worker、Realtime。
迁移保留内部 UUID、余额和历史公开 ID 映射；发布后客户端需重新读取用户 ID
与邀请码。遵循对应迁移的兼容约束，不通过删除数据库或数据卷完成升级。
