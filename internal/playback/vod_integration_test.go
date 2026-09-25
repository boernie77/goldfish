package playback

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Echter Durchlauf mit ffmpeg (Software-Weg): Testvideo erzeugen, VOD-Sitzung
// starten, Segmente holen, weit springen (Neustart) und prüfen, dass die
// Segmente nach dem Sprung zeitlich genau dort liegen, wo die Playlist sie
// verspricht. Übersprungen ohne ffmpeg oder mit -short.
func TestVODIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("kein ffmpeg")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	// 120 s, 29,97 fps, mit Ton — dieselbe Bildrate wie der Server-Messfall.
	gen := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30000/1001",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "120", "-c:v", "libx264", "-preset", "ultrafast", "-g", "250",
		"-c:a", "aac", "-shortest", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("Testvideo: %v %s", err, out)
	}

	plan := PlanVOD(src, 120, 0, false, false)
	if plan == nil {
		t.Fatal("kein VOD-Plan für konstante 29,97 fps")
	}
	m := &Manager{
		sessions:    map[string]*Session{},
		cacheDir:    filepath.Join(dir, "cache"),
		hw:          HWAccel{Selected: BackendSoftware},
		freshTokens: map[string]string{},
		stoppedAt:   map[int64]time.Time{},
		vodRestarts: map[string]time.Time{},
	}
	s, err := m.StartOrGetVOD(1, src, ProfileByID("orig"), -1, 0, false, false, 180, "h264", plan, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.mu.Lock(); cur := m.sessions[s.ID]; m.mu.Unlock(); cur.Stop() }()

	ctx := context.Background()
	get := func(k int) string {
		t.Helper()
		p, err := m.EnsureVODSegment(ctx, s.ID, k, time.Now())
		if err != nil {
			t.Fatalf("Segment %d: %v", k, err)
		}
		return p
	}
	firstPTSOf := func(path, stream string) float64 {
		t.Helper()
		out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", stream,
			"-show_entries", "packet=pts_time", "-of", "csv=p=0", "-read_intervals", "%+#1", path).Output()
		if err != nil {
			t.Fatalf("ffprobe %s: %v", path, err)
		}
		line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		v, err := strconv.ParseFloat(strings.TrimSuffix(line, ","), 64)
		if err != nil {
			t.Fatalf("PTS nicht lesbar: %q", line)
		}
		return v
	}

	firstPTS := func(path string) float64 { return firstPTSOf(path, "v:0") }
	p0 := get(0)
	base := firstPTS(p0) // MPEG-TS beginnt mit ~1,4 s Mux-Vorlauf
	get(1)

	// Sprung weit nach vorn: Neustart bei Segment 50 (≈ 100 s). Direkt nach
	// Segment 1 erzwungen, damit der erste Lauf Segment 50 sicher nicht mehr
	// selbst geschrieben hat — ob gewartet oder neu gestartet wird, hängt
	// sonst von der Rechnergeschwindigkeit ab (das prüft TestVodDecide).
	m.mu.Lock()
	first := m.sessions[s.ID]
	m.mu.Unlock()
	if err := m.restartVOD(s.ID, first, 50); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	cur := m.sessions[s.ID]
	m.mu.Unlock()
	if cur.spec.vodStartSeg != 50 {
		t.Fatalf("Neustart bei Segment 50 erwartet, Lauf beginnt bei %d", cur.spec.vodStartSeg)
	}
	start := time.Now()
	p50 := get(50)
	t.Logf("Segment 50 nach Neustart in %v", time.Since(start).Round(time.Millisecond))
	// Bild: höchstens ±0,1 s (≈ 3 Bilder) neben dem Versprechen der Playlist.
	// Gemessen: VAAPI am Server 21 ms, libx264 lokal 67 ms (B-Frame-Vorlauf
	// geht beim Lauf mit Zeitversatz anders in die Zeitstempel ein).
	want := base + plan.SegStart(50)
	gotV := firstPTS(p50)
	if gotV < want-0.1 || gotV > want+0.1 {
		t.Fatalf("Segment 50 beginnt bei %.3f s, Playlist verspricht %.3f s", gotV, want)
	}
	// Bild zu Ton muss in beiden Läufen gleich stehen (Lippensynchronität):
	// Abweichung unter 45 ms, der üblichen Wahrnehmungsschwelle.
	avFirst := firstPTSOf(p0, "v:0") - firstPTSOf(p0, "a:0")
	avRestart := gotV - firstPTSOf(p50, "a:0")
	t.Logf("Versatz Bild: %+.0f ms, Bild-Ton erster Lauf %+.0f ms, nach Neustart %+.0f ms",
		(gotV-want)*1000, avFirst*1000, avRestart*1000)
	if d := avRestart - avFirst; d > 0.045 || d < -0.045 {
		t.Fatalf("Bild-Ton-Versatz nach Neustart um %.0f ms verändert", d*1000)
	}
	// Die vor dem Sprung geschriebenen Segmente bleiben abrufbar.
	if p := get(1); p == "" {
		t.Fatal("Segment 1 nach dem Neustart verloren")
	}
	// Das letzte Segment der Playlist muss ffmpeg auch liefern.
	get(plan.Segments - 1)
	// Hinter dem Ende gibt es nichts.
	if _, err := m.EnsureVODSegment(ctx, s.ID, plan.Segments, time.Now()); err == nil {
		t.Fatal("Segment hinter dem Ende darf es nicht geben")
	}
}
