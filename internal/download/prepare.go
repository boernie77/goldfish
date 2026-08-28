// Package download prüft und stellt bei Bedarf eine für native Player
// (aktuell: die Mac/iOS-App, die per AVFoundation abspielt) kompatible Kopie
// einer Videodatei her, BEVOR sie den Server verlässt.
//
// Bisher hat `/api/download` immer die Originaldatei geliefert und die
// Mac/iOS-App hat inkompatible Formate (MKV-Container, DTS/AC3-Ton, …) nach
// dem Download selbst per lokalem ffmpeg repariert (siehe
// GoldfishApple/…/LocalTranscodeService.swift). User-Anfrage 2026-08-27:
// "wie löst Jellyfin das eigentlich" — Jellyfins offizielle Apps lassen den
// SERVER anhand des Geräteprofils entscheiden, ob Direct Play möglich ist,
// und liefern sonst eine schon serverseitig passend gemachte Datei aus.
// Dieses Package überträgt genau dieses Prinzip auf Goldfish-Downloads: der
// Client bekommt nie mehr eine kaputte Datei zum Nachbearbeiten. Die alte
// client-seitige Formatanpassung bleibt nur noch für lokale/externe
// Bibliotheken bestehen, die direkt vom Datenträger gescannt werden und
// keinen Server zum Fragen haben.
package download

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"github.com/boernie77/goldfish/internal/playback"
)

// AudioStream ist ein einzelner Audiostream, wie ffprobe ihn meldet.
type AudioStream struct {
	Index    int    // absoluter Stream-Index in der Quelldatei, für -map 0:<Index>
	Codec    string // z.B. "aac", "ac3", "dts"
	Language string // BCP-47-artiger Tag, leer wenn nicht gesetzt
}

type cacheMeta struct {
	SourceModTime int64 `json:"sourceModTime"`
	SourceSize    int64 `json:"sourceSize"`
}

// Progress ist der Zustand der server-seitigen Formatanpassung für ein Item —
// vom `/api/download/{id}/compat-status`-Endpoint an den Client geliefert, damit
// die App "wird vorbereitet … X %" zeigen kann statt minutenlang auf einen
// stummen Download zu warten.
type Progress struct {
	State   string `json:"state"` // ready | preparing | error | idle
	Percent int    `json:"percent"`
	Message string `json:"message,omitempty"`
}

// prepJob ist ein laufender (oder gerade abgeschlossener) Formatanpassungs-Lauf
// für genau eine Ziel-Cache-Datei. Mehrere gleichzeitige Download-Requests für
// dasselbe Item — inklusive der Resume-Versuche der Apple-App nach einem
// Client-seitigen Read-Timeout — teilen sich EINEN ffmpeg-Lauf, statt jeweils
// einen neuen loszutreten.
type prepJob struct {
	done    chan struct{}
	path    string
	err     error
	totalMS atomic.Int64 // Gesamtlaufzeit der Quelle in ms (aus ffprobe); 0 = unbekannt
	doneMS  atomic.Int64 // fortlaufender ffmpeg-Fortschritt in ms (aus -progress)
	started time.Time
	ended   time.Time
}

// percent liefert 1..99 während der Lauf läuft, 100 bei Erfolg, bei Fehler den
// zuletzt erreichten Stand.
func (j *prepJob) percent() int {
	total := j.totalMS.Load()
	var p int64
	if total > 0 {
		p = j.doneMS.Load() * 100 / total
	}
	select {
	case <-j.done:
		if j.err == nil {
			return 100
		}
	default:
	}
	if p < 1 {
		p = 1
	}
	if p > 99 {
		p = 99
	}
	return int(p)
}

type prepRegistry struct {
	mu     sync.Mutex
	jobs   map[string]*prepJob
	recent map[string]*prepJob // gerade fertig — für Status-Abfragen, ~2 min aufbewahrt
}

// start liefert für `key` einen bereits laufenden Job zurück oder startet einen
// neuen in einer eigenen Goroutine. `fn` läuft entkoppelt vom Request weiter,
// auch wenn alle Waiter abgebrochen haben — so wärmt ein abgebrochener Download
// trotzdem den Cache für den nächsten Versuch.
func (r *prepRegistry) start(key string, fn func(*prepJob) (string, error)) *prepJob {
	r.mu.Lock()
	if j, ok := r.jobs[key]; ok {
		r.mu.Unlock()
		return j
	}
	j := &prepJob{done: make(chan struct{}), started: time.Now()}
	r.jobs[key] = j
	r.mu.Unlock()

	go func() {
		j.path, j.err = fn(j)
		j.ended = time.Now()
		close(j.done)
		r.mu.Lock()
		delete(r.jobs, key)
		r.recent[key] = j
		for k, rj := range r.recent {
			if time.Since(rj.ended) > 2*time.Minute {
				delete(r.recent, k)
			}
		}
		r.mu.Unlock()
	}()
	return j
}

func (r *prepRegistry) lookup(key string) *prepJob {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j, ok := r.jobs[key]; ok {
		return j
	}
	return r.recent[key]
}

var prepReg = &prepRegistry{jobs: map[string]*prepJob{}, recent: map[string]*prepJob{}}

// plan entscheidet, ob überhaupt eine Formatanpassung nötig ist, und liefert die
// Cache-Pfade. needsPrep=false → die Originaldatei kann direkt ausgeliefert
// werden. `container`/`videoCodecHint`/`audioCodecHint` kommen aus den beim Scan
// ermittelten DB-Feldern — reicht für die häufige "ist eh schon passend"-
// Kurzentscheidung ohne zusätzlichen ffprobe-Call.
func plan(cacheDir string, itemID int64, sourcePath, container, videoCodecHint, audioCodecHint string) (needsPrep bool, outPath, metaPath string, info os.FileInfo, err error) {
	info, err = os.Stat(sourcePath)
	if err != nil {
		return false, "", "", nil, err
	}
	containerOK := container == "mp4" || container == "mov" || container == "m4v"
	if containerOK && videoCodecHint == "h264" && audioCodecHint == "aac" {
		return false, "", "", info, nil
	}
	if err = os.MkdirAll(cacheDir, 0o755); err != nil {
		return false, "", "", info, err
	}
	outPath = filepath.Join(cacheDir, fmt.Sprintf("%d.mp4", itemID))
	metaPath = outPath + ".json"
	return true, outPath, metaPath, info, nil
}

// EnsureCompatible liefert einen abspielbaren Pfad für `sourcePath` zurück —
// entweder die Originaldatei selbst (schneller Normalfall: schon mp4/mov mit
// h264+aac) oder eine einmalig erzeugte, dauerhaft gecachte Remux-/Transcode-
// Kopie unter `cacheDir` (Datei-Name `<itemID>.mp4`).
//
// `ctx` steuert nur, wie lange HIER auf das Ergebnis gewartet wird. Der
// eigentliche ffmpeg-Lauf hängt bewusst NICHT am Request-Context: bricht die
// Apple-App wegen ihres Read-Timeouts ab, läuft die Konvertierung entkoppelt
// zu Ende und füllt den Cache.
func EnsureCompatible(ctx context.Context, hw playback.HWAccel, cacheDir string, itemID int64, sourcePath, container, videoCodecHint, audioCodecHint string) (string, error) {
	needsPrep, outPath, metaPath, info, err := plan(cacheDir, itemID, sourcePath, container, videoCodecHint, audioCodecHint)
	if err != nil {
		return "", err
	}
	if !needsPrep {
		return sourcePath, nil
	}
	if cachedCopyValid(outPath, metaPath, info) {
		return outPath, nil
	}

	job := prepReg.start(outPath, func(j *prepJob) (string, error) {
		return runPrep(j, hw, outPath, metaPath, sourcePath, info)
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-job.done:
		return job.path, job.err
	}
}

// Status liefert nicht-blockierend den aktuellen Zustand der Formatanpassung.
// "idle" heißt: nötig, aber noch nicht angestoßen (Aufrufer soll StartPrep rufen).
func Status(cacheDir string, itemID int64, sourcePath, container, videoCodecHint, audioCodecHint string) Progress {
	needsPrep, outPath, metaPath, info, err := plan(cacheDir, itemID, sourcePath, container, videoCodecHint, audioCodecHint)
	if err != nil {
		return Progress{State: "error", Message: err.Error()}
	}
	if !needsPrep || cachedCopyValid(outPath, metaPath, info) {
		return Progress{State: "ready", Percent: 100}
	}
	j := prepReg.lookup(outPath)
	if j == nil {
		return Progress{State: "idle"}
	}
	select {
	case <-j.done:
		if j.err != nil {
			return Progress{State: "error", Message: shortMsg(j.err.Error()), Percent: j.percent()}
		}
		return Progress{State: "ready", Percent: 100}
	default:
		return Progress{State: "preparing", Percent: j.percent()}
	}
}

// StartPrep stößt die Formatanpassung an (idempotent) und kehrt SOFORT zurück.
func StartPrep(hw playback.HWAccel, cacheDir string, itemID int64, sourcePath, container, videoCodecHint, audioCodecHint string) Progress {
	needsPrep, outPath, metaPath, info, err := plan(cacheDir, itemID, sourcePath, container, videoCodecHint, audioCodecHint)
	if err != nil {
		return Progress{State: "error", Message: err.Error()}
	}
	if !needsPrep || cachedCopyValid(outPath, metaPath, info) {
		return Progress{State: "ready", Percent: 100}
	}
	prepReg.start(outPath, func(j *prepJob) (string, error) {
		return runPrep(j, hw, outPath, metaPath, sourcePath, info)
	})
	return Progress{State: "preparing", Percent: 0}
}

func shortMsg(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}

// cachedCopyValid prüft, ob unter outPath eine nicht-leere Kopie liegt, deren
// Sidecar zur aktuellen Quelle (mtime + size) passt.
func cachedCopyValid(outPath, metaPath string, srcInfo os.FileInfo) bool {
	m, ok := readMeta(metaPath)
	if !ok || m.SourceModTime != srcInfo.ModTime().UnixNano() || m.SourceSize != srcInfo.Size() {
		return false
	}
	fi, err := os.Stat(outPath)
	return err == nil && fi.Size() > 0
}

// runPrep führt genau einen Konvertierungslauf aus. Läuft in einer eigenen
// Goroutine (siehe prepRegistry.start) und ist damit vom Request entkoppelt.
func runPrep(j *prepJob, hw playback.HWAccel, outPath, metaPath, sourcePath string, info os.FileInfo) (string, error) {
	if cachedCopyValid(outPath, metaPath, info) {
		return outPath, nil
	}

	// Plattenplatz-Schutz: lieber sofort mit klarer Meldung abbrechen als nach
	// Minuten mitten im ffmpeg-Lauf an "No space left on device" scheitern.
	need := info.Size() + (2 << 30)
	if free, ok := freeBytes(filepath.Dir(outPath)); ok && free < need {
		return "", fmt.Errorf("zu wenig Speicherplatz im Download-Cache: %d MiB frei, ~%d MiB nötig",
			free>>20, need>>20)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	started := time.Now()
	log.Printf("[download] compat-prep start src=%q size=%dMiB", sourcePath, info.Size()>>20)

	videoCodec, videoTag, err := probeVideo(ctx, sourcePath)
	if err != nil {
		log.Printf("[download] compat-prep ffprobe(video) FEHLER src=%q: %v", sourcePath, err)
		return "", fmt.Errorf("ffprobe (video): %w", err)
	}
	audioStreams, err := probeAudioStreams(ctx, sourcePath)
	if err != nil {
		log.Printf("[download] compat-prep ffprobe(audio) FEHLER src=%q: %v", sourcePath, err)
		return "", fmt.Errorf("ffprobe (audio): %w", err)
	}
	if durMS, e := probeDurationMS(ctx, sourcePath); e == nil && durMS > 0 {
		j.totalMS.Store(durMS)
	}
	log.Printf("[download] compat-prep src=%q video=%s/%s audiostreams=%d dauer=%dms",
		sourcePath, videoCodec, videoTag, len(audioStreams), j.totalMS.Load())

	// Verwaiste .tmp aus einem hart abgebrochenen Lauf (Container-Restart mitten
	// im Lauf → kein defer-Cleanup, u. U. mehrere GB) vorab wegräumen.
	if stale, _ := filepath.Glob(outPath + ".tmp.*.mp4"); len(stale) > 0 {
		for _, f := range stale {
			_ = os.Remove(f)
		}
	}

	tmp := outPath + ".tmp." + strconv.FormatInt(time.Now().UnixNano(), 10) + ".mp4"
	defer func() { _ = os.Remove(tmp) }()

	needsReencode := videoCodec != "hevc" && videoCodec != "h264" && videoCodec != "prores"

	// `+faststart` schreibt die FERTIGE Datei nochmal um (moov-Atom nach vorn).
	// IMMER setzen: ohne moov am Anfang spielt AVFoundation die Datei je nach
	// Gerät gar nicht ab (2026-08-28 real: Enola-Holmes-Download, 6 GB, moov
	// am Ende → unabspielbar). Die Extra-Zeit (ein Datei-Rewrite) ist jetzt
	// durch die „Wird vorbereitet … %"-Anzeige im Client abgedeckt.
	faststart := true

	args := buildArgs(sourcePath, tmp, videoCodec, videoTag, audioStreams, hw, false, faststart)
	out, runErr := runFFmpeg(ctx, j, args)
	if runErr != nil && needsReencode && hw.Selected != playback.BackendSoftware {
		_ = os.Remove(tmp)
		j.doneMS.Store(0) // Fortschritt startet für den Fallback-Lauf neu
		args = buildArgs(sourcePath, tmp, videoCodec, videoTag, audioStreams, hw, true, faststart)
		out, runErr = runFFmpeg(ctx, j, args)
	}
	if runErr != nil {
		log.Printf("[download] compat-prep ffmpeg FEHLER src=%q nach %s: %v — %s",
			sourcePath, time.Since(started).Round(time.Second), runErr, tail(string(out), 2000))
		return "", fmt.Errorf("ffmpeg fehlgeschlagen: %w — %s", runErr, tail(string(out), 800))
	}

	if err := os.Rename(tmp, outPath); err != nil {
		return "", err
	}
	writeMeta(metaPath, cacheMeta{SourceModTime: info.ModTime().UnixNano(), SourceSize: info.Size()})
	if fi, statErr := os.Stat(outPath); statErr == nil {
		log.Printf("[download] compat-prep FERTIG src=%q -> %dMiB in %s",
			sourcePath, fi.Size()>>20, time.Since(started).Round(time.Second))
	}
	return outPath, nil
}

// runFFmpeg startet ffmpeg, liest die `-progress pipe:1`-Ausgabe mit und
// aktualisiert `j.doneMS`. Rückgabe ist stderr (für die Fehlermeldung).
func runFFmpeg(ctx context.Context, j *prepJob, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var eb bytes.Buffer
	cmd.Stderr = &eb
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		// ffmpeg -progress: "out_time_us=1234567" (Mikrosekunden). "out_time_ms"
		// ist in vielen Builds ebenfalls µs (ffmpeg-Bug) — deshalb us verwenden.
		if v, ok := strings.CutPrefix(line, "out_time_us="); ok {
			if us, e := strconv.ParseInt(strings.TrimSpace(v), 10, 64); e == nil && us >= 0 {
				j.doneMS.Store(us / 1000)
			}
		}
	}
	return eb.Bytes(), cmd.Wait()
}

// freeBytes liefert den freien Speicherplatz (in Bytes) des Dateisystems, auf
// dem `dir` liegt. ok=false, wenn das nicht ermittelbar ist — dann NICHT
// blockieren.
func freeBytes(dir string) (int64, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true
}

func buildArgs(sourcePath, tmp, videoCodec, videoTag string, audioStreams []AudioStream, hw playback.HWAccel, forceSoftware, faststart bool) []string {
	needsReencode := videoCodec != "hevc" && videoCodec != "h264" && videoCodec != "prores"

	args := []string{"-hide_banner", "-loglevel", "error", "-nostats", "-progress", "pipe:1", "-y", "-nostdin"}
	if needsReencode && !forceSoftware {
		args = append(args, hwaccelDecodeArgs(hw)...)
	}
	// Vor -i: (a) großzügiges Probing, damit ffmpeg ALLE Tonspuren einer großen
	// MKV erkennt (eine spät startende zweite Sprache wird sonst übersehen),
	// (b) kaputte NAL-Units / Stream-Fehler in Release-Rips nicht als Abbruch
	// werten (analog Trickplay, CLAUDE.md "Software-Fallback").
	args = append(args,
		"-analyzeduration", "200M", "-probesize", "200M",
		"-err_detect", "ignore_err", "-fflags", "+genpts",
	)
	args = append(args, "-i", sourcePath, "-map", "0:v:0")

	if len(audioStreams) == 0 {
		// Kein Audiostream gefunden — lieber irgendeinen Ton mitnehmen als gar keinen.
		args = append(args, "-map", "0:a:0?", "-c:a:0", "aac", "-ac", "2", "-b:a", "192k")
	} else {
		for i, a := range audioStreams {
			args = append(args, "-map", fmt.Sprintf("0:%d", a.Index))
			// AVFoundation (macOS + iOS) dekodiert AAC, AC-3 und E-AC-3 in MP4
			// nativ — die per Stream-Copy durchreichen (Sekunden statt Minuten).
			// Nur DTS/TrueHD/FLAC/PCM/… müssen zu AAC transkodiert werden.
			switch a.Codec {
			case "aac", "ac3", "eac3":
				args = append(args, fmt.Sprintf("-c:a:%d", i), "copy")
			default:
				// DTS/TrueHD/FLAC/PCM → E-AC3 (AVFoundation dekodiert das auf
				// macOS + iOS nativ). Bewusst KEIN `-ac 2`-Downmix mehr: die
				// Kanalanzahl (z.B. 5.1) bleibt erhalten (User-Wunsch 2026-08-28).
				// E-AC3 deckt mono/stereo/5.1 gleichermaßen ab; 640 kbit/s ist
				// Dolbys Referenz für 5.1.
				args = append(args, fmt.Sprintf("-c:a:%d", i), "eac3", "-b:a", "640k")
			}
			if a.Language != "" {
				args = append(args, fmt.Sprintf("-metadata:s:a:%d", i), "language="+a.Language)
			}
		}
	}

	switch {
	case videoCodec == "hevc":
		if videoTag == "hvc1" {
			args = append(args, "-c:v", "copy")
		} else {
			// hev1-getaggtes HEVC lehnt AVFoundation/QuickTime oft ab, reines Retag kostet nichts.
			args = append(args, "-c:v", "copy", "-tag:v", "hvc1")
		}
	case videoCodec == "h264", videoCodec == "prores":
		args = append(args, "-c:v", "copy")
	default:
		// av1, vp9, mpeg2video, vc1, … — von AVFoundation gar nicht dekodierbar.
		if forceSoftware {
			args = append(args, "-c:v", "libx264", "-preset", "medium", "-crf", "20", "-pix_fmt", "yuv420p")
		} else {
			args = append(args, videoEncodeArgs(hw)...)
		}
	}

	// -max_muxing_queue_size: MKV→MP4 mit mehreren Streams bricht sonst gern mit
	// "Too many packets buffered for output stream" ab, wenn Video-Copy und
	// Audio-Transcode zeitlich auseinanderlaufen.
	args = append(args, "-max_muxing_queue_size", "4096")
	// +faststart: moov nach vorn (siehe runPrep). +negative_cts_offsets:
	// B-Frame-Verzögerung als negative Composition-Time schreiben statt als
	// edit list (`elst`) — ffmpegs Standard-`elst` bringt AVFoundation bei
	// kopiertem h264 gelegentlich dazu, die Datei GAR NICHT abzuspielen,
	// obwohl VLC sie klaglos abspielt (2026-08-28: Kill Bill).
	if faststart {
		args = append(args, "-movflags", "+faststart+negative_cts_offsets")
	} else {
		args = append(args, "-movflags", "+negative_cts_offsets")
	}
	args = append(args, tmp)
	return args
}

func hwaccelDecodeArgs(hw playback.HWAccel) []string {
	switch hw.Selected {
	case playback.BackendVAAPI:
		return []string{"-vaapi_device", hw.VAAPIDevice, "-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi"}
	case playback.BackendNVENC:
		return []string{"-hwaccel", "cuda"}
	default:
		return nil
	}
}

func videoEncodeArgs(hw playback.HWAccel) []string {
	switch hw.Selected {
	case playback.BackendVAAPI:
		return []string{"-c:v", "h264_vaapi", "-qp", "20"}
	case playback.BackendNVENC:
		return []string{"-c:v", "h264_nvenc", "-preset", "p4", "-rc", "vbr", "-cq", "20", "-pix_fmt", "yuv420p"}
	default:
		return []string{"-c:v", "libx264", "-preset", "medium", "-crf", "20", "-pix_fmt", "yuv420p"}
	}
}

func readMeta(path string) (cacheMeta, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cacheMeta{}, false
	}
	var m cacheMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return cacheMeta{}, false
	}
	return m, true
}

func writeMeta(path string, m cacheMeta) {
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
