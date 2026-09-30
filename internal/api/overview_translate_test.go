package api

import "testing"

func TestLooksEnglish(t *testing.T) {
	for text, want := range map[string]bool{
		"There is a point in every human being when they are lonely. Lisa Brenner knew that moment. When her body is found one gray morning in front of a high-rise building, she leaves behind a number of men who adored and loved her.": true,
		"Batic und Leitmayr ermitteln im Umfeld eines Oktoberfest-Wirts, der tot in seinem Festzelt aufgefunden wird. Die Spur führt zu einem alten Streit.": false,
		"Kurz.": false,
		"Ein Toter im Hafen – und the Kommissar sucht.": false,
	} {
		if got := looksEnglish(text); got != want {
			t.Errorf("looksEnglish(%.40q…) = %v, erwartet %v", text, got, want)
		}
	}
}
