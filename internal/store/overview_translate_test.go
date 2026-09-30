package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

// TestTranslatedOverviewSurvivesReEnrich: Eine übersetzte Beschreibung bleibt
// beim erneuten TMDB-Upsert mit demselben englischen Text erhalten (keine
// zweite DeepL-Anfrage); ändert TMDB den Text, gilt der neue und wird wieder
// als ungeprüft markiert.
func TestTranslatedOverviewSurvivesReEnrich(t *testing.T) {
	s := newTestStore(t)
	m := &model.Metadata{TMDBType: "episode", TMDBID: 777, Season: 1, Episode: 1, Title: "X", Overview: "The inspector arrives."}
	id, err := s.UpsertMetadata(m)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := s.PendingOverviews(10); len(p) != 1 || p[0].ID != id {
		t.Fatalf("erwartet 1 offene Beschreibung, bekam %+v", p)
	}
	if err := s.SetTranslatedOverview(id, "Der Kommissar kommt an.", "The inspector arrives."); err != nil {
		t.Fatal(err)
	}
	get := func() string { mm, _ := s.GetMetadata(id); return mm.Overview }

	if _, err := s.UpsertMetadata(&model.Metadata{TMDBType: "episode", TMDBID: 777, Season: 1, Episode: 1, Title: "X", Overview: "The inspector arrives."}); err != nil {
		t.Fatal(err)
	}
	if got := get(); got != "Der Kommissar kommt an." {
		t.Errorf("Übersetzung überschrieben: %q", got)
	}
	if p, _ := s.PendingOverviews(10); len(p) != 0 {
		t.Errorf("nach unverändertem Upsert nichts offen erwartet, bekam %+v", p)
	}

	if _, err := s.UpsertMetadata(&model.Metadata{TMDBType: "episode", TMDBID: 777, Season: 1, Episode: 1, Title: "X", Overview: "The inspector leaves."}); err != nil {
		t.Fatal(err)
	}
	if got := get(); got != "The inspector leaves." {
		t.Errorf("neuer TMDB-Text nicht übernommen: %q", got)
	}
	if p, _ := s.PendingOverviews(10); len(p) != 1 {
		t.Errorf("geänderter Text muss wieder offen sein, bekam %+v", p)
	}
}
