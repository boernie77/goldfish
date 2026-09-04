// Package ytdlp extrahiert eine direkt abspielbare Stream-URL für einen
// YouTube-Trailer, damit die nativen Apple-Apps (Mac/iOS/tvOS) ihn per
// AVPlayer abspielen können — ohne WebKit, das auf tvOS gar nicht existiert
// (siehe CLAUDE.md "Trailer"). Eigene Extraktion (Cipher-Entschlüsselung,
// PO-Token) ist seit YouTubes 2024er Anti-Bot-Härtung praktisch nicht mehr
// selbst reimplementierbar — yt-dlp wird dagegen sehr aktiv dagegen gepflegt.
//
// Muster für den yt-dlp-Aufruf (Retry-bei-403 mit kurzer Pause) übernommen
// von ~/Projekte/Tatort_Fetcher/tatort_fetcher/downloader.py — dort läuft
// genau dieser Aufruf bereits zuverlässig produktiv, ganz ohne PO-Token-
// Sidecar. 403 von googlevideo ist demnach meist transient/IP-Reputations-
// bedingt, kein harter Block.
package ytdlp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type cacheEntry struct {
	url string
	exp time.Time
}

// Extractor cached extrahierte Stream-URLs (googlevideo-URLs sind ohnehin nur
// wenige Stunden gültig, siehe TTL unten) und serialisiert damit gleichzeitige
// Anfragen für denselben Trailer nicht extra — ein doppelter yt-dlp-Aufruf für
// dieselbe ID kurz hintereinander ist unwahrscheinlich genug, um das simpel zu
// halten.
type Extractor struct {
	mu    sync.Mutex
	cache map[string]cacheEntry
}

func New() *Extractor {
	return &Extractor{cache: make(map[string]cacheEntry)}
}

// StreamURL liefert eine direkt per HTTP abspielbare Video-URL für das
// YouTube-Video `youtubeKey`. `bestvideo+bestaudio` würde getrennte Streams
// liefern (kein Muxing hier möglich ohne ffmpeg-Merge) — wir wollen genau
// EINEN progressiven Stream mit Bild+Ton für AVPlayer, daher `-f "b"`
// (yt-dlp-Kurzform für "best single format with both video and audio").
func (e *Extractor) StreamURL(ctx context.Context, youtubeKey string) (string, error) {
	e.mu.Lock()
	if c, ok := e.cache[youtubeKey]; ok && time.Now().Before(c.exp) {
		e.mu.Unlock()
		return c.url, nil
	}
	e.mu.Unlock()

	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		url, err := e.runYtDlp(ctx, youtubeKey)
		if err == nil {
			e.mu.Lock()
			e.cache[youtubeKey] = cacheEntry{url: url, exp: time.Now().Add(4 * time.Hour)}
			e.mu.Unlock()
			return url, nil
		}
		lastErr = err
		if !strings.Contains(err.Error(), "403") || attempt == maxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return "", lastErr
}

func (e *Extractor) runYtDlp(ctx context.Context, youtubeKey string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "yt-dlp",
		"-f", "b[ext=mp4]/b",
		"-g",
		"--no-warnings",
		"https://www.youtube.com/watch?v="+youtubeKey,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("yt-dlp: %s", msg)
	}
	fields := strings.Fields(stdout.String())
	if len(fields) == 0 {
		return "", errors.New("yt-dlp: keine URL erhalten")
	}
	return fields[0], nil
}
