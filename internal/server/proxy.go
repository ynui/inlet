package server

import (
	"io"
	"net/http"
	"strings"

	"github.com/ynui/inlet/internal/proto"
)

type Proxy struct {
	hub *Hub
}

func NewProxy(h *Hub) *Proxy {
	return &Proxy{hub: h}
}

func (p *Proxy) HandlePublic(w http.ResponseWriter, r *http.Request) {
	// path based: /<id>/rest...
	parts := strings.SplitN(strings.Trim(r.URL.Path, "/"), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "inlet: tunnel not found", http.StatusNotFound)
		return
	}
	tid := parts[0]
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

	resp, err := p.hub.sendReq(req.ID, tid, req)
	if err != nil {
		http.Error(w, "inlet: "+err.Error(), http.StatusBadGateway)
		return
	}
	for k, vv := range resp.Headers {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.Status)
	if len(resp.Body) > 0 {
		w.Write(resp.Body)
	}
}
