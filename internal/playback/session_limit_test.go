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
func fakeSession(m *Manager, id string, itemID int64, audioOnly bool) {
	m.sessions[id] = &Session{
		ID:        id,
		ItemID:    itemID,
		StartedAt: time.Now(),
		done:      make(chan struct{}),
		spec:      sessionSpec{itemID: itemID, audioOnly: audioOnly},
	}
}

func newTestManager(t *testing.T, max int) *Manager {
	t.Helper()
	m := &Manager{
		sessions:    map[string]*Session{},
		cacheDir:    t.TempDir(),
		freshTokens: map[string]string{},
		stoppedAt:   map[int64]time.Time{},
		maxSessions: max,
	}
	return m
}

// Am Limit muss eine NEUE Video-Session abgelehnt werden — mit
// ErrTooManySessions, damit der API-Layer daraus ein 503 machen kann.
func TestStartOrGetRejectsAtLimit(t *testing.T) {
	m := newTestManager(t, 4)
	for i := int64(1); i <= 4; i++ {
		fakeSession(m, "sess-"+string(rune('a'+i)), i, false)
	}
	_, err := m.StartOrGet(99, "/media/x.mkv", ProfileByID("orig"), -1, 0, false, false)
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
	fakeSession(m, id, 7, false)
	fakeSession(m, "other", 8, false) // Limit damit voll (2/2)

	s, err := m.StartOrGet(7, "/media/x.mkv", ProfileByID("orig"), -1, 0, false, false)
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
	_, err := m.StartOrGet(999, "/media/none.mkv", ProfileByID("orig"), -1, 0, false, false)
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
	// Neue Anfrage muss jetzt abgelehnt werden (5 >= 2).
	if _, err := m.StartOrGet(77, "/media/x.mkv", ProfileByID("orig"), -1, 0, false, false); !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("erwartet Ablehnung nach Senken, bekommen: %v", err)
	}
}
