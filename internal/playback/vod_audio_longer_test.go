package playback

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Regression 14.10.2026 (Unforgettable S01E05, Apple TV -12938/404): der Ton
// läuft länger als das Bild. Der Plan darf nur Segmente ankündigen, die ffmpeg
// auch schreibt — sonst 404 auf die letzten Segmente. Übersprungen ohne ffmpeg.
func TestVODPlanVideoShorterThanAudio(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("kein ffmpeg")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	// Bild 100 s, Ton 104 s (Container-Laufzeit = 104 s).
	gen := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24000/1001",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-map", "0:v", "-map", "1:a", "-t", "104", "-c:v", "libx264", "-preset", "ultrafast",
		"-vf", "trim=duration=100,setpts=PTS-STARTPTS", "-c:a", "aac", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("Testvideo: %v %s", err, out)
	}
	plan := PlanVOD(src, 104, 0, false, false)
	if plan == nil {
		t.Fatal("kein VOD-Plan")
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
	for k := 0; k < plan.Segments; k++ {
		if _, err := m.EnsureVODSegment(ctx, s.ID, k, time.Now()); err != nil {
			t.Fatalf("angekündigtes Segment %d von %d fehlt: %v", k, plan.Segments, err)
		}
	}
}
