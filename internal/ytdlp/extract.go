// Package ytdlp lädt YouTube-Trailer per yt-dlp herunter, damit die nativen
// Apple-Apps (Mac/iOS/tvOS) sie per AVPlayer abspielen können — ohne WebKit,
// das auf tvOS gar nicht existiert (siehe CLAUDE.md "Trailer").
//
// **Warum Download statt reiner URL-Extraktion (`-g`):** YouTube liefert für
// die meisten Videos inzwischen KEIN kombiniertes Video+Audio-Format mehr
// (nur noch getrennte "video only"/"audio only"-Streams) — `-g` kann nicht
// muxen, AVPlayer kann aber nur EINE Datei laden. yt-dlp lädt daher
// bv+ba herunter und muxt sie per ffmpeg (bereits im Image vorhanden,
// dasselbe Binary wie für Transcodes) zu einer einzelnen MP4-Datei, die der
// Server danach wie eine normale lokale Datei ausliefert (Range-Requests via
// http.ServeContent). Gecached unter ConfigDir/cache/trailers/<key>.mp4,
// key-basiert statt Live-URL macht die Downloads auch beliebig oft
// wiederverwendbar (kein Ablaufdatum wie bei den signierten googlevideo-URLs).
//
// Aufrufmuster (Format-Selektor, Retry-bei-403) übernommen von
// ~/Projekte/Tatort_Fetcher/tatort_fetcher/downloader.py — dort läuft genau
// dieser yt-dlp-Aufruf bereits produktiv, ganz ohne PO-Token-Sidecar. Ein
// eigener Live-Test aus einer Cloud-Sandbox heraus schlug fehl (403), was
// sich als Sandbox-IP-Reputationsproblem herausstellte, NICHT als generelles
// YouTube-Anti-Bot-Problem — auf dem echten Server (Heimnetz-IP) funktioniert
// der einfache Aufruf.
package ytdlp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// validKey: YouTube-Video-IDs sind immer 11 Zeichen [A-Za-z0-9_-] — Guard
// gegen Pfad-Traversal, falls `youtubeKey` je aus unerwarteter Quelle käme
// (aktuell kommt er ausschließlich aus TMDBs eigener Video-Response).
var validKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// Extractor lädt Trailer herunter und cached sie auf Disk. `inFlight`
// verhindert, dass zwei gleichzeitige Anfragen für denselben Trailer den
// yt-dlp-Download doppelt anstoßen (z. B. zwei Geräte öffnen fast zeitgleich
// denselben Film).
type Extractor struct {
	CacheDir string

	mu       sync.Mutex
	inFlight map[string]*sync.WaitGroup
}

func New(cacheDir string) *Extractor {
	return &Extractor{CacheDir: cacheDir, inFlight: make(map[string]*sync.WaitGroup)}
}

// FilePath liefert den Pfad zur (bei Bedarf frisch heruntergeladenen)
// lokalen MP4-Datei für den Trailer `youtubeKey`. Bereits gecachte Trailer
// kosten nur einen os.Stat-Aufruf.
func (e *Extractor) FilePath(ctx context.Context, youtubeKey string) (string, error) {
	if !validKey.MatchString(youtubeKey) {
		return "", fmt.Errorf("ungültiger YouTube-Key")
	}
	dest := filepath.Join(e.CacheDir, youtubeKey+".mp4")
	if info, err := os.Stat(dest); err == nil && info.Size() > 0 {
		return dest, nil
	}

	e.mu.Lock()
	if wg, ok := e.inFlight[youtubeKey]; ok {
		e.mu.Unlock()
		wg.Wait()
		if info, err := os.Stat(dest); err == nil && info.Size() > 0 {
			return dest, nil
		}
		return "", fmt.Errorf("Trailer-Download fehlgeschlagen")
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	e.inFlight[youtubeKey] = wg
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.inFlight, youtubeKey)
		e.mu.Unlock()
		wg.Done()
	}()

	if err := os.MkdirAll(e.CacheDir, 0o755); err != nil {
		return "", err
	}
	if err := e.download(ctx, youtubeKey, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// download läuft mit bis zu 3 Versuchen bei HTTP 403 (siehe Paket-Kommentar
// — auf googlevideo laut Tatort_Fetcher-Erfahrung gelegentlich transient,
// besonders bei mehreren gleichzeitigen Downloads).
func (e *Extractor) download(ctx context.Context, youtubeKey, dest string) error {
	tmp := dest + ".part.mp4"
	_ = os.Remove(tmp)

	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = e.runYtDlp(ctx, youtubeKey, tmp)
		if lastErr == nil {
			return os.Rename(tmp, dest)
		}
		_ = os.Remove(tmp)
		if !strings.Contains(lastErr.Error(), "403") || attempt == maxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return lastErr
}

func (e *Extractor) runYtDlp(ctx context.Context, youtubeKey, outPath string) error {
	// 5 min Timeout — Trailer sind kurz (1-3 min), großzügig für langsamere
	// Verbindungen/Format-Wechsel bei Retry.
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	// height<=1080 deckelt die Dateigröße (4K-Trailer wären >200 MB für ein
	// paar Minuten Vorschau unnötig groß). bv*+ba muxt yt-dlp per ffmpeg
	// (bereits im Image) zu EINER Datei — AVPlayer kann keine getrennten
	// Video/Audio-Streams laden, nur `-g` (reine URL, kein Mux) würde die
	// liefern.
	cmd := exec.CommandContext(cctx, "yt-dlp",
		"-f", "bv*[ext=mp4][height<=1080]+ba[ext=m4a]/b[ext=mp4][height<=1080]/best",
		"--merge-output-format", "mp4",
		"--no-warnings",
		"--no-playlist",
		"-o", outPath,
		"https://www.youtube.com/watch?v="+youtubeKey,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("yt-dlp: %s", msg)
	}
	return nil
}
