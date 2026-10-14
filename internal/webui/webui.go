// Package webui serves the embedded SvelteKit SPA with precompressed assets (spec §13).
package webui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves hashed assets as immutable and falls back to index.html for SPA routes.
func Handler() http.Handler {
	root, _ := fs.Sub(dist, "dist")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if st, err := fs.Stat(root, p); err != nil || st.IsDir() {
			p = "index.html"
		}
		if p == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		} else if strings.HasPrefix(p, "_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		ct := mime.TypeByExtension(path.Ext(p))
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Add("Vary", "Accept-Encoding")
		ae := r.Header.Get("Accept-Encoding")
		for _, enc := range []struct{ name, ext string }{{"br", ".br"}, {"gzip", ".gz"}} {
			if strings.Contains(ae, enc.name) {
				if b, err := fs.ReadFile(root, p+enc.ext); err == nil {
					w.Header().Set("Content-Encoding", enc.name)
					_, _ = w.Write(b)
					return
				}
			}
		}
		b, err := fs.ReadFile(root, p)
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<!doctype html><title>Loxy</title><p>Web UI not built. Run <code>make web</code>.</p>"))
			return
		}
		_, _ = w.Write(b)
	})
}
