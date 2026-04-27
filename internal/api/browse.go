package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// browseRoot ist die Wurzel, unterhalb der gebrowst werden darf.
// Einschränkung aus Sicherheitsgründen: keine Host-Pfade außerhalb von /media.
const browseRoot = "/media"

type browseEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type browseResponse struct {
	Path    string        `json:"path"`
	Parent  string        `json:"parent,omitempty"`
	Entries []browseEntry `json:"entries"`
}

func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Query().Get("path")
	if reqPath == "" {
		reqPath = browseRoot
	}
	abs, err := safeResolve(reqPath)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		writeError(w, 404, "Pfad nicht gefunden")
		return
	}
	if !info.IsDir() {
		writeError(w, 400, "kein Verzeichnis")
		return
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	out := browseResponse{Path: abs, Entries: []browseEntry{}}
	if abs != browseRoot {
		out.Parent = filepath.Dir(abs)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out.Entries = append(out.Entries, browseEntry{
			Name: e.Name(),
			Path: filepath.Join(abs, e.Name()),
		})
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	writeJSON(w, 200, out)
}

// safeResolve normalisiert einen Pfad und verweigert alles außerhalb von /media.
func safeResolve(p string) (string, error) {
	abs := filepath.Clean(p)
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(browseRoot, abs)
	}
	// Pfad darf nicht aus /media ausbrechen
	rel, err := filepath.Rel(browseRoot, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", errInvalidPath
	}
	return abs, nil
}

var errInvalidPath = &pathError{msg: "Pfad außerhalb des erlaubten Bereichs (" + browseRoot + ")"}

type pathError struct{ msg string }

func (e *pathError) Error() string { return e.msg }
