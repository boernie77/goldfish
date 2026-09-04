#!/usr/bin/env bash
# Goldfish — Interactive Installer
#
# Lädt Goldfish, fragt die wichtigen Werte in Dialog-Boxen ab und startet den
# Container. Funktioniert auf jedem Linux mit Bash + Docker.
#
# Nutzung:
#   wget https://raw.githubusercontent.com/<owner>/goldfish/main/install.sh
#   chmod +x install.sh
#   ./install.sh
#
# Voraussetzungen (werden geprüft):
#   - Docker + Docker Compose (compose-Plugin oder docker-compose-Binary)
#   - git
#   - whiptail oder dialog für die TUI-Boxen (wird ggf. nachinstalliert)

set -euo pipefail

REPO_URL_DEFAULT="https://github.com/boernie77/goldfish.git"
REPO_URL="${GOLDFISH_REPO:-$REPO_URL_DEFAULT}"

# ─────────────────────────────────────────────────────────────────────────────
# 0. Helper
# ─────────────────────────────────────────────────────────────────────────────

color_red()    { printf '\033[31m%s\033[0m\n' "$*"; }
color_green()  { printf '\033[32m%s\033[0m\n' "$*"; }
color_yellow() { printf '\033[33m%s\033[0m\n' "$*"; }

die() { color_red "✗ $*"; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

# Wählt das verfügbare Dialog-Tool (whiptail bevorzugt, dialog als Fallback,
# read als Last-Resort).
DIALOG=""
choose_dialog_tool() {
  if have whiptail; then DIALOG="whiptail"
  elif have dialog; then DIALOG="dialog"
  else DIALOG=""
  fi
}

# ─────────────────────────────────────────────────────────────────────────────
# 1. Pre-Flight-Checks
# ─────────────────────────────────────────────────────────────────────────────

preflight() {
  color_green "→ Pre-Flight-Checks..."
  have git    || die "git fehlt — install via deinen Paket-Manager (apt install git, brew install git, …)"
  have docker || die "docker fehlt — siehe https://docs.docker.com/engine/install/"
  if ! docker info >/dev/null 2>&1; then
    die "Docker daemon läuft nicht oder dein User ist nicht in der docker-group. Versuche 'sudo systemctl start docker' bzw. 'sudo usermod -aG docker \$USER' + Re-Login."
  fi
  if ! docker compose version >/dev/null 2>&1 && ! have docker-compose; then
    die "Docker Compose fehlt — installiere das compose-Plugin: https://docs.docker.com/compose/install/"
  fi
  # whiptail/dialog versuchen nachzuinstallieren wenn nicht da
  choose_dialog_tool
  if [[ -z "$DIALOG" ]]; then
    color_yellow "  whiptail/dialog nicht gefunden — versuche zu installieren..."
    if have apt-get; then
      sudo apt-get update -qq && sudo apt-get install -y whiptail >/dev/null 2>&1 || true
    elif have dnf; then
      sudo dnf install -y newt >/dev/null 2>&1 || true
    elif have pacman; then
      sudo pacman -S --noconfirm libnewt >/dev/null 2>&1 || true
    elif have brew; then
      brew install newt >/dev/null 2>&1 || true
    fi
    choose_dialog_tool
    if [[ -z "$DIALOG" ]]; then
      color_yellow "  Konnte whiptail nicht installieren — falle auf simple Text-Prompts zurück."
    fi
  fi
  color_green "  ✓ alle Voraussetzungen OK"
}

# ─────────────────────────────────────────────────────────────────────────────
# 2. UI-Helfer (Dialog-Boxen mit Fallback auf read)
# ─────────────────────────────────────────────────────────────────────────────

# ui_msg "Title" "Body" — Hinweis-Box, Enter zum Schließen
ui_msg() {
  if [[ "$DIALOG" == "whiptail" ]]; then
    whiptail --title "$1" --msgbox "$2" 14 70
  elif [[ "$DIALOG" == "dialog" ]]; then
    dialog --title "$1" --msgbox "$2" 14 70; clear
  else
    echo "" >&2; echo "═══ $1 ═══" >&2; echo "$2" >&2; echo "" >&2; read -rp "Weiter mit Enter… " _
  fi
}

# ui_input "Title" "Body" "default" → echoed der eingegebene Wert
ui_input() {
  local title="$1" body="$2" default="$3"
  if [[ "$DIALOG" == "whiptail" ]]; then
    whiptail --title "$title" --inputbox "$body" 14 70 "$default" 3>&1 1>&2 2>&3
  elif [[ "$DIALOG" == "dialog" ]]; then
    dialog --title "$title" --inputbox "$body" 14 70 "$default" 2>&1 1>&3 3>&-; clear
  else
    echo "" >&2; echo "═══ $title ═══" >&2; echo "$body" >&2
    read -rp "[$default] > " val
    echo "${val:-$default}"
  fi
}

# ui_password "Title" "Body" → echoed das eingegebene Passwort
ui_password() {
  local title="$1" body="$2"
  if [[ "$DIALOG" == "whiptail" ]]; then
    whiptail --title "$title" --passwordbox "$body" 14 70 3>&1 1>&2 2>&3
  elif [[ "$DIALOG" == "dialog" ]]; then
    dialog --title "$title" --passwordbox "$body" 14 70 2>&1 1>&3 3>&-; clear
  else
    echo "" >&2; echo "═══ $title ═══" >&2; echo "$body" >&2
    read -rsp "> " val; echo >&2
    echo "$val"
  fi
}

# ui_yesno "Title" "Body" → Exit 0 = ja, 1 = nein
ui_yesno() {
  if [[ "$DIALOG" == "whiptail" ]]; then
    whiptail --title "$1" --yesno "$2" 12 70
  elif [[ "$DIALOG" == "dialog" ]]; then
    dialog --title "$1" --yesno "$2" 12 70; local rc=$?; clear; return $rc
  else
    echo "" >&2; echo "═══ $1 ═══" >&2; echo "$2" >&2
    read -rp "[j/N] > " val
    [[ "${val,,}" == "j" || "${val,,}" == "y" ]]
  fi
}

# ─────────────────────────────────────────────────────────────────────────────
# 3. Auto-Detection von Werten
# ─────────────────────────────────────────────────────────────────────────────

# render-Group-ID auto-erkennen (auf Linux). Auf macOS unwichtig (kein /dev/dri).
detect_render_gid() {
  if have getent && getent group render >/dev/null 2>&1; then
    getent group render | awk -F: '{print $3}' && return 0
  fi
  if have getent && getent group video >/dev/null 2>&1; then
    getent group video | awk -F: '{print $3}' && return 0
  fi
  echo "107"  # Debian-Default
}

# /dev/dri vorhanden? Wenn ja, VAAPI sinnvoll; sonst Software-Encode.
has_igpu() {
  [[ -d /dev/dri ]]
}

# ─────────────────────────────────────────────────────────────────────────────
# 4. Wizard
# ─────────────────────────────────────────────────────────────────────────────

run_wizard() {
  ui_msg "Goldfish Installer" \
"Willkommen!\n\nDieser Assistent führt dich in wenigen Schritten durch die Goldfish-Installation. Du kannst jederzeit mit Esc abbrechen — es wird nichts geschrieben bevor du am Ende bestätigst.\n\nVoraussetzung: Docker läuft auf diesem Host."

  # Install-Verzeichnis
  INSTALL_DIR=$(ui_input "Install-Verzeichnis" \
    "Wo soll Goldfish installiert werden?\n(Repo wird hierhin geklont, plus eine ./config-Subfolder mit DB + Cache)" \
    "$HOME/goldfish")
  [[ -z "$INSTALL_DIR" ]] && die "Abgebrochen."

  if [[ -e "$INSTALL_DIR" ]] && ! [[ -d "$INSTALL_DIR/.git" ]]; then
    if ! ui_yesno "Verzeichnis existiert" "$INSTALL_DIR existiert schon und ist kein git-Repo. Trotzdem fortfahren? (Vorhandene Dateien werden NICHT überschrieben.)"; then
      die "Abgebrochen."
    fi
  fi

  # HTTP-Port
  HTTP_PORT=$(ui_input "Web-Port" \
    "Auf welchem Port soll Goldfish im LAN erreichbar sein?\n(Default 8096; falls Jellyfin/Plex bereits läuft, nimm z.B. 8098)" \
    "8096")
  [[ -z "$HTTP_PORT" ]] && HTTP_PORT="8096"

  # Media-Pfad
  MEDIA_ROOT=$(ui_input "Medien-Verzeichnis" \
"Pfad zu deinen Medien auf diesem Host. Goldfish mountet ihn read-only nach /media im Container.\n\nBeispiele:\n  /mnt/media       (Standard-Linux)\n  /mnt/user        (Unraid)\n  /volume1/Video   (Synology)\n  /srv/media" \
    "/mnt/media")
  [[ -z "$MEDIA_ROOT" ]] && die "Medien-Pfad fehlt."
  if [[ ! -e "$MEDIA_ROOT" ]]; then
    if ! ui_yesno "Pfad existiert nicht" "$MEDIA_ROOT existiert nicht. Trotzdem fortfahren? (Container startet, aber Library-Scan findet nichts.)"; then
      die "Abgebrochen — leg den Pfad an und ruf den Installer nochmal."
    fi
  fi

  # render-Group-ID
  if has_igpu; then
    DETECTED_GID=$(detect_render_gid)
    RENDER_GID=$(ui_input "render-Group-ID (für VAAPI)" \
"Damit Goldfish auf /dev/dri zugreifen darf, muss die render-Group-ID des Hosts mitgegeben werden.\n\nAuto-erkannt: $DETECTED_GID\n\n(Bei Unraid oft 109, bei Arch/Fedora 989, bei Debian/Ubuntu/Mint 107.)" \
      "$DETECTED_GID")
    [[ -z "$RENDER_GID" ]] && RENDER_GID="$DETECTED_GID"
  else
    RENDER_GID="107"  # egal, ohne /dev/dri wirkungslos
    ui_msg "Keine iGPU gefunden" "Auf diesem Host gibt es kein /dev/dri — VAAPI-Hardware-Encoding ist nicht verfügbar. Goldfish nutzt automatisch Software-Encoding (langsamer, aber funktioniert)."
  fi

  # NVIDIA?
  WANT_NVIDIA=0
  if ui_yesno "NVIDIA NVENC?" \
"Hat dieser Host eine CUDA-fähige NVIDIA-GPU UND den NVIDIA Container Runtime installiert?\n\n(Wenn nein, einfach mit 'Nein' antworten — die iGPU/Software reicht in den meisten Fällen. Du kannst NVIDIA auch später nachträglich aktivieren.)"; then
    WANT_NVIDIA=1
  fi

  # OIDC?
  WANT_OIDC=0
  OIDC_ISSUER_URL=""; OIDC_CLIENT_ID=""; OIDC_CLIENT_SECRET=""; OIDC_REDIRECT_URL=""
  if ui_yesno "Single-Sign-On?" \
"Möchtest du SSO via OpenID-Connect aktivieren? (Authentik, Keycloak, Authelia, Zitadel u.a.)\n\nWenn nein, nutzt Goldfish nur klassisches Username/Passwort — das geht immer."; then
    WANT_OIDC=1
    OIDC_ISSUER_URL=$(ui_input "OIDC Issuer-URL" \
"Issuer-URL deines IdP, MIT Trailing-Slash.\nBeispiel Authentik: https://auth.example.com/application/o/goldfish/" "")
    OIDC_CLIENT_ID=$(ui_input "OIDC Client-ID" "Client-ID aus dem IdP-Provider" "")
    OIDC_CLIENT_SECRET=$(ui_password "OIDC Client-Secret" "Client-Secret aus dem IdP-Provider")
    PUBLIC_URL=$(ui_input "Goldfish-URL (öffentlich)" \
"Unter welcher URL ist Goldfish öffentlich erreichbar?\n(Wird für die Redirect-URI gebraucht.)" \
      "https://goldfish.example.com")
    OIDC_REDIRECT_URL="${PUBLIC_URL%/}/api/auth/oidc/callback"
  fi

  # Zusammenfassung
  local summary
  summary="Bereit zur Installation:

  Verzeichnis:    $INSTALL_DIR
  Port:           $HTTP_PORT
  Medien-Wurzel:  $MEDIA_ROOT
  render-GID:     $RENDER_GID
  NVIDIA NVENC:   $([ "$WANT_NVIDIA" = "1" ] && echo "ja" || echo "nein")
  OIDC SSO:       $([ "$WANT_OIDC"  = "1" ] && echo "ja ($OIDC_ISSUER_URL)" || echo "nein")

Im nächsten Schritt: git clone, .env schreiben, docker compose up -d --build."
  if ! ui_yesno "Bestätigen" "$summary"; then
    die "Abgebrochen."
  fi
}

# ─────────────────────────────────────────────────────────────────────────────
# 5. Apply
# ─────────────────────────────────────────────────────────────────────────────

apply() {
  color_green "→ Klone Repo nach $INSTALL_DIR..."
  if [[ -d "$INSTALL_DIR/.git" ]]; then
    git -C "$INSTALL_DIR" pull --ff-only
  else
    mkdir -p "$INSTALL_DIR"
    git clone "$REPO_URL" "$INSTALL_DIR"
  fi

  color_green "→ Schreibe .env..."
  cat > "$INSTALL_DIR/.env" <<EOF
# Generated by install.sh on $(date -Iseconds)
RENDER_GID=$RENDER_GID
MEDIA_ROOT=$MEDIA_ROOT
EOF
  if [[ "$WANT_OIDC" = "1" ]]; then
    cat >> "$INSTALL_DIR/.env" <<EOF
OIDC_ISSUER_URL=$OIDC_ISSUER_URL
OIDC_CLIENT_ID=$OIDC_CLIENT_ID
OIDC_CLIENT_SECRET=$OIDC_CLIENT_SECRET
OIDC_REDIRECT_URL=$OIDC_REDIRECT_URL
EOF
  fi
  if [[ "$WANT_NVIDIA" = "1" ]]; then
    cat >> "$INSTALL_DIR/.env" <<EOF
NVIDIA_VISIBLE_DEVICES=all
NVIDIA_DRIVER_CAPABILITIES=compute,video,utility
EOF
  fi

  # Compose-Override für Port (statt Repo-Default 8096)
  if [[ "$HTTP_PORT" != "8096" ]]; then
    cat > "$INSTALL_DIR/docker-compose.override.yml" <<EOF
# Erzeugt vom Installer — überschreibt nur den Port-Bind.
services:
  goldfish:
    ports:
      - "$HTTP_PORT:8096"
EOF
  fi

  # NVIDIA-Override (auskommentierten Block aktivieren)
  if [[ "$WANT_NVIDIA" = "1" ]]; then
    cat >> "$INSTALL_DIR/docker-compose.override.yml" <<EOF
$([ -f "$INSTALL_DIR/docker-compose.override.yml" ] || echo "services:")
  goldfish:
    runtime: nvidia
    environment:
      - NVIDIA_VISIBLE_DEVICES=all
      - NVIDIA_DRIVER_CAPABILITIES=compute,video,utility
EOF
  fi

  color_green "→ Build + Start (kann beim ersten Mal mehrere Minuten dauern)..."
  ( cd "$INSTALL_DIR" && docker compose up -d --build )

  # Health-Check
  color_green "→ Warte auf Goldfish-Health..."
  local tries=0
  until curl -fsS "http://localhost:$HTTP_PORT/api/health" >/dev/null 2>&1; do
    tries=$((tries+1))
    if (( tries > 30 )); then
      color_yellow "  Health-Endpoint nach 30 Versuchen nicht erreichbar — Container läuft trotzdem, Logs:"
      ( cd "$INSTALL_DIR" && docker compose logs --tail 30 )
      break
    fi
    sleep 2
  done

  color_green ""
  color_green "════════════════════════════════════════════════════════"
  color_green "  ✓ Goldfish läuft!"
  color_green "════════════════════════════════════════════════════════"
  echo ""
  echo "  Web-UI:    http://$(hostname -I 2>/dev/null | awk '{print $1}' || echo localhost):$HTTP_PORT"
  echo "  Logs:      cd $INSTALL_DIR && docker compose logs -f"
  echo "  Stop:      cd $INSTALL_DIR && docker compose down"
  echo "  Update:    cd $INSTALL_DIR && git pull && docker compose up -d --build"
  echo ""
  echo "  Erste Schritte im Browser:"
  echo "    1. Admin-Konto anlegen (Setup-Form)"
  echo "    2. Optional: TMDB-Key eintragen (für Poster + Plot)"
  echo "    3. Library hinzufügen (📁 Symbol oben), Pfad aus /media wählen"
  echo "    4. Scan starten"
  echo ""
}

# ─────────────────────────────────────────────────────────────────────────────
# main
# ─────────────────────────────────────────────────────────────────────────────

main() {
  preflight
  run_wizard
  apply
}

main "$@"
