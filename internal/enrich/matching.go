// matching.go -- TMDB-Matching-Logik (Ordner-Shows + Einzel-Items), aus
// worker.go ausgelagert (Schritt 6 der Modularisierung, siehe CLAUDE.md
// "Code-Review 2026-09-06"). Reine Funktionsverschiebung, keine
// Logik-/Signaturaenderung.
package enrich

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/nameparser"
	"github.com/boernie77/goldfish/internal/tmdb"
)

// enrichFolders matcht Top-Level-Ordner in TV-Bibliotheken als Shows.
func (w *Worker) enrichFolders(ctx context.Context) error {
	folders, err := w.store.PendingFolders(200)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.status.FoldersTotal = len(folders)
	w.status.FoldersMatched = 0
	w.mu.Unlock()

	for _, f := range folders {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := w.matchShow(ctx, f.LibraryID, f.Folder); err != nil {
			log.Printf("[enrich] show %q: %v", f.Folder, err)
			continue
		}
		w.mu.Lock()
		w.status.FoldersMatched++
		w.mu.Unlock()
	}
	return nil
}

// enrichItems matcht einzelne Items als Film oder Episode.
func (w *Worker) enrichItems(ctx context.Context) error {
	items, err := w.store.PendingItems(500)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.status.ItemsTotal = len(items)
	w.status.ItemsMatched = 0
	w.status.ItemsFailed = 0
	w.mu.Unlock()

	for _, it := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lib, err := w.store.GetLibrary(it.LibraryID)
		if err != nil || lib == nil {
			continue
		}
		// Musik-Bibliotheken haben ihren eigenen Enrichment-Pfad (MusicBrainz/
		// Cover Art Archive, siehe internal/enrich/music_worker.go) — der
		// TMDB-Worker darf sie nie anfassen, genau wie Privat-Libs.
		if lib.Kind == model.KindPrivate || lib.Kind == model.KindMusic {
			continue
		}
		if err := w.matchItem(ctx, lib, it); err != nil {
			log.Printf("[enrich] %s: %v", it.Path, err)
			w.mu.Lock()
			w.status.ItemsFailed++
			w.mu.Unlock()
			continue
		}
		w.mu.Lock()
		w.status.ItemsMatched++
		w.mu.Unlock()
	}
	return nil
}

func (w *Worker) matchShow(ctx context.Context, libraryID int64, folder string) error {
	parsed := nameparser.ParseFolder(folder)
	if parsed.Title == "" {
		return errors.New("leerer Titel")
	}
	results, err := w.client.SearchTV(ctx, parsed.Title)
	if err != nil {
		return err
	}
	best := pickBest(results, parsed.Year)
	if best == nil {
		// Kein Treffer – setze NULL damit wir nicht endlos retry'en
		_ = w.store.SetFolderMetadata(libraryID, folder, 0)
		return errors.New("keine TMDB-TV-Treffer")
	}
	meta, err := w.fetchShowMetadata(ctx, best.ID)
	if err != nil {
		return err
	}
	if err := w.store.SetFolderMetadata(libraryID, folder, meta.ID); err != nil {
		return err
	}
	log.Printf("[enrich] show %q → %s (%d)", folder, meta.Title, meta.Year)
	return nil
}

func (w *Worker) matchItem(ctx context.Context, lib *model.Library, it model.Item) error {
	// TV-Kontext: Priorität liegt auf explizitem SxxExx in den Ordner-Segmenten —
	// Release-Dateinamen wie "tvs-911-dd51-dl-x264-108.mkv" würden sonst eine
	// zufällige 3-stellige Zahl (911) als S9E11 fehlinterpretieren und ALLE
	// Episoden der Serie auf dieselbe Metadata ziehen.
	var parsed nameparser.Parsed
	if lib.Kind == model.KindTV {
		// 1) Erst im Dateinamen nach SxxExx/NxN suchen (explizite Muster, keine numerischen Raten).
		fileParsed := nameparser.ParseFileStrict(it.Path)
		if fileParsed.IsEpisode {
			parsed = fileParsed
		} else if it.RelPath != "" {
			// 2) Ordner-Segmente rückwärts mit demselben strikten Parser prüfen
			segs := strings.Split(it.RelPath, "/")
			for i := len(segs) - 2; i >= 0; i-- {
				folderParsed := nameparser.ParseSegmentStrict(segs[i])
				if folderParsed.IsEpisode {
					parsed.Season = folderParsed.Season
					parsed.Episode = folderParsed.Episode
					parsed.EpisodeEnd = folderParsed.EpisodeEnd
					parsed.IsEpisode = true
					break
				}
			}
		}
		// 3) Als letztes greift der aggressive Parser auf die Datei (inkl. numerischer
		//    3-4-stelliger Episode-Codes wie "104" → S1E04 für "Derrick 104.avi").
		if !parsed.IsEpisode {
			parsed = nameparser.ParseEpisodeFile(it.Path)
		}
	} else {
		parsed = nameparser.ParseFile(it.Path)
	}

	switch lib.Kind {
	case model.KindMovies:
		// Grundkandidaten: File-Name + alle Ordner-Segmente rückwärts.
		base := []nameparser.Parsed{}
		if parsed.Title != "" {
			base = append(base, parsed)
		}
		if it.RelPath != "" {
			segs := strings.Split(it.RelPath, "/")
			for i := len(segs) - 2; i >= 0; i-- {
				c := nameparser.ParseFile(segs[i])
				if c.Title != "" {
					base = append(base, c)
				}
			}
		}
		// Erweiterte Kandidaten: pro Basis noch De-Leet- und Längstes-Token-Varianten.
		// So werden obfuskierte Releases wie "Undispu73d" → "Undisputed" und
		// vertauschte Titel wie "Sitrb Langsam" → "Langsam" gefunden.
		candidates := []nameparser.Parsed{}
		seen := map[string]struct{}{}
		for _, b := range base {
			for _, v := range nameparser.ExpandCandidates(b) {
				key := strings.ToLower(v.Title) + "|" + itoaLocal(v.Year)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				candidates = append(candidates, v)
			}
		}
		if len(candidates) == 0 {
			return errors.New("leerer Titel auf allen Ebenen")
		}
		// Jeden Kandidaten nacheinander bei TMDB und notfalls OMDb probieren.
		for _, c := range candidates {
			results, err := w.client.SearchMovie(ctx, c.Title, c.Year)
			if err != nil {
				return err
			}
			if best := pickBest(results, c.Year); best != nil {
				meta, err := w.fetchMovieMetadata(ctx, best.ID)
				if err != nil {
					return err
				}
				return w.store.SetItemMetadata(it.ID, meta.ID)
			}
			// OMDb-Fallback — mit Year wenn vorhanden, sonst ohne. Die interne
			// Kaskade in enrichItemViaOMDb probiert zuerst strikten Title-Match,
			// dann Loose-Search, sodass kleine Titel-Abweichungen (z.B.
			// „und" vs „&") toleriert werden. Zu strenge Vorfilter hier würden
			// dem Fallback wieder die Luft nehmen.
			if meta, err := w.enrichItemViaOMDb(ctx, c.Title, c.Year); err == nil && meta != nil {
				log.Printf("[enrich] OMDb-Fallback (%q %d) → %s", c.Title, c.Year, meta.Title)
				return w.store.SetItemMetadata(it.ID, meta.ID)
			}
		}
		return errors.New("kein Film-Treffer auf allen Ebenen (TMDB + OMDb)")

	case model.KindTV:
		if !parsed.IsEpisode {
			return errors.New("kein Episodenformat SxxExx im Namen")
		}
		// Show-ID über den Top-Level-Ordner
		folder := topFolder(it.RelPath)
		if folder == "" {
			return errors.New("episode ohne Show-Ordner")
		}
		showMetaID, err := w.store.GetFolderMetadataID(lib.ID, folder)
		if err != nil {
			return err
		}
		if showMetaID == 0 {
			// "0" heißt entweder "noch nie versucht" (keine folder_metadata-Zeile)
			// ODER "bewusst/automatisch unmatched" (Zeile MIT metadata_id=NULL —
			// TMDB fand nichts, ODER ein Admin hat die Zuordnung über "🚫
			// Zuordnung entfernen" gelöscht). NUR im ersten Fall soll erneut
			// gesucht werden — sonst würde jeder der 5-minütlichen Worker-Läufe
			// eine bewusst entfernte Zuordnung sofort wieder herstellen (User-
			// Report 2026-09-06: "Terra X" war Minuten nach dem Entfernen wieder
			// zugeordnet, weil hier nur auf showMetaID==0 statt auf einen
			// bereits existierenden NULL-Eintrag geprüft wurde).
			hasRow, err := w.store.FolderMetadataRowExists(lib.ID, folder)
			if err != nil {
				return err
			}
			if hasRow {
				return errors.New("Ordner ist bewusst unmatched (kein Auto-Retry)")
			}
			// Show-Match ist noch nie gelaufen – trigger jetzt
			if err := w.matchShow(ctx, lib.ID, folder); err != nil {
				return err
			}
			showMetaID, err = w.store.GetFolderMetadataID(lib.ID, folder)
			if err != nil || showMetaID == 0 {
				return errors.New("show konnte nicht gematcht werden")
			}
		}
		showMeta, err := w.store.GetMetadata(showMetaID)
		if err != nil || showMeta == nil {
			return errors.New("Show-Metadata nicht auffindbar")
		}
		ep, err := w.client.GetEpisode(ctx, showMeta.TMDBID, parsed.Season, parsed.Episode)
		if err != nil {
			return err
		}
		epMeta := &model.Metadata{
			TMDBType:    "episode",
			TMDBID:      ep.ID,
			ParentID:    showMeta.ID,
			Title:       ep.Name,
			Year:        showMeta.Year,
			ReleaseDate: tmdb.ParseDate(ep.AirDate),
			Overview:    ep.Overview,
			Rating:      ep.VoteAverage,
			RuntimeMin:  ep.Runtime,
			PosterPath:  ep.StillPath,
			Season:      ep.SeasonNumber,
			Episode:     ep.EpisodeNumber,
		}
		id, err := w.store.UpsertMetadata(epMeta)
		if err != nil {
			return err
		}
		// Episoden-Still als Poster cachen (fällt zurück auf Show-Poster falls leer)
		posterPath := ep.StillPath
		if posterPath == "" {
			posterPath = showMeta.PosterPath
		}
		w.cachePoster(ctx, id, posterPath, "w342")
		w.fetchEpisodeGuests(ctx, id, showMeta.TMDBID, ep.SeasonNumber, ep.EpisodeNumber)
		if err := w.store.SetItemMetadata(it.ID, id); err != nil {
			return err
		}
		// Doppelfolge (S07E23E24) → episode_end auf dem Item setzen. Die
		// zusätzlich mitgerissenen Episoden werden in der Staffel-Ansicht als
		// owned angezeigt, alle zeigen auf dasselbe Item.
		if parsed.EpisodeEnd > parsed.Episode {
			_ = w.store.SetItemEpisodeEnd(it.ID, parsed.EpisodeEnd)
		} else {
			_ = w.store.SetItemEpisodeEnd(it.ID, 0)
		}
		return nil
	}
	return nil
}
