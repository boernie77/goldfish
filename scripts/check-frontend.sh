#!/usr/bin/env bash
# Pre-Commit/Pre-Deploy-Check fuer das Goldfish-Frontend.
#
# Hintergrund: Wir haben mehrfach JS-Syntax-Errors deployed (z.B. deutsche
# Anfuehrungszeichen mit eingebettetem ASCII-" in einem "..."-String),
# die die komplette Browser-App tot machen. Dieser Check faengt das ab.
#
# Wird lokal vom pre-commit-Hook und in CI vor dem Build aufgerufen.
# Faellt ueber den ersten Fehler — Exit-Code != 0 = nicht deployen.

set -euo pipefail

cd "$(dirname "$0")/.."

ROOT="internal/webassets/web"
errors=0

check_js() {
  local file="$1"
  # node --check faengt alle Parse-/Syntax-Errors ab. Reicht fuer den Use-Case
  # (semantische Probleme wie tote Funktionsaufrufe sieht's nicht — das ist OK,
  # die werden im Browser sofort sichtbar; Parse-Errors brechen ALLES).
  if ! node --check "$file" 2>&1; then
    echo "  ↳ Syntax-Error in $file" >&2
    errors=$((errors + 1))
  fi
}

# Alle JS-Files im embed-Pfad pruefen. Wenn jemand app_1.js o.ä. liegen
# laesst, schauen wir's auch an — landet ueber `//go:embed all:web` im
# Binary, also relevant.
while IFS= read -r f; do
  check_js "$f"
done < <(find "$ROOT" -type f -name "*.js")

if [ "$errors" -gt 0 ]; then
  echo ""
  echo "❌ Frontend-Check failed: $errors Datei(en) mit Syntax-Errors." >&2
  echo "   Vor dem Commit/Deploy fixen." >&2
  exit 1
fi

echo "✓ Frontend syntax check passed ($(find "$ROOT" -type f -name "*.js" | wc -l | tr -d ' ') JS-Datei(en))"
