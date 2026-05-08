package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/boernie77/goldfish/internal/store"
	"github.com/boernie77/goldfish/internal/translate"
	"github.com/boernie77/goldfish/internal/whisper"
)

var validLangs = map[string]bool{"de": true, "en": true, "it": true}

// generateSubtitle: POST /api/items/{id}/generate-subtitle (admin-only, via router middleware)
// Body: {"language":"de"|"en"|"it"}
func (s *Server) generateSubtitle(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	var body struct {
		Language string `json:"language"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !validLangs[body.Language] {
		writeError(w, 400, "language muss 'de', 'en' oder 'it' sein")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil || it == nil {
		writeError(w, 404, "Item nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	if s.Whisper == nil {
		writeError(w, 503, "Whisper-Worker nicht initialisiert")
		return
	}
	if err := s.Store.UpsertSubtitleJob(id, body.Language); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.Whisper.Trigger()
	writeJSON(w, 202, map[string]string{"status": "queued"})
}

// subtitleJobs: GET /api/items/{id}/subtitle-jobs
func (s *Server) subtitleJobs(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil || it == nil {
		writeError(w, 404, "Item nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	jobs, err := s.Store.ListItemSubtitles(id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if len(jobs) == 0 {
		jobs = []store.SubtitleJob{}
	}
	writeJSON(w, 200, jobs)
}

// deleteSubtitle: DELETE /api/items/{id}/subtitle/{lang} (admin-only)
func (s *Server) deleteSubtitle(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	lang := chi.URLParam(r, "lang")
	if !validLangs[lang] {
		writeError(w, 400, "ungültige Sprache")
		return
	}
	if err := s.Store.DeleteSubtitleJob(id, lang); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	vttPath := whisper.VTTPath(s.ConfigDir, id, lang)
	_ = os.Remove(vttPath)
	dir := filepath.Dir(vttPath)
	if entries, _ := os.ReadDir(dir); len(entries) == 0 {
		_ = os.Remove(dir)
	}
	w.WriteHeader(http.StatusNoContent)
}

// serveGeneratedSubtitle: GET /api/generated-subtitle/{id}/{lang}.vtt
func (s *Server) serveGeneratedSubtitle(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeError(w, 400, "ungültige id")
		return
	}
	lang := chi.URLParam(r, "lang")
	if !validLangs[lang] {
		writeError(w, 400, "ungültige Sprache")
		return
	}
	it, err := s.Store.GetItem(id)
	if err != nil || it == nil {
		writeError(w, 404, "Item nicht gefunden")
		return
	}
	if !s.requireLibAccess(w, r, it.LibraryID) {
		return
	}
	vttPath := whisper.VTTPath(s.ConfigDir, id, lang)
	f, err := os.Open(vttPath)
	if err != nil {
		writeError(w, 404, "Untertitel nicht gefunden")
		return
	}
	defer func() { _ = f.Close() }()
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, vttPath, info.ModTime(), f)
}

// whisperStatus: GET /api/whisper/status
func (s *Server) whisperStatus(w http.ResponseWriter, r *http.Request) {
	if s.Whisper == nil {
		writeJSON(w, 200, map[string]any{"running": false, "queue": 0, "modelAvailable": false})
		return
	}
	st := s.Whisper.Status()
	queue, _ := s.Store.CountPendingSubtitleJobs()
	modelName, _ := s.Store.GetSetting("whisper_model", "ggml-small")
	writeJSON(w, 200, map[string]any{
		"running":        st.Running,
		"queue":          queue,
		"currentItemId":  st.CurrentItemID,
		"currentLang":    st.CurrentLang,
		"currentTitle":   st.CurrentTitle,
		"modelAvailable": s.Whisper.IsModelAvailable(),
		"modelName":      modelName,
	})
}

// whisperGetSettings: GET /api/whisper/settings (admin-only)
//
// Achtung: API-Keys werden NICHT zurückgegeben (auch nicht maskiert!) — wenn
// das Frontend den maskierten Wert beim nächsten Save zurückschreibt, würde
// der echte Key in der DB durch die Maske überschrieben (klassischer Mask-
// Save-Roundtrip-Bug). Stattdessen reicht eine bool, ob ein Key gespeichert
// ist; das UI zeigt einen Placeholder "(gespeichert)" plus leeres Feld zum
// Überschreiben.
func (s *Server) whisperGetSettings(w http.ResponseWriter, r *http.Request) {
	backend, _ := s.Store.GetSetting("translation_backend", "none")
	deeplKey, _ := s.Store.GetSetting("deepl_api_key", "")
	libreURL, _ := s.Store.GetSetting("libretranslate_url", "")
	libreKey, _ := s.Store.GetSetting("libretranslate_key", "")
	modelName, _ := s.Store.GetSetting("whisper_model", "ggml-small")
	avail := s.Whisper != nil && s.Whisper.IsModelAvailable()
	writeJSON(w, 200, map[string]any{
		"backend":          backend,
		"deeplKeySet":      deeplKey != "",
		"libreUrl":         libreURL,
		"libreKeySet":      libreKey != "",
		"modelName":        modelName,
		"modelAvailable":   avail,
	})
}

// whisperSaveSettings: PUT /api/whisper/settings (admin-only)
func (s *Server) whisperSaveSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Backend  string `json:"backend"`
		DeepLKey string `json:"deeplKey"`
		LibreURL string `json:"libreUrl"`
		LibreKey string `json:"libreKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "ungültiger Body")
		return
	}
	allowed := map[string]bool{"none": true, "deepl": true, "libretranslate": true}
	if !allowed[body.Backend] {
		writeError(w, 400, "backend muss 'none', 'deepl' oder 'libretranslate' sein")
		return
	}
	_ = s.Store.SetSetting("translation_backend", body.Backend)
	_ = s.Store.SetSetting("libretranslate_url", body.LibreURL)
	// API-Keys nur überschreiben wenn das Feld nicht leer ist UND kein
	// Maskierungs-Sentinel enthält (defensiv falls die UI doch noch was
	// Maskiertes hochreicht). Sonst behalten wir den existierenden DB-Wert.
	if k := strings.TrimSpace(body.DeepLKey); k != "" && !strings.Contains(k, "…") && !strings.Contains(k, "***") {
		_ = s.Store.SetSetting("deepl_api_key", k)
	}
	if k := strings.TrimSpace(body.LibreKey); k != "" && !strings.Contains(k, "…") && !strings.Contains(k, "***") {
		_ = s.Store.SetSetting("libretranslate_key", k)
	}
	s.ApplyTranslationBackend()
	w.WriteHeader(http.StatusNoContent)
}

// ApplyTranslationBackend liest die Settings und konfiguriert den Whisper-Worker-Translator.
// Wird beim App-Start und nach Settings-Änderungen aufgerufen.
func (s *Server) ApplyTranslationBackend() {
	if s.Whisper == nil {
		return
	}
	backend, _ := s.Store.GetSetting("translation_backend", "none")
	switch backend {
	case "deepl":
		key, _ := s.Store.GetSetting("deepl_api_key", "")
		if key != "" {
			s.Whisper.SetTranslator(translate.NewDeepL(key))
			return
		}
	case "libretranslate":
		url, _ := s.Store.GetSetting("libretranslate_url", "")
		libreKey, _ := s.Store.GetSetting("libretranslate_key", "")
		if url != "" {
			s.Whisper.SetTranslator(translate.NewLibreTranslate(url, libreKey))
			return
		}
	}
	s.Whisper.SetTranslator(translate.NopTranslator{})
}

// --- Modell-Download ---

var (
	dlMu       sync.Mutex
	dlProgress int
	dlDone     bool
	dlErr      string
	dlActive   bool
)

// whisperDownloadModel: POST /api/whisper/download-model (admin-only)
// Body: {"model":"ggml-tiny"|"ggml-base"|"ggml-small"|"ggml-medium"}
func (s *Server) whisperDownloadModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model == "" {
		writeError(w, 400, "model erforderlich")
		return
	}
	allowed := map[string]bool{"ggml-tiny": true, "ggml-base": true, "ggml-small": true, "ggml-medium": true}
	if !allowed[body.Model] {
		writeError(w, 400, "unbekanntes Modell")
		return
	}
	dlMu.Lock()
	if dlActive {
		dlMu.Unlock()
		writeError(w, 409, "Download läuft bereits")
		return
	}
	dlActive = true
	dlProgress = 0
	dlDone = false
	dlErr = ""
	dlMu.Unlock()

	ctx := s.bgCtx
	ch, err := whisper.DownloadModel(ctx, s.ConfigDir, body.Model)
	if err != nil {
		dlMu.Lock()
		dlActive = false
		dlMu.Unlock()
		writeError(w, 500, err.Error())
		return
	}
	modelName := body.Model
	go func() {
		for p := range ch {
			dlMu.Lock()
			if p == -1 {
				dlErr = "Download fehlgeschlagen"
				dlActive = false
			} else {
				dlProgress = p
				if p == 100 {
					dlDone = true
					dlActive = false
					_ = s.Store.SetSetting("whisper_model", modelName)
				}
			}
			dlMu.Unlock()
		}
	}()
	writeJSON(w, 202, map[string]string{"status": "downloading"})
}

// whisperDownloadStatus: GET /api/whisper/download-status
func (s *Server) whisperDownloadStatus(w http.ResponseWriter, _ *http.Request) {
	dlMu.Lock()
	defer dlMu.Unlock()
	writeJSON(w, 200, map[string]any{
		"downloading": dlActive,
		"progress":    dlProgress,
		"done":        dlDone,
		"error":       dlErr,
		"ts":          time.Now().Unix(),
	})
}

func maskKey(k string) string {
	if len(k) <= 8 {
		return k
	}
	return k[:4] + "…" + k[len(k)-4:]
}

func whisperLangLabel(code string) string {
	switch code {
	case "de":
		return "Deutsch"
	case "en":
		return "Englisch"
	case "it":
		return "Italienisch"
	}
	return code
}

func whisperSubStreamLabel(lang string) string {
	return fmt.Sprintf("🎤 %s (KI)", whisperLangLabel(lang))
}
