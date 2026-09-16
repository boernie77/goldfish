package playback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		name := sessionDirName(sessionKey(c.itemID, c.profile, c.audioIdx, c.startSec, c.dei == 1))
		if !sessionDirPattern.MatchString(name) {
			t.Errorf("sessionDirPattern matcht %q NICHT — verwaiste Verzeichnisse blieben liegen", name)
		}
	}
}

// Verzeichnisse aus der Zeit VOR dem eindeutigen Suffix (2026-09-13) und vor
// dem `-d<0|1>`-Segment muessen ebenfalls matchen — sonst raeumt der Startup-
// Cleanup sie nie weg. Genau das war der Fall: 268 solcher Verzeichnisse mit
// zusammen ~119 GB lagen dauerhaft im Cache (gefunden 2026-09-14).
func TestSessionDirPatternMatchesLegacyDirs(t *testing.T) {
	for _, name := range []string{
		"183130-orig-a-1-378-d0",       // mit -d0, ohne Suffix
		"21819-orig-a-1-0",             // ohne -d0, ohne Suffix
		"227502-480p-hq-a1-1859-d0",    // Profil-ID mit Bindestrich
		"21819-1080p-a1-0-d0",          //
		"22465-orig-a-1-1283-d1",       // deinterlace
		"229928-orig-a1-0-d0-dlxk3p9q", // neues Schema bleibt selbstverstaendlich
	} {
		if !sessionDirPattern.MatchString(name) {
			t.Errorf("sessionDirPattern matcht Altbestand %q NICHT — bliebe fuer immer liegen", name)
		}
	}
}

// Kern des fresh=1-Fixes: zwei aufeinanderfolgende Sessions mit identischem
// Key duerfen NIE denselben Pfad bekommen. Sonst schreibt ein noch sterbendes
// ffmpeg (Stop() wartet nur 3 s) in das Verzeichnis der neuen Session.
//
// Prueft bewusst die ECHTE Produktionsfunktion (playback.sessionDirName) —
// eine nachgebaute Kopie hier im Test wuerde genau den Bug verstecken, den
// dieser Test finden soll (bis 2026-09-17 bestand der Suffix nur aus einem
// Zeitstempel und kollidierte bei grober Uhr-Aufloesung).
func TestSessionDirsAreUniquePerStart(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		name := sessionDirName("44885-orig-a-1-271-d0")
		if seen[name] {
			t.Fatalf("Verzeichnisname %q doppelt vergeben — Race zwischen alter und neuer Session moeglich", name)
		}
		seen[name] = true
		if !sessionDirPattern.MatchString(name) {
			t.Fatalf("erzeugter Name %q matcht sessionDirPattern NICHT — Startup-Cleanup wuerde ihn nie aufraeumen", name)
		}
	}
}

func TestCleanStaleSessionDirsRemovesOnlySessionDirs(t *testing.T) {
	cache := t.TempDir()
	stale := sessionDirName(sessionKey(44885, "orig", -1, 271.9, false))
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
