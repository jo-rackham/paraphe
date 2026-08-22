package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The object store, against a REAL one.
//
// The signature is hand-written (media.go says why), so the only test worth
// having is the one that lets the store judge it. A unit test of the
// canonical request would only check this file against itself.
//
// `task garage` starts a single node locally; the variables below are what
// it prints.

// testMedia points the settings at the disposable store and returns a client
// for it, or skips. It sets PARAPHE_MEDIA_* from PARAPHE_TEST_MEDIA_*: the
// code under test reads the former, and a suite that wrote to the real ones
// would upload into a live bucket.
func testMedia(t *testing.T) *MediaStore {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("PARAPHE_TEST_MEDIA_ENDPOINT"))
	if endpoint == "" {
		t.Skip("PARAPHE_TEST_MEDIA_ENDPOINT not set: a disposable object " +
			"store is required (task garage)")
	}
	for _, v := range []string{"ENDPOINT", "BUCKET", "ACCESS_KEY",
		"SECRET_KEY", "PUBLIC_URL"} {
		t.Setenv("PARAPHE_MEDIA_"+v, os.Getenv("PARAPHE_TEST_MEDIA_"+v))
	}
	store, err := NewMediaStore()
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Fatal("the test variables are set and NewMediaStore returned nothing")
	}
	return store
}

// fetchPublicly: what the BROWSER does — a plain GET on the media origin,
// no credentials, no signature. That path is the whole point of storing the
// logo outside the application, so it is the one asserted.
func fetchPublicly(t *testing.T, url string) (int, []byte, http.Header) {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx // a test against a local store
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body, resp.Header
}

func TestALogoWrittenToTheStoreIsPubliclyReadableThenGone(t *testing.T) {
	store := testMedia(t)
	ctx := context.Background()
	raw := rasterPNG(t, 64, 24)
	logo, code, why := readLogo("roundtrip", dataURI("image/png", raw))
	if logo == nil {
		t.Fatalf("fixture refused: %d %s", code, why)
	}

	if err := store.Put(ctx, logo.Key, logo.ContentType, logo.Raw); err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Cleanup(func() { _ = store.Delete(ctx, logo.Key) })

	status, body, headers := fetchPublicly(t, store.URL(logo.Key))
	if status != http.StatusOK {
		t.Fatalf("the object answered %d to an anonymous GET: %s", status, body)
	}
	if !bytes.Equal(body, raw) {
		t.Errorf("the bytes came back changed: %d in, %d out", len(raw), len(body))
	}
	if got := headers.Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png — a logo served under the "+
			"wrong type renders as nothing at all", got)
	}
	// Set at WRITE time, so it travels with the object rather than depending
	// on whatever sits in front of the store. The key carries a digest of
	// the content, which is what makes `immutable` safe.
	if got := headers.Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("Cache-Control = %q: the digest-keyed URL is meant to be "+
			"cached for ever, and every page load refetches it without this", got)
	}

	if err := store.Delete(ctx, logo.Key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if status, _, _ := fetchPublicly(t, store.URL(logo.Key)); status != http.StatusNotFound {
		t.Errorf("after deletion the object answered %d, want 404", status)
	}
}

// An SVG is a DOCUMENT: opened at its own address it runs what it contains,
// and a single review pass found two ways past the validator that did
// exactly that. `Content-Disposition: attachment` closes the whole class —
// the browser downloads the file instead of rendering it — and costs
// nothing, because `<img src>` ignores the header and draws the logo as
// before. Both halves verified in Chromium.
func TestAnSVGIsStoredSoNoBrowserRendersItAsAPage(t *testing.T) {
	store := testMedia(t)
	ctx := context.Background()
	logo, code, why := readLogo("disposition", dataURI("image/svg+xml",
		[]byte(cleanSVG)))
	if logo == nil {
		t.Fatalf("fixture refused: %d %s", code, why)
	}
	if err := store.Put(ctx, logo.Key, logo.ContentType, logo.Raw); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Delete(ctx, logo.Key) })
	status, _, headers := fetchPublicly(t, store.URL(logo.Key))
	if status != http.StatusOK {
		t.Fatalf("the SVG answered %d", status)
	}
	if got := headers.Get("Content-Disposition"); got != "attachment" {
		t.Errorf("Content-Disposition = %q: without it, an SVG that gets past "+
			"the validator is a page on the media origin", got)
	}

	// …and ONLY the SVG. A raster cannot execute anything, and marking it
	// as an attachment would make "open the image in a new tab" download it
	// for no reason at all.
	raster, _, _ := readLogo("disposition", dataURI("image/png", rasterPNG(t, 8, 8)))
	if err := store.Put(ctx, raster.Key, raster.ContentType, raster.Raw); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Delete(ctx, raster.Key) })
	if _, _, h := fetchPublicly(t, store.URL(raster.Key)); h.Get("Content-Disposition") != "" {
		t.Errorf("a PNG is served as an attachment: %q", h.Get("Content-Disposition"))
	}
}

// Deleting a key that is not there is how a pointer the database lost track
// of gets cleaned up. It must not be an error.
func TestDeletingAnAbsentObjectIsNotAFailure(t *testing.T) {
	store := testMedia(t)
	if err := store.Delete(context.Background(),
		"logos/nobody/0000000000000000.png"); err != nil {
		t.Errorf("deleting an absent key failed: %v", err)
	}
}

// The signature is hand-written: this is the case that proves the store
// actually verifies it, rather than accepting anything with an
// Authorization header.
func TestAWrongSecretIsRefused(t *testing.T) {
	store := testMedia(t)
	store.secretKey = strings.Repeat("0", len(store.secretKey))
	err := store.Put(context.Background(), "logos/refused/dead.png",
		"image/png", rasterPNG(t, 8, 8))
	if err == nil {
		t.Fatal("the store accepted a write signed with the wrong secret: " +
			"either the signature is not checked, or this test is not " +
			"reaching the store")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected a 403 from the store, got: %v", err)
	}
}

func TestAMissingBucketFailsTheStart(t *testing.T) {
	store := testMedia(t)
	store.bucket = "ce-seau-n-existe-pas"
	err := store.CheckBucket(context.Background())
	if err == nil {
		t.Fatal("a bucket that does not exist passed the startup check: an " +
			"instance would come up green and refuse every upload")
	}
	if !strings.Contains(err.Error(), "ce-seau-n-existe-pas") {
		t.Errorf("the error does not name the bucket: %v", err)
	}
}

// Five settings, all or nothing. Half of them is a deployment somebody
// believes is finished — a campaign uploads a logo and nobody can fetch it.
func TestTheObjectStoreIsConfiguredWhollyOrNotAtAll(t *testing.T) {
	for _, v := range []string{"ENDPOINT", "BUCKET", "ACCESS_KEY",
		"SECRET_KEY", "PUBLIC_URL"} {
		t.Setenv("PARAPHE_MEDIA_"+v, "")
	}
	store, err := NewMediaStore()
	if err != nil || store != nil {
		t.Fatalf("nothing configured should mean no store and no error, "+
			"got (%v, %v)", store, err)
	}

	t.Setenv("PARAPHE_MEDIA_ENDPOINT", "http://garage:3900")
	store, err = NewMediaStore()
	if err == nil {
		t.Fatal("an endpoint with no credentials was accepted")
	}
	if store != nil {
		t.Error("a refused configuration still produced a store")
	}
	for _, expected := range []string{"PARAPHE_MEDIA_BUCKET",
		"PARAPHE_MEDIA_ACCESS_KEY", "PARAPHE_MEDIA_SECRET_KEY",
		"PARAPHE_MEDIA_PUBLIC_URL"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the refusal does not name %s: %v", expected, err)
		}
	}
}

func TestAnUnusableEndpointIsRefusedAtStartup(t *testing.T) {
	for _, v := range []string{"BUCKET", "ACCESS_KEY", "SECRET_KEY"} {
		t.Setenv("PARAPHE_MEDIA_"+v, "x")
	}
	t.Setenv("PARAPHE_MEDIA_PUBLIC_URL", "https://media.exemple.fr")
	for _, bad := range []string{"garage:3900", "/var/run/garage", "not a url"} {
		t.Setenv("PARAPHE_MEDIA_ENDPOINT", bad)
		if _, err := NewMediaStore(); err == nil {
			t.Errorf("PARAPHE_MEDIA_ENDPOINT = %q was accepted: it carries no "+
				"scheme, and every write would fail on a deployment that "+
				"started clean", bad)
		}
	}
}

// The Content-Security-Policy is what decides whether the browser loads the
// logo at all, and it is assembled from a setting rather than written as a
// constant. A policy that forgets the origin shows as an image that never
// appears, in the console, and nowhere else.
func TestTheContentSecurityPolicyNamesTheMediaOrigin(t *testing.T) {
	plain := contentSecurityPolicy("", "")
	if !strings.Contains(plain, "img-src 'self' data:;") {
		t.Errorf("with no object store the policy should be unchanged: %q", plain)
	}
	if !strings.Contains(plain, "connect-src 'self';") {
		t.Errorf("with no object store the policy should be unchanged: %q", plain)
	}
	with := contentSecurityPolicy("https://media.paraphe.org", "")
	if !strings.Contains(with, "img-src 'self' data: https://media.paraphe.org;") {
		t.Errorf("the media origin is missing from img-src: %q", with)
	}
	// And from connect-src, which is a SECOND thing the same origin is
	// needed for: the account-less version served under /navigateur/
	// downloads the logo and inlines it as a data URI, because that mode
	// promises nothing leaves the browser. Left out, the campaign was
	// adopted without its mark and the failure showed in the console alone.
	if !strings.Contains(with, "connect-src 'self' https://media.paraphe.org;") {
		t.Errorf("the media origin is missing from connect-src: %q", with)
	}
	// The store's origin, and nothing else. What it must never become is a
	// place scripts, frames or forms may come from.
	for _, directive := range []string{"default-src 'self';",
		"form-action 'self';", "base-uri 'none';", "frame-ancestors 'none'"} {
		if !strings.Contains(with, directive) {
			t.Errorf("%q no longer holds: %q", directive, with)
		}
	}
	if strings.Contains(with, "script-src") {
		t.Errorf("the media origin reached a script directive: %q", with)
	}
}

// The OTHER call `connect-src` decides, and the one nothing held: a `?org=`
// link resolves to `https://<slug>.<base domain>/api/campaign/public`, which
// is a different origin from the page offering it. Left out of the policy,
// the browser refused the fetch and the screen said « Ce lien ne propose
// aucune campagne : Failed to fetch » — on production, in both directions,
// for a feature that had never worked there. The end-to-end suite builds
// with PARAPHE_BASE_DOMAIN empty, so it makes no cross-origin fetch and this
// is exactly what it cannot see.
func TestTheContentSecurityPolicyReachesTheInstancesOwnCampaigns(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.org")
	if got := campaignOrigins(); got != "https://*.paraphe.org" {
		t.Errorf("campaignOrigins() = %q, want the wildcard over the base domain", got)
	}
	policy := contentSecurityPolicy(mediaOrigin(), campaignOrigins())
	if !strings.Contains(policy, "connect-src 'self' https://*.paraphe.org;") {
		t.Errorf("a ?org= link cannot reach the campaign it names: %q", policy)
	}
	// The subdomains are a CONNECT target and nothing else: they are not a
	// place scripts, images, frames or forms may come from.
	for _, directive := range []string{"default-src 'self';", "img-src 'self' data:;",
		"form-action 'self';", "base-uri 'none';", "frame-ancestors 'none'"} {
		if !strings.Contains(policy, directive) {
			t.Errorf("%q no longer holds: %q", directive, policy)
		}
	}
	// Single-campaign: no subdomain space, so nothing is added and the policy
	// is what it always was.
	t.Setenv("PARAPHE_BASE_DOMAIN", "")
	if got := campaignOrigins(); got != "" {
		t.Errorf("campaignOrigins() = %q on a single-campaign instance", got)
	}
	if !strings.Contains(contentSecurityPolicy("", campaignOrigins()), "connect-src 'self';") {
		t.Error("a single-campaign instance widened its policy")
	}
}

// A REFUSED domain yields nothing, like a refused media origin: the value
// lands between two sources of a policy, and the startup check is what says
// it is a bare DNS name. Forwarding it is how `paraphe.org; script-src *`
// would become a directive of the operator's choosing on a process that
// starts clean, and `https://paraphe.test` — checked AFTER normalising —
// would become the one-label domain `https`.
func TestARefusedBaseDomainStaysOutOfThePolicy(t *testing.T) {
	for _, bad := range []string{
		"paraphe.org; script-src *",
		"paraphe.org script-src",
		"https://paraphe.test",
		"paraphe.org:8443",
		"paraphe.org/campagnes",
		"'self' *",
	} {
		t.Setenv("PARAPHE_BASE_DOMAIN", bad)
		if got := campaignOrigins(); got != "" {
			t.Errorf("PARAPHE_BASE_DOMAIN = %q reached the policy as %q", bad, got)
		}
		if policy := contentSecurityPolicy("", campaignOrigins()); !strings.Contains(
			policy, "connect-src 'self';") {
			t.Errorf("PARAPHE_BASE_DOMAIN = %q widened the policy: %q", bad, policy)
		}
	}
}

func TestTheMediaOriginDropsAnyPath(t *testing.T) {
	t.Setenv("PARAPHE_MEDIA_PUBLIC_URL", "https://media.paraphe.org/seau/")
	// As a CSP source a path is a PREFIX match, so the same setting written
	// with and without a trailing slash would allow different things.
	if got := mediaOrigin(); got != "https://media.paraphe.org" {
		t.Errorf("mediaOrigin() = %q, want the scheme and host alone", got)
	}
	t.Setenv("PARAPHE_MEDIA_PUBLIC_URL", "")
	if got := mediaOrigin(); got != "" {
		t.Errorf("mediaOrigin() = %q with nothing configured", got)
	}
}

// The setting becomes a SOURCE inside a security header, and a policy
// separates its directives with ';' and its sources with whitespace. A
// value carrying either does not name an origin — it appends directives.
//
// `* ; script-src *` is the one that mattered: it is a valid URL as far as
// net/url is concerned (no error, empty host), so the startup check passed
// it, the process began, every probe went green, and every page was served
// allowing scripts from anywhere.
func TestAMediaOriginCannotAppendDirectivesToThePolicy(t *testing.T) {
	poisons := []string{
		"* ; script-src *",
		"https://media.org; script-src *",
		"https://media.org;script-src 'unsafe-inline'",
		"https://media.org  ; default-src *",
		"not a url at all; script-src *",
		"https://a\n; script-src *",
		"https://media.org, https://ailleurs.example",
		"javascript:alert(1)",
		"media.paraphe.org", // no scheme: names no origin
	}
	for _, poison := range poisons {
		// Refused where an operator can still read why…
		if origin, err := MediaOrigin(poison); err == nil {
			t.Errorf("MediaOrigin(%q) = %q, accepted", poison, origin)
		}
		// …and, whatever happens, ABSENT from the header. This is the
		// assertion that counts: the policy must still say exactly what it
		// said before, with no directive the operator did not write.
		t.Setenv("PARAPHE_MEDIA_PUBLIC_URL", poison)
		policy := contentSecurityPolicy(mediaOrigin(), campaignOrigins())
		if strings.Count(policy, ";") != 6 {
			t.Errorf("PARAPHE_MEDIA_PUBLIC_URL = %q produced %d directives "+
				"instead of 7:\n\t%s", poison, strings.Count(policy, ";")+1, policy)
		}
		// NOT "unsafe-inline": the honest policy carries one, on style-src.
		// What must never appear is a directive nobody wrote.
		for _, forbidden := range []string{"script-src", "default-src *", "\n"} {
			if strings.Contains(policy, forbidden) {
				t.Errorf("PARAPHE_MEDIA_PUBLIC_URL = %q injected %q:\n\t%s",
					poison, forbidden, policy)
			}
		}
	}
}

// …and the honest value still works, or the guard above would be a guard
// that refuses everything.
func TestAPlainMediaOriginIsAccepted(t *testing.T) {
	for _, good := range []string{
		"https://media.paraphe.org",
		"https://media.paraphe.org/",
		"http://paraphe-media.media.localhost:3902",
		"https://media.paraphe.org/seau",
	} {
		origin, err := MediaOrigin(good)
		if err != nil {
			t.Errorf("MediaOrigin(%q) refused a legitimate value: %v", good, err)
			continue
		}
		t.Setenv("PARAPHE_MEDIA_PUBLIC_URL", good)
		if !strings.Contains(contentSecurityPolicy(mediaOrigin(), campaignOrigins()),
			"img-src 'self' data: "+origin+";") {
			t.Errorf("%q did not reach img-src", good)
		}
	}
}

// Two tabs, or a double click: a DELETE and a re-upload of the SAME image
// overlapping. A key is a digest of the content, so the re-upload writes
// exactly the key the deletion is about to remove — and the deletion is
// detached, so it can land afterwards and destroy an object every screen
// still names.
//
// Measured at 14 rounds out of 15 before the campaign's row was locked
// across both paths, on a store running on the same machine. A re-read
// without that lock does NOT close it: the writer commits after the read.
// A store that stops answering must cost a picture, and nothing else.
//
// Both logo routes talk to another machine. Held across that round trip, a
// pool connection makes a store outage into an application outage: measured
// on a paused Garage at the pgx default of four connections, six uploads
// took every one of them and six readiness probes out of six were lost —
// a pod dropped from its Service because a picture would not upload.
//
// The route hands its connection back before the call and asks for one
// after, which it can do because a key can no longer come back and so no
// lock has to span the network. This pins the property directly: while an
// upload sits on a store that never answers, the pool is untouched.
func TestAnUploadHoldsNoConnectionWhileTheStoreIsSilent(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 1, "01")
	email := "coord@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}

	// A store that accepts the connection, reads the request, and answers
	// nothing until this test lets it go — the shape of a wedged Garage,
	// without pausing one.
	arrived, release := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	silent := httptest.NewServer(http.HandlerFunc(
		func(_ http.ResponseWriter, _ *http.Request) {
			arrived <- struct{}{}
			<-release
		}))
	defer silent.Close()
	defer unblock()
	endpoint, err := url.Parse(silent.URL)
	if err != nil {
		t.Fatal(err)
	}
	s.media = &MediaStore{
		endpoint: endpoint, bucket: "seau", region: "garage",
		accessKey: "GKtest", secretKey: "secret",
		publicURL: "https://media.exemple.fr",
		client:    &http.Client{Timeout: mediaTimeout},
	}

	body := map[string]any{"data_uri": dataURI("image/png", rasterPNG(t, 30, 30))}
	done := make(chan int, 1)
	go func() {
		code, _ := c.call(http.MethodPost, "/api/campaign/logo", body)
		done <- code
	}()

	select {
	case <-arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("the upload never reached the store")
	}
	// The request is inside the call to the store. Anything it holds now, it
	// holds for as long as that store stays silent.
	if held := s.pool.Stat().AcquiredConns(); held != 0 {
		t.Errorf("%d pool connection(s) held while the store answers nothing: "+
			"a store outage is then an application outage", held)
	}
	// And the readiness probe, which needs one, answers.
	if code, _ := c.call(http.MethodGet, "/health/db", nil); code != http.StatusOK {
		t.Errorf("readiness answered %d while an upload waited on the store", code)
	}

	// Let the store answer, and the route must finish: it takes a connection
	// back, moves the pointer and commits. Handing the connection away
	// mid-request is only safe if the request can pick one up again.
	unblock()
	if code := <-done; code != http.StatusOK {
		t.Errorf("the upload answered %d once the store replied: the request "+
			"gave its connection back and could not take one again", code)
	}
	if held := s.pool.Stat().AcquiredConns(); held != 0 {
		t.Errorf("%d connection(s) still held after the answer", held)
	}
}

// The object goes in BEFORE the pointer moves, and nothing used to hold that
// order in place: a review inverted it — lock, update, commit, then write —
// and every test stayed green. Inverted, a write that FAILS leaves the row
// naming a key the bucket never received, so every screen of that campaign
// shows a broken image until somebody uploads again.
func TestAFailedWriteLeavesThePointerWhereItWas(t *testing.T) {
	s, srv := testServer(t)
	if s.media == nil {
		t.Skip("no object store: set PARAPHE_TEST_MEDIA_* (task garage)")
	}
	seedMayors(t, s, 1, "01")
	email := "coord@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}

	body := map[string]any{"data_uri": dataURI("image/png", rasterPNG(t, 30, 30))}
	if code, rep := c.call(http.MethodPost, "/api/campaign/logo", body); code != http.StatusOK {
		t.Fatalf("the first upload was refused: %d %v", code, rep)
	}
	_, first := c.call(http.MethodGet, "/api/config", nil)
	established, _ := first["logo"].(map[string]any)
	if established == nil {
		t.Fatal("/api/config names no logo after a successful upload")
	}

	// A store that refuses everything, in place of the one that worked —
	// under the SAME public origin, so what this test compares is the KEY
	// and not the address the answer happens to be built from.
	public := s.media.publicURL
	refusing := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
	defer refusing.Close()
	endpoint, err := url.Parse(refusing.URL)
	if err != nil {
		t.Fatal(err)
	}
	s.media = &MediaStore{
		endpoint: endpoint, bucket: "seau", region: "garage",
		accessKey: "GKtest", secretKey: "secret",
		publicURL: public,
		client:    &http.Client{Timeout: mediaTimeout},
	}

	replacement := map[string]any{"data_uri": dataURI("image/png", rasterPNG(t, 31, 30))}
	if code, _ := c.call(http.MethodPost, "/api/campaign/logo", replacement); code != http.StatusBadGateway {
		t.Errorf("a refused write answered %d, want 502", code)
	}
	_, after := c.call(http.MethodGet, "/api/config", nil)
	still, _ := after["logo"].(map[string]any)
	if still == nil || still["url"] != established["url"] {
		t.Errorf("the pointer moved on a write that failed: %v then %v — the "+
			"campaign now names an object the store never received",
			established["url"], still)
	}
}

// An instance with no object store is a supported state — the default one for
// a developer, and for most of this suite. Both routes say so in a sentence.
// Removing BOTH guards left all 318 tests green, and without them the upload
// dereferences a nil store: a panic, a 500, and no sentence at all.
func TestTheLogoRoutesSayWhenNoStoreIsConfigured(t *testing.T) {
	s, srv := testServer(t)
	s.media = nil
	seedMayors(t, s, 1, "01")
	email := "coord@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	body := map[string]any{"data_uri": dataURI("image/png", rasterPNG(t, 30, 30))}
	for _, call := range []struct {
		method string
		body   map[string]any
	}{
		{http.MethodPost, body},
		{http.MethodDelete, map[string]any{}},
	} {
		code, rep := c.call(call.method, "/api/campaign/logo", call.body)
		if code != http.StatusNotImplemented {
			t.Errorf("%s answered %d, want 501", call.method, code)
		}
		text, _ := rep["error"].(string)
		if !strings.Contains(text, "stockage d'images") {
			t.Errorf("%s: the answer does not say what is missing: %q",
				call.method, text)
		}
	}
}

// A deletion in flight when SIGTERM lands must be waited for, like a message
// on its way to a relay. Started as a bare goroutine it is simply cut, and
// the object it was removing stays in the bucket for ever with nothing
// naming it — an orphan produced by a rollout, which is a thing that happens
// on purpose and often.
func TestADeletionInFlightIsDrainedAtShutdown(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 1, "01")
	email := "coord@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}

	// A store that takes its time on a DELETE, and says it happened.
	var deleted atomic.Bool
	slow := httptest.NewServer(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				time.Sleep(300 * time.Millisecond)
				deleted.Store(true)
			}
		}))
	defer slow.Close()
	endpoint, err := url.Parse(slow.URL)
	if err != nil {
		t.Fatal(err)
	}
	s.media = &MediaStore{
		endpoint: endpoint, bucket: "seau", region: "garage",
		accessKey: "GKtest", secretKey: "secret",
		publicURL: "https://media.exemple.fr",
		client:    &http.Client{Timeout: mediaTimeout},
	}

	first := map[string]any{"data_uri": dataURI("image/png", rasterPNG(t, 30, 30))}
	second := map[string]any{"data_uri": dataURI("image/png", rasterPNG(t, 31, 30))}
	if code, rep := c.call(http.MethodPost, "/api/campaign/logo", first); code != http.StatusOK {
		t.Fatalf("first upload: %d %v", code, rep)
	}
	// The second replaces the first, which is what schedules the deletion.
	if code, rep := c.call(http.MethodPost, "/api/campaign/logo", second); code != http.StatusOK {
		t.Fatalf("second upload: %d %v", code, rep)
	}

	// Shutdown, right away: the deletion has had no time to finish.
	s.drainOutbound(3 * time.Second)
	if !deleted.Load() {
		t.Error("the shutdown drain did not wait for the deletion: a rollout " +
			"landing on one leaves an object nothing names")
	}
}

func TestARemovedLogoCannotDestroyTheOneThatReplacedIt(t *testing.T) {
	s, srv := testServer(t)
	if s.media == nil {
		t.Skip("no object store: set PARAPHE_TEST_MEDIA_* (task garage)")
	}
	seedMayors(t, s, 1, "01")
	email := "coord@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	body := map[string]any{"data_uri": dataURI("image/png", rasterPNG(t, 30, 30))}

	corrupt := 0
	for round := range 15 {
		c := newClient(t, srv)
		if code := c.signIn(email, pw); code != http.StatusOK {
			t.Fatalf("sign-in: %d", code)
		}
		if code, rep := c.call(http.MethodPost, "/api/campaign/logo", body); code != http.StatusOK {
			t.Fatalf("seed upload: %d %v", code, rep)
		}
		var wg sync.WaitGroup
		var removal, replacement int
		wg.Add(2)
		go func() {
			defer wg.Done()
			removal, _ = c.call(http.MethodDelete, "/api/campaign/logo", map[string]any{})
		}()
		go func() {
			defer wg.Done()
			replacement, _ = c.call(http.MethodPost, "/api/campaign/logo", body)
		}()
		wg.Wait()
		// Both ADMITTED: the two together are the race, and a refusal
		// would make this test pass by running nothing at all.
		if removal >= 500 || replacement >= 500 {
			t.Fatalf("round %d: one of the two was refused (delete %d, "+
				"upload %d) — the race is not being run",
				round, removal, replacement)
		}
		// The detached deletions are counted in s.outbound, so they can be
		// WAITED for instead of guessed at: 250 ms of sleep is a bet on a
		// machine's speed, and it loses on a loaded CI or against a store
		// one network away.
		s.drainOutbound(3 * time.Second)

		code, cfg := c.call(http.MethodGet, "/api/config", nil)
		if code != http.StatusOK {
			t.Fatalf("/api/config: %d", code)
		}
		logo, _ := cfg["logo"].(map[string]any)
		if logo == nil {
			// the deletion won the race: no pointer, so nothing to be wrong
			continue
		}
		url, _ := logo["url"].(string)
		if status, _, _ := fetchPublicly(t, url); status != http.StatusOK {
			corrupt++
			t.Logf("round %d: /api/config names %s, the store answers %d",
				round, url, status)
		}
		c.call(http.MethodDelete, "/api/campaign/logo", map[string]any{})
	}
	if corrupt > 0 {
		t.Errorf("%d/15 rounds left every screen naming an object the store "+
			"had already deleted", corrupt)
	}
}
