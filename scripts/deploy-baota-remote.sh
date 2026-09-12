#!/usr/bin/env bash

# 在宝塔服务器上执行：停止应用、补跑迁移、原子切换发布目录并恢复四个进程。
set -Eeuo pipefail

RELEASE_DIR="${1:?用法: deploy-baota-remote.sh <release-dir>}"
APP_DIR="${APP_DIR:-/opt/block-beast}"
ENV_FILE="${ENV_FILE:-/etc/block-beast/block-beast.env}"
SUPERVISORCTL="${SUPERVISORCTL:-/www/server/panel/pyenv/bin/supervisorctl}"
PG_BIN_DIR="${PG_BIN_DIR:-/www/server/pgsql/bin}"
SERVICES=(block-beast-api block-beast-worker block-beast-realtime block-beast-lulu-worker)

for binary in api worker realtime lulu-worker bootstrap-admin; do
  [ -f "${RELEASE_DIR}/bin/${binary}" ] && [ -x "${RELEASE_DIR}/bin/${binary}" ] || {
    echo "发布目录缺少可执行文件 bin/${binary}: ${RELEASE_DIR}" >&2
    exit 1
  }
done
[ -d "${RELEASE_DIR}/migrations" ] || { echo "发布目录缺少 migrations" >&2; exit 1; }
[ -f "${RELEASE_DIR}/scripts/migrate.sh" ] && [ -x "${RELEASE_DIR}/scripts/migrate.sh" ] || {
  echo "发布目录缺少可执行迁移脚本 scripts/migrate.sh" >&2
  exit 1
}
[ -r "${ENV_FILE}" ] || { echo "无法读取环境文件: ${ENV_FILE}" >&2; exit 1; }
[ -x "${SUPERVISORCTL}" ] || { echo "无法执行 Supervisor: ${SUPERVISORCTL}" >&2; exit 1; }

started=0
restore_services() {
  if [ "${started}" -eq 0 ]; then
    "${SUPERVISORCTL}" start "${SERVICES[@]}" >/dev/null 2>&1 || true
  fi
}
trap restore_services EXIT

"${SUPERVISORCTL}" stop "${SERVICES[@]}"

set -a
# shellcheck disable=SC1090
. "${ENV_FILE}"
set +a
export PATH="${PG_BIN_DIR}:${PATH}"

"${RELEASE_DIR}/scripts/migrate.sh"

ln -s "${RELEASE_DIR}" "${APP_DIR}/current.next"
mv -Tf "${APP_DIR}/current.next" "${APP_DIR}/current"

"${SUPERVISORCTL}" start "${SERVICES[@]}"
started=1

curl --fail --silent --show-error --max-time 15 http://127.0.0.1:8080/healthz
curl --fail --silent --show-error --max-time 15 http://127.0.0.1:8080/readyz
curl --fail --silent --show-error --max-time 15 http://127.0.0.1:8081/healthz
echo "宝塔发布完成：${RELEASE_DIR}"
