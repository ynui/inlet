package server

import (
	"net/http/httptest"
	"testing"
)

func TestPublicURL(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		addr     string
		host     string
		headers  map[string]string
		expected string
	}{
		{name: "explicit base wins", base: "https://tunnel.test", addr: ":8080", host: "control:8081", expected: "https://tunnel.test/abc"},
		{name: "uses public port", addr: ":8080", host: "localhost:8081", expected: "http://localhost:8080/abc"},
		{name: "host without port", addr: ":8080", host: "localhost", expected: "http://localhost:8080/abc"},
		{name: "forwarded host untouched", addr: ":8080", host: "control:8081", headers: map[string]string{
			"X-Forwarded-Host": "tunnel.example.com", "X-Forwarded-Proto": "https",
		}, expected: "https://tunnel.example.com/abc"},
		{name: "no public addr", host: "localhost:8081", expected: "http://localhost:8081/abc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHub(nil)
			h.SetPublicBase(tc.base)
			if tc.addr != "" {
				h.SetPublicAddr(tc.addr)
			}
			r := httptest.NewRequest("GET", "/_inlet/control", nil)
			r.Host = tc.host
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if got := h.PublicURL("abc", r); got != tc.expected {
				t.Fatalf("PublicURL = %q, want %q", got, tc.expected)
			}
		})
	}
}
