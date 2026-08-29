package api

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// SPA serves the built frontend from memory.
//
// Every file is read once at construction, with its ETag and content type precomputed. A
// bundle is a few hundred kilobytes; holding it sidesteps every http.FileServerFS quirk
// that would otherwise need working around, and means the file system is never touched
// again after startup.
type SPA struct {
	files map[string]*asset
	// shells is the prefix table: which document a navigation gets. One table, checked in
	// one place, so "which island is this?" has one answer.
	shells []shell
}

type asset struct {
	body        []byte
	contentType string
	etag        string
	immutable   bool
}

type shell struct {
	prefix string
	doc    string
}

// contentTypes is explicit rather than mime.TypeByExtension, which reads /etc/mime.types —
// a file a minimal container may not have, and whose absence would serve every stylesheet
// as application/octet-stream.
var contentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json; charset=utf-8",
	".webmanifest": "application/manifest+json; charset=utf-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".ico":         "image/x-icon",
	".woff2":       "font/woff2",
	".txt":         "text/plain; charset=utf-8",
}

// NewSPA walks whatever it is handed once and copies every file into a map.
//
// A missing or empty directory is not fatal: the placeholder page explains itself, and the
// API is still useful. That is also what lets `go test ./...` pass with no frontend build
// present.
func NewSPA(fsys fs.FS) (*SPA, error) {
	s := &SPA{
		files: map[string]*asset{},
		shells: []shell{
			{prefix: "/login", doc: "login.html"},
			{prefix: "/invite", doc: "login.html"},
			{prefix: "/", doc: "index.html"},
		},
	}
	if fsys == nil {
		return s, nil
	}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		ct, ok := contentTypes[strings.ToLower(path.Ext(p))]
		if !ok {
			ct = "application/octet-stream"
		}
		s.files["/"+p] = &asset{
			body:        body,
			contentType: ct,
			etag:        `"` + hex.EncodeToString(sum[:16]) + `"`,
			// Vite content-hashes everything under /assets, so the name changes when the
			// bytes do and a year is safe.
			immutable: strings.HasPrefix(p, "assets/"),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Empty reports whether any bundle was found, so serve can say so at startup rather than
// letting somebody discover it in a browser.
func (s *SPA) Empty() bool { return len(s.files) == 0 }

func (s *SPA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a, ok := s.files[r.URL.Path]; ok {
		s.write(w, r, a)
		return
	}
	// A request that does not look like navigation gets a 404, not the shell. A missing
	// /app.js served as HTML presents as a MIME-type error with no hint that the file
	// simply is not there.
	if !navigation(r) {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	for _, sh := range s.shells {
		if strings.HasPrefix(r.URL.Path, sh.prefix) {
			if a, ok := s.files["/"+sh.doc]; ok {
				s.write(w, r, a)
				return
			}
		}
	}
	s.placeholder(w)
}

func (s *SPA) write(w http.ResponseWriter, r *http.Request, a *asset) {
	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("ETag", a.etag)
	if a.immutable {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// no-cache, never no-store: the browser may keep it and must revalidate, which is
		// what makes a deploy visible on the next navigation.
		w.Header().Set("Cache-Control", "no-cache")
	}
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, a.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(a.body)
	}
}

// navigation is whether this request is a browser asking for a page.
//
// Sec-Fetch-Mode is set by every browser that can run this application, so the Accept
// sniffing is only for curl and for tests.
func navigation(r *http.Request) bool {
	if mode := r.Header.Get("Sec-Fetch-Mode"); mode != "" {
		return mode == "navigate"
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

const placeholderHTML = `<!doctype html>
<meta charset="utf-8">
<title>btw</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>body{font:16px/1.6 system-ui,sans-serif;margin:4rem auto;max-width:34rem;padding:0 1.5rem}</style>
<h1>btw</h1>
<p>The frontend has not been built. Run <code>npm ci &amp;&amp; npm run build</code> in <code>web/</code>,
or set <code>BTW_WEB_DIR</code> to a directory that holds one.</p>
<p>The API is running: <a href="/healthz">/healthz</a>.</p>
`

func (s *SPA) placeholder(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(placeholderHTML))
}
