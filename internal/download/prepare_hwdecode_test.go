package download

import (
	"strings"
	"testing"

	"github.com/boernie77/goldfish/internal/playback"
)

func argsString(a []string) string { return strings.Join(a, " ") }

func vaapi() playback.HWAccel {
	return playback.HWAccel{Selected: playback.BackendVAAPI, VAAPIDevice: "/dev/dri/renderD128"}
}

// Diese Kommandozeile wurde am 2026-09-14 am laufenden Server gemessen:
// 5 s CPU-Zeit gegenüber 157 s mit dem alten Software-Decode-Weg, bei
// identischer Ausgabe (853x480, yuv420p). Der Test hält fest, dass der Code
// genau das erzeugt — ein stiller Rückfall auf Software-Decode würde sonst
// erst wieder durch einen heißen Server auffallen.
func TestDownscaleUsesHardwareDecode(t *testing.T) {
	profile := playback.Profile{ID: "480p", MaxHeight: 480, VideoKbps: 600, AudioKbps: 96}
	args := buildArgs("/m/film.mkv", "/tmp/out.mp4", "hevc", "hvc1", "yuv420p10le",
		[]AudioStream{{Index: 1, Codec: "truehd"}}, vaapi(), false, true, profile, true)
	got := argsString(args)

	for _, want := range []string{
		"-hwaccel vaapi",
		"-hwaccel_output_format vaapi",
		"scale_vaapi=w=-2:h=480:force_original_aspect_ratio=decrease:format=nv12",
		"-c:v h264_vaapi",
		"-b:v 600k",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("erwartet %q in den Argumenten, fehlt:\n%s", want, got)
		}
	}
	// Software-Weg darf NICHT mehr auftauchen.
	for _, unwanted := range []string{"hwupload", "scale=-2:480"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("%q sollte beim HW-Decode nicht mehr vorkommen:\n%s", unwanted, got)
		}
	}
	// -vaapi_device genau EINMAL: einmal vor -i (hwaccelDecodeArgs). Ein
	// zweites Vorkommen hinter -i wäre ein Überbleibsel des alten Pfads.
	if n := strings.Count(got, "-vaapi_device"); n != 1 {
		t.Errorf("-vaapi_device %dx, erwartet genau 1x:\n%s", n, got)
	}
	// Die GPU-Skalierung muss NACH -i stehen, der Decoder-Schalter davor.
	if strings.Index(got, "-hwaccel vaapi") > strings.Index(got, " -i ") {
		t.Error("-hwaccel muss VOR -i stehen")
	}
}

// Der Software-Rückfall (runPrep wiederholt mit forceSoftware, wenn die
// Hardware streikt) darf keine VAAPI-Reste enthalten — sonst scheitert auch
// der zweite Versuch.
func TestDownscaleSoftwareFallbackHasNoVAAPI(t *testing.T) {
	profile := playback.Profile{ID: "480p", MaxHeight: 480, VideoKbps: 600}
	args := buildArgs("/m/film.mkv", "/tmp/out.mp4", "hevc", "hvc1", "yuv420p10le",
		[]AudioStream{{Index: 1}}, vaapi(), true /*forceSoftware*/, true, profile, true)
	got := argsString(args)
	for _, unwanted := range []string{"-hwaccel", "vaapi", "hwupload"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("Software-Rückfall enthält %q:\n%s", unwanted, got)
		}
	}
}

// NVENC wurde bewusst NICHT umgestellt (nicht gemessen, keine Karte im
// Einsatz) — der Pfad muss unverändert ohne HW-Decode laufen.
func TestDownscaleNVENCUnchanged(t *testing.T) {
	hw := playback.HWAccel{Selected: playback.BackendNVENC}
	profile := playback.Profile{ID: "720p", MaxHeight: 720, VideoKbps: 2500}
	args := buildArgs("/m/film.mkv", "/tmp/out.mp4", "hevc", "hvc1", "yuv420p",
		[]AudioStream{{Index: 1}}, hw, false, true, profile, true)
	got := argsString(args)
	if strings.Contains(got, "-hwaccel") {
		t.Errorf("NVENC-Downscale sollte unverändert ohne HW-Decode laufen:\n%s", got)
	}
	if !strings.Contains(got, "h264_nvenc") {
		t.Errorf("h264_nvenc fehlt:\n%s", got)
	}
}

// Ein reiner Codec-Fix ohne Herunterrechnen nutzte HW-Decode schon vorher —
// das darf der Umbau nicht kaputt gemacht haben.
func TestNonDownscaleStillUsesHardwareDecode(t *testing.T) {
	profile := playback.Profile{ID: "orig"}
	args := buildArgs("/m/film.mkv", "/tmp/out.mp4", "av1", "", "yuv420p",
		[]AudioStream{{Index: 1}}, vaapi(), false, true, profile, false)
	if got := argsString(args); !strings.Contains(got, "-hwaccel vaapi") {
		t.Errorf("HW-Decode fehlt beim reinen Codec-Fix:\n%s", got)
	}
}
