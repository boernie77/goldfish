#!/usr/bin/env python3
"""Liest die Wikipedia-„Liste der Tatort-Folgen" und schreibt den Ermittler-
Katalog nach internal/catalog/data/tatort.json.

Warum: TMDB kennt zu Tatort-Folgen keinen Ermittler (nur Gastdarsteller). Die
Kommissar-Ansicht braucht aber „welche Folgen gehören zu Batic und Leitmayr",
um fehlende Folgen anzuzeigen. Quelle: de.wikipedia.org (CC BY-SA 4.0), nur
Fakten (Nummer, Titel, Sender, Datum, Ermittler).

Aufruf (bei Bedarf erneut, z. B. einmal im Jahr):
    python3 scripts/import_tatort_catalog.py
Danach committen — der Server bettet die Datei ein (go:embed).
"""
import json
import re
import sys
import urllib.parse
import urllib.request
from datetime import date
from pathlib import Path

PAGE = "Liste_der_Tatort-Folgen"
OUT = Path(__file__).resolve().parent.parent / "internal" / "catalog" / "data" / "tatort.json"
MONTHS = {"jan": 1, "feb": 2, "mär": 3, "mar": 3, "apr": 4, "mai": 5, "jun": 6, "jul": 7,
          "aug": 8, "sep": 9, "okt": 10, "nov": 11, "dez": 12}


def fetch_wikitext() -> str:
    url = "https://de.wikipedia.org/w/api.php?" + urllib.parse.urlencode(
        {"action": "parse", "page": PAGE, "prop": "wikitext", "format": "json", "formatversion": 2})
    req = urllib.request.Request(url, headers={"User-Agent": "GoldfishTatortImport/1.0 (github.com/boernie77/goldfish)"})
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.load(r)["parse"]["wikitext"]


def plain(cell: str) -> str:
    """Wiki-Markup → Klartext: [[a|b]] → b, [[a]] → a, Vorlagen und HTML weg."""
    s = re.sub(r"<br\s*/?>.*", "", cell, flags=re.S)          # alles nach <br> (Gastauftritte, Hinweise)
    s = re.sub(r"<small>.*?</small>", "", s, flags=re.S)
    s = re.sub(r"<ref[^>]*/>|<ref[^>]*>.*?</ref>", "", s, flags=re.S)
    s = re.sub(r"\{\{0\}\}", "", s)
    s = re.sub(r"\{\{[^{}]*\}\}", "", s)
    s = re.sub(r"\[\[(?:[^\]|]*\|)?([^\]]*)\]\]", r"\1", s)
    s = re.sub(r"<[^>]+>", "", s)
    s = s.replace("&nbsp;", " ").replace("''", "")
    return re.sub(r"\s+", " ", s).strip()


def teams(text: str) -> list:
    """„A (Gastauftritt X)" → ["A"]; Crossover „A / B" → ["A", "B"]."""
    text = re.sub(r"\((?:[^()]*Gastauftritt|Gast)[^()]*\)", "", text)
    out = [t.strip(" ,;") for t in text.split(" / ")]
    return [t for t in out if t]


def parse_date(cell: str) -> str:
    m = re.search(r"DatumZelle\|(\d{4}-\d{2}-\d{2})", cell)
    if m:
        return m.group(1)
    m = re.search(r"(\d{1,2})\.\s*([A-Za-zä]{3})[a-zä]*\.?\s*(\d{4})", plain(cell))
    if m and m.group(2).lower() in MONTHS:
        return date(int(m.group(3)), MONTHS[m.group(2).lower()], int(m.group(1))).isoformat()
    return ""


def rows_of(table: str):
    for chunk in re.split(r"\n\|-[^\n]*", table)[1:]:
        cells = []
        for line in chunk.strip().split("\n"):
            if line.startswith("|") and not line.startswith("|}"):
                cells.extend(p for p in line[1:].split("||"))
            elif cells:
                cells[-1] += "\n" + line
        if len(cells) >= 5:
            yield [c.strip() for c in cells]


def parse_table(text: str, heading: str, orf: bool):
    start = text.find(heading)
    if start < 0:
        sys.exit(f"Abschnitt fehlt: {heading}")
    t0 = text.find("{|", start)
    t1 = text.find("\n|}", t0)
    out = []
    for c in rows_of(text[t0:t1]):
        nr = plain(c[0])
        if not re.fullmatch(r"\d+[a-z]?", nr):
            continue
        out.append({
            "nr": nr,
            "title": plain(c[1]),
            "sender": plain(c[2]),
            "date": parse_date(c[3]),
            "ermittler": teams(plain(c[4])),
            "orf": orf,
        })
    return out


def main():
    text = fetch_wikitext()
    main_rows = parse_table(text, "=== Ausgestrahlte Folgen ===", False)
    orf_rows = parse_table(text, "== ORF-eigene Produktionen ==", True)
    rows = main_rows + orf_rows
    bad = [r for r in rows if not r["date"] or not r["ermittler"] or not r["title"]]
    if bad:
        print(f"⚠ {len(bad)} unvollständige Zeilen, z. B.: {bad[:3]}", file=sys.stderr)
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(json.dumps({
        "source": "https://de.wikipedia.org/wiki/Liste_der_Tatort-Folgen (CC BY-SA 4.0)",
        "tmdbShowId": 3034,
        "episodes": rows,
    }, ensure_ascii=False, indent=0) + "\n", encoding="utf-8")
    print(f"{len(main_rows)} Folgen + {len(orf_rows)} ORF-Folgen → {OUT}")


if __name__ == "__main__":
    main()
