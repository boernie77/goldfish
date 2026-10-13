package enrich

import "testing"

func TestFolderEpisodeEnd(t *testing.T) {
	const pack = "Luther.S01.-.S05.COMPLETE.German.DL.1080p.BluRay.x264-Scene/"
	cases := []struct {
		name, rel string
		season    int
		episode   int
		want      int
	}{
		{"Staffel 3 Doppelfolge", pack + "Luther.S03E01E02.German.DL.1080p.BluRay.x264-RIPLEY/r-luther.s03e01-1080p.mkv", 3, 1, 2},
		{"Staffel 5 Doppelfolge", pack + "Luther.S05E03E04.German.DL.1080p.BluRay.x264-AWARDS/luther.s05e03.german.dl.1080p.bluray.x264-awards.mkv", 5, 3, 4},
		{"Einzelfolge im Ordner", "Show/Show.S01E05.German/show.s01e05.mkv", 1, 5, 0},
		{"anderer Startwert", "Show/Show.S01E03E04/show.s01e04.mkv", 1, 4, 0},
		{"andere Staffel", "Show/Show.S02E01E02/show.s01e01.mkv", 1, 1, 0},
		{"kein Ordner", "show.s01e01.mkv", 1, 1, 0},
	}
	for _, c := range cases {
		if got := folderEpisodeEnd(c.rel, c.season, c.episode); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
