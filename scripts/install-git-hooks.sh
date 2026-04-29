#!/usr/bin/env bash
# Installiert die lokalen Git-Hooks. Hooks sind nicht im Repo eingecheckt
# (.git/hooks/ ist git-intern), daher muss jeder Klon den Hook einmal
# manuell aktivieren:
#
#   ./scripts/install-git-hooks.sh
#
# Der Pre-Commit-Hook ruft scripts/check-frontend.sh auf, sobald JS-
# Dateien im Commit sind, und blockt Commits mit Syntax-Errors.

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
echo "✓ Pre-Commit-Hook installiert."
