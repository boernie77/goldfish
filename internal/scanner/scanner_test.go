package scanner

import "testing"

// TestLookupTagRespectsKeyPriority sichert die 2026-09-05-Regression ab: bei
// mehreren `keys` muss deren Reihenfolge (nicht die randomisierte Go-Map-
// Iterationsreihenfolge von `tags`) die Priorität bestimmen. Betraf konkret
// items.Artist = lookupTag(tags, "album_artist", "artist") für Musik-Alben.
func TestLookupTagRespectsKeyPriority(t *testing.T) {
	tags := map[string]string{
		"artist":       "Sarah Brightman",
		"album_artist": "Das Phantom der Oper (Original Cast)",
	}

	// album_artist zuerst gewünscht -> muss IMMER album_artist liefern,
	// unabhängig davon, in welcher Reihenfolge Go über `tags` iteriert.
	for i := 0; i < 50; i++ {
		if got := lookupTag(tags, "album_artist", "artist"); got != "Das Phantom der Oper (Original Cast)" {
			t.Fatalf("run %d: expected album_artist to win, got %q", i, got)
		}
	}

	// artist zuerst gewünscht -> muss IMMER artist liefern.
	for i := 0; i < 50; i++ {
		if got := lookupTag(tags, "artist", "album_artist"); got != "Sarah Brightman" {
			t.Fatalf("run %d: expected artist to win, got %q", i, got)
		}
	}
}

func TestLookupTagFallsBackWhenFirstKeyMissing(t *testing.T) {
	tags := map[string]string{"ARTIST": "Only Artist Tag"}
	if got := lookupTag(tags, "album_artist", "artist"); got != "Only Artist Tag" {
		t.Fatalf("expected fallback to artist tag, got %q", got)
	}
}

func TestLookupTagEmptyValueSkipped(t *testing.T) {
	tags := map[string]string{"album_artist": "", "artist": "Fallback Artist"}
	if got := lookupTag(tags, "album_artist", "artist"); got != "Fallback Artist" {
		t.Fatalf("expected empty album_artist to be skipped, got %q", got)
	}
}

func TestLookupTagNoMatch(t *testing.T) {
	tags := map[string]string{"genre": "Soundtrack"}
	if got := lookupTag(tags, "album_artist", "artist"); got != "" {
		t.Fatalf("expected empty string when no key matches, got %q", got)
	}
}
