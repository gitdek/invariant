package dashboard

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
)

//go:embed web
var web embed.FS

// assets are the page's files, by the path they're served at. Nothing else
// on disk is ever served.
var assets = func() map[string][]byte {
	out := map[string][]byte{}
	fs.WalkDir(web, "web", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := web.ReadFile(p)
			out["/"+strings.TrimPrefix(p, "web/")] = b
		}
		return nil
	})
	return out
}()

// version changes whenever any asset does, so browsers and Cloudflare
// fetch the new ones.
var version = func() string {
	h := sha256.New()
	for _, p := range sortedKeys(assets) {
		h.Write([]byte(p))
		h.Write(assets[p])
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}()

var graphPath = regexp.MustCompile(`^/api/graph/([0-9a-f]{16})\.json$`)

// Handler serves the page, its assets, the snapshot and the state graphs.
// It answers only GET and HEAD.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'; object-src 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		switch p := r.URL.Path; {
		case p == "/api/state.json":
			s.mu.RLock()
			body := s.state
			s.mu.RUnlock()
			if body == nil {
				http.Error(w, "reading GitHub for the first time; try again in a few seconds", http.StatusServiceUnavailable)
				return
			}
			h.Set("Cache-Control", "no-store")
			serveGzip(w, r, "application/json", body)
		case graphPath.MatchString(p):
			key := graphPath.FindStringSubmatch(p)[1]
			s.mu.RLock()
			body := s.graphs[key]
			s.mu.RUnlock()
			if body == nil {
				http.Error(w, "that state graph isn't drawn yet", http.StatusNotFound)
				return
			}
			h.Set("Cache-Control", "public, max-age=86400, immutable")
			serveGzip(w, r, "application/json", body)
		case p == "/" || p == "/index.html":
			page := strings.ReplaceAll(string(assets["/index.html"]), "{{version}}", version)
			h.Set("Cache-Control", "no-cache")
			h.Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(page))
		default:
			body, ok := assets[p]
			if !ok || p == "/index.html" {
				http.NotFound(w, r)
				return
			}
			if r.URL.Query().Get("v") == version {
				h.Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				h.Set("Cache-Control", "public, max-age=300")
			}
			ct := mime.TypeByExtension(path.Ext(p))
			if ct == "" {
				ct = "application/octet-stream"
			}
			h.Set("Content-Type", ct)
			w.Write(body)
		}
	})
}

// serveGzip sends a gzipped body as is when the client takes gzip, and
// unzips it for one that doesn't.
func serveGzip(w http.ResponseWriter, r *http.Request, contentType string, gz []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Add("Vary", "Accept-Encoding")
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		w.Write(gz)
		return
	}
	b, err := gunzip(gz)
	if err != nil {
		http.Error(w, "couldn't read the stored response", http.StatusInternalServerError)
		return
	}
	w.Write(b)
}
