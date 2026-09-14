package playback

import (
	"strings"
	"testing"
)

func vaapiMgr() *Manager {
	return &Manager{hw: HWAccel{Selected: BackendVAAPI, VAAPIDevice: "/dev/dri/renderD128", Available: true}}
}

func argsOf(t *testing.T, m *Manager, p Profile, deinterlace, softwareDecode bool) string {
	t.Helper()
	return strings.Join(m.buildArgs("/m/film.mkv", "/tmp/out", p, -1, 0, deinterlace, false, softwareDecode), " ")
}

// Diese Kommandozeile wurde am 2026-09-14 am laufenden Server gemessen:
// 6 s CPU-Zeit gegenüber 186 s mit dem alten Weg, bei 60 s 4K-HEVC und
// profile=orig — genau der Fall, der eine einzelne Sitzung dauerhaft bei
// ~570 % CPU hielt.
func TestStreamingUsesHardwareDecode(t *testing.T) {
	got := argsOf(t, vaapiMgr(), Profile{ID: "orig"}, false, false)
	for _, want := range []string{
		"-hwaccel vaapi", "-hwaccel_output_format vaapi",
		"-vf scale_vaapi=format=nv12", "-c:v h264_vaapi",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("erwartet %q, fehlt:\n%s", want, got)
		}
	}
	if strings.Contains(got, "hwupload") {
		t.Errorf("hwupload gehört zum alten Weg:\n%s", got)
	}
	// Ohne Größenänderung bleibt scale_vaapi trotzdem nötig — allein für die
	// 8-Bit-Wandlung, sonst scheitert h264_vaapi an 10-Bit-HDR.
	if !strings.Contains(got, "format=nv12") {
		t.Errorf("format=nv12 fehlt — 10-Bit-HDR würde den Encoder brechen:\n%s", got)
	}
	if strings.Index(got, "-hwaccel vaapi") > strings.Index(got, " -i ") {
		t.Error("-hwaccel muss VOR -i stehen")
	}
}

func TestStreamingHardwareDecodeWithScale(t *testing.T) {
	got := argsOf(t, vaapiMgr(), Profile{ID: "720p", MaxHeight: 720, VideoKbps: 2500}, false, false)
	want := "scale_vaapi=w=-2:h=720:force_original_aspect_ratio=decrease:format=nv12"
	if !strings.Contains(got, want) {
		t.Errorf("erwartet %q:\n%s", want, got)
	}
	if strings.Contains(got, "scale=-2:720") {
		t.Errorf("CPU-Skalierung gehört zum alten Weg:\n%s", got)
	}
}

// Beim Entflimmern muss deinterlace_vaapi VOR scale_vaapi laufen: auf voller
// Auflösung entflimmern und erst danach verkleinern gibt das bessere Bild.
func TestStreamingDeinterlaceBeforeScale(t *testing.T) {
	got := argsOf(t, vaapiMgr(), Profile{ID: "720p", MaxHeight: 720}, true, false)
	di, sc := strings.Index(got, "deinterlace_vaapi"), strings.Index(got, "scale_vaapi")
	if di < 0 || sc < 0 {
		t.Fatalf("beide Filter erwartet:\n%s", got)
	}
	if di > sc {
		t.Errorf("deinterlace_vaapi muss vor scale_vaapi stehen:\n%s", got)
	}
	if strings.Contains(got, "bwdif") {
		t.Errorf("bwdif ist der CPU-Weg, hier unerwünscht:\n%s", got)
	}
}

// Der Rückfallweg muss exakt die alte Kommandozeile ergeben — er ist die
// Rettung, wenn die Grafikeinheit an einer Datei scheitert.
func TestStreamingSoftwareDecodeFallback(t *testing.T) {
	got := argsOf(t, vaapiMgr(), Profile{ID: "orig"}, false, true)
	if strings.Contains(got, "-hwaccel") {
		t.Errorf("Rückfall darf nicht hardware-dekodieren:\n%s", got)
	}
	for _, want := range []string{"format=nv12,hwupload", "-c:v h264_vaapi"} {
		if !strings.Contains(got, want) {
			t.Errorf("alter Weg erwartet %q:\n%s", want, got)
		}
	}
}

// Musik hat keinen Videostrom — dort darf gar nichts Video-Spezifisches
// auftauchen, auch kein Hardware-Decode.
func TestAudioOnlyUntouched(t *testing.T) {
	got := strings.Join(vaapiMgr().buildArgs("/m/song.flac", "/tmp/out",
		Profile{ID: "orig"}, -1, 0, false, true, false), " ")
	for _, unwanted := range []string{"-hwaccel", "scale_vaapi", "-c:v"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("Audio-Pfad enthält %q:\n%s", unwanted, got)
		}
	}
}

// Ein zweiter Rückfall ist nicht möglich — sonst Endlosschleife bei einer
// wirklich kaputten Datei.
func TestRetryRefusesWhenAlreadySoftware(t *testing.T) {
	m := vaapiMgr()
	m.sessions = map[string]*Session{}
	s := &Session{ID: "x", softwareDecode: true}
	m.sessions["x"] = s
	if _, err := m.RetryWithSoftwareDecode(s); err == nil {
		t.Error("ein zweiter Rückfall müsste abgelehnt werden")
	}
}

// Eine inzwischen abgelöste Sitzung (Seek, GC) darf nicht wiederbelebt werden.
func TestRetryRefusesStaleSession(t *testing.T) {
	m := vaapiMgr()
	m.sessions = map[string]*Session{}
	if _, err := m.RetryWithSoftwareDecode(&Session{ID: "weg"}); err == nil {
		t.Error("eine nicht mehr eingetragene Sitzung müsste abgelehnt werden")
	}
}
