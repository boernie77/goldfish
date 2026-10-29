package api

import (
	"fmt"
	"path"
	"strings"
)

// movePlan: ein Item und der Zielordner (relativ zur Ziel-Bibliothek), in den es soll.
type movePlan struct {
	ID     int64
	Target string
}

// folderMove: ein ganzer Ordner, der mit seiner Unterstruktur verschoben wird.
type folderMove struct {
	Src    string // echter rel_path-Pfad des Ordners
	NewDir string // neuer Ordnerpfad in der Ziel-Bibliothek
}

// planMoves baut aus den gewählten Item-IDs und ganzen Ordnern die Liste
// "Item → Zielordner". Ordner behalten ihren Namen und ihre Unterordner:
// Quelle "Serie" mit "Serie/S1/x.mkv" und Ziel "Archiv" ergibt "Archiv/Serie/S1".
// Angezeigte Pfade (folder_alias) werden zuerst auf echte Pfade zurückgeführt.
func (s *Server) planMoves(libID int64, ids []int64, folders []string, targetFolder string) ([]movePlan, []folderMove, error) {
	targetFolder = strings.Trim(strings.TrimSpace(targetFolder), "/")
	plan := make([]movePlan, 0, len(ids))
	for _, id := range ids {
		plan = append(plan, movePlan{ID: id, Target: targetFolder})
	}
	var moves []folderMove
	for _, raw := range folders {
		src := s.Store.ResolveFolderAlias(libID, strings.Trim(raw, "/"))
		if src == "" {
			continue
		}
		if targetFolder == src || strings.HasPrefix(targetFolder+"/", src+"/") {
			return nil, nil, fmt.Errorf("Zielordner liegt im verschobenen Ordner %q selbst", src)
		}
		items, err := s.Store.ItemsUnderFolder(libID, src)
		if err != nil {
			return nil, nil, err
		}
		parent := ""
		if i := strings.LastIndex(src, "/"); i >= 0 {
			parent = src[:i+1]
		}
		for _, it := range items {
			dir := path.Dir(strings.TrimPrefix(it.RelPath, parent))
			if dir == "." {
				dir = ""
			}
			plan = append(plan, movePlan{ID: it.ID, Target: path.Join(targetFolder, dir)})
		}
		moves = append(moves, folderMove{Src: src, NewDir: path.Join(targetFolder, strings.TrimPrefix(src, parent))})
	}
	return plan, moves, nil
}
