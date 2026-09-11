package api

import (
	"net/http/httptest"
	"testing"
)

func TestDeviceLabel(t *testing.T) {
	cases := []struct {
		name       string
		customHdr  string
		userAgent  string
		wantDevice string
	}{
		{"eigener Header hat Vorrang", "Goldfish-Mac/209", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15", "Goldfish-Mac/209"},
		{"Chrome unter macOS", "", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36", "Chrome · macOS"},
		{"Safari unter iOS", "", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", "Safari · iOS"},
		{"Firefox unter Windows", "", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:129.0) Gecko/20100101 Firefox/129.0", "Firefox · Windows"},
		{"Chrome unter Android", "", "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Mobile Safari/537.36", "Chrome · Android"},
		{"kein User-Agent, kein Header", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if c.customHdr != "" {
				r.Header.Set("X-Goldfish-Client", c.customHdr)
			}
			if c.userAgent != "" {
				r.Header.Set("User-Agent", c.userAgent)
			}
			got := deviceLabel(r)
			if got != c.wantDevice {
				t.Errorf("deviceLabel() = %q, want %q", got, c.wantDevice)
			}
		})
	}
}
