// Package translate provides a minimal interface for text translation,
// with backends for DeepL and LibreTranslate.
package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Translator translates a block of text to the given target language code ("de", "en", "it").
type Translator interface {
	Translate(ctx context.Context, text, targetLang string) (string, error)
}

// NopTranslator returns the input unchanged. Used when no backend is configured.
type NopTranslator struct{}

func (NopTranslator) Translate(_ context.Context, text, _ string) (string, error) { return text, nil }

// --- DeepL ---

// BatchTranslator ist ein optionales Interface, das Translatoren implementieren
// können, um mehrere Texte in einem einzigen Request zu übersetzen.
// TranslateVTT nutzt es bevorzugt für DeepL/LibreTranslate — single-line wäre
// bei einem 1000-Cue-Film 1000 Round-Trips und triggert Rate-Limits.
type BatchTranslator interface {
	TranslateBatch(ctx context.Context, texts []string, targetLang string) ([]string, error)
}

type DeepLTranslator struct {
	APIKey string
	client *http.Client
}

func NewDeepL(apiKey string) *DeepLTranslator {
	return &DeepLTranslator{APIKey: apiKey, client: &http.Client{Timeout: 30 * time.Second}}
}

func (d *DeepLTranslator) endpoint() string {
	// Free-Keys enden auf ":fx" → api-free, sonst Pro-Endpoint
	if strings.HasSuffix(d.APIKey, ":fx") {
		return "https://api-free.deepl.com/v2/translate"
	}
	return "https://api.deepl.com/v2/translate"
}

func (d *DeepLTranslator) Translate(ctx context.Context, text, targetLang string) (string, error) {
	out, err := d.TranslateBatch(ctx, []string{text}, targetLang)
	if err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "", fmt.Errorf("deepl: empty result")
	}
	return out[0], nil
}

// TranslateBatch übersetzt mehrere Texte in einem Request. DeepL erlaubt das
// nativ — spart 50× Round-Trip bei langen VTTs und reduziert Rate-Limit-Druck.
func (d *DeepLTranslator) TranslateBatch(ctx context.Context, texts []string, targetLang string) ([]string, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]any{
		"text":        texts,
		"target_lang": strings.ToUpper(targetLang),
	})
	req, err := http.NewRequestWithContext(ctx, "POST", d.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "DeepL-Auth-Key "+d.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("DeepL %d: %s", resp.StatusCode, truncateForErr(string(raw)))
	}
	var result struct {
		Translations []struct {
			Text string `json:"text"`
		} `json:"translations"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("deepl: unexpected response: %s", truncateForErr(string(raw)))
	}
	if len(result.Translations) != len(texts) {
		return nil, fmt.Errorf("deepl: expected %d translations, got %d", len(texts), len(result.Translations))
	}
	out := make([]string, len(result.Translations))
	for i, t := range result.Translations {
		out[i] = t.Text
	}
	return out, nil
}

func truncateForErr(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// --- LibreTranslate ---

type LibreTranslator struct {
	BaseURL string
	APIKey  string
	client  *http.Client
}

func NewLibreTranslate(baseURL, apiKey string) *LibreTranslator {
	return &LibreTranslator{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (l *LibreTranslator) Translate(ctx context.Context, text, targetLang string) (string, error) {
	payload := map[string]string{
		"q":      text,
		"source": "en",
		"target": strings.ToLower(targetLang),
		"format": "text",
	}
	if l.APIKey != "" {
		payload["api_key"] = l.APIKey
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", l.BaseURL+"/translate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("libretranslate %d: %s", resp.StatusCode, string(raw))
	}
	var result struct {
		TranslatedText string `json:"translatedText"`
		Error          string `json:"error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("libretranslate: unexpected response: %s", string(raw))
	}
	if result.Error != "" {
		return "", fmt.Errorf("libretranslate: %s", result.Error)
	}
	return result.TranslatedText, nil
}

// TranslateVTT translates the cue text lines in a WebVTT file, leaving
// timestamps and WEBVTT header untouched. Input and output are VTT strings.
//
// Verwendet Batch-Übersetzung wenn der Translator BatchTranslator implementiert
// (DeepL/LibreTranslate) — das spart bei langen VTTs Faktor 50 Round-Trips.
// Einzelne Fehler im Batch (z. B. transientes Rate-Limit) brechen den Job
// NICHT mehr ab — die betroffene Cue bleibt im Original (Englisch). Wäre
// schade um die anderen 99 % schon übersetzten Zeilen.
func TranslateVTT(ctx context.Context, t Translator, vtt, targetLang string) (string, error) {
	lines := strings.Split(vtt, "\n")
	out := make([]string, len(lines))
	// Sammelt Zeilen die übersetzt werden müssen, mit ihren Indizes
	var pendingIdx []int
	var pendingTxt []string

	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		lines[i] = line
		trimmed := strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "WEBVTT") ||
			strings.Contains(line, " --> ") || strings.HasPrefix(line, "NOTE") {
			out[i] = line
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			out[i] = line
			continue
		}
		pendingIdx = append(pendingIdx, i)
		pendingTxt = append(pendingTxt, line)
		out[i] = line // Default: Original behalten falls Übersetzung scheitert
	}

	// Batch-Übersetzung wenn unterstützt; sonst Single-Line-Fallback
	const batchSize = 50
	bt, _ := t.(BatchTranslator)

	for start := 0; start < len(pendingTxt); start += batchSize {
		end := start + batchSize
		if end > len(pendingTxt) {
			end = len(pendingTxt)
		}
		batch := pendingTxt[start:end]
		idxBatch := pendingIdx[start:end]

		if bt != nil {
			translated, err := bt.TranslateBatch(ctx, batch, targetLang)
			if err == nil && len(translated) == len(batch) {
				for j, txt := range translated {
					out[idxBatch[j]] = txt
				}
				continue
			}
			// Batch-Fehler → einzeln versuchen, einzelne Fehler überspringen
		}
		for j, txt := range batch {
			translated, err := t.Translate(ctx, txt, targetLang)
			if err == nil {
				out[idxBatch[j]] = translated
			}
			// bei Fehler: Original bleibt drin (s. oben)
		}
	}
	return strings.Join(out, "\n"), nil
}
