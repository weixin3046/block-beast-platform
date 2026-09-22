#!/usr/bin/env bash
# Shared fixed environment mapping; do not accept arbitrary remote hosts.
deploy_target() {
case "${1:-}" in
  staging) printf '%s\n' root@120.26.41.254 ;;
  production) printf '%s\n' root@121.43.230.83 ;;
  *) echo "环境只支持 staging|production" >&2; exit 2 ;;
esac
}
