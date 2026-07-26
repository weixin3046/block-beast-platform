#!/usr/bin/env bash

# 兼容原有本地开发命令；实际迁移逻辑统一维护在 migrate.sh。

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
unset POSTGRES_DSN
exec "${SCRIPT_DIR}/migrate.sh"
