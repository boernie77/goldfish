package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/model"
)

func (s *Server) getThumb(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if it == nil || !it.HasThumb {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	path := it.ThumbPath
	// ?format=portrait: großes Hochformat-Bild für die einheitlichen 2:3-Kacheln
	// der Startseite (seit 1.4.54). Fehlt es oder schlägt die Erzeugung fehl,
	// gibt es das normale Vorschaubild — die Kachel bleibt nie leer.
	if r.URL.Query().Get("format") == "portrait" {
		if p := ensurePortraitThumb(r.Context(), it); p != "" {
			path = p
		}
	}
	f, err := os.Open(path)
	if err != nil {
		http.Redirect(w, r, "/placeholder.svg", http.StatusFound)
		return
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, path, info.ModTime(), f)
}

// portraitSlots begrenzt die gleichzeitigen ffmpeg-Aufrufe für Hochformat-
// Bilder. Beim ersten Öffnen der Startseite fragen Browser/Apps bis zu ~50
// Kacheln auf einmal an — ungebremst wären das 50 ffmpeg parallel neben einer
// laufenden Wiedergabe (siehe AGENTS.md „Stabilitätsgrenze").
var portraitSlots = make(chan struct{}, 2)

// ensurePortraitThumb liefert den Pfad eines 400×600-Hochformat-Bildes (Mitte
// des Videobildes im Format 2:3, volle Auflösung als Quelle) und erzeugt es bei
// Bedarf. Gleiche Stelle im Video wie das normale Vorschaubild (Scanner
// makeThumbnail: 10 % der Laufzeit, 2–600 s). Gespeichert neben dem normalen
// Vorschaubild als `<name>_p.jpg`.
//
// User-Wunsch 2026-09-29: Die erste Umsetzung der einheitlichen Kacheln zeigte
// das 480×270-Vorschaubild klein in der Mitte vor einer unscharfen,
// vergrößerten Kopie — „schaut furchtbar aus", es sollen nur große Bilder sein.
func ensurePortraitThumb(ctx context.Context, it *model.Item) string {
	if it.VideoCodec == "" || it.DurationSec <= 0 || !strings.HasSuffix(it.ThumbPath, ".jpg") {
		return "" // Musik/Audio: kein Videobild, normales Cover/Vorschaubild
	}
	out := strings.TrimSuffix(it.ThumbPath, ".jpg") + "_p.jpg"
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		if src, err := os.Stat(it.ThumbPath); err != nil || !src.ModTime().After(st.ModTime()) {
			return out
		}
	}
	select {
	case portraitSlots <- struct{}{}:
	case <-ctx.Done():
		return ""
	}
	defer func() { <-portraitSlots }()
	// Nochmal prüfen: ein paralleler Request kann es inzwischen erzeugt haben.
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		return out
	}
	seek := it.DurationSec * 0.1
	if seek < 2 {
		seek = 2
	}
	if seek > 600 {
		seek = 600
	}
	cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tmp := out + ".tmp.jpg"
	// Erst auf quadratische Pixel bringen (anamorphe Quellen), dann die Mitte
	// im Format 2:3 ausschneiden (bei Hochkant-Quellen oben/unten), dann auf
	// 400×600. Kommas in Ausdrücken müssen im Filtergraph maskiert sein.
	cmd := exec.CommandContext(cctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-ss", fmt.Sprintf("%.2f", seek),
		"-i", it.Path,
		"-vframes", "1",
		"-vf", `scale=iw*sar:ih,setsar=1,crop=w='min(iw\,ih*2/3)':h='min(ih\,iw*3/2)',scale=400:600`,
		"-q:v", "4",
		tmp,
	)
	if outb, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[thumb] Hochformat %s: %v %s", it.Path, err, strings.TrimSpace(string(outb)))
		_ = os.Remove(tmp)
		return ""
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return ""
	}
	return out
}
