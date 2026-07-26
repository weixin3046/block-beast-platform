# 生产环境 Docker 部署

生产环境使用 `compose.production.yaml`，不要使用本地开发的 `compose.yaml` 或
`scripts/dev-up.sh`。生产配置具有以下约束：

- PostgreSQL 和 NATS 只加入 Docker 内部网络，不映射宿主机端口。
- API 与 Realtime 只绑定宿主机 `127.0.0.1`，由宿主机上的 Nginx、Caddy
  或云负载均衡提供 HTTPS/WSS。
- PostgreSQL 与 NATS JetStream 使用独立持久化卷。
- 应用容器以非 root、只读文件系统运行，并限制日志文件大小。
- 每次发布先执行带版本记录的增量数据库迁移，再更新应用容器。

## 1. 服务器准备

安装 Docker Engine、Docker Compose v2、Git，并只向公网开放 SSH、HTTP 和
HTTPS 端口。不要向公网开放 5432、4222、8222、8080 或 8081。

## 2. 创建生产配置

在仓库根目录执行：

```bash
cp .env.production.example .env.production
chmod 600 .env.production
```

编辑 `.env.production`，至少替换：

- `POSTGRES_PASSWORD` 和对应的 `POSTGRES_DSN`
- `NATS_PASSWORD` 和对应的 `NATS_URL`
- `AUTH_TOKEN_SECRET`
- 玩家端、管理后台的 Origin
- PQPA 与 QuickNode 凭据

如果服务器访问 Go 官方模块代理较慢，可将 `BUILD_GOPROXY` 改为部署区域可用
的可信代理；中国大陆常用 `https://goproxy.cn,direct`。

`AUTH_STRICT_PASSWORD_POLICY` 是密码严格规范的唯一开关，不受 `APP_ENV`
影响。正式环境是否开启由部署配置明确决定。

如果数据库密码包含 `@`、`:`、`/` 等 URL 特殊字符，写入
`POSTGRES_DSN` 时必须进行百分号编码。生产 `.env.production` 已被
`.gitignore` 和 `.dockerignore` 排除，不得提交。

## 3. 首次部署与更新

执行：

```bash
./scripts/deploy-production.sh
```

也可以传入其他环境文件：

```bash
./scripts/deploy-production.sh /secure/path/block-beast.env
```

脚本依次校验 Compose、构建镜像、启动 PostgreSQL/NATS、执行所有尚未应用的
迁移，然后更新 API、Worker 和 Realtime。迁移失败时脚本立即停止，不会更新
应用进程。

发布固定版本时，建议先检出 Git 标签或提交，并将 `APP_IMAGE_TAG` 设置为相同
版本：

```bash
git checkout v1.0.0
./scripts/deploy-production.sh
```

## 4. HTTPS 与 WebSocket

反向代理应将 API 域名转发至 `127.0.0.1:8080`，将实时域名的 `/v1/ws`
转发至 `127.0.0.1:8081`，并为 WebSocket 转发 `Upgrade` 与
`Connection` 请求头。PQPA Webhook 使用公开的 HTTPS API 域名。

示例 Nginx 片段：

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}

location /v1/ws {
    proxy_pass http://127.0.0.1:8081;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
}
```

## 5. 验证与运维

部署后检查：

```bash
curl --fail http://127.0.0.1:8080/healthz
curl --fail http://127.0.0.1:8080/readyz
curl --fail http://127.0.0.1:8081/healthz
docker compose --env-file .env.production -f compose.production.yaml ps
docker compose --env-file .env.production -f compose.production.yaml logs --tail=200 api worker realtime
```

停止应用但保留数据：

```bash
docker compose --env-file .env.production -f compose.production.yaml down
```

不要在生产环境使用 `down --volumes`。上线前必须建立 PostgreSQL 定时备份，
并实际验证恢复流程；同时监控容器健康、磁盘空间、Worker 错误、NATS
JetStream 积压以及 PQPA/OKX/QuickNode 调用失败。
