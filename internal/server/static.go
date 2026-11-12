package server

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"strings"
)

//go:embed all:webdist
var webFS embed.FS

// serveStatic serves files from the embedded SPA build output.
func serveStatic(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(webFS, "webdist")
	if err != nil {
		http.Error(w, "web assets missing", http.StatusInternalServerError)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	if _, err := fs.Stat(sub, name); err != nil {
		serveIndex(w)
		return
	}
	http.FileServerFS(sub).ServeHTTP(w, r)
}

// serveIndex returns the SPA entry point for client-side routes.
func serveIndex(w http.ResponseWriter) {
	data, err := webFS.ReadFile("webdist/index.html")
	if err != nil {
		log.Printf("server: embedded webdist/index.html missing: %v", err)
		http.Error(w, "web UI not built; run scripts/build-web.sh", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
