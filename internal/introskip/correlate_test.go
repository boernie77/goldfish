package introskip

import "testing"

// xorshiftFrames erzeugt eine deterministische Folge von n "Fingerprint"-
// Frames aus einem Seed — für Tests statt echter Audiodaten. Gleicher Seed
// + gleiche Länge ⇒ exakt gleiche Frames (simuliert "dieselbe Audioquelle").
//
// WICHTIG: bei maxHammingPerFrame=6 (von 32 Bit, aus Jellyfins Intro-
// Skipper-Plugin übernommen, siehe correlate.go) matchen rein zufällige,
// unabhängige 32-Bit-Werte nur mit sehr geringer Wahrscheinlichkeit
// (Hamming-Distanz zweier Zufallswerte ist Binomial(32, 0.5)-verteilt,
// P(≤6) < 1%) — anders als beim früheren, laxeren Schwellenwert (15)
// braucht es hier keine künstlichen Bit-Komplemente mehr, um "kein Match"
// zuverlässig zu erzeugen. Sie werden trotzdem verwendet, wo Determinismus
// wichtiger ist als Bequemlichkeit.
func xorshiftFrames(seed uint64, n int) []uint32 {
	state := splitmix64(seed) | 1
	out := make([]uint32, n)
	for i := range out {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		out[i] = uint32(state)
	}
	return out
}

func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	z := x
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// complement gibt eine bitweise invertierte Kopie zurück — garantiert
// Hamming-Distanz 32 zu jedem Frame des Originals, deterministisch "so
// unähnlich wie möglich" statt zufällig-manchmal-ähnlich.
func complement(frames []uint32) []uint32 {
	out := make([]uint32, len(frames))
	for i, f := range frames {
		out[i] = ^f
	}
	return out
}

// withValueJitter gibt eine Kopie von frames zurück, bei der jeder Wert um
// ein kleines deterministisches Delta verschoben ist (±maxDelta) — simuliert
// die Art von Rauschen, die der Inverted-Index-Shift-Search (candidateShifts32,
// Suchradius invertedIndexShift) noch findet: echte Chromaprint-Werte
// benachbarter Encodes liegen oft numerisch nah beieinander, NICHT nur
// Hamming-nah. Ein zufälliges Bit-Flip (wie in einer früheren Fassung
// dieses Tests) verändert den Integer-Wert dagegen beliebig stark und wird
// vom Inverted Index gar nicht erst als Kandidaten-Shift erkannt — das
// testet also am strengeren, index-basierten Algorithmus vorbei.
func withValueJitter(frames []uint32, seed uint64, maxDelta int) []uint32 {
	state := splitmix64(seed) | 1
	out := make([]uint32, len(frames))
	for i, f := range frames {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		delta := int(state%uint64(2*maxDelta+1)) - maxDelta
		out[i] = uint32(int64(f) + int64(delta))
	}
	return out
}

func concatFrames(chunks ...[]uint32) []uint32 {
	var out []uint32
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}

// testFrameDuration: 0.2s/Frame — 150 Frames "Intro" ⇒ 30s, liegt bequem
// zwischen minIntroDurationSec(15) und maxIntroDurationSec(120).
const testFrameDuration = 0.2

func TestCorrelateAudio(t *testing.T) {
	introA := xorshiftFrames(1001, 150) // 30s bei testFrameDuration
	introB := xorshiftFrames(1002, 100) // 20s
	introC := xorshiftFrames(1004, 120) // 24s
	shortIntro := xorshiftFrames(1003, 30) // 6s, < minIntroDurationSec

	t.Run("identisches Intro, kein Tail, delta=0", func(t *testing.T) {
		m, ok := correlateAudio(introA, introA, testFrameDuration)
		if !ok || m.StartFrame != 0 || m.EndFrame != 150 {
			t.Fatalf("got %+v ok=%v, want [0,150)", m, ok)
		}
	})

	t.Run("Intro mit garantiert unähnlichem Rest davor/danach", func(t *testing.T) {
		refPad := xorshiftFrames(3005, 100)
		candPad := complement(refPad) // garantiert unähnlich zu refPad, nicht zu sich selbst
		reference := concatFrames(refPad, introB, refPad)
		candidate := concatFrames(candPad, introB, candPad)
		m, ok := correlateAudio(reference, candidate, testFrameDuration)
		if !ok || m.StartFrame != 100 || m.EndFrame < 200 || m.EndFrame > 210 {
			t.Errorf("got %+v ok=%v, want Lauf ab Frame 100 (Ende~200)", m, ok)
		}
	})

	t.Run("Intro mit Offset, korrekter Shift", func(t *testing.T) {
		refPad := xorshiftFrames(3006, 100)
		candPad := complement(refPad)
		reference := concatFrames(refPad, introB)
		candidate := concatFrames(candPad, candPad, introB)
		m, ok := correlateAudio(reference, candidate, testFrameDuration)
		if !ok || m.StartFrame != 200 {
			t.Errorf("got %+v ok=%v, want Lauf ab Frame 200", m, ok)
		}
	})

	t.Run("garantiert kein gemeinsamer Inhalt (Bit-Komplement)", func(t *testing.T) {
		ref := xorshiftFrames(4001, 300)
		_, ok := correlateAudio(ref, complement(ref), testFrameDuration)
		if ok {
			t.Error("want kein Treffer bei garantiert unähnlichem Inhalt")
		}
	})

	t.Run("verrauschter Near-Duplicate matcht trotzdem", func(t *testing.T) {
		candidate := withValueJitter(introC, 9001, invertedIndexShift) // innerhalb des Index-Suchradius
		m, ok := correlateAudio(introC, candidate, testFrameDuration)
		if !ok || m.StartFrame != 0 || m.EndFrame != 120 {
			t.Fatalf("got %+v ok=%v, want [0,120)", m, ok)
		}
	})

	t.Run("Lauf unter Mindestlänge liefert dennoch einen Rohtreffer (Filterung erfolgt in detectIntro)", func(t *testing.T) {
		m, ok := correlateAudio(shortIntro, shortIntro, testFrameDuration)
		if !ok || m.EndFrame-m.StartFrame != 30 {
			t.Fatalf("got %+v ok=%v, want vollen Rohlauf über 30 Frames", m, ok)
		}
	})
}

func TestDetectIntro(t *testing.T) {
	introA := xorshiftFrames(1001, 150) // 30s
	shortIntro := xorshiftFrames(1003, 30) // 6s, unter minIntroDurationSec

	t.Run("akzeptierter Treffer liefert Start/End in Sekunden", func(t *testing.T) {
		start, end, _, ok := detectIntro(introA, introA, testFrameDuration)
		if !ok {
			t.Fatal("want ok=true")
		}
		if start != 0 {
			t.Errorf("start = %v, want 0", start)
		}
		if end <= 0 || end > 30 {
			t.Errorf("end = %v, want (0,30]", end)
		}
	})

	t.Run("zu kurzer Lauf wird abgelehnt (unter minIntroDurationSec)", func(t *testing.T) {
		_, _, _, ok := detectIntro(shortIntro, shortIntro, testFrameDuration)
		if ok {
			t.Error("want ok=false, Lauf ist kürzer als minIntroDurationSec")
		}
	})

	t.Run("kein gemeinsamer Inhalt liefert ok=false", func(t *testing.T) {
		_, _, _, ok := detectIntro(introA, complement(introA), testFrameDuration)
		if ok {
			t.Error("want ok=false bei garantiert unähnlichem Inhalt")
		}
	})
}

func TestWithinIntroWindow(t *testing.T) {
	if !withinIntroWindow(maxIntroStartSec) {
		t.Error("Grenzwert selbst sollte noch akzeptiert werden")
	}
	if withinIntroWindow(maxIntroStartSec + 0.1) {
		t.Error("Wert knapp über der Grenze sollte abgelehnt werden")
	}
	if !withinIntroWindow(0) {
		t.Error("Start bei 0s sollte akzeptiert werden")
	}
}

func TestAggregateObservations(t *testing.T) {
	t.Run("genug übereinstimmende Beobachtungen werden akzeptiert", func(t *testing.T) {
		obs := []observation{{startSec: 30, endSec: 60}, {startSec: 32, endSec: 61}, {startSec: 200, endSec: 230}}
		agg, ok := aggregateObservations(obs, 2)
		if !ok {
			t.Fatal("want ok=true")
		}
		if agg.startSec != 30 && agg.startSec != 32 {
			t.Errorf("startSec = %v, want Median aus dem größeren Cluster (30/32)", agg.startSec)
		}
	})

	t.Run("zu wenige übereinstimmende Beobachtungen werden abgelehnt", func(t *testing.T) {
		obs := []observation{{startSec: 30, endSec: 60}, {startSec: 200, endSec: 230}}
		_, ok := aggregateObservations(obs, 2)
		if ok {
			t.Error("want ok=false, keine zwei Beobachtungen stimmen überein")
		}
	})

	t.Run("weniger Beobachtungen als minAgree wird sofort abgelehnt", func(t *testing.T) {
		obs := []observation{{startSec: 30, endSec: 60}}
		_, ok := aggregateObservations(obs, 2)
		if ok {
			t.Error("want ok=false")
		}
	})
}

func TestVerifyVideoMatch(t *testing.T) {
	ref := make([]uint64, 500)
	for i := range ref {
		ref[i] = uint64(splitmix64(uint64(i) + 7777))
	}
	t.Run("identische Hashes im Fenster werden bestätigt", func(t *testing.T) {
		cand := make([]uint64, 500)
		copy(cand, ref)
		if !verifyVideoMatch(ref, cand, 100, 130, 0) {
			t.Error("want Bild-Bestätigung bei identischen Hashes")
		}
	})
	t.Run("komplett andere Hashes im Fenster werden abgelehnt", func(t *testing.T) {
		cand := make([]uint64, 500)
		for i := range cand {
			cand[i] = ^ref[i]
		}
		if verifyVideoMatch(ref, cand, 100, 130, 0) {
			t.Error("want keine Bild-Bestätigung bei komplett unähnlichen Hashes")
		}
	})
	t.Run("Shift wird korrekt angewendet", func(t *testing.T) {
		cand := make([]uint64, 500)
		copy(cand, ref)
		// candidate bei Index i entspricht reference bei Index i+10
		copy(cand[90:120], ref[100:130])
		if !verifyVideoMatch(ref, cand, 90, 120, 10) {
			t.Error("want Bild-Bestätigung mit korrektem Shift")
		}
	})
}
