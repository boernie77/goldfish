package api

import (
	"os"
	"path/filepath"
	"testing"
)

// mkFiles legt eine Reihe leerer Dateien in dir an.
func mkFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("WEBVTT\n\n"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", n, err)
		}
	}
}

func TestNormalizeSidecarLang(t *testing.T) {
	cases := map[string]string{
		"de": "deu", "DE": "deu", "deu": "deu", "ger": "deu", "german": "deu",
		"en": "eng", "eng": "eng", "English": "eng",
		"de-DE": "deu", "en-US": "eng", "en-orig": "eng", "pt_BR": "por",
		"":  "",
		"2": "", "1080p": "", "xyz": "", "sdh": "",
	}
	for in, want := range cases {
		if got := normalizeSidecarLang(in); got != want {
			t.Errorf("normalizeSidecarLang(%q) = %q, erwartet %q", in, got, want)
		}
	}
}

func TestFindSidecarSubsYtDlpNaming(t *testing.T) {
	dir := t.TempDir()
	// Realer Fall aus der YouTube-Bibliothek: Rautezeichen + Leerzeichen im Namen.
	video := filepath.Join(dir, "#215 A Hot Week Heavy Machines.mkv")
	mkFiles(t, dir,
		"#215 A Hot Week Heavy Machines.mkv",
		"#215 A Hot Week Heavy Machines.de.vtt",
		"#215 A Hot Week Heavy Machines.en.vtt",
	)
	found := findSidecarSubs(video)
	if len(found) != 2 {
		t.Fatalf("erwartet 2 Sidecars, gefunden %d: %+v", len(found), found)
	}
	// Sortierung nach Pfad: ".de.vtt" vor ".en.vtt" — das garantiert stabile
	// synthetische Stream-Indizes zwischen playbackInfo und subtitleVTT.
	if found[0].Language != "deu" || found[1].Language != "eng" {
		t.Errorf("Sprachen/Reihenfolge falsch: %q, %q", found[0].Language, found[1].Language)
	}
	if found[0].Ext != ".vtt" {
		t.Errorf("Ext = %q", found[0].Ext)
	}
}

func TestFindSidecarSubsVariants(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "Film.mkv")
	mkFiles(t, dir,
		"Film.mkv",
		"Film.vtt",            // ohne Sprachkürzel
		"Film.de.srt",         // SRT, wird später gewandelt
		"Film.en.forced.vtt",  // Forced-Flag
		"Film.ger.sdh.ass",    // 3-Buchstaben-Code + Zusatz
		"Film.DE.ssa",         // Groß-/Kleinschreibung
		"Film.de.sub",         // nicht unterstützte Endung
		"Film.txt",            // keine Untertitel-Datei
		"Anderer Film.de.vtt", // anderes Video
	)
	found := findSidecarSubs(video)
	if len(found) != 5 {
		names := []string{}
		for _, f := range found {
			names = append(names, filepath.Base(f.Path))
		}
		t.Fatalf("erwartet 5 Sidecars, gefunden %d: %v", len(found), names)
	}
	byName := map[string]sidecarSub{}
	for _, f := range found {
		byName[filepath.Base(f.Path)] = f
	}
	if s := byName["Film.vtt"]; s.Language != "" {
		t.Errorf("Film.vtt sollte ohne Sprache sein, ist %q", s.Language)
	}
	if s := byName["Film.en.forced.vtt"]; s.Language != "eng" || !s.Forced {
		t.Errorf("Forced-Erkennung falsch: %+v", s)
	}
	if s := byName["Film.ger.sdh.ass"]; s.Language != "deu" || len(s.Notes) != 1 || s.Notes[0] != "sdh" {
		t.Errorf("sdh-Zusatz falsch: %+v", s)
	}
	if s := byName["Film.DE.ssa"]; s.Language != "deu" {
		t.Errorf("Groß-/Kleinschreibung falsch: %+v", s)
	}
}

// Der Präfix-Vergleich darf nicht die Sidecars eines ANDEREN Videos einsammeln,
// dessen Name mit demselben Stamm beginnt.
func TestFindSidecarSubsIgnoresOtherVideosSidecar(t *testing.T) {
	dir := t.TempDir()
	mkFiles(t, dir,
		"Film.mkv",
		"Film.2.mkv",
		"Film.de.vtt",
		"Film.2.de.vtt", // gehört zu Film.2.mkv
	)
	found := findSidecarSubs(filepath.Join(dir, "Film.mkv"))
	if len(found) != 1 || filepath.Base(found[0].Path) != "Film.de.vtt" {
		names := []string{}
		for _, f := range found {
			names = append(names, filepath.Base(f.Path))
		}
		t.Fatalf("erwartet nur Film.de.vtt, gefunden: %v", names)
	}
	found2 := findSidecarSubs(filepath.Join(dir, "Film.2.mkv"))
	if len(found2) != 1 || filepath.Base(found2[0].Path) != "Film.2.de.vtt" {
		t.Fatalf("Film.2.mkv: unerwartet %+v", found2)
	}
}

// Release-Namen enthalten reichlich Punkte — die dürfen die Token-Auswertung
// nicht durcheinanderbringen, weil nur der Teil NACH dem Stamm zählt.
func TestFindSidecarSubsReleaseStyleStem(t *testing.T) {
	dir := t.TempDir()
	mkFiles(t, dir,
		"Kill.Bill.2003.German.DL.1080p.BluRay.x264.mkv",
		"Kill.Bill.2003.German.DL.1080p.BluRay.x264.srt",
		"Kill.Bill.2003.German.DL.1080p.BluRay.x264.en.srt",
	)
	found := findSidecarSubs(filepath.Join(dir, "Kill.Bill.2003.German.DL.1080p.BluRay.x264.mkv"))
	if len(found) != 2 {
		t.Fatalf("erwartet 2, gefunden %d: %+v", len(found), found)
	}
	langs := map[string]bool{found[0].Language: true, found[1].Language: true}
	if !langs[""] || !langs["eng"] {
		t.Errorf("Sprachen falsch: %+v", langs)
	}
}

func TestFindSidecarSubsNoneAndMissingDir(t *testing.T) {
	dir := t.TempDir()
	mkFiles(t, dir, "Film.mkv")
	if got := findSidecarSubs(filepath.Join(dir, "Film.mkv")); len(got) != 0 {
		t.Errorf("erwartet keine Treffer, gefunden %+v", got)
	}
	if got := findSidecarSubs(filepath.Join(dir, "gibtsnicht", "Film.mkv")); got != nil {
		t.Errorf("fehlender Ordner sollte nil liefern, lieferte %+v", got)
	}
}

func TestSidecarSubLabel(t *testing.T) {
	cases := []struct {
		in   sidecarSub
		want string
	}{
		{sidecarSub{Language: "deu"}, "📄 Deutsch (Datei)"},
		{sidecarSub{Language: "eng", Forced: true}, "📄 Englisch (Datei · Forced)"},
		{sidecarSub{Language: "deu", Notes: []string{"sdh"}}, "📄 Deutsch (Datei · sdh)"},
		{sidecarSub{}, "📄 Untertitel (Datei)"},
		{sidecarSub{Language: "xyz"}, "📄 XYZ (Datei)"},
	}
	for _, c := range cases {
		if got := sidecarSubLabel(c.in); got != c.want {
			t.Errorf("sidecarSubLabel(%+v) = %q, erwartet %q", c.in, got, c.want)
		}
	}
}

// Die synthetischen Indizes dürfen sich nicht mit Whisper (2000+) oder
// OCR (2100+) überschneiden.
func TestSidecarIndexBaseAboveGeneratedRanges(t *testing.T) {
	if sidecarIndexBase <= 2100+6 {
		t.Fatalf("sidecarIndexBase %d kollidiert mit dem OCR-Bereich", sidecarIndexBase)
	}
}
