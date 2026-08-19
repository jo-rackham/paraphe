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
	// Through the SAME preparation the start runs: the page this build
	// serves is held in memory and carries the instance marker, and a
	// fixture reading the file off the disk would exercise a path production
	// never takes — which is exactly how ?org= would go inert unnoticed.
	if err := s.prepareBrowserVersion(); err != nil {
		t.Fatal(err)
	}
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
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
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
	// …and it DOES carry the instance marker, which is what makes ?org=
	// work here. The published image is built with no domain baked in, so
	// this line is the only thing standing between a link that offers the
	// campaign and one that drops a volunteer on an empty configuration.
	if !strings.Contains(resp.body, `<meta name="paraphe-instance" content="paraphe.test">`) {
		t.Fatalf("the browser version's page does not name its instance: "+
			"?org= would resolve against nothing.\n%s", resp.body)
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

// A single-campaign instance has no subdomain space, so `?org=` has nowhere
// to resolve. The marker is then ABSENT rather than empty: the interface
// reads "no instance here" and leaves the parameter inert, which is what the
// published build does on a static host.
func TestASingleCampaignInstanceNamesNoInstanceOnTheBrowserPage(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "")
	_, srv := browserFixture(t)

	resp := browserGet(t, srv, "/navigateur/")
	if strings.Contains(resp.body, "paraphe-instance") {
		t.Fatalf("no base domain, yet the page names an instance:\n%s", resp.body)
	}
}

// THE WAY BACK, and the single-campaign case is the one worth a test of its
// own: that instance names no domain, and derived from the domain the door
// back to the account version would have opened only on a multi-campaign
// instance. Both modes serve an account version at the root of this origin,
// so both say so.
func TestTheBrowserPageSaysAnInstanceServesIt(t *testing.T) {
	for name, domain := range map[string]string{
		"single-campaign": "", "multi-campaign": "paraphe.test",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PARAPHE_BASE_DOMAIN", domain)
			_, srv := browserFixture(t)

			resp := browserGet(t, srv, "/navigateur/")
			if !strings.Contains(resp.body,
				`<meta name="paraphe-served-by" content="instance">`) {
				t.Fatalf("an instance serves this build and the page does not "+
					"say so, so it offers no way back:\n%s", resp.body)
			}
		})
	}
}

// The value becomes an HTML attribute. It goes through the SAME check the
// start applies to the setting — a guard that re-states the rule is the copy
// that drifts — so a domain that could close the attribute fails the start
// instead of reaching the page.
func TestABaseDomainThatIsNotOneNeverReachesTheBrowserPage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"),
		[]byte("<html><head></head><body>version navigateur</body></html>"),
		0o600); err != nil {
		t.Fatal(err)
	}
	for _, poison := range []string{
		`paraphe.test"><script src=//ailleurs.test></script>`,
		// The two that prove the CHECK runs before the normalisation:
		// normaliseHost strips at the last colon, so run first it would
		// hand `https` and `paraphe.test` to a check that accepts both.
		"https://paraphe.test",
		"paraphe.test:8047",
		"paraphe.test/chemin",
		"paraphe.test?x=1",
		".paraphe.test",
		"127.0.0.1",
	} {
		page, err := markBrowserVersion(dir, poison)
		if err == nil {
			t.Errorf("markBrowserVersion(%q) accepted it: %s", poison, page)
		}
		if page != nil {
			t.Errorf("markBrowserVersion(%q) returned a page as well as an "+
				"error: %s", poison, page)
		}
	}
	// And what the check tolerates, the page carries NORMALISED — the form
	// Host headers are matched against. Written out raw, `PARAPHE.TEST `
	// passed validBaseDomain (which lowercases and trims before judging)
	// and put a space inside the URL the pre-fill builds.
	page, err := markBrowserVersion(dir, "PARAPHE.TEST ")
	if err != nil {
		t.Fatalf("a domain differing only in case and space was refused: %v", err)
	}
	if !strings.Contains(string(page), `content="paraphe.test"`) {
		t.Errorf("the instance marker is not the normalised domain:\n%s", page)
	}
}

// …and a build that already thinks it is talking to an API is refused
// outright: served under /navigateur/ it would never fall into browser mode,
// and every one of its /api calls would answer HTML to a screen expecting
// JSON.
func TestABuildCarryingTheModeMarkerIsRefusedAsTheBrowserVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"),
		[]byte("<html><head>"+modeMarker+"</head><body>x</body></html>"),
		0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := markBrowserVersion(dir, "paraphe.test"); err == nil {
		t.Fatal("a build carrying the mode marker was accepted as the " +
			"browser version")
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
