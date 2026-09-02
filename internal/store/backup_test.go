package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackupAndRestore(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.CreateLibrary("Filme", t.TempDir(), "movies"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("admin", "pw123456", true); err != nil {
		t.Fatal(err)
	}

	backupPath := filepath.Join(t.TempDir(), "backup.db")
	if err := s.BackupToFile(backupPath); err != nil {
		t.Fatalf("BackupToFile: %v", err)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	if err := ValidateBackupFile(backupPath); err != nil {
		t.Fatalf("ValidateBackupFile rejected a valid backup: %v", err)
	}

	// Eine leere/kaputte Datei muss abgelehnt werden.
	garbagePath := filepath.Join(t.TempDir(), "garbage.db")
	if err := os.WriteFile(garbagePath, []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBackupFile(garbagePath); err == nil {
		t.Fatal("expected ValidateBackupFile to reject a non-SQLite file")
	}

	// Restore: neuen Store mit anderer Ausgangs-DB anlegen, dann mit dem
	// obigen Backup überschreiben — danach muss die Bibliothek "Filme" da sein.
	restoreTarget, err := Open(filepath.Join(t.TempDir(), "restore_target.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restoreTarget.CreateLibrary("Alte Lib", t.TempDir(), "private"); err != nil {
		t.Fatal(err)
	}

	// RestoreFromFile erwartet die eingehende Datei im selben Verzeichnis wie
	// die Ziel-DB (os.Rename). Backup dorthin kopieren.
	incomingPath := restoreTarget.Path() + ".incoming"
	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(incomingPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	safetyPath, err := restoreTarget.RestoreFromFile(incomingPath)
	if err != nil {
		t.Fatalf("RestoreFromFile: %v", err)
	}
	if _, err := os.Stat(safetyPath); err != nil {
		t.Fatalf("safety backup missing: %v", err)
	}

	// Nach dem Restore ist restoreTarget.db (Filesystem) jetzt die Backup-
	// Datei — im echten Betrieb würde der Prozess neu starten und Open() erneut
	// aufgerufen. Hier simulieren wir das direkt.
	reopened, err := Open(restoreTarget.Path())
	if err != nil {
		t.Fatalf("reopen after restore: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	libs, err := reopened.ListLibraries()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range libs {
		if l.Name == "Filme" {
			found = true
		}
		if l.Name == "Alte Lib" {
			t.Error("alte Bibliothek aus der überschriebenen DB sollte nach Restore weg sein")
		}
	}
	if !found {
		t.Error("Bibliothek aus dem Backup fehlt nach Restore")
	}
}
