#!/usr/bin/env bash
# Replace the template placeholders across the repo.
# Usage: ./scripts/init.sh <app-name> <domain> <container-port> [namespace] [owner]
set -euo pipefail

APP="${1:?usage: init.sh <app-name> <domain> <port> [namespace] [owner]}"
DOMAIN="${2:?missing domain}"
PORT="${3:?missing container port}"
NAMESPACE="${4:-apps}"
prawnfarmerdev="${5:-}"

if [ -n "$prawnfarmerdev" ]; then
  IMAGE="ghcr.io/${prawnfarmerdev}/${APP}"
else
  IMAGE="ghcr.io/prawnfarmerdev/${APP}"
fi

cd "$(dirname "$0")/.."

# Files to scan (skip .git)
mapfile -t FILES < <(grep -rIl -e 'status-api' -e 'apps' -e 'status.ciceley.cc' -e '8080' -e 'prawnfarmerdev' . --exclude-dir=.git 2>/dev/null || true)

for f in "${FILES[@]}"; do
  sed -i \
    -e "s/apps/${NAMESPACE}/g" \
    -e "s/status-api/${APP}/g" \
    -e "s/status.ciceley.cc/${DOMAIN}/g" \
    -e "s/8080/${PORT}/g" \
    -e "s#ghcr.io/prawnfarmerdev/${APP}#${IMAGE}#g" \
    -e "s/prawnfarmerdev/${prawnfarmerdev:-prawnfarmerdev}/g" \
    "$f"
done

echo "Initialized '${APP}'"
echo "  domain:    ${DOMAIN}"
echo "  namespace: ${NAMESPACE}"
echo "  port:      ${PORT}"
echo
echo "Next:"
echo "  1. Replace app/ + Dockerfile with your real app."
echo "  2. git add -A && git commit -m 'init ${APP}'"
echo "  3. push, then add applications/${APP}.yaml in bakuran-gitops."
