#!/usr/bin/env bash

# 为本地 PostgreSQL 数据卷补跑尚未执行的迁移。
# 旧版项目仅依赖 docker-entrypoint-initdb.d，因此已有数据卷没有迁移记录；
# 本脚本会先根据每个历史迁移的标志性表/字段补登记，再执行真正缺失的迁移。

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${ROOT_DIR}"

psql_command() {
  docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U blockbeast -d blockbeast "$@"
}

psql_command >/dev/null <<'SQL'
CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
SQL

migration_is_present() {
  case "$1" in
    0001) psql_command -Atqc "SELECT to_regclass('public.users') IS NOT NULL" ;;
    0002) psql_command -Atqc "SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'outbox_events' AND column_name = 'failed_at')" ;;
    0003) psql_command -Atqc "SELECT to_regclass('public.checkin_records') IS NOT NULL" ;;
    0004) psql_command -Atqc "SELECT to_regclass('public.bet_task_configs') IS NOT NULL" ;;
    0005) psql_command -Atqc "SELECT to_regclass('public.provider_webhook_events') IS NOT NULL" ;;
    0006) psql_command -Atqc "SELECT to_regclass('public.provider_supported_assets') IS NOT NULL" ;;
    0007) psql_command -Atqc "SELECT to_regclass('public.point_withdrawals') IS NOT NULL" ;;
    0008) psql_command -Atqc "SELECT to_regclass('public.agent_commission_rates') IS NOT NULL" ;;
    0009) psql_command -Atqc "SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'commission_entries' AND column_name = 'currency')" ;;
    0010) psql_command -Atqc "SELECT to_regclass('public.commission_adjustments') IS NOT NULL" ;;
    0011) psql_command -Atqc "SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'withdrawals' AND column_name = 'provider_chain_token_id')" ;;
    0012) psql_command -Atqc "SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'provider_supported_assets' AND column_name = 'support_deposit')" ;;
    0013) psql_command -Atqc "SELECT (SELECT count(DISTINCT code) FROM roles WHERE code IN ('player', 'operator', 'admin')) = 3" ;;
    0014) psql_command -Atqc "SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='sessions' AND column_name='audience')" ;;
    *) printf 'f\n' ;;
  esac
}

for migration_path in migrations/*.sql; do
  migration_file="$(basename "${migration_path}")"
  version="${migration_file%%_*}"
  recorded="$(psql_command -Atqc "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = '${version}')")"
  if [ "${recorded}" = "t" ]; then
    continue
  fi

  if [ "$(migration_is_present "${version}")" = "t" ]; then
    echo "      登记已有迁移 ${migration_file}"
  else
    echo "      执行迁移 ${migration_file}"
    psql_command < "${migration_path}" >/dev/null
  fi

  psql_command -c "INSERT INTO schema_migrations (version) VALUES ('${version}') ON CONFLICT DO NOTHING" >/dev/null
done
