package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
	"github.com/go-chi/chi/v5"
)

// nextEpisodeTestServer baut einen Server mit Store + einem Nutzer, dessen
// Rechte der Test vorgibt. Bewusst OHNE Router/Auth-Middleware: geprüft wird
// der Handler selbst (`s.nextEpisode`), der User kommt so in den Context wie
// ihn sonst die Middleware setzt (withUser).
func nextEpisodeTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "next_episode_api.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &Server{Store: st}, st
}

// callNextEpisode ruft den Handler wie der Router auf: mit chi-Pfadparameter
// {id} und dem Nutzer im Context.
func callNextEpisode(t *testing.T, s *Server, user *model.User, itemID int64) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/items/1/next-episode", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", itoa(itemID))
	ctx := withUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx), user)
	rec := httptest.NewRecorder()
	s.nextEpisode(rec, req.WithContext(ctx))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestNextEpisodeHandlerRespectsLibraryACL ist der Isolationstest zu
// "Nächste Folge automatisch starten": eine Serie kann über dasselbe
// Serien-Metadaten-Objekt Episoden in mehreren Bibliotheken haben (Auto-Merge
// doppelter Serien-Ordner), und der Store liefert sie bewusst ALLE —
// gefiltert wird erst hier. Dieser Test hält fest, dass ein Nutzer ohne
// Zugriff auf die zweite Bibliothek niemals deren Episoden als "nächste
// Folge" zurückbekommt, sondern die nächste, die ER sehen darf.
func TestNextEpisodeHandlerRespectsLibraryACL(t *testing.T) {
	s, st := nextEpisodeTestServer(t)

	libAllowed, err := st.CreateLibrary("Erlaubt", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}
	libForbidden, err := st.CreateLibrary("Fremd", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}

	// Nutzer OHNE Adminrechte, nur auf libAllowed freigegeben.
	uid, err := st.CreateUser("kind", "pw123456", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserLibraryAccess(uid, []int64{libAllowed}); err != nil {
		t.Fatal(err)
	}
	user, err := st.GetUser(uid)
	if err != nil {
		t.Fatal(err)
	}

	showID, err := st.UpsertMetadata(&model.Metadata{TMDBType: "tv", TMDBID: 4242, Title: "Serie"})
	if err != nil {
		t.Fatal(err)
	}
	addEp := func(libID int64, rel string, season, episode int) int64 {
		t.Helper()
		it := &model.Item{LibraryID: libID, Path: t.TempDir() + "/" + rel, RelPath: rel, Title: rel}
		if err := st.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		metaID, err := st.UpsertMetadata(&model.Metadata{
			TMDBType: "episode", TMDBID: int64(5000 + season*100 + episode),
			ParentID: showID, Title: rel, Season: season, Episode: episode,
		})
		if err != nil {
			t.Fatal(err)
		}
		id, err := st.ItemIDByPath(it.Path)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetItemMetadata(id, metaID); err != nil {
			t.Fatal(err)
		}
		return id
	}

	current := addEp(libAllowed, "S01E01.mkv", 1, 1)
	// Die direkt folgende Folge liegt in einer Bibliothek OHNE Freigabe …
	forbidden := addEp(libForbidden, "S01E02.mkv", 1, 2)
	// … die darauffolgende wieder in der erlaubten.
	allowed := addEp(libAllowed, "S01E03.mkv", 1, 3)

	code, body := callNextEpisode(t, s, user, current)
	if code != 200 {
		t.Fatalf("Status = %d, want 200 (Body: %v)", code, body)
	}
	next, ok := body["next"].(map[string]any)
	if !ok {
		t.Fatalf("kein Item in der Antwort: %v", body)
	}
	if gotID := int64(next["id"].(float64)); gotID == forbidden || gotID != allowed {
		t.Fatalf("nächste Folge = %v — erwartet die erlaubte Folge %d, NICHT die gesperrte %d (Body: %v)",
			next["id"], allowed, forbidden, body)
	}

	// Gegenprobe Admin: derselbe Aufruf darf die zweite Bibliothek sehen —
	// der Filter ist eine ACL-Entscheidung, keine generelle Sperre.
	adminID, err := st.CreateUser("chef", "pw123456", true)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := st.GetUser(adminID)
	if err != nil {
		t.Fatal(err)
	}
	code, body = callNextEpisode(t, s, admin, current)
	if code != 200 {
		t.Fatalf("Admin-Status = %d, want 200", code)
	}
	next, ok = body["next"].(map[string]any)
	if !ok {
		t.Fatalf("Admin: kein Item in der Antwort: %v", body)
	}
	if gotID := int64(next["id"].(float64)); gotID != forbidden {
		t.Fatalf("Admin bekam %v — erwartet die direkt folgende Folge %d", next["id"], forbidden)
	}
}

// TestNextEpisodeHandlerRespectsAgeRating: dieselbe Prüfung für die
// Altersfreigabe — eine FSK-18-Folge darf einem Nutzer mit Limit 12 nicht als
// "nächste Folge" angedient werden (sonst wäre der Autoplay-Modus ein Weg an
// der FSK-Sperre vorbei).
func TestNextEpisodeHandlerRespectsAgeRating(t *testing.T) {
	s, st := nextEpisodeTestServer(t)

	lib, err := st.CreateLibrary("Serien", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := st.CreateUser("kind", "pw123456", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserLibraryAccess(uid, []int64{lib}); err != nil {
		t.Fatal(err)
	}
	limit := 12
	if err := st.SetUserMaxAgeRating(uid, &limit); err != nil {
		t.Fatal(err)
	}
	user, err := st.GetUser(uid)
	if err != nil {
		t.Fatal(err)
	}
	if user.MaxAgeRating == nil || *user.MaxAgeRating != 12 {
		t.Fatalf("Testaufbau: MaxAgeRating = %v, want 12", user.MaxAgeRating)
	}

	showID, _ := st.UpsertMetadata(&model.Metadata{TMDBType: "tv", TMDBID: 4242, Title: "Serie", AgeRating: "16"})
	addEp := func(rel string, season, episode int) int64 {
		t.Helper()
		it := &model.Item{LibraryID: lib, Path: t.TempDir() + "/" + rel, RelPath: rel, Title: rel}
		if err := st.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		metaID, _ := st.UpsertMetadata(&model.Metadata{
			TMDBType: "episode", TMDBID: int64(6000 + season*100 + episode),
			ParentID: showID, Title: rel, Season: season, Episode: episode,
		})
		id, err := st.ItemIDByPath(it.Path)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetItemMetadata(id, metaID); err != nil {
			t.Fatal(err)
		}
		return id
	}

	current := addEp("S01E01.mkv", 1, 1)
	_ = addEp("S01E02.mkv", 1, 2) // FSK 16 über das Eltern-Metadaten-Objekt → gesperrt

	code, body := callNextEpisode(t, s, user, current)
	if code != 200 {
		t.Fatalf("Status = %d, want 200 (Body: %v)", code, body)
	}
	if body["next"] != nil {
		t.Fatalf("FSK-16-Folge wurde einem Nutzer mit Limit 12 angeboten: %v", body)
	}
}
