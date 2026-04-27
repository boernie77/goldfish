package playback

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// IsMP4Streamable prüft, ob das `moov`-Atom (Header) im MP4-Container vor dem
// `mdat`-Atom (Daten) liegt — Voraussetzung für progressives Direct Play im
// Browser. Liegt `moov` am Ende der Datei (typisch für ältere ffmpeg-Outputs
// ohne `-movflags +faststart`), kann der Browser den Stream nicht ohne
// vollständigen Download dekodieren.
//
// Liefert:
//   true  – moov vor mdat (streamfähig)
//   false – mdat zuerst (nicht streamfähig, Transcode/Remux nötig)
//   error – I/O-Fehler oder unleserlicher Container
//
// Nur die Atom-Header (8–16 Byte pro Atom) werden gelesen, der Rest der
// Atome wird per Seek übersprungen — die Prüfung ist daher schnell, auch
// bei mehreren GB großen Dateien.
func IsMP4Streamable(path string) (bool, error) {
	ext := strings.ToLower(extOf(path))
	if ext != ".mp4" && ext != ".mov" && ext != ".m4v" {
		// Andere Container brauchen ohnehin Transcode/Remux. Wir behaupten
		// hier "true" damit der bestehende Decider die Containerlogik macht.
		return true, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	var hdr [16]byte
	for {
		// Atom-Header: 8 Bytes (size + type). Bei size==1 folgen weitere
		// 8 Bytes für eine 64-Bit-Größe.
		n, err := io.ReadFull(f, hdr[:8])
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				// EOF ohne moov gefunden → wahrscheinlich kaputt; konservativ
				// als nicht-streamfähig melden, damit der Decider Transcode wählt.
				return false, nil
			}
			return false, err
		}
		if n < 8 {
			return false, fmt.Errorf("short read")
		}
		size := uint64(binary.BigEndian.Uint32(hdr[0:4]))
		atomType := string(hdr[4:8])
		headerLen := int64(8)
		if size == 1 {
			// 64-bit size folgt
			if _, err := io.ReadFull(f, hdr[8:16]); err != nil {
				return false, err
			}
			size = binary.BigEndian.Uint64(hdr[8:16])
			headerLen = 16
		} else if size == 0 {
			// Atom reicht bis EOF — keine weiteren Atome danach.
			return atomType == "moov", nil
		}
		if size < uint64(headerLen) {
			return false, fmt.Errorf("ungültige Atom-Größe %d in %s", size, atomType)
		}
		switch atomType {
		case "moov":
			return true, nil // Header vor Daten → streamfähig
		case "mdat":
			return false, nil // Daten zuerst → moov muss hinten liegen → nicht streamfähig
		}
		// Andere Atome (ftyp, free, skip, …) überspringen
		skip := int64(size) - headerLen
		if _, err := f.Seek(skip, io.SeekCurrent); err != nil {
			return false, err
		}
	}
}

// extOf gibt die letzte Datei-Extension einer Pfad-Zeichenkette zurück
// (inkl. Punkt, lowercased im Aufrufer). Vermeidet eine Abhängigkeit auf
// path/filepath nur für die eine Stelle.
func extOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return ""
		}
		if p[i] == '.' {
			return p[i:]
		}
	}
	return ""
}
