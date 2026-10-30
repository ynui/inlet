package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeWebAssets(t *testing.T) {
	tests := []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/", contentType: "text/html; charset=utf-8", contains: `<title>inlet — your localhost, now a URL</title>`},
		{path: "/docs", contentType: "text/html; charset=utf-8", contains: `<title>inlet docs — setup and usage</title>`},
		{path: "/site.css", contentType: "text/css; charset=utf-8", contains: ".topbar"},
		{path: "/docs/site.css", contentType: "text/css; charset=utf-8", contains: ".topbar"},
	}

	proxy := NewProxy(nil)
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			proxy.HandlePublic(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if got := rec.Header().Get("Content-Type"); got != tt.contentType {
				t.Fatalf("Content-Type = %q, want %q", got, tt.contentType)
			}
			if !strings.Contains(rec.Body.String(), tt.contains) {
				t.Fatalf("response does not contain %q", tt.contains)
			}
		})
	}
}
