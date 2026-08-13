package introskip

import (
	"math"
	"math/bits"
	"sort"
)

// Schwellenwerte, übernommen von Jellyfins "Intro Skipper"-Plugin
// (ConfusedPolarBear/intro-skipper, ChromaprintAnalyzer.cs +
// PluginConfiguration.cs, Stand 2026-08-13 von GitHub gelesen) — nicht mehr
// selbst erraten, siehe CLAUDE.md "Intro-Erkennung" für Details/Quelle und
// die Vorgeschichte der vorherigen, zu unpräzisen Eigenkonstruktion.
const (
	// maxHammingPerFrame: maximale Hamming-Distanz (von 32 Bit), ab der
	// zwei Chromaprint-Frames als "gleich" gelten. Jellyfin-Default: 6
	// (81% Ähnlichkeit) — deutlich strenger als unser vorheriger Wert (15).
	maxHammingPerFrame = 6

	// invertedIndexShift: beim Suchen nach Kandidaten-Zeitversätzen werden
	// nur Fingerprint-WERTE verglichen, die höchstens um diese Zahl
	// auseinanderliegen (direkte Integer-Differenz, nicht Hamming-Distanz —
	// Chromaprint-Werte benachbarter Audioabschnitte liegen oft auch
	// numerisch nah beieinander). Ersetzt den früheren Brute-Force-Scan
	// über ALLE möglichen Zeitversätze durch einen gezielten, index-
	// basierten Ansatz (schneller UND präziser: nur echte Ausrichtungs-
	// Hinweise werden überhaupt als Kandidat betrachtet).
	invertedIndexShift = 2

	// maxTimeSkipSec: maximale Lücke (in Sekunden) zwischen zwei
	// ähnlichen Frames, damit sie noch als zusammenhängender Lauf gelten.
	maxTimeSkipSec = 3.5

	// minIntroDurationSec/maxIntroDurationSec: ein acceptierter Lauf muss
	// in diesem Bereich liegen. Die Obergrenze fehlte in der ersten
	// Eigenkonstruktion komplett — dadurch wurden lang andauernde
	// wiederverwendete Szenen-Hintergrundmusik-Cues (die über eine ganze
	// Dialogszene laufen können) fälschlich als "Vorspann" akzeptiert.
	minIntroDurationSec = 15.0
	maxIntroDurationSec = 120.0
)

// maxIntroStartSec: ein Treffer, der erst nach dieser Zeit im Kandidaten
// beginnt, wird verworfen — grobe Plausibilitätsgrenze knapp unterhalb von
// prefixSeconds (fingerprint.go).
const maxIntroStartSec = 800.0

func hamming32(a, b uint32) int {
	return bits.OnesCount32(a ^ b)
}

// buildInvertedIndex32: Fingerprint-Wert → letzter Index, an dem er
// vorkommt (wie Jellyfins FFmpegWrapper.CreateInvertedIndex).
func buildInvertedIndex32(fp []uint32) map[uint32]int {
	idx := make(map[uint32]int, len(fp))
	for i, p := range fp {
		idx[p] = i
	}
	return idx
}

// candidateShifts32: findet Zeitversätze, an denen reference und candidate
// (nahezu) identische Fingerprint-WERTE haben (Integer-Differenz bis
// invertedIndexShift, per Wraparound-Arithmetik wie Jellyfins
// `(uint)(originalPoint + i)`). Liefert nur echte Ausrichtungs-Kandidaten
// statt jeden möglichen Shift brute-force zu prüfen.
func candidateShifts32(refIdx map[uint32]int, candidate []uint32) []int {
	seen := map[int]bool{}
	var shifts []int
	for i, p := range candidate {
		for d := -invertedIndexShift; d <= invertedIndexShift; d++ {
			modified := p + uint32(d)
			if refPos, ok := refIdx[modified]; ok {
				shift := refPos - i
				if !seen[shift] {
					seen[shift] = true
					shifts = append(shifts, shift)
				}
			}
		}
	}
	return shifts
}

// findLongestContiguous: findet unter den (aufsteigend sortierten)
// candidate-Frame-Indizes matchedFrames den LÄNGSTEN Lauf, bei dem
// aufeinanderfolgende Einträge höchstens maxGapFrames auseinanderliegen —
// exakte Semantik von Jellyfins TimeRangeHelpers.FindContiguous (dort
// sortiert nach Dauer absteigend, nimmt den ersten = längsten).
func findLongestContiguous(matchedFrames []int, maxGapFrames int) (start, end int, ok bool) {
	if len(matchedFrames) == 0 {
		return 0, 0, false
	}
	bestStart, bestEnd := matchedFrames[0], matchedFrames[0]
	curStart, curEnd := matchedFrames[0], matchedFrames[0]
	for i := 1; i < len(matchedFrames); i++ {
		if matchedFrames[i]-curEnd <= maxGapFrames {
			curEnd = matchedFrames[i]
			continue
		}
		if curEnd-curStart > bestEnd-bestStart {
			bestStart, bestEnd = curStart, curEnd
		}
		curStart, curEnd = matchedFrames[i], matchedFrames[i]
	}
	if curEnd-curStart > bestEnd-bestStart {
		bestStart, bestEnd = curStart, curEnd
	}
	return bestStart, bestEnd, true
}

// MatchResult beschreibt den gefundenen Übereinstimmungs-Bereich in der
// Zeitleiste des KANDIDATEN — StartFrame/EndFrame sind Indizes ins
// candidate-Array, EndFrame exklusiv. ShiftFrames ist der Zeitversatz
// (referenceIndex - candidateIndex), mit dem der Treffer gefunden wurde —
// wird für die Video-Gegenprüfung gebraucht, um den entsprechenden
// Zeitbereich in der REFERENZ zu berechnen, ohne eine zweite unabhängige
// Suche zu machen.
type MatchResult struct {
	StartFrame  int
	EndFrame    int
	ShiftFrames int
}

// correlateAudio sucht per Inverted-Index-Ausrichtung (siehe
// candidateShifts32) den längsten zusammenhängenden Übereinstimmungs-Lauf
// zwischen reference und candidate (Chromaprint-Fingerprints). Für jeden
// Kandidaten-Zeitversatz wird der längste Lauf ermittelt, danach gewinnt
// der längste Lauf ÜBER ALLE Zeitversätze hinweg (wie Jellyfins
// GetLongestTimeRange).
func correlateAudio(reference, candidate []uint32, frameDuration float64) (MatchResult, bool) {
	if len(reference) == 0 || len(candidate) == 0 {
		return MatchResult{}, false
	}
	maxGapFrames := int(maxTimeSkipSec/frameDuration) + 1

	refIdx := buildInvertedIndex32(reference)
	shifts := candidateShifts32(refIdx, candidate)

	var best MatchResult
	bestLen := -1
	found := false

	for _, shift := range shifts {
		var matched []int
		for i := 0; i < len(candidate); i++ {
			j := i + shift
			if j < 0 || j >= len(reference) {
				continue
			}
			if hamming32(candidate[i], reference[j]) <= maxHammingPerFrame {
				matched = append(matched, i)
			}
		}
		start, end, ok := findLongestContiguous(matched, maxGapFrames)
		if !ok {
			continue
		}
		if runLen := end - start; runLen > bestLen {
			bestLen, best, found = runLen, MatchResult{StartFrame: start, EndFrame: end + 1, ShiftFrames: shift}, true
		}
	}

	return best, found
}

// detectIntro wendet correlateAudio() an, wendet Jellyfins End-Trimming
// (verhindert Überschießen durch die Lücken-Toleranz) und die Min/Max-
// Dauer- sowie Plausibilitäts-Grenzen an, und liefert Start-/Endzeit in
// Sekunden (Kandidaten-Zeitleiste).
func detectIntro(reference, candidate []uint32, frameDuration float64) (startSec, endSec, shiftSec float64, ok bool) {
	m, found := correlateAudio(reference, candidate, frameDuration)
	if !found {
		return 0, 0, 0, false
	}
	startSec = float64(m.StartFrame) * frameDuration
	endSec = float64(m.EndFrame) * frameDuration
	shiftSec = float64(m.ShiftFrames) * frameDuration
	dur := endSec - startSec

	// End-Trimming wie Jellyfin: die Lücken-Toleranz (maxTimeSkipSec) kann
	// dazu führen, dass der letzte paar Sekunden des erkannten Laufs schon
	// echter Inhalt sind (der letzte MATCH lag ja noch innerhalb der
	// Toleranz, nicht zwingend exakt am wahren Ende). Bei längeren Läufen
	// wird deshalb etwas vom Ende abgeschnitten.
	switch {
	case dur >= 90:
		endSec -= 2 * maxTimeSkipSec
	case dur >= 30:
		endSec -= maxTimeSkipSec
	}
	if endSec < startSec {
		endSec = startSec
	}
	dur = endSec - startSec

	if dur < minIntroDurationSec || dur > maxIntroDurationSec {
		return 0, 0, 0, false
	}
	// Sehr früher Start (Rundungsrauschen) auf 0 snappen, wie Jellyfin.
	if startSec <= 5 {
		startSec = 0
	}
	if !withinIntroWindow(startSec) {
		return 0, 0, 0, false
	}
	return startSec, endSec, shiftSec, true
}

func withinIntroWindow(startSec float64) bool { return startSec <= maxIntroStartSec }

// --- Konsens über mehrere Episoden-Paarungen (echtes Paarweise-Vergleichen) ---

type observation struct {
	startSec, endSec float64
}

// aggreementToleranceSec: wie nah zwei unabhängige Beobachtungen (von
// unterschiedlichen Referenz-Episoden) für DIESELBE Kandidaten-Episode
// beieinander liegen müssen, um als "derselbe Treffer" zu gelten.
const agreementToleranceSec = 20.0

// aggregateObservations clustert die Beobachtungen (aus mehreren
// Referenz-Vergleichen für dieselbe Episode) nach zeitlicher Nähe und
// verlangt mindestens minAgree übereinstimmende Beobachtungen, bevor ein
// Ergebnis akzeptiert wird — echtes Paarweise-Vergleichen plus Konsens
// statt sich auf eine einzelne Paarung zu verlassen.
func aggregateObservations(obs []observation, minAgree int) (observation, bool) {
	if len(obs) < minAgree {
		return observation{}, false
	}
	sort.Slice(obs, func(i, j int) bool { return obs[i].startSec < obs[j].startSec })

	bestStart, bestSize := 0, 1
	curStart, curSize := 0, 1
	for i := 1; i < len(obs); i++ {
		if obs[i].startSec-obs[i-1].startSec <= agreementToleranceSec {
			curSize++
		} else {
			curStart, curSize = i, 1
		}
		if curSize > bestSize {
			bestStart, bestSize = curStart, curSize
		}
	}
	if bestSize < minAgree {
		return observation{}, false
	}
	cluster := obs[bestStart : bestStart+bestSize]
	starts := make([]float64, len(cluster))
	ends := make([]float64, len(cluster))
	for i, o := range cluster {
		starts[i] = o.startSec
		ends[i] = o.endSec
	}
	sort.Float64s(starts)
	sort.Float64s(ends)
	return observation{startSec: starts[len(starts)/2], endSec: ends[len(ends)/2]}, true
}

// --- Bild-Gegenprüfung (dHash, siehe videofingerprint.go) ---
//
// Audio allein kann getäuscht werden von wiederverwendetem Audio, das
// NICHT zum Vorspann gehört (z.B. Szenen-Score-Musik, die in mehreren
// Episoden identisch wiederverwendet wird). Der Bild-Inhalt einer echten
// Titelsequenz ist dagegen praktisch IMMER exakt identisch zwischen
// Episoden (dieselbe Animation) — ein Bild-Abgleich am audio-gefundenen
// Zeitfenster dient als zweite, unabhängige Bestätigung. Nur wenn BEIDE
// Signale übereinstimmen, gilt ein Treffer als verlässlich.

const (
	// maxHammingPerFrameVideo: maximale Hamming-Distanz (von 64 Bit dHash),
	// ab der zwei Bilder als "gleich" gelten. Grosszügiger als beim exakten
	// Pixel-Vergleich nötig wäre, da unterschiedliche Encodes/Kompression
	// leichte Abweichungen erzeugen — 12 von 64 Bit (~19%) lässt das zu,
	// verwirft aber klar unterschiedliche Einstellungen zuverlässig.
	maxHammingPerFrameVideo = 12

	// minVideoAgreementFraction: Anteil der Bild-Vergleiche im Fenster, die
	// unter der Hamming-Schwelle liegen müssen, damit das Fenster als
	// visuell bestätigt gilt.
	minVideoAgreementFraction = 0.6
)

// verifyVideoMatch prüft, ob der per Audio gefundene Kandidaten-Zeitbereich
// [candStartSec, candEndSec) auch BILDLICH mit der Referenz übereinstimmt.
// shiftSec ist der Zeitversatz aus detectIntro (referenceSec - candidateSec)
// — die Bild-Hashes liegen im Sekundentakt (videoFPS=1), daher genügt eine
// einfache Index-Verschiebung ohne erneute Suche.
func verifyVideoMatch(referenceHashes, candidateHashes []uint64, candStartSec, candEndSec, shiftSec float64) bool {
	startIdx := int(math.Round(candStartSec))
	endIdx := int(math.Round(candEndSec))
	if endIdx <= startIdx {
		return false
	}
	total, matched := 0, 0
	for i := startIdx; i < endIdx; i++ {
		if i < 0 || i >= len(candidateHashes) {
			continue
		}
		refIdx := int(math.Round(float64(i) + shiftSec))
		if refIdx < 0 || refIdx >= len(referenceHashes) {
			continue
		}
		total++
		if bits.OnesCount64(candidateHashes[i]^referenceHashes[refIdx]) <= maxHammingPerFrameVideo {
			matched++
		}
	}
	if total == 0 {
		return false
	}
	return float64(matched)/float64(total) >= minVideoAgreementFraction
}
