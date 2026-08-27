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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
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

// prepJob ist ein laufender (oder gerade abgeschlossener) Formatanpassungs-Lauf
// für genau eine Ziel-Cache-Datei. Mehrere gleichzeitige Download-Requests für
// dasselbe Item — inklusive der Resume-Versuche der Apple-App nach einem
// Client-seitigen Read-Timeout — teilen sich EINEN ffmpeg-Lauf, statt jeweils
// einen neuen loszutreten. Der frühere Zustand: jeder Retry startete eine
// frische, minutenlange Konvertierung (und schrieb dabei in dieselbe
// `.tmp.mp4`) → der Download kam nie über 99 % hinaus.
type prepJob struct {
	done chan struct{}
	path string
	err  error
}

type prepRegistry struct {
	mu   sync.Mutex
	jobs map[string]*prepJob
}

// start liefert für `key` einen bereits laufenden Job zurück oder startet einen
// neuen in einer eigenen Goroutine. `fn` läuft entkoppelt vom Request weiter,
// auch wenn alle Waiter abgebrochen haben — so wärmt ein abgebrochener Download
// trotzdem den Cache für den nächsten Versuch.
func (r *prepRegistry) start(key string, fn func() (string, error)) *prepJob {
	r.mu.Lock()
	if j, ok := r.jobs[key]; ok {
		r.mu.Unlock()
		return j
	}
	j := &prepJob{done: make(chan struct{})}
	r.jobs[key] = j
	r.mu.Unlock()

	go func() {
		j.path, j.err = fn()
		close(j.done)
		r.mu.Lock()
		delete(r.jobs, key)
		r.mu.Unlock()
	}()
	return j
}

var prepReg = &prepRegistry{jobs: map[string]*prepJob{}}

// EnsureCompatible liefert einen abspielbaren Pfad für `sourcePath` zurück —
// entweder die Originaldatei selbst (schneller Normalfall: schon mp4/mov mit
// h264+aac) oder eine einmalig erzeugte, dauerhaft gecachte Remux-/Transcode-
// Kopie unter `cacheDir` (Datei-Name `<itemID>.mp4`). `container`/
// `videoCodecHint`/`audioCodecHint` kommen aus den beim Scan bereits
// ermittelten DB-Feldern (`items.container/video_codec/audio_codec`) — reicht
// für die häufige "ist eh schon passend"-Kurzentscheidung ohne zusätzlichen
// ffprobe-Call. Nur im "muss angepasst werden"-Zweig wird frisch geprobt
// (Video-Codec+Tag, ALLE Audiostreams inkl. Sprache — die DB kennt nur den
// ERSTEN Audiostream), damit KEINE Tonspur beim Remux verloren geht (Fix für
// genau den Kill-Bill-Bug, der den client-seitigen Vorgänger hatte).
//
// `ctx` steuert nur, wie lange HIER auf das Ergebnis gewartet wird. Der
// eigentliche ffmpeg-Lauf hängt bewusst NICHT am Request-Context: bricht die
// Apple-App wegen ihres Read-Timeouts ab, läuft die Konvertierung entkoppelt
// zu Ende und füllt den Cache, statt beim nächsten Resume-Versuch komplett von
// vorn zu beginnen (der Grund für den 99-%-Hänger).
func EnsureCompatible(ctx context.Context, hw playback.HWAccel, cacheDir string, itemID int64, sourcePath, container, videoCodecHint, audioCodecHint string) (string, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return "", err
	}

	containerOK := container == "mp4" || container == "mov" || container == "m4v"
	if containerOK && videoCodecHint == "h264" && audioCodecHint == "aac" {
		return sourcePath, nil
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	outPath := filepath.Join(cacheDir, fmt.Sprintf("%d.mp4", itemID))
	metaPath := outPath + ".json"
	if cachedCopyValid(outPath, metaPath, info) {
		return outPath, nil
	}

	job := prepReg.start(outPath, func() (string, error) {
		return runPrep(hw, outPath, metaPath, sourcePath, info)
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-job.done:
		return job.path, job.err
	}
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
func runPrep(hw playback.HWAccel, outPath, metaPath, sourcePath string, info os.FileInfo) (string, error) {
	// Zwischen cachedCopyValid() in EnsureCompatible und dem Start dieser
	// Goroutine kann ein paralleler Lauf den Cache bereits gefüllt haben.
	if cachedCopyValid(outPath, metaPath, info) {
		return outPath, nil
	}

	// Plattenplatz-Schutz (analog zum Mac-`LocalTranscodeService`): lieber
	// sofort mit klarer Meldung abbrechen als nach Minuten mitten im
	// ffmpeg-Lauf an "No space left on device" scheitern und eine
	// abgeschnittene Datei zu hinterlassen.
	need := info.Size() + (2 << 30) // Quelle + 2 GiB Reserve
	if free, ok := freeBytes(filepath.Dir(outPath)); ok && free < need {
		return "", fmt.Errorf("zu wenig Speicherplatz im Download-Cache: %d MiB frei, ~%d MiB nötig",
			free>>20, need>>20)
	}

	// Großzügiger Deckel, damit ein hängender ffmpeg nicht ewig läuft — aber
	// unabhängig davon, ob der auslösende Request noch da ist.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	videoCodec, videoTag, err := probeVideo(ctx, sourcePath)
	if err != nil {
		return "", fmt.Errorf("ffprobe (video): %w", err)
	}
	audioStreams, err := probeAudioStreams(ctx, sourcePath)
	if err != nil {
		return "", fmt.Errorf("ffprobe (audio): %w", err)
	}

	tmp := outPath + ".tmp." + strconv.FormatInt(time.Now().UnixNano(), 10) + ".mp4"
	defer func() { _ = os.Remove(tmp) }()

	needsReencode := videoCodec != "hevc" && videoCodec != "h264" && videoCodec != "prores"

	// Erst mit dem konfigurierten HW-Backend versuchen (falls überhaupt ein
	// Re-Encode nötig ist — reine Remuxe/Retags brauchen kein hwaccel), bei
	// Fehlschlag EINMAL komplett in Software erneut versuchen. Gleiches
	// Fallback-Muster wie Trickplay (CLAUDE.md "Software-Fallback").
	args := buildArgs(sourcePath, tmp, videoCodec, videoTag, audioStreams, hw, false)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil && needsReencode && hw.Selected != playback.BackendSoftware {
		_ = os.Remove(tmp)
		args = buildArgs(sourcePath, tmp, videoCodec, videoTag, audioStreams, hw, true)
		cmd = exec.CommandContext(ctx, "ffmpeg", args...)
		out, runErr = cmd.CombinedOutput()
	}
	if runErr != nil {
		return "", fmt.Errorf("ffmpeg fehlgeschlagen: %w — %s", runErr, tail(string(out), 500))
	}

	if err := os.Rename(tmp, outPath); err != nil {
		return "", err
	}
	writeMeta(metaPath, cacheMeta{SourceModTime: info.ModTime().UnixNano(), SourceSize: info.Size()})
	return outPath, nil
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

func buildArgs(sourcePath, tmp, videoCodec, videoTag string, audioStreams []AudioStream, hw playback.HWAccel, forceSoftware bool) []string {
	needsReencode := videoCodec != "hevc" && videoCodec != "h264" && videoCodec != "prores"

	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	if needsReencode && !forceSoftware {
		args = append(args, hwaccelDecodeArgs(hw)...)
	}
	args = append(args, "-i", sourcePath, "-map", "0:v:0")

	if len(audioStreams) == 0 {
		// Kein Audiostream gefunden (Probe-Fehler o.ä.) — lieber irgendeinen
		// Ton mitnehmen als gar keinen.
		args = append(args, "-map", "0:a:0?", "-c:a:0", "aac", "-ac", "2", "-b:a", "192k")
	} else {
		for i, a := range audioStreams {
			args = append(args, "-map", fmt.Sprintf("0:%d", a.Index))
			if a.Codec == "aac" {
				args = append(args, fmt.Sprintf("-c:a:%d", i), "copy")
			} else {
				args = append(args, fmt.Sprintf("-c:a:%d", i), "aac", "-ac", "2", "-b:a", "192k")
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
			// Gleicher Grund wie beim Client: hev1-getaggtes HEVC lehnt
			// AVFoundation/QuickTime oft ab, reines Retag kostet nichts.
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

	args = append(args, "-movflags", "+faststart", tmp)
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
