package store

import (
	"path"
	"strings"

	"github.com/boernie77/goldfish/internal/model"
)

// CrossFolderNameDupes liefert Items in `libraryID` (optional auf den Ordner
// `folder` inkl. Unterbaum eingeschränkt), deren Dateiname (case-insensitiv)
// UND Größe (± `tolBytes`) mit mindestens einem Item in einem ANDEREN Ordner
// derselben Library übereinstimmen. Pro Treffer stehen die rel_path(s) der
// „Zwillinge" in `item.DupeOtherPaths`.
//
// Zweck: versehentlich in zwei Ordner kopierte gleiche Dateien finden (typisch:
// ein „aaa"/Sammel-Ordner neben dem eigentlichen Ablageort) — der Nutzer prüft
// die markierten Kacheln und löscht eine der beiden Kopien manuell.
func (s *Store) CrossFolderNameDupes(libraryID, userID int64, isAdmin bool, folder string, tolBytes int64) ([]model.Item, error) {
	if tolBytes <= 0 {
		tolBytes = 2048
	}
	folder = strings.Trim(folder, "/")

	type ent struct {
		id      int64
		relPath string
		size    int64
	}
	rows, err := s.db.Query(`SELECT id, rel_path, size_bytes FROM items WHERE library_id = ? AND size_bytes > 0`, libraryID)
	if err != nil {
		return nil, err
	}
	byName := map[string][]ent{}
	var all []ent
	for rows.Next() {
		var e ent
		if scanErr := rows.Scan(&e.id, &e.relPath, &e.size); scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		k := strings.ToLower(path.Base(e.relPath))
		byName[k] = append(byName[k], e)
		all = append(all, e)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	inScope := func(rel string) bool {
		if folder == "" {
			return true
		}
		return rel == folder || strings.HasPrefix(rel, folder+"/")
	}

	twins := map[int64][]string{}
	for _, e := range all {
		if !inScope(e.relPath) {
			continue
		}
		dir := path.Dir(e.relPath)
		for _, c := range byName[strings.ToLower(path.Base(e.relPath))] {
			if c.id == e.id || path.Dir(c.relPath) == dir {
				continue // sich selbst / gleicher Ordner zählt nicht
			}
			d := e.size - c.size
			if d < 0 {
				d = -d
			}
			if d <= tolBytes {
				twins[e.id] = append(twins[e.id], c.relPath)
			}
		}
	}
	if len(twins) == 0 {
		return nil, nil
	}

	f := ItemFilter{UserID: userID, IsAdmin: isAdmin, LibraryID: libraryID}
	if folder != "" {
		f.Folder = folder
	}
	items, err := s.ListItems(f)
	if err != nil {
		return nil, err
	}
	out := make([]model.Item, 0, len(twins))
	for _, it := range items {
		if paths, ok := twins[it.ID]; ok {
			it.DupeOtherPaths = paths
			out = append(out, it)
		}
	}
	return out, nil
}
