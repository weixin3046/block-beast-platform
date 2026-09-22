#!/usr/bin/env bash

# 在宝塔服务器上执行：停止应用、补跑迁移、原子切换发布目录并恢复四个进程。
set -Eeuo pipefail
umask 077

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

# 与域名管理共用锁；先完成发布包检查，再接触运行状态。
LOCK_FILE="${BLOCK_BEAST_LOCK_FILE:-/run/lock/block-beast-deploy.lock}"
MANAGED_ORIGINS_PATH="${MANAGED_ORIGINS_PATH:-/etc/block-beast/managed-origins.json}"
DOMAIN_PENDING_FILE="${DOMAIN_PENDING_FILE:-/etc/block-beast/domains/pending.json}"
mkdir -p "$(dirname "$LOCK_FILE")"
exec 9>"$LOCK_FILE"
flock -n 9 || { echo "部署或域名操作正在进行" >&2; exit 1; }
[ ! -e "$DOMAIN_PENDING_FILE" ] || { echo "存在未完成域名事务，请先通过域名工具恢复" >&2; exit 1; }
if [ -f "${MANAGED_ORIGINS_PATH}" ]; then
  [ -x "${RELEASE_DIR}/bin/domainctl" ] || { echo "发布包不支持独立白名单" >&2; exit 1; }
  "${RELEASE_DIR}/bin/domainctl" validate-origins "${MANAGED_ORIGINS_PATH}"
fi

# 保存配置和发布链接；失败时恢复旧配置及旧发布目录。
ENV_BACKUP=""
PREVIOUS_RELEASE="$(readlink -f "${APP_DIR}/current" || true)"
if [ -f "${RELEASE_DIR}/.env.production" ] || [ -f "${MANAGED_ORIGINS_PATH}" ]; then
  ENV_BACKUP="${ENV_FILE}.backup-$(date -u +%Y%m%dT%H%M%SZ)"
  cp -p "${ENV_FILE}" "${ENV_BACKUP}"
fi
started=0
restore_services() {
  if [ "${started}" -eq 0 ]; then
    if [ -n "${ENV_BACKUP}" ]; then
      cp -p "${ENV_BACKUP}" "${ENV_FILE}"
    fi
    if [ -n "${PREVIOUS_RELEASE}" ]; then
      ln -sfn "${PREVIOUS_RELEASE}" "${APP_DIR}/current.next"
      mv -Tf "${APP_DIR}/current.next" "${APP_DIR}/current"
    fi
    # 新进程可能已启动：先停掉，确保恢复后的配置和旧二进制被实际使用。
    "${SUPERVISORCTL}" stop "${SERVICES[@]}" >/dev/null 2>&1 || true
    if ! "${SUPERVISORCTL}" start "${SERVICES[@]}"; then
      echo "恢复旧服务失败，请检查 Supervisor" >&2
    fi
  fi
}
trap restore_services EXIT

"${SUPERVISORCTL}" stop "${SERVICES[@]}"

# 保留原配置属主、权限，供现有启动脚本读取。
if [ -n "${ENV_BACKUP}" ]; then
  cp -p "${ENV_FILE}" "${ENV_FILE}.next"
  if [ -f "${RELEASE_DIR}/.env.production" ]; then
    cat "${RELEASE_DIR}/.env.production" > "${ENV_FILE}.next"
  fi
  if [ -f "${MANAGED_ORIGINS_PATH}" ]; then
    # 只替换此工具拥有的路径配置，保持其余环境变量及文件权限。
    sed '/^[[:space:]]*\(export[[:space:]]\{1,\}\)\{0,1\}MANAGED_ORIGINS_FILE=/d' "${ENV_FILE}.next" > "${ENV_FILE}.managed"
    cat "${ENV_FILE}.managed" > "${ENV_FILE}.next"
    rm -f "${ENV_FILE}.managed"
    printf '\nMANAGED_ORIGINS_FILE=%s\n' "${MANAGED_ORIGINS_PATH}" >> "${ENV_FILE}.next"
  fi
  mv -f "${ENV_FILE}.next" "${ENV_FILE}"
fi
set -a
# shellcheck disable=SC1090
. "${ENV_FILE}"
set +a
export PATH="${PG_BIN_DIR}:${PATH}"

"${RELEASE_DIR}/scripts/migrate.sh"

ln -s "${RELEASE_DIR}" "${APP_DIR}/current.next"
mv -Tf "${APP_DIR}/current.next" "${APP_DIR}/current"

"${SUPERVISORCTL}" start "${SERVICES[@]}"

curl --fail --silent --show-error --max-time 15 http://127.0.0.1:8080/healthz
curl --fail --silent --show-error --max-time 15 http://127.0.0.1:8080/readyz
curl --fail --silent --show-error --max-time 15 http://127.0.0.1:8081/healthz
started=1
echo "宝塔发布完成：${RELEASE_DIR}"
