package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/boernie77/goldfish/internal/api"
	"github.com/boernie77/goldfish/internal/enrich"
	"github.com/boernie77/goldfish/internal/introskip"
	"github.com/boernie77/goldfish/internal/nameparser"
	"github.com/boernie77/goldfish/internal/ocrsub"
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
	backfillIntroSkipOutliers(db)
	backfillIntroSkipDisableAllExceptChuckS2(db)
	backfillOrphanedLibraryPaths(db)

	// Generisches Hardening (siehe internal/store/hardening.go): optionale,
	// rein per Env-Var konfigurierte Liste von Bibliotheks-NAMEN, die für
	// JEDEN Non-Admin immer gesperrt bleiben — unabhängig davon, was in der
	// Benutzerverwaltung eingestellt wird. Bewusst nicht in irgendeiner
	// Konfigurationsdatei im Repo, nur als Laufzeit-Env-Var — leer/unset ist
	// ein reines No-op.
	if v := os.Getenv("GOLDFISH_FORCE_ADMIN_ONLY_LIBRARIES"); v != "" {
		db.SetForceAdminOnlyLibraries(strings.Split(v, ","))
	}

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

	// IntroSkip-Worker (Vorspann-Erkennung via Audio-Fingerprint-Korrelation)
	introSkipWorker := introskip.New(db, configDir)
	// Pausiert automatisch, solange ein Library-Scan läuft — beide sind sehr
	// I/O-intensiv (ffmpeg/fpcalc bzw. ffprobe) auf demselben Netzwerk-Mount.
	// Ohne das kollidieren sie: real beobachtet massenhaft
	// "ffprobe: exit status 1" während eines Scans, weil zeitgleich ein
	// Introskip-Job mit 125 Episoden lief (2026-08-13).
	introSkipWorker.SetPauseCheck(func() bool { return sc.Status().Running })
	go introSkipWorker.Run(workerCtx)

	// OCR-Untertitel-Worker (Bild-Untertitel PGS/VOBSUB → Text per Tesseract).
	// `pgsrip` kommt aus dem Image (Dockerfile) — fehlt es, no-opt der Worker.
	pgsripPath, _ := exec.LookPath("pgsrip")
	if pgsripPath == "" {
		log.Printf("[ocrsub] pgsrip nicht gefunden — OCR-Untertitel deaktiviert")
	}
	ocrSubWorker := ocrsub.New(db, configDir, "ffmpeg", pgsripPath)
	ocrSubWorker.SetPauseCheck(func() bool { return sc.Status().Running })
	go ocrSubWorker.Run(workerCtx)

	// Nach jedem Scan alle Hintergrund-Worker anstoßen.
	// `EnrichAllFoldersNow` arbeitet den TV-Backlog folder-weise ab, damit bei
	// mehreren tausend unmatched Items die Queue-Reihenfolge des 5-Min-Tickers
	// keine Show auf Ewigkeit in der Warteschleife hält.
	sc.OnComplete(func() {
		enricher.Trigger()
		enricher.EnrichAllFoldersNow()
		trickplayWorker.Trigger()
		introSkipWorker.EnqueueStaleFolders()
		ocrSubWorker.EnqueueNewItems()
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
		IntroSkip: introSkipWorker,
		OCRSub:    ocrSubWorker,
		SubsDir:   filepath.Join(configDir, "subs"),
		ConfigDir: configDir,
		PosterDir: filepath.Join(configDir, "posters"),
		WebFS:     webassets.FS(),
		OIDC:      api.NewOIDCRuntime(oidcCfg),
	}
	srv.ApplyTranslationBackend()
	srv.BackfillAllWatchLinksOnStartup()

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

// backfillOrphanedLibraryPaths läuft einmal beim Container-Start (Fix
// 2026-09-02, User-Report "UNIQUE constraint failed: libraries.path" beim
// Anlegen einer neuen Bibliothek) und repariert Libraries, deren alte
// Single-Path-Spalte `libraries.path` noch einen bereits über "Entfernen"
// gelöschten Pfad trägt — siehe Kommentar bei `Store.DeleteLibraryPath`.
// Ohne diesen Backfill bliebe der Karteileichen-Pfad UNIQUE-blockiert und
// könnte nie wieder für eine neue Bibliothek verwendet werden.
func backfillOrphanedLibraryPaths(db *store.Store) {
	done, _ := db.GetSetting("library_path_repair_v1", "")
	if done == "1" {
		return
	}
	fixed, err := db.RepairOrphanedLibraryPaths()
	if err != nil {
		log.Printf("[backfill] library-path-repair: %v", err)
		return
	}
	_ = db.SetSetting("library_path_repair_v1", "1")
	if fixed > 0 {
		log.Printf("[backfill] %d Bibliothek(en) mit verwaistem Pfad repariert", fixed)
	}
}

// backfillIntroSkipOutliers läuft einmal beim Container-Start und bereinigt
// bereits gespeicherte Intro-Erkennungs-Ergebnisse (items.intro_start_sec/
// intro_end_sec) um Ausreißer, OHNE die Episoden erneut zu fingerprinten
// (das wäre bei tausenden Episoden zu teuer — reines ffmpeg+fpcalc-Neu-
// Processing würde je nach Bibliotheksgröße Stunden dauern). Grund: vor
// diesem Fix (2026-08-12) konnte die pärchenweise Korrelation bei
// einzelnen Episoden ein anderes wiederkehrendes Audio-Motiv statt des
// echten (frühen) Vorspanns treffen (z.B. Abspann-Musik bei ~67% der
// Laufzeit) — reale Beobachtung an mehreren Serien in Produktion. Wendet
// dieselbe Median-Ausreißer-Logik wie internal/introskip/worker.go
// filterOutliers auf die bereits gespeicherten Werte an (dupliziert
// bewusst statt Import, um keinen store→introskip-Zyklus zu erzeugen).
func backfillIntroSkipOutliers(db *store.Store) {
	// v3 ersetzt v2 komplett (nicht additiv). v1 filterte nur nachträglich
	// am schon gespeicherten Ergebnis mit einem simplen globalen Median
	// herum — bei Serien, bei denen das FALSCHE wiederkehrende Audio-Motiv
	// (z.B. Abspann-Musik) öfter traf als das echte, frühe Intro, hat das
	// versehentlich die RICHTIGEN Treffer gelöscht und die falschen
	// behalten (z.B. "Abbott Elementary": 6 echte vs. 14 falsche Treffer —
	// v1 behielt die 14 falschen). v2 setzte die Item-Daten korrekt zurück,
	// nutzte zum Neu-Einreihen aber `UpsertIntroSkipJob` — das springt NUR
	// bei Jobs mit status='failed' auf 'pending' zurück (per SQL-WHERE),
	// bei bereits 'done' markierten Jobs (der Normalfall hier) war es ein
	// stiller No-op. Ergebnis: Item-Daten korrekt gelöscht, aber die Jobs
	// blieben auf 'done' stehen und wurden vom Worker nie erneut gezogen
	// (`ListPendingIntroSkipJobs` filtert auf status='pending') — reale
	// Beobachtung 2026-08-12: User sah leere Item-Daten, aber Job-Status
	// weiterhin "Fertig" mit den alten Zahlen. v3 nutzt stattdessen
	// `ForceRetryIntroSkipJob` (setzt IMMER auf 'pending', unabhängig vom
	// aktuellen Status).
	done, _ := db.GetSetting("intro_skip_outlier_backfill_v3", "")
	if done == "1" {
		return
	}
	folders, err := db.ListAllIntroSkipFolders()
	if err != nil {
		log.Printf("[backfill] introskip-outliers-v3: %v", err)
		return
	}
	reset := 0
	for _, f := range folders {
		if err := db.ResetIntroDataForFolder(f.LibraryID, f.Folder); err != nil {
			log.Printf("[backfill] introskip-outliers-v3 %s: reset: %v", f.Folder, err)
			continue
		}
		if err := db.ForceRetryIntroSkipJob(f.LibraryID, f.Folder); err != nil {
			log.Printf("[backfill] introskip-outliers-v3 %s: re-enqueue: %v", f.Folder, err)
			continue
		}
		reset++
	}
	_ = db.SetSetting("intro_skip_outlier_backfill_v3", "1")
	log.Printf("[backfill] introskip-outliers-v3: %d Serien-Ordner für komplette Neuanalyse zurückgesetzt (korrigierter Algorithmus, Jobs korrekt auf pending)", reset)
}

// backfillIntroSkipDisableAllExceptChuckS2 läuft EINMALIG beim ersten Start
// nach der Umstellung auf den Jellyfin-abgeleiteten Algorithmus (Inverted-
// Index + Bild-Fingerprint-Gegenprüfung + echtes Paarweise-Vergleichen,
// siehe internal/introskip/correlate.go). Explizite User-Anweisung
// (2026-08-12): "Wenn du es geändert hast, dann deaktiviere die Erkennung
// für alle Serien und wir testen es erstmal nur an Staffel 2 von Chuck" —
// der neue Algorithmus soll kontrolliert an einer einzigen, bekannten
// Staffel verifiziert werden, bevor er wieder für alle bisher aktivierten
// Serien läuft. Deaktiviert alle bestehenden Ordner-Opt-ins
// (intro_skip_folders) und aktiviert danach NUR den Chuck-Ordner
// (Namensvergleich am letzten Pfadsegment), beschränkt auf Staffel 2.
// Der globale Schalter (settings.introskip_enabled) bleibt unangetastet —
// er wird separat im Admin-Dialog gesetzt.
func backfillIntroSkipDisableAllExceptChuckS2(db *store.Store) {
	done, _ := db.GetSetting("intro_skip_disable_all_except_chuck_s2_v1", "")
	if done == "1" {
		return
	}
	folders, err := db.ListAllIntroSkipFolders()
	if err != nil {
		log.Printf("[backfill] introskip-disable-all-except-chuck-s2: %v", err)
		return
	}
	var chuck *store.FolderSelector
	for i := range folders {
		f := folders[i]
		base := f.Folder
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}
		if strings.EqualFold(base, "Chuck") {
			chuckCopy := f
			chuck = &chuckCopy
			continue // Chuck selbst NICHT deaktivieren, wird unten gezielt (re-)konfiguriert
		}
		if err := db.SetIntroSkipFolder(f.LibraryID, f.Folder, false); err != nil {
			log.Printf("[backfill] introskip-disable-all-except-chuck-s2 %s: %v", f.Folder, err)
		}
	}
	if chuck != nil {
		if err := db.SetIntroSkipFolderSeason(chuck.LibraryID, chuck.Folder, 2); err != nil {
			log.Printf("[backfill] introskip-disable-all-except-chuck-s2: chuck season: %v", err)
		}
		if err := db.ResetIntroDataForFolder(chuck.LibraryID, chuck.Folder); err != nil {
			log.Printf("[backfill] introskip-disable-all-except-chuck-s2: chuck reset: %v", err)
		}
		if err := db.ForceRetryIntroSkipJob(chuck.LibraryID, chuck.Folder); err != nil {
			log.Printf("[backfill] introskip-disable-all-except-chuck-s2: chuck re-enqueue: %v", err)
		}
		log.Printf("[backfill] introskip-disable-all-except-chuck-s2: alle anderen Serien deaktiviert, Chuck auf Staffel 2 beschränkt und für Neuanalyse eingereiht (libID=%d folder=%s)", chuck.LibraryID, chuck.Folder)
	} else {
		log.Printf("[backfill] introskip-disable-all-except-chuck-s2: alle Serien deaktiviert, Chuck-Ordner war nicht aktiviert — bitte manuell im Admin-Dialog aktivieren und auf Staffel 2 beschränken")
	}
	_ = db.SetSetting("intro_skip_disable_all_except_chuck_s2_v1", "1")
}
