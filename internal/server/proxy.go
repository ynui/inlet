package server

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ynui/inlet/internal/proto"
)

// affinityCookie remembers the tunnel that a browser last used so requests
// without a Referer (no-referrer policy, direct hits) can still be routed.
const affinityCookie = "__inlet_tid"

type Proxy struct {
	hub *Hub
}

func NewProxy(h *Hub) *Proxy {
	return &Proxy{hub: h}
}

func (p *Proxy) HandlePublic(w http.ResponseWriter, r *http.Request) {
	// Path based: /<id>/rest...
	if tid := firstPathSegment(r.URL.Path); isTunnelID(tid) {
		p.serveTunnel(w, r, tid)
		return
	}

	// Root-absolute request from a tunneled page (e.g. /style.css). The
	// Referer is per-document, so it identifies the right tunnel per tab.
	// Canonicalise the URL under /<id>/ and let the browser follow.
	if tid := p.refererTunnel(r); tid != "" {
		redirectToTunnel(w, r, tid)
		return
	}

	// Reserved inlet pages/styles. Reached only when the Referer names no
	// tunnel, so a tunneled app's /site.css wins when its page loads it.
	switch r.URL.Path {
	case "/site.css", "/docs/site.css":
		serveSiteCSS(w, r)
		return
	case "/":
		serveLanding(w, r)
		return
	case "/docs", "/docs/":
		serveDocs(w, r)
		return
	}

	// No Referer at all: fall back to the affinity cookie. It is host-wide,
	// so the last opened tunnel wins; the Referer stays the primary signal.
	if tid := p.cookieTunnel(r); tid != "" {
		redirectToTunnel(w, r, tid)
		return
	}

	http.Error(w, "inlet: tunnel not found", http.StatusNotFound)
}

func (p *Proxy) serveTunnel(w http.ResponseWriter, r *http.Request, tid string) {
	started := time.Now()
	logFailure := func(status int, reason string) {
		log.Printf("tunnel request failed tunnel=%s method=%s status=%d duration=%s reason=%q", tid, r.Method, status, time.Since(started).Round(time.Millisecond), reason)
	}

	rest := "/"
	if len(r.URL.Path) > len(tid)+1 {
		rest = r.URL.Path[len(tid)+1:]
		if !strings.HasPrefix(rest, "/") {
			rest = "/" + rest
		}
	}
	if r.URL.RawQuery != "" {
		rest += "?" + r.URL.RawQuery
	}

	// read body
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
		r.Body.Close()
	}

	headers := make(map[string][]string, len(r.Header))
	for k, v := range r.Header {
		// skip hop by hop
		if strings.EqualFold(k, "Connection") || strings.EqualFold(k, "Upgrade") {
			continue
		}
		headers[k] = append(headers[k], v...)
	}

	req := &proto.Request{
		ID:      randID(8),
		Method:  r.Method,
		Path:    rest,
		Headers: headers,
		Body:    body,
		Remote:  r.RemoteAddr,
	}

	ch, err := p.hub.sendReq(req.ID, tid, req)
	if err != nil {
		logFailure(http.StatusBadGateway, err.Error())
		http.Error(w, "inlet: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() {
		p.hub.respMu.Lock()
		delete(p.hub.resp, req.ID)
		p.hub.respMu.Unlock()
	}()

	setAffinityCookie(w, r, tid)

	idle := time.NewTimer(60 * time.Second)
	defer idle.Stop()
	select {
	case res := <-ch:
		if res == nil {
			logFailure(http.StatusBadGateway, "empty_response")
			http.Error(w, "inlet: bad response", http.StatusBadGateway)
			return
		}
		copyTunnelHeaders(w.Header(), res.Headers, tid)
		if res.Status == 0 {
			res.Status = 200
		}
		w.WriteHeader(res.Status)
		if len(res.Body) > 0 {
			w.Write(res.Body)
		}
		if res.Final {
			return
		}
	case <-idle.C:
		logFailure(http.StatusGatewayTimeout, "response_timeout")
		http.Error(w, "inlet: timeout", http.StatusGatewayTimeout)
		return
	}

	fl, _ := w.(http.Flusher)
	for {
		idle.Reset(5 * time.Minute)
		select {
		case res := <-ch:
			if res == nil {
				return
			}
			if len(res.Body) > 0 {
				w.Write(res.Body)
				if fl != nil {
					fl.Flush()
				}
			}
			if res.Final {
				return
			}
		case <-idle.C:
			logFailure(http.StatusGatewayTimeout, "stream_timeout")
			return
		}
	}
}

func redirectToTunnel(w http.ResponseWriter, r *http.Request, tid string) {
	target := "/" + tid + r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	h := w.Header()
	h.Set("Location", target)
	h.Set("Cache-Control", "no-store")
	h.Add("Vary", "Referer")
	w.WriteHeader(http.StatusTemporaryRedirect)
}

// setAffinityCookie must run before WriteHeader.
func setAffinityCookie(w http.ResponseWriter, r *http.Request, tid string) {
	secure := r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     affinityCookie,
		Value:    tid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
}

// copyTunnelHeaders rewrites root-absolute Location headers so app redirects
// stay under the tunnel prefix, and drops any Set-Cookie that would clobber
// the affinity cookie.
func copyTunnelHeaders(dst http.Header, src map[string][]string, tid string) {
	for k, vv := range src {
		for _, v := range vv {
			switch {
			case strings.EqualFold(k, "Set-Cookie") && strings.HasPrefix(strings.TrimSpace(v), affinityCookie+"="):
				continue
			case strings.EqualFold(k, "Location"):
				v = rewriteLocation(v, tid)
			}
			dst.Add(k, v)
		}
	}
}

func rewriteLocation(loc, tid string) string {
	if !strings.HasPrefix(loc, "/") || strings.HasPrefix(loc, "//") {
		return loc
	}
	if loc == "/"+tid || strings.HasPrefix(loc, "/"+tid+"/") {
		return loc
	}
	return "/" + tid + loc
}

func (p *Proxy) refererTunnel(r *http.Request) string {
	ref := r.Referer()
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return p.onlineTunnel(firstPathSegment(u.Path))
}

func (p *Proxy) cookieTunnel(r *http.Request) string {
	c, err := r.Cookie(affinityCookie)
	if err != nil {
		return ""
	}
	return p.onlineTunnel(c.Value)
}

func (p *Proxy) onlineTunnel(tid string) string {
	if p.hub == nil || !isTunnelID(tid) || !p.hub.Online(tid) {
		return ""
	}
	return tid
}

func firstPathSegment(path string) string {
	path = strings.TrimPrefix(path, "/")
	if i := strings.IndexByte(path, '/'); i >= 0 {
		return path[:i]
	}
	return path
}

// Tunnel IDs are 12 lowercase hex chars (store.randID(6)).
func isTunnelID(s string) bool {
	if len(s) != 12 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('0' <= c && c <= '9') && !('a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

func randID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
