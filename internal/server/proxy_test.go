package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testTunnelID = "abc123abc123" // 12 lowercase hex

func onlineProxy() *Proxy {
	h := NewHub(nil)
	h.register(testTunnelID, &Client{})
	return NewProxy(h)
}

func TestHandlePublicRefererRedirect(t *testing.T) {
	p := onlineProxy()
	req := httptest.NewRequest(http.MethodGet, "/style.css?v=1", nil)
	req.Header.Set("Referer", "https://host/"+testTunnelID+"/page")
	rec := httptest.NewRecorder()

	p.HandlePublic(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTemporaryRedirect)
	}
	if got, want := rec.Header().Get("Location"), "/"+testTunnelID+"/style.css?v=1"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestHandlePublicCookieFallback(t *testing.T) {
	p := onlineProxy()
	req := httptest.NewRequest(http.MethodGet, "/main.js", nil)
	req.AddCookie(&http.Cookie{Name: affinityCookie, Value: testTunnelID})
	rec := httptest.NewRecorder()

	p.HandlePublic(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTemporaryRedirect)
	}
	if got, want := rec.Header().Get("Location"), "/"+testTunnelID+"/main.js"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

func TestHandlePublicUnknownRootPathIs404(t *testing.T) {
	p := onlineProxy()
	req := httptest.NewRequest(http.MethodGet, "/main.js", nil)
	rec := httptest.NewRecorder()

	p.HandlePublic(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestReservedPathFollowsTunnelReferer(t *testing.T) {
	p := onlineProxy()
	req := httptest.NewRequest(http.MethodGet, "/site.css", nil)
	req.Header.Set("Referer", "https://host/"+testTunnelID+"/")
	rec := httptest.NewRecorder()

	p.HandlePublic(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTemporaryRedirect)
	}
	if got, want := rec.Header().Get("Location"), "/"+testTunnelID+"/site.css"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

// A stale affinity cookie must not hijack the landing page's own stylesheet.
func TestReservedPathIgnoresCookie(t *testing.T) {
	p := onlineProxy()
	req := httptest.NewRequest(http.MethodGet, "/site.css", nil)
	req.AddCookie(&http.Cookie{Name: affinityCookie, Value: testTunnelID})
	rec := httptest.NewRecorder()

	p.HandlePublic(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("Content-Type = %q, want text/css", rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), ".topbar") {
		t.Fatal("expected inlet's stylesheet, not a redirect")
	}
}

func TestOfflineTunnelStillReportsOffline(t *testing.T) {
	p := NewProxy(NewHub(nil))
	req := httptest.NewRequest(http.MethodGet, "/"+testTunnelID+"/foo", nil)
	rec := httptest.NewRecorder()

	p.HandlePublic(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	if !strings.Contains(rec.Body.String(), "tunnel offline") {
		t.Fatalf("body = %q, want tunnel offline", rec.Body.String())
	}
}

func TestSetAffinityCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")

	setAffinityCookie(rec, req, testTunnelID)

	cookie := rec.Header().Get("Set-Cookie")
	for _, want := range []string{affinityCookie + "=" + testTunnelID, "Path=/", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(cookie, want) {
			t.Fatalf("Set-Cookie = %q, missing %q", cookie, want)
		}
	}
}

func TestRewriteLocation(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/login", "/" + testTunnelID + "/login"},
		{"/", "/" + testTunnelID + "/"},
		{"/" + testTunnelID + "/x", "/" + testTunnelID + "/x"},
		{"https://example.com/x", "https://example.com/x"},
		{"//cdn.example.com/x", "//cdn.example.com/x"},
		{"relative", "relative"},
	}
	for _, tc := range tests {
		if got := rewriteLocation(tc.in, testTunnelID); got != tc.want {
			t.Errorf("rewriteLocation(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
