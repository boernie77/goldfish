package download

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/boernie77/goldfish/internal/playback"
)

func TestNeedsDownscale(t *testing.T) {
	orig := playback.ProfileByID("orig")
	p1080 := playback.ProfileByID("1080p")

	cases := []struct {
		name            string
		profile         playback.Profile
		itemHeight      int
		itemBitrateKbps int
		want            bool
	}{
		{"orig nie", orig, 4000, 100000, false},
		{"unter Cap", p1080, 1080, 4000, false},
		{"Hoehe ueber Cap", p1080, 2160, 4000, true},
		{"Bitrate ueber Cap", p1080, 1080, 6000, true},
		{"beides unter Cap", p1080, 720, 2000, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := needsDownscale(c.profile, c.itemHeight, c.itemBitrateKbps)
			if got != c.want {
				t.Errorf("needsDownscale(%+v, %d, %d) = %v, want %v", c.profile, c.itemHeight, c.itemBitrateKbps, got, c.want)
			}
		})
	}
}

// TestPlanProfileCachePaths prüft, dass "Automatisch"/Profil-unter-Cap den
// UNVERÄNDERTEN <itemID>.mp4-Cache-Pfad nutzt (kein neuer Fall gegenüber der
// bisherigen Compat-Download-Logik), während ein tatsächlicher Downscale
// einen eigenen, profilspezifischen Pfad bekommt — siehe plan()-Kommentar.
func TestPlanProfileCachePaths(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.mkv") // nicht mp4/h264/aac -> braucht ohnehin Prep
	if err := os.WriteFile(src, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(dir, "cache")

	orig := playback.ProfileByID("orig")
	p720 := playback.ProfileByID("720p")

	// Automatisch: normaler Cache-Pfad ohne Profil-Suffix.
	needsPrep, outPath, _, _, err := plan(cacheDir, 42, src, "mkv", "hevc", "aac", orig, 2160, 20000)
	if err != nil {
		t.Fatal(err)
	}
	if !needsPrep {
		t.Fatal("needsPrep sollte true sein (mkv-Container)")
	}
	if filepath.Base(outPath) != "42.mp4" {
		t.Errorf("outPath = %q, want 42.mp4", filepath.Base(outPath))
	}

	// 720p-Profil, Item ist 2160p -> Downscale nötig -> eigener Pfad.
	needsPrep, outPath, _, _, err = plan(cacheDir, 42, src, "mkv", "hevc", "aac", p720, 2160, 20000)
	if err != nil {
		t.Fatal(err)
	}
	if !needsPrep {
		t.Fatal("needsPrep sollte true sein (Downscale)")
	}
	if filepath.Base(outPath) != "42-720p.mp4" {
		t.Errorf("outPath = %q, want 42-720p.mp4", filepath.Base(outPath))
	}

	// 720p-Profil, Item ist bereits 720p -> kein Downscale nötig -> normaler Pfad.
	needsPrep, outPath, _, _, err = plan(cacheDir, 42, src, "mkv", "hevc", "aac", p720, 720, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if !needsPrep {
		t.Fatal("needsPrep sollte true sein (mkv-Container, unabhängig vom Profil)")
	}
	if filepath.Base(outPath) != "42.mp4" {
		t.Errorf("outPath = %q, want 42.mp4 (Item liegt schon unter dem Cap)", filepath.Base(outPath))
	}
}

// TestClampProfileToSource — Regressionstest für den "größer statt kleiner"-
// Bug (User-Report 2026-09-11, ZWEI Runden): eine Downscale-Zielbitrate darf
// NIE über der bekannten Quell-GESAMTbitrate liegen (Video+Audio zusammen —
// `itemBitrateKbps` ist ffprobes `format.bit_rate`, keine reine
// Video-Bitrate, siehe Funktionskommentar).
func TestClampProfileToSource(t *testing.T) {
	p480hq := playback.ProfileByID("480p-hq") // VideoKbps=2000, AudioKbps=128

	cases := []struct {
		name            string
		itemBitrateKbps int
		wantVideoKbps   int
	}{
		{"Quelle unbekannt (0) -> Katalogwert bleibt", 0, 2000},
		{"Quelle effizienter als Katalogwert -> geklemmt (Audio-Anteil abgezogen)", 1900, 1900 - 128},
		{"realer Bug-Fall: 1,94 Mbps Quelle", 1940, 1940 - 128},
		{"Quelle über Katalogwert -> Katalogwert bleibt (kein Hochsetzen)", 5000, 2000},
		{"sehr niedrige Quelle -> Sicherheits-Untergrenze greift", 250, 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := clampProfileToSource(p480hq, c.itemBitrateKbps)
			if got.VideoKbps != c.wantVideoKbps {
				t.Errorf("clampProfileToSource(.., %d).VideoKbps = %d, want %d", c.itemBitrateKbps, got.VideoKbps, c.wantVideoKbps)
			}
			if got.AudioKbps != p480hq.AudioKbps {
				t.Errorf("AudioKbps sollte unverändert bleiben, got %d want %d", got.AudioKbps, p480hq.AudioKbps)
			}
			// Kernversprechen: Video+Audio zusammen nie über der Quelle (außer
			// bei der Sicherheits-Untergrenze, die bewusst nicht weiter runter darf).
			if c.itemBitrateKbps >= 400 && got.VideoKbps+got.AudioKbps > c.itemBitrateKbps {
				t.Errorf("Video+Audio (%d) liegt über der Quell-Bitrate (%d)", got.VideoKbps+got.AudioKbps, c.itemBitrateKbps)
			}
		})
	}
}
