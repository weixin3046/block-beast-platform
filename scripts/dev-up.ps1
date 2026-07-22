#requires -Version 5.1
<#
.SYNOPSIS
    BlockBeast 本地开发一键启动脚本（基础设施 + API + Worker）。

.DESCRIPTION
    1. 启动基础设施容器：postgres / nats / redis
    2. 等待 postgres 健康检查通过
    3. 在独立新窗口中启动 API (:8080) 和 Worker

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File scripts/dev-up.ps1

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File scripts/dev-up.ps1 -Swagger
#>
[CmdletBinding()]
param(
    # 内部使用：子窗口模式，请勿手动指定
    [ValidateSet('all', 'api', 'worker')]
    [string]$Role = 'all',

    # 跳过容器启动（基础设施已在运行时使用）
    [switch]$SkipInfra,

    # 同时启动 Swagger UI 容器 (:8082)
    [switch]$Swagger
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

# ---------- 子窗口模式：在当前窗口运行单个服务 ----------
if ($Role -ne 'all') {
    Set-Location $root
    $env:APP_ENV = 'development'
    $env:POSTGRES_DSN = 'postgres://blockbeast:blockbeast@localhost:5433/blockbeast?sslmode=disable'
    $env:AUTH_TOKEN_SECRET = 'dev-only-signing-secret-change-me-in-production-0123456789abcdef'

    if ($Role -eq 'api') {
        $env:API_ADDRESS = ':8080'
        $env:ACCESS_TOKEN_TTL = '15m'
        $env:CHAIN_WEBHOOK_SECRET = 'dev-only-chain-webhook-secret-change-me-in-production'
        $env:CHAIN_WEBHOOK_ALLOWED_SKEW = '5m'
        Write-Host 'API 启动中 -> http://localhost:8080' -ForegroundColor Green
        go run ./cmd/api
    }
    else {
        $env:WORKER_POLL_INTERVAL = '5s'
        $env:NATS_URL = 'nats://localhost:4222'
        Write-Host 'Worker 启动中...' -ForegroundColor Green
        go run ./cmd/worker
    }
    exit $LASTEXITCODE
}

# ---------- 编排模式 ----------
Set-Location $root

Write-Host '[1/3] 启动基础设施容器 (postgres / nats / redis)...' -ForegroundColor Cyan
if (-not $SkipInfra) {
    docker compose up -d postgres nats redis
    if ($LASTEXITCODE -ne 0) { throw 'docker compose 启动失败，请确认 Docker Desktop 已运行' }

    Write-Host '      等待 postgres 就绪...' -ForegroundColor Cyan
    $ready = $false
    $deadline = (Get-Date).AddSeconds(90)
    while ((Get-Date) -lt $deadline) {
        docker compose exec -T postgres pg_isready -U blockbeast -d blockbeast *>$null
        if ($LASTEXITCODE -eq 0) { $ready = $true; break }
        Start-Sleep -Seconds 2
    }
    if (-not $ready) { throw 'postgres 未在 90 秒内就绪，请运行 docker compose logs postgres 排查' }
}
else {
    Write-Host '      已跳过 (-SkipInfra)' -ForegroundColor DarkGray
}

Write-Host '[2/3] 在新窗口启动 API (:8080)...' -ForegroundColor Cyan
Start-Process powershell -WorkingDirectory $root -ArgumentList @(
    '-NoExit', '-ExecutionPolicy', 'Bypass', '-File', "`"$PSCommandPath`"", '-Role', 'api'
)

Write-Host '[3/3] 在新窗口启动 Worker...' -ForegroundColor Cyan
Start-Process powershell -WorkingDirectory $root -ArgumentList @(
    '-NoExit', '-ExecutionPolicy', 'Bypass', '-File', "`"$PSCommandPath`"", '-Role', 'worker'
)

if ($Swagger) {
    $existing = docker ps -a --filter 'name=^/blockbeast-swagger-ui$' --format '{{.State}}'
    if ($existing -eq 'running') {
        Write-Host 'Swagger UI 已在运行 -> http://localhost:8082' -ForegroundColor DarkGray
    }
    elseif ($existing) {
        docker start blockbeast-swagger-ui | Out-Null
        Write-Host 'Swagger UI 已启动 -> http://localhost:8082' -ForegroundColor Green
    }
    else {
        docker run -d --name blockbeast-swagger-ui -p 8082:8080 `
            -e SWAGGER_JSON=/docs/openapi.yaml `
            -v "${root}/docs:/docs:ro" swaggerapi/swagger-ui | Out-Null
        Write-Host 'Swagger UI 已启动 -> http://localhost:8082' -ForegroundColor Green
    }
}

Write-Host ''
Write-Host '全部启动完成：' -ForegroundColor Green
Write-Host '  API      -> http://localhost:8080'
Write-Host '  Postgres -> localhost:5432 (容器内 5432)'
Write-Host '  NATS     -> localhost:4222 (监控 http://localhost:8222)'
Write-Host '  Redis    -> localhost:6379'
Write-Host ''
Write-Host '停止方式：关闭 API / Worker 窗口，然后执行 docker compose stop postgres nats redis' -ForegroundColor DarkGray
