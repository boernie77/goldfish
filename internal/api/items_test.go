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

// TestListItemsSearchModeFuzzy: End-to-Ende-Probe über den echten Router für
// die FTS5-Suche + den neuen searchMode=fuzzy-Parameter samt
// X-Fuzzy-Extra-Count-Header (siehe listItems in items.go).
func TestListItemsSearchModeFuzzy(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "search_fuzzy.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.CreateUser("christian", "pw123456", true); err != nil {
		t.Fatal(err)
	}
	lib, err := st.CreateLibrary("Filme", t.TempDir(), model.KindMovies)
	if err != nil {
		t.Fatal(err)
	}
	addMovie := func(tmdbID int64, rel, title string) int64 {
		t.Helper()
		it := &model.Item{LibraryID: lib, Path: t.TempDir() + "/" + rel, RelPath: rel, Title: title}
		if err := st.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
		id, err := st.ItemIDByPath(it.Path)
		if err != nil {
			t.Fatal(err)
		}
		metaID, err := st.UpsertMetadata(&model.Metadata{TMDBType: "movie", TMDBID: tmdbID, Title: title})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetItemMetadata(id, metaID); err != nil {
			t.Fatal(err)
		}
		return id
	}
	exact := addMovie(1, "a.mkv", "Star Wars")
	prefixOnly := addMovie(2, "b.mkv", "Starship Troopers")
	_ = addMovie(3, "c.mkv", "Der Pate")

	srv := &Server{Store: st, WebFS: os.DirFS(t.TempDir())}
	router := srv.Router()
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	// Login übers echte Endpoint, Cookie fürs Weitere merken.
	body, _ := json.Marshal(map[string]string{"username": "christian", "password": "pw123456"})
	res, err := http.Post(ts.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
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
	get := func(path string) (int, []map[string]any, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.AddCookie(cookie)
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Body.Close() }()
		var out []map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out, r.Header
	}

	// 1) Ohne searchMode (exakter Modus, Default): "star" findet "Star Wars",
	//    NICHT "Starship Troopers" — und der Header meldet genau 1 weiteren
	//    Fuzzy-Treffer.
	code, items, hdr := get("/api/items?libraryId=" + itoa(lib) + "&search=star")
	if code != 200 {
		t.Fatalf("Status = %d, want 200", code)
	}
	if !containsItemID(items, exact) {
		t.Fatalf("exakter Modus haette 'Star Wars' finden muessen, items=%v", items)
	}
	if containsItemID(items, prefixOnly) {
		t.Fatalf("exakter Modus haette 'Starship Troopers' NICHT finden duerfen, items=%v", items)
	}
	if got := hdr.Get("X-Fuzzy-Extra-Count"); got != "1" {
		t.Fatalf("X-Fuzzy-Extra-Count = %q, want \"1\"", got)
	}

	// 2) searchMode=fuzzy: liefert zusätzlich "Starship Troopers", und der
	//    Extra-Count-Header wird im Fuzzy-Modus selbst NICHT gesetzt (dort
	//    ist er sinnlos — der Client hat die Erweiterung bereits angefordert).
	code, items, hdr = get("/api/items?libraryId=" + itoa(lib) + "&search=star&searchMode=fuzzy")
	if code != 200 {
		t.Fatalf("Status = %d, want 200", code)
	}
	if !containsItemID(items, exact) || !containsItemID(items, prefixOnly) {
		t.Fatalf("Fuzzy-Modus haette beide Filme finden muessen, items=%v", items)
	}
	if got := hdr.Get("X-Fuzzy-Extra-Count"); got != "" {
		t.Fatalf("X-Fuzzy-Extra-Count im Fuzzy-Modus selbst = %q, want leer", got)
	}

	// 3) Kein Suchbegriff → kein Fuzzy-Header (nichts zu erweitern).
	code, _, hdr = get("/api/items?libraryId=" + itoa(lib))
	if code != 200 {
		t.Fatalf("Status = %d, want 200", code)
	}
	if got := hdr.Get("X-Fuzzy-Extra-Count"); got != "" {
		t.Fatalf("X-Fuzzy-Extra-Count ohne Suchbegriff = %q, want leer", got)
	}

	// 4) Ohne Login: 401 (Middleware greift vor dem Handler).
	res2, err := http.Get(ts.URL + "/api/items?libraryId=" + itoa(lib) + "&search=star")
	if err != nil {
		t.Fatal(err)
	}
	_ = res2.Body.Close()
	if res2.StatusCode != 401 {
		t.Fatalf("ohne Login Status = %d, want 401", res2.StatusCode)
	}
}

func containsItemID(items []map[string]any, id int64) bool {
	for _, it := range items {
		v, ok := it["id"].(float64)
		if ok && int64(v) == id {
			return true
		}
	}
	return false
}
