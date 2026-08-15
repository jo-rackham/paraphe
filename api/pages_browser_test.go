package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The self-hosted browser version: a second build served under /navigateur/
// with NO mode marker, whose /api paths answer HTML. Those two facts are the
// whole feature — they are what makes the interface decide "no API here" on
// the very origin that serves one.

func browserFixture(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"),
		[]byte("<html><head></head><body>version navigateur</body></html>"),
		0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "index-abc.js"),
		[]byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	// the lists mapping serves the file the csv setting names, from its
	// directory — the same file the startup import reads
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "04_base_complete.csv"),
		[]byte("insee;nom\n01000;Test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARAPHE_CSV", filepath.Join(data, "04_base_complete.csv"))
	s, srv := testServer(t)
	// testServer builds the struct directly; the handler reads the field
	s.browserDir = dir
	return s, srv
}

// browserGet reads the whole answer and closes it: what a test asserts on
// is values, not a live body.
type browserAnswer struct {
	code      int
	header    http.Header
	body      string
	finalPath string
}

func browserGet(t *testing.T, srv *httptest.Server, path string) browserAnswer {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "paraphe.test"
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return browserAnswer{
		code:      resp.StatusCode,
		header:    resp.Header,
		body:      string(body),
		finalPath: resp.Request.URL.Path,
	}
}

func TestBrowserVersionCarriesNoMarkerAndItsAPIAnswersHTML(t *testing.T) {
	_, srv := browserFixture(t)

	// the page: HTML, never cached, and WITHOUT the marker — with it, this
	// build could never fall into browser mode
	resp := browserGet(t, srv, "/navigateur/")
	if resp.code != http.StatusOK {
		t.Fatalf("/navigateur/: %d", resp.code)
	}
	if ct := resp.header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("/navigateur/ content-type: %q", ct)
	}
	if cc := resp.header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("/navigateur/ cache-control: %q", cc)
	}
	if strings.Contains(resp.body, `name="paraphe-mode"`) {
		t.Fatal("the browser version's page carries the mode marker: " +
			"it would never switch to browser mode")
	}

	// /navigateur/api/* answers HTML, exactly like a static host with no
	// API: a JSON answer here would flip the build into team mode
	resp = browserGet(t, srv, "/navigateur/api/config")
	if ct := resp.header.Get("Content-Type"); strings.Contains(ct, "application/json") {
		t.Fatalf("/navigateur/api/config answers JSON (%q): the browser "+
			"build would take it for a live API and leave browser mode", ct)
	}

	// an extension-less path is the single page again (a reload deep in the
	// application must render it, not 404)
	resp = browserGet(t, srv, "/navigateur/progression")
	if resp.code != http.StatusOK {
		t.Fatalf("/navigateur/progression: %d", resp.code)
	}

	// hashed assets are pinned for ever, like the main interface's
	resp = browserGet(t, srv, "/navigateur/assets/index-abc.js")
	if resp.code != http.StatusOK {
		t.Fatalf("asset: %d", resp.code)
	}
	if cc := resp.header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("asset cache-control: %q", cc)
	}

	// /navigateur without the slash redirects to it: every URL inside the
	// build is relative to /navigateur/
	respNoSlash := browserGet(t, srv, "/navigateur")
	if respNoSlash.finalPath != "/navigateur/" {
		t.Fatalf("/navigateur landed on %q, not /navigateur/",
			respNoSlash.finalPath)
	}
}

func TestBrowserVersionServesTheListsAndOnlyThem(t *testing.T) {
	_, srv := browserFixture(t)

	resp := browserGet(t, srv, "/navigateur/donnees/04_base_complete.csv")
	if resp.code != http.StatusOK {
		t.Fatalf("/navigateur/donnees/04_base_complete.csv: %d", resp.code)
	}
	if ct := resp.header.Get("Content-Type"); !strings.Contains(ct, "csv") {
		t.Fatalf("list content-type: %q", ct)
	}
	if cc := resp.header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("list cache-control: %q — 9 MB pinned for ever, or "+
			"re-sent on every load, are both wrong", cc)
	}

	// this mapping is an allowlist of two names, not a directory: anything
	// else — other files, subpaths — is refused
	for _, path := range []string{
		"/navigateur/donnees/autre.csv",
		"/navigateur/donnees/x/04_base_complete.csv",
	} {
		if resp := browserGet(t, srv, path); resp.code != http.StatusNotFound {
			t.Fatalf("%s answered %d, want 404", path, resp.code)
		}
	}
}

func TestBrowserVersionAbsentStepsAside(t *testing.T) {
	s, srv := testServer(t)
	if s.browserDir != "" {
		t.Fatal("fixture assumption broken: browserDir set")
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/navigateur/donnees/04_base_complete.csv", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "paraphe.test"
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only
	// with no build configured the handler steps aside: no data mapping,
	// the path belongs to the ordinary interface fallback
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "csv") {
		t.Fatal("/navigateur/donnees/ served a list with no build configured")
	}
}
