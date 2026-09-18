package playback

import (
	"errors"
	"testing"
)

// TestStartOrGetAfterStopReturnsSentinel sichert den 503-Pfad ab, der einen
// echten, wiederkehrenden Nutzerfehler behebt (User-Report macOS 2026-09-18:
// „Stream-Fehler (-16847) … HTTP 500" erschien AM FOLGEN-ENDE, gleichzeitig
// mit dem „Nächste Folge"-Hinweis).
//
// Ursache der Kette: am Ende einer EVENT-Playlist holen AVPlayer/ExoPlayer von
// sich aus die Playlist erneut nach. Für dieses Item war aber gerade ein
// Client-Stop gemeldet worden (Wiedergabe-Ende), also greift das
// stopSuppressWindow in StartOrGet. Der Fehler wurde als gewöhnlicher Fehler
// behandelt → HTTP 500 → die Player zeigten einen modalen Abspielfehler.
//
// Der Test hält fest, dass dieser Fall als SENTINEL zurückkommt, damit der
// API-Layer ihn als temporären Zustand (503 + Retry-After) beantworten kann.
// Ein blanker fmt.Errorf-Text wäre nicht unterscheidbar und der Mapping-Zweig
// würde still verrotten.
func TestStartOrGetAfterStopReturnsSentinel(t *testing.T) {
	m := newTestManager(t, DefaultMaxSessions)
	const itemID int64 = 4711

	// Kein Fehler vor dem Stop: eine „normale" Anfrage muss durchlaufen (hier
	// fängt sie am fehlenden ffmpeg ab, aber NICHT am Sperrfenster).
	m.StopAllForItem(itemID) // setzt stoppedAt[itemID] = jetzt
	_, err := m.StartOrGet(itemID, "/tmp/nicht-vorhanden.mkv", ProfileByID("orig"), -1, 0, false, false, 1080)
	if !errors.Is(err, ErrStoppedRecently) {
		t.Fatalf("StartOrGet direkt nach StopAllForItem = %v, want ErrStoppedRecently", err)
	}

	// Gegenprobe: ein ANDERES Item darf in derselben Sekunde nicht blockiert
	// sein — das Sperrfenster gilt pro Item (sonst hätte ein Stop die nächste
	// Folge des Nutzers mit blockiert).
	_, err = m.StartOrGet(itemID+1, "/tmp/nicht-vorhanden.mkv", ProfileByID("orig"), -1, 0, false, false, 1080)
	if errors.Is(err, ErrStoppedRecently) {
		t.Fatalf("anderes Item wurde vom Sperrfenster blockiert: %v", err)
	}
}
