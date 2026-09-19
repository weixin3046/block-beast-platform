#!/usr/bin/env bash
# 在本地执行：测试与正式环境统一使用宝塔/Supervisor 发布链路。
set -Eeuo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
case "${1:-}" in
  staging) target=root@120.26.41.254 ;;
  production) target=root@121.43.230.83 ;;
  *) echo "用法: ./scripts/deploy.sh staging|production" >&2; exit 2 ;;
esac
[ "$#" -eq 1 ] || { echo "只接受一个环境参数" >&2; exit 2; }
environment="$1"
file=".env.${environment}"
[ -f "$file" ] || { echo "缺少配置: $file" >&2; exit 1; }
grep -qx "APP_ENV=${environment}" "$file" || { echo "APP_ENV 与所选环境不一致" >&2; exit 1; }
# 两边均在本地先执行测试，失败时不上传。
go test ./...
DEPLOY_HOST="$target" APP_DIR=/opt/block-beast DEPLOY_ENV_FILE="$PWD/$file" SKIP_TEST=1 ./scripts/deploy-baota.sh
