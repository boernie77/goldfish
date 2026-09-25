package playback

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// buildPlaylist erzeugt eine ffmpeg-artige EVENT-Playlist mit n Segmenten.
func buildPlaylist(n int, endlist bool) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "#EXTINF:2.002000,\nseg%05d.ts\n", i)
	}
	if endlist {
		b.WriteString("#EXT-X-ENDLIST\n")
	}
	return b.String()
}

// Die erste Anfrage zeigt alles — nie schlechter als vor dem Pacing.
func TestPaceFirstRequestShowsEverything(t *testing.T) {
	if got := paceExposure(0, 40, 0, false); got != 40 {
		t.Fatalf("erste Anfrage muss alles zeigen, zeigt %d von 40", got)
	}
}

// Ist ffmpeg fertig, wird sofort alles gezeigt (abgeschlossene Playlist).
func TestPaceDoneShowsEverything(t *testing.T) {
	if got := paceExposure(10, 500, 0, true); got != 500 {
		t.Fatalf("fertige Sitzung muss alles zeigen, zeigt %d von 500", got)
	}
}

// Höchstens paceReserveSegments bleiben zurück, egal wie weit ffmpeg vorn ist.
func TestPaceReserveIsBounded(t *testing.T) {
	got := paceExposure(10, 1000, 0, false)
	if got != 1000-paceReserveSegments {
		t.Fatalf("Vorrat muss auf %d begrenzt sein, gezeigt %d von 1000", paceReserveSegments, got)
	}
}

// Ist ffmpeg langsamer als die Freigabe, entsteht kein Vorrat.
func TestPaceSlowFFmpegShowsEverything(t *testing.T) {
	// 3 s vergangen, ffmpeg hat seitdem 2 neue Segmente geschrieben.
	if got := paceExposure(10, 12, 3*time.Second, false); got != 12 {
		t.Fatalf("langsames ffmpeg: alles zeigen erwartet, zeigt %d von 12", got)
	}
}

// Kernfall 2026-09-25: ffmpeg weit vorn, stockt 8 s. Jede Nachlade-Anfrage
// im Sekundentakt muss trotzdem eine veränderte Playlist bekommen.
func TestPaceSurvivesFFmpegStall(t *testing.T) {
	prev, total := 0, 0
	elapsed := time.Duration(0)
	// 60 s Anlauf: ffmpeg schreibt 5 Segmente/s (10-fache Echtzeit).
	for sec := 0; sec < 60; sec++ {
		total += 5
		elapsed += time.Second
		n := paceExposure(prev, total, elapsed, false)
		if n != prev {
			prev, elapsed = n, 0
		}
	}
	if total-prev < 8 {
		t.Fatalf("Testaufbau: zu wenig Vorrat (%d)", total-prev)
	}
	// 8 s Stocken: total bleibt stehen, Player lädt jede Sekunde nach.
	for sec := 0; sec < 8; sec++ {
		elapsed += time.Second
		n := paceExposure(prev, total, elapsed, false)
		if n <= prev {
			t.Fatalf("Sekunde %d des Stockens: Playlist unverändert (%d) — AVPlayer bräche ab", sec+1, n)
		}
		prev, elapsed = n, 0
	}
}

// Die Playlist einer Sitzung darf nie schrumpfen.
func TestPacedSegmentCountNeverShrinks(t *testing.T) {
	s := &Session{}
	first := s.PacedSegmentCount(50, false)
	if first != 50 {
		t.Fatalf("erste Anfrage: 50 erwartet, %d", first)
	}
	if got := s.PacedSegmentCount(40, false); got < first {
		t.Fatalf("Playlist geschrumpft: %d < %d", got, first)
	}
}

func TestTruncatePlaylistDropsEndlistAndTail(t *testing.T) {
	raw := buildPlaylist(10, true)
	got := TruncatePlaylist(raw, 4)
	if CountPlaylistSegments(got) != 4 {
		t.Fatalf("4 Segmente erwartet, %d", CountPlaylistSegments(got))
	}
	if strings.Contains(got, "ENDLIST") {
		t.Fatal("gekürzte Playlist darf kein ENDLIST tragen")
	}
	if !strings.HasPrefix(got, "#EXTM3U\n") || !strings.Contains(got, "#EXT-X-PLAYLIST-TYPE:EVENT") {
		t.Fatal("Kopfzeilen fehlen")
	}
	if !strings.HasSuffix(got, "seg00003.ts\n") {
		t.Fatalf("muss mit dem 4. Segment enden, endet mit %q", got[len(got)-20:])
	}
}

func TestTruncatePlaylistNoopWhenAllShown(t *testing.T) {
	raw := buildPlaylist(5, true)
	if TruncatePlaylist(raw, 5) != raw || TruncatePlaylist(raw, 99) != raw {
		t.Fatal("bei n >= Segmentzahl muss die Playlist unverändert bleiben")
	}
}
