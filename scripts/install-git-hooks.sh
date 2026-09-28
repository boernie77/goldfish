#!/usr/bin/env bash
# Installiert die lokalen Git-Hooks. Hooks sind nicht im Repo eingecheckt
# (.git/hooks/ ist git-intern), daher muss jeder Klon den Hook einmal
# manuell aktivieren:
#
#   ./scripts/install-git-hooks.sh
#
# Der Pre-Commit-Hook ruft scripts/check-frontend.sh auf, sobald JS-
# Dateien im Commit sind, und blockt Commits mit Syntax-Errors.
#
# Der Pre-Push-Hook (User-Vorgabe 2026-09-18, nach einem Datenleck-Verdacht
# durch einen ACL-Query-Bug) lässt bei Go-Änderungen zwingend die
# Nutzertrennungs-Tests laufen (go test ./internal/store/... -run
# 'ACL|FieldParity') — ein Push mit einer roten ACL-/Feld-Paritäts-
# Suite wird geblockt. Umgeht man NUR mit `git push --no-verify`.

set -euo pipefail
cd "$(dirname "$0")/.."

cat > .git/hooks/pre-commit <<'EOF'
#!/usr/bin/env bash
set -e
if git diff --cached --name-only | grep -qE '^internal/webassets/web/.*\.js$'; then
  echo "→ Frontend-JS geaendert, fuehre Syntax-Check aus..."
  ./scripts/check-frontend.sh
fi
EOF
chmod +x .git/hooks/pre-commit

cat > .git/hooks/pre-push <<'EOF'
#!/usr/bin/env bash
# Blockt den Push, wenn die Nutzertrennungs-Tests (ACL, Library-Feld-
# Paritaet zwischen Admin-/Nicht-Admin-Pfad) nicht gruen sind. Nur bei
# geaenderten Go-Dateien im zu pushenden Bereich, damit reine Frontend-/
# Doku-Pushes nicht unnoetig ausgebremst werden.
#
# Prueft AUSSERDEM (User-Vorgabe 2026-09-18, nach einem Vorfall: ein Deploy
# lief mitten in eine laufende Transcode-Wiedergabe hinein) IMMER, unabhaengig
# von geaenderten Dateien, ob auf dem Produktions-Host gerade eine aktive
# ffmpeg-Transcode-Wiedergabe laeuft — Auto-Deploy (goldfish-ci) startet den
# Container neu und wuerde sie abbrechen. "Der Server darf nicht abstuerzen"
# gilt auch fuer laufende Wiedergaben: lieber den Push verschieben.
set -e

echo "→ Pruefe aktive Wiedergaben auf dem Server (Tower)..."
# Nur Wiedergabe-Transcodes zaehlen (2026-09-28): frueher zaehlte hier JEDER
# ffmpeg-Prozess auf dem Host — auch Intro-Erkennung, Trickplay und Whisper,
# die im Hintergrund laufend kurze ffmpeg-Aufrufe starten. Ergebnis: "17
# aktive Transcode-Wiedergaben", obwohl seit 37 Minuten niemand schaute, und
# ein blockierter Deploy. Wiedergabe-Transcodes erkennt man eindeutig an der
# HLS-Ausgabe (-hls_segment_filename, siehe playback/ffmpeg.go), und nur im
# goldfish-Container (docker top braucht pid in der Spaltenliste).
active=$(ssh -p 2202 -o ConnectTimeout=5 root@192.168.2.140 \
  "docker top goldfish -eo pid,args 2>/dev/null | grep -c -- '-hls_segment_filename' || true" 2>/dev/null || echo "?")
if [ "$active" = "?" ]; then
  echo "⚠ Konnte den Server nicht erreichen (SSH/Timeout) — Aktivitaets-Check uebersprungen."
  echo "  Manuell pruefen: ssh -p 2202 root@192.168.2.140 \"docker top goldfish -eo pid,args | grep ffmpeg\""
elif [ "$active" != "0" ]; then
  echo ""
  echo "✗ Push abgebrochen: $active aktive Transcode-Wiedergabe(n) auf dem Server."
  echo "  Ein Deploy jetzt wuerde laufende Streams unterbrechen."
  echo "  Warten, bis niemand mehr streamt, oder erzwingen mit 'git push --no-verify'."
  exit 1
fi
# Zweite Pruefung (2026-09-26): ffmpeg allein reicht nicht. Seit der
# VOD-Playlist ist ein Film oft nach wenigen Minuten fertig umgewandelt,
# waehrend weiter geschaut wird — ein Deploy riss so eine Mac-App-Wiedergabe
# ab (HTTP 502 waehrend des Neustarts). Der Server meldet deshalb, wie lange
# die letzte Wiedergabe-Aktivitaet (Segment-/Fortschritts-Abruf, Direct Play)
# zurueckliegt — nur auf Anfragen aus dem Container selbst.
idle=$(ssh -p 2202 -o ConnectTimeout=5 root@192.168.2.140 \
  "docker exec goldfish curl -s --max-time 5 localhost:8096/api/health" 2>/dev/null \
  | grep -o '"playbackIdleSec":-\?[0-9]*' | cut -d: -f2 || true)
if [ -n "$idle" ] && [ "$idle" -ge 0 ] && [ "$idle" -lt 600 ]; then
  echo ""
  echo "✗ Push abgebrochen: vor ${idle}s wurde noch etwas abgespielt."
  echo "  Ein Deploy jetzt wuerde die Wiedergabe unterbrechen (Neustart = kurz HTTP 502)."
  echo "  Warten, bis niemand mehr schaut, oder erzwingen mit 'git push --no-verify'."
  exit 1
fi
echo "✓ Keine aktive Wiedergabe — Deploy unbedenklich."

range="$(git rev-parse @{u} 2>/dev/null || echo '')..HEAD"
changed=""
if [ -n "$range" ] && git rev-parse @{u} >/dev/null 2>&1; then
  changed=$(git diff --name-only "$range" 2>/dev/null || true)
else
  changed=$(git diff --name-only HEAD~5..HEAD 2>/dev/null || true)
fi
if echo "$changed" | grep -qE '\.go$'; then
  echo "→ Go-Aenderungen im Push, fuehre Nutzertrennungs-Tests aus..."
  if ! go test ./internal/store/... -run 'ACL|FieldParity' -v; then
    echo ""
    echo "✗ Nutzertrennungs-Test fehlgeschlagen — Push abgebrochen."
    echo "  Nur mit 'git push --no-verify' erzwingbar (NICHT empfohlen)."
    exit 1
  fi
fi
EOF
chmod +x .git/hooks/pre-push

echo "✓ Pre-Commit- und Pre-Push-Hook installiert."
