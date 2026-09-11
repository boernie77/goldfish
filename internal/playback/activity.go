package playback

import (
	"sync"
	"time"
)

// activityWindow: wie lange nach dem letzten beobachteten Wiedergabe-Request
// (Direct-Play-Range-Read ODER Transcode-Segment/Progress-Poll) noch als
// "gerade aktiv" gilt. Muss länger sein als der übliche Abstand zwischen
// zwei Requests derselben laufenden Wiedergabe (Transcode-Progress pollt
// alle 800ms während des Start-Puffer-Gates, danach holt der Browser HLS-
// Segmente im Sekundentakt; ein voll gepuffertes Direct-Play-Video kann
// dagegen für längere Zeit GAR KEINEN weiteren Range-Request auslösen) —
// bewusst grosszügig gewählt, damit eine kurze Pufferpause nicht sofort als
// "Wiedergabe beendet" missverstanden wird und Hintergrund-Worker mitten in
// einer laufenden Wiedergabe wieder anspringen.
const activityWindow = 25 * time.Second

var (
	activityMu   sync.Mutex
	lastActivity time.Time
)

// TouchActivity markiert "gerade wird etwas wiedergegeben" — aufgerufen aus
// Session.Touch() (Transcode: Segment-/Progress-Requests) sowie direkt aus
// dem Direct-Play-Handler (internal/api/stream.go streamDirect), der keine
// Session hat. User-Wunsch 2026-09-11: "wenn etwas abgespielt wird, dann
// muss ein Scan und Trickbild auch pausieren oder reduziert werden ... das
// hat immer Vorrang" — Scanner/Trickplay/Introskip/OCR fragen Active() über
// ihren jeweiligen SetPauseCheck-Callback ab (siehe cmd/goldfish/main.go).
func TouchActivity() {
	activityMu.Lock()
	lastActivity = time.Now()
	activityMu.Unlock()
}

// Active meldet, ob kürzlich Wiedergabe-Aktivität beobachtet wurde.
func Active() bool {
	activityMu.Lock()
	defer activityMu.Unlock()
	return !lastActivity.IsZero() && time.Since(lastActivity) < activityWindow
}
