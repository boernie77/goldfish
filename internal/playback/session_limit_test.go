package playback

import (
	"errors"
	"testing"
	"time"
)

// Die Tests hier prüfen die Zähl-/Limit-Logik OHNE echtes ffmpeg zu starten:
// Sessions werden direkt in die Map gelegt (wie startLocked es täte), und
// geprüft wird nur, ob StartOrGet den Grenzfall korrekt erkennt.
//
// Hintergrund: bis 2026-09-17 gab es überhaupt kein Limit — acht parallele
// 4K-Transcodes rissen am 2026-09-16 den ganzen Unraid-Host mit.

// fakeSession legt eine Session in die Map, ohne einen Prozess zu starten.
// Ohne weitere Angabe gilt der teuerste Fall (4K-Quelle, Ziel = Original).
func fakeSession(m *Manager, id string, itemID int64, audioOnly bool) {
	fakeSessionSized(m, id, itemID, audioOnly, 2160, 0)
}

// fakeSessionSized wie fakeSession, aber mit Quell-/Zielhöhe für die
// Kostenrechnung (siehe transcodeCost).
func fakeSessionSized(m *Manager, id string, itemID int64, audioOnly bool, srcHeight, maxHeight int) {
	m.sessions[id] = &Session{
		ID:        id,
		ItemID:    itemID,
		StartedAt: time.Now(),
		done:      make(chan struct{}),
		spec: sessionSpec{
			itemID:    itemID,
			audioOnly: audioOnly,
			srcHeight: srcHeight,
			profile:   Profile{MaxHeight: maxHeight},
		},
	}
}

func newTestManager(t *testing.T, max int) *Manager {
	t.Helper()
	return &Manager{
		sessions:    map[string]*Session{},
		cacheDir:    t.TempDir(),
		freshTokens: map[string]string{},
		stoppedAt:   map[int64]time.Time{},
		maxSessions: max,
	}
}

// Bei vollem Budget muss eine NEUE Video-Session abgelehnt werden — mit
// ErrTooManySessions, damit der API-Layer daraus ein 503 machen kann.
func TestStartOrGetRejectsAtLimit(t *testing.T) {
	m := newTestManager(t, 4)
	// Vier 4K→4K-Sessions = volles Budget (4 × 100 Punkte).
	for i := int64(1); i <= 4; i++ {
		fakeSessionSized(m, "sess-"+string(rune('a'+i)), i, false, 2160, 0)
	}
	_, err := m.StartOrGet(99, "/media/x.mkv", ProfileByID("orig"), -1, 0, false, false, 2160)
	if err == nil {
		t.Fatal("erwartet: Ablehnung am Limit, bekommen: nil")
	}
	if !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("erwartet ErrTooManySessions, bekommen: %v", err)
	}
	if len(m.sessions) != 4 {
		t.Fatalf("abgelehnte Anfrage darf keine Session anlegen, sind jetzt %d", len(m.sessions))
	}
}

// Eine BESTEHENDE Session weiterzubenutzen darf nie am Limit scheitern —
// sonst bricht ein laufender Film beim nächsten Playlist-Reload ab.
func TestStartOrGetExistingSessionNotLimited(t *testing.T) {
	m := newTestManager(t, 2)
	// Exakt der Key, den StartOrGet für diese Parameter bildet.
	id := sessionKey(7, ProfileByID("orig").ID, -1, 0, false)
	fakeSessionSized(m, id, 7, false, 2160, 0)
	fakeSessionSized(m, "other", 8, false, 2160, 0) // Budget damit voll

	s, err := m.StartOrGet(7, "/media/x.mkv", ProfileByID("orig"), -1, 0, false, false, 2160)
	if err != nil {
		t.Fatalf("bestehende Session muss zurückkommen, bekommen: %v", err)
	}
	if s.ID != id {
		t.Fatalf("falsche Session: %s", s.ID)
	}
}

// Reine Audio-Sessions (Musik) dürfen keinen Videoplatz belegen.
func TestAudioOnlySessionsDoNotCountTowardLimit(t *testing.T) {
	m := newTestManager(t, 2)
	fakeSession(m, "music-1", 101, true)
	fakeSession(m, "music-2", 102, true)
	fakeSession(m, "music-3", 103, true)
	if got := m.activeVideoSessionsLocked(); got != 0 {
		t.Fatalf("Audio-Sessions dürfen nicht zählen, gezählt: %d", got)
	}
	if got := m.activeCostLocked(); got != 0 {
		t.Fatalf("Audio-Sessions dürfen keine Kosten verursachen, Punkte: %d", got)
	}
	fakeSession(m, "video-1", 201, false)
	if got := m.activeVideoSessionsLocked(); got != 1 {
		t.Fatalf("erwartet 1 Video-Session, gezählt: %d", got)
	}
}

// maxSessions == 0 heißt „unbegrenzt" (nur für Tests/Sonderfälle).
func TestZeroMaxMeansUnlimited(t *testing.T) {
	m := newTestManager(t, 0)
	for i := int64(1); i <= 20; i++ {
		fakeSession(m, "s"+string(rune('a'+i)), i, false)
	}
	// Kein Limit-Fehler; der Aufruf scheitert höchstens am echten ffmpeg,
	// deshalb nur auf die Fehlerart prüfen.
	_, err := m.StartOrGet(999, "/media/none.mkv", ProfileByID("orig"), -1, 0, false, false, 2160)
	if errors.Is(err, ErrTooManySessions) {
		t.Fatal("bei maxSessions=0 darf nie am Limit abgelehnt werden")
	}
}

// SetMaxSessions wirkt zur Laufzeit und killt keine laufenden Sessions.
func TestSetMaxSessionsRuntime(t *testing.T) {
	m := newTestManager(t, 8)
	for i := int64(1); i <= 5; i++ {
		fakeSession(m, "s"+string(rune('a'+i)), i, false)
	}
	m.SetMaxSessions(2)
	if m.MaxSessions() != 2 {
		t.Fatalf("Limit nicht übernommen: %d", m.MaxSessions())
	}
	if len(m.sessions) != 5 {
		t.Fatalf("laufende Sessions dürfen beim Senken nicht gekillt werden, sind: %d", len(m.sessions))
	}
	// Neue Anfrage muss jetzt abgelehnt werden (5 × 100 Punkte > Budget 200).
	if _, err := m.StartOrGet(77, "/media/x.mkv", ProfileByID("orig"), -1, 0, false, false, 2160); !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("erwartet Ablehnung nach Senken, bekommen: %v", err)
	}
}

// ─── Gewichtung (seit 2026-09-17) ──────────────────────────────────────────

// Die Kostenordnung muss der Messung auf echter Hardware entsprechen
// (siehe Kommentarblock bei transcodeCost): 4K→4K am teuersten, kleinere
// Ziele billiger, und dasselbe Ziel aus kleinerer Quelle nochmals billiger.
func TestTranscodeCostOrdering(t *testing.T) {
	c4k4k := transcodeCost(2160, 0) // 4K → Original
	c4k1080 := transcodeCost(2160, 1080)
	c4k720 := transcodeCost(2160, 720)
	cHD1080 := transcodeCost(1080, 1080)
	cHD720 := transcodeCost(1080, 720)
	cHD480 := transcodeCost(1080, 480)

	if c4k4k != CostFullBudgetUnit {
		t.Fatalf("4K→4K muss das volle Budgetmaß kosten, ist: %d", c4k4k)
	}
	if !(c4k4k > c4k1080 && c4k1080 >= c4k720) {
		t.Fatalf("4K-Quelle: Kosten müssen mit kleinerem Ziel fallen (%d/%d/%d)", c4k4k, c4k1080, c4k720)
	}
	if !(cHD1080 > cHD720 && cHD720 > cHD480) {
		t.Fatalf("HD-Quelle: Kosten müssen mit kleinerem Ziel fallen (%d/%d/%d)", cHD1080, cHD720, cHD480)
	}
	// Kernaussage der Messung: dasselbe Ziel ist aus kleinerer Quelle billiger,
	// weil das Dekodieren unabhängig vom Ziel anfällt.
	if !(c4k720 > cHD720) {
		t.Fatalf("720p aus 4K-Quelle (%d) muss teurer sein als aus HD-Quelle (%d)", c4k720, cHD720)
	}
}

// Unbekannte Quellhöhe muss als teuerster Fall behandelt werden — lieber zu
// früh ablehnen als den Server überbuchen.
func TestUnknownSourceHeightCostsFullUnit(t *testing.T) {
	if got := transcodeCost(0, 0); got != CostFullBudgetUnit {
		t.Fatalf("unbekannte Quelle muss voll zählen, ist: %d", got)
	}
	// Auch mit kleinem Ziel bleibt die Decode-Last einer 4K-Quelle.
	if got := transcodeCost(0, 480); got < 40 {
		t.Fatalf("unbekannte Quelle mit kleinem Ziel zu billig: %d", got)
	}
}

// Ein Profil, das GRÖSSER ist als die Quelle, darf nicht mehr kosten als die
// Quelle selbst — hochskaliert wird nicht.
func TestTargetLargerThanSourceCappedAtSource(t *testing.T) {
	// 720p-Quelle mit 1080p-Profil: darf nicht wie eine echte 1080p-Last zählen.
	small := transcodeCost(720, 1080)
	if small > transcodeCost(1080, 1080) {
		t.Fatalf("Ziel über Quellauflösung darf nicht teurer sein (%d)", small)
	}
}

// Das Budget muss deutlich mehr kleine als große Umwandlungen zulassen —
// genau der Zweck der Gewichtung.
func TestBudgetAllowsMoreSmallSessions(t *testing.T) {
	m := newTestManager(t, 4) // Budget 400 Punkte
	// Sechs 480p-Sessions aus HD-Quelle (je 20 Punkte = 120) müssen passen,
	// obwohl das weit über der alten starren Grenze von 4 Sitzungen liegt.
	for i := int64(1); i <= 6; i++ {
		fakeSessionSized(m, "small"+string(rune('a'+i)), i, false, 1080, 480)
	}
	if _, err := m.StartOrGet(50, "/media/x.mkv", ProfileByID("480p"), -1, 0, false, false, 1080); errors.Is(err, ErrTooManySessions) {
		t.Fatalf("kleine Umwandlungen müssen über die alte 4er-Grenze hinaus erlaubt sein (Punkte: %d)", m.activeCostLocked())
	}

	// Gegenprobe: eine 4K-Umwandlung (100 Punkte) muss bei 350/400 scheitern.
	m2 := newTestManager(t, 4)
	for i := int64(1); i <= 7; i++ {
		fakeSessionSized(m2, "hd"+string(rune('a'+i)), i, false, 2160, 1080) // je 50 = 350
	}
	if _, err := m2.StartOrGet(60, "/media/big.mkv", ProfileByID("orig"), -1, 0, false, false, 2160); !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("4K bei 350/400 Punkten muss abgelehnt werden, bekommen: %v", err)
	}
}

// Der Anzahl-Deckel greift auch dann, wenn das Punktebudget noch Luft hätte —
// viele Prozesse kosten Speicher und Dateihandles, nicht nur GPU-Zeit.
func TestHardSessionCapIndependentOfBudget(t *testing.T) {
	m := newTestManager(t, 2) // Budget 200 Punkte, Anzahl-Deckel 2×3 = 6
	// Sechs sehr billige Sessions (720p-Quelle → 480p = 15 Punkte, gesamt 90).
	for i := int64(1); i <= 6; i++ {
		fakeSessionSized(m, "tiny"+string(rune('a'+i)), i, false, 720, 480)
	}
	if cost := m.activeCostLocked(); cost >= 200 {
		t.Fatalf("Testaufbau falsch: Budget schon erschöpft (%d)", cost)
	}
	_, err := m.StartOrGet(70, "/media/x.mkv", ProfileByID("480p"), -1, 0, false, false, 720)
	if !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("Anzahl-Deckel muss greifen, bekommen: %v", err)
	}
}

// Auslastungsanzeige: 0 % ohne Sessions, 100 % bei vollem Budget.
func TestActiveLoadPercent(t *testing.T) {
	m := newTestManager(t, 4)
	if got := m.ActiveLoadPercent(); got != 0 {
		t.Fatalf("ohne Sessions 0%% erwartet, ist: %d", got)
	}
	for i := int64(1); i <= 4; i++ {
		fakeSessionSized(m, "f"+string(rune('a'+i)), i, false, 2160, 0)
	}
	if got := m.ActiveLoadPercent(); got != 100 {
		t.Fatalf("bei vollem Budget 100%% erwartet, ist: %d", got)
	}
}
