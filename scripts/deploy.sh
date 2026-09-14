#!/usr/bin/env bash
# 在本地执行：统一选择测试 Docker 部署或生产宝塔部署。
set -Eeuo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
case "${1:-}" in
  staging) target=root@58.87.64.208 ;;
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
if [ "$environment" = production ]; then
  DEPLOY_HOST="$target" APP_DIR=/opt/block-beast DEPLOY_ENV_FILE="$PWD/$file" SKIP_TEST=1 ./scripts/deploy-baota.sh
else
  ssh "$target" 'mkdir -p /opt/block-beast && if [ -f /opt/block-beast/.env.staging ]; then cp -p /opt/block-beast/.env.staging "/opt/block-beast/.env.staging.backup-$(date -u +%Y%m%dT%H%M%SZ)"; fi'
  # 不上传任何环境文件、开发数据或其他部署方式的发布目录。
  rsync -az --exclude '.git/' --exclude '.env*' --exclude '.DS_Store' \
    --exclude 'data/' --exclude 'releases/' --exclude 'node_modules/' \
    ./ "$target:/opt/block-beast/"
  ssh "$target" 'umask 077; cat > /opt/block-beast/.env.staging' < "$file"
  ssh "$target" 'cd /opt/block-beast && ./scripts/deploy-production.sh .env.staging'
fi
