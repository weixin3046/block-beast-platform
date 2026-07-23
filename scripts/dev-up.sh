#!/usr/bin/env bash

# Block Beast macOS/Linux 本地开发一键启动脚本。
# 启动基础设施容器，并在当前终端后台运行 API、Worker 与 Realtime。

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
SKIP_INFRA=false
SWAGGER=false
SERVICE_PIDS=()
BUILD_DIR=""

usage() {
  cat <<'EOF'
用法：./scripts/dev-up.sh [选项]

选项：
  --skip-infra  跳过 postgres、nats、redis 容器启动
  --swagger     同时启动 Swagger UI（http://localhost:8082）
  -h, --help    显示帮助

按 Ctrl+C 停止 API、Worker 和 Realtime。基础设施容器会继续运行。
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --skip-infra)
      SKIP_INFRA=true
      ;;
    --swagger)
      SWAGGER=true
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "未知参数：$1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少依赖：$1" >&2
    exit 1
  fi
}

cleanup() {
  exit_code=$?
  trap - INT TERM EXIT
  set +e
  if [ "${#SERVICE_PIDS[@]}" -gt 0 ]; then
    echo
    echo "正在停止 API、Worker 和 Realtime..."
    kill "${SERVICE_PIDS[@]}" 2>/dev/null

    # 最多等待 5 秒完成优雅退出，之后强制清理仍存活的服务。
    attempt=0
    while [ "${attempt}" -lt 50 ]; do
      alive=false
      for pid in "${SERVICE_PIDS[@]}"; do
        if kill -0 "${pid}" 2>/dev/null; then
          alive=true
          break
        fi
      done
      if [ "${alive}" = false ]; then
        break
      fi
      attempt=$((attempt + 1))
      sleep 0.1
    done
    for pid in "${SERVICE_PIDS[@]}"; do
      if kill -0 "${pid}" 2>/dev/null; then
        kill -KILL "${pid}" 2>/dev/null
      fi
    done
    wait "${SERVICE_PIDS[@]}" 2>/dev/null
    echo "Go 服务已停止。"
  fi
  if [ -n "${BUILD_DIR}" ] && [ -d "${BUILD_DIR}" ]; then
    rm -rf "${BUILD_DIR}"
  fi
  exit "${exit_code}"
}

trap 'exit 130' INT
trap 'exit 143' TERM
trap cleanup EXIT

require_command go
require_command docker
if ! docker compose version >/dev/null 2>&1; then
  echo "缺少 Docker Compose 插件，请安装或更新 Docker Desktop / Docker Engine。" >&2
  exit 1
fi

cd "${ROOT_DIR}"

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

export APP_ENV="${APP_ENV:-development}"
# .env 中保存的是 Docker 容器之间使用的主机名（postgres/nats/redis）。
# 本脚本在宿主机直接运行 Go 二进制，必须改用宿主机映射地址。
# 如需覆盖，请使用 DEV_* 变量，避免改变 docker compose 使用的配置。
export POSTGRES_DSN="${DEV_POSTGRES_DSN:-postgres://blockbeast:blockbeast@localhost:5433/blockbeast?sslmode=disable}"
export REDIS_ADDRESS="${DEV_REDIS_ADDRESS:-localhost:6379}"
export AUTH_TOKEN_SECRET="${AUTH_TOKEN_SECRET:-dev-only-signing-secret-change-me-in-production-0123456789abcdef}"
export API_ADDRESS="${API_ADDRESS:-:8080}"
export REALTIME_ADDRESS="${REALTIME_ADDRESS:-:8081}"
export ACCESS_TOKEN_TTL="${ACCESS_TOKEN_TTL:-15m}"
export REFRESH_TOKEN_TTL="${REFRESH_TOKEN_TTL:-720h}"
export CHAIN_WEBHOOK_ALLOWED_SKEW="${CHAIN_WEBHOOK_ALLOWED_SKEW:-5m}"
export WORKER_POLL_INTERVAL="${WORKER_POLL_INTERVAL:-5s}"
export NATS_URL="${DEV_NATS_URL:-nats://localhost:4222}"

echo "[1/5] 检查基础设施..."
if [ "${SKIP_INFRA}" = false ]; then
  docker compose up -d postgres nats redis
  echo "      等待 PostgreSQL 就绪..."
  READY=false
  ATTEMPT=0
  while [ "${ATTEMPT}" -lt 45 ]; do
    if docker compose exec -T postgres pg_isready -U blockbeast -d blockbeast >/dev/null 2>&1; then
      READY=true
      break
    fi
    ATTEMPT=$((ATTEMPT + 1))
    sleep 2
  done
  if [ "${READY}" != true ]; then
    echo "PostgreSQL 未在 90 秒内就绪，请运行 docker compose logs postgres 排查。" >&2
    exit 1
  fi
else
  echo "      已跳过（--skip-infra）"
fi

if [ "${SWAGGER}" = true ]; then
  echo "[2/5] 启动 Swagger UI..."
  SWAGGER_STATE="$(docker inspect -f '{{.State.Running}}' blockbeast-swagger-ui 2>/dev/null || true)"
  if [ "${SWAGGER_STATE}" = "true" ]; then
    echo "      Swagger UI 已在运行"
  elif [ -n "${SWAGGER_STATE}" ]; then
    docker start blockbeast-swagger-ui >/dev/null
  else
    docker run -d --name blockbeast-swagger-ui -p 8082:8080 \
      -e SWAGGER_JSON=/docs/openapi.yaml \
      -v "${ROOT_DIR}/docs:/docs:ro" swaggerapi/swagger-ui >/dev/null
  fi
else
  echo "[2/5] 跳过 Swagger UI（使用 --swagger 启用）"
fi

echo "[3/5] 编译 API、Worker 与 Realtime..."
BUILD_DIR="$(mktemp -d "${TMPDIR:-/tmp}/block-beast-dev.XXXXXX")"
go build -o "${BUILD_DIR}/api" ./cmd/api
go build -o "${BUILD_DIR}/worker" ./cmd/worker
go build -o "${BUILD_DIR}/realtime" ./cmd/realtime

echo "[4/5] 启动 API、Worker 与 Realtime..."
"${BUILD_DIR}/api" &
API_PID=$!
SERVICE_PIDS+=("${API_PID}")

"${BUILD_DIR}/worker" &
WORKER_PID=$!
SERVICE_PIDS+=("${WORKER_PID}")

"${BUILD_DIR}/realtime" &
REALTIME_PID=$!
SERVICE_PIDS+=("${REALTIME_PID}")

echo "[5/5] 本地开发环境已启动"
echo "  API       -> http://localhost:8080"
echo "  Realtime  -> ws://localhost:8081/v1/ws"
echo "  PostgreSQL-> localhost:5433（容器内 5432）"
echo "  NATS      -> localhost:4222（监控 http://localhost:8222）"
echo "  Redis     -> localhost:6379"
if [ "${SWAGGER}" = true ]; then
  echo "  Swagger   -> http://localhost:8082"
fi
echo
echo "按 Ctrl+C 停止 Go 服务；基础设施可用 docker compose stop postgres nats redis 停止。"

wait
