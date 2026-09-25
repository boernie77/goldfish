package playback

import (
	"math"
	"strings"
	"testing"
	"time"
)

// 29,97 fps: 60 Bilder je Segment = 2,002 s — genau der am 2026-09-25 am
// Server gemessene Wert.
func TestPlanVOD2997(t *testing.T) {
	p, ok := planVOD(30000, 1001, 100, 0)
	if !ok {
		t.Fatal("Plan erwartet")
	}
	if p.FramesPerSeg != 60 || math.Abs(p.SegDur-2.002) > 1e-9 {
		t.Fatalf("60 Bilder / 2,002 s erwartet, ist %d / %.6f", p.FramesPerSeg, p.SegDur)
	}
	// 100 s / 2,002 = 49 volle Segmente + Rest 1,902 s.
	if p.Segments != 50 || math.Abs(p.LastDur-1.902) > 1e-6 {
		t.Fatalf("50 Segmente, letztes 1,902 s erwartet: %d / %.6f", p.Segments, p.LastDur)
	}
}

func TestPlanVODExactFrameRates(t *testing.T) {
	for _, c := range []struct {
		num, den, frames int
		dur              float64
	}{
		{25, 1, 50, 2.0},
		{24000, 1001, 48, 2.002},
		{60000, 1001, 120, 2.002},
		{50, 1, 100, 2.0},
	} {
		p, ok := planVOD(c.num, c.den, 600, 0)
		if !ok || p.FramesPerSeg != c.frames || math.Abs(p.SegDur-c.dur) > 1e-9 {
			t.Errorf("%d/%d: %d Bilder / %.3f s erwartet, ist %d / %.6f", c.num, c.den, c.frames, c.dur, p.FramesPerSeg, p.SegDur)
		}
	}
}

// Ein winziger Rest am Ende wird kein eigenes Segment — ffmpeg schreibt es
// womöglich nie, der Player bekäme dort einen Fehler.
func TestPlanVODDropsTinyTail(t *testing.T) {
	p, _ := planVOD(25, 1, 10.1, 0) // 5 × 2 s + 0,1 s
	if p.Segments != 5 || p.LastDur != 2.0 {
		t.Fatalf("5 Segmente ohne Mini-Rest erwartet: %d / %.3f", p.Segments, p.LastDur)
	}
}

func TestPlanVODRespectsStart(t *testing.T) {
	p, _ := planVOD(25, 1, 100, 60) // 40 s Rest
	if p.Segments != 20 {
		t.Fatalf("20 Segmente ab 60 s erwartet, ist %d", p.Segments)
	}
}

func TestPlanVODRejects(t *testing.T) {
	if _, ok := planVOD(0, 1, 100, 0); ok {
		t.Error("fps 0 darf nicht planbar sein")
	}
	if _, ok := planVOD(25, 1, 100, 98); ok {
		t.Error("weniger als 4 s Rest darf nicht planbar sein")
	}
	if _, ok := planVOD(1000, 1, 100, 0); ok {
		t.Error("unplausible Bildrate darf nicht planbar sein")
	}
}

func TestParseFrameRates(t *testing.T) {
	if f := parseFrameRates("30000/1001,30000/1001"); !f.ok || f.num != 30000 || f.den != 1001 {
		t.Fatalf("konstante Bildrate falsch gelesen: %+v", f)
	}
	if f := parseFrameRates("30/1,24000/1001"); f.ok {
		t.Fatal("variable Bildrate darf nicht als konstant gelten")
	}
	if f := parseFrameRates("0/0,0/0"); f.ok {
		t.Fatal("0/0 darf nicht gelten")
	}
}

func TestBuildVODPlaylist(t *testing.T) {
	p, _ := planVOD(30000, 1001, 7, 0) // 3 Segmente: 2,002 / 2,002 / 2,996
	pl := BuildVODPlaylist(p, "profile=orig&hls=vod")
	for _, want := range []string{
		"#EXT-X-PLAYLIST-TYPE:VOD", "#EXT-X-ENDLIST", "#EXT-X-TARGETDURATION:3",
		"seg00000.ts?profile=orig&hls=vod", "seg00002.ts?profile=orig&hls=vod",
	} {
		if !strings.Contains(pl, want) {
			t.Errorf("Playlist ohne %q:\n%s", want, pl)
		}
	}
	if CountPlaylistSegments(pl) != p.Segments {
		t.Fatalf("%d Segmente erwartet, %d in der Playlist", p.Segments, CountPlaylistSegments(pl))
	}
	// TARGETDURATION muss jede gerundete Segmentdauer abdecken.
	if p.LastDur > 3 {
		t.Fatalf("letztes Segment länger als TARGETDURATION: %.3f", p.LastDur)
	}
}

func TestParseVODSegment(t *testing.T) {
	if k, ok := ParseVODSegment("seg00042.ts"); !ok || k != 42 {
		t.Fatalf("seg00042.ts → 42 erwartet, %d/%v", k, ok)
	}
	for _, bad := range []string{"index.m3u8", "seg.ts", "segabc.ts", "seg-1.ts"} {
		if _, ok := ParseVODSegment(bad); ok {
			t.Errorf("%q darf nicht als Segment gelten", bad)
		}
	}
}

func TestApplyVODArgs(t *testing.T) {
	m := vaapiMgr()
	p, _ := planVOD(30000, 1001, 600, 100)
	first := strings.Join(m.buildArgs("/m/f.mkv", "/tmp/o", Profile{ID: "orig"}, -1, 100, false, false, stageHardware, &p, 0), " ")
	for _, want := range []string{"expr:gte(n,n_forced*60)", "-hls_time 1.902", "independent_segments+temp_file", "-start_number 0", "-ss 100.000"} {
		if !strings.Contains(first, want) {
			t.Errorf("erster Lauf ohne %q:\n%s", want, first)
		}
	}
	if strings.Contains(first, "-output_ts_offset") {
		t.Error("erster Lauf darf keinen Zeitversatz haben")
	}
	if !strings.HasSuffix(first, "/tmp/o/index.m3u8") {
		t.Errorf("Ausgabedatei muss letztes Argument bleiben:\n%s", first)
	}

	// Neustart bei Segment 7: Quelle ab 100 + 7×2,002, Zeitstempel ab 14,014.
	re := strings.Join(m.buildArgs("/m/f.mkv", "/tmp/o", Profile{ID: "orig"}, -1, 100, false, false, stageHardware, &p, 7), " ")
	for _, want := range []string{"-ss 114.014", "-start_number 7", "-output_ts_offset 14.014000"} {
		if !strings.Contains(re, want) {
			t.Errorf("Neustart ohne %q:\n%s", want, re)
		}
	}
	if !strings.HasSuffix(re, "/tmp/o/index.m3u8") {
		t.Errorf("Ausgabedatei muss letztes Argument bleiben:\n%s", re)
	}
}

// EVENT-Modus bleibt byte-identisch zu vorher.
func TestEventArgsUnchangedWithoutPlan(t *testing.T) {
	got := strings.Join(vaapiMgr().buildArgs("/m/f.mkv", "/tmp/o", Profile{ID: "orig"}, -1, 0, false, false, stageHardware, nil, 0), " ")
	if !strings.Contains(got, "expr:gte(t,n_forced*2)") || !strings.Contains(got, "-hls_time 2 ") ||
		strings.Contains(got, "temp_file") || strings.Contains(got, "-start_number") {
		t.Fatalf("EVENT-Argumente verändert:\n%s", got)
	}
}

func TestVodDecide(t *testing.T) {
	sec := time.Second
	cases := []struct {
		name                  string
		k, runStart, produced int
		age                   time.Duration
		done, failed          bool
		want                  vodAction
	}{
		{"vor dem Lauf", 3, 10, 20, 5 * sec, false, false, vodRestart},
		{"knapp voraus", 21, 10, 20, 5 * sec, false, false, vodWait},
		// 10 Segmente in 5 s = 2/s → 50 Segmente Abstand = 25 s → neu starten
		{"weit voraus", 70, 10, 20, 5 * sec, false, false, vodRestart},
		// 100 Segmente in 10 s = 10/s → 30 Abstand = 3 s → warten
		{"schnelles ffmpeg holt auf", 130, 0, 100, 10 * sec, false, false, vodWait},
		{"Anlauf ohne Messwert", 50, 0, 0, 1 * sec, false, false, vodWait},
		{"Anlauf haengt", 50, 0, 0, 4 * sec, false, false, vodRestart},
		{"Dateiende erreicht", 30, 0, 25, 9 * sec, true, false, vodGiveUp},
		{"Lauf gescheitert", 30, 0, 25, 9 * sec, true, true, vodRestart},
		{"gerade umbenannt", 19, 10, 20, 5 * sec, false, false, vodWait},
	}
	for _, c := range cases {
		if got := vodDecide(c.k, c.runStart, c.produced, c.age, c.done, c.failed); got != c.want {
			t.Errorf("%s: %v erwartet, %v", c.name, c.want, got)
		}
	}
}
