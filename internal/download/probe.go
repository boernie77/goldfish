package download

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
)

// probeDurationMS liefert die Gesamtlaufzeit der Datei in Millisekunden — für
// die Fortschrittsberechnung der Formatanpassung (ffmpeg -progress out_time_us
// gegen diesen Wert).
func probeDurationMS(ctx context.Context, path string) (int64, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error", "-analyzeduration", "200M", "-probesize", "200M",
		"-show_entries", "format=duration", "-of", "default=nk=1:nw=1", path,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, err
	}
	return int64(f * 1000), nil
}

// probeVideo liefert Codec-Name + Container-Tag (z.B. "hvc1" vs "hev1" bei
// HEVC) des ersten Videostreams.
func probeVideo(ctx context.Context, path string) (codec, tag string, err error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error", "-analyzeduration", "200M", "-probesize", "200M",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name,codec_tag_string",
		"-of", "json", path,
	)
	out, runErr := cmd.Output()
	if runErr != nil {
		return "", "", runErr
	}
	var parsed struct {
		Streams []struct {
			CodecName   string `json:"codec_name"`
			CodecTagStr string `json:"codec_tag_string"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return "", "", err
	}
	if len(parsed.Streams) == 0 {
		return "", "", nil
	}
	return parsed.Streams[0].CodecName, parsed.Streams[0].CodecTagStr, nil
}

// probeAudioStreams liefert JEDEN Audiostream der Quelldatei (Index im
// Container, Codec, Sprachtag) — anders als das einzelne `AudioCodec`-Feld in
// der DB (das nur den ERSTEN Stream kennt), damit beim Remux keine zusätzliche
// Tonspur (z.B. eine zweite Sprache) verlorengeht.
func probeAudioStreams(ctx context.Context, path string) ([]AudioStream, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error", "-analyzeduration", "200M", "-probesize", "200M",
		"-select_streams", "a",
		"-show_entries", "stream=index,codec_name:stream_tags=language",
		"-of", "json", path,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Streams []struct {
			Index     int               `json:"index"`
			CodecName string            `json:"codec_name"`
			Tags      map[string]string `json:"tags"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, err
	}
	streams := make([]AudioStream, 0, len(parsed.Streams))
	for _, s := range parsed.Streams {
		streams = append(streams, AudioStream{
			Index:    s.Index,
			Codec:    s.CodecName,
			Language: s.Tags["language"],
		})
	}
	return streams, nil
}
