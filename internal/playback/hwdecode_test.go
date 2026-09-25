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
	stage := stageHardware
	if softwareDecode {
		stage = stageFullSoftware
	}
	return argsOfStage(t, m, p, deinterlace, stage)
}

func argsOfStage(t *testing.T, m *Manager, p Profile, deinterlace bool, stage fallbackStage) string {
	t.Helper()
	return strings.Join(m.buildArgs("/m/film.mkv", "/tmp/out", p, -1, 0, deinterlace, false, stage), " ")
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

// 🔴 Der Rückfallweg muss KOMPLETT ohne Grafikeinheit auskommen.
//
// Bis 2026-09-17 prüfte dieser Test das Gegenteil („exakt die alte
// Kommandozeile"): CPU-Decode, dann `hwupload` zurück auf die Grafikeinheit
// und `h264_vaapi` zum Encodieren. Das ist für den Zweck des Rückfalls
// nutzlos — bei einer WMV3-Datei (VC-1-Familie) meldete ffmpeg live
// „No support for codec wmv3 profile 1" und „Failed setup for format vaapi",
// und zwar in BEIDEN Anläufen: `vainfo` listet auf dieser Hardware kein
// VAProfileVC1* (Intel hat den VC-1-Decoder ab Gen 12 gestrichen). Die
// Wiedergabe war damit tot statt nur langsam.
//
// Der Rückfall existiert für genau die Dateien, welche die Grafikeinheit
// NICHT kann — er darf sie folglich an keiner Stelle mehr anfassen.
func TestStreamingSoftwareDecodeFallback(t *testing.T) {
	got := argsOf(t, vaapiMgr(), Profile{ID: "orig"}, false, true)
	for _, unwanted := range []string{"-hwaccel", "hwupload", "h264_vaapi", "vaapi_device", "scale_vaapi"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("Rückfall darf die Grafikeinheit nicht berühren, enthält %q:\n%s", unwanted, got)
		}
	}
	for _, want := range []string{"-c:v libx264", "-pix_fmt yuv420p"} {
		if !strings.Contains(got, want) {
			t.Errorf("reiner Software-Weg erwartet %q:\n%s", want, got)
		}
	}
}

// Auch mit Zielauflösung und Entflimmern bleibt der Rückfall rein CPU-seitig
// — dort müssen die CPU-Filter (bwdif/scale) greifen, nicht ihre
// VAAPI-Gegenstücke.
func TestSoftwareFallbackUsesCpuFilters(t *testing.T) {
	got := argsOf(t, vaapiMgr(), Profile{ID: "720p", MaxHeight: 720}, true, true)
	if !strings.Contains(got, "bwdif") {
		t.Errorf("CPU-Entflimmern (bwdif) erwartet:\n%s", got)
	}
	if strings.Contains(got, "deinterlace_vaapi") || strings.Contains(got, "scale_vaapi") {
		t.Errorf("VAAPI-Filter im Software-Rückfall:\n%s", got)
	}
	if !strings.Contains(got, "scale=") {
		t.Errorf("CPU-Skalierung erwartet:\n%s", got)
	}
}

// Musik hat keinen Videostrom — dort darf gar nichts Video-Spezifisches
// auftauchen, auch kein Hardware-Decode.
func TestAudioOnlyUntouched(t *testing.T) {
	got := strings.Join(vaapiMgr().buildArgs("/m/song.flac", "/tmp/out",
		Profile{ID: "orig"}, -1, 0, false, true, stageHardware), " ")
	for _, unwanted := range []string{"-hwaccel", "scale_vaapi", "-c:v"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("Audio-Pfad enthält %q:\n%s", unwanted, got)
		}
	}
}

// Die erste Rückfallstufe (2026-09-22): CPU dekodiert, die Grafikeinheit
// encodiert. Muss den Upload erzeugen und darf KEINEN Hardware-Decode
// anfordern — sonst entsteht genau der Fehler, der die Stufe auslöst.
// Gemessen an AV1: 11,0 s CPU-Zeit statt 38,6 s im reinen Software-Weg.
func TestFallbackCpuDecodeWithVaapiEncode(t *testing.T) {
	got := argsOfStage(t, vaapiMgr(), Profile{ID: "orig"}, false, stageCPUEncodeVAAPI)
	for _, want := range []string{
		"-vaapi_device /dev/dri/renderD128",
		"-vf format=nv12,hwupload",
		"-c:v h264_vaapi",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Stufe 1 erwartet %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"-hwaccel", "libx264", "scale_vaapi"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("Stufe 1 darf %q nicht enthalten:\n%s", unwanted, got)
		}
	}
}

// Auch mit Skalierung und Entflimmern bleibt die Filterkette in Stufe 1 auf
// der CPU — nur der Upload ans Ende gehört dazu.
func TestFallbackCpuDecodeWithScaleAndDeinterlace(t *testing.T) {
	got := argsOfStage(t, vaapiMgr(), Profile{ID: "720p", MaxHeight: 720}, true, stageCPUEncodeVAAPI)
	for _, want := range []string{"bwdif", "scale=-2:720", "format=nv12,hwupload"} {
		if !strings.Contains(got, want) {
			t.Errorf("Stufe 1 mit Skalierung/Entflimmern erwartet %q:\n%s", want, got)
		}
	}
}

// Ein zweiter Rückfall ist nicht möglich — sonst Endlosschleife bei einer
// wirklich kaputten Datei.
func TestRetryRefusesWhenAlreadySoftware(t *testing.T) {
	m := vaapiMgr()
	m.sessions = map[string]*Session{}
	s := &Session{ID: "x", stage: stageFullSoftware}
	m.sessions["x"] = s
	if _, err := m.RetryWithFallback(s); err == nil {
		t.Error("ein zweiter Rückfall müsste abgelehnt werden")
	}
}

// Nach der ersten Stufe ist die letzte noch möglich — aber nur einmal:
// Stufe 1 → Stufe 2 → Schluss.
func TestRetryStageOrder(t *testing.T) {
	m := vaapiMgr()
	m.sessions = map[string]*Session{}

	first := &Session{ID: "y", stage: stageHardware}
	m.sessions["y"] = first
	next, err := m.RetryWithFallback(first)
	if err != nil {
		t.Fatalf("Stufe 1 müsste angenommen werden: %v", err)
	}
	if next == nil || next.stage != stageCPUEncodeVAAPI {
		t.Fatalf("nach Hardware erwartet Stufe 1, bekam %v", next)
	}

	m.sessions["y"] = next
	last, err := m.RetryWithFallback(next)
	if err != nil {
		t.Fatalf("Stufe 2 müsste angenommen werden: %v", err)
	}
	if last == nil || last.stage != stageFullSoftware {
		t.Fatalf("nach Stufe 1 erwartet Stufe 2, bekam %v", last)
	}

	m.sessions["y"] = last
	if _, err = m.RetryWithFallback(last); err == nil {
		t.Error("nach der letzten Stufe darf es keinen weiteren Versuch geben")
	}
}

// Ohne VAAPI (NVENC oder gar keine Hardware) gibt es keine Upload-Stufe —
// dann geht es direkt auf den reinen Software-Weg.
func TestRetrySkipsVaapiStageWithoutHardware(t *testing.T) {
	m := &Manager{hw: HWAccel{Selected: BackendNVENC}}
	m.sessions = map[string]*Session{}
	s := &Session{ID: "z", stage: stageHardware}
	m.sessions["z"] = s
	next, err := m.RetryWithFallback(s)
	if err != nil {
		t.Fatalf("Rückfall müsste angenommen werden: %v", err)
	}
	if next == nil || next.stage != stageFullSoftware {
		t.Fatalf("ohne VAAPI erwartet Stufe 2 direkt, bekam %v", next)
	}
}

// Eine inzwischen abgelöste Sitzung (Seek, GC) darf nicht wiederbelebt werden.
func TestRetryRefusesStaleSession(t *testing.T) {
	m := vaapiMgr()
	m.sessions = map[string]*Session{}
	if _, err := m.RetryWithFallback(&Session{ID: "weg"}); err == nil {
		t.Error("eine nicht mehr eingetragene Sitzung müsste abgelehnt werden")
	}
}

// AV1 auf VAAPI-Hardware scheitert am Decoder IMMER (Intel-iGPU kann laut
// vainfo kein AV1 decodieren) — die Startstufe soll deshalb direkt
// stageCPUEncodeVAAPI sein und nicht erst den zum Scheitern verurteilten
// stageHardware-Versuch durchlaufen (siehe initialStageFor-Kommentar).
func TestInitialStageSkipsHardwareForAV1OnVAAPI(t *testing.T) {
	m := vaapiMgr()
	if got := m.initialStageFor("av1"); got != stageCPUEncodeVAAPI {
		t.Fatalf("AV1 auf VAAPI erwartet stageCPUEncodeVAAPI, bekam %v", got)
	}
	// Groß-/Kleinschreibung darf keine Rolle spielen (ffprobe liefert
	// durchgängig klein, aber die Funktion soll robust sein).
	if got := m.initialStageFor("AV1"); got != stageCPUEncodeVAAPI {
		t.Fatalf("Groß-AV1 auf VAAPI erwartet stageCPUEncodeVAAPI, bekam %v", got)
	}
}

// Alle anderen Codecs (und alles ohne VAAPI) starten unverändert bei
// stageHardware — die Sonderbehandlung gilt exklusiv für AV1+VAAPI.
func TestInitialStageStaysHardwareOtherwise(t *testing.T) {
	m := vaapiMgr()
	for _, codec := range []string{"hevc", "h264", "vp9", "mpeg2video", ""} {
		if got := m.initialStageFor(codec); got != stageHardware {
			t.Errorf("Codec %q auf VAAPI erwartet stageHardware, bekam %v", codec, got)
		}
	}
	// NVENC-Backend: auch bei AV1 keine Sonderbehandlung, die betrifft nur VAAPI.
	nvenc := &Manager{hw: HWAccel{Selected: BackendNVENC, Available: true}}
	if got := nvenc.initialStageFor("av1"); got != stageHardware {
		t.Fatalf("AV1 auf NVENC erwartet stageHardware (unveraendert), bekam %v", got)
	}
}
