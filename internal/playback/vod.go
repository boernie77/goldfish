package playback

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
)

// ─── VOD-Playlist (Jellyfin-Ansatz, seit 2026-09-25, hinter Schalter) ───────
//
// Statt einer wachsenden EVENT-Playlist liefert der Server sofort die
// KOMPLETTE Playlist inkl. ENDLIST aus, berechnet aus Laufzeit und Bildrate.
// Der Player hält damit den ganzen Film für vorhanden: keine Nachlade-Pflicht
// (→ kein -12888 „Playlist unchanged" bei ffmpeg-Stocken mehr), korrekte
// Laufzeit und natives Spulen in jedem Player.
//
// Fragt der Player ein Segment an, das ffmpeg noch nicht geschrieben hat,
// wartet der Server, wenn ffmpeg bald dort ist — sonst startet er ffmpeg im
// SELBEN Verzeichnis genau an diesem Segment neu (-ss, -output_ts_offset,
// -start_number). Voraussetzung: Segmentgrenzen exakt vorab berechenbar.
// Deshalb schneidet ffmpeg nach BILDANZAHL (force_key_frames auf `n`), nicht
// nach Zeit: F = ceil(2 s × fps) Bilder je Segment, Dauer F/fps. Am
// 2026-09-25 am laufenden Server gemessen (29,97 fps, VAAPI): alle Segmente
// exakt 2,002 s; ein Neustart bei Segment 7 lag 21 ms (Bild) / 45 ms (Ton)
// neben dem durchgehenden Lauf — unter einem Einzelbild.
//
// Nicht für: variable Bildrate (Grenzen nicht vorab berechenbar), Entflimmern
// (CPU-`bwdif` verdoppelt die Bildrate), Musik, unbekannte Laufzeit. Dort
// bleibt es bei der EVENT-Playlist (siehe PlanVOD).

// VODPlan beschreibt die vorab berechnete Segmentierung einer Sitzung.
type VODPlan struct {
	FramesPerSeg int     // Bilder je Segment (Keyframe-Abstand)
	SegDur       float64 // Dauer eines vollen Segments in Sekunden
	Segments     int     // Anzahl Segmente ab Sitzungsstart
	LastDur      float64 // Dauer des letzten Segments
}

// SegStart liefert den Beginn von Segment k relativ zum Sitzungsstart.
func (p *VODPlan) SegStart(k int) float64 { return float64(k) * p.SegDur }

// planVOD berechnet die Segmentierung für fps = num/den und die Restlaufzeit
// ab startSec. ok=false, wenn die Werte keine verlässliche Planung erlauben.
func planVOD(num, den int, durationSec, startSec float64) (VODPlan, bool) {
	if num <= 0 || den <= 0 {
		return VODPlan{}, false
	}
	fps := float64(num) / float64(den)
	if fps < 10 || fps > 121 {
		return VODPlan{}, false
	}
	rest := durationSec - startSec
	if rest < 4 {
		return VODPlan{}, false
	}
	f := int(math.Ceil(2*fps - 1e-6))
	segDur := float64(f) * float64(den) / float64(num)
	n := int(rest / segDur)
	last := segDur
	if r := rest - float64(n)*segDur; r > 0.25 {
		n++
		last = r
	}
	return VODPlan{FramesPerSeg: f, SegDur: segDur, Segments: n, LastDur: last}, true
}

// BuildVODPlaylist erzeugt die komplette Playlist. segQuery wird an jede
// Segment-URI gehängt (Profil/Start/Tonspur + hls=vod, damit der
// Segment-Handler den VOD-Weg nimmt).
func BuildVODPlaylist(p VODPlan, segQuery string) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", int(math.Ceil(p.SegDur)))
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	for k := 0; k < p.Segments; k++ {
		d := p.SegDur
		if k == p.Segments-1 {
			d = p.LastDur
		}
		fmt.Fprintf(&b, "#EXTINF:%.6f,\n%s", d, VODSegmentName(k))
		if segQuery != "" {
			b.WriteByte('?')
			b.WriteString(segQuery)
		}
		b.WriteByte('\n')
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// VODSegmentName ist der Dateiname von Segment k — identisch zu ffmpegs
// `-hls_segment_filename seg%05d.ts` mit `-start_number`.
func VODSegmentName(k int) string { return fmt.Sprintf("seg%05d.ts", k) }

// ParseVODSegment liest k aus einem Segmentnamen („seg00042.ts" → 42).
func ParseVODSegment(name string) (int, bool) {
	if !strings.HasPrefix(name, "seg") || !strings.HasSuffix(name, ".ts") {
		return 0, false
	}
	k, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "seg"), ".ts"))
	return k, err == nil && k >= 0
}

// ─── Bildrate ──────────────────────────────────────────────────────────────

type fpsInfo struct {
	num, den int
	ok       bool
}

var (
	fpsCacheMu sync.Mutex
	fpsCache   = map[string]fpsInfo{}
)

// probeFrameRate liest die Bildrate der Quelle per ffprobe (gecacht je Pfad).
// ok=false bei variabler Bildrate: r_frame_rate und avg_frame_rate weichen
// dann voneinander ab, und die Segmentgrenzen wären nicht vorab berechenbar.
func probeFrameRate(path string) (int, int, bool) {
	fpsCacheMu.Lock()
	if c, hit := fpsCache[path]; hit {
		fpsCacheMu.Unlock()
		return c.num, c.den, c.ok
	}
	fpsCacheMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "V:0",
		"-show_entries", "stream=r_frame_rate,avg_frame_rate", "-of", "csv=p=0", path).Output()
	info := fpsInfo{}
	if err == nil {
		info = parseFrameRates(strings.TrimSpace(string(out)))
	}
	fpsCacheMu.Lock()
	fpsCache[path] = info
	fpsCacheMu.Unlock()
	return info.num, info.den, info.ok
}

// parseFrameRates wertet „r_frame_rate,avg_frame_rate" aus („30000/1001,30000/1001").
func parseFrameRates(s string) fpsInfo {
	parts := strings.Split(strings.SplitN(s, "\n", 2)[0], ",")
	if len(parts) < 2 {
		return fpsInfo{}
	}
	rn, rd, ok1 := parseRational(parts[0])
	an, ad, ok2 := parseRational(parts[1])
	if !ok1 || !ok2 {
		return fpsInfo{}
	}
	r, a := float64(rn)/float64(rd), float64(an)/float64(ad)
	if math.Abs(r-a)/r > 0.005 {
		return fpsInfo{} // variable Bildrate
	}
	return fpsInfo{num: rn, den: rd, ok: true}
}

func parseRational(s string) (int, int, bool) {
	n, d, found := strings.Cut(strings.TrimSpace(s), "/")
	if !found {
		return 0, 0, false
	}
	a, err1 := strconv.Atoi(n)
	b, err2 := strconv.Atoi(d)
	if err1 != nil || err2 != nil || a <= 0 || b <= 0 {
		return 0, 0, false
	}
	return a, b, true
}

// PlanVOD prüft, ob eine Sitzung als VOD laufen kann, und liefert den Plan.
// nil = bisherige EVENT-Playlist verwenden.
func PlanVOD(inputPath string, durationSec, startSec float64, deinterlace, audioOnly bool) *VODPlan {
	if audioOnly || deinterlace || durationSec <= 0 {
		return nil
	}
	num, den, ok := probeFrameRate(inputPath)
	if !ok {
		return nil
	}
	p, ok := planVOD(num, den, durationSec, startSec)
	if !ok {
		return nil
	}
	return &p
}

// ─── Segment besorgen: warten oder neu starten ─────────────────────────────

type vodAction int

const (
	vodWait vodAction = iota
	vodRestart
	vodGiveUp
)

// vodMaxWait: so lange darf die erwartete Wartezeit auf ein Segment höchstens
// sein, bevor stattdessen neu gestartet wird. Bewusst unter den 8 s
// Standard-Zeitlimit von ExoPlayer (Android/Fire TV).
const vodMaxWait = 5 * time.Second

// vodDecide entscheidet für ein fehlendes Segment k. runStart: erstes Segment
// des laufenden ffmpeg-Laufs, produced: nächstes noch nicht geschriebenes
// Segment, runAge: Laufzeit des Laufs, done/failed: ffmpeg beendet.
func vodDecide(k, runStart, produced int, runAge time.Duration, done, failed bool) vodAction {
	if k < runStart {
		return vodRestart // davor, dieser Lauf kommt nie dorthin
	}
	if done {
		if failed {
			return vodRestart
		}
		if k >= produced {
			return vodGiveUp // regulär am Dateiende angekommen, Segment gibt es nicht
		}
		return vodRestart // geschrieben, aber weg (sollte nicht vorkommen)
	}
	if k < produced {
		return vodWait // gerade im Umbenennen (temp_file)
	}
	gap := k - produced
	if gap <= 2 {
		return vodWait
	}
	made := produced - runStart
	if made < 1 || runAge <= 0 {
		// Lauf fängt gerade erst an: ohne Messwert nur kurz auf ihn setzen.
		if runAge < 3*time.Second {
			return vodWait
		}
		return vodRestart
	}
	rate := float64(made) / runAge.Seconds() // Segmente je Sekunde
	if time.Duration(float64(gap+1)/rate*float64(time.Second)) <= vodMaxWait {
		return vodWait
	}
	return vodRestart
}

// ErrVODSegmentGone: das Segment existiert nicht (Dateiende überschritten oder
// Sitzung beendet).
var ErrVODSegmentGone = errors.New("Segment existiert nicht")

// vodProduced liefert das nächste noch nicht fertige Segment des laufenden
// Laufs (aus ffmpegs eigener index.m3u8, die nur fertige Segmente listet).
func (s *Session) vodProduced() int {
	data, err := os.ReadFile(filepath.Join(s.Dir, "index.m3u8"))
	if err != nil {
		return s.spec.vodStartSeg
	}
	return s.spec.vodStartSeg + CountPlaylistSegments(string(data))
}

// EnsureVODSegment besorgt Segment k der Sitzung id: sofort, nach Warten oder
// nach einem Neustart von ffmpeg an Segment k. arrived ist der Eingang der
// Anfrage — nur Anfragen, die NACH dem letzten Neustart dieser Sitzung
// eingetroffen sind, dürfen erneut neu starten. Sonst könnten veraltete
// Anfragen von vor einem Sprung ffmpeg zurückreißen und sich mit der
// aktuellen Anfrage ein Ping-Pong liefern (Lehre aus 2026-09-13).
func (m *Manager) EnsureVODSegment(ctx context.Context, id string, k int, arrived time.Time) (string, error) {
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		s := m.sessions[id]
		m.mu.Unlock()
		if s == nil || s.spec.vod == nil {
			return "", ErrVODSegmentGone
		}
		if k >= s.spec.vod.Segments {
			return "", ErrVODSegmentGone
		}
		s.Touch()
		path := filepath.Join(s.Dir, VODSegmentName(k))
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		act := vodDecide(k, s.spec.vodStartSeg, s.vodProduced(), time.Since(s.StartedAt), s.Done(), s.Done() && s.Failed())
		switch act {
		case vodGiveUp:
			return "", ErrVODSegmentGone
		case vodRestart:
			if arrived.After(m.vodLastRestart(id)) {
				if err := m.restartVOD(id, s, k); err != nil {
					return "", err
				}
				continue
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return "", ErrVODSegmentGone
}

// StoppedWithin meldet, ob fuer das Item innerhalb von d ein expliziter
// Client-Stop (StopAllForItem) eintraf. Der VOD-Segment-Weg erzeugt dann
// keine neue Sitzung: verirrte Anfragen eines geschlossenen Players wuerden
// sonst eine Umwandlung starten, die nie wieder jemand beendet (die Lehre
// vom 2026-09-14, dort ueber die Playlist-Anfrage).
func (m *Manager) StoppedWithin(itemID int64, d time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.stoppedAt[itemID]
	return ok && time.Since(t) < d
}

func (m *Manager) vodLastRestart(id string) time.Time {
	m.vodMu.Lock()
	defer m.vodMu.Unlock()
	return m.vodRestarts[id]
}

// restartVOD beendet den laufenden ffmpeg-Lauf von old und startet im SELBEN
// Verzeichnis einen neuen ab Segment k. Die bereits geschriebenen Segmente
// bleiben liegen und werden weiter ausgeliefert. Kein Einfluss aufs Budget:
// ein Lauf ersetzt den anderen.
func (m *Manager) restartVOD(id string, old *Session, k int) error {
	m.vodMu.Lock()
	defer m.vodMu.Unlock()
	m.mu.Lock()
	if m.sessions[id] != old {
		m.mu.Unlock()
		return nil // ein anderer Request war schneller
	}
	m.mu.Unlock()

	old.stopProcess()
	_ = os.Remove(filepath.Join(old.Dir, "index.m3u8"))

	spec := old.spec
	spec.vodStartSeg = k
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[id] != old {
		return nil // inzwischen gestoppt (Client-Stop, GC)
	}
	s, err := m.launchLocked(id, spec, old.stage, old.Dir)
	if err != nil {
		delete(m.sessions, id)
		return err
	}
	s.lastUsed = time.Now()
	if m.vodRestarts == nil {
		m.vodRestarts = map[string]time.Time{}
	}
	m.vodRestarts[id] = time.Now()
	log.Printf("[transcode] session %s: VOD-Neustart bei Segment %d (%.1fs)", id, k, spec.startSec+spec.vod.SegStart(k))
	return nil
}
