package musicbrainz

import "testing"

func TestTitleCase(t *testing.T) {
	cases := map[string]string{
		"hard rock":     "Hard Rock",
		"pop":           "Pop",
		"":              "",
		"drum and bass": "Drum And Bass",
	}
	for in, want := range cases {
		if got := titleCase(in); got != want {
			t.Errorf("titleCase(%q) = %q, want %q", in, got, want)
		}
	}
}
