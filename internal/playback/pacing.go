package playback

import (
	"strings"
	"time"
)

// ─── Playlist-Pacing: Vorrat gegen ffmpeg-Stocken (seit 2026-09-25) ─────────
//
// Auslöser: tvOS brach einen Film nach 9 Minuten mit -12888 „Playlist File
// unchanged for longer than 1.5 * target duration" ab. ffmpeg lag ~80 Minuten
// VOR der Wiedergabe, stockte aber einmal 8 s lang (Segment-mtimes seg02778 →
// seg02779). AVPlayer verlangt bei einer EVENT-Playlist ohne ENDLIST, dass sie
// sich spätestens nach 1,5 × TARGETDURATION (= 3 s bei `-hls_time 2`) ändert —
// unabhängig davon, wie viel Vorlauf da ist. ExoPlayer (Android/Fire TV) wirft
// nach 3,5 × TD (7 s) eine PlaylistStuckException.
//
// Lösung rein serverseitig, damit keine App neu gebaut werden muss: die
// Playlist zeigt nicht jedes fertige Segment sofort, sondern hält einen Vorrat
// (`paceReserveSegments`) zurück, sobald ffmpeg weit genug vorn liegt. Jede
// Playlist-Anfrage, die mindestens `paceStep` nach der letzten Änderung kommt,
// gibt mindestens ein weiteres Segment frei — stockt ffmpeg, wächst die
// Playlist also aus dem Vorrat weiter und kein Player merkt etwas.
//
// Bewusst NIE schlechter als vorher:
//   - Die erste Anfrage einer Sitzung zeigt alles, was da ist.
//   - Ist ffmpeg nicht schneller als die Freigabe (z. B. 4K nahe Echtzeit),
//     entsteht kein Vorrat — dann ist alles sichtbar wie bisher.
//   - Ist ffmpeg fertig (Done oder ENDLIST), wird sofort alles inkl. ENDLIST
//     gezeigt — dann ist es eine abgeschlossene Playlist ohne Stall-Prüfung.
// Einziger Preis: der lokal spulbare Bereich endet bis zu einem Vorrat vor
// dem ffmpeg-Stand; ein Sprung dorthin startet wie jeder Sprung über das
// Sichtbare hinaus eine neue Sitzung.

const (
	// paceReserveSegments: so viele fertige Segmente bleiben höchstens
	// zurückgehalten (30 × 2 s = 60 s Video). Deckt ein ffmpeg-Stocken von
	// bis zu ~30 s ab (Freigabe 1 Segment je paceStep).
	paceReserveSegments = 30
	// paceStep: je vergangener paceStep seit der letzten Änderung wird ein
	// Segment freigegeben. AVPlayer lädt eine unveränderte Playlist nach
	// TD/2 = 1 s erneut, eine veränderte nach TD = 2 s — beides trifft damit
	// immer auf eine Änderung. Bei 2-s-Segmenten entspricht das doppelter
	// Echtzeit, der Player-Puffer wächst also weiter.
	paceStep = time.Second
)

// paceExposure berechnet, wie viele Segmente die Playlist zeigen soll.
// prev: bisher gezeigte Segmente (0 = erste Anfrage), total: von ffmpeg
// fertig geschriebene, elapsed: Zeit seit der letzten Erhöhung.
func paceExposure(prev, total int, elapsed time.Duration, done bool) int {
	if done || prev <= 0 || total <= prev {
		return total
	}
	n := prev + int(elapsed/paceStep)
	if floor := total - paceReserveSegments; n < floor {
		n = floor
	}
	if n > total {
		n = total
	}
	return n
}

// PacedSegmentCount liefert, wie viele der `total` fertigen Segmente die
// Playlist dieser Sitzung jetzt zeigen soll, und merkt sich den Stand.
// Nie weniger als bei der vorherigen Anfrage (eine Playlist darf nicht
// schrumpfen).
func (s *Session) PacedSegmentCount(total int, done bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	n := paceExposure(s.paceExposed, total, now.Sub(s.paceChanged), done)
	if n < s.paceExposed {
		n = s.paceExposed
	}
	if n != s.paceExposed {
		s.paceExposed = n
		s.paceChanged = now
	}
	return n
}

// CountPlaylistSegments zählt die Segment-URIs einer m3u8.
func CountPlaylistSegments(raw string) int {
	n := 0
	for _, line := range strings.Split(raw, "\n") {
		tl := strings.TrimSpace(line)
		if tl != "" && !strings.HasPrefix(tl, "#") {
			n++
		}
	}
	return n
}

// TruncatePlaylist kürzt eine m3u8 auf die ersten n Segmente. Tags NACH dem
// n-ten Segment (EXTINF des nächsten, ENDLIST) fallen weg — eine gekürzte
// Playlist darf kein ENDLIST tragen, sonst hielte der Player den Film dort
// für zu Ende. Bei n >= Segmentzahl bleibt die Playlist unverändert.
func TruncatePlaylist(raw string, n int) string {
	if n >= CountPlaylistSegments(raw) {
		return raw
	}
	var out strings.Builder
	seen := 0
	for _, line := range strings.Split(raw, "\n") {
		if seen >= n {
			break
		}
		out.WriteString(line)
		out.WriteByte('\n')
		tl := strings.TrimSpace(line)
		if tl != "" && !strings.HasPrefix(tl, "#") {
			seen++
		}
	}
	return out.String()
}
