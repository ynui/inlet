package server

import (
	"bytes"
	"embed"
	"net/http"
	"time"
)

//go:embed landing.html
var landingAssets embed.FS

func serveLanding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b, err := landingAssets.ReadFile("landing.html")
	if err != nil {
		http.Error(w, "landing page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(b))
}
