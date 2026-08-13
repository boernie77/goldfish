// Package introskip erkennt Vorspann-/Opening-Sequenzen von Serien-Episoden
// automatisch via Audio-Fingerprint-Korrelation (Chromaprint/fpcalc) über
// mehrere Episoden derselben Show hinweg — analog zu Jellyfins
// "Intro Skipper"-Plugin. Ein Job = ein Serien-Ordner, da die Erkennung
// immer alle Episoden einer Show gemeinsam vergleicht.
package introskip

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/boernie77/goldfish/internal/store"
)

const (
	pollInterval = 10 * time.Second

	// minEpisodesRequired: Cross-Episode-Vergleich braucht mindestens 2
	// Episoden. Einzelepisoden-"Shows" bekommen keinen Skip-Button — das ist
	// eine inhärente Grenze der Methode, kein Fehler.
	minEpisodesRequired = 2

	settingEnabledKey = "introskip_enabled"

	// minAgreeObservations: bei echtem Paarweise-Vergleich (jede Episode
	// gegen JEDE andere) müssen mindestens so viele unabhängige
	// Referenz-Episoden zum selben Ergebnis kommen, BEVOR es akzeptiert
	// wird — plus die Bild-Gegenprüfung pro einzelner Paarung (siehe
	// verifyVideoMatch in correlate.go). Zwei Signale (Audio-Konsens UND
	// Bild-Bestätigung) statt nur einem war die explizite User-Anforderung
	// ("Ton nicht mit Bild kombinieren und auf Paarweise umsteigen") für
	// höhere Zuverlässigkeit auf Kosten der Laufzeit.
	minAgreeObservations = 2
)

// Status spiegelt den aktuellen Worker-Zustand für die Status-API.
type Status struct {
	Running          bool   `json:"running"`
	Queue            int    `json:"queue"`
	CurrentLibraryID int64  `json:"currentLibraryId,omitempty"`
	CurrentFolder    string `json:"currentFolder,omitempty"`
	EpisodesTotal    int    `json:"episodesTotal,omitempty"`
	EpisodesDone     int    `json:"episodesDone,omitempty"`
}

type Worker struct {
	store     *store.Store
	configDir string

	mu     sync.Mutex
	status Status

	trigger chan struct{}

	// pauseCheck: wenn gesetzt und true zurückgibt, pausiert der Worker
	// (startet keinen neuen Job, unterbricht die Episode-Fingerprint-
	// Schleife eines laufenden Jobs zwischen zwei Episoden). Vom
	// Scanner-Status gespeist (siehe main.go SetPauseCheck) — die Intro-
	// Erkennung ist sehr I/O-intensiv (ffmpeg+fpcalc pro Episode) und
	// kollidiert sonst mit einem gleichzeitig laufenden Library-Scan auf
	// demselben Netzwerk-Mount (real beobachtet: massenhaft
	// "ffprobe: exit status 1" während eines Scans, weil zeitgleich ein
	// Introskip-Job mit 125 Episoden lief).
	pauseCheck func() bool
}

func New(s *store.Store, configDir string) *Worker {
	return &Worker{
		store:     s,
		configDir: configDir,
		trigger:   make(chan struct{}, 1),
	}
}

// SetPauseCheck registriert die Pause-Bedingung (siehe Worker.pauseCheck).
func (w *Worker) SetPauseCheck(fn func() bool) {
	w.pauseCheck = fn
}

func (w *Worker) paused() bool {
	return w.pauseCheck != nil && w.pauseCheck()
}

// waitWhilePaused blockiert, solange paused() true liefert (kurzes Polling),
// respektiert aber ctx-Abbruch. Wird zwischen zwei Episoden innerhalb eines
// laufenden Jobs aufgerufen, damit ein währenddessen startender Scan nicht
// erst nach dem kompletten (ggf. sehr langen) Job berücksichtigt wird.
func (w *Worker) waitWhilePaused(ctx context.Context) error {
	for w.paused() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil
}

func (w *Worker) Trigger() {
	select {
	case w.trigger <- struct{}{}:
	default:
	}
}

func (w *Worker) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

// RetryFolder setzt den Job eines Ordners zurück auf 'pending' (legt ihn
// bei Bedarf neu an) und triggert den Worker sofort.
func (w *Worker) RetryFolder(libraryID int64, folder string) error {
	if err := w.store.ForceRetryIntroSkipJob(libraryID, folder); err != nil {
		return err
	}
	w.Trigger()
	return nil
}

// EnqueueStaleFolders iteriert alle aktivierten Serien-Ordner und reiht
// jene neu ein, die noch unanalysierte Episoden haben (z.B. nach einem
// Scan mit neuen Folgen einer bereits aktivierten Show). Wird vom
// Scanner.OnComplete-Hook aufgerufen, analog enricher.EnrichAllFoldersNow.
func (w *Worker) EnqueueStaleFolders() {
	folders, err := w.store.ListAllIntroSkipFolders()
	if err != nil {
		log.Printf("[introskip] EnqueueStaleFolders: %v", err)
		return
	}
	queued := 0
	for _, f := range folders {
		season, _, _ := w.store.IntroSkipFolderSeason(f.LibraryID, f.Folder)
		stale, err := w.store.HasUnanalyzedEpisodes(f.LibraryID, f.Folder, season)
		if err != nil {
			log.Printf("[introskip] %s: HasUnanalyzedEpisodes: %v", f.Folder, err)
			continue
		}
		if !stale {
			continue
		}
		if err := w.store.UpsertIntroSkipJob(f.LibraryID, f.Folder); err != nil {
			log.Printf("[introskip] %s: UpsertIntroSkipJob: %v", f.Folder, err)
			continue
		}
		queued++
	}
	if queued > 0 {
		log.Printf("[introskip] %d Ordner mit neuen Episoden neu eingereiht", queued)
		w.Trigger()
	}
}

func (w *Worker) Run(ctx context.Context) {
	if err := w.store.ResetRunningIntroSkipJobs(); err != nil {
		log.Printf("[introskip] reset running jobs: %v", err)
	}

	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		w.runOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-w.trigger:
		}
	}
}

func (w *Worker) enabled() bool {
	v, _ := w.store.GetSetting(settingEnabledKey, "false")
	return v == "true"
}

// runOnce verarbeitet höchstens EINEN Job (Batch-Größe 1, da ein Job = ein
// ganzer Serien-Ordner mit potenziell vielen Episoden — anders als Whisper,
// das mehrere kleine Jobs pro Poll zieht).
func (w *Worker) runOnce(ctx context.Context) {
	if !w.enabled() {
		return
	}
	if w.paused() {
		return
	}
	jobs, err := w.store.ListPendingIntroSkipJobs(1)
	if err != nil {
		log.Printf("[introskip] list jobs: %v", err)
		return
	}
	if len(jobs) == 0 {
		return
	}
	job := jobs[0]

	w.mu.Lock()
	w.status = Status{Running: true, Queue: 1, CurrentLibraryID: job.LibraryID, CurrentFolder: job.Folder}
	w.mu.Unlock()

	if err := w.store.SetIntroSkipJobRunning(job.ID); err != nil {
		log.Printf("[introskip] %s: mark running: %v", job.Folder, err)
		w.mu.Lock()
		w.status = Status{}
		w.mu.Unlock()
		return
	}

	total, matched, err := w.processShow(ctx, job)
	if err != nil {
		log.Printf("[introskip] %s: %v", job.Folder, err)
		_ = w.store.SetIntroSkipJobFailed(job.ID, err.Error())
	} else {
		_ = w.store.SetIntroSkipJobDone(job.ID, total, matched)
		log.Printf("[introskip] %s: fertig (%d/%d Episoden mit erkanntem Intro)", job.Folder, matched, total)
	}

	w.mu.Lock()
	w.status = Status{}
	w.mu.Unlock()
}

type episodeFingerprint struct {
	itemID        int64
	path          string
	frames        []uint32
	frameDuration float64
	videoHashes   []uint64
	durationSec   float64
}

// processShow lädt alle Episoden des Ordners (ggf. eingeschränkt auf eine
// einzelne Staffel, siehe season-Parameter/store.IntroSkipFolderSeason),
// fingerprintet jede per Audio (Chromaprint) UND Bild (dHash), und
// vergleicht dann JEDE Episode gegen JEDE andere (echtes Paarweise-
// Vergleichen statt einer kleinen Referenz-Auswahl — dauert länger,
// liefert aber ein robusteres Ergebnis: siehe minAgreeObservations).
// Ergebnisse werden direkt pro Episode via SetItemIntroRange geschrieben.
// Ein einzelner fpcalc/ffmpeg-Fehler bei EINER Episode lässt den restlichen
// Job nicht scheitern (diese Episode bleibt "nicht analysiert" und wird
// beim nächsten EnqueueStaleFolders-Lauf erneut versucht).
func (w *Worker) processShow(ctx context.Context, job store.IntroSkipJob) (total, matched int, err error) {
	episodes, _, err := w.store.SeriesOwnedEpisodes(job.LibraryID, job.Folder)
	if err != nil {
		return 0, 0, fmt.Errorf("episoden laden: %w", err)
	}
	if season, _, err := w.store.IntroSkipFolderSeason(job.LibraryID, job.Folder); err == nil && season > 0 {
		filtered := episodes[:0]
		for _, e := range episodes {
			if e.Season == season {
				filtered = append(filtered, e)
			}
		}
		episodes = filtered
	}
	total = len(episodes)
	if total < minEpisodesRequired {
		log.Printf("[introskip] %s: nur %d Episode(n) — Cross-Episode-Vergleich nicht möglich", job.Folder, total)
		return total, 0, nil
	}

	var fps []episodeFingerprint
	for _, e := range episodes {
		if ctx.Err() != nil {
			return total, matched, ctx.Err()
		}
		// Zwischen zwei Episoden pausieren, wenn währenddessen ein Scan
		// gestartet wurde — die ffmpeg/fpcalc-I/O-Last würde sonst mit dem
		// Scan um denselben Netzwerk-Mount konkurrieren (siehe
		// Worker.pauseCheck-Kommentar).
		if err := w.waitWhilePaused(ctx); err != nil {
			return total, matched, err
		}
		item, err := w.store.GetItem(e.ItemID)
		if err != nil || item == nil {
			log.Printf("[introskip] item %d: nicht gefunden, übersprungen", e.ItemID)
			continue
		}
		fp, err := extractFingerprint(ctx, item.Path)
		if err != nil {
			log.Printf("[introskip] item %d: audio-fingerprint fehlgeschlagen: %v", item.ID, err)
			continue
		}
		vh, err := extractVideoHashes(ctx, item.Path)
		if err != nil {
			log.Printf("[introskip] item %d: bild-fingerprint fehlgeschlagen: %v", item.ID, err)
			continue
		}
		fps = append(fps, episodeFingerprint{
			itemID: item.ID, path: item.Path,
			frames: fp.frames, frameDuration: fp.frameDuration,
			videoHashes: vh, durationSec: item.DurationSec,
		})

		w.mu.Lock()
		w.status.EpisodesTotal = total
		w.status.EpisodesDone = len(fps)
		w.mu.Unlock()
	}
	if len(fps) < minEpisodesRequired {
		log.Printf("[introskip] %s: zu wenige erfolgreich gefingerprintete Episoden (%d von %d)", job.Folder, len(fps), total)
		return total, 0, nil
	}

	minAgree := minAgreeObservations
	if minAgree > len(fps)-1 {
		minAgree = len(fps) - 1 // bei sehr wenigen Episoden Anforderung absenken statt nie zu treffen
	}

	for ci, cand := range fps {
		if ctx.Err() != nil {
			return total, matched, ctx.Err()
		}
		var obs []observation
		for ri, ref := range fps {
			if ri == ci {
				continue
			}
			startSec, endSec, shiftSec, ok := detectIntro(ref.frames, cand.frames, cand.frameDuration)
			if !ok {
				continue
			}
			// Bild-Gegenprüfung: nur akzeptieren, wenn der audio-gefundene
			// Bereich auch VISUELL mit der Referenz übereinstimmt — filtert
			// wiederverwendetes Audio ohne zugehörige Vorspann-Bilder raus.
			if !verifyVideoMatch(ref.videoHashes, cand.videoHashes, startSec, endSec, shiftSec) {
				continue
			}
			obs = append(obs, observation{startSec: startSec, endSec: endSec})
		}
		if agg, ok := aggregateObservations(obs, minAgree); ok {
			start, end := agg.startSec, agg.endSec
			_ = w.store.SetItemIntroRange(cand.itemID, &start, &end)
			matched++
		} else {
			_ = w.store.SetItemIntroRange(cand.itemID, nil, nil)
		}
	}

	return total, matched, nil
}
