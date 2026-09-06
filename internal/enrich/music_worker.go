package enrich

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/musicbrainz"
	"github.com/boernie77/goldfish/internal/store"
)

// MusicWorker ist BEWUSST ein eigenständiger, von Worker (TMDB/OMDb) komplett
// unabhängiger Enrichment-Pfad — Worker hält konkrete *tmdb.Client/*omdb.Client-
// Felder ohne Abstraktionsgrenze, ein music-Zweig hätte diese Felder überall
// optional machen müssen. Zwei unabhängige Fallback-Phasen pro Lauf
// (runCoverPhase, runMetadataPhase) — beide greifen NUR, wenn die primäre
// Quelle (eingebettete Tags bzw. Scanner-Cover-Extraktion) nichts geliefert
// hat: Cover-Beschaffung passiert primär im Scanner (extractAlbumCovers,
// eingebettetes Bild aus der Audiodatei), Genre/Jahr primär aus den
// eingebetteten ID3/FLAC/Vorbis-Tags (Scanner probeItem). Seit 2026-09-06
// (User-Wunsch: "Viele Titel haben zum Beispiel kein Genre") backfillt
// runMetadataPhase fehlendes Genre/Jahr aus MusicBrainz.
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

	w.runCoverPhase(ctx)
	if ctx.Err() != nil {
		return
	}
	w.runMetadataPhase(ctx)
}

// musicPhaseBatchLimit: Alben pro Store-Query. musicPhaseMaxBatches deckelt
// die Anzahl Batches PRO Worker-Lauf (200×50 = 10.000 Alben) — großzügig
// über jeder realistischen Bibliotheksgröße, verhindert aber einen
// theoretischen Endlos-Loop, falls eine zukünftige Änderung die
// Pending-Query nie leerlaufen lässt. Beide Phasen laufen batch-weise IN
// EINEM Worker-Zyklus durch, statt nur 50 Alben alle 30 Min zu schaffen —
// bei tausenden Alben mit fehlendem Genre/Jahr (z. B. Erstlauf nach diesem
// Feature) wäre die 30-Min-Kadenz sonst untragbar langsam (User-Wunsch
// 2026-09-06 will zeitnah sehen, "wie sich das entwickelt").
const musicPhaseBatchLimit = 50
const musicPhaseMaxBatches = 200

// runCoverPhase sucht MusicBrainz-Cover für Alben ohne eingebettetes Bild
// (cover_source=''). Aus der ursprünglichen Feature-Runde, nur aus runOnce
// herausgelöst + auf Batch-Looping umgestellt (siehe musicPhaseMaxBatches),
// damit runMetadataPhase (Genre/Jahr-Backfill, User-Wunsch 2026-09-06)
// danach unabhängig laufen kann — beide teilen sich denselben
// MusicBrainz-Client (und damit dessen 1 req/s-Rate-Limiter).
func (w *MusicWorker) runCoverPhase(ctx context.Context) {
	for batchNum := 0; batchNum < musicPhaseMaxBatches; batchNum++ {
		if ctx.Err() != nil {
			return
		}
		albums, err := w.store.PendingMusicAlbums(musicPhaseBatchLimit)
		if err != nil {
			log.Printf("[music-enrich] PendingMusicAlbums: %v", err)
			return
		}
		if len(albums) == 0 {
			return
		}
		log.Printf("[music-enrich] %d Alben ohne Cover, starte MusicBrainz-Suche", len(albums))
		w.runCoverBatch(ctx, albums)
		if len(albums) < musicPhaseBatchLimit {
			return
		}
	}
}

func (w *MusicWorker) runCoverBatch(ctx context.Context, albums []model.MusicAlbum) {
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

// runMetadataPhase backfillt Genre + Jahr für Alben, denen eines von beiden
// fehlt (siehe PendingMusicMetadataAlbums-Kommentar für die genaue
// Abgrenzung zu runCoverPhase). User-Wunsch 2026-09-06: "Viele Titel haben
// zum Beispiel kein Genre" — eingebettete Tags bleiben die primäre Quelle
// (Store.ApplyMusicBrainzMetadata überschreibt nie einen vorhandenen Wert),
// das hier ist reiner Fallback.
func (w *MusicWorker) runMetadataPhase(ctx context.Context) {
	for batchNum := 0; batchNum < musicPhaseMaxBatches; batchNum++ {
		if ctx.Err() != nil {
			return
		}
		albums, err := w.store.PendingMusicMetadataAlbums(musicPhaseBatchLimit)
		if err != nil {
			log.Printf("[music-enrich] PendingMusicMetadataAlbums: %v", err)
			return
		}
		if len(albums) == 0 {
			return
		}
		log.Printf("[music-enrich] %d Alben ohne Genre/Jahr, starte MusicBrainz-Metadaten-Suche", len(albums))
		w.runMetadataBatch(ctx, albums)
		if len(albums) < musicPhaseBatchLimit {
			return
		}
	}
}

func (w *MusicWorker) runMetadataBatch(ctx context.Context, albums []model.MusicAlbum) {
	for _, album := range albums {
		if ctx.Err() != nil {
			return
		}
		match, err := w.mb.SearchRelease(ctx, album.Artist, album.Album)
		if err != nil {
			log.Printf("[music-enrich] SearchRelease (Metadaten) %s/%s: %v", album.Artist, album.Album, err)
			continue
		}
		if match == nil {
			// Kein Treffer — als "versucht" markieren (verhindert Endlos-Retry).
			if err := w.store.ApplyMusicBrainzMetadata(album.ID, "", 0, ""); err != nil {
				log.Printf("[music-enrich] ApplyMusicBrainzMetadata (kein Treffer) album=%d: %v", album.ID, err)
			}
			continue
		}
		genre := ""
		if album.Genre == "" {
			g, err := w.mb.LookupReleaseGenres(ctx, match.MBID)
			if err != nil {
				log.Printf("[music-enrich] LookupReleaseGenres %s: %v", match.MBID, err)
			} else {
				genre = g
			}
		}
		if err := w.store.ApplyMusicBrainzMetadata(album.ID, match.MBID, match.Year, genre); err != nil {
			log.Printf("[music-enrich] ApplyMusicBrainzMetadata album=%d: %v", album.ID, err)
		}
	}
}
