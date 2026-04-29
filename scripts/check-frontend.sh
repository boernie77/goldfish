#!/bin/sh
# Pre-Commit/Pre-Deploy-Check fuer das Goldfish-Frontend.
#
# Hintergrund: Wir haben mehrfach JS-Syntax-Errors deployed (z.B. deutsche
# Anfuehrungszeichen mit eingebettetem ASCII-" in einem "..."-String),
# die die komplette Browser-App tot machen. Dieser Check faengt das ab.
#
# Wird lokal vom pre-commit-Hook und in CI vor dem Build aufgerufen.
# Faellt ueber den ersten Fehler — Exit-Code != 0 = nicht deployen.
#
# POSIX-kompatibel (laeuft in busybox/alpine ohne bash).

set -eu

cd "$(dirname "$0")/.."

ROOT="internal/webassets/web"
errors=0
count=0

# `find -exec sh -c` Pattern statt bash process substitution — laeuft auch
# in /bin/sh (busybox/alpine).
for f in $(find "$ROOT" -type f -name "*.js"); do
  count=$((count + 1))
  if ! node --check "$f" 2>&1; then
    echo "  ↳ Syntax-Error in $f" >&2
    errors=$((errors + 1))
  fi
done

if [ "$errors" -gt 0 ]; then
  echo ""
  echo "Frontend-Check failed: $errors Datei(en) mit Syntax-Errors." >&2
  echo "Vor dem Commit/Deploy fixen." >&2
  exit 1
fi

echo "Frontend syntax check passed ($count JS-Datei(en))"
