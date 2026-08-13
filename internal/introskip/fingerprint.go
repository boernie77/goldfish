package introskip

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	fpcalcBin = "fpcalc"

	// prefixSeconds: wie viel Audio (ab Dateianfang) fpcalc fingerprinten
	// soll. Bewusst großzügig (15 Minuten) — Live-Test gegen "Chuck" zeigte
	// echte Vorspann-Treffer teils erst nach 7-8 Minuten (Cold Open +
	// "Previously on..."-Recap), ein knappes Fenster hätte den Treffer
	// abgeschnitten.
	prefixSeconds = 15 * 60 // 900s

	// fpcalcTimeout: fester Cap statt laufzeit-skaliert (wie bei Whisper),
	// weil immer nur ein fixes Präfix dekodiert wird, unabhängig von der
	// Gesamtlänge der Episode.
	fpcalcTimeout = 5 * time.Minute
)

// fingerprint bündelt das Ergebnis eines fpcalc-Laufs: das rohe
// Fingerprint-Array plus die daraus abgeleitete Sekunden-pro-Frame-Dauer.
// Letztere wird NICHT hart kodiert (Chromaprints Frame-Dauer ist je nach
// Version/Algorithmus leicht unterschiedlich), sondern aus fpcalcs eigener
// DURATION-Angabe geteilt durch die Anzahl Fingerprint-Werte berechnet.
type fingerprint struct {
	frames        []uint32
	frameDuration float64 // Sekunden pro Frame
}

// extractFingerprint ruft fpcalc auf ein Mediafile auf und parst die
// rohe Integer-Fingerprint-Folge der ersten prefixSeconds Sekunden.
//
// Erwartetes fpcalc-Textformat (Default-Ausgabe mit -raw):
//
//	DURATION=480
//	FINGERPRINT=123,456,789,...
func extractFingerprint(ctx context.Context, mediaPath string) (fingerprint, error) {
	cctx, cancel := context.WithTimeout(ctx, fpcalcTimeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, fpcalcBin, "-raw", "-length", strconv.Itoa(prefixSeconds), mediaPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fingerprint{}, fmt.Errorf("fpcalc: %w — %s", err, truncate(string(out), 200))
	}

	var durationSec float64
	var rawFP string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "DURATION="):
			durationSec, _ = strconv.ParseFloat(strings.TrimPrefix(line, "DURATION="), 64)
		case strings.HasPrefix(line, "FINGERPRINT="):
			rawFP = strings.TrimPrefix(line, "FINGERPRINT=")
		}
	}
	if rawFP == "" {
		return fingerprint{}, fmt.Errorf("fpcalc: keine FINGERPRINT-Zeile in der Ausgabe (%s)", truncate(string(out), 200))
	}

	parts := strings.Split(rawFP, ",")
	frames := make([]uint32, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return fingerprint{}, fmt.Errorf("fpcalc: ungültiger Fingerprint-Wert %q: %w", p, err)
		}
		frames = append(frames, uint32(v))
	}
	if len(frames) == 0 {
		return fingerprint{}, fmt.Errorf("fpcalc: leeres Fingerprint-Array")
	}
	if durationSec <= 0 {
		return fingerprint{}, fmt.Errorf("fpcalc: keine gültige DURATION in der Ausgabe (%s)", truncate(string(out), 200))
	}
	// WICHTIG: fpcalcs DURATION-Feld ist die volle Datei-/Stream-Länge, NICHT
	// die tatsächlich fingerprintete Länge (die "-length"-Restriktion wirkt
	// nur auf die Fingerprint-Erzeugung selbst, nicht auf den ausgegebenen
	// DURATION-Wert). Bei einer 42-Minuten-Episode mit -length 900 stünde
	// hier trotzdem ~2520 — würde frameDuration um Faktor ~2,8 verfälschen.
	// Deshalb auf prefixSeconds deckeln (Live-Test 2026-08-11 aufgefallen).
	analyzedSec := durationSec
	if analyzedSec > prefixSeconds {
		analyzedSec = prefixSeconds
	}

	return fingerprint{
		frames:        frames,
		frameDuration: analyzedSec / float64(len(frames)),
	}, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
