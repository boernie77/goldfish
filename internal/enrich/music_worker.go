package enrich

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/boernie77/goldfish/internal/musicbrainz"
	"github.com/boernie77/goldfish/internal/store"
)

// MusicWorker ist BEWUSST ein eigenständiger, von Worker (TMDB/OMDb) komplett
// unabhängiger Enrichment-Pfad — Worker hält konkrete *tmdb.Client/*omdb.Client-
// Felder ohne Abstraktionsgrenze, ein music-Zweig hätte diese Felder überall
// optional machen müssen. Läuft nur als Fallback: die eigentliche Cover-
// Beschaffung passiert primär im Scanner (extractAlbumCovers, eingebettetes
// Bild aus der Audiodatei) — dieser Worker greift nur, wenn das fehlgeschlagen
// ist (cover_source noch '').
type MusicWorker struct {
	store       *store.Store
	mb          *musicbrainz.Client
	albumArtDir string

	mu      sync.Mutex
	running bool
	trigger chan struct{}
}

func NewMusicWorker(s *store.Store, mb *musicbrainz.Client, albumArtDir string) *MusicWorker {
	return &MusicWorker{store: s, mb: mb, albumArtDir: albumArtDir, trigger: make(chan struct{}, 1)}
}

// Trigger stößt einen Enrichment-Lauf an (non-blocking) — z.B. direkt nach
// einem Musik-Library-Scan (siehe cmd/goldfish/main.go OnComplete-Hook).
func (w *MusicWorker) Trigger() {
	select {
	case w.trigger <- struct{}{}:
	default:
	}
}

// Run blockiert bis der Kontext abläuft. Läuft periodisch (alle 30 Minuten —
// MusicBrainz' 1 req/s-Limit macht häufigere Läufe ohnehin nicht sinnvoll)
// oder bei Trigger().
func (w *MusicWorker) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	time.Sleep(5 * time.Second)
	w.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.runOnce(ctx)
		case <-w.trigger:
			w.runOnce(ctx)
		}
	}
}

func (w *MusicWorker) runOnce(ctx context.Context) {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.running = false
		w.mu.Unlock()
	}()

	albums, err := w.store.PendingMusicAlbums(50)
	if err != nil {
		log.Printf("[music-enrich] PendingMusicAlbums: %v", err)
		return
	}
	if len(albums) == 0 {
		return
	}
	log.Printf("[music-enrich] %d Alben ohne Cover, starte MusicBrainz-Suche", len(albums))
	for _, album := range albums {
		if ctx.Err() != nil {
			return
		}
		match, err := w.mb.SearchRelease(ctx, album.Artist, album.Album)
		if err != nil {
			log.Printf("[music-enrich] SearchRelease %s/%s: %v", album.Artist, album.Album, err)
			continue
		}
		if match == nil {
			// Kein Treffer — als "versucht" markieren, verhindert Endlos-Retry
			// bei jedem Worker-Lauf (analog metadata.cast_fetched_at).
			if err := w.store.SetMusicAlbumCover(album.ID, "coverart_archive", ""); err != nil {
				log.Printf("[music-enrich] SetMusicAlbumCover (kein Treffer) album=%d: %v", album.ID, err)
			}
			continue
		}
		data, err := w.mb.DownloadCoverFront(ctx, match.MBID)
		if err != nil {
			log.Printf("[music-enrich] DownloadCoverFront %s: %v", match.MBID, err)
			continue
		}
		if data == nil {
			// Release gefunden, aber kein Cover im Archive — trotzdem als
			// versucht markieren (mb_release_id bleibt gesetzt für später).
			if err := w.store.SetMusicAlbumCover(album.ID, "coverart_archive", match.MBID); err != nil {
				log.Printf("[music-enrich] SetMusicAlbumCover (kein Cover) album=%d: %v", album.ID, err)
			}
			continue
		}
		if w.albumArtDir != "" {
			out := filepath.Join(w.albumArtDir, fmt.Sprintf("album_%d.jpg", album.ID))
			if err := os.WriteFile(out, data, 0o644); err != nil {
				log.Printf("[music-enrich] write cover album=%d: %v", album.ID, err)
				continue
			}
		}
		if err := w.store.SetMusicAlbumCover(album.ID, "coverart_archive", match.MBID); err != nil {
			log.Printf("[music-enrich] SetMusicAlbumCover album=%d: %v", album.ID, err)
		}
	}
}
