package api

import (
	"net/http/httptest"
	"testing"
)

// Die Wiedergabe-Aktivität im Health-Endpoint ist nur für Anfragen aus dem
// Container selbst gedacht (Deploy-Schutz). Über den Reverse-Proxy — erkennbar
// an X-Forwarded-For/X-Real-IP — oder von einer fremden Adresse darf sie nie
// herausgegeben werden.
func TestIsLoopbackRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/health", nil)
	r.RemoteAddr = "127.0.0.1:51234"
	if !isLoopbackRequest(r) {
		t.Fatal("localhost ohne Proxy-Header muss als lokal gelten")
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	if isLoopbackRequest(r) {
		t.Fatal("Anfrage über den Reverse-Proxy darf nicht als lokal gelten")
	}
	r2 := httptest.NewRequest("GET", "/api/health", nil)
	r2.RemoteAddr = "172.17.0.1:40000"
	if isLoopbackRequest(r2) {
		t.Fatal("Docker-Bridge-Adresse darf nicht als lokal gelten")
	}
	r3 := httptest.NewRequest("GET", "/api/health", nil)
	r3.RemoteAddr = "127.0.0.1:1"
	r3.Header.Set("X-Real-IP", "198.51.100.2")
	if isLoopbackRequest(r3) {
		t.Fatal("X-Real-IP muss ebenfalls als Proxy-Anfrage gelten")
	}
}
