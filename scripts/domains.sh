#!/usr/bin/env bash
# Run locally. All domain operations use the same fixed targets as deploy.sh.
set -Eeuo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
. ./scripts/deploy-target.sh
target="$(deploy_target "${1:-}")"
shift
[ "$#" -ge 1 ] || { echo "用法: domains.sh staging|production add|replace|remove|certificate|import|list ..." >&2; exit 2; }
work="$(mktemp -d)"
remote="/tmp/block-beast-domain-$(openssl rand -hex 16)"
uploaded=0
cleanup() {
  rm -rf -- "$work"
  if [ "$uploaded" = 1 ]; then
    ssh -o BatchMode=yes -o ConnectTimeout=10 "$target" "rm -rf -- '$remote'" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT
# Validate all user inputs before any network access. No shell eval or env sourcing.
go build -trimpath -o "$work/domainctl" ./cmd/domainctl
mode="$("$work/domainctl" prepare "$work" "$remote" "$@")"
if [ "$mode" = dry-run ] || [ "$mode" = list ]; then
  ssh -o BatchMode=yes -o ConnectTimeout=10 "$target" \
    '/opt/block-beast/current/bin/domainctl check' < "$work/request.json"
  exit
fi
ssh -o BatchMode=yes -o ConnectTimeout=10 "$target" \
  'test "$(/opt/block-beast/current/bin/domainctl capabilities)" = 1'
ssh -o BatchMode=yes -o ConnectTimeout=10 "$target" "umask 077; mkdir '$remote'"
uploaded=1
for name in request.json fullchain.pem privkey.pem session.txt; do
  if [ -f "$work/$name" ]; then
    scp -q -o BatchMode=yes -o ConnectTimeout=10 "$work/$name" "$target:$remote/$name"
  fi
done
ssh -o BatchMode=yes -o ConnectTimeout=10 "$target" \
  "/opt/block-beast/current/bin/domainctl apply '$remote/request.json'"
