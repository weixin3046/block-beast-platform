# Backend Domain Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 提供后端域名新增、替换、移除、证书更新及可靠回滚命令，保留现有部署入口。

**Architecture:** Go 标准库实现状态、证书、Nginx 生成及远端事务执行器，Bash 只负责固定环境选择、构建与 SSH 传输。独立 Origin 文件与环境白名单合并，域名操作和部署共用远端锁。状态文件、证书和应用可读 Origin 文件分开保存。

**Tech Stack:** Go 1.26.5、Bash、OpenSSH、宝塔 Nginx、Supervisor；不增加第三方依赖。

**Spec:** [后端域名管理设计](../specs/2026-09-22-backend-domain-management-design.md)

## Global Constraints

- 不修改玩家端、管理端，不新增后台页面，不修改 DNS，不申请或自动续签证书，不修改第三方回调，不迁移数据库。
- HTTP 始终启用，不强制跳转 HTTPS。
- 没有证书时只声明 HTTP 可用，不使用其他域名的默认证书冒充 HTTPS 支持。
- 所有远程操作只允许 AGENTS.md 授权的测试、正式服务器。
- 只变更后端路由且没有 Origin 变化时，不重启 API 或 Realtime。
- 现有未提交的 README.md、docs/deployment.md、docs/lulu-integration.md 是本任务此前的文档清理，必须保留。
- 不提交真实域名、地址、证书、私钥或环境文件；沿用现有部署入口的环境映射，避免在新文件重复保存生产地址。
- 实施前建立隔离工作区；迁入需要沿用的文档改动时逐文件核对，不覆盖用户改动。

## Review Focus

1. 上传路径带空格或 shell 字符时不执行插值命令；任务 5 覆盖传输参数测试。
2. 删除一个域名不能删除其他域名共用的 Origin 或原有环境来源；任务 1、2 覆盖。
3. 进程被强制结束后不能跳过未完成操作记录；任务 4 覆盖启动恢复。
4. Nginx 重复 server_name 可能仅警告，工具必须自行拒绝冲突；任务 3 覆盖。
5. 配置载入不等于公网可用，HTTP-only 不能被报告为 HTTPS 成功；任务 4、5 覆盖结果分类。

## 文件及模块边界

- `internal/platform/domainops/model.go`：请求、版本状态、纯状态转换、域名和 Origin 校验。
- `internal/platform/domainops/certificate.go`：证书链、私钥及域名验证。
- `internal/platform/domainops/nginx.go`：Nginx 生成、配置冲突检测和受限导入。
- `internal/platform/domainops/transaction.go`：快照、锁、阶段日志、应用及恢复。
- `internal/platform/domainops/probe.go`：固定地址 HTTP/TLS 验收、结构化检查结果。
- `cmd/domainctl/main.go`：远端 CLI，解析 JSON 请求和输出非敏感结果。
- `scripts/domains.sh`：本地入口，复用固定环境目标映射，发送请求及可选证书。
- `scripts/deploy-target.sh`：从现有 deploy.sh 提取环境映射，两个入口共用；不是用户可覆盖的目标参数。
- `internal/config/origins.go`：独立 Origin 文件加载与合并。
- `internal/config/config.go`、`cmd/api/main.go`、`cmd/realtime/main.go`：接入启动加载与失败退出。
- `scripts/deploy-baota.sh`、`scripts/deploy-baota-remote.sh`：打包执行器、共享锁、保留独立 Origin 路径。
- `docs/domain-management.md`、`.env.example`、`README.md`、`docs/deployment.md`：使用与部署兼容说明。
- 上述 Go 文件的同包 `_test.go`、`scripts/domains_test.go` 和现有 `scripts/deploy_baota_test.go`：测试。

### Task 1: 状态和证书纯逻辑

**Files:** 新增 model.go、certificate.go 及同包测试。

**Interfaces:**

```go
type Request struct {
    Operation string `json:"operation"`
    Domain string `json:"domain"`
    OldDomain string `json:"old_domain,omitempty"`
    Origins []string `json:"origins,omitempty"`
    CertPath string `json:"cert_path,omitempty"`
    KeyPath string `json:"key_path,omitempty"`
    DryRun bool `json:"dry_run"`
}
type Domain struct {
    Name string `json:"name"`
    Origins []string `json:"origins"`
    CertPath string `json:"cert_path,omitempty"`
    KeyPath string `json:"key_path,omitempty"`
}
type State struct {
    Version int `json:"version"`
    Domains map[string]Domain `json:"domains"`
}
func NormalizeDomain(string) (string, error)
func NormalizeOrigin(string) (string, error)
func NextState(State, Request) (State, error)
func ValidateCertificate(domain string, certPEM, keyPEM []byte, roots *x509.CertPool, now time.Time) error
```

- [ ] 写域名规范化、非法字符、状态不变性及来源归属测试。至少包含：

```go
func TestRejectDomainInjection(t *testing.T) {
    for _, input := range []string{"https://api.example.com", "a.example;", "a.example\nserver{}", "*.example.com", "a.example:443"} {
        if _, err := NormalizeDomain(input); err == nil { t.Fatalf("accepted %q", input) }
    }
}
```

- [ ] 执行 `go test ./internal/platform/domainops`，确认因缺失实现失败。
- [ ] 实现严格 DNS 标签校验，转换小写；只接受无路径、userinfo、fragment 的 HTTP/HTTPS Origin。状态转换复制输入 map，add 重复请求幂等，未知域名 remove/replace 报错。来源按域名记录，输出联合来源时去重。
- [ ] 使用 `x509.CreateCertificate` 生成临时 CA 与服务器证书，覆盖 RSA/ECDSA、SAN 不匹配、过期、尚未生效、缺中间链、错私钥、加密私钥和共用证书。生产 roots=nil 使用系统信任根，测试注入专用 CA。
- [ ] 用 `tls.X509KeyPair` 校验匹配，`x509.Verify` 检查 DNSName、CurrentTime、Intermediates 与 ServerAuth，不联网下载链、不输出 PEM。
- [ ] 运行同包测试，检查通过后仅提交本任务文件。

### Task 2: 独立 Origin 文件加载

**Files:** 新增 internal/config/origins.go、origins_test.go；修改 config.go、API/Realtime main.go、.env.example。

**Interfaces:** `func (cfg *Config) LoadManagedOrigins() error`；新增 `Config.ManagedOriginsFile string`，环境变量 `MANAGED_ORIGINS_FILE`，未设置时不读取文件。文件结构：

```json
{"version":1,"origins":["http://web.example.com","https://web.example.com"]}
```

- [ ] 写临时 JSON 文件测试，覆盖未配置不变、缺失/损坏拒绝、未知 version、尾随 JSON 拒绝、原有环境来源保留、HTTP/HTTPS 分开保留、Realtime 主机去重。
- [ ] 执行 `go test ./internal/config`，确认新测试失败。
- [ ] 实现有限大小读取、严格 JSON 解析及 Task 1 来源校验；校验所有内容后一次性更新 cfg，避免出错时部分合并。
- [ ] 两个进程在 config.Load 后、创建数据库连接前调用：

```go
if err := cfg.LoadManagedOrigins(); err != nil {
    logger.Error("invalid managed origins configuration", "error", err)
    os.Exit(1)
}
```

- [ ] `.env.example` 添加可选路径说明，默认留空；错误只包含文件位置和原因，不回显完整配置。
- [ ] 执行 `go test ./internal/config ./internal/platform/httpapi ./internal/platform/realtime`；补充真实处理器测试确认合并来源可访问、未知来源仍被拒绝。通过后提交本任务文件。

### Task 3: Nginx 渲染和既有域名接管

**Files:** 新增 nginx.go、nginx_test.go。

**Interfaces:**

```go
func RenderNginx(Domain) ([]byte, error)
func ImportDomain(source []byte, domain string) (remaining []byte, imported Domain, err error)
```

- [ ] 写 HTTP-only、TLS 双监听、无 301、exact WebSocket、Host/转发协议、10m 上传限制、3600s WS 超时测试。
- [ ] 写 server_name 冲突测试及真实结构 fixture：两个 server 块、多个域名、注释、引号、嵌套 location、其他前端站点。复杂 include、变量 server_name、非本机 upstream 或无法证明是后端站点必须拒绝导入。
- [ ] 运行同包测试观察失败；实现固定模板，用户输入仅进入经校验的域名和受控证书路径。
- [ ] 以词法 token/括号深度处理导入，不做字符串全局替换；保留其他域名和全部代理指令，删除空 server 块。导入必须由 `import DOMAIN --source FILE` 显式触发，要求文件在宝塔 vhost 目录且无路径穿越；输出差异供 dry-run 查看。
- [ ] 检查活动 include 图中的名称冲突，不把 .bak/.disabled 文件当活动配置；遇无法解析结构保守拒绝。导入后 TLS 仍必须验证证书，不把旧的不匹配证书带入新配置。
- [ ] 执行同包测试，通过后提交本任务文件。

### Task 4: 远端事务和验收

**Files:** 新增 transaction.go、probe.go 及测试、cmd/domainctl/main.go。

**Interfaces:**

```go
type Runner interface { Run(context.Context, string, ...string) error }
type ProbeResult struct { HTTP, HTTPS, WebSocket string }
func Apply(ctx context.Context, root string, req Request, runner Runner) error
func Probe(ctx context.Context, address string, domain Domain) (ProbeResult, error)
```

`root` 是测试文件系统根，生产为 `/`。生产命令路径固定；测试 Runner 记录参数、模拟指定步骤失败，不通过环境变量放开任意远程目标。

- [ ] 写故障注入用例：nginx -t、reload、服务重启、探测、回滚 reload 分别失败；验证恢复内容、模式、属主和原先不存在的文件。记录命令事件，断言只改域名不重启，来源变化只重启 api/realtime。
- [ ] 写 replace 顺序测试：必须出现“新域名探测成功”后才允许旧配置移除；第二阶段失败恢复整体旧状态。
- [ ] 执行同包测试观察失败；实现 Linux 文件锁，共享 `/run/lock/block-beast-deploy.lock`，持锁覆盖读状态到验收结束。通过平台文件分离保持 macOS 本地可测试，不添加依赖。
- [ ] 状态放 `/etc/block-beast/domains/state.json`；证书和快照目录 0700，私钥0600。应用来源放独立 `/etc/block-beast/managed-origins.json`，root 写入、blockbeast 组只读，避免应用穿过私钥目录。每次使用版本化目录及原子 rename。
- [ ] 实现磁盘阶段日志 prepared/applied/verified/committed。每阶段 fsync；中断后在下一次操作前恢复未完成事务，恢复失败退出并保留材料。dry-run/list 不创建状态、日志或锁文件。
- [ ] 候选 Nginx 主配置引用受管暂存文件，并保留原非受管 include；候选检查通过后再发布文件并检查实际配置。受管更改不能覆盖外部并发修改，应用前核对快照摘要。
- [ ] Probe 固定连接本次授权目标 IP，设置 Host/SNI，拒绝代理环境和重定向，HTTP 及 TLS 检查 API healthz/readyz 状态和响应。每步 5 秒、最多 3 次、退避 1 秒；TLS 正常验证系统信任链。
- [ ] `/v1/ws` 无会话返回401只记录 route-only；可选测试会话从权限0600文件读取，禁止放命令参数，成功握手检查 hello，禁止日志记录 token。用现有会话测试 fixture 覆盖真实 Origin 拒绝及有效握手。
- [ ] HTTP-only 输出 HTTPS=not-configured。dry-run 本地校验证书，只读远端检查配置，不能写临时私钥；只读检查做不到的验收项标记 not-run。
- [ ] CLI 使用 `json.Decoder.DisallowUnknownFields` 读取请求，校验 operation；不拼接 shell 命令。执行成功返回0，失败返回非0，清楚区分原操作与回滚失败。
- [ ] 执行 `go test ./internal/platform/domainops ./cmd/domainctl`，通过后提交本任务文件。

### Task 5: 本地入口与部署集成

**Files:** 新增 scripts/domains.sh、scripts/deploy-target.sh、scripts/domains_test.go；修改 deploy.sh、deploy-baota.sh、deploy-baota-remote.sh、deploy_baota_test.go；新增 docs/domain-management.md，更新 README.md、docs/deployment.md。

- [ ] 用替身 ssh/scp/go 和临时文件写命令测试：非法环境在任何网络操作前失败；域名注入拒绝；路径带空格安全传输；证书成对；dry-run 不传私钥；无证书新域名请求只含 HTTP 配置。
- [ ] 测试部署覆盖环境文件后 `MANAGED_ORIGINS_FILE` 仍被保留；不存在受管文件时沿用旧行为。用部署 fixture 覆盖共用锁及失败恢复，不触碰真实服务器。
- [ ] 执行 `go test ./scripts` 观察新测试失败。
- [ ] 把 deploy.sh 现有固定目标 case 提取到 deploy-target.sh；两个入口 source 后调用，拒绝第三个环境及目标覆盖，不修改既有 staging/production 语义。
- [ ] 正常操作构建 Linux domainctl，使用随机、安全远端暂存目录；请求用 JSON 文件传输，不将域名、路径或 PEM 拼入远端 shell。本地路径使用数组传给命令，远端仅使用固定路径和校验后的随机目录名。
- [ ] dry-run 使用已安装 domainctl 只读子命令，执行器尚未安装时明确只完成本地预检，不谎称完整检查；安装通过正常发布完成。
- [ ] deploy-baota.sh 打包 domainctl。远端发布在任何状态变更前获取同一锁；存在受管来源时，向候选环境文件写入唯一正确的 MANAGED_ORIGINS_FILE，保持原始权限。应用新配置前确认 JSON 可读有效。
- [ ] 修正与新增受管路径直接相关的发布回滚：健康检查成功后才设置成功标记，失败恢复旧配置/链接并确保旧进程重新启动；明确数据库迁移不自动撤销。
- [ ] 写用户文档，列出 add/replace/remove/certificate/import/list/dry-run、复用远端证书路径参数、来源参数、测试会话文件、HTTP-only 边界、回滚、独立来源文件和首次部署要求。保持两条既有部署命令不变。
- [ ] 执行格式化、相关测试和全量测试：

```sh
gofmt -w internal/platform/domainops internal/config/origins.go internal/config/origins_test.go internal/config/config.go cmd/domainctl cmd/api/main.go cmd/realtime/main.go scripts/domains_test.go scripts/deploy_baota_test.go
bash -n scripts/domains.sh scripts/deploy-target.sh scripts/deploy.sh scripts/deploy-baota.sh scripts/deploy-baota-remote.sh
go test ./...
git diff --check
```

- [ ] 审查完整变更，核对没有前端、数据库迁移、DNS 或私钥入库；仅提交本次实现文件，不顺带提交其他修改。

## 环境验收与交付门槛

- [ ] 本地测试通过后，用用户指定的测试域名在 staging 验收新增 HTTP、可选 HTTPS、重复执行、替换与移除；没有真实域名/证书时明确记录未执行线上验收，不擅自替换现有站点。
- [ ] 第一次引入独立 Origin 加载必须先部署支持它的 API/Realtime，之后再运行带 --allow-origin 的操作；工具检查已部署能力标记，旧版本拒绝该操作。
- [ ] 真实证书由运行时提供；不为测试索取或提交生产私钥。生产服务器不因完成本地实现而自动发布。
- [ ] 最终报告区分已完成本地测试、已完成服务器路由检查和未完成公网验证，并给出可复制的运行命令。
