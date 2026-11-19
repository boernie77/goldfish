package api

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// checkTargetSpace prüft vor dem Verschieben, ob der Zielordner auf einem echten,
// eingehängten Datenträger mit genug Platz liegt. Nicht eingehängte Unraid-
// Datenträger erscheinen im Container als winziges tmpfs (~1 MB) — Schreiben
// dorthin scheitert mittendrin. Liefert "" wenn alles passt, sonst die Meldung.
func checkTargetSpace(newDir string, need int64) string {
	d := newDir
	for {
		if _, err := os.Stat(d); err == nil {
			break
		}
		p := filepath.Dir(d)
		if p == d {
			return ""
		}
		d = p
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(d, &st); err != nil {
		return ""
	}
	bs := uint64(st.Bsize)
	total, avail := uint64(st.Blocks)*bs, uint64(st.Bavail)*bs
	if total < 1<<30 {
		return fmt.Sprintf("Ziel %s ist kein eingehängter Datenträger (nur %d MB groß) — Datenträger nicht gemountet?", d, total>>20)
	}
	if need > 0 && avail < uint64(need) {
		return fmt.Sprintf("Zu wenig Platz am Ziel %s (%d MB frei)", d, avail>>20)
	}
	return ""
}
