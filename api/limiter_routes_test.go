package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// Which ceiling covers which route — declared HERE, checked against the
// router's own tree. A route added without a line in this map turns the test
// red: the judgement "this one needs no limit" must be written down, because
// an unlimited route looks exactly like a limited one until someone leans
// on it.
var routeLimits = map[string]string{
	"GET /health":    "never: kubelet's liveness probe",
	"GET /health/db": "never: kubelet's readiness probe",

	"GET /api/config":     "anon_ip",
	"POST /api/session":   "signin_ip + signin_account",
	"DELETE /api/session": "none: clears a cookie, touches nothing",
	// Lower than signing in, because an admitted call makes this service
	// send a real message to somebody else's inbox.
	"POST /api/session/link": "magic_link_ip + magic_link_account",
	// No account class: the body carries a 256-bit token and no address,
	// so there is no subject to count under and nothing to search for.
	"POST /api/session/link/redeem": "signin_ip",
	"GET /api/campaign/public":      "anon_ip",

	"GET /api/me":                "none: authenticated read of one's own row",
	"POST /api/me/personal_note": "write_account",
	// Its own class, and narrow: this route VERIFIES the current password,
	// so it is a guessing surface for the same secret /api/session is —
	// reached by whoever already holds the session, which is exactly the
	// case where guessing it buys the account for good.
	"POST /api/me/password": "password_account",

	"GET /api/dashboard":              "none: authenticated read",
	"GET /api/facets":                 "none: authenticated read",
	"GET /api/mayors":                 "none: authenticated read",
	"GET /api/mayors/{insee}":         "none: authenticated read",
	"GET /api/export.csv":             "export_account",
	"POST /api/mayors/{insee}/status": "write_account",
	// Correcting one's own note, and removing one: authenticated writes on a
	// card, the same act as recording a status one line up.
	"POST /api/mayors/{insee}/notes/{id}":   "write_account",
	"DELETE /api/mayors/{insee}/notes/{id}": "write_account",
	"POST /api/batch":                       "write_account",

	"GET /api/team":                         "none: authenticated read",
	"POST /api/team/group":                  "write_account",
	"POST /api/team/group/{id}":             "write_account",
	"POST /api/team/request":                "team_request_ip",
	"POST /api/team/requests/{id}":          "write_account",
	"POST /api/team/account":                "write_account",
	"POST /api/team/account/{email}/active": "write_account",
	"POST /api/team/account/{email}/role":   "write_account",
	// Drawing a new password for somebody who lost theirs: a WRITE, not a
	// verification — nothing is guessed here, and it is the same act as
	// opening an access, one line up.
	"POST /api/team/account/{email}/password": "write_account",
	"POST /api/campaign":                      "write_account",
	// The campaign's own message templates, and one team's. Same ceiling as
	// every other authenticated write: they are edited by hand, by somebody
	// who signed in, a few times in a campaign's life.
	"POST /api/campaign/templates":                  "write_account",
	"POST /api/team/templates":                      "write_account",
	"POST /api/campaign/logo":                       "write_account",
	"DELETE /api/campaign/logo":                     "write_account",
	"POST /api/request":                             "hosting_ip",
	"GET /api/admin/requests":                       "none: authenticated read",
	"POST /api/admin/requests/{id}":                 "write_account",
	"POST /api/admin/campaigns":                     "write_account",
	"POST /api/admin/campaigns/{slug}/coordination": "write_account",
	"GET /api/campaigns":                            "anon_ip",
}

func TestEveryRouteDeclaresItsCeiling(t *testing.T) {
	s, _ := testServer(t)
	seen := map[string]bool{}
	err := chi.Walk(s.router(), func(method, route string, _ http.Handler,
		_ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		if route == "/api" || route == "" || !strings.HasPrefix(route, "/") {
			return nil
		}
		if !strings.HasPrefix(route, "/api") && !strings.HasPrefix(route, "/health") {
			return nil // the interface fallback, cached files, not the API
		}
		key := method + " " + route
		seen[key] = true
		if _, declared := routeLimits[key]; !declared {
			t.Errorf("%s carries no line in routeLimits: say which ceiling "+
				"covers it, or why none does", key)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) < 15 {
		t.Fatalf("only %d routes walked: the tree was not read", len(seen))
	}
	for key := range routeLimits {
		if !seen[key] {
			t.Errorf("routeLimits describes %s, which is not a route any "+
				"more: the next route under that name would inherit its claim", key)
		}
	}
}

// routeLimits declares; this drives. The map above says "write_account"
// beside nine routes and nothing confronted that word with the middleware
// actually hung on them — dropping `guard(s.limitAccount(limitWriteAccount))`
// from any one of them left every test green.
//
// Cheap because the class is keyed on the ACCOUNT: all nine share one
// bucket. Exhaust it once, and each of the others must answer 429 without
// its handler running. A route whose ceiling was lost answers its handler
// instead — 200, 400, 404, anything but 429.
func TestEveryWriteRouteIsActuallyUnderItsCeiling(t *testing.T) {
	s, srv := testServer(t)
	email := "coord@exemple.fr"
	password := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, password); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}

	// exhausted through ONE route, asserted at the crossing so the rest of
	// the test cannot rest on a bucket that was never full
	for i := 1; i <= limitWriteAccount.events; i++ {
		if code, _ := c.call(http.MethodPost, "/api/me/personal_note",
			map[string]any{"note": "x"}); code == http.StatusTooManyRequests {
			t.Fatalf("429 at attempt %d, below the ceiling of %d",
				i, limitWriteAccount.events)
		}
	}
	if code, _ := c.call(http.MethodPost, "/api/me/personal_note",
		map[string]any{"note": "x"}); code != http.StatusTooManyRequests {
		t.Fatalf("the bucket did not fill: attempt %d answered %d",
			limitWriteAccount.events+1, code)
	}

	// every OTHER route the map declares under the same class, reachable by
	// a coordination inside its campaign. `{…}` placeholders take harmless
	// values: a wired route refuses before its handler ever reads them.
	reachable := map[string]string{
		"POST /api/mayors/{insee}/status":         "/api/mayors/01001/status",
		"POST /api/mayors/{insee}/notes/{id}":     "/api/mayors/01001/notes/1",
		"DELETE /api/mayors/{insee}/notes/{id}":   "/api/mayors/01001/notes/1",
		"POST /api/batch":                         "/api/batch",
		"POST /api/team/group":                    "/api/team/group",
		"POST /api/team/group/{id}":               "/api/team/group/1",
		"POST /api/team/account":                  "/api/team/account",
		"POST /api/team/account/{email}/active":   "/api/team/account/qui@exemple.fr/active",
		"POST /api/team/account/{email}/role":     "/api/team/account/qui@exemple.fr/role",
		"POST /api/team/account/{email}/password": "/api/team/account/qui@exemple.fr/password",
		"POST /api/team/requests/{id}":            "/api/team/requests/1",
		"POST /api/campaign":                      "/api/campaign",
		"POST /api/campaign/templates":            "/api/campaign/templates",
		"POST /api/campaign/logo":                 "/api/campaign/logo",
		"DELETE /api/campaign/logo":               "/api/campaign/logo",
	}
	for declared, class := range routeLimits {
		if class != "write_account" || declared == "POST /api/me/personal_note" {
			continue
		}
		path, covered := reachable[declared]
		if !covered {
			// the administration routes: another scope, another test
			if strings.HasPrefix(declared, "POST /api/admin/") {
				continue
			}
			// a lead's own routes: another ROLE, and the role guard sits
			// before the ceiling, so a coordination gets 403 here and proves
			// nothing. Skipped by the SAME list that drives them one test
			// down, never by a name written twice.
			if _, isLead := leadWriteRoutes[declared]; isLead {
				continue
			}
			t.Errorf("%s is declared write_account and no case drives it: a "+
				"ceiling nothing exercises is a ceiling nothing keeps", declared)
			continue
		}
		// the METHOD the map declares, not POST: a route driven with the
		// wrong verb answers 405 from the mux, before any limiter, and the
		// case would then pass while exercising nothing
		method, _, _ := strings.Cut(declared, " ")
		if code, _ := c.call(method, path, map[string]any{}); code !=
			http.StatusTooManyRequests {
			t.Errorf("%s answered %d with the write_account bucket exhausted: "+
				"it carries no ceiling any more", declared, code)
		}
	}
}

// The routes ONE TEAM'S LEAD reaches and a coordination does not. One list,
// two readers: the walk above skips exactly what this one drives, so a lead
// route added here cannot fall out of both.
var leadWriteRoutes = map[string]string{
	"POST /api/team/templates": "/api/team/templates",
}

// A lead's own write routes, under the same ceiling, driven by a lead —
// `leadOnly` sits BEFORE the limiter, so a coordination answers 403 there and
// proves nothing about any ceiling. Same lesson as the administration walk
// below, one role over.
func TestEveryLeadWriteRouteIsUnderItsCeiling(t *testing.T) {
	s, srv := testServer(t)
	team := createTeam(t, s, "Nord", "01")
	pw := createAccount(t, s, "referent@exemple.fr", RoleLead, &team)
	c := newClient(t, srv)
	if code := c.signIn("referent@exemple.fr", pw); code != http.StatusOK {
		t.Fatalf("lead sign-in: %d", code)
	}
	// filled through a route EVERY role reaches, so the bucket is full before
	// the first lead-only route is touched — and asserted at the crossing, or
	// the walk below would rest on a bucket that was never filled
	for i := 1; i <= limitWriteAccount.events; i++ {
		if code, _ := c.call(http.MethodPost, "/api/me/personal_note",
			map[string]any{"note": "x"}); code == http.StatusTooManyRequests {
			t.Fatalf("429 at attempt %d, below the ceiling of %d",
				i, limitWriteAccount.events)
		}
	}
	if code, _ := c.call(http.MethodPost, "/api/me/personal_note",
		map[string]any{"note": "x"}); code != http.StatusTooManyRequests {
		t.Fatalf("the bucket did not fill: attempt %d answered %d",
			limitWriteAccount.events+1, code)
	}

	for declared, path := range leadWriteRoutes {
		if class := routeLimits[declared]; class != "write_account" {
			t.Errorf("%s is driven here as a write_account route but declares "+
				"%q", declared, class)
			continue
		}
		method, _, _ := strings.Cut(declared, " ")
		if code, _ := c.call(method, path, map[string]any{}); code !=
			http.StatusTooManyRequests {
			t.Errorf("%s answered %d with the write_account bucket exhausted: "+
				"it carries no ceiling any more", declared, code)
		}
	}
}

// The administration write routes, in the scope that reaches them: the
// administration signs in on the apex, and `administrationOnly` sits BEFORE
// the ceiling — so a coordination gets 403 there and proves nothing.
//
// Walked against routeLimits rather than written out. Enumerated by hand it
// named two routes, and the test one function up skips every `POST
// /api/admin/` on the grounds that this one covers them: a third arrived
// declared write_account with nothing driving it, and both tests stayed
// green. A list that decides what is checked, beside a list that decides
// what is protected, loses both in one edit.
func TestEveryAdministrationWriteRouteIsUnderItsCeiling(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	execAsMaintenance(t, s,
		"INSERT INTO accounts(org_id, email, name, password_hash, role) VALUES($1,$2,$3,$4,$5)",
		OrgInstance, "admin@paraphe.test", "Administration",
		testHash(t, "mot-de-passe-admin"), RoleAdministration)
	admin := clientOn(t, srv, "paraphe.test")
	if code := admin.signIn("admin@paraphe.test", "mot-de-passe-admin"); code != http.StatusOK {
		t.Fatalf("administration sign-in: %d", code)
	}
	// `{…}` placeholders take harmless values: a wired route refuses before
	// its handler ever reads them.
	reachable := map[string]string{
		"POST /api/admin/campaigns":                     "/api/admin/campaigns",
		"POST /api/admin/requests/{id}":                 "/api/admin/requests/1",
		"POST /api/admin/campaigns/{slug}/coordination": "/api/admin/campaigns/inconnue/coordination",
	}
	const filler = "POST /api/admin/campaigns"

	for i := 1; i <= limitWriteAccount.events; i++ {
		if code, _ := admin.call(http.MethodPost, reachable[filler],
			map[string]any{}); code == http.StatusTooManyRequests {
			t.Fatalf("429 at attempt %d, below the ceiling of %d",
				i, limitWriteAccount.events)
		}
	}
	if code, _ := admin.call(http.MethodPost, reachable[filler],
		map[string]any{}); code != http.StatusTooManyRequests {
		t.Fatalf("%s: the bucket did not fill (%d)", filler, code)
	}

	for declared, class := range routeLimits {
		if class != "write_account" ||
			!strings.HasPrefix(declared, "POST /api/admin/") || declared == filler {
			continue
		}
		path, covered := reachable[declared]
		if !covered {
			t.Errorf("%s is declared write_account and no case drives it: a "+
				"ceiling nothing exercises is a ceiling nothing keeps", declared)
			continue
		}
		if code, _ := admin.call(http.MethodPost, path,
			map[string]any{}); code != http.StatusTooManyRequests {
			t.Errorf("%s answered %d with the bucket exhausted: it carries no "+
				"ceiling any more", declared, code)
		}
	}
}

// The anonymous class, same shape: two routes, one bucket per source.
func TestEveryAnonymousRouteIsUnderItsCeiling(t *testing.T) {
	_, srv := testServer(t)
	c := newClient(t, srv)
	for i := 1; i <= limitAnonIP.events; i++ {
		if code, _ := c.call(http.MethodGet, "/api/config", nil); code ==
			http.StatusTooManyRequests {
			t.Fatalf("429 at attempt %d, below the ceiling of %d",
				i, limitAnonIP.events)
		}
	}
	if code, _ := c.call(http.MethodGet, "/api/config", nil); code !=
		http.StatusTooManyRequests {
		t.Fatalf("the anon bucket did not fill (%d)", code)
	}
	if code, _ := c.call(http.MethodGet, "/api/campaign/public", nil); code !=
		http.StatusTooManyRequests {
		t.Errorf("GET /api/campaign/public answered %d with the anon_ip "+
			"bucket exhausted: it carries no ceiling any more", code)
	}
}

// The apex directory is the instance's front door: anonymous, and it queries
// the database. Its line in routeLimits once read « none: public read, same
// class as /api/config » — prose naming a ceiling, a value declaring none,
// and the canary believes the value. Every OTHER route declared « none » is
// an authenticated read; this was the only anonymous one.
func TestTheApexDirectoryIsUnderTheAnonymousCeiling(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	_, srv := testServer(t)
	c := clientOn(t, srv, "paraphe.test")
	for i := 1; i <= limitAnonIP.events; i++ {
		if code, _ := c.call(http.MethodGet, "/api/campaigns", nil); code ==
			http.StatusTooManyRequests {
			t.Fatalf("429 at attempt %d, below the ceiling of %d",
				i, limitAnonIP.events)
		}
	}
	if code, _ := c.call(http.MethodGet, "/api/campaigns", nil); code !=
		http.StatusTooManyRequests {
		t.Fatalf("the apex directory answered %d with the anon bucket "+
			"exhausted: it carries no ceiling", code)
	}
}

// The ceilings, exercised end to end — one route per class, driven to the
// crossing and one step past it. What is asserted is the ANSWER: the 429,
// its Retry-After, and — where a write was refused — the absence of the row.

func TestSignInCeilingPerSource(t *testing.T) {
	_, srv := testServer(t)
	c := newClient(t, srv)
	// distinct addresses so the per-account ceiling (10) stays out of the
	// way: what is driven here is the per-source one (20)
	for i := 1; i <= limitSignInIP.events; i++ {
		code, _ := c.call(http.MethodPost, "/api/session", map[string]string{
			"email": fmt.Sprintf("probe%d@exemple.fr", i), "password": "wrong"})
		if code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d, want 401 below the ceiling", i, code)
		}
	}
	code, body := c.call(http.MethodPost, "/api/session", map[string]string{
		"email": "probe-final@exemple.fr", "password": "wrong"})
	if code != http.StatusTooManyRequests {
		t.Fatalf("attempt %d: %d, want 429 — the per-source ceiling did not hold",
			limitSignInIP.events+1, code)
	}
	if body["error"] == "" {
		t.Fatal("the 429 carries no readable sentence")
	}
}

func TestSignInCeilingPerAccountIsBlindToExistence(t *testing.T) {
	s, srv := testServer(t)
	createAccount(t, s, "existing@exemple.fr", RoleVolunteer, nil)

	attempt := func(c *client, email string) (int, string) {
		t.Helper()
		resp, err := c.http.Do(c.request(http.MethodPost, "/api/session",
			map[string]string{"email": email, "password": "wrong-password-42"}))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&out)
		// the sentence, not the Retry-After seconds: the message is
		// minute-coarse by design, the seconds shift with the clock and
		// would make this comparison flaky without telling anyone anything
		if resp.StatusCode == http.StatusTooManyRequests &&
			resp.Header.Get("Retry-After") == "" {
			t.Fatal("a 429 without Retry-After")
		}
		return resp.StatusCode, out["error"]
	}

	// Driving two addresses to the ACCOUNT ceiling takes 22 attempts, which
	// would trip the per-source ceiling (20) first and prove nothing about
	// this one. The source counter is cleared between the two runs: what is
	// under test is the account ceiling, alone.
	clearSource := func() {
		s.limiter.forget(context.Background(), limitSignInIP, "127.0.0.1")
	}

	drive := func(email string) (int, string) {
		var code int
		var shape string
		for i := 1; i <= limitSignInAccount.events+1; i++ {
			code, shape = attempt(newClient(t, srv), email)
		}
		return code, shape
	}
	codeExisting, shapeExisting := drive("existing@exemple.fr")
	clearSource()
	codeGhost, shapeGhost := drive("ghost@exemple.fr")

	if codeExisting != http.StatusTooManyRequests || codeGhost != http.StatusTooManyRequests {
		t.Fatalf("attempt %d: existing=%d ghost=%d, want 429 for both",
			limitSignInAccount.events+1, codeExisting, codeGhost)
	}
	// The whole point: the ceiling must not become the enumeration oracle
	// the decoy hash closed. Same code, same sentence, same shape — the
	// Retry-After is compared inside the shape, so a timing hint would show.
	if shapeExisting != shapeGhost {
		t.Fatalf("the 429 differs between an existing account (%q) and a "+
			"nonexistent one (%q): the limiter answers the question the decoy "+
			"hash exists to refuse", shapeExisting, shapeGhost)
	}
}

// Signing in costs nothing, and erases nothing.
//
// The ceiling counts every attempt against the submitted address, SUCCESSES
// INCLUDED, so a door that only counted would lock an account out of its own
// password after ten legitimate sign-ins — twice what a volunteer does in a
// day of switching between a phone and a desk, and the end-to-end journeys
// hit it in under a minute.
//
// Clearing the counter fixes that and opens what
// TestBurnedCeilingDoesNotAnnounceThatSomebodySignedIn walks. Refunding the
// attempt is the third answer: the success is counted like everything else
// and then given back, so it costs nothing — and it erases nothing either,
// which is exactly why the bucket says nothing about the owner.
func TestSignInSuccessCostsNothingAndErasesNothing(t *testing.T) {
	s, srv := testServer(t)
	password := createAccount(t, s, "team-box@exemple.fr", RoleVolunteer, nil)
	c := newClient(t, srv)

	// Twice the ceiling in correct sign-ins, one after another: none of them
	// may bring the account any closer to being locked out.
	for i := 1; i <= 2*limitSignInAccount.events; i++ {
		if code := c.signIn("team-box@exemple.fr", password); code != http.StatusOK {
			t.Fatalf("legitimate sign-in %d answered %d: signing in spends "+
				"the ceiling that exists to bound GUESSING, and an account is "+
				"locked out of its own password by using it", i, code)
		}
	}

	// And the fumbles still count. All of them, up to the ceiling, whatever
	// happened in between — a success that erased them is a success an
	// attacker could watch for.
	for i := 1; i <= limitSignInAccount.events; i++ {
		if code := c.signIn("team-box@exemple.fr", "wrong"); code != http.StatusUnauthorized {
			t.Fatalf("fumble %d answered %d, want 401 below the ceiling", i, code)
		}
	}
	if code := c.signIn("team-box@exemple.fr", "wrong"); code != http.StatusTooManyRequests {
		t.Errorf("the ceiling did not close after %d wrong passwords (%d): "+
			"the successes in between erased them", limitSignInAccount.events, code)
	}
}

func TestHostingFormCeilingPerSource(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	c := clientOn(t, srv, "paraphe.test")
	// invalid bodies on purpose: the ceiling counts ATTEMPTS — an attacker
	// probing with garbage is still probing — and nothing may be written
	for i := 1; i <= limitHostingIP.events; i++ {
		code, _ := c.call(http.MethodPost, "/api/request", map[string]string{"slug": "!!"})
		if code != http.StatusBadRequest {
			t.Fatalf("attempt %d: %d, want 400 below the ceiling", i, code)
		}
	}
	code, _ := c.call(http.MethodPost, "/api/request", map[string]string{"slug": "!!"})
	if code != http.StatusTooManyRequests {
		t.Fatalf("attempt %d: %d, want 429", limitHostingIP.events+1, code)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM hosting_requests"); n != 0 {
		t.Fatalf("%d hosting requests written by refused attempts", n)
	}
}

func TestAnonymousReadCeilingSparesTheProbes(t *testing.T) {
	_, srv := testServer(t)
	c := newClient(t, srv)
	for i := 1; i <= limitAnonIP.events; i++ {
		code, _ := c.call(http.MethodGet, "/api/config", nil)
		if code != http.StatusOK {
			t.Fatalf("read %d: %d below the ceiling", i, code)
		}
	}
	code, resp := c.call(http.MethodGet, "/api/config", nil)
	if code != http.StatusTooManyRequests {
		t.Fatalf("read %d: %d, want 429", limitAnonIP.events+1, code)
	}
	_ = resp
	// the probes live OUTSIDE every ceiling: a kubelet asks relentlessly,
	// and a probe refused for zeal is a pod restarted for nothing
	for i := range limitAnonIP.events + 5 {
		if code, _ := c.call(http.MethodGet, "/health", nil); code != http.StatusOK {
			t.Fatalf("liveness probe %d answered %d: the ceiling reached the "+
				"probes", i, code)
		}
	}
}

func TestWriteCeilingPerAccount(t *testing.T) {
	s, srv := testServer(t)
	password := createAccount(t, s, "writer@exemple.fr", RoleVolunteer, nil)
	c := newClient(t, srv)
	if code := c.signIn("writer@exemple.fr", password); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	for i := 1; i <= limitWriteAccount.events; i++ {
		code, _ := c.call(http.MethodPost, "/api/me/personal_note",
			map[string]string{"personal_note": "note"})
		if code != http.StatusOK {
			t.Fatalf("write %d: %d below the ceiling", i, code)
		}
	}
	code, _ := c.call(http.MethodPost, "/api/me/personal_note",
		map[string]string{"personal_note": "one too many"})
	if code != http.StatusTooManyRequests {
		t.Fatalf("write %d: %d, want 429", limitWriteAccount.events+1, code)
	}
	if got := scalar[string](t, s, "SELECT personal_note FROM accounts WHERE "+
		"org_id=$1 AND email='writer@exemple.fr'", orgID(t, s, testSlug)); got != "note" {
		t.Fatalf("the refused write reached the row: %q", got)
	}
}

func TestExportCeilingPerAccount(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 2, "01")
	password := createAccount(t, s, "exporter@exemple.fr", RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn("exporter@exemple.fr", password); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	for i := 1; i <= limitExportAccount.events; i++ {
		code, _ := c.call(http.MethodGet, "/api/export.csv", nil)
		if code != http.StatusOK {
			t.Fatalf("export %d: %d below the ceiling", i, code)
		}
	}
	code, _ := c.call(http.MethodGet, "/api/export.csv", nil)
	if code != http.StatusTooManyRequests {
		t.Fatalf("export %d: %d, want 429 — this is the whole table, %d times "+
			"a window is enough for anyone", limitExportAccount.events+1, code,
			limitExportAccount.events)
	}
}

func TestRefusalCarriesRetryAfterAndCORSWhereDue(t *testing.T) {
	_, srv := testServer(t)
	c := newClient(t, srv)
	var last *http.Response
	for range limitAnonIP.events + 1 {
		resp, err := c.http.Do(c.request(http.MethodGet, "/api/campaign/public", nil))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		last = resp
	}
	if last.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("final read: %d, want 429", last.StatusCode)
	}
	if last.Header.Get("Retry-After") == "" {
		t.Fatal("the 429 says nothing about when to come back")
	}
	// this route is read cross-origin by the browser version: without the
	// header on the REFUSAL too, the browser hides the sentence and shows
	// « Failed to fetch »
	if last.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("the 429 dropped the CORS header the route promises every origin")
	}
	if cc := last.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control on an API answer: %q, want no-store", cc)
	}
}

// A ceiling an anonymous caller can fill for an address of their choosing
// must not report when that address turns out to name somebody.
//
// Burned on the SAME door the owner then comes through, which is the hard
// case: clearing the counter on success made its reopening say "somebody
// just signed in as this address". Refunding the attempt instead leaves the
// bucket exactly where it stood, so the walk below — burn a real address and
// a ghost, sign in as the real one, count how many attempts the ceiling
// still allows — answers the same for both.
// The door refunded is the one THIS ROUTE counted — which is not the door
// the caller came through.
//
// Redeeming a link counts nothing per account: it carries a 256-bit token
// and no address, so the source ceiling is its whole bound and the router
// says so. Refunding the link-REQUEST ceiling there gives back an event the
// redeem never spent, and that credit is addressable by anyone who knows an
// e-mail: burn the request ceiling for it, wait, and the slot that reopens
// says its owner has just clicked their link.
func TestRedeemingALinkRefundsNothingItDidNotSpend(t *testing.T) {
	s, srv := testServer(t)
	withMailer(t, s, "https://campagne.exemple.fr")
	const real = "marie@exemple.fr"
	const ghost = "personne@exemple.fr"
	createAccount(t, s, real, RoleVolunteer, nil)

	// An INVITATION, because the purpose is part of the key: the attacker's
	// requests below mint sign-in links, and each one replaces the last, so a
	// sign-in link taken before the burn would be dead by the time its owner
	// clicked it. An invitation survives them, which is the shape a volunteer
	// meets in practice — invited on Monday, clicking on Tuesday.
	org := orgID(t, s, testSlug)
	var token string
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		var err error
		if token, err = s.mintInvitation(context.Background(), tx, org, real); err != nil {
			t.Fatal(err)
		}
	})

	// The attacker spends what is left of the ceiling on both addresses.
	burn := func(address string) {
		attacker := newClient(t, srv)
		// EXACTLY to the ceiling and no further: driven well past it, one
		// refunded event does not bring the bucket back under, and the walk
		// would come out even for the wrong reason.
		for i := 1; i <= limitMagicLinkAccount.events; i++ {
			if code, _ := attacker.call(http.MethodPost, "/api/session/link",
				map[string]string{"email": address}); code != http.StatusOK {
				t.Fatalf("burning %s, request %d: %d", address, i, code)
			}
		}
	}
	burn(real)
	burn(ghost)

	// The owner clicks their link.
	if code, _ := redeem(t, newClient(t, srv), token); code != http.StatusOK {
		t.Fatalf("the owner could not use their link: %d", code)
	}

	// And the attacker asks again for each address.
	after := func(address string) int {
		code, _ := newClient(t, srv).call(http.MethodPost, "/api/session/link",
			map[string]string{"email": address})
		return code
	}
	realAfter, ghostAfter := after(real), after(ghost)
	if realAfter != ghostAfter {
		t.Errorf("the burned ceiling answers %d for an address whose owner "+
			"has just redeemed a link and %d for one nobody bears: redeeming "+
			"refunded a request it never made, and the ceiling reports the "+
			"click", realAfter, ghostAfter)
	}
}

func TestBurnedCeilingDoesNotAnnounceThatSomebodySignedIn(t *testing.T) {
	s, srv := testServer(t)
	const real = "marie@exemple.fr"
	const ghost = "personne@exemple.fr"
	password := createAccount(t, s, real, RoleVolunteer, nil)

	// The attacker spends most of the window on each address, from their own
	// source so the per-source ceiling is not what answers.
	burn := func(address string) {
		attacker := newClient(t, srv)
		for i := 1; i < limitSignInAccount.events; i++ {
			if code := attacker.signIn(address, "wrong"); code != http.StatusUnauthorized {
				t.Fatalf("burning %s, attempt %d: %d", address, i, code)
			}
		}
	}
	burn(real)
	burn(ghost)

	// The owner comes through the same door.
	if code := newClient(t, srv).signIn(real, password); code != http.StatusOK {
		t.Fatalf("the owner could not sign in: %d", code)
	}

	// And the attacker measures what is left of each ceiling.
	remaining := func(address string) int {
		attacker := newClient(t, srv)
		for n := 0; ; n++ {
			if code := attacker.signIn(address, "wrong"); code == http.StatusTooManyRequests {
				return n
			}
			if n > limitSignInAccount.events {
				t.Fatalf("the ceiling for %s never closed", address)
			}
		}
	}
	realLeft, ghostLeft := remaining(real), remaining(ghost)
	if realLeft != ghostLeft {
		t.Errorf("the burned ceiling allows %d more attempts for an address "+
			"whose owner has just signed in and %d for one nobody bears: "+
			"whoever fills it for an address learns, by counting what is "+
			"left, the moment that address turns out to name somebody — "+
			"which the constant sentence and the decoy hash exist to refuse",
			realLeft, ghostLeft)
	}
}
