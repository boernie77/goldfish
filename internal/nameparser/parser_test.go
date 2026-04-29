package nameparser

import "testing"

// Tests fuer ParseFile (Movie-Kontext): SxxExx + NxN, KEINE numerischen Episoden.
// Filme wie "Matrix 1999 1080.mkv" duerfen nicht als S10E80 fehlinterpretiert werden.
func TestParseFile_Movies(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantTitle string
		wantYear  int
	}{
		{"klassisch mit Jahr", "Inception.2010.1080p.BluRay.x264-GROUP.mkv", "Inception", 2010},
		{"Klammer-Jahr", "The Matrix (1999) 1080p.mkv", "The Matrix", 1999},
		{"Punkt-getrennt + Release-Tags", "Mad.Max.Fury.Road.2015.GERMAN.DL.1080p.BluRay-ABC.mkv", "Mad Max Fury Road", 2015},
		{"Jahr als Titel + Release-Jahr", "1917.2019.German.1080p.BluRay.x264-AAA.mkv", "1917", 2019},
		{"einzelnes Jahr im Titel", "Der Bulle 2009.mkv", "Der Bulle", 2009},
		{"Hash-Praefix vor Bindestrich", "abc123def-MATRIX.2021.1080p.mkv", "MATRIX", 2021},
		{"keine Hash-Praefix-Stripping bei kurzen Token", "MAN-MOT.2019.mkv", "MAN-MOT", 2019},
		{"Klammern werden ersetzt", "[GROUP] Movie (2020) 720p.mkv", "GROUP Movie", 2020},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseFile(tt.path)
			if got.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", got.Title, tt.wantTitle)
			}
			if got.Year != tt.wantYear {
				t.Errorf("Year = %d, want %d", got.Year, tt.wantYear)
			}
			if got.IsEpisode {
				t.Errorf("IsEpisode = true, want false (Movie-Kontext)")
			}
		})
	}
}

// Filme mit Zahlen, die wie 3-/4-stellige Episoden-Codes aussehen, duerfen
// im Movie-Kontext NICHT als Episoden interpretiert werden.
func TestParseFile_NoNumericEpisodes(t *testing.T) {
	tests := []string{
		"Matrix 1999 1080.mkv",     // 1999 ist Jahr, 1080 ist Aufloesung
		"Movie 104 Action.mkv",     // 104 koennte S1E04 sein, im Movie-Kontext aber nicht
		"Film 720p German.mkv",     // 720 wuerde S7E20 nahelegen — verboten
		"Some Movie 2024 1080.mkv", // 2024 ist Jahr
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			got := ParseFile(path)
			if got.IsEpisode {
				t.Errorf("ParseFile(%q): IsEpisode = true, want false", path)
			}
		})
	}
}

// ParseFile erkennt SxxExx korrekt (Episoden-Format trotz Movie-Funktion).
// Wird im Code beim TV-Kontext fuer eindeutige Pfade auch genutzt.
func TestParseFile_SxxExx(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		wantSeason  int
		wantEpisode int
		wantEnd     int
	}{
		{"Standard", "Show.S01E02.mkv", 1, 2, 0},
		{"klein", "show s2e3.mkv", 2, 3, 0},
		{"ohne Trenner", "The MiddleS1E01.avi", 1, 1, 0},
		{"Doppelfolge mit E", "Show.S07E23E24.mkv", 7, 23, 24},
		{"Doppelfolge mit Bindestrich", "Show.S07E23-E24.mkv", 7, 23, 24},
		{"Doppelfolge mit Leerzeichen", "Show S07E23 E24 Finale.mkv", 7, 23, 24},
		{"Triple — nur erste Range gecaptured", "Show.S02E10E11E12.mkv", 2, 10, 11},
		{"Range > 10 Sanity-Cap", "Show.S07E23E50.mkv", 7, 23, 0},
		{"NxN-Format", "Show 1x05.mkv", 1, 5, 0},
		{"NxN Range", "Show 2x01-2x02.mkv", 2, 1, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseFile(tt.path)
			if !got.IsEpisode {
				t.Fatalf("IsEpisode = false, want true")
			}
			if got.Season != tt.wantSeason {
				t.Errorf("Season = %d, want %d", got.Season, tt.wantSeason)
			}
			if got.Episode != tt.wantEpisode {
				t.Errorf("Episode = %d, want %d", got.Episode, tt.wantEpisode)
			}
			if got.EpisodeEnd != tt.wantEnd {
				t.Errorf("EpisodeEnd = %d, want %d", got.EpisodeEnd, tt.wantEnd)
			}
		})
	}
}

// ParseEpisodeFile (TV-Kontext) erweitert um numerische Episoden-Codes.
func TestParseEpisodeFile_NumericCodes(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		wantSeason  int
		wantEpisode int
	}{
		{"3-stellig: Derrick 104", "Derrick 104.avi", 1, 4},
		{"4-stellig: Show 1004", "Some Show 1004.mkv", 10, 4},
		{"3-stellig mit Episoden-Titel", "The Wire 304 Dead Soldiers.mkv", 3, 4},
		{"Edge: 999 → S9E99", "Show 999.mkv", 9, 99},
		{"Edge: 100 → S1E00 invalid → ignoriert", "Show 100.mkv", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseEpisodeFile(tt.path)
			if got.Season != tt.wantSeason || got.Episode != tt.wantEpisode {
				t.Errorf("S/E = %d/%d, want %d/%d", got.Season, got.Episode, tt.wantSeason, tt.wantEpisode)
			}
		})
	}
}

// Auch im TV-Kontext duerfen Jahre nicht als Episoden gelesen werden.
func TestParseEpisodeFile_YearsExcluded(t *testing.T) {
	tests := []string{
		"Show 1999.mkv",       // Jahr → keine S19E99
		"Show 2024.mkv",       // Jahr → keine S20E24
		"Show 1900.mkv",       // unteres Jahr-Limit
		"Show 2099.mkv",       // oberes Jahr-Limit
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			got := ParseEpisodeFile(path)
			if got.IsEpisode {
				t.Errorf("ParseEpisodeFile(%q): IsEpisode = true, want false (Jahr)", path)
			}
		})
	}
}

// E-only-Format wird im TV-Kontext als Season 1 interpretiert.
func TestParseEpisodeFile_EOnly(t *testing.T) {
	got := ParseEpisodeFile("E07 Bluthund.avi")
	if !got.IsEpisode || got.Season != 1 || got.Episode != 7 {
		t.Errorf("E07: got S=%d E=%d IsEpisode=%v, want S=1 E=7 IsEpisode=true",
			got.Season, got.Episode, got.IsEpisode)
	}
}

// SxxExx hat hoehere Prioritaet als numerische Codes — im TV-Kontext wird
// das spezifischere Pattern bevorzugt, auch wenn ein 3-stelliger Code da ist.
func TestParseEpisodeFile_SxxExxBeatsNumeric(t *testing.T) {
	got := ParseEpisodeFile("Show.S05E12.404.mkv") // 404 koennte verleiten, aber S05E12 gewinnt
	if got.Season != 5 || got.Episode != 12 {
		t.Errorf("got S=%d E=%d, want S=5 E=12", got.Season, got.Episode)
	}
}

// ParseFolder: Show-Name + Jahr aus Ordnernamen.
func TestParseFolder(t *testing.T) {
	tests := []struct {
		name      string
		folder    string
		wantTitle string
		wantYear  int
	}{
		{"Klammer-Jahr", "Banshee (2013)", "Banshee", 2013},
		{"Season-Range Cut", "House.of.Cards.S01.-.S06.Complete.German.DL.BluRay", "House of Cards", 0},
		{"einzelner Season-Marker mit Release-Tail", "Show S01 Complete BluRay", "Show", 0},
		{"kein Trim ohne Release-Wort", "Sisi", "Sisi", 0},
		{"Jahr ohne Klammer", "Better Call Saul 2015", "Better Call Saul", 2015},
		{"Punkt-Trennung", "The.X.Files", "The X Files", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseFolder(tt.folder)
			if got.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", got.Title, tt.wantTitle)
			}
			if got.Year != tt.wantYear {
				t.Errorf("Year = %d, want %d", got.Year, tt.wantYear)
			}
			if got.IsEpisode {
				t.Errorf("IsEpisode = true, want false (Folder)")
			}
		})
	}
}

// Deleetify ersetzt Leet-Ziffern in alphanumerischen Tokens, laesst aber reine
// Zahlen (Jahre, Staffelnummern) unangetastet.
func TestDeleetify(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"G3rm4n", "German"},
		{"Undispu73d", "Undisputed"},
		{"L4ngs4m", "Langsam"},
		{"2019", "2019"},                    // reine Zahl bleibt
		{"S1trb L4ngs4m 1988", "Sitrb Langsam 1988"}, // S1trb hat zu wenig Buchstaben (nur 4) → bleibt? Test: 4 letters/1 digit → letters > digits → wird umgewandelt
		{"abc", "abc"},                      // zu kurz fuer Deleet (< 4)
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := Deleetify(tt.in)
			if got != tt.want {
				t.Errorf("Deleetify(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ExpandCandidates erzeugt mehrere Varianten fuer TMDB-Queries.
func TestExpandCandidates(t *testing.T) {
	p := Parsed{Title: "Sitrb Langsam", Year: 1988}
	got := ExpandCandidates(p)
	if len(got) < 2 {
		t.Fatalf("got %d Kandidaten, want >= 2 (Original + Longest-Word)", len(got))
	}
	// Sollte das Original enthalten.
	hasOriginal := false
	hasLangsam := false
	for _, c := range got {
		if c.Title == "Sitrb Langsam" {
			hasOriginal = true
		}
		if c.Title == "Langsam" {
			hasLangsam = true
		}
	}
	if !hasOriginal {
		t.Errorf("Originaltitel fehlt in Kandidaten")
	}
	if !hasLangsam {
		t.Errorf("Longest-Word-Fallback 'Langsam' fehlt in Kandidaten")
	}
}

// Dedup-Garantie: gleicher Titel + gleiches Jahr nur einmal.
func TestExpandCandidates_Dedup(t *testing.T) {
	p := Parsed{Title: "Mama", Year: 2020}
	got := ExpandCandidates(p)
	keys := map[string]int{}
	for _, c := range got {
		keys[c.Title]++
	}
	for k, n := range keys {
		if n > 1 {
			t.Errorf("Kandidat %q taucht %d-mal auf, want max 1", k, n)
		}
	}
}

// Der Bug aus dem Decision-Log: tvs-911-... darf nicht als S9E11 enden, wenn
// keine SxxExx- oder NxN-Markierung vorliegt — bei ParseFileStrict (no numeric).
func TestParseFileStrict_NoNumericMisinterpretation(t *testing.T) {
	got := ParseFileStrict("tvs-911-dd51-dl-x264-108.mkv")
	if got.IsEpisode {
		t.Errorf("ParseFileStrict darf 911 nicht als S9E11 lesen, got Season=%d Episode=%d",
			got.Season, got.Episode)
	}
}

// longestWord interner Helper: Trim + Mindestlaenge.
func TestLongestWord(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"a b cd efgh ij", "efgh"},
		{"foo bar.", ""},     // beide < 4 Zeichen (Trim entfernt den Punkt) → leer
		{"abcd", "abcd"},     // genau 4 Zeichen ist OK
		{"abc", ""},          // < 4 Zeichen → leer
		{"", ""},
		{"Sitrb Langsam 1988", "Langsam"}, // laengstes Token mit >= 4
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := longestWord(tt.in)
			if got != tt.want {
				t.Errorf("longestWord(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
