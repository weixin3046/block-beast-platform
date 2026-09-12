# 宝塔生产部署实施计划

**目标：** 在本次会话获授权的正式服务器上，以全新数据库部署当前后端，尽量使用宝塔管理组件。

**架构：** 宝塔 Nginx 提供 IP HTTPS 和 WebSocket 代理；宝塔 PostgreSQL 管理器安装数据库；宝塔 Supervisor 管理 NATS 和四个应用进程。Go 1.26.5 在本地构建 Linux amd64 二进制。管理员初始化使用已有 bootstrap-admin 命令。

**约束：** 本次会话已授权连续安装。服务器目标仅取本次会话明确授权值，不写入仓库。没有域名，使用全新数据库，不读取旧服务器，不迁移旧业务数据。凭据在服务器生成，不输出到日志或提交到仓库。数据库、NATS 和应用仅监听回环地址。

## 安装与验证

- [x] 阅读现有部署文档、接口约束和应用启动配置。
- [x] 运行 `go test ./...`；构建 api、worker、realtime、lulu-worker 和 bootstrap-admin。
- [x] 安装免费的宝塔 PostgreSQL 管理器与 Supervisor 管理器。
- [ ] 完成宝塔 Nginx 安装，以 `nginx -t` 验证配置。
- [ ] 通过宝塔插件安装 PostgreSQL 17，创建独立数据库角色和全新数据库。
- [ ] 安装 NATS 2.10，启用鉴权及 JetStream 持久化，用 Supervisor 守护。
- [ ] 上传二进制与迁移文件，生成权限受限的生产环境文件。
- [ ] 执行现有 `scripts/migrate.sh`，核对迁移版本及空数据库初始化结果。
- [ ] 注册四个应用守护进程，检查 API readyz、Realtime healthz 和 NATS healthz。
- [ ] 配置 IP HTTPS、WebSocket 转发及证书续期重载；验证鉴权拒绝和管理员登录。
- [ ] 设置数据库和上传文件备份，验证备份可恢复；记录异地备份尚需配置。
- [ ] 整理运维路径、启动状态、实际版本和未启用的外部业务集成。
