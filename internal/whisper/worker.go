// Package whisper provides a background worker that generates subtitles
// using whisper-cli (whisper.cpp) and optionally translates them via a
// configurable translation backend.
package whisper

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/boernie77/goldfish/internal/store"
	"github.com/boernie77/goldfish/internal/translate"
)

const (
	whisperBin    = "whisper-cli"
	ffmpegBin     = "ffmpeg"
	defaultModel  = "ggml-small"
	pollInterval  = 10 * time.Second
	audioTimeout  = 5 * time.Minute
	whisperPerMin = 5 * time.Minute  // 5× real-time pro Minute Audio als Puffer
	whisperMaxTimeout = 8 * time.Hour // Hard-Cap für sehr lange Inhalte
)

// Status holds the current worker state for the status API.
type Status struct {
	Running       bool   `json:"running"`
	Queue         int    `json:"queue"`
	CurrentItemID int64  `json:"currentItemId,omitempty"`
	CurrentLang   string `json:"currentLang,omitempty"`
	CurrentTitle  string `json:"currentTitle,omitempty"`
	ProgressPct   int    `json:"progressPct"` // 0–100, nur während Transkription
	Phase         string `json:"phase"`        // "transcribe" | "translate" | ""
}

// Worker processes pending subtitle generation jobs in a background goroutine.
type Worker struct {
	store     *store.Store
	configDir string

	mu     sync.Mutex
	status Status

	trigger chan struct{}

	// translation backend — set by SetTranslator
	translator translate.Translator
}

func New(s *store.Store, configDir string) *Worker {
	outDir := filepath.Join(configDir, "generated-subs")
	_ = os.MkdirAll(outDir, 0o755)
	return &Worker{
		store:      s,
		configDir:  configDir,
		trigger:    make(chan struct{}, 1),
		translator: translate.NopTranslator{},
	}
}

// SetTranslator swaps the translation backend at runtime.
func (w *Worker) SetTranslator(t translate.Translator) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.translator = t
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

// ModelPath returns the full path to the currently configured whisper model file.
func (w *Worker) ModelPath() string {
	model, _ := w.store.GetSetting("whisper_model", defaultModel)
	if model == "" {
		model = defaultModel
	}
	return filepath.Join(w.configDir, "whisper-models", model+".bin")
}

// IsModelAvailable returns true if the model binary exists on disk.
func (w *Worker) IsModelAvailable() bool {
	_, err := os.Stat(w.ModelPath())
	return err == nil
}

func (w *Worker) Run(ctx context.Context) {
	// Crash recovery: reset any jobs left in "running" state
	if err := w.store.ResetRunningSubtitleJobs(); err != nil {
		log.Printf("[whisper] reset running jobs: %v", err)
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

func (w *Worker) runOnce(ctx context.Context) {
	jobs, err := w.store.ListPendingSubtitleJobs(5)
	if err != nil {
		log.Printf("[whisper] list jobs: %v", err)
		return
	}
	n := len(jobs)
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		// fetch item path
		item, err := w.store.GetItem(job.ItemID)
		if err != nil || item == nil {
			_ = w.store.SetSubtitleJobFailed(job.ID, "item not found")
			continue
		}

		w.mu.Lock()
		w.status = Status{Running: true, Queue: n, CurrentItemID: item.ID, CurrentLang: job.Language, CurrentTitle: item.Title}
		w.mu.Unlock()

		if err := w.store.SetSubtitleJobRunning(job.ID); err != nil {
			log.Printf("[whisper] item %d: mark running: %v", item.ID, err)
			continue
		}

		if err := w.process(ctx, job, item.Path, item.DurationSec); err != nil {
			log.Printf("[whisper] item %d lang %s: %v", item.ID, job.Language, err)
			_ = w.store.SetSubtitleJobFailed(job.ID, err.Error())
		} else {
			_ = w.store.SetSubtitleJobDone(job.ID)
			log.Printf("[whisper] item %d lang %s: done", item.ID, job.Language)
		}
		n--
	}
	w.mu.Lock()
	w.status = Status{}
	w.mu.Unlock()
}

// process runs the full pipeline for one job:
//  1. Extract audio with ffmpeg
//  2. Transcribe with whisper-cli → English VTT
//  3. If targetLang != "en": translate VTT cues
//  4. Save to /config/generated-subs/{itemID}/{lang}.vtt
func (w *Worker) process(ctx context.Context, job store.SubtitleJob, videoPath string, durationSec float64) error {
	tmpWAV := filepath.Join(os.TempDir(), fmt.Sprintf("ws_%d_%s.wav", job.ItemID, job.Language))
	defer os.Remove(tmpWAV)

	// --- 1. Extract audio ---
	audioCtx, audioCancel := context.WithTimeout(ctx, audioTimeout)
	defer audioCancel()
	ffArgs := []string{
		"-y", "-i", videoPath,
		"-ar", "16000", "-ac", "1", "-vn",
		"-c:a", "pcm_s16le",
		tmpWAV,
	}
	ffCmd := exec.CommandContext(audioCtx, ffmpegBin, ffArgs...)
	if out, err := ffCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg audio extract: %w — %s", err, truncate(string(out), 200))
	}

	// --- 2. Whisper transcription ---
	modelPath := w.ModelPath()
	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("whisper model not found at %s — bitte im Admin-Menü herunterladen", modelPath)
	}

	outDir := filepath.Join(w.configDir, "generated-subs", fmt.Sprintf("%d", job.ItemID))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}

	// whisper-cli writes {outFile}.vtt — we control the stem
	outStem := filepath.Join(outDir, "en_tmp")

	timeout := audioTimeout + time.Duration(durationSec/60)*whisperPerMin
	if timeout > whisperMaxTimeout {
		timeout = whisperMaxTimeout
	}
	wCtx, wCancel := context.WithTimeout(ctx, timeout)
	defer wCancel()

	w.mu.Lock()
	w.status.Phase = "transcribe"
	w.status.ProgressPct = 0
	w.mu.Unlock()

	// Architektur: VTT-Zwischenschritt ist IMMER Englisch (kanonisch). Bei
	// nicht-englischen Quellen (italienisches YouTube, deutsche Doku, …)
	// muss whisper-cli die Quellsprache auto-erkennen UND nach Englisch
	// übersetzen — sonst transkribiert es z. B. Italienisch als Englisch
	// gehört und produziert Phonetik-Müll. `-tr` ist der Translate-Modus.
	// `-l auto` aktiviert die automatische Sprach-Erkennung von whisper.cpp.
	wArgs := []string{
		"-m", modelPath,
		"-l", "auto",
		"-tr",
		"--output-vtt",
		"-of", outStem,
		tmpWAV,
	}
	wCmd := exec.CommandContext(wCtx, whisperBin, wArgs...)
	// Capture stdout for progress parsing (whisper-cli prints "[HH:MM:SS.mmm --> ...]")
	stdoutPipe, _ := wCmd.StdoutPipe()
	wCmd.Stderr = wCmd.Stdout // merge stderr into stdout pipe
	if err := wCmd.Start(); err != nil {
		return fmt.Errorf("whisper-cli start: %w", err)
	}
	// Parse progress in background
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			if pct := parseWhisperProgress(line, durationSec); pct > 0 {
				w.mu.Lock()
				w.status.ProgressPct = pct
				w.mu.Unlock()
			}
		}
	}()
	if err := wCmd.Wait(); err != nil {
		// Exit code 3 = Modell konnte nicht geladen werden
		if strings.Contains(err.Error(), "exit status 3") {
			return fmt.Errorf("Modell konnte nicht geladen werden — bitte im Admin-Menü (🎤 Whisper) Modell herunterladen")
		}
		return fmt.Errorf("whisper-cli: %w", err)
	}

	enVTT := outStem + ".vtt"
	enData, err := os.ReadFile(enVTT)
	if err != nil {
		return fmt.Errorf("read whisper output: %w", err)
	}

	// Save English VTT (always produced, regardless of target language)
	enDest := filepath.Join(outDir, "en.vtt")
	if err := os.WriteFile(enDest, enData, 0o644); err != nil {
		return fmt.Errorf("write en.vtt: %w", err)
	}
	_ = os.Remove(enVTT) // clean temp stem

	// If job is for English, we're done
	if job.Language == "en" {
		return nil
	}

	w.mu.Lock()
	w.status.Phase = "translate"
	w.status.ProgressPct = 0
	w.mu.Unlock()

	// --- 3. Translate ---
	w.mu.Lock()
	tr := w.translator
	w.mu.Unlock()

	log.Printf("[whisper] item %d lang %s: translating with %T", job.ItemID, job.Language, tr)
	translated, err := translate.TranslateVTT(ctx, tr, string(enData), job.Language)
	if err != nil {
		log.Printf("[whisper] item %d lang %s: translate ERROR: %v", job.ItemID, job.Language, err)
		return fmt.Errorf("translate to %s: %w", job.Language, err)
	}
	if translated == string(enData) {
		log.Printf("[whisper] item %d lang %s: WARN — output identical to English input (Translator nop or all calls failed silently)", job.ItemID, job.Language)
	} else {
		log.Printf("[whisper] item %d lang %s: translated %d -> %d bytes", job.ItemID, job.Language, len(enData), len(translated))
	}

	// --- 4. Save translated VTT ---
	dest := filepath.Join(outDir, job.Language+".vtt")
	if err := os.WriteFile(dest, []byte(translated), 0o644); err != nil {
		return fmt.Errorf("write %s.vtt: %w", job.Language, err)
	}
	return nil
}

// VTTPath returns the path to a generated VTT file (may not exist yet).
func VTTPath(configDir string, itemID int64, lang string) string {
	return filepath.Join(configDir, "generated-subs", fmt.Sprintf("%d", itemID), lang+".vtt")
}

// DownloadModel downloads a whisper model binary from Hugging Face.
// Runs in background; progress is reported via the returned channel (0–100, -1 = error).
func DownloadModel(ctx context.Context, configDir, modelName string) (<-chan int, error) {
	modelsDir := filepath.Join(configDir, "whisper-models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		return nil, err
	}
	dest := filepath.Join(modelsDir, modelName+".bin")
	// Use the download script bundled with whisper.cpp, or fall back to curl
	ch := make(chan int, 10)
	go func() {
		defer close(ch)
		url := fmt.Sprintf(
			"https://huggingface.co/ggerganov/whisper.cpp/resolve/main/%s.bin",
			modelName,
		)
		// Use curl with progress output
		cmd := exec.CommandContext(ctx, "curl", "-L", "--progress-bar", "-o", dest, url)
		stderr, _ := cmd.StderrPipe()
		if err := cmd.Start(); err != nil {
			log.Printf("[whisper] download start: %v", err)
			ch <- -1
			return
		}
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			// curl progress lines contain '%'
			if idx := strings.Index(line, "%"); idx > 0 {
				var pct int
				fmt.Sscanf(strings.TrimSpace(line[:idx]), "%d", &pct)
				ch <- pct
			}
		}
		if err := cmd.Wait(); err != nil {
			log.Printf("[whisper] download: %v", err)
			_ = os.Remove(dest)
			ch <- -1
			return
		}
		ch <- 100
	}()
	return ch, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// reWhisperTS parst die Zeilen "[HH:MM:SS.mmm --> HH:MM:SS.mmm]  text" die
// whisper-cli auf stdout ausgibt, und berechnet daraus einen Fortschritt in %.
var reWhisperTS = regexp.MustCompile(`\[(\d{2}):(\d{2}):(\d{2})\.(\d{3})\s*-->`)

func parseWhisperProgress(line string, totalSec float64) int {
	if totalSec <= 0 {
		return 0
	}
	m := reWhisperTS.FindStringSubmatch(line)
	if m == nil {
		return 0
	}
	h, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	sec, _ := strconv.Atoi(m[3])
	ms, _ := strconv.Atoi(m[4])
	cur := float64(h*3600+min*60+sec) + float64(ms)/1000.0
	pct := int(cur / totalSec * 100)
	if pct > 99 {
		pct = 99
	}
	return pct
}
