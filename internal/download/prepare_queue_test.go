package download

import (
	"sync/atomic"
	"testing"
	"time"
)

// Kern des Fixes vom 2026-09-14: es darf NIE mehr als maxConcurrentPreps
// gleichzeitig rechnen. Vorher startete jeder Status-Poll für ein weiteres
// Item sofort einen weiteren ffmpeg — drei parallele Läufe zogen den Server
// über eine Stunde auf 1700 % CPU.
func TestPrepRegistryLimitsConcurrency(t *testing.T) {
	reg := &prepRegistry{jobs: map[string]*prepJob{}, recent: map[string]*prepJob{}}

	var running, peak atomic.Int64
	release := make(chan struct{})
	const jobs = 5

	for i := 0; i < jobs; i++ {
		reg.start(string(rune('a'+i)), func(j *prepJob) (string, error) {
			cur := running.Add(1)
			for {
				old := peak.Load()
				if cur <= old || peak.CompareAndSwap(old, cur) {
					break
				}
			}
			<-release
			running.Add(-1)
			return "", nil
		})
	}

	// Kurz laufen lassen, damit alle Goroutinen ihren Platz angefordert haben.
	time.Sleep(150 * time.Millisecond)
	if got := running.Load(); got > maxConcurrentPreps {
		t.Fatalf("%d Läufe gleichzeitig, erlaubt sind %d", got, maxConcurrentPreps)
	}
	close(release)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		reg.mu.Lock()
		n := len(reg.jobs)
		reg.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if p := peak.Load(); p > maxConcurrentPreps {
		t.Errorf("höchstens %d gleichzeitig erwartet, gemessen %d", maxConcurrentPreps, p)
	}
	reg.mu.Lock()
	left := len(reg.jobs)
	reg.mu.Unlock()
	if left != 0 {
		t.Errorf("%d Jobs blieben hängen — die Warteschlange gibt Plätze nicht frei", left)
	}
}

// Ein wartender Job muss als "wird vorbereitet" sichtbar sein, nicht als
// Fehler oder als "nichts los" — sonst sieht der Client ihn gar nicht.
func TestWaitingJobIsVisible(t *testing.T) {
	reg := &prepRegistry{jobs: map[string]*prepJob{}, recent: map[string]*prepJob{}}
	release := make(chan struct{})
	// WICHTIG: erst warten, bis der Blocker den Platz wirklich hält. Ohne das
	// ist der Test ein Rennen — läuft die Goroutine des zweiten Jobs zuerst,
	// nimmt SIE den freien Platz, ist sofort fertig, und "wartet" wäre dann
	// zu Recht false.
	holding := make(chan struct{})
	blocker := reg.start("blocker", func(j *prepJob) (string, error) {
		close(holding)
		<-release
		return "", nil
	})
	<-holding
	waiter := reg.start("waiter", func(j *prepJob) (string, error) { return "", nil })
	time.Sleep(100 * time.Millisecond)

	if blocker.waiting.Load() {
		t.Error("der erste Job sollte rechnen, nicht warten")
	}
	if !waiter.waiting.Load() {
		t.Error("der zweite Job müsste auf einen Platz warten")
	}
	if reg.lookup("waiter") == nil {
		t.Error("ein wartender Job muss auffindbar bleiben")
	}
	close(release)
}
