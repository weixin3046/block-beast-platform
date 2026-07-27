#!/usr/bin/env bash

# 构建镜像、启动基础设施、执行增量迁移并滚动更新三个应用进程。

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
ENV_FILE="${1:-.env.production}"

cd "${ROOT_DIR}"

if [ ! -f "${ENV_FILE}" ]; then
  echo "生产环境文件不存在：${ENV_FILE}" >&2
  echo "请先复制 .env.production.example 并填入真实配置。" >&2
  exit 1
fi
if ! docker compose version >/dev/null 2>&1; then
  echo "需要 Docker Compose v2。" >&2
  exit 1
fi

COMPOSE=(docker compose --env-file "${ENV_FILE}" -f compose.production.yaml)
export APP_ENV_FILE="${ENV_FILE}"

"${COMPOSE[@]}" config --quiet
"${COMPOSE[@]}" build api worker realtime
"${COMPOSE[@]}" up -d --wait postgres nats
"${COMPOSE[@]}" run --rm migrate
"${COMPOSE[@]}" run --rm uploads-init
"${COMPOSE[@]}" up -d --no-deps api worker realtime
"${COMPOSE[@]}" ps
