package playback

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// helper: minimaler Item-Builder fuer Decider-Tests.
func mkItem(container, vc, ac string, height, bitrate int) *model.Item {
	return &model.Item{
		Container:   container,
		VideoCodec:  vc,
		AudioCodec:  ac,
		Height:      height,
		BitrateKbps: bitrate,
	}
}

// Decide: browser-kompatibel = mp4/mov + h264 + (aac|mp3) → Direct Play.
// Alles andere → Transcode.
func TestDecide_Matrix(t *testing.T) {
	tests := []struct {
		name      string
		container string
		vc        string
		ac        string
		wantMode  Mode
	}{
		// Direct Play — alle browser-kompatiblen Kombis
		{"mp4/h264/aac", "mp4", "h264", "aac", ModeDirectPlay},
		{"mov/h264/aac", "mov", "h264", "aac", ModeDirectPlay},
		{"mp4/h264/mp3", "mp4", "h264", "mp3", ModeDirectPlay},
		{"mp4/avc/aac", "mp4", "avc", "aac", ModeDirectPlay},
		{"mp4/avc1/aac", "mp4", "avc1", "aac", ModeDirectPlay},
		{"Mixed-Case Container", "MP4", "H264", "AAC", ModeDirectPlay},
		// ffprobe liefert manchmal Multi-Container-Strings wie "mov,mp4,m4a"
		{"Multi-Container mit mp4", "mov,mp4,m4a", "h264", "aac", ModeDirectPlay},

		// Transcode — Container falsch
		{"mkv mit h264/aac", "mkv", "h264", "aac", ModeTranscode},
		{"avi mit h264/aac", "avi", "h264", "aac", ModeTranscode},

		// Transcode — Codec falsch
		{"mp4/hevc/aac", "mp4", "hevc", "aac", ModeTranscode},
		{"mp4/h265/aac", "mp4", "h265", "aac", ModeTranscode},
		{"mp4/h264/ac3", "mp4", "h264", "ac3", ModeTranscode},
		{"mp4/h264/eac3", "mp4", "h264", "eac3", ModeTranscode},
		{"mp4/h264/dts", "mp4", "h264", "dts", ModeTranscode},
		{"mp4/vp9/opus", "mp4", "vp9", "opus", ModeTranscode},

		// Audio-Only (Musik-Bibliotheken, kein Video-Stream)
		{"Audio mp3 direct", "mp3", "", "mp3", ModeDirectPlay},
		{"Audio aac direct", "m4a", "", "aac", ModeDirectPlay},
		{"Audio vorbis direct", "ogg", "", "vorbis", ModeDirectPlay},
		{"Audio opus direct", "opus", "", "opus", ModeDirectPlay},
		{"Audio flac transcode", "flac", "", "flac", ModeTranscode},
		{"Audio wav transcode", "wav", "", "pcm_s16le", ModeTranscode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decide(mkItem(tt.container, tt.vc, tt.ac, 1080, 5000))
			if got.Mode != tt.wantMode {
				t.Errorf("Decide(%s/%s/%s) = %s, want %s — Reason: %s",
					tt.container, tt.vc, tt.ac, got.Mode, tt.wantMode, got.Reason)
			}
		})
	}
}

// Interlaced Content erzwingt immer Transcode, egal wie kompatibel der Codec sonst ist.
func TestDecide_Interlaced(t *testing.T) {
	it := mkItem("mp4", "h264", "aac", 576, 4000)
	it.Streams = []model.ItemStream{
		{Type: "video", FieldOrder: "tt"}, // top-field-first → interlaced
	}
	got := Decide(it)
	if got.Mode != ModeTranscode {
		t.Errorf("Interlaced Item: Mode = %s, want %s", got.Mode, ModeTranscode)
	}
	if got.Reason == "" {
		t.Errorf("Reason fehlt — sollte 'Interlaced' enthalten")
	}
}

// IsInterlaced: progressive/unknown/leer → false; tt/bb/tb/bt → true.
func TestIsInterlaced(t *testing.T) {
	tests := []struct {
		name       string
		fieldOrder string
		want       bool
	}{
		{"progressive", "progressive", false},
		{"unknown", "unknown", false},
		{"leer", "", false},
		{"tt", "tt", true},
		{"bb", "bb", true},
		{"tb", "tb", true},
		{"bt", "bt", true},
		{"groß", "TT", true},
		{"trim", "  tt  ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			it := &model.Item{Streams: []model.ItemStream{
				{Type: "video", FieldOrder: tt.fieldOrder},
			}}
			if got := IsInterlaced(it); got != tt.want {
				t.Errorf("IsInterlaced(%q) = %v, want %v", tt.fieldOrder, got, tt.want)
			}
		})
	}
}

// IsInterlaced ignoriert Audio-Streams.
func TestIsInterlaced_AudioStreamIgnored(t *testing.T) {
	it := &model.Item{Streams: []model.ItemStream{
		{Type: "audio", FieldOrder: "tt"}, // tt bei Audio darf NICHT zu interlaced fuehren
		{Type: "video", FieldOrder: "progressive"},
	}}
	if IsInterlaced(it) {
		t.Errorf("Audio-Stream mit FieldOrder='tt' sollte nicht als interlaced gelten")
	}
}

// IsInterlaced bei nil-Item ist sicher.
func TestIsInterlaced_Nil(t *testing.T) {
	if IsInterlaced(nil) {
		t.Errorf("IsInterlaced(nil) = true, want false")
	}
}

// DecideWithCap: Profil ohne Cap (orig) verhaelt sich wie Decide.
func TestDecideWithCap_OrigPasses(t *testing.T) {
	it := mkItem("mp4", "h264", "aac", 2160, 30000)
	cap := Profile{ID: "orig", MaxHeight: 0, VideoKbps: 0}
	got := DecideWithCap(it, cap)
	if got.Mode != ModeDirectPlay {
		t.Errorf("orig-Profil + browser-kompatibel: got %s, want Direct Play", got.Mode)
	}
}

// DecideWithCap: Profil-Limit (Hoehe) erzwingt Transcode auch bei sonst kompatiblen Items.
func TestDecideWithCap_HeightCap(t *testing.T) {
	// 4K-Item, browser-kompatibler Codec
	it := mkItem("mp4", "h264", "aac", 2160, 30000)
	// 1080p-Cap → Item ueberschreitet → Transcode
	cap := Profile{ID: "1080p", MaxHeight: 1080, VideoKbps: 5000}
	got := DecideWithCap(it, cap)
	if got.Mode != ModeTranscode {
		t.Errorf("4K + 1080p-Cap: got %s, want Transcode", got.Mode)
	}
}

// DecideWithCap: Profil-Limit (Bitrate) erzwingt Transcode.
func TestDecideWithCap_BitrateCap(t *testing.T) {
	// 1080p-Item mit hoher Bitrate
	it := mkItem("mp4", "h264", "aac", 1080, 15000)
	// 1080p-low-Cap (3 Mbps) → Bitrate ueberschreitet → Transcode
	cap := Profile{ID: "1080p-lq", MaxHeight: 1080, VideoKbps: 3000}
	got := DecideWithCap(it, cap)
	if got.Mode != ModeTranscode {
		t.Errorf("1080p+15Mbps + 3Mbps-Cap: got %s, want Transcode", got.Mode)
	}
}

// DecideWithCap: Item passt unter den Cap → normaler Flow (Direct Play wenn kompatibel).
func TestDecideWithCap_UnderCap(t *testing.T) {
	it := mkItem("mp4", "h264", "aac", 720, 1500)
	cap := Profile{ID: "1080p", MaxHeight: 1080, VideoKbps: 5000}
	got := DecideWithCap(it, cap)
	if got.Mode != ModeDirectPlay {
		t.Errorf("720p+1.5Mbps unter 1080p+5Mbps-Cap: got %s, want Direct Play", got.Mode)
	}
}

// DecideWithCap mit Profil ohne MaxHeight aber mit VideoKbps: nur Bitrate-Check.
func TestDecideWithCap_BitrateOnly(t *testing.T) {
	cap := Profile{ID: "test", MaxHeight: 0, VideoKbps: 2000}
	// Hohe Bitrate → Transcode trotz fehlender Hoehen-Begrenzung
	it := mkItem("mp4", "h264", "aac", 4320, 5000)
	if got := DecideWithCap(it, cap); got.Mode != ModeTranscode {
		t.Errorf("Bitrate-Cap erzwingt Transcode")
	}
	// Niedrige Bitrate → Direct Play
	it.BitrateKbps = 1500
	if got := DecideWithCap(it, cap); got.Mode != ModeDirectPlay {
		t.Errorf("Bitrate unter Cap: Direct Play erwartet")
	}
}

// ProfileByID: existierendes ID gibt das Profil; unbekanntes faellt auf orig zurueck.
func TestProfileByID(t *testing.T) {
	if p := ProfileByID("1080p"); p.ID != "1080p" {
		t.Errorf("ProfileByID(1080p) = %s, want 1080p", p.ID)
	}
	if p := ProfileByID(""); p.ID != "orig" {
		t.Errorf("ProfileByID('') = %s, want orig (Fallback)", p.ID)
	}
	if p := ProfileByID("does-not-exist"); p.ID != "orig" {
		t.Errorf("ProfileByID(unknown) = %s, want orig (Fallback)", p.ID)
	}
}
