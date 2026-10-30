package server

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ynui/inlet/internal/proto"
)

type Proxy struct {
	hub *Hub
}

func NewProxy(h *Hub) *Proxy {
	return &Proxy{hub: h}
}

func (p *Proxy) HandlePublic(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		serveLanding(w, r)
		return
	}
	// path based: /<id>/rest...
	parts := strings.SplitN(strings.Trim(r.URL.Path, "/"), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "inlet: tunnel not found", http.StatusNotFound)
		return
	}
	tid := parts[0]
	started := time.Now()
	logFailure := func(status int, reason string) {
		log.Printf("tunnel request failed tunnel=%s method=%s status=%d duration=%s reason=%q", tid, r.Method, status, time.Since(started).Round(time.Millisecond), reason)
	}
	rest := "/"
	if len(parts) == 2 {
		rest = "/" + parts[1]
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

	idle := time.NewTimer(60 * time.Second)
	defer idle.Stop()
	select {
	case res := <-ch:
		if res == nil {
			logFailure(http.StatusBadGateway, "empty_response")
			http.Error(w, "inlet: bad response", http.StatusBadGateway)
			return
		}
		for k, vv := range res.Headers {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
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

func randID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
