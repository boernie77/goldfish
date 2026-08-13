package introskip

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"
)

const (
	ffmpegBin = "ffmpeg"

	// videoFPS: wie viele Bild-Hashes pro Sekunde erzeugt werden. 1 reicht,
	// da Vorspann-Sequenzen mehrere Sekunden pro Einstellung zeigen — eine
	// höhere Rate würde nur Rechenzeit kosten, ohne Genauigkeit zu gewinnen.
	videoFPS = 1

	// hashSize: Kantenlänge des Graustufen-Thumbnails vor dem dHash.
	// dHash braucht (hashSize+1) x hashSize Pixel (vergleicht jedes Pixel
	// mit seinem rechten Nachbarn) → 64 Bit Hash bei 8x8.
	hashSize = 8

	// videoTimeout: pro Episode wird nur videoFPS*prefixSeconds Frames
	// dekodiert (bei 1fps und 900s Fenster: 900 Bilder) — günstig, aber
	// ffmpeg muss trotzdem den ganzen Videostream bis dahin durchlaufen.
	videoTimeout = 3 * time.Minute
)

// extractVideoHashes dekodiert die ersten prefixSeconds Sekunden des Videos
// mit videoFPS Bildern/Sekunde, skaliert jedes Bild auf ein kleines
// Graustufen-Thumbnail und berechnet einen 64-Bit Differenz-Hash (dHash)
// pro Bild. dHash ist robust gegen leichte Helligkeits-/Kompressions-
// Unterschiede, aber empfindlich genug, um unterschiedliche Einstellungen
// (z.B. eine andere Werbung oder eine andere Szene) klar zu unterscheiden.
func extractVideoHashes(ctx context.Context, mediaPath string) ([]uint64, error) {
	cctx, cancel := context.WithTimeout(ctx, videoTimeout)
	defer cancel()

	scaleFilter := fmt.Sprintf("fps=%d,scale=%d:%d:flags=bilinear,format=gray", videoFPS, hashSize+1, hashSize)
	cmd := exec.CommandContext(cctx, ffmpegBin,
		"-y", "-i", mediaPath,
		"-t", strconv.Itoa(prefixSeconds),
		"-vf", scaleFilter,
		"-f", "rawvideo", "-pix_fmt", "gray",
		"-",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}
	var stderr []byte
	cmd.Stderr = nil // CombinedOutput() nicht nutzbar wegen StdoutPipe; Fehler kommen über ExitError

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ffmpeg (video) start: %w", err)
	}

	frameSize := (hashSize + 1) * hashSize
	buf := make([]byte, frameSize)
	var hashes []uint64
	for {
		_, err := io.ReadFull(stdout, buf)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			_ = cmd.Wait()
			return nil, fmt.Errorf("ffmpeg (video) read: %w", err)
		}
		hashes = append(hashes, dHash(buf))
	}

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("ffmpeg (video): %w — %s", err, truncate(string(stderr), 200))
	}
	if len(hashes) == 0 {
		return nil, fmt.Errorf("ffmpeg (video): keine Frames extrahiert")
	}
	return hashes, nil
}

// dHash berechnet einen 64-Bit-Differenz-Hash aus einem (hashSize+1) x
// hashSize großen Graustufen-Bild (row-major, ein Byte pro Pixel): Bit i
// ist gesetzt, wenn Pixel i heller ist als sein rechter Nachbar. Zwei
// visuell ähnliche Bilder ergeben eine niedrige Hamming-Distanz, zwei
// unterschiedliche Einstellungen (andere Szene/Werbung) landen nahe der
// Zufalls-Distanz (~32 von 64 Bit).
func dHash(pixels []byte) uint64 {
	var hash uint64
	bit := 0
	for row := 0; row < hashSize; row++ {
		rowStart := row * (hashSize + 1)
		for col := 0; col < hashSize; col++ {
			if pixels[rowStart+col] < pixels[rowStart+col+1] {
				hash |= 1 << uint(bit)
			}
			bit++
		}
	}
	return hash
}
