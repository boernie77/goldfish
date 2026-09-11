package playback

import (
	"testing"
	"time"
)

func TestActivityTouchAndExpiry(t *testing.T) {
	// Frisch: vor jedem Touch() darf Active() nicht durch einen früheren
	// Testlauf im selben Prozess "true" bleiben.
	activityMu.Lock()
	lastActivity = time.Time{}
	activityMu.Unlock()
	if Active() {
		t.Fatal("Active() sollte ohne vorherigen Touch() false sein")
	}
	TouchActivity()
	if !Active() {
		t.Fatal("Active() sollte direkt nach TouchActivity() true sein")
	}
}

func TestActivityExpiresAfterWindow(t *testing.T) {
	TouchActivity()
	activityMu.Lock()
	lastActivity = lastActivity.Add(-activityWindow - 1)
	activityMu.Unlock()
	if Active() {
		t.Fatal("Active() sollte nach Ablauf von activityWindow false sein")
	}
}
