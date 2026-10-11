#!/usr/bin/env bash
# Replace the template placeholders across the repo.
# Usage: ./scripts/init.sh <app-name> <domain> <container-port> [namespace] [owner]
set -euo pipefail

APP="${1:?usage: init.sh <app-name> <domain> <port> [namespace] [owner]}"
DOMAIN="${2:?missing domain}"
PORT="${3:?missing container port}"
NAMESPACE="${4:-apps}"
OWNER="${5:-}"

if [ -n "$OWNER" ]; then
  IMAGE="ghcr.io/${OWNER}/${APP}"
else
  IMAGE="ghcr.io/OWNER/${APP}"
fi

cd "$(dirname "$0")/.."

# Files to scan (skip .git)
mapfile -t FILES < <(grep -rIl -e 'APPNAME' -e 'APPNAMESPACE' -e 'APP_DOMAIN' -e 'APP_PORT' -e 'OWNER' . --exclude-dir=.git 2>/dev/null || true)

for f in "${FILES[@]}"; do
  sed -i \
    -e "s/APPNAMESPACE/${NAMESPACE}/g" \
    -e "s/APPNAME/${APP}/g" \
    -e "s/APP_DOMAIN/${DOMAIN}/g" \
    -e "s/APP_PORT/${PORT}/g" \
    -e "s#ghcr.io/OWNER/${APP}#${IMAGE}#g" \
    -e "s/OWNER/${OWNER:-OWNER}/g" \
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
