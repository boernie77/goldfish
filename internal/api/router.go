package api

import (
	"context"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/boernie77/goldfish/internal/enrich"
	"github.com/boernie77/goldfish/internal/introskip"
	"github.com/boernie77/goldfish/internal/ocrsub"
	"github.com/boernie77/goldfish/internal/playback"
	"github.com/boernie77/goldfish/internal/scanner"
	"github.com/boernie77/goldfish/internal/store"
	"github.com/boernie77/goldfish/internal/trickplay"
	"github.com/boernie77/goldfish/internal/whisper"
)

type Server struct {
	Store     *store.Store
	Scanner   *scanner.Scanner
	Playback  *playback.Manager
	HW        playback.HWAccel
	Enrich    *enrich.Worker
	Trickplay *trickplay.Worker
	Whisper   *whisper.Worker
	IntroSkip *introskip.Worker
	OCRSub    *ocrsub.Worker
	SubsDir   string    // z.B. /config/subs — Cache für extrahierte Untertitel-VTTs
	ConfigDir string    // z.B. /config — Basis für alle persistenten Daten
	PosterDir string    // z.B. /config/posters — Cache für TMDB-Poster + Custom-Uploads
	WebFS     fs.FS
	OIDC      *OIDCRuntime // optional, nil/disabled wenn OIDC_*-Env nicht gesetzt
	bgCtx     context.Context
}

func (s *Server) Router() http.Handler {
	if s.bgCtx == nil {
		s.bgCtx = context.Background()
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	// gzip-Kompression aller Responses (JSON, HTML, JS, CSS, SVG, VTT).
	// Video-Segmente (mpegts, mp4, m2ts, jpg) sind schon binär-komprimiert →
	// explizit nur komprimierbare Typen.
	r.Use(middleware.Compress(5,
		"text/html", "text/css", "application/javascript",
		"application/json", "text/plain", "text/vtt",
		"image/svg+xml", "application/vnd.apple.mpegurl",
	))
	r.Use(s.authMiddleware)

	r.Route("/api", func(r chi.Router) {
		// Auth-Routes (keine Session nötig)
		r.Get("/auth/status", s.authStatus)
		r.Post("/auth/login", s.authLogin)
		r.Post("/auth/logout", s.authLogout)
		r.Post("/auth/setup", s.authSetup)
		r.Put("/auth/password", s.changeMyPassword)
		r.Post("/auth/cast-token", s.castToken)

		// OIDC-SSO via Authentik (optional — nur aktiv wenn OIDC_*-Env gesetzt)
		r.Get("/auth/oidc/login", s.oidcLogin)
		r.Get("/auth/oidc/callback", s.oidcCallback)

		// User-Verwaltung (admin-only)
		r.Get("/users", requireAdmin(s.listUsers))
		r.Post("/users", requireAdmin(s.createUserAdmin))
		r.Delete("/users/{id}", requireAdmin(s.deleteUser))
		r.Put("/users/{id}/password", requireAdmin(s.resetUserPassword))
		r.Put("/users/{id}/admin", requireAdmin(s.setUserAdmin))
		r.Put("/users/{id}/age-rating", requireAdmin(s.setUserAgeRating))
		r.Get("/users/{id}/libraries", requireAdmin(s.getUserLibraries))
		r.Put("/users/{id}/libraries", requireAdmin(s.setUserLibraries))

		r.Get("/health", s.handleHealth)
		r.Get("/home", s.home)

		r.Get("/libraries", s.listLibraries)
		r.Post("/libraries", requireAdmin(s.createLibrary))
		r.Delete("/libraries/{id}", requireAdmin(s.deleteLibrary))
		r.Get("/libraries/{id}/folders", s.listFolders)
		r.Put("/libraries/{id}/folders/drilldown", requireAdmin(s.setFolderDrilldown))
		r.Put("/libraries/{id}/home-visibility", requireAdmin(s.setLibraryHomeVisibility))
		r.Put("/libraries/{id}/channel-label-on-top", requireAdmin(s.setLibraryChannelLabelOnTop))
		r.Put("/libraries/order", requireAdmin(s.setLibraryOrder))
		r.Get("/libraries/{id}/stats", s.libraryStats)
		r.Get("/libraries/{id}/stats-detail", s.libraryStatDetail)
		r.Get("/libraries/{id}/name-dupes", s.nameDupes)
		r.Get("/libraries/{id}/seasons", s.seriesSeasons)
		r.Get("/libraries/{id}/paths", requireAdmin(s.listLibraryPaths))
		r.Post("/libraries/{id}/paths", requireAdmin(s.addLibraryPath))
		r.Delete("/libraries/{id}/paths", requireAdmin(s.deleteLibraryPath))

		r.Get("/items", s.listItems)
		r.Get("/items/random", s.randomItem)
		r.Get("/items/years", s.listYears)
		r.Get("/items/search-path", requireAdmin(s.searchItemsByPath))
		r.Get("/items/suspicious", s.suspiciousMatches)
		r.Get("/items/{id}", s.getItem)
		r.Get("/items/{id}/variants", s.getItemVariants)
		r.Put("/items/{id}/variant-split", requireAdmin(s.setItemVariantSplit))
		r.Delete("/items/{id}", requireAdmin(s.deleteItem))
		r.Get("/download/{id}", s.downloadItem)
		r.Get("/download/{id}/compat-status", s.downloadCompatStatus)
		r.Put("/items/{id}/watched", s.setWatched)
		r.Put("/items/{id}/confirm", s.confirmItemMetadata)
		r.Post("/items/{id}/write-nfo", requireAdmin(s.writeItemNFO))
		r.Post("/items/write-all-nfos", requireAdmin(s.writeAllConfirmedNFOs))
		// Auto-Rename: Preview ist lesend (nicht-admin OK), die schreibenden
		// Endpoints sind admin-only.
		r.Get("/items/{id}/rename-preview", s.renamePreview)
		r.Post("/items/{id}/rename", requireAdmin(s.renameItemNow))
		r.Get("/admin/renames", requireAdmin(s.renameList))
		r.Get("/admin/renames.csv", requireAdmin(s.renameCSV))
		r.Post("/admin/renames/{id}/undo", requireAdmin(s.renameUndo))
		r.Post("/admin/rename-all-confirmed", requireAdmin(s.renameAllConfirmed))
		r.Post("/items/{id}/move", requireAdmin(s.moveItem))
		r.Post("/items/move", requireAdmin(s.moveItemsBulk))
		r.Get("/libraries/{id}/all-folders", requireAdmin(s.listAllFolders))

		// Gesehen-Sync zwischen zwei Usern (kein Admin nötig — jeder User
		// darf für sich selbst einen Partner vorschlagen/bestätigen/trennen)
		r.Get("/users/names", s.listOtherUsernames)
		r.Get("/watch-links", s.listWatchLinks)
		r.Post("/watch-links", s.requestWatchLink)
		r.Post("/watch-links/{partnerId}/confirm", s.confirmWatchLink)
		r.Delete("/watch-links/{partnerId}", s.unlinkWatchLink)

		r.Put("/items/{id}/favorite", s.setFavorite)
		r.Put("/items/{id}/rating", s.setItemRating)
		r.Post("/items/{id}/played", s.touchPlayed)
		r.Put("/items/{id}/resume", s.setResumePos)
		r.Get("/items/{id}/resume", s.getResumePos)

		r.Get("/browse", s.browse)

		r.Get("/thumb/{id}", s.getThumb)

		r.Get("/playback/{id}", s.playbackInfo)
		r.Get("/stream/{id}", s.streamDirect)
		r.Get("/transcode/{id}/index.m3u8", s.transcodePlaylist)
		r.Get("/transcode/{id}/progress", s.transcodeProgress)
		r.Get("/transcode/{id}/{seg}", s.transcodeSegment)

		r.Post("/scan/{id}", requireAdmin(s.startScan))
		r.Post("/scan/all", requireAdmin(s.startScanAll))
		r.Get("/scan/status", s.scanStatus)
		r.Post("/scan/cancel", requireAdmin(s.cancelScan))

		r.Get("/settings", s.getSettings)
		r.Put("/settings", requireAdmin(s.putSettings))
		r.Get("/settings/autoscan", requireAdmin(s.getAutoScan))
		r.Put("/settings/autoscan", requireAdmin(s.putAutoScan))

		// TMDB-Integration
		r.Put("/libraries/{id}/kind", requireAdmin(s.updateLibraryKind))
		r.Post("/enrich/run", requireAdmin(s.runEnrich))
		r.Post("/enrich/unmatch-duplicates", requireAdmin(s.unmatchDuplicates))
		r.Post("/enrich/refresh-collection-parts", requireAdmin(s.refreshCollectionParts))
		r.Get("/enrich/status", s.enrichStatus)
		r.Get("/metadata/search", requireAdmin(s.searchMetadata))
		r.Post("/items/{id}/metadata", requireAdmin(s.setItemMetadata))
		r.Post("/items/{id}/metadata-manual", requireAdmin(s.createCustomMetadata))
		r.Post("/items/{id}/refresh-metadata", requireAdmin(s.refreshItemMetadata))
		r.Post("/enrich/refresh-all-metadata", requireAdmin(s.startRefreshAllMetadata))
		r.Get("/enrich/refresh-all-status", s.refreshAllMetadataStatus)
		r.Put("/metadata/{id}", requireAdmin(s.updateMetadata))
		r.Get("/metadata/{id}/posters", requireAdmin(s.listMetadataPosters))
		r.Post("/metadata/{id}/poster", requireAdmin(s.setMetadataPoster))
		r.Post("/items/merge", requireAdmin(s.mergeItems))
		r.Post("/libraries/{id}/auto-merge-duplicates", requireAdmin(s.autoMergeDuplicates))
		r.Post("/libraries/{id}/folders/metadata", requireAdmin(s.setFolderMetadata))
		r.Post("/libraries/{id}/folders/re-enrich-episodes", requireAdmin(s.reEnrichFolderEpisodes))
		r.Post("/enrich/backfill-age-ratings", requireAdmin(s.backfillAgeRatings))
		r.Get("/poster/metadata/{id}", s.getPoster)

		// Collections
		r.Get("/collections", s.listCollections)
		r.Get("/collections/{id}/items", s.collectionItems)
		r.Post("/collections/{id}/parts/{tmdbMovieId}/hide", s.hideCollectionPart)
		r.Delete("/collections/{id}/parts/{tmdbMovieId}/hide", s.unhideCollectionPart)
		r.Get("/tmdb/movie/{tmdbId}", s.getTMDBMovieDetail)

		// Missing-Export für Radarr/Sonarr-Brücke
		r.Get("/missing/movies", s.missingMovies)
		r.Get("/missing/episodes", s.missingEpisodes)
		r.Get("/poster/collection/{id}", s.getCollectionPoster)

		// Cast/Schauspieler
		r.Get("/metadata/{id}/cast", s.getMetadataCast)
		r.Get("/person/{tmdbId}", s.getPerson)
		r.Get("/person/{tmdbId}/profile", s.getPersonProfile)

		// Playlists
		r.Get("/playlists", s.listPlaylists)
		r.Post("/playlists", s.createPlaylist)
		r.Put("/playlists/{id}", s.renamePlaylist)
		r.Delete("/playlists/{id}", s.deletePlaylist)
		r.Get("/playlists/{id}/items", s.listPlaylistItems)
		r.Post("/playlists/{id}/items", s.addPlaylistItem)
		r.Delete("/playlists/{id}/items/{itemId}", s.removePlaylistItem)
		r.Put("/playlists/{id}/items", s.reorderPlaylist)
		r.Get("/items/{id}/playlists", s.playlistsForItem)

		// Untertitel on-demand als WebVTT extrahieren
		r.Get("/subtitle/{id}/{idx}.vtt", s.subtitleVTT)

		// KI-generierte Untertitel (Whisper)
		r.Post("/items/{id}/generate-subtitle", requireAdmin(s.generateSubtitle))
		r.Get("/items/{id}/subtitle-jobs", s.subtitleJobs)
		r.Delete("/items/{id}/subtitle/{lang}", requireAdmin(s.deleteSubtitle))
		r.Get("/generated-subtitle/{id}/{lang}.vtt", s.serveGeneratedSubtitle)
		r.Get("/ocr-subtitle/{id}/{lang}.vtt", s.serveOCRSubtitle)
		r.Get("/whisper/status", s.whisperStatus)
		r.Get("/whisper/settings", requireAdmin(s.whisperGetSettings))
		r.Put("/whisper/settings", requireAdmin(s.whisperSaveSettings))
		r.Post("/whisper/download-model", requireAdmin(s.whisperDownloadModel))
		r.Get("/whisper/download-status", s.whisperDownloadStatus)

		// Trickplay: Aktivierung admin-only, Konsum für alle
		r.Get("/libraries/{id}/trickplay", requireAdmin(s.listTrickplayFolders))
		r.Put("/libraries/{id}/trickplay", requireAdmin(s.setTrickplayFolder))
		r.Get("/libraries/{id}/trickplay/status", s.folderTrickplayStatus)
		r.Get("/trickplay/status", s.trickplayWorkerStatus)
		r.Post("/trickplay/cancel", requireAdmin(s.cancelTrickplay))
		r.Post("/trickplay/delete-all", requireAdmin(s.deleteAllTrickplay))
		r.Post("/trickplay/retry-failed", requireAdmin(s.retryFailedTrickplay))
		r.Post("/trickplay/items/{id}/retry", requireAdmin(s.retryItemTrickplay))
		r.Get("/trickplay/log", requireAdmin(s.trickplayLog))
		r.Get("/trickplay/{id}/thumbs.vtt", s.trickplayVTT)
		r.Get("/trickplay/{id}/sprite.jpg", s.trickplaySprite)

		// Intro-Erkennung: Verwaltung admin-only, Konsum (Skip-Button-Daten
		// via GetItemFor) für alle. Aktivierung ist bewusst strikt pro
		// einzelnem Serien-Ordner (kein Bibliotheks-weiter Schalter).
		r.Get("/introskip/settings", requireAdmin(s.introSkipGetSettings))
		r.Put("/introskip/settings", requireAdmin(s.introSkipSaveSettings))
		r.Get("/libraries/{id}/introskip", requireAdmin(s.listIntroSkipFolders))
		r.Put("/libraries/{id}/introskip", requireAdmin(s.setIntroSkipFolder))
		r.Get("/libraries/{id}/introskip/episodes", requireAdmin(s.introSkipFolderEpisodes))
		r.Get("/introskip/status", s.introSkipWorkerStatus)
		r.Get("/introskip/log", requireAdmin(s.introSkipLog))
		r.Post("/introskip/folders/{id}/retry", requireAdmin(s.retryIntroSkipFolder))
		r.Post("/introskip/retry-failed", requireAdmin(s.retryFailedIntroSkip))

		// OCR-Untertitel (Bild-Untertitel PGS/VOBSUB → Text per Tesseract)
		r.Get("/ocrsubs/status", requireAdmin(s.ocrSubStatus))
		r.Put("/ocrsubs/settings", requireAdmin(s.ocrSubSetEnabled))
		r.Get("/ocrsubs/folders", requireAdmin(s.ocrSubListFolders))
		r.Put("/ocrsubs/folders", requireAdmin(s.ocrSubSetFolder))
		r.Get("/ocrsubs/log", requireAdmin(s.ocrSubLog))
		r.Post("/ocrsubs/run", requireAdmin(s.ocrSubRunAll))
		r.Post("/ocrsubs/retry-failed", requireAdmin(s.ocrSubRetryFailed))
		r.Post("/ocrsubs/items/{id}/retry", requireAdmin(s.ocrSubRetryItem))
	})

	// Static frontend mit Cache-Control. Da wir kein Hash-Versioning haben,
	// setzen wir einen moderaten max-age. HTML bleibt kurz cached, damit Releases
	// nach dem Deploy zügig im Browser ankommen.
	fileSrv := http.FileServer(http.FS(s.WebFS))
	r.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, ".woff2") || strings.HasSuffix(p, ".woff") ||
			strings.HasSuffix(p, ".ttf") || strings.HasSuffix(p, ".svg"):
			// Fonts/SVGs ändern sich selten — lang cachen.
			w.Header().Set("Cache-Control", "public, max-age=604800")
		case strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".css"):
			// App-Code: jeder Request muss revalidieren, damit Deploys sofort
			// ankommen. ETag via http.ServeContent sorgt für billige 304-Responses,
			// also kein echter Traffic-Overhead.
			w.Header().Set("Cache-Control", "no-cache")
		case strings.HasSuffix(p, ".html") || p == "/" || p == "":
			w.Header().Set("Cache-Control", "no-cache")
		case strings.HasSuffix(p, ".webmanifest"):
			// Go-mime-Package kennt .webmanifest nicht — explizit setzen, sonst
			// warnt der Browser bei der PWA-Installation.
			w.Header().Set("Content-Type", "application/manifest+json")
			w.Header().Set("Cache-Control", "no-cache")
		}
		// Service-Worker muss mit Scope=/ registrierbar sein. http.FileServer
		// setzt den korrekten Content-Type für .js. Wichtig nur: kein Caching,
		// damit Updates des SW sofort greifen.
		if p == "/sw.js" {
			w.Header().Set("Service-Worker-Allowed", "/")
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileSrv.ServeHTTP(w, r)
	}))
	return r
}

// buildTag wird bei jedem Code-Push aktualisiert und ist im /api/health
// sichtbar — schneller Smoke-Test, ob der laufende Container die aktuelle
// Binärversion ist (statt z.B. eines fehlgeschlagenen Redeploys).
const buildTag = "2026-05-02T10:00Z"

// appVersion — semantische Version des Servers. Ab 2026-08-30 offiziell
// versioniert. **Bei JEDEM Deploy die Patch-Stelle um 1 erhöhen** (User-Vorgabe
// 2026-08-31: "Server Version bei jedem deploy um x.x.1 erhöhen"). Wird im
// /api/health ausgeliefert und im Zahnrad-Menü der Web-UI angezeigt.
const appVersion = "1.0.2"

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	resp := map[string]any{
		"status":  "ok",
		"version": appVersion,
		"build":   buildTag,
		"hwaccel": map[string]any{
			"enabled":        s.HW.Selected != playback.BackendSoftware,
			"backend":        string(s.HW.Selected),
			"device":         s.HW.VAAPIDevice,
			"driver":         s.HW.VAAPIDriver,
			"vaapiAvailable": s.HW.VAAPIAvailable,
			"vaapiDriver":    s.HW.VAAPIDriver,
			"nvencAvailable": s.HW.NVENCAvailable,
			"nvencInfo":      s.HW.NVENCInfo,
		},
		"tmdb": map[string]any{
			"enabled": s.Enrich != nil && s.Enrich.Client().Enabled(),
		},
	}
	// Trickplay-Diagnose: Worker-Zustand + Status-Verteilung. Hilfreich für
	// Außen-Checks („läuft der Worker?", „wie viele pending?") ohne Auth.
	if s.Trickplay != nil {
		st := s.Trickplay.Status()
		counts, _ := s.Store.CountTrickplayByStatus()
		resp["trickplay"] = map[string]any{
			"running":          st.Running,
			"startedAt":        st.StartedAt,
			"lastRun":          st.LastRun,
			"sessionTotal":     st.Total,
			"sessionProcessed": st.Processed,
			"sessionFailed":    st.Failed,
			"currentItemId":    st.CurrentItemID,
			"currentTitle":     st.CurrentTitle,
			"counts":           counts, // {""/"pending"/"done"/"failed": N}
		}
	}
	writeJSON(w, 200, resp)
}
