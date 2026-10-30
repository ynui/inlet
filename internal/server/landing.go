package server

import (
	"bytes"
	"embed"
	"net/http"
	"time"
)

//go:embed web
var landingAssets embed.FS

func serveLanding(w http.ResponseWriter, r *http.Request) {
	servePage(w, r, "landing.html")
}

func serveDocs(w http.ResponseWriter, r *http.Request) {
	servePage(w, r, "docs.html")
}

func servePage(w http.ResponseWriter, r *http.Request, name string) {
	serveEmbeddedAsset(w, r, "web/"+name, "text/html; charset=utf-8", "page unavailable")
}

func serveSiteCSS(w http.ResponseWriter, r *http.Request) {
	serveEmbeddedAsset(w, r, "web/site.css", "text/css; charset=utf-8", "stylesheet unavailable")
}

func serveEmbeddedAsset(w http.ResponseWriter, r *http.Request, name, contentType, unavailableMessage string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b, err := landingAssets.ReadFile(name)
	if err != nil {
		http.Error(w, unavailableMessage, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
}
