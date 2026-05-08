#!/usr/bin/env bash
# Goldfish Live-Redeploy via Portainer-API.
#
# Was es macht:
#   1. Tar des Source-Trees
#   2. Build via Portainer-Docker-Proxy
#   3. Stack-Redeploy von Stack 37 — UND erhält das aktuelle Env-Array
#      (sonst löscht Portainer alle OIDC-Vars usw. → SSO 503)
#
# Nutzung:
#   PORTAINER_PASS='<pwd>' ./scripts/redeploy.sh
#   (oder $JWT vorher gesetzt)
#
# Voraussetzung:
#   - Erreichbar auf <UNRAID-LAN-IP>:9000 (Tailscale oder LAN)
#   - python3 vorhanden für JSON-Verarbeitung

set -euo pipefail

PORTAINER_HOST="${PORTAINER_HOST:-http://<UNRAID-LAN-IP>:9000}"
ENDPOINT_ID="${ENDPOINT_ID:-3}"
STACK_ID="${STACK_ID:-37}"
IMAGE_TAG="${IMAGE_TAG:-simple-videoplayer:latest}"
SRC_DIR="$(cd "$(dirname "$0")/.." && pwd)"

if [[ -z "${JWT:-}" ]]; then
  if [[ -z "${PORTAINER_PASS:-}" ]]; then
    echo "ERROR: weder \$JWT noch \$PORTAINER_PASS gesetzt" >&2
    exit 1
  fi
  echo "→ Auth bei Portainer..."
  JWT=$(curl -sS -X POST "$PORTAINER_HOST/api/auth" \
    -H "Content-Type: application/json" \
    -d "{\"Username\":\"admin\",\"Password\":\"$PORTAINER_PASS\"}" \
    | python3 -c 'import sys,json;print(json.load(sys.stdin)["jwt"])')
fi

echo "→ Tar Source ($SRC_DIR)..."
TARFILE="$(mktemp -t videoplayer-src.XXXXXX.tar)"
trap "rm -f $TARFILE /tmp/redeploy-build.log /tmp/redeploy-stack.json /tmp/redeploy-result.json" EXIT
COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata \
  --exclude='.DS_Store' --exclude='config' --exclude='cache' --exclude='.git' \
  -cf "$TARFILE" -C "$SRC_DIR" .

echo "→ Build (kann ~1 min dauern)..."
curl -sS --max-time 600 -X POST \
  "$PORTAINER_HOST/api/endpoints/$ENDPOINT_ID/docker/build?t=$IMAGE_TAG&rm=true&forcerm=true" \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/x-tar" \
  --data-binary "@$TARFILE" \
  -o /tmp/redeploy-build.log
LAST=$(tail -n 1 /tmp/redeploy-build.log)
if ! echo "$LAST" | grep -q "Successfully tagged"; then
  echo "BUILD FAILED — letzte Zeile: $LAST" >&2
  tail -n 20 /tmp/redeploy-build.log >&2
  exit 1
fi
echo "  $LAST"

echo "→ Stack-Daten lesen (für env-Array)..."
STACK_JSON=$(curl -sS "$PORTAINER_HOST/api/stacks/$STACK_ID?endpointId=$ENDPOINT_ID" \
  -H "Authorization: Bearer $JWT")
COMPOSE_FILE=$(curl -sS "$PORTAINER_HOST/api/stacks/$STACK_ID/file" \
  -H "Authorization: Bearer $JWT" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["StackFileContent"])')

echo "→ Stack-Update zusammenstellen (env BLEIBT erhalten)..."
EXISTING_ENV_COUNT=$(echo "$STACK_JSON" | python3 -c 'import sys,json;print(len(json.load(sys.stdin).get("Env",[])))')
echo "  $EXISTING_ENV_COUNT Env-Vars werden mitgesendet (NICHT 0 erwartet — sonst OIDC weg!)"
if [[ "$EXISTING_ENV_COUNT" -lt 4 ]]; then
  echo "  ⚠ WARNUNG: weniger als 4 Env-Vars im Stack — OIDC braucht 4 (ISSUER_URL, CLIENT_ID, CLIENT_SECRET, REDIRECT_URL)" >&2
  echo "  Trotzdem fortfahren? [y/N]"
  read -r ANSWER
  [[ "$ANSWER" != "y" ]] && exit 1
fi

python3 - <<EOF > /tmp/redeploy-stack.json
import json, os
stack = json.loads('''$STACK_JSON''')
body = {
    "stackFileContent": '''$COMPOSE_FILE''',
    "env": stack.get("Env", []),   # 1:1 zurückschreiben — OIDC bleibt
    "prune": False,
    "pullImage": False
}
print(json.dumps(body))
EOF

echo "→ Stack-Redeploy..."
curl -sS -X PUT "$PORTAINER_HOST/api/stacks/$STACK_ID?endpointId=$ENDPOINT_ID" \
  -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  --data-binary @/tmp/redeploy-stack.json \
  -o /tmp/redeploy-result.json

sleep 5
HEALTH=$(curl -sS http://<UNRAID-LAN-IP>:8098/api/health -o /dev/null -w "%{http_code}" || echo "fail")
echo "  /api/health: $HEALTH"

# OIDC-Health gegenchecken
OIDC=$(curl -sS -o /dev/null -w "%{http_code}" -i https://goldfish.<your-domain>/api/auth/oidc/login -L --max-redirs 0 || true)
if [[ "$OIDC" == "302" ]]; then
  echo "  OIDC: ✓ 302 Redirect zu Authentik (SSO live)"
elif [[ "$OIDC" == "503" ]]; then
  echo "  OIDC: ✗ 503 — Env-Vars sind weg, NEU SETZEN" >&2
  exit 1
else
  echo "  OIDC: $OIDC (unerwartet)"
fi
echo "✓ Redeploy fertig."
