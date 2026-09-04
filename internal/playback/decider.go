package playback

import (
	"strings"

	"github.com/boernie77/goldfish/internal/model"
)

type Mode string

const (
	ModeDirectPlay Mode = "direct"
	ModeTranscode  Mode = "transcode"
)

type Decision struct {
	Mode   Mode
	Reason string
}

// DecideWithCap berücksichtigt zusätzlich einen Max-Quality-Cap (Profil).
// Wenn das Item das Profil-Limit überschreitet (Höhe oder Bitrate), wird
// Transcode erzwungen, selbst bei browser-kompatiblen Dateien. "orig"-Profil
// (MaxHeight=0 und VideoKbps=0) bedeutet "keine Beschränkung" und verhält sich
// wie Decide ohne Cap.
func DecideWithCap(it *model.Item, cap Profile) Decision {
	if cap.MaxHeight == 0 && cap.VideoKbps == 0 {
		return Decide(it)
	}
	// Cap-Überschreitung prüfen
	exceedsH := cap.MaxHeight > 0 && it.Height > cap.MaxHeight
	exceedsB := cap.VideoKbps > 0 && it.BitrateKbps > cap.VideoKbps
	if exceedsH || exceedsB {
		reason := "Qualitäts-Cap greift: "
		if exceedsH {
			reason += "Auflösung zu hoch"
		}
		if exceedsB {
			if exceedsH {
				reason += " + "
			}
			reason += "Bitrate zu hoch"
		}
		return Decision{Mode: ModeTranscode, Reason: reason}
	}
	// Unter Cap-Limit: ganz normaler Auto-Flow
	return Decide(it)
}

// Decide chooses how to deliver the media. Kept simple: browser-friendly
// mp4/mov with h264/aac → Direct Play; everything else → Transcode to HLS.
// Remux is intentionally omitted to keep the code path small; QSV transcoding
// is cheap enough on modern Intel iGPUs.
func Decide(it *model.Item) Decision {
	container := strings.ToLower(it.Container)
	vc := strings.ToLower(it.VideoCodec)
	ac := strings.ToLower(it.AudioCodec)

	// Interlaced Content (DVDs, alte TV-Captures) → Transcode mit Deinterlace-
	// Filter. Browser können Halbbilder nicht selbst entkämmen.
	if IsInterlaced(it) {
		return Decision{Mode: ModeTranscode, Reason: "Interlaced — Deinterlace-Filter angewendet"}
	}

	// Audio-Only (Musik-Bibliotheken, kein Video-Stream): eigener Zweig VOR den
	// Video-Checks unten, sonst fällt jede Audio-Datei mangels VideoCodec durch
	// in den generischen Video-Transcode-Reason-String. mp3/aac/vorbis/opus
	// spielt jeder Browser nativ; flac/wav/alac (patchiere native Unterstützung,
	// v1-Vereinfachung) werden zu AAC transcodiert.
	if vc == "" && ac != "" {
		if ac == "mp3" || ac == "aac" || ac == "vorbis" || ac == "opus" {
			return Decision{Mode: ModeDirectPlay, Reason: "Audio direct-play (" + ac + ")"}
		}
		return Decision{Mode: ModeTranscode, Reason: "Audio-Transcode (" + ac + " → aac)"}
	}

	isDirectContainer := container == "mp4" || container == "mov" ||
		strings.Contains(container, "mp4") || strings.Contains(container, "mov")
	isDirectVideo := vc == "h264" || vc == "avc" || vc == "avc1"
	isDirectAudio := ac == "aac" || ac == "mp3"

	if isDirectContainer && isDirectVideo && isDirectAudio {
		return Decision{Mode: ModeDirectPlay, Reason: "browser-kompatibel (mp4/h264/aac)"}
	}
	return Decision{Mode: ModeTranscode, Reason: "Transcode zu HLS/H264 (" + container + "/" + vc + "/" + ac + ")"}
}

// IsInterlaced gibt true zurück, wenn mindestens ein Video-Stream des Items
// ein field_order ungleich "progressive"/"unknown"/leer hat (also tt/bb/tb/bt).
func IsInterlaced(it *model.Item) bool {
	if it == nil {
		return false
	}
	for _, st := range it.Streams {
		if st.Type != "video" {
			continue
		}
		fo := strings.ToLower(strings.TrimSpace(st.FieldOrder))
		if fo == "" || fo == "progressive" || fo == "unknown" {
			continue
		}
		return true
	}
	return false
}
