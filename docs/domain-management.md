# 后端域名管理

仅管理后端 API/WS 入口及可选浏览器来源，不修改前端、DNS、数据库或第三方回调。
先通过现有 `./scripts/deploy.sh staging` 或 `./scripts/deploy.sh production` 发布本版本，安装远端 domainctl。
以下命令中的 staging 可替换为 production；两者沿用现有固定服务器映射。

## 操作

```sh
# 只读预检，不上传私钥、不修改配置
./scripts/domains.sh staging add api.example.com --dry-run
# 无证书时仅启用 HTTP
./scripts/domains.sh staging add api.example.com
# 有证书时同时启用 HTTP/HTTPS，不强制跳转
./scripts/domains.sh staging add api.example.com --cert /local/fullchain.pem --key /local/privkey.pem
# 更新或补充证书
./scripts/domains.sh staging certificate api.example.com --cert /local/fullchain.pem --key /local/privkey.pem
# 显式复用远端证书（会复制为独立版本，不自动跟随原路径续期）
./scripts/domains.sh staging add api.example.com --remote-cert /etc/certs/fullchain.pem --remote-key /etc/certs/privkey.pem
# 先验证新入口，再移除旧入口；证书参数同 add
./scripts/domains.sh staging replace old-api.example.com new-api.example.com
./scripts/domains.sh staging remove old-api.example.com
./scripts/domains.sh staging list
```

证书需域名匹配、有效、链完整可信且与私钥匹配，不支持交互解密私钥。
未上传且未指定可复用证书时只有 HTTP。HTTP-only 域名的443可能命中其他默认站点，不能视为 HTTPS 可用。
相同配置重复新增或上传同一证书不重复重载。替换默认继承旧域名来源，不继承旧证书。

## 浏览器来源

来源是页面域名，与后端访问域名分开维护。需要时明确添加：

```sh
./scripts/domains.sh staging add api.example.com --allow-origin http://web.example.com --allow-origin https://web.example.com
```

原环境来源保留，共用来源不会因删除其中一个域名而消失。来源变更重启 API/Realtime，实时连接需要重连；只改变路由不重启业务进程。
当前来源采用增量添加；撤回某域名独有来源需要先移除再以所需来源添加，会有短暂入口中断。

## 既有域名导入

```sh
./scripts/domains.sh staging import api.example.com --source /www/server/panel/vhost/nginx/backend.conf --dry-run
./scripts/domains.sh staging import api.example.com --source /www/server/panel/vhost/nginx/backend.conf
```

只支持明确的本机8080/8081标准代理，保留同文件其他域名。遇前端路由、自定义鉴权、限流或其他不能可靠迁移的配置会拒绝，避免丢失保护。普通 add 不覆盖非受管站点。导入已有 TLS 时仍校验证书，可显式提供正确的新证书。

## 配置与恢复

- 状态、私钥及恢复材料：`/etc/block-beast/domains/`，root 专用，私钥0600。
- 来源文件：`/etc/block-beast/managed-origins.json`，root 写、blockbeast 组只读。
- 应用通过 `MANAGED_ORIGINS_FILE` 加载来源，文件无效时拒绝启动。
- deploy.sh 检测到来源文件后校验并保留其路径，不用本地环境文件覆盖来源列表。
- 域名操作与部署共用 `/run/lock/block-beast-deploy.lock`。
- 受管站点使用 `block-beast-domain-<域名>.conf`，不要手工修改。

失败时恢复本次实际修改的文件；恢复失败保留 pending.json 并报错。下一次写操作先恢复未完成事务，不应手工删除日志。
last-operation.json 包含上一操作快照，可能含环境配置秘密，不得公开或提交仓库。
当前提供失败自动回滚及中断恢复，不提供任意历史版本的回退命令。

## 验证边界

dry-run 检查输入、状态、冲突和可读取的证书；不写候选文件，因此 Nginx 候选检查与在线验收明确标记未执行。上传证书的 dry-run 在本地验证，不上传私钥。
实际操作在目标服务器回环地址使用域名 Host/SNI 检查 API 健康、就绪、TLS 和 WS 路由，不使用代理、不跟随重定向、不跳过证书校验。
这不等于证明公网 DNS、备案及所有网络可用。

未提供测试会话时 WS 的401只证明未认证路由可达。完整握手可使用仅含一个有效访问令牌的0600文件：

```sh
chmod 600 /local/test-session.txt
./scripts/domains.sh staging add api.example.com --session-file /local/test-session.txt
```

令牌不通过命令参数或日志输出，上传临时文件会清理；检查 hello 不触发业务写入。
本地需要 Go、OpenSSH、OpenSSL；远端使用现有宝塔 Nginx、Supervisor、Linux flock 和 blockbeast 用户组。
