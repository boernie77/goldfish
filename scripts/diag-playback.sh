#!/usr/bin/env bash
# Sammelt alles, was zur Diagnose eines fehlgeschlagenen Playbacks gebraucht
# wird: Server-Log rund um den Zeitpunkt (inkl. ffmpeg-stderr), das
# Aktivitäts-Protokoll und die Transcode-Entscheidung des Servers.
#
#   ./scripts/diag-playback.sh              # letzte 15 Minuten
#   ./scripts/diag-playback.sh 18:37        # rund um diese Uhrzeit (lokal)
#   ./scripts/diag-playback.sh 18:37 30     # ... mit +/- 30 Minuten Fenster
#
# Voraussetzungen (beides ausserhalb des Repos, nichts davon gehoert hier rein):
#   ~/.config/portainer/credentials.env   PORTAINER_HOST/_USER/_PASS
#   ~/.config/goldfish-linux/settings.json  serverUrl + sessionToken der App
set -euo pipefail

CRED="$HOME/.config/portainer/credentials.env"
[ -r "$CRED" ] || { echo "FEHLER: $CRED fehlt (Portainer-Zugang)." >&2; exit 1; }
set -a; . "$CRED"; set +a

AT="${1:-}"; SPAN_MIN="${2:-15}"
OUT="$(mktemp -d)/goldfish-diag-$(date +%Y%m%d-%H%M%S).txt"

# Zeitfenster bestimmen. Der Container loggt in UTC, die Uhrzeit gibt der
# Nutzer lokal an — deshalb ueber `date` umrechnen statt selbst zu basteln.
if [ -n "$AT" ]; then
  CENTER=$(date -d "today $AT" +%s 2>/dev/null) || { echo "Uhrzeit '$AT' nicht lesbar (erwartet z.B. 18:37)" >&2; exit 1; }
else
  CENTER=$(date +%s)
fi
SINCE=$((CENTER - SPAN_MIN * 60))
UNTIL=$((CENTER + SPAN_MIN * 60))

{
  echo "=== Goldfish Playback-Diagnose ==="
  echo "Fenster: $(date -d @$SINCE '+%F %T') bis $(date -d @$UNTIL '+%F %T') (lokal)"
  echo
} | tee "$OUT"

JWT=$(curl -sS -X POST "$PORTAINER_HOST/api/auth" -H "Content-Type: application/json" \
  -d "{\"username\":\"$PORTAINER_USER\",\"password\":\"$PORTAINER_PASS\"}" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["jwt"])')

CID=$(curl -sS "$PORTAINER_HOST/api/endpoints/3/docker/containers/json" \
  -H "Authorization: Bearer $JWT" \
  | python3 -c 'import sys,json
for c in json.load(sys.stdin):
    if "/videoplayer" in c["Names"]: print(c["Id"]); break')
[ -n "$CID" ] || { echo "FEHLER: Container 'videoplayer' nicht gefunden." >&2; exit 1; }

# Docker rahmt Logs pro Frame mit 8 Byte (Stream-Typ + Laenge) — ohne
# Demultiplexen stehen Steuerzeichen mitten im Text.
curl -sS "$PORTAINER_HOST/api/endpoints/3/docker/containers/$CID/logs?stdout=1&stderr=1&timestamps=1&since=$SINCE&until=$UNTIL" \
  -H "Authorization: Bearer $JWT" --output /tmp/gf-diag-log.bin

python3 - "$OUT" <<'PY'
import sys, re
out = sys.argv[1]
d = open("/tmp/gf-diag-log.bin", "rb").read()
parts, i = [], 0
while i + 8 <= len(d):
    if d[i] in (1, 2) and d[i+1:i+4] == b"\0\0\0":
        n = int.from_bytes(d[i+4:i+8], "big"); parts.append(d[i+8:i+8+n]); i += 8 + n
    else:
        j = d.find(b"\n", i); parts.append(d[i:j+1] if j >= 0 else d[i:]); i = (j+1) if j >= 0 else len(d)
lines = b"".join(parts).decode("utf-8", "replace").splitlines()

# Enrichment-Rauschen raus, alles Wiedergabe-Relevante behalten.
keep = [l for l in lines if not re.search(r"\[enrich\]", l)]
hits = [l for l in keep if re.search(r"transcode|ffmpeg|error|fehler|panic", l, re.I)]

with open(out, "a") as f:
    def w(s=""): print(s); f.write(s + "\n")
    w(f"--- Server-Log: {len(lines)} Zeilen, {len(keep)} ohne Enrichment ---")
    w()
    w("### Wiedergabe-relevante Zeilen (transcode/ffmpeg/error)")
    for l in (hits or ["  (keine)"]):
        w("  " + l[:200])
    w()
    if not any("ffmpeg beendet mit" in l for l in hits):
        w("  Hinweis: keine 'ffmpeg beendet mit'-Zeile. Entweder ist ffmpeg sauber")
        w("  gelaufen, oder der Fix mit dem stderr-Logging ist noch nicht deployed.")
    w()
PY

# Aktivitaets-Protokoll: zeigt play/stop/error je Geraet inkl. Client-Fehlertext.
SET="$HOME/.config/goldfish-linux/settings.json"
if [ -r "$SET" ]; then
  read -r BASE TOKEN < <(python3 -c '
import json,pathlib,sys
d=json.loads(pathlib.Path(sys.argv[1]).read_text())
print(d.get("serverUrl","").rstrip("/"), d.get("sessionToken",""))' "$SET")
  if [ -n "$TOKEN" ]; then
    {
      echo "### Aktivitäts-Protokoll (playback)"
      curl -sS "$BASE/api/admin/activity-log?category=playback&limit=40" \
        -H "Cookie: goldfish_session=$TOKEN" \
        | python3 -c '
import sys, json
d = json.load(sys.stdin)
es = d if isinstance(d, list) else d.get("entries", d.get("items", []))
for e in es[:25]:
    at = str(e.get("at") or "")[:19]
    act = str(e.get("action") or "")
    dev = str(e.get("device") or "-")[:20]
    det = str(e.get("detail") or "")[:110]
    print("  %-19s  %-6s  %-20s  %s" % (at, act, dev, det))
' || echo "  (Protokoll nicht abrufbar — Token abgelaufen? Dann in der App neu anmelden.)"
      echo
    } | tee -a "$OUT"
  fi
fi

echo "=== Fertig ===" | tee -a "$OUT"
echo "Bericht: $OUT" | tee -a "$OUT"
