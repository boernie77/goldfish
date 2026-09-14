package download

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mkCopy legt eine Cache-Kopie mit gegebenem Datei-Alter an. servedAt < 0 heisst
// "gar kein Sidecar", servedAt == 0 "Sidecar ohne servedAt" (Bestandsdatei).
func mkCopy(t *testing.T, dir, name string, age time.Duration, servedAt int64) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("xxxxxxxxxx"), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
	if servedAt >= 0 {
		writeMeta(p+".json", cacheMeta{SourceModTime: 1, SourceSize: 1, ConvVersion: convVersion, ServedAt: servedAt})
	}
	return p
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestCleanupCacheFristen(t *testing.T) {
	cacheDir := t.TempDir()
	dir := filepath.Join(cacheDir, "downloads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()

	altNieAbgeholt := mkCopy(t, dir, "1.mp4", 5*24*time.Hour, 0)
	neuNieAbgeholt := mkCopy(t, dir, "2.mp4", 2*24*time.Hour, 0)
	langeAbgeholt := mkCopy(t, dir, "3.mp4", 30*24*time.Hour, now-int64((48*time.Hour).Seconds()))
	ebenAbgeholt := mkCopy(t, dir, "4.mp4", 30*24*time.Hour, now-60)
	ohneSidecar := mkCopy(t, dir, "5.mp4", 5*24*time.Hour, -1)
	// Laufende Konvertierung — endet ebenfalls auf .mp4 und ist uralt datiert.
	laufend := mkCopy(t, dir, "6.mp4.tmp.123456.mp4", 90*24*time.Hour, -1)

	removed, freed := CleanupCache(cacheDir)

	for _, c := range []struct {
		path string
		want bool
		why  string
	}{
		{altNieAbgeholt, false, "nie abgeholt + aelter als 3 Tage → muss weg"},
		{neuNieAbgeholt, true, "nie abgeholt, aber erst 2 Tage alt → muss bleiben"},
		{langeAbgeholt, false, "abgeholt, letzter Zugriff 48 h her → muss weg"},
		{ebenAbgeholt, true, "abgeholt vor einer Minute → muss bleiben (laufender Transfer!)"},
		{ohneSidecar, false, "kein Sidecar = nie abgeholt + alt → muss weg"},
		{laufend, true, "laufende .tmp.-Konvertierung → darf NIE angefasst werden"},
	} {
		if got := exists(c.path); got != c.want {
			t.Errorf("%s: existiert=%v, erwartet=%v (%s)", filepath.Base(c.path), got, c.want, c.why)
		}
	}
	if removed != 3 {
		t.Errorf("removed=%d, erwartet 3", removed)
	}
	if freed != 30 {
		t.Errorf("freed=%d, erwartet 30 (3 Dateien à 10 Byte)", freed)
	}
	// Der Sidecar einer geloeschten Kopie darf nicht zurueckbleiben.
	if exists(altNieAbgeholt + ".json") {
		t.Error("Sidecar der geloeschten Kopie blieb liegen")
	}
}

func TestCleanupCacheOhneVerzeichnis(t *testing.T) {
	removed, freed := CleanupCache(t.TempDir()) // kein downloads/-Unterordner
	if removed != 0 || freed != 0 {
		t.Errorf("removed=%d freed=%d, erwartet 0/0", removed, freed)
	}
}

func TestMarkServedSetztUndDrosselt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "7.mp4")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeMeta(p+".json", cacheMeta{SourceModTime: 42, SourceSize: 7, ConvVersion: convVersion})

	MarkServed(p)
	m, ok := readMeta(p + ".json")
	if !ok || m.ServedAt == 0 {
		t.Fatalf("ServedAt wurde nicht gesetzt: %+v", m)
	}
	if m.SourceModTime != 42 || m.SourceSize != 7 || m.ConvVersion != convVersion {
		t.Errorf("MarkServed hat die uebrigen Sidecar-Felder zerstoert: %+v", m)
	}

	// Zweiter Aufruf innerhalb des Drossel-Fensters darf nicht neu schreiben.
	m.ServedAt = time.Now().Unix() - 60
	writeMeta(p+".json", m)
	MarkServed(p)
	after, _ := readMeta(p + ".json")
	if after.ServedAt != m.ServedAt {
		t.Error("MarkServed hat innerhalb des Drossel-Fensters erneut geschrieben")
	}

	// Ohne Sidecar darf MarkServed keinen erfinden.
	q := filepath.Join(dir, "8.mp4")
	if err := os.WriteFile(q, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	MarkServed(q)
	if exists(q + ".json") {
		t.Error("MarkServed hat einen Sidecar ohne Quell-Metadaten erfunden")
	}
}
