package store

import (
	"sort"
	"testing"
)

func TestNaturalCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		// Reine Zahl-Vergleiche im String
		{"Folge 2", "Folge 10", -1},
		{"Folge 10", "Folge 2", 1},
		{"Folge 9", "Folge 10", -1},
		{"Episode 1", "Episode 1", 0},
		// Mehrere Zahlblöcke
		{"S01E02", "S01E10", -1},
		{"S02E01", "S10E01", -1},
		// Case-insensitive
		{"folge 2", "Folge 2", 0},
		{"ALEX", "alex", 0},
		// Reines Lexem
		{"Alex", "Bob", -1},
		{"Bob", "Alex", 1},
		// Zahlen < Buchstaben (gleiches Verhalten wie ASCII)
		{"10 Cloverfield Lane", "Alex", -1},
		// Fuehrende Nullen sind irrelevant
		{"007 Bond", "7 Bond", 0},
		{"Track 03", "Track 3", 0},
		// Ein String ist Praefix des anderen
		{"Folge 2", "Folge 2 Extra", -1},
		{"Folge", "Folge 2", -1},
		// Leerstring-Edge
		{"", "x", -1},
		{"x", "", 1},
		{"", "", 0},
	}
	for _, c := range cases {
		got := naturalCompare(c.a, c.b)
		if got != c.want {
			t.Errorf("naturalCompare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNaturalCompareSort(t *testing.T) {
	in := []string{"Folge 10", "Folge 1", "Folge 9", "Folge 2", "Folge 11", "Episode 100", "Episode 20", "Episode 3"}
	want := []string{"Episode 3", "Episode 20", "Episode 100", "Folge 1", "Folge 2", "Folge 9", "Folge 10", "Folge 11"}
	sort.Slice(in, func(i, j int) bool { return naturalCompare(in[i], in[j]) < 0 })
	for i := range in {
		if in[i] != want[i] {
			t.Errorf("position %d: got %q, want %q", i, in[i], want[i])
		}
	}
}
