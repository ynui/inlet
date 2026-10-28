// Demo target for testing the tunnel: echoes whatever it receives and can
// fake other methods, statuses, redirects, slow responses and payload sizes.
//
//	go run ./scripts/demo-server -addr :8000
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type echo struct {
	Method    string              `json:"method"`
	Path      string              `json:"path"`
	Query     string              `json:"query,omitempty"`
	Headers   map[string][]string `json:"headers"`
	Body      string              `json:"body,omitempty"`
	BodyBytes int                 `json:"bodyBytes"`
}

func main() {
	addr := flag.String("addr", ":8000", "listen address")
	flag.Parse()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/status/"):
			code, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/status/"))
			if err != nil || code < 100 {
				http.Error(w, "bad status", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(code)
			fmt.Fprintf(w, "status %d\n", code)
		case r.URL.Path == "/redirect":
			http.Redirect(w, r, "/echo", http.StatusFound)
		case r.URL.Path == "/slow":
			ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
			if ms == 0 {
				ms = 500
			}
			time.Sleep(time.Duration(ms) * time.Millisecond)
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(w, "slept %dms\n", ms)
		case strings.HasPrefix(r.URL.Path, "/bytes/"):
			n, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/bytes/"))
			if err != nil || n < 0 || n > 10<<20 {
				http.Error(w, "bad size", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(make([]byte, n))
		case r.URL.Path == "/html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, "<!doctype html><title>demo</title><h1>hello</h1>")
		default:
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			w.Header().Set("Content-Type", "application/json")
			w.Header().Add("X-Demo", "true")
			json.NewEncoder(w).Encode(echo{
				Method:    r.Method,
				Path:      r.URL.Path,
				Query:     r.URL.RawQuery,
				Headers:   r.Header,
				Body:      string(body),
				BodyBytes: len(body),
			})
		}
	})

	fmt.Println("demo server on", *addr)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		fmt.Println(err)
	}
}
