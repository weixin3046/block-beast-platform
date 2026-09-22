#!/usr/bin/env bash
# 在本地执行：测试与正式环境统一使用宝塔/Supervisor 发布链路。
set -Eeuo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
. ./scripts/deploy-target.sh
target="$(deploy_target "${1:-}")"
[ "$#" -eq 1 ] || { echo "只接受一个环境参数" >&2; exit 2; }
environment="$1"
file=".env.${environment}"
[ -f "$file" ] || { echo "缺少配置: $file" >&2; exit 1; }
grep -qx "APP_ENV=${environment}" "$file" || { echo "APP_ENV 与所选环境不一致" >&2; exit 1; }
# 两边均在本地先执行测试，失败时不上传。
go test ./...
DEPLOY_HOST="$target" APP_DIR=/opt/block-beast DEPLOY_ENV_FILE="$PWD/$file" SKIP_TEST=1 ./scripts/deploy-baota.sh
