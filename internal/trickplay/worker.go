// Package trickplay erzeugt Hover-Vorschau-Thumbnails ("trickplay") für Items in
// aktivierten Ordnern. Pro Item wird ein Sprite-Sheet (JPEG-Kachel mit allen
// Thumbnails) und ein WebVTT-Manifest erzeugt.
package trickplay

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
)

// Parameter für die Generierung.
// Das Intervall ist nicht mehr const, sondern wird pro Generate-Call aus den Settings gelesen.
const (
	defaultIntervalSec = 10
	minIntervalSec     = 2
	maxIntervalSec     = 60
	tileWidth          = 160
	tileHeight         = 90
)

func (w *Worker) intervalSec() int {
	v, _ := w.store.GetSetting("trickplay_interval_sec", "")
	if v == "" {
		return defaultIntervalSec
	}
	n, err := strconvAtoi(v)
	if err != nil || n < minIntervalSec || n > maxIntervalSec {
		return defaultIntervalSec
	}
	return n
}

// kleiner Wrapper, um strconv nur hier zu importieren
func strconvAtoi(s string) (int, error) { return strconv.Atoi(s) }

type Worker struct {
	store    *store.Store
	outDir   string
	hwDevice string // /dev/dri/renderD128 wenn VAAPI verfügbar, sonst "" (Software-Decode)
	backend  string // "vaapi" | "nvenc" | "software" — zur Laufzeit von main.go gesetzt

	mu       sync.Mutex
	running  bool
	cancelFn context.CancelFunc // gesetzt während runOnce, null sonst
	trigger  chan struct{}
	status   Status
}

// SetHWAccelDevice konfiguriert das VAAPI-Device für Hardware-Decode. Ohne Setup
// (leer) läuft Decode rein in Software, was bei 4K-60fps-Quellen leicht in Timeout
// läuft.
func (w *Worker) SetHWAccelDevice(device string) { w.hwDevice = device }

// SetBackend legt fest, welcher Hardware-Pfad genutzt wird: "vaapi" (bisheriges
// Verhalten), "nvenc" (NVIDIA) oder "software" (libx264). Wird beim Start aus
// den Settings gesetzt und kann live aktualisiert werden.
func (w *Worker) SetBackend(backend string) { w.backend = backend }

// Cancel bricht einen aktuell laufenden Enrichment-Pass ab. Noch nicht verarbeitete
// Items bleiben als "pending" in der Queue und werden beim nächsten Lauf erneut
// probiert.
func (w *Worker) Cancel() {
	w.mu.Lock()
	fn := w.cancelFn
	w.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// OutDir gibt das Wurzelverzeichnis der Trickplay-Ausgabe zurück (z. B. /config/trickplay).
func (w *Worker) OutDir() string { return w.outDir }

// DeleteAll entfernt alle generierten Trickplay-Dateien unter outDir.
// Der trickplay_status der Items wird vom Aufrufer zurückgesetzt (Store-Layer).
func (w *Worker) DeleteAll() error {
	entries, err := os.ReadDir(w.outDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(w.outDir, e.Name()))
	}
	return nil
}

// Status beschreibt den aktuellen Lauf des Workers (für UI-Anzeige).
type Status struct {
	Running          bool      `json:"running"`
	StartedAt        time.Time `json:"startedAt,omitempty"`
	Processed        int       `json:"processed"`          // erfolgreich generiert in dieser Session
	Failed           int       `json:"failed"`             // Fehler in dieser Session
	Total            int       `json:"total"`              // Gesamt in der Queue dieser Session
	CurrentItemID    int64     `json:"currentItemId,omitempty"`
	CurrentTitle     string    `json:"currentTitle,omitempty"`
	CurrentLibraryID int64     `json:"currentLibraryId,omitempty"`
	LastRun          time.Time `json:"lastRun,omitempty"`
}

func New(s *store.Store, outDir string) *Worker {
	_ = os.MkdirAll(outDir, 0o755)
	return &Worker{store: s, outDir: outDir, trigger: make(chan struct{}, 1)}
}

func (w *Worker) Trigger() {
	select {
	case w.trigger <- struct{}{}:
	default:
	}
}

// Status gibt den aktuellen Worker-Status zurück (für API).
func (w *Worker) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

// Run blockiert bis der Kontext endet; löst alle 2 min oder auf Trigger Läufe aus.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()
	time.Sleep(5 * time.Second)
	// Beim Start einmalig hängengebliebene "pending" zurücksetzen. Sie kommen
	// daher, dass der Worker das Item auf "pending" markiert *bevor* ffmpeg
	// startet — bei Container-Restart, Cancel oder Crash bleibt der Status
	// hängen und das Item würde sonst nie wieder aufgegriffen.
	if n, err := w.store.ResetStuckPendingTrickplay(); err == nil && n > 0 {
		log.Printf("[trickplay] %d stuck-pending Items zurückgesetzt → werden in diesem Lauf bearbeitet", n)
	}
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

func (w *Worker) runOnce(ctx context.Context) {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	w.running = true
	w.cancelFn = cancel
	w.status = Status{Running: true, StartedAt: time.Now()}
	w.mu.Unlock()

	// Orphan-Cleanup: Items mit Trickplay-Status, deren Folder (mehr) nicht
	// aktiviert ist (z. B. Altlast aus früheren Versionen ohne Folder-Check).
	w.cleanupOrphans()
	items, err := w.store.PendingTrickplayItems(100)
	if err != nil {
		log.Printf("[trickplay] list pending: %v", err)
		cancel()
		w.mu.Lock()
		w.running = false
		w.cancelFn = nil
		w.status.Running = false
		w.mu.Unlock()
		return
	}
	w.mu.Lock()
	w.status.Total = len(items)
	w.mu.Unlock()

	defer func() {
		cancel()
		w.mu.Lock()
		w.running = false
		w.cancelFn = nil
		w.status.Running = false
		w.status.LastRun = time.Now()
		w.status.CurrentItemID = 0
		w.status.CurrentTitle = ""
		w.status.CurrentLibraryID = 0
		w.mu.Unlock()
	}()

	for _, it := range items {
		if runCtx.Err() != nil {
			return
		}
		w.mu.Lock()
		w.status.CurrentItemID = it.ID
		w.status.CurrentTitle = it.Title
		w.status.CurrentLibraryID = it.LibraryID
		w.mu.Unlock()

		if err := w.generate(runCtx, it); err != nil {
			// Cancel-Fehler nicht als "failed" markieren — bleibt pending für den nächsten Lauf.
			if runCtx.Err() != nil {
				return
			}
			log.Printf("[trickplay] item %d (%s): %v", it.ID, it.Path, err)
			_ = w.store.SetItemTrickplayError(it.ID, "failed", err.Error())
			w.mu.Lock()
			w.status.Failed++
			w.mu.Unlock()
			continue
		}
		_ = w.store.SetItemTrickplayError(it.ID, "done", "")
		w.mu.Lock()
		w.status.Processed++
		w.mu.Unlock()
	}
}

// generate erzeugt sprite.jpg + thumbs.vtt für ein Item.
func (w *Worker) generate(ctx context.Context, it model.Item) error {
	if it.DurationSec <= 0 {
		return errors.New("unbekannte Laufzeit")
	}
	_ = w.store.SetItemTrickplay(it.ID, "pending")
	interval := w.intervalSec()

	tileCount := int(math.Ceil(it.DurationSec / float64(interval)))
	if tileCount < 1 {
		tileCount = 1
	}
	// Quadratisches Grid, dessen Fläche tileCount abdeckt
	gridSide := int(math.Ceil(math.Sqrt(float64(tileCount))))
	gridX, gridY := gridSide, gridSide

	dir := filepath.Join(w.outDir, strconv.FormatInt(it.ID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	sprite := filepath.Join(dir, "sprite.jpg")
	vtt := filepath.Join(dir, "thumbs.vtt")

	// Timeout proportional zur Laufzeit. Großzügig dimensioniert, weil
	// 4K-Files mit langen GOPs trotz -skip_frame nokey noch substantielle
	// Demux-/Parse-Zeit brauchen, und der Software-Fallback bei
	// VAAPI-Aussetzern deutlich langsamer ist.
	timeout := time.Duration(it.DurationSec/5)*time.Second + 5*time.Minute
	maxTimeout := 30 * time.Minute
	if it.Height >= 2160 {
		maxTimeout = 3 * time.Hour
	} else if it.Height >= 1080 {
		maxTimeout = 60 * time.Minute
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Filter-Chains pro Backend. Alle landen am Ende bei einem Sprite-Bild
	// mit `tile=gridX x gridY`-Kacheln; der Weg dorthin unterscheidet sich
	// nur im HW-Upload-/Download-Pfad.
	// format=nv12 in scale_vaapi erzwingt 8-bit-Ausgabe — nötig für 10-bit HDR
	// Quellen (HEVC Main10), bei denen hwdownload sonst p010le statt nv12 bekommt
	// und der nachfolgende Software-Filter die Konversion ablehnt.
	vaapiVF := fmt.Sprintf(
		"fps=1/%d,scale_vaapi=w=%d:h=%d:force_original_aspect_ratio=decrease:format=nv12,hwdownload,format=nv12,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black,tile=%dx%d",
		interval, tileWidth, tileHeight, tileWidth, tileHeight, gridX, gridY,
	)
	swVF := fmt.Sprintf(
		"fps=1/%d,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black,tile=%dx%d",
		interval, tileWidth, tileHeight, tileWidth, tileHeight, gridX, gridY,
	)
	// NVENC-Pfad: Decode auf GPU (cuda), Scale auf CPU (funktioniert mit allen
	// Quell-Codecs), Sprite zusammenbauen in Software. NVENC ist für Sprites
	// nicht nötig — wir speichern nur ein JPEG, kein h264. Der Speed-Gewinn
	// kommt aus HW-Decode bei großen Quelldateien.
	nvencVF := swVF

	buildArgs := func(backend string) []string {
		args := []string{
			"-hide_banner", "-loglevel", "error", "-y",
			// Tolerant gegenüber kaputten Streams — Invalid NAL / POC-Fehler
			// (typisch in alten Release-Encodes) sollen ffmpeg nicht killen,
			// sondern werden übersprungen.
			"-err_detect", "ignore_err",
			"-fflags", "+discardcorrupt+genpts",
			// Decoder gibt nur Keyframes raus → 4K-60fps-Files werden 30-100×
			// schneller verarbeitet (kein Linear-Decode aller Inter-Frames).
			// Bei interval=10s und typischem Keyframe-Abstand ≤5s ist jeder
			// Sprite-Slot weiterhin nahe genug am Soll-Timestamp.
			"-skip_frame", "nokey",
		}
		switch backend {
		case "vaapi":
			args = append(args,
				"-hwaccel", "vaapi",
				"-hwaccel_device", w.hwDevice,
				"-hwaccel_output_format", "vaapi",
			)
			args = append(args, "-i", it.Path, "-vf", vaapiVF)
		case "nvenc":
			// Nur -hwaccel cuda, KEIN -hwaccel_output_format cuda: Frames
			// landen im CPU-RAM, die bestehende Software-Filter-Chain
			// (scale/pad/tile) läuft ohne hwdownload. Für Trickplay mit
			// fps=0.1 fallen wenige Frames an — GPU→CPU-Transfer ist
			// vernachlässigbar, und die Chain funktioniert mit allen
			// Filter-Versionen.
			args = append(args, "-hwaccel", "cuda")
			args = append(args, "-i", it.Path, "-vf", nvencVF)
		default:
			args = append(args, "-i", it.Path, "-vf", swVF)
		}
		return append(args, "-frames:v", "1", "-q:v", "4", "-an", "-sn", sprite)
	}

	// Primary-Backend wählen:
	//   - User-Override (w.backend)
	//   - sonst VAAPI wenn Device + Codec passen
	//   - sonst Software
	primary := w.backend
	if primary == "" {
		if w.hwDevice != "" && vaapiSupportsCodec(it.VideoCodec) {
			primary = "vaapi"
		} else {
			primary = "software"
		}
	}
	if primary == "vaapi" && (w.hwDevice == "" || !vaapiSupportsCodec(it.VideoCodec)) {
		primary = "software"
	}

	run := func(backend string) (string, error) {
		cmd := exec.CommandContext(tctx, "ffmpeg", buildArgs(backend)...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run(primary)
	// Runtime-Fail-Fallback: bei HW-Init-Problemen auf Software weichen.
	// Erkennungsmerkmale (treffen für VAAPI und NVENC): „hwaccel initialisation",
	// „Function not implemented", „No support for codec", „Cannot load cuda".
	if err != nil && primary != "software" &&
		(strings.Contains(out, "hwaccel initialisation") ||
			strings.Contains(out, "Function not implemented") ||
			strings.Contains(out, "No support for codec") ||
			strings.Contains(out, "Cannot load") ||
			strings.Contains(out, "CUDA_ERROR") ||
			strings.Contains(out, "Could not find ref") ||
			strings.Contains(out, "Failed to inject frame") ||
			strings.Contains(out, "Failed to query surface") ||
			strings.Contains(out, "hwdownload")) {
		log.Printf("[trickplay] item %d: %s-Init fehlgeschlagen, fallback auf Software", it.ID, primary)
		out, err = run("software")
	}
	if err != nil {
		// Timeout vom context.WithTimeout: ffmpeg-stderr ist meist leer
		// (Prozess wurde mid-decode SIGKILL'd). Klare Meldung statt
		// nichtssagendem "signal: killed ()".
		if tctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("ffmpeg: timeout nach %v (Datei zu lang oder Decoder zu langsam)", timeout)
		}
		return fmt.Errorf("ffmpeg: %w (%s)", err, truncate(out, 300))
	}

	if err := writeVTT(vtt, tileCount, gridX, interval); err != nil {
		return err
	}
	log.Printf("[trickplay] item %d fertig (%d tiles @ %ds, %dx%d grid)", it.ID, tileCount, interval, gridX, gridY)
	return nil
}

// writeVTT schreibt das WebVTT-Manifest. Jeder Cue verweist auf eine Kachel im Sprite
// via xywh-Fragment, so dass der Player die richtige Region zeigt.
func writeVTT(path string, tileCount, gridX, intervalSec int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintln(f, "WEBVTT"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f); err != nil {
		return err
	}
	for i := 0; i < tileCount; i++ {
		start := i * intervalSec
		end := (i + 1) * intervalSec
		row := i / gridX
		col := i % gridX
		x := col * tileWidth
		y := row * tileHeight
		if _, err := fmt.Fprintln(f, formatTime(start), "-->", formatTime(end)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(f, "sprite.jpg#xywh=%d,%d,%d,%d\n\n", x, y, tileWidth, tileHeight); err != nil {
			return err
		}
	}
	return nil
}

func formatTime(sec int) string {
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	return fmt.Sprintf("%02d:%02d:%02d.000", h, m, s)
}

// SpriteFile gibt den Pfad zum Sprite eines Items zurück (leer wenn nicht vorhanden).
func (w *Worker) SpriteFile(itemID int64) string {
	p := filepath.Join(w.outDir, strconv.FormatInt(itemID, 10), "sprite.jpg")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// VTTFile gibt den Pfad zur VTT-Datei zurück (leer wenn nicht vorhanden).
func (w *Worker) VTTFile(itemID int64) string {
	p := filepath.Join(w.outDir, strconv.FormatInt(itemID, 10), "thumbs.vtt")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// cleanupOrphans entfernt Trickplay-Dateien + Status für Items, deren Folder
// nicht mehr aktiviert ist. Wird zu Beginn jedes runOnce aufgerufen.
func (w *Worker) cleanupOrphans() {
	ids, err := w.store.ListOrphanTrickplayItems()
	if err != nil {
		log.Printf("[trickplay] orphan-list: %v", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	log.Printf("[trickplay] cleanup %d orphan items (folder nicht aktiviert)", len(ids))
	for _, id := range ids {
		w.Delete(id)
		_ = w.store.SetItemTrickplay(id, "")
	}
}

// vaapiSupportsCodec prüft, ob der Intel-iHD-Decoder diesen Codec verarbeiten
// kann. MPEG-4 Part 2 (DivX/XviD, ffprobe-Name "mpeg4"), VC-1, WMV3 und
// exotischere Codecs laufen dort nicht und müssen in Software decodiert werden.
func vaapiSupportsCodec(codec string) bool {
	switch strings.ToLower(codec) {
	case "h264", "avc", "avc1",
		"hevc", "h265", "hvc1",
		"vp8", "vp9",
		"av1",
		"mpeg2video", "mpeg2":
		return true
	}
	return false
}

// Delete entfernt Trickplay-Dateien eines Items.
func (w *Worker) Delete(itemID int64) {
	_ = os.RemoveAll(filepath.Join(w.outDir, strconv.FormatInt(itemID, 10)))
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
