package server

import (
	"bytes"
	"embed"
	"net/http"
	"time"
)

//go:embed landing.html docs.html
var landingAssets embed.FS

func serveLanding(w http.ResponseWriter, r *http.Request) {
	servePage(w, r, "landing.html")
}

func serveDocs(w http.ResponseWriter, r *http.Request) {
	servePage(w, r, "docs.html")
}

func servePage(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b, err := landingAssets.ReadFile(name)
	if err != nil {
		http.Error(w, "page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
}
