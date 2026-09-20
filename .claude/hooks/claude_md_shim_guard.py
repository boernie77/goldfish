#!/usr/bin/env python3
"""CLAUDE.md-Shim-Waechter.

Haelt CLAUDE.md in Projekten, die auf das agent-uebergreifende Muster umgestellt
sind, auf der einen Zeile "@AGENTS.md". Grund: In solchen Projekten ist AGENTS.md
die einzige Quelle (Hermes liest AGENTS.md und liest CLAUDE.md gar nicht mehr),
Inhalt in CLAUDE.md wuerde also still auseinanderlaufen.

Einsatz als Claude-Code-Hook:
  PreToolUse  (Write|Edit)          -> Schreibversuch wird abgelehnt und erklaert
  PostToolUse (Write|Edit|Bash)     -> falls doch etwas durchkam: sofort zuruecksetzen
  SessionStart                      -> Drift aus anderen Wegen (Editor, Skript) heilen

Sicherheitsregel: Es wird NUR repariert, wenn neben der CLAUDE.md eine AGENTS.md
liegt. In jedem anderen Projekt bleibt die Datei unangetastet.
"""
from __future__ import annotations

import json
import os
import sys
from datetime import datetime

SHIM = "@AGENTS.md\n"
LOG = os.path.expanduser("~/.hermes/logs/claude-md-shim-guard.log")


def log(msg: str) -> None:
    try:
        os.makedirs(os.path.dirname(LOG), exist_ok=True)
        with open(LOG, "a", encoding="utf-8") as fh:
            fh.write(f"{datetime.now().isoformat(timespec='seconds')} {msg}\n")
    except Exception:
        pass


def is_shim(candidate: str) -> bool:
    try:
        with open(candidate, encoding="utf-8") as fh:
            return fh.read().strip() == SHIM.strip()
    except Exception:
        return False


def targets(file_path: str, cwd: str) -> str | None:
    """Gibt den Pfad der CLAUDE.md zurueck, wenn sie geschuetzt werden soll."""
    if not file_path:
        return None
    if not os.path.isabs(file_path):
        file_path = os.path.join(cwd or os.getcwd(), file_path)
    if os.path.basename(file_path) != "CLAUDE.md":
        return None
    directory = os.path.dirname(os.path.abspath(file_path))
    if os.path.isfile(os.path.join(directory, "AGENTS.md")):
        return os.path.join(directory, "CLAUDE.md")
    return None


def repair(candidate: str) -> bool:
    """Setzt die Datei auf den Shim zurueck. True = es wurde etwas geaendert."""
    if os.path.isfile(candidate) and is_shim(candidate):
        return False
    try:
        with open(candidate, "w", encoding="utf-8") as fh:
            fh.write(SHIM)
        log(f"repariert: {candidate}")
        return True
    except Exception as exc:  # pragma: no cover
        log(f"FEHLER beim Reparieren von {candidate}: {exc}")
        return False


def mentioned_in_bash(command: str, cwd: str) -> str | None:
    if "CLAUDE.md" not in command:
        return None
    candidate = targets(os.path.join(cwd or os.getcwd(), "CLAUDE.md"), cwd)
    if candidate and not is_shim(candidate):
        return candidate
    return None


def main() -> None:
    event = sys.argv[1] if len(sys.argv) > 1 else "unknown"
    raw = sys.stdin.read()
    try:
        data = json.loads(raw) if raw.strip() else {}
    except Exception:
        data = {}

    cwd = data.get("cwd") or os.getcwd()
    tool_input = data.get("tool_input") or {}
    file_path = tool_input.get("file_path") or tool_input.get("path") or ""
    command = tool_input.get("command") or ""

    if event == "pre":
        candidate = targets(file_path, cwd)
        if candidate:
            log(f"Schreibversuch abgelehnt: {candidate}")
            print(json.dumps({
                "hookSpecificOutput": {
                    "hookEventName": "PreToolUse",
                    "permissionDecision": "deny",
                    "permissionDecisionReason": (
                        "CLAUDE.md ist in diesem Projekt absichtlich nur der Import-Shim "
                        "'@AGENTS.md' und wird nicht beschrieben. Inhalt gehoert nach AGENTS.md "
                        "(Regeln, die in jeder Session gelten) oder in einen Themenskill unter "
                        ".claude/skills/<thema>/SKILL.md (Detailwissen)."
                    ),
                }
            }))
        return

    if event == "post":
        candidate = targets(file_path, cwd) or mentioned_in_bash(command, cwd)
        if candidate and repair(candidate):
            print(json.dumps({
                "decision": "block",
                "reason": (
                    "CLAUDE.md wurde soeben automatisch auf den Shim '@AGENTS.md' zurueckgesetzt. "
                    "Schreibe stattdessen nach AGENTS.md oder in einen Themenskill unter "
                    ".claude/skills/<thema>/SKILL.md."
                ),
            }))
        return

    if event == "session":
        candidates = [os.path.join(cwd, "CLAUDE.md")]
        for extra in (data.get("workspaceFolders") or []):
            if isinstance(extra, str):
                candidates.append(os.path.join(extra, "CLAUDE.md"))
        healed = [c for c in candidates if targets(c, cwd) and repair(c)]
        if healed:
            print(json.dumps({
                "systemMessage": "CLAUDE.md war nicht mehr der reine Shim und wurde wiederhergestellt: "
                                 + ", ".join(os.path.dirname(h) for h in healed),
            }))
        return


if __name__ == "__main__":
    main()