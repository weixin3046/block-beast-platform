#!/usr/bin/env bash

# 在开发机执行。目标主机必须通过 DEPLOY_HOST 显式提供，避免把生产地址写入仓库。
set -Eeuo pipefail

: "${DEPLOY_HOST:?请设置 DEPLOY_HOST，例如 root@your-server}"
APP_DIR="${APP_DIR:-/opt/block-beast}"
VERSION="${VERSION:-$(date -u +%Y%m%dT%H%M%SZ)-$(git rev-parse --short HEAD)}"
REMOTE_RELEASE="${APP_DIR}/releases/${VERSION}"
WORK_DIR="$(mktemp -d)"
ARCHIVE="${WORK_DIR}/block-beast-${VERSION}.tar.gz"

cleanup() { rm -rf "${WORK_DIR}"; }
trap cleanup EXIT

if [ "${SKIP_TEST:-0}" != "1" ]; then
  go test ./...
fi

mkdir -p "${WORK_DIR}/release/bin" "${WORK_DIR}/release/scripts" "${WORK_DIR}/release/migrations"
for binary in api worker realtime lulu-worker bootstrap-admin; do
  case "${binary}" in
    api) package=./cmd/api ;;
    worker) package=./cmd/worker ;;
    realtime) package=./cmd/realtime ;;
    lulu-worker) package=./cmd/lulu-worker ;;
    bootstrap-admin) package=./cmd/bootstrap-admin ;;
  esac
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "${WORK_DIR}/release/bin/${binary}" "${package}"
done

cp migrations/*.sql "${WORK_DIR}/release/migrations/"
cp scripts/migrate.sh scripts/deploy-baota-remote.sh "${WORK_DIR}/release/scripts/"
chmod 0755 "${WORK_DIR}/release/scripts/"*.sh
COPYFILE_DISABLE=1 tar -C "${WORK_DIR}/release" -czf "${ARCHIVE}" .

ssh "${DEPLOY_HOST}" "mkdir -p '${REMOTE_RELEASE}'"
scp "${ARCHIVE}" "${DEPLOY_HOST}:/tmp/block-beast-${VERSION}.tar.gz"
ssh "${DEPLOY_HOST}" "tar -xzf '/tmp/block-beast-${VERSION}.tar.gz' -C '${REMOTE_RELEASE}' && rm -f '/tmp/block-beast-${VERSION}.tar.gz' && bash '${REMOTE_RELEASE}/scripts/deploy-baota-remote.sh' '${REMOTE_RELEASE}'"
