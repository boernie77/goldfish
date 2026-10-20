package mediainput

import "testing"

func TestArg(t *testing.T) {
	if got := Arg("/media/Film/a.mkv"); got != "/media/Film/a.mkv" {
		t.Errorf("mkv: %q", got)
	}
	if got := Arg("/media/Film/a.ISO"); got != "bluray:/media/Film/a.ISO" {
		t.Errorf("iso: %q", got)
	}
	if len(ProbeOpts("/x/a.mkv")) != 0 || len(ProbeOpts("/x/a.iso")) == 0 {
		t.Error("ProbeOpts")
	}
}
