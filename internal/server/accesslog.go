package server

import (
	"bufio"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// WithAccessLog logs method, tunnel ID, status and duration without recording
// request paths, query strings, headers, bodies or client addresses.
func WithAccessLog(next http.Handler, enabled bool) http.Handler {
	if !enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/_inlet/") {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		recorder := &accessLogResponse{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		id := "root"
		if path := strings.Trim(r.URL.Path, "/"); path != "" {
			id = strings.SplitN(path, "/", 2)[0]
		}
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		log.Printf("http request method=%s tunnel=%s status=%d duration=%s", r.Method, id, status, time.Since(started).Round(time.Millisecond))
	})
}

type accessLogResponse struct {
	http.ResponseWriter
	status int
}

func (w *accessLogResponse) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *accessLogResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *accessLogResponse) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *accessLogResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (w *accessLogResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
