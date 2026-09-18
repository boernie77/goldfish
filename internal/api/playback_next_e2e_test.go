package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
)

// TestNextEpisodeEndpointOverHTTP ist die Ende-zu-Ende-Probe der neuen
// Endpunkte durch den ECHTEN Router (chi) samt Auth-Middleware: Login per
// HTTP, dann der Aufruf mit Session-Cookie.
//
// Warum überhaupt: die Handler-Tests in playback_next_test.go rufen die
// Handler direkt auf und würden eine falsch registrierte Route, einen
// chi-Routenkonflikt (/playback/preferences vs. /playback/{id}) oder eine
// fehlende Auth-Freigabe nicht bemerken. Genau diese Klasse von Fehlern
// (Endpoint existiert, aber liefert nichts) ist bei diesem Projekt schon
// vorgekommen.
func TestNextEpisodeEndpointOverHTTP(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	uid, err := st.CreateUser("christian", "pw123456", true)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := st.CreateLibrary("Serien", t.TempDir(), model.KindTV)
	if err != nil {
		t.Fatal(err)
	}
	showID, _ := st.UpsertMetadata(&model.Metadata{TMDBType: "tv", TMDBID: 777, Title: "Testserie"})
	addEp := func(rel string, season, episode int) int64 {
		t.Helper()
		it := &model.Item{LibraryID: lib, Path: t.TempDir() + "/" + rel, RelPath: rel, Title: rel}
		if err := st.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		// TMDB-Titel bewusst ABWEICHEND vom Dateinamen: nur so fällt auf, wenn
		// ein Client den Dateinamen statt des Folgentitels anzeigt
		// (User-Report 2026-09-18).
		metaID, _ := st.UpsertMetadata(&model.Metadata{
			TMDBType: "episode", TMDBID: int64(9000 + season*100 + episode),
			ParentID: showID, Title: tmdbTitle(season, episode), Season: season, Episode: episode,
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
	e1 := addEp("S01E01.mkv", 1, 1)
	e2 := addEp("S01E02.mkv", 1, 2)

	srv := &Server{Store: st, WebFS: os.DirFS(t.TempDir())}
	router := srv.Router()
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	// 1) Ohne Login: 401 (Middleware greift vor dem Handler).
	res, err := http.Get(ts.URL + "/api/items/1/next-episode")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("ohne Login Status = %d, want 401", res.StatusCode)
	}

	// 2) Login über den echten Login-Endpunkt (Cookie holen).
	body, _ := json.Marshal(map[string]string{"username": "christian", "password": "pw123456"})
	res, err = http.Post(ts.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("Login Status = %d, want 200", res.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
		}
	}
	_ = res.Body.Close()
	if cookie == nil {
		t.Fatalf("Login lieferte kein %s-Cookie", sessionCookieName)
	}
	client := &http.Client{}
	get := func(path string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.AddCookie(cookie)
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Body.Close() }()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}

	// 3) Route existiert und liefert die nächste Folge.
	code, out := get("/api/items/" + itoa(e1) + "/next-episode")
	if code != 200 {
		t.Fatalf("next-episode Status = %d, want 200 (Body: %v)", code, out)
	}
	next, ok := out["next"].(map[string]any)
	if !ok {
		t.Fatalf("next fehlt: %v", out)
	}
	if gotID := int64(next["id"].(float64)); gotID != e2 {
		t.Fatalf("next = %v, want %d", next["id"], e2)
	}
	// Anzeigename muss der TMDB-Folgentitel sein, NICHT der Dateiname
	// (item.title ist "S01E02.mkv").
	if got := out["nextTitle"]; got != tmdbTitle(1, 2) {
		t.Fatalf("nextTitle = %v, want %q (Dateiname wäre %q)", got, tmdbTitle(1, 2), "S01E02.mkv")
	}

	// 4) Letzte Folge: 200 + next=null (kein Fehler).
	code, out = get("/api/items/" + itoa(e2) + "/next-episode")
	if code != 200 || out["next"] != nil {
		t.Fatalf("letzte Folge: Status %d, Body %v — erwartet 200 + next=null", code, out)
	}
	if out["nextTitle"] != "" {
		t.Fatalf("nextTitle bei letzter Folge = %v, want leer", out["nextTitle"])
	}

	// 5) Wiedergabe-Präferenzen: Default AUS, dann per PUT einschalten.
	code, out = get("/api/playback/preferences")
	if code != 200 {
		t.Fatalf("preferences GET Status = %d, want 200", code)
	}
	if out["autoplayNext"] != false {
		t.Fatalf("Default autoplayNext = %v, want false", out["autoplayNext"])
	}
	putBody, _ := json.Marshal(map[string]bool{"autoplayNext": true})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/playback/preferences", bytes.NewReader(putBody))
	req.AddCookie(cookie)
	r, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != 204 {
		t.Fatalf("preferences PUT Status = %d, want 204", r.StatusCode)
	}
	// Nachlesen beim Server (nicht nur den lokalen Merker glauben).
	code, out = get("/api/playback/preferences")
	if code != 200 || out["autoplayNext"] != true {
		t.Fatalf("autoplayNext nach PUT = %v (Status %d), want true", out["autoplayNext"], code)
	}

	// 6) Die Einstellung ist PRO KONTO: ein zweiter Nutzer sieht sie nicht.
	uid2, err := st.CreateUser("alex", "pw123456", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserLibraryAccess(uid2, []int64{lib}); err != nil {
		t.Fatal(err)
	}
	body2, _ := json.Marshal(map[string]string{"username": "alex", "password": "pw123456"})
	res, err = http.Post(ts.URL+"/api/auth/login", "application/json", bytes.NewReader(body2))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	var cookie2 *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == sessionCookieName {
			cookie2 = c
		}
	}
	if cookie2 == nil {
		t.Fatal("zweiter Login lieferte kein Cookie")
	}
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/playback/preferences", nil)
	req.AddCookie(cookie2)
	r, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out2 map[string]any
	_ = json.NewDecoder(r.Body).Decode(&out2)
	_ = r.Body.Close()
	if out2["autoplayNext"] != false {
		t.Fatalf("Nutzer alex sieht autoplayNext = %v — Einstellung ist nicht pro Konto getrennt", out2["autoplayNext"])
	}
	_ = uid
}

// tmdbTitle — TMDB-Folgentitel für den Testaufbau. Bewusst verschieden vom
// Dateinamen (der heißt S0xE0y.mkv), damit der Test den Unterschied prüft.
func tmdbTitle(season, episode int) string {
	return "Folgentitel " + itoa(int64(season)) + "x" + itoa(int64(episode))
}
