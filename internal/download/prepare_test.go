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
