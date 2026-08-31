package ocrsub

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/boernie77/goldfish/internal/store"
)

const (
	pollInterval      = 30 * time.Second
	settingEnabledKey = "ocr_subs_enabled"
)

// Status ist der Live-Zustand für das Admin-Dialog.
type Status struct {
	Enabled       bool           `json:"enabled"`
	Running       bool           `json:"running"`
	CurrentItemID int64          `json:"currentItemId"`
	CurrentTitle  string         `json:"currentTitle"`
	Counts        map[string]int `json:"counts"`
	LastError     string         `json:"lastError,omitempty"`
	ToolMissing   bool           `json:"toolMissing"`
}

type Worker struct {
	store     *store.Store
	configDir string
	ffmpeg    string
	pgsrip    string

	mu      sync.Mutex
	status  Status
	trigger chan struct{}

	pauseCheck func() bool
}

func New(s *store.Store, configDir, ffmpegPath, pgsripPath string) *Worker {
	return &Worker{
		store:     s,
		configDir: configDir,
		ffmpeg:    ffmpegPath,
		pgsrip:    pgsripPath,
		trigger:   make(chan struct{}, 1),
	}
}

func (w *Worker) SetPauseCheck(fn func() bool) { w.pauseCheck = fn }
func (w *Worker) paused() bool                 { return w.pauseCheck != nil && w.pauseCheck() }

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

func (w *Worker) enabled() bool {
	v, _ := w.store.GetSetting(settingEnabledKey, "false")
	return v == "true"
}

// SetEnabled schaltet den Worker global an/aus und triggert bei An sofort.
func (w *Worker) SetEnabled(on bool) error {
	val := "false"
	if on {
		val = "true"
	}
	if err := w.store.SetSetting(settingEnabledKey, val); err != nil {
		return err
	}
	if on {
		w.Trigger()
	}
	return nil
}

// EnqueueBacklogAndRun reiht alle noch nicht verarbeiteten Items in
// aktivierten Ordnern ein (User-Aktion "alle jetzt erzeugen") und triggert.
func (w *Worker) EnqueueBacklogAndRun() (int, error) {
	n, err := w.store.EnqueueOCRSubBacklog()
	if err != nil {
		return 0, err
	}
	if n > 0 {
		log.Printf("[ocrsub] %d Items neu eingereiht", n)
	}
	w.Trigger()
	return n, nil
}

// EnqueueNewItems wird vom Scanner.OnComplete-Hook aufgerufen — neue Dateien
// mit Bild-Untertiteln in aktivierten Ordnern bekommen automatisch einen Job.
func (w *Worker) EnqueueNewItems() {
	n, err := w.store.EnqueueOCRSubBacklog()
	if err != nil {
		log.Printf("[ocrsub] EnqueueNewItems: %v", err)
		return
	}
	if n > 0 {
		log.Printf("[ocrsub] Scan-Ende: %d neue Items eingereiht", n)
		w.Trigger()
	}
}

func (w *Worker) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	st := w.status
	st.Enabled = w.enabled()
	if counts, err := w.store.CountOCRSubJobsByStatus(); err == nil {
		st.Counts = counts
	}
	st.ToolMissing = w.pgsrip == ""
	return st
}

func (w *Worker) setRunning(id int64, title string) {
	w.mu.Lock()
	w.status.Running = id != 0
	w.status.CurrentItemID = id
	w.status.CurrentTitle = title
	w.mu.Unlock()
}

func (w *Worker) Run(ctx context.Context) {
	if err := w.store.ResetRunningOCRSubJobs(); err != nil {
		log.Printf("[ocrsub] reset running: %v", err)
	}
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		w.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-w.trigger:
		}
	}
}

// drain arbeitet die Warteschlange leer (ein Job nach dem anderen), solange
// aktiviert und nicht pausiert.
func (w *Worker) drain(ctx context.Context) {
	if w.pgsrip == "" {
		return // Tool fehlt im Image
	}
	for {
		if ctx.Err() != nil || !w.enabled() {
			return
		}
		if err := w.waitWhilePaused(ctx); err != nil {
			return
		}
		job, ok, err := w.store.NextPendingOCRSubJob()
		if err != nil {
			log.Printf("[ocrsub] NextPendingOCRSubJob: %v", err)
			return
		}
		if !ok {
			return
		}
		w.runJob(ctx, job.ItemID)
	}
}

func (w *Worker) runJob(ctx context.Context, itemID int64) {
	it, _ := w.store.GetItem(itemID)
	title := ""
	if it != nil {
		if it.RelPath != "" {
			title = it.RelPath
		} else {
			title = it.Title
		}
	}
	w.setRunning(itemID, title)
	defer w.setRunning(0, "")

	_ = w.store.SetOCRSubJobRunning(itemID)
	// Eigener Timeout pro Item (OCR eines Films ~2–10 min je nach Cue-Anzahl).
	jctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()

	langs, err := w.processItem(jctx, itemID)
	if err != nil {
		log.Printf("[ocrsub] item %d FEHLER: %v", itemID, err)
		_ = w.store.SetOCRSubJobFailed(itemID, err.Error())
		w.mu.Lock()
		w.status.LastError = err.Error()
		w.mu.Unlock()
		return
	}
	joined := ""
	for i, l := range langs {
		if i > 0 {
			joined += ","
		}
		joined += l
	}
	_ = w.store.SetOCRSubJobDone(itemID, joined)
	log.Printf("[ocrsub] item %d fertig: %s", itemID, joined)
}
