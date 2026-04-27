package playback

import (
	"log"
	"os"
	"os/exec"
	"strings"
)

// Backend beschreibt den gewünschten Encoding-Pfad.
type Backend string

const (
	BackendAuto     Backend = "auto"     // Detect: vaapi > nvenc > software
	BackendVAAPI    Backend = "vaapi"    // Intel iGPU / AMD via Mesa
	BackendNVENC    Backend = "nvenc"    // NVIDIA GPU
	BackendSoftware Backend = "software" // libx264
)

// HWAccel beschreibt die erkannten Backends + den aktuell gewählten.
// Der Wert wird beim Start detektiert und anhand der User-Settings auf
// ein konkretes Backend resolved. Die Trickplay/ffmpeg-Runner fragen
// `Selected` ab.
type HWAccel struct {
	// Auto-Auswahl (ohne User-Override): der erste verfügbare Backend in
	// Reihenfolge vaapi > nvenc > software.
	Selected Backend

	// Detektionsergebnisse
	VAAPIAvailable bool
	VAAPIDevice    string // /dev/dri/renderD128
	VAAPIDriver    string // vainfo-Driver-Version-String

	NVENCAvailable bool
	NVENCInfo      string // "NVIDIA … / driver 535.104"

	// Rückwärtskompatibilität für älteren Code-Pfad
	Available bool
	Device    string
	Driver    string
}

// Detect prüft alle bekannten Hardware-Pfade einmalig beim Start.
func Detect() HWAccel {
	h := HWAccel{
		VAAPIDevice: "/dev/dri/renderD128",
	}
	detectVAAPI(&h)
	detectNVENC(&h)

	// Backward-Compat: Available/Device/Driver spiegeln weiterhin VAAPI,
	// damit bestehender Code (der diese Felder direkt liest) unverändert läuft.
	h.Available = h.VAAPIAvailable
	h.Device = h.VAAPIDevice
	h.Driver = h.VAAPIDriver

	// Default-Auswahl: vaapi > nvenc > software. Kann später per Settings
	// überschrieben werden via ApplySelection().
	switch {
	case h.VAAPIAvailable:
		h.Selected = BackendVAAPI
	case h.NVENCAvailable:
		h.Selected = BackendNVENC
	default:
		h.Selected = BackendSoftware
	}
	log.Printf("[hwaccel] detektiert: vaapi=%v nvenc=%v → aktiv: %s",
		h.VAAPIAvailable, h.NVENCAvailable, h.Selected)
	return h
}

// ApplySelection setzt den Backend anhand eines User-Settings-Werts.
// „auto" fällt auf die Detect-Default-Logik zurück. Ein manuell gewähltes
// Backend, das nicht verfügbar ist, wird auf das nächstbeste Fallback
// umgelenkt (und geloggt).
func (h *HWAccel) ApplySelection(mode string) {
	m := Backend(strings.TrimSpace(strings.ToLower(mode)))
	switch m {
	case BackendVAAPI:
		if h.VAAPIAvailable {
			h.Selected = BackendVAAPI
			return
		}
		log.Printf("[hwaccel] VAAPI gewünscht, aber nicht verfügbar — fallback")
	case BackendNVENC:
		if h.NVENCAvailable {
			h.Selected = BackendNVENC
			return
		}
		log.Printf("[hwaccel] NVENC gewünscht, aber nicht verfügbar — fallback")
	case BackendSoftware:
		h.Selected = BackendSoftware
		return
	}
	// auto / ungültig / Fallback
	switch {
	case h.VAAPIAvailable:
		h.Selected = BackendVAAPI
	case h.NVENCAvailable:
		h.Selected = BackendNVENC
	default:
		h.Selected = BackendSoftware
	}
}

func detectVAAPI(h *HWAccel) {
	if _, err := os.Stat(h.VAAPIDevice); err != nil {
		return
	}
	out, err := exec.Command("vainfo", "--display", "drm", "--device", h.VAAPIDevice).CombinedOutput()
	if err != nil {
		return
	}
	text := string(out)
	if !strings.Contains(text, "VAProfileH264") {
		return
	}
	h.VAAPIAvailable = true
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "Driver version") {
			h.VAAPIDriver = strings.TrimSpace(strings.TrimPrefix(line, "vainfo: Driver version:"))
			break
		}
	}
}

func detectNVENC(h *HWAccel) {
	// Kein `/dev/nvidia0` → GPU ist nicht durchgereicht, Runtime/Plugin fehlt.
	// Egal ob ffmpeg nvenc-fähig ist — encoding schlägt zur Laufzeit fehl.
	if _, err := os.Stat("/dev/nvidia0"); err != nil {
		return
	}
	// ffmpeg muss den Encoder überhaupt eingebaut haben.
	out, err := exec.Command("ffmpeg", "-hide_banner", "-encoders").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "h264_nvenc") {
		return
	}
	h.NVENCAvailable = true
	// GPU-Infos via nvidia-smi (falls im Image installiert). Optional.
	if smi, err := exec.Command("nvidia-smi",
		"--query-gpu=name,driver_version", "--format=csv,noheader").CombinedOutput(); err == nil {
		line := strings.TrimSpace(strings.SplitN(string(smi), "\n", 2)[0])
		if line != "" {
			h.NVENCInfo = line
		}
	}
	if h.NVENCInfo == "" {
		h.NVENCInfo = "NVIDIA GPU erkannt"
	}
}
