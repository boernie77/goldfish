package rename

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Inception", "Inception"},
		{"The Lord: Two Towers", "The Lord Two Towers"},     // Doppelpunkt raus
		{`Path\to\file`, "Pathtofile"},                       // Backslash raus
		{"Question?", "Question"},                            // Fragezeichen raus
		{"  Mad Max  ", "Mad Max"},                           // Whitespace getrimmt
		{"Title.", "Title"},                                  // trailing dot raus
		{"Title ", "Title"},                                  // trailing space raus
		{"a/b\"c|d*e<f>g", "abcdefg"},                        // alle unsafe-chars raus
		{"foo  bar   baz", "foo bar baz"},                    // multiple spaces collapsed
		{"", ""},                                             // leer bleibt leer
		{"<<<>>>", ""},                                       // nur unsafe → leer
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := SanitizeFilename(tt.in)
			if got != tt.want {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTargetFilename(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		year        int
		ext         string
		want        string
	}{
		{"Standard", "Inception", 2010, ".mkv", "Inception (2010).mkv"},
		{"Sonderzeichen entfernt", "Spider-Man: No Way Home", 2021, ".mp4", "Spider-Man No Way Home (2021).mp4"},
		{"Year=0 ohne Klammer", "Untitled Project", 0, ".mkv", "Untitled Project.mkv"},
		{"Leerer Titel", "", 2020, ".mkv", ""},
		{"Nur unsafe → leer", "<<<>>>", 2020, ".mkv", ""},
		{"Cinemascope-Title", "1917", 2019, ".mkv", "1917 (2019).mkv"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TargetFilename(tt.title, tt.year, tt.ext)
			if got != tt.want {
				t.Errorf("TargetFilename(%q, %d, %q) = %q, want %q",
					tt.title, tt.year, tt.ext, got, tt.want)
			}
		})
	}
}

// ResolveConflict + RenameOnDisk: echtes Filesystem-Test mit t.TempDir().
func TestResolveConflict_NoConflict(t *testing.T) {
	dir := t.TempDir()
	got := ResolveConflict(dir, "Inception (2010).mkv", "")
	want := filepath.Join(dir, "Inception (2010).mkv")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveConflict_WithConflict(t *testing.T) {
	dir := t.TempDir()
	// Lege existing Datei an.
	must(t, os.WriteFile(filepath.Join(dir, "Inception (2010).mkv"), []byte{}, 0o644))
	got := ResolveConflict(dir, "Inception (2010).mkv", "")
	want := filepath.Join(dir, "Inception (2010) (2).mkv")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveConflict_CurrentPathIsTarget(t *testing.T) {
	// Wenn die existierende Datei IST der Kandidat (currentPath==full), darf
	// es nicht als Konflikt gelten — sonst kreieren wir „ (2)" obwohl die
	// Datei schon den Wunsch-Namen hat.
	dir := t.TempDir()
	current := filepath.Join(dir, "Inception (2010).mkv")
	must(t, os.WriteFile(current, []byte{}, 0o644))
	got := ResolveConflict(dir, "Inception (2010).mkv", current)
	if got != current {
		t.Errorf("got %q, want %q (no rename needed)", got, current)
	}
}

func TestPreviewTarget_NormalCase(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "Inception.2010.1080p.x264.mkv")
	must(t, os.WriteFile(current, []byte{}, 0o644))
	target, alreadyOK, err := PreviewTarget(current, "Inception", 2010)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if alreadyOK {
		t.Errorf("alreadyOK = true, want false (different stem)")
	}
	want := filepath.Join(dir, "Inception (2010).mkv")
	if target != want {
		t.Errorf("target = %q, want %q", target, want)
	}
}

func TestPreviewTarget_AlreadyOK(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "Inception (2010).mkv")
	must(t, os.WriteFile(current, []byte{}, 0o644))
	target, alreadyOK, err := PreviewTarget(current, "Inception", 2010)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !alreadyOK {
		t.Errorf("alreadyOK = false, want true")
	}
	if target != current {
		t.Errorf("target = %q, want %q", target, current)
	}
}

func TestPreviewTarget_EmptyTitleError(t *testing.T) {
	_, _, err := PreviewTarget("/tmp/foo.mkv", "<<<>>>", 2020)
	if err == nil {
		t.Errorf("expected error for empty sanitized title, got nil")
	}
}

func TestRenameOnDisk_Success(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "old.mkv")
	dst := filepath.Join(dir, "new.mkv")
	must(t, os.WriteFile(src, []byte("data"), 0o644))
	if err := RenameOnDisk(src, dst); err != nil {
		t.Fatalf("RenameOnDisk failed: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("Quelldatei sollte nicht mehr existieren")
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("Zieldatei sollte existieren: %v", err)
	}
}

func TestRenameOnDisk_NoOpWhenSamePath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "same.mkv")
	must(t, os.WriteFile(src, []byte{}, 0o644))
	if err := RenameOnDisk(src, src); err != nil {
		t.Errorf("Same path should be no-op, got error: %v", err)
	}
}

func TestRenameOnDisk_TargetExists(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	dst := filepath.Join(dir, "dst.mkv")
	must(t, os.WriteFile(src, []byte{}, 0o644))
	must(t, os.WriteFile(dst, []byte{}, 0o644))
	if err := RenameOnDisk(src, dst); err == nil {
		t.Errorf("Erwarte Fehler weil Ziel existiert")
	}
}

func TestRenameOnDisk_SourceMissing(t *testing.T) {
	dir := t.TempDir()
	if err := RenameOnDisk(filepath.Join(dir, "nope"), filepath.Join(dir, "x")); err == nil {
		t.Errorf("Erwarte Fehler weil Quelle fehlt")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
