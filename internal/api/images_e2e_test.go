package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/boernie77/goldfish/internal/model"
	"github.com/boernie77/goldfish/internal/store"
)

// TestImagesACLOverHTTP: Bilder folgen der Bibliotheks-ACL (Mehrbenutzer-Trennung),
// dem images_enabled-Schalter, und ?images=1 verändert die Ordnerliste nur auf Wunsch.
func TestImagesACLOverHTTP(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "img.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateUser("admin", "pw123456", true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser("fremd", "pw123456", false); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	lib, _ := st.CreateLibrary("A", dir, model.KindPrivate)
	imgPath := filepath.Join(dir, "Bilder", "x.png")
	_ = os.MkdirAll(filepath.Dir(imgPath), 0o755)
	_ = os.WriteFile(imgPath, []byte("PNGDATA"), 0o644)
	if err := st.UpsertImage(&model.Image{LibraryID: lib, Path: imgPath, RelPath: "Bilder/x.png", Title: "x", ModTime: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryImagesEnabled(lib, true); err != nil {
		t.Fatal(err)
	}
	imgID := func() int64 { l, _ := st.ListImagesInFolder(lib, "Bilder"); return l[0].ID }()

	srv := &Server{Store: st, WebFS: os.DirFS(t.TempDir()), ConfigDir: t.TempDir()}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)

	login := func(user string) *http.Cookie {
		b, _ := json.Marshal(map[string]string{"username": user, "password": "pw123456"})
		r, err := http.Post(ts.URL+"/api/auth/login", "application/json", bytes.NewReader(b))
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("login %s: %v %v", user, err, r)
		}
		defer func() { _ = r.Body.Close() }()
		for _, c := range r.Cookies() {
			if c.Name == sessionCookieName {
				return c
			}
		}
		t.Fatal("kein Cookie")
		return nil
	}
	get := func(c *http.Cookie, path string) (int, []byte) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.AddCookie(c)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Body.Close() }()
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		return r.StatusCode, buf.Bytes()
	}
	admin, fremd := login("admin"), login("fremd")
	file := "/api/images/" + itoa(imgID) + "/file"

	if code, body := get(admin, file); code != 200 || string(body) != "PNGDATA" {
		t.Fatalf("Admin file: %d %q", code, body)
	}
	if code, _ := get(fremd, file); code != 403 {
		t.Fatalf("ohne Zugriff muss 403 kommen, got %d", code)
	}
	if code, _ := get(fremd, "/api/images/"+itoa(imgID)+"/thumb"); code != 403 {
		t.Fatalf("thumb ohne Zugriff: %d", code)
	}
	if code, _ := get(fremd, "/api/libraries/"+itoa(lib)+"/images?folder=Bilder"); code != 403 {
		t.Fatalf("Liste ohne Zugriff: %d", code)
	}
	var list []map[string]any
	if code, body := get(admin, "/api/libraries/"+itoa(lib)+"/images?folder=Bilder"); code != 200 || json.Unmarshal(body, &list) != nil || len(list) != 1 {
		t.Fatalf("Liste Admin: %d %s", code, body)
	}
	if _, has := list[0]["Path"]; has || list[0]["path"] != nil {
		t.Fatalf("Disk-Pfad darf nicht ausgeliefert werden: %v", list[0])
	}
	// Ordnerliste: ohne ?images=1 unverändert (Apps), mit ?images=1 inkl. Bild-Ordner.
	var folders []map[string]any
	_, body := get(admin, "/api/libraries/"+itoa(lib)+"/folders")
	_ = json.Unmarshal(body, &folders)
	if len(folders) != 0 {
		t.Fatalf("ohne images=1 keine Bild-Ordner erwartet: %s", body)
	}
	_, body = get(admin, "/api/libraries/"+itoa(lib)+"/folders?images=1")
	_ = json.Unmarshal(body, &folders)
	if len(folders) != 1 || folders[0]["name"] != "Bilder" || folders[0]["imageCount"] != float64(1) || folders[0]["drilldown"] != true {
		t.Fatalf("mit images=1: %s", body)
	}
	// Schalter aus -> Bild nicht mehr abrufbar.
	_ = st.SetLibraryImagesEnabled(lib, false)
	if code, _ := get(admin, file); code != 404 {
		t.Fatalf("bei ausgeschaltetem Schalter 404 erwartet, got %d", code)
	}
}
