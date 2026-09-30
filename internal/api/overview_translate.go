package api

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/boernie77/goldfish/internal/translate"
)

// Englische TMDB-Folgenbeschreibungen ins Deutsche übersetzen (seit 1.4.70,
// User-Wunsch 2026-09-30: „warum sind die Folgenbeschreibungen auf englisch?").
// TMDB hat für viele Folgen keine deutsche Beschreibung; der Client greift dann
// auf Englisch zurück (tmdb/client.go). Dieser Hintergrund-Job übersetzt solche
// Texte mit dem in den Untertitel-Einstellungen gewählten Dienst (User-Wahl:
// DeepL) und speichert Übersetzung + Ausgangstext — jeder Text wird nur EINMAL
// übersetzt (UpsertMetadata behält die Übersetzung bei unverändertem Original).

const overviewTranslateInterval = 30 * time.Minute

// RunOverviewTranslator läuft kurz nach dem Start und danach alle 30 Minuten.
func (s *Server) RunOverviewTranslator(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
	}
	s.translateOverviewsOnce(ctx)
	t := time.NewTicker(overviewTranslateInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.translateOverviewsOnce(ctx)
		}
	}
}

// overviewTranslator liefert den konfigurierten Dienst (nil = keiner).
func (s *Server) overviewTranslator() (translate.Translator, string) {
	backend, _ := s.Store.GetSetting("translation_backend", "none")
	switch backend {
	case "deepl":
		if key, _ := s.Store.GetSetting("deepl_api_key", ""); key != "" {
			return translate.NewDeepL(key), "DeepL"
		}
	case "libretranslate":
		if url, _ := s.Store.GetSetting("libretranslate_url", ""); url != "" {
			key, _ := s.Store.GetSetting("libretranslate_key", "")
			return translate.NewLibreTranslate(url, key), "LibreTranslate"
		}
	}
	return nil, ""
}

func (s *Server) translateOverviewsOnce(ctx context.Context) {
	tr, name := s.overviewTranslator()
	if tr == nil {
		return
	}
	translated, checked, chars := 0, 0, 0
	var stopErr error
	for ctx.Err() == nil && stopErr == nil {
		cands, err := s.Store.PendingOverviews(200)
		if err != nil || len(cands) == 0 {
			break
		}
		var batch []int
		for i, c := range cands {
			if looksEnglish(c.Overview) {
				batch = append(batch, i)
			} else {
				_ = s.Store.MarkOverviewChecked(c.ID)
				checked++
			}
		}
		// In Paketen zu 40 übersetzen (DeepL-Batch; LibreTranslate einzeln).
		for start := 0; start < len(batch) && stopErr == nil; start += 40 {
			end := min(start+40, len(batch))
			texts := make([]string, 0, end-start)
			for _, i := range batch[start:end] {
				texts = append(texts, cands[i].Overview)
			}
			out, err := translateBatch(ctx, tr, texts)
			if err != nil {
				// Kontingent erschöpft (DeepL 456) oder Dienst weg: Lauf beenden,
				// nächster Versuch in 30 Minuten setzt genau hier fort.
				stopErr = err
				break
			}
			for k, i := range batch[start:end] {
				if strings.TrimSpace(out[k]) == "" {
					continue
				}
				if err := s.Store.SetTranslatedOverview(cands[i].ID, out[k], cands[i].Overview); err == nil {
					translated++
					chars += len(cands[i].Overview)
				}
			}
		}
		if len(cands) < 200 {
			break
		}
	}
	if translated == 0 && stopErr == nil {
		return
	}
	msg := fmt.Sprintf("%d Folgenbeschreibungen mit %s übersetzt (%d Zeichen), %d bereits deutsch", translated, name, chars, checked)
	if stopErr != nil {
		msg += " — abgebrochen: " + stopErr.Error()
	}
	log.Printf("[translate] %s", msg)
	_ = s.Store.LogActivity(0, "", "job", "overview_translate", msg, "")
}

func translateBatch(ctx context.Context, tr translate.Translator, texts []string) ([]string, error) {
	if bt, ok := tr.(interface {
		TranslateBatch(context.Context, []string, string) ([]string, error)
	}); ok {
		return bt.TranslateBatch(ctx, texts, "de")
	}
	out := make([]string, len(texts))
	for i, t := range texts {
		r, err := tr.Translate(ctx, t, "de")
		if err != nil {
			return nil, err
		}
		out[i] = r
	}
	return out, nil
}

// looksEnglish: grobe Spracherkennung über häufige Funktionswörter. Deutsch
// wird nie übersetzt; im Zweifel (zu kurz, gemischt) ebenfalls nicht.
func looksEnglish(text string) bool {
	en, de := 0, 0
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r == 'ä' || r == 'ö' || r == 'ü' || r == 'ß')
	}) {
		switch w {
		case "the", "and", "of", "to", "is", "his", "her", "with", "when", "who", "their", "they", "has", "was", "for", "that", "a", "an", "in":
			en++
		case "der", "die", "das", "und", "ist", "mit", "sich", "ein", "eine", "nicht", "zu", "den", "von", "im", "auf", "dem", "wird", "sie", "er":
			de++
		}
	}
	return en >= 3 && en > 2*de
}
