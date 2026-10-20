// Package mediainput übersetzt einen Dateipfad in das, was ffmpeg/ffprobe als
// Eingabe bekommen. Normale Videodateien gehen unverändert durch; Blu-ray-
// Abbilder (.iso) werden über das libbluray-Protokoll gelesen, das den
// Hauptfilm (längste Playlist) auswählt.
package mediainput

import (
	"path/filepath"
	"strings"
)

// IsDisc meldet, ob path ein Disc-Abbild ist, das nur über "bluray:" lesbar ist.
func IsDisc(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".iso")
}

// Arg liefert den Wert für `-i` bzw. das ffprobe-Argument.
func Arg(path string) string {
	if IsDisc(path) {
		return "bluray:" + path
	}
	return path
}

// ProbeOpts sind zusätzliche Eingabe-Optionen, die VOR `-i` stehen müssen.
// Blu-ray-Streams (viele PGS-/Audiospuren) brauchen mehr Analysezeit, sonst
// meldet ffmpeg unvollständige Codec-Parameter.
func ProbeOpts(path string) []string {
	if IsDisc(path) {
		return []string{"-analyzeduration", "20M", "-probesize", "50M"}
	}
	return nil
}
