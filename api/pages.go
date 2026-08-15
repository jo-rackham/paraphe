package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Serving the interface: one binary serves the pages AND the JSON, so the
// page path exercised by every test is the one production runs, and whoever
// serves a response is who sets its headers.

// Mode marker, injected into the page served by the API.
//
// Without it, the interface deduces "no API, so browser mode" from any
// failure on /api/config — including a Wi-Fi portal or a 502. The volunteer
// then works in their browser, on the team's origin, noticing nothing:
// their work never reaches the server and stays on the computer after they
// leave. The marker makes that switch impossible.
const modeMarker = `<meta name="paraphe-mode" content="team">`

func markInterface(dir string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		return nil, err
	}
	page := string(raw)
	if !strings.Contains(page, "</head>") {
		return nil, fmt.Errorf("%s/index.html has no </head>: the mode marker "+
			"cannot be set, and the interface would switch to browser mode at "+
			"the first failure", dir)
	}
	return []byte(strings.Replace(page, "</head>", modeMarker+"\n</head>", 1)), nil
}

// gzipBytes compresses once, at startup. BestCompression because this runs
// exactly one time for a document served on every load.
func gzipBytes(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	z, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := z.Write(raw); err != nil {
		return nil, err
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// serveInterface serves web/dist. No directory listing, and fallback to
// index.html for extension-less paths (the application is a single page: a
// reload on /team must render the application, not 404).
//
// This binary serves the pages AND the JSON, which is why securityHeaders
// wraps the whole router: whoever serves a response is who sets its headers,
// and splitting the two once left every page without a Content-Security-
// Policy while the API kept its own.
func (s *Server) serveInterface(w http.ResponseWriter, r *http.Request) {
	// A file answers a read, and nothing else. Left alone, a POST to
	// /assets/index-a1b2.js returned 200 and the whole bundle — harmless in
	// itself, since no CORS header lets another origin read it, but it makes
	// a proxy log and a cache key say something that never happened.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		errorJSON(w, http.StatusMethodNotAllowed,
			"Cette adresse ne répond qu'en lecture.")
		return
	}
	path := filepath.Clean(r.URL.Path)
	if path == "/" || filepath.Ext(path) == "" {
		path = "/index.html"
	}
	file := filepath.Join(s.webDir, filepath.FromSlash(path))
	// filepath.Clean on an absolute URL path already neutralises "..", but
	// the check is free and survives the day this prefix changes
	root, err := filepath.Abs(s.webDir)
	if err != nil {
		s.failure(w, err)
		return
	}
	abs, err := filepath.Abs(file)
	if err != nil || !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		errorJSON(w, http.StatusNotFound, "Chemin inconnu.")
		return
	}
	// the landing page is served from memory, marked: it is what tells the
	// interface it is talking to an API
	if path == "/index.html" {
		if s.landingPage == nil {
			errorJSON(w, http.StatusNotFound,
				"Interface introuvable. Construire avec `task web-build`.")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// never cached: a volunteer holding an index.html from before a
		// deployment loads asset names that no longer exist
		w.Header().Set("Cache-Control", "no-store")
		s.writePage(w, r)
		return
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		errorJSON(w, http.StatusNotFound,
			"Interface introuvable (%s). Construire avec `task web-build`.", path)
		return
	}
	// Everything under /assets/ is content-hashed by the build, so its name
	// changes whenever its bytes do and it can be kept for ever. Everything
	// else — the favicon, robots.txt — carries a stable name and must not be.
	if strings.HasPrefix(path, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	s.serveFile(w, r, abs)
}

// encodings: the precompressed variants the build produces, best first. They
// are built once at image build time rather than compressed per request:
// brotli at quality 11 is far too slow to run on the fly, and it is what
// takes the interface bundle from 357 kB to 90.
var encodings = []struct{ token, suffix string }{
	{"br", ".br"},
	{"gzip", ".gz"},
}

// serveFile answers with a precompressed variant when the client accepts one
// and it exists beside the original.
//
// Content-Type comes from the ORIGINAL name: index-a1b2.js.br is JavaScript,
// and http.ServeFile would call it application/x-brotli. Vary is required or
// a cache serves the compressed bytes to a client that asked for none.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, abs string) {
	w.Header().Set("Vary", "Accept-Encoding")
	accepted := r.Header.Get("Accept-Encoding")
	for _, e := range encodings {
		if !acceptsEncoding(accepted, e.token) {
			continue
		}
		variant := abs + e.suffix
		info, err := os.Stat(variant)
		if err != nil || info.IsDir() {
			continue
		}
		w.Header().Set("Content-Type", contentType(abs))
		w.Header().Set("Content-Encoding", e.token)
		// Content-Length, set here because net/http will not: serveContent
		// leaves it out whenever a Content-Encoding is already present, and
		// the response goes out chunked. Some caches decline to store a
		// response with no length, which quietly undoes the
		// `immutable, max-age=31536000` on the very files it is meant for.
		//
		// A byte range over an ENCODED body counts in encoded bytes, which
		// is not what a client asking for one means; the assets are a few
		// hundred kilobytes and nothing requests a range of them, so ranges
		// are declined rather than half-supported.
		f, err := os.Open(variant)
		if err != nil {
			// the variant vanished between the stat and here: serve the
			// original rather than an empty 200
			w.Header().Del("Content-Encoding")
			break
		}
		defer f.Close() //nolint:errcheck // read-only
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		w.Header().Set("Accept-Ranges", "none")
		if r.Method == http.MethodHead {
			return
		}
		if _, err := io.Copy(w, f); err != nil {
			slog.Error("asset not served", "path", r.URL.Path, "error", err)
		}
		return
	}
	http.ServeFile(w, r, abs)
}

// acceptsEncoding: does this Accept-Encoding name the token, other than to
// refuse it? `gzip;q=0` is a client saying NOT gzip, and serving it gzip is
// how a response arrives unreadable.
//
// Only q=0 refuses. A prefix test on "q=0.0" also caught `q=0.001`, which is
// a client expressing a LOW preference and not a refusal, and every such
// client — proxies and CDNs negotiating on behalf of others write them —
// received 357 kB where 90 would have done. The value is parsed.
//
// The comparison on the token is case-insensitive because the header is:
// `GZIP` and `Q=0` are as legal as the lowercase forms.
func acceptsEncoding(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(fields[0]), token) {
			continue
		}
		for _, p := range fields[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(p), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") {
				continue
			}
			// An unreadable q is not a refusal: RFC 9110 says a malformed
			// parameter is ignored, and refusing here would silently drop
			// compression for a client that asked for it clumsily.
			if q, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && q == 0 {
				return false
			}
		}
		return true
	}
	return false
}

func contentType(name string) string {
	if t := mime.TypeByExtension(filepath.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// writePage serves the marked index.html, gzipped when the client accepts
// it. Only gzip: the page is compressed once at startup, in memory, and
// compress/gzip is in the standard library — bringing a brotli encoder into
// the module to save two kilobytes on one document would be a dependency
// bought for nothing. The assets, where the bytes actually are, get brotli
// from the build.
func (s *Server) writePage(w http.ResponseWriter, r *http.Request) {
	if s.landingPageGz != nil && acceptsEncoding(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		if _, err := w.Write(s.landingPageGz); err != nil {
			slog.Error("landing page not served", "error", err)
		}
		return
	}
	if _, err := w.Write(s.landingPage); err != nil {
		slog.Error("landing page not served", "error", err)
	}
}
