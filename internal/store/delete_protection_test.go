package store

import (
	"testing"

	"github.com/boernie77/goldfish/internal/model"
)

func TestDeleteProtection(t *testing.T) {
	s := newTestStore(t)
	lib, err := s.CreateLibrary("L", t.TempDir(), model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateLibrary("O", t.TempDir(), model.KindPrivate)
	if err != nil {
		t.Fatal(err)
	}
	check := func(libID int64, rel string, want bool) {
		t.Helper()
		got, _, err := s.DeleteProtected(libID, rel)
		if err != nil || got != want {
			t.Fatalf("DeleteProtected(%d,%q)=%v,%v want %v", libID, rel, got, err, want)
		}
	}
	check(lib, "Kanal/a.mp4", false)
	if err := s.SetDeleteProtection(lib, "Kanal", true); err != nil {
		t.Fatal(err)
	}
	check(lib, "Kanal/a.mp4", true)
	check(lib, "Kanal/sub/b.mp4", true)
	check(lib, "Kanal2/c.mp4", false) // Präfix-Falle
	check(other, "Kanal/a.mp4", false)
	_ = s.SetDeleteProtection(lib, "Kanal", false)
	check(lib, "Kanal/a.mp4", false)
	_ = s.SetDeleteProtection(lib, "", true)
	check(lib, "irgendwas/x.mp4", true)
}
