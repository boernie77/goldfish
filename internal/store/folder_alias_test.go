package store

import "testing"

func TestResolveAlias(t *testing.T) {
	al := []FolderAlias{{Folder: "DaphneDare", Parent: "Siterips"}}
	cases := map[string]string{
		"Siterips/DaphneDare":    "DaphneDare",
		"Siterips/DaphneDare/S1": "DaphneDare/S1",
		"Siterips":               "Siterips",
		"Siterips/DaphneDareX":   "Siterips/DaphneDareX",
		"DaphneDare":             "DaphneDare",
	}
	for in, want := range cases {
		if got := ResolveAlias(al, in); got != want {
			t.Errorf("%q → %q, erwartet %q", in, got, want)
		}
	}
	if v := (FolderAlias{Folder: "a/X", Parent: ""}).Virtual(); v != "X" {
		t.Errorf("Virtual = %q", v)
	}
}
