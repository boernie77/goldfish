package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/boernie77/goldfish/internal/api"
	"github.com/boernie77/goldfish/internal/enrich"
	"github.com/boernie77/goldfish/internal/nameparser"
	"github.com/boernie77/goldfish/internal/omdb"
	"github.com/boernie77/goldfish/internal/playback"
	"github.com/boernie77/goldfish/internal/scanner"
	"github.com/boernie77/goldfish/internal/store"
	"github.com/boernie77/goldfish/internal/tmdb"
	"github.com/boernie77/goldfish/internal/trickplay"
	"github.com/boernie77/goldfish/internal/webassets"
	"github.com/boernie77/goldfish/internal/whisper"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)

	configDir := env("VP_CONFIG_DIR", "/config")
	addr := env("VP_LISTEN", ":8096")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		log.Fatalf("config dir: %v", err)
	}

	db, err := store.Open(filepath.Join(configDir, "videoplayer.db"))
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer func() { _ = db.Close() }()

	backfillEpisodeRanges(db)

	hw := playback.Detect()
	// Settings-Override: User kann VAAPI/NVENC/Software erzwingen.
	hwMode, _ := db.GetSetting("hwaccel_mode", "auto")
	hw.ApplySelection(hwMode)
	pb := playback.NewManager(filepath.Join(configDir, "cache"), hw)
	sc := scanner.New(db, filepath.Join(configDir, "thumbs"))

	// TMDB + OMDb Enrichment-Worker (Keys aus Settings, können leer sein)
	tmdbKey, _ := db.GetSetting("tmdb_api_key", "")
	tmdbClient := tmdb.New(tmdbKey, "de-DE")
	omdbKey, _ := db.GetSetting("omdb_api_key", "")
	omdbClient := omdb.New(omdbKey)
	enricher := enrich.New(db, tmdbClient, filepath.Join(configDir, "posters"))
	enricher.SetOMDb(omdbClient)
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	go enricher.Run(workerCtx)

	// Trickplay-Worker (Sprite-Sheet + VTT für Hover-Preview)
	trickplayWorker := trickplay.New(db, filepath.Join(configDir, "trickplay"))
	if hw.VAAPIAvailable {
		trickplayWorker.SetHWAccelDevice(hw.VAAPIDevice)
	}
	trickplayWorker.SetBackend(string(hw.Selected))
	go trickplayWorker.Run(workerCtx)

	// Whisper-Worker (KI-Untertitel-Generierung)
	whisperWorker := whisper.New(db, configDir)
	go whisperWorker.Run(workerCtx)

	// Nach jedem Scan beide Hintergrund-Worker anstoßen.
	// `EnrichAllFoldersNow` arbeitet den TV-Backlog folder-weise ab, damit bei
	// mehreren tausend unmatched Items die Queue-Reihenfolge des 5-Min-Tickers
	// keine Show auf Ewigkeit in der Warteschleife hält.
	sc.OnComplete(func() {
		enricher.Trigger()
		enricher.EnrichAllFoldersNow()
		trickplayWorker.Trigger()
	})

	oidcCfg := api.OIDCConfig{
		IssuerURL:    env("OIDC_ISSUER_URL", ""),
		ClientID:     env("OIDC_CLIENT_ID", ""),
		ClientSecret: env("OIDC_CLIENT_SECRET", ""),
		RedirectURL:  env("OIDC_REDIRECT_URL", ""),
	}
	if oidcCfg.Enabled() {
		log.Printf("[oidc] aktiviert: issuer=%s redirect=%s", oidcCfg.IssuerURL, oidcCfg.RedirectURL)
	} else {
		log.Printf("[oidc] deaktiviert (OIDC_*-Env nicht vollständig)")
	}

	srv := &api.Server{
		Store:     db,
		Scanner:   sc,
		Playback:  pb,
		HW:        hw,
		Enrich:    enricher,
		Trickplay: trickplayWorker,
		Whisper:   whisperWorker,
		SubsDir:   filepath.Join(configDir, "subs"),
		ConfigDir: configDir,
		PosterDir: filepath.Join(configDir, "posters"),
		WebFS:     webassets.FS(),
		OIDC:      api.NewOIDCRuntime(oidcCfg),
	}
	srv.ApplyTranslationBackend()

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("[http] listening on %s", addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	}()
	go srv.RunAutoScan(workerCtx)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Printf("[http] shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// backfillEpisodeRanges läuft einmal beim Container-Start und parsed
// Dateinamen aller gematchten Episoden-Items, die noch nicht auf Doppelfolgen-
// Ranges geprüft wurden (episode_end=0 + mind. zwei 'E' im Pfad). Erkannte
// Ranges (S07E23E24) werden in items.episode_end geschrieben.
// Abgeschlossene Runs werden über settings.episode_range_backfill_v2 markiert,
// damit der Backfill bei Restart nicht erneut läuft. v2 erfasst zusätzlich
// NxN-Pattern mit "&"-Separator (z. B. "Matlock - 2x01 & 2x02"), die der
// alte Parser ignoriert hat.
func backfillEpisodeRanges(db *store.Store) {
	done, _ := db.GetSetting("episode_range_backfill_v2", "")
	if done == "1" {
		return
	}
	rows, err := db.EpisodeItemsForRangeBackfill()
	if err != nil {
		log.Printf("[backfill] episode-range-scan: %v", err)
		return
	}
	updated := 0
	for _, r := range rows {
		p := nameparser.ParseEpisodeFile(r.Path)
		if p.IsEpisode && p.EpisodeEnd > p.Episode {
			if err := db.SetItemEpisodeEnd(r.ID, p.EpisodeEnd); err == nil {
				updated++
			}
		}
	}
	_ = db.SetSetting("episode_range_backfill_v2", "1")
	if updated > 0 {
		log.Printf("[backfill] %d Doppelfolgen-Items mit episode_end befüllt (von %d Kandidaten)", updated, len(rows))
	} else {
		log.Printf("[backfill] %d Episoden-Kandidaten geprüft, keine Doppelfolgen erkannt", len(rows))
	}
}
