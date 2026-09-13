package playback

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sessionDirName baut einen Verzeichnisnamen exakt so zusammen wie StartOrGet:
// Session-Key plus eindeutiger base36-Suffix.
func sessionDirName(itemID int64, profileID string, audioIdx int, startSec float64, dei int) string {
	id := fmt.Sprintf("%d-%s-a%d-%d-d%d", itemID, profileID, audioIdx, int(startSec), dei)
	return id + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// Der Startup-Cleanup loescht alles, was sessionDirPattern matcht. Im selben
// cacheDir liegen aber auch der Download- und der Trailer-Cache — matcht das
// Muster die versehentlich, loescht ein Neustart teuer erzeugte Dateien.
func TestSessionDirPatternNeverMatchesOtherCaches(t *testing.T) {
	for _, name := range []string{
		"downloads", "trailers", "posters", "people", "whisper-models",
		"generated-subs", "downloads-old", "trailers.tmp", "",
		"44885.mp4", "goldfish.db",
	} {
		if sessionDirPattern.MatchString(name) {
			t.Errorf("sessionDirPattern matcht %q — der Startup-Cleanup wuerde das loeschen", name)
		}
	}
}

func TestSessionDirPatternMatchesRealSessionDirs(t *testing.T) {
	cases := []struct {
		itemID   int64
		profile  string
		audioIdx int
		startSec float64
		dei      int
	}{
		{44885, "orig", -1, 271.9, 0},   // der Fall aus dem Fehlerbericht
		{44885, "orig", -1, 271.9, 1},   // deinterlace
		{1, "1080p", 0, 0, 0},           // Start bei 0
		{53846, "480p-hq", 2, 433.4, 0}, // Profil-ID mit Bindestrich + Ziffern
		{999999, "720p", 11, 99999, 1},
	}
	for _, c := range cases {
		name := sessionDirName(c.itemID, c.profile, c.audioIdx, c.startSec, c.dei)
		if !sessionDirPattern.MatchString(name) {
			t.Errorf("sessionDirPattern matcht %q NICHT — verwaiste Verzeichnisse blieben liegen", name)
		}
	}
}

// Kern des fresh=1-Fixes: zwei aufeinanderfolgende Sessions mit identischem
// Key duerfen NIE denselben Pfad bekommen. Sonst schreibt ein noch sterbendes
// ffmpeg (Stop() wartet nur 3 s) in das Verzeichnis der neuen Session.
func TestSessionDirsAreUniquePerStart(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		name := sessionDirName(44885, "orig", -1, 271.9, 0)
		if seen[name] {
			t.Fatalf("Verzeichnisname %q doppelt vergeben — Race zwischen alter und neuer Session moeglich", name)
		}
		seen[name] = true
	}
}

func TestCleanStaleSessionDirsRemovesOnlySessionDirs(t *testing.T) {
	cache := t.TempDir()
	stale := sessionDirName(44885, "orig", -1, 271.9, 0)
	keep := []string{"downloads", "trailers"}

	for _, d := range append([]string{stale}, keep...) {
		if err := os.MkdirAll(filepath.Join(cache, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Eine Nutzdatei im Download-Cache, die den Neustart ueberleben muss.
	payload := filepath.Join(cache, "downloads", "44885.mp4")
	if err := os.WriteFile(payload, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cleanStaleSessionDirs(cache)

	if _, err := os.Stat(filepath.Join(cache, stale)); !os.IsNotExist(err) {
		t.Errorf("verwaistes Session-Verzeichnis %q wurde nicht entfernt", stale)
	}
	for _, d := range keep {
		if _, err := os.Stat(filepath.Join(cache, d)); err != nil {
			t.Errorf("%q wurde faelschlich entfernt: %v", d, err)
		}
	}
	if _, err := os.Stat(payload); err != nil {
		t.Errorf("Datei im Download-Cache wurde zerstoert: %v", err)
	}
}

func TestRingBufferKeepsTail(t *testing.T) {
	r := &ringBuffer{max: 16}
	for i := 0; i < 10; i++ {
		if _, err := r.Write([]byte("0123456789")); err != nil {
			t.Fatal(err)
		}
	}
	got := r.String()
	if len(got) > 16 {
		t.Errorf("ringBuffer waechst ueber max hinaus: %d Bytes", len(got))
	}
	if !strings.HasSuffix("0123456789", got[len(got)-1:]) && got != "" {
		t.Logf("Inhalt: %q", got)
	}
}

func TestRingBufferTrimsWhitespace(t *testing.T) {
	r := &ringBuffer{max: 4096}
	if _, err := r.Write([]byte("  ffmpeg: kaputt\n\n")); err != nil {
		t.Fatal(err)
	}
	if got := r.String(); got != "ffmpeg: kaputt" {
		t.Errorf("String() = %q, erwartet %q", got, "ffmpeg: kaputt")
	}
}
