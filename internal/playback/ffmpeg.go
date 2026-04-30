package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Profile definiert Auflösungs- und Bitraten-Zielwerte für einen Transcode.
type Profile struct {
	ID        string
	Label     string
	MaxHeight int    // 0 = Original
	VideoKbps int    // 0 = Qualitäts-basiert (QP 23)
	AudioKbps int
}

var Profiles = []Profile{
	{ID: "orig", Label: "Original (Qualität)", MaxHeight: 0, VideoKbps: 0, AudioKbps: 192},
	// 1080p — drei Bitratenstufen für unterschiedliche Bandbreiten.
	{ID: "1080p-hq", Label: "1080p · 8 Mbps (hoch)", MaxHeight: 1080, VideoKbps: 8000, AudioKbps: 192},
	{ID: "1080p", Label: "1080p · 5 Mbps (mittel)", MaxHeight: 1080, VideoKbps: 5000, AudioKbps: 160},
	{ID: "1080p-lq", Label: "1080p · 3 Mbps (niedrig)", MaxHeight: 1080, VideoKbps: 3000, AudioKbps: 128},
	// 720p
	{ID: "720p-hq", Label: "720p · 4 Mbps (hoch)", MaxHeight: 720, VideoKbps: 4000, AudioKbps: 160},
	{ID: "720p", Label: "720p · 2,5 Mbps (mittel)", MaxHeight: 720, VideoKbps: 2500, AudioKbps: 128},
	{ID: "720p-lq", Label: "720p · 1,5 Mbps (niedrig)", MaxHeight: 720, VideoKbps: 1500, AudioKbps: 96},
	// 480p
	{ID: "480p-hq", Label: "480p · 2 Mbps (hoch)", MaxHeight: 480, VideoKbps: 2000, AudioKbps: 128},
	{ID: "480p", Label: "480p · 1 Mbps (mittel)", MaxHeight: 480, VideoKbps: 1000, AudioKbps: 96},
	{ID: "480p-lq", Label: "480p · 600 kbps (niedrig)", MaxHeight: 480, VideoKbps: 600, AudioKbps: 96},
}

func ProfileByID(id string) Profile {
	for _, p := range Profiles {
		if p.ID == id {
			return p
		}
	}
	return Profiles[0]
}

// Session manages one ffmpeg transcode that produces an HLS playlist.
type Session struct {
	ID        string
	ItemID    int64
	Profile   string
	AudioIdx  int  // -1 = default (erster Audio-Stream)
	Dir       string
	StartSec  float64
	StartedAt time.Time // Wall-Clock-Zeit beim Session-Erzeugen — fresh=1-Idempotenz
	Cmd       *exec.Cmd
	cancel    context.CancelFunc
	lastUsed  time.Time
	mu        sync.Mutex
	done      chan struct{}
}

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	cacheDir string
	hw       HWAccel
	// freshHandledAt: Zeitpunkt, an dem ein `fresh=1`-Request fuer diesen
	// Session-Key zuletzt das Stop-and-Recreate ausgeloest hat. VHS laedt
	// EVENT-Playlists periodisch (alle ~targetDuration ≈ 4 s) mit DERSELBEN
	// URL inkl. fresh=1. Ohne diese Drosselung wuerde jeder Reload nach
	// 4 s die Session toeten und neu starten → Wiedergabe stottert sichtbar
	// (Server-Buffer klettert hoch, fällt auf 0, klettert wieder hoch).
	// Mit der Map honorieren wir fresh=1 nur einmal pro Key in einem
	// 60-Sekunden-Fenster — damit reicht der erste Klick „Von Anfang"
	// zum Stale-Session-Reset, alle folgenden VHS-Reloads sind no-op.
	freshHandledAt map[string]time.Time
}

func NewManager(cacheDir string, hw HWAccel) *Manager {
	_ = os.MkdirAll(cacheDir, 0o755)
	m := &Manager{
		sessions:       map[string]*Session{},
		cacheDir:       cacheDir,
		hw:             hw,
		freshHandledAt: map[string]time.Time{},
	}
	go m.gcLoop()
	return m
}

// SetHWAccel wechselt das aktive Backend zur Laufzeit (z. B. wenn der User in
// den Settings von VAAPI auf NVENC wechselt). Bereits laufende Sessions
// behalten ihre alte Konfiguration; neue Sessions nutzen das neue Backend.
func (m *Manager) SetHWAccel(hw HWAccel) {
	m.mu.Lock()
	m.hw = hw
	m.mu.Unlock()
}

func (m *Manager) gcLoop() {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for range t.C {
		m.mu.Lock()
		for id, s := range m.sessions {
			s.mu.Lock()
			idle := time.Since(s.lastUsed)
			s.mu.Unlock()
			if idle > 5*time.Minute {
				log.Printf("[transcode] session %s idle %v → stop", id, idle)
				s.Stop()
				delete(m.sessions, id)
			}
		}
		m.mu.Unlock()
	}
}

// StopSession beendet eine spezifische Session und entfernt sie aus dem Pool,
// falls sie existiert. Wird vom „Von Anfang"-Pfad genutzt: ohne Reset würde
// eine alte Session bei start=0 (von einem vorherigen Lauf, akkumuliertes
// Material) wiederverwendet — der Browser bekommt eine Playlist mit weit
// fortgeschrittenem Stand und springt nicht zu 0.
func (m *Manager) StopSession(itemID int64, profile Profile, audioIdx int, startSec float64, deinterlace bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dei := 0
	if deinterlace {
		dei = 1
	}
	id := fmt.Sprintf("%d-%s-a%d-%d-d%d", itemID, profile.ID, audioIdx, int(startSec), dei)
	if s, ok := m.sessions[id]; ok {
		log.Printf("[transcode] session %s manuell gestoppt (fresh)", id)
		s.Stop()
		delete(m.sessions, id)
	}
}

// SessionAge liefert die Lebensdauer der existierenden Session zur Key oder
// (false, _), wenn keine läuft. Wird vom Playlist-Handler genutzt, um
// `fresh=1` idempotent zu machen: VHS holt EVENT-Playlists periodisch neu
// mit derselben URL — würde der Server jedes Mal die ffmpeg-Session killen,
// käme nie ein zweites Segment beim Player an, Playback hängt nach 4 s.
// Akzeptiert wird `fresh` nur, wenn die Session noch nicht existiert oder
// älter als ein paar Sekunden ist (bereits genug Material produziert hat).
func (m *Manager) SessionAge(itemID int64, profile Profile, audioIdx int, startSec float64, deinterlace bool) (bool, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dei := 0
	if deinterlace {
		dei = 1
	}
	id := fmt.Sprintf("%d-%s-a%d-%d-d%d", itemID, profile.ID, audioIdx, int(startSec), dei)
	if s, ok := m.sessions[id]; ok {
		return true, time.Since(s.StartedAt)
	}
	return false, 0
}

// ConsumeFresh meldet, ob ein `fresh=1`-Request fuer diesen Session-Key gerade
// JETZT ausgefuehrt werden darf (= der erste fresh=1 in einem 60-Sekunden-
// Fenster). Wird true zurueckgegeben, ist der Caller fuer das anschliessende
// StopSession+StartOrGet zustaendig; der Timestamp wurde intern gesetzt, sodass
// VHS-Playlist-Reloads in den naechsten 60 s false bekommen und keine zweite
// Stop-and-Restart-Welle ausloesen.
func (m *Manager) ConsumeFresh(itemID int64, profile Profile, audioIdx int, startSec float64, deinterlace bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	dei := 0
	if deinterlace {
		dei = 1
	}
	id := fmt.Sprintf("%d-%s-a%d-%d-d%d", itemID, profile.ID, audioIdx, int(startSec), dei)
	if last, ok := m.freshHandledAt[id]; ok && time.Since(last) < 60*time.Second {
		return false
	}
	m.freshHandledAt[id] = time.Now()
	// Map gelegentlich aufraeumen — alte Eintraege bleiben sonst forever
	if len(m.freshHandledAt) > 200 {
		cutoff := time.Now().Add(-10 * time.Minute)
		for k, t := range m.freshHandledAt {
			if t.Before(cutoff) {
				delete(m.freshHandledAt, k)
			}
		}
	}
	return true
}

// LookupSession liefert die existierende Session zur Key oder nil, wenn keine
// läuft. Im Gegensatz zu StartOrGet wird KEINE neue Session erzeugt — der
// Progress-Handler nutzt das, damit ein Progress-Poll mit nicht ganz exakt
// passenden Parametern (z.B. veraltetem `start=`) nicht versehentlich eine
// zweite ffmpeg-Instanz parallel zur eigentlichen Playback-Session startet.
// Eine konkurrierende ffmpeg-Instanz wuerde sich mit der laufenden Wiedergabe
// um die iGPU/CPU streiten und Stutter erzeugen.
func (m *Manager) LookupSession(itemID int64, profile Profile, audioIdx int, startSec float64, deinterlace bool) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	dei := 0
	if deinterlace {
		dei = 1
	}
	id := fmt.Sprintf("%d-%s-a%d-%d-d%d", itemID, profile.ID, audioIdx, int(startSec), dei)
	if s, ok := m.sessions[id]; ok {
		s.Touch()
		return s
	}
	return nil
}

// StartOrGet returns an existing session for the item or starts a new one.
// Sessions werden pro (Item, Profil, Audio-Stream, Start-Offset, Deinterlace) gehalten.
// audioIdx = -1 → default (erster Audio-Stream). Sonst ffprobe-Stream-Index.
// deinterlace = true → backend-spezifischer Deinterlace-Filter wird in die
// Filter-Chain eingebaut (für interlaced Content wie alte TV-Captures).
func (m *Manager) StartOrGet(itemID int64, inputPath string, profile Profile, audioIdx int, startSec float64, deinterlace bool) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dei := 0
	if deinterlace {
		dei = 1
	}
	id := fmt.Sprintf("%d-%s-a%d-%d-d%d", itemID, profile.ID, audioIdx, int(startSec), dei)
	if s, ok := m.sessions[id]; ok {
		s.Touch()
		return s, nil
	}
	dir := filepath.Join(m.cacheDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	_ = cleanDir(dir)

	ctx, cancel := context.WithCancel(context.Background())
	args := m.buildArgs(inputPath, dir, profile, audioIdx, startSec, deinterlace)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stderr = io.Discard
	cmd.Stdout = io.Discard

	log.Printf("[transcode] start session=%s profile=%s audio=%d hw=%v start=%.1fs deinterlace=%v", id, profile.ID, audioIdx, m.hw.Available, startSec, deinterlace)
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}

	now := time.Now()
	s := &Session{
		ID:        id,
		ItemID:    itemID,
		Profile:   profile.ID,
		AudioIdx:  audioIdx,
		Dir:       dir,
		StartSec:  startSec,
		StartedAt: now,
		Cmd:       cmd,
		cancel:    cancel,
		lastUsed:  now,
		done:      make(chan struct{}),
	}
	go func() {
		_ = cmd.Wait()
		close(s.done)
	}()
	m.sessions[id] = s
	return s, nil
}

func (m *Manager) buildArgs(input, outDir string, p Profile, audioIdx int, startSec float64, deinterlace bool) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}

	if startSec > 0 {
		args = append(args, "-ss", strconv.FormatFloat(startSec, 'f', 3, 64))
	}

	// Video-Filter: Skalierung (CPU) + VAAPI-Upload. Software-scaling ist billig
	// und funktioniert mit beliebigen Input-Codecs.
	scaleFilter := ""
	if p.MaxHeight > 0 {
		scaleFilter = fmt.Sprintf("scale=-2:%d:force_original_aspect_ratio=decrease,", p.MaxHeight)
	}
	// CPU-Deinterlace-Filter (für NVENC- und Software-Pfad). bwdif liefert
	// minimal bessere Kanten als yadif, vergleichbare CPU-Last.
	cpuDeintFilter := ""
	if deinterlace {
		cpuDeintFilter = "bwdif,"
	}

	switch m.hw.Selected {
	case BackendVAAPI:
		// VAAPI-Pfad: HW-Deinterlace via deinterlace_vaapi vor scale_vaapi.
		// Reihenfolge: hwupload → format=nv12 + hwupload → deinterlace → scale.
		// Da unsere Standard-Chain Software-scale + hwupload ist, sieht das so aus:
		// scaleFilter (CPU) + format=nv12,hwupload + (optional) deinterlace_vaapi.
		vaapiPost := "format=nv12,hwupload"
		if deinterlace {
			vaapiPost += ",deinterlace_vaapi=mode=motion_adaptive"
		}
		args = append(args,
			"-vaapi_device", m.hw.VAAPIDevice,
			"-i", input,
			"-vf", scaleFilter+vaapiPost,
			"-c:v", "h264_vaapi",
		)
		if p.VideoKbps > 0 {
			args = append(args,
				"-b:v", fmt.Sprintf("%dk", p.VideoKbps),
				"-maxrate", fmt.Sprintf("%dk", p.VideoKbps*3/2),
				"-bufsize", fmt.Sprintf("%dk", p.VideoKbps*2),
			)
		} else {
			args = append(args, "-qp", "23")
		}
	case BackendNVENC:
		// NVENC-Pfad: `-hwaccel cuda` ohne `-hwaccel_output_format cuda` →
		// Frames werden nach dem Decode in den CPU-RAM kopiert, die CPU-
		// Scale-/Format-Filter laufen normal, ffmpeg lädt für den NVENC-
		// Encoder automatisch wieder hoch. Robuste Filter-Kompatibilität
		// mit allen Input-Codecs.
		videoFilter := cpuDeintFilter + scaleFilter
		if videoFilter != "" {
			videoFilter = strings.TrimSuffix(videoFilter, ",") // trailing Komma
		}
		args = append(args, "-hwaccel", "cuda", "-i", input)
		if videoFilter != "" {
			args = append(args, "-vf", videoFilter)
		}
		args = append(args,
			"-c:v", "h264_nvenc",
			"-preset", "p4",
			"-rc", "vbr",
			"-pix_fmt", "yuv420p",
		)
		if p.VideoKbps > 0 {
			args = append(args,
				"-b:v", fmt.Sprintf("%dk", p.VideoKbps),
				"-maxrate", fmt.Sprintf("%dk", p.VideoKbps*3/2),
				"-bufsize", fmt.Sprintf("%dk", p.VideoKbps*2),
			)
		} else {
			args = append(args, "-cq", "23")
		}
	default:
		// Software (libx264)
		videoFilter := cpuDeintFilter + scaleFilter
		if videoFilter != "" {
			videoFilter = strings.TrimSuffix(videoFilter, ",") // trailing Komma wegnehmen
		}
		args = append(args, "-i", input)
		if videoFilter != "" {
			args = append(args, "-vf", videoFilter)
		}
		args = append(args,
			"-c:v", "libx264",
			"-preset", "veryfast",
			"-pix_fmt", "yuv420p",
		)
		if p.VideoKbps > 0 {
			args = append(args,
				"-b:v", fmt.Sprintf("%dk", p.VideoKbps),
				"-maxrate", fmt.Sprintf("%dk", p.VideoKbps*3/2),
				"-bufsize", fmt.Sprintf("%dk", p.VideoKbps*2),
			)
		} else {
			args = append(args, "-crf", "23")
		}
	}

	// Stream-Mapping: erstes Video + ausgewählter Audio-Stream
	args = append(args, "-map", "0:v:0")
	if audioIdx >= 0 {
		args = append(args, "-map", fmt.Sprintf("0:%d", audioIdx))
	} else {
		args = append(args, "-map", "0:a:0?") // ? = optional, falls keine Audio-Spur
	}

	audioKbps := p.AudioKbps
	if audioKbps <= 0 {
		audioKbps = 160
	}
	args = append(args,
		"-c:a", "aac",
		"-b:a", fmt.Sprintf("%dk", audioKbps),
		"-ac", "2",
		// Erzwinge Keyframe alle 2 s im Output. Ohne das schneidet
		// `-hls_time 2` Segmente nur an den Keyframes der QUELLE — und die
		// liegen oft 4–5 s auseinander. Dann wird das erste Segment trotz
		// hls_time=2 erst nach 4–5 s fertig. Mit `expr:gte(t,n_forced*2)`
		// setzt der Encoder beim ENCODEN selbst alle 2 s einen Keyframe.
		// Frame-Rate-unabhaengig, funktioniert bei VAAPI / NVENC / libx264.
		"-force_key_frames", "expr:gte(t,n_forced*2)",
		"-f", "hls",
		// Segment-Dauer 2 s: erstes Segment ist nach ~2 s startfaehig (statt
		// 4 s bei `hls_time=4`). Browser-Buffer-Anzeige aktualisiert sich
		// doppelt so haeufig, gefuehlte Latenz beim Play-Klick halbiert.
		// Kein nennenswerter Overhead — doppelt so viele kleine .ts-Files,
		// VHS reloadet die EVENT-Playlist statt alle 4 s nun alle 2 s
		// (`fresh=1`-Idempotenz hat ein 60 s-Fenster, also weiter unkritisch).
		"-hls_time", "2",
		"-hls_list_size", "0",
		// `independent_segments` erlaubt Segment-level Seek. `append_list`
		// ist BEWUSST NICHT dabei — ffmpeg würde sonst `#EXT-X-DISCONTINUITY`
		// direkt vor seg00000.ts einfügen, und VHS interpretiert das als
		// Lücke am Anfang → kein Buffer-Aufbau bei currentTime=0. Da wir die
		// Session mit `cleanDir` frisch starten, brauchen wir append_list nicht.
		"-hls_flags", "independent_segments",
		// EVENT-Playlist statt Live: Video.js behandelt sie als bounded
		// (kein Live-Edge-Snap beim Play nach Pause).
		"-hls_playlist_type", "event",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(outDir, "seg%05d.ts"),
		filepath.Join(outDir, "index.m3u8"),
	)
	return args
}

func (s *Session) Touch() {
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
}

func (s *Session) Stop() {
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
	}
	_ = os.RemoveAll(s.Dir)
}

// Position gibt zurück, bis zu welcher Quelldatei-Sekunde ffmpeg transcodiert hat.
// Wird aus der Summe der #EXTINF-Dauern in index.m3u8 + StartSec berechnet.
// Verwendung: Client kann ahead = Position - currentTime anzeigen.
func (s *Session) Position() (float64, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir, "index.m3u8"))
	if err != nil {
		return 0, err
	}
	total := 0.0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#EXTINF:") {
			continue
		}
		rest := line[len("#EXTINF:"):]
		if c := strings.IndexByte(rest, ','); c >= 0 {
			rest = rest[:c]
		}
		if d, err := strconv.ParseFloat(rest, 64); err == nil {
			total += d
		}
	}
	return s.StartSec + total, nil
}

// Done zeigt an, ob der ffmpeg-Prozess bereits beendet ist (Transcode abgeschlossen
// oder abgebrochen). Der Client kann damit die Progress-Anzeige verstecken.
func (s *Session) Done() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// WaitForPlaylist blocks until the playlist file exists (up to timeout).
func (s *Session) WaitForPlaylist(timeout time.Duration) error {
	playlist := filepath.Join(s.Dir, "index.m3u8")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(playlist); err == nil {
			return nil
		}
		select {
		case <-s.done:
			return errors.New("ffmpeg beendet, bevor Playlist erstellt wurde")
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("timeout beim Warten auf Playlist")
}

func cleanDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
	return nil
}
