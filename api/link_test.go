package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// captureMailer is the relay, read back. It dials nothing: what these tests
// need to know is what would have left, and to whom.
type captureMailer struct {
	mu   sync.Mutex
	sent []sentMail
	// fail: a relay that refuses, for the cases where that must be visible
	// to the caller (invitations) and invisible to it (sign-in links).
	fail error
	// hold: a relay that answers nothing until this channel closes — what a
	// slow or dead one looks like from here.
	hold chan struct{}
}

type sentMail struct{ to, subject, body string }

func (m *captureMailer) Send(ctx context.Context, to, subject, body string) error {
	m.mu.Lock()
	hold, fail := m.hold, m.fail
	m.mu.Unlock()
	// a relay that has stopped answering, which is the state that matters
	// for what a send is allowed to be holding while it waits
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if fail != nil {
		return fail
	}
	m.sent = append(m.sent, sentMail{to, subject, body})
	return nil
}

func (m *captureMailer) all() []sentMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]sentMail(nil), m.sent...)
}

// only: the single message that must have gone out, or a failure naming what
// did instead.
func (m *captureMailer) only(t *testing.T) sentMail {
	t.Helper()
	all := m.all()
	if len(all) != 1 {
		t.Fatalf("expected exactly one message, %d went out: %+v", len(all), all)
	}
	return all[0]
}

// withMailer gives the server a relay it can read back, and the origin its
// links are built from.
func withMailer(t *testing.T, s *Server, origin string) *captureMailer {
	t.Helper()
	public, err := parsePublicURL(origin)
	if err != nil {
		t.Fatal(err)
	}
	m := &captureMailer{}
	s.mailer, s.publicURL = m, public
	return m
}

// tokenIn reads the token out of a message, the way its recipient's browser
// will: after the fragment marker.
func tokenIn(t *testing.T, body string) string {
	t.Helper()
	_, after, found := strings.Cut(body, "#jeton=")
	if !found {
		t.Fatalf("no sign-in link in this message:\n%s", body)
	}
	return strings.TrimSpace(strings.SplitN(after, "\n", 2)[0])
}

// askForLink requests one and returns the token that reached the inbox.
func askForLink(t *testing.T, s *Server, c *client, email string) string {
	t.Helper()
	mails, ok := s.mailer.(*captureMailer)
	if !ok {
		t.Fatal("this server has no capture mailer")
	}
	before := len(mails.all())
	if code, body := c.call(http.MethodPost, "/api/session/link",
		map[string]string{"email": email}); code != http.StatusOK {
		t.Fatalf("asking for a link: %d %v", code, body)
	}
	s.outbound.Wait()
	all := mails.all()
	if len(all) != before+1 {
		t.Fatalf("asking for a link sent %d messages", len(all)-before)
	}
	return tokenIn(t, all[len(all)-1].body)
}

func redeem(t *testing.T, c *client, token string) (int, map[string]any) {
	t.Helper()
	return c.call(http.MethodPost, "/api/session/link/redeem",
		map[string]string{"token": token})
}

// The whole point of the constant answer: whether the address names an
// account or nothing at all, the caller reads the same status and the same
// body. A difference of one word here would turn this route into a roster of
// the campaign's volunteers, readable by anyone.
func TestAnUnknownAddressIsAnsweredExactlyLikeAKnownOne(t *testing.T) {
	s, srv := testServer(t)
	mails := withMailer(t, s, "https://campagne.exemple.fr")
	createAccount(t, s, "connue@exemple.fr", RoleVolunteer, nil)

	c := newClient(t, srv)
	knownCode, known := c.call(http.MethodPost, "/api/session/link",
		map[string]string{"email": "connue@exemple.fr"})
	unknownCode, unknown := c.call(http.MethodPost, "/api/session/link",
		map[string]string{"email": "personne@exemple.fr"})
	s.outbound.Wait()

	if knownCode != http.StatusOK {
		t.Fatalf("a known address was answered %d: %v", knownCode, known)
	}
	if unknownCode != knownCode {
		t.Errorf("an unknown address was answered %d where a known one got %d",
			unknownCode, knownCode)
	}
	// The WHOLE body, compared as it arrived: a difference of one word here
	// — or one field added later to only one branch — is what would turn
	// this route into a roster of the campaign's volunteers.
	if !reflect.DeepEqual(known, unknown) {
		t.Errorf("the two answers differ:\n known: %v\n unknown: %v", known, unknown)
	}
	// and nothing left for the address that names nobody
	sent := mails.only(t)
	if sent.to != "connue@exemple.fr" {
		t.Errorf("a message went to %q", sent.to)
	}
}

// The answer is identical; so is the WORK done before it.
//
// The constant sentence closes the enumeration channel on the wire, and a
// stopwatch reopens it: minting is a DELETE, an INSERT and a COMMIT, and
// while they sat before the reply an existing address answered three to six
// times slower than an unknown one. Nothing between the request and the
// answer may depend on whether the account exists.
//
// Measured as a RATIO of medians, which is what an attacker averages towards
// — absolute times mean nothing on a loaded machine.
//
// Measured through the ROUTER rather than over a socket, deliberately: TLS
// and the loopback cost the same milliseconds to both branches, and averaged
// into the ratio they hide the very difference this test exists to see. The
// first shape of this test went over HTTPS, could not tell the defect from
// the fix, and would have certified it.
func TestAnExistingAddressIsNoSlowerThanAnUnknownOne(t *testing.T) {
	s, _ := testServer(t)
	withMailer(t, s, "https://campagne.exemple.fr")
	handler := s.routes()

	// One address AND one source per sample: both ceilings are narrow by
	// design and neither is what is under test here. Believing a forwarded
	// address is what a deployment behind an ingress does anyway.
	proxies, err := parseTrustedProxies("192.0.2.0/24")
	if err != nil {
		t.Fatal(err)
	}
	s.proxies = proxies

	const samples = 25
	for i := range samples {
		createAccount(t, s, fmt.Sprintf("connue-%d@exemple.fr", i),
			RoleVolunteer, nil)
	}
	ask := func(email, from string) time.Duration {
		body, err := json.Marshal(map[string]string{"email": email})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/session/link",
			strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", from)
		req.RemoteAddr = "192.0.2.1:1234" // a trusted proxy, so the header counts
		rec := httptest.NewRecorder()
		start := time.Now()
		handler.ServeHTTP(rec, req)
		elapsed := time.Since(start)
		if rec.Code != http.StatusOK {
			t.Fatalf("asking for a link for %s: %d %s", email, rec.Code, rec.Body)
		}
		return elapsed
	}
	// A round of each first: the pool, the statement cache and the limiter's
	// map all cost something the first time, to whichever branch goes first.
	ask("connue-0@exemple.fr", "203.0.113.1")
	ask("inconnue-0@exemple.fr", "203.0.113.2")

	var hit, miss []time.Duration
	for i := range samples {
		// alternating, so that a machine which slows down halfway through
		// slows down for both
		from := fmt.Sprintf("203.0.113.%d", i+10)
		hit = append(hit, ask(fmt.Sprintf("connue-%d@exemple.fr", i), from))
		miss = append(miss, ask(fmt.Sprintf("inconnue-%d@exemple.fr", i), from))
	}
	s.outbound.Wait()

	median := func(d []time.Duration) time.Duration {
		slices.Sort(d)
		return d[len(d)/2]
	}
	withAccount, without := median(hit), median(miss)
	ratio := float64(withAccount) / float64(without)
	// Logged whatever happens: when this fails in CI, the two numbers are
	// the finding.
	t.Logf("median with an account %s, without %s, ratio %.2f",
		withAccount, without, ratio)
	// 1.5 sits between the two regimes MEASURED on this code: minting before
	// the reply gave 2.0 here (and 3.5x to 6.5x over a socket), minting after
	// it gives 0.93 to 1.06. A threshold at 2.0 was tried first and passed
	// the defect one run in two.
	if ratio > 1.5 {
		t.Errorf("an address that names an account answers %.1fx slower "+
			"(%s against %s): work that differs between the two branches is "+
			"happening before the reply, and it hands an attacker the roster "+
			"the wording withholds", ratio, withAccount, without)
	}
}

// A relay that stopped answering must not take the application down with it.
//
// The mint moved out of the request to close a timing channel, and it takes
// a pool connection of its own to do it. If that connection were held for
// the whole SMTP exchange — up to thirty seconds — then more sends than the
// pool has connections would hang every other request in the campaign, for
// free, from a public route. pgx sizes the pool from NumCPU, so the burst is
// sized from the pool rather than fixed: a number that proves nothing on a
// big builder proves everything on the small VPS the deployment doc
// recommends. This is the shape TestADribblingBodyHoldsNoConnection already
// refuses on the request side.
func TestASlowRelayHoldsNoConnection(t *testing.T) {
	s, srv := testServer(t)
	mails := withMailer(t, s, "https://campagne.exemple.fr")
	mails.hold = make(chan struct{})
	defer close(mails.hold)

	proxies, err := parseTrustedProxies("192.0.2.0/24")
	if err != nil {
		t.Fatal(err)
	}
	s.proxies = proxies
	handler := s.routes()

	burst := int(s.pool.Config().MaxConns) + 4
	for i := range burst {
		createAccount(t, s, fmt.Sprintf("bloquee-%d@exemple.fr", i),
			RoleVolunteer, nil)
	}
	// CONCURRENTLY, the way traffic arrives. One at a time, a connection
	// held across the send would stall the LOOP rather than anything else,
	// and the assertion below would run once the stall had already passed.
	answered := make(chan int, burst)
	for i := range burst {
		go func() {
			body, err := json.Marshal(map[string]string{
				"email": fmt.Sprintf("bloquee-%d@exemple.fr", i)})
			if err != nil {
				answered <- 0
				return
			}
			req := httptest.NewRequest(http.MethodPost, "/api/session/link",
				strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1))
			req.RemoteAddr = "192.0.2.1:1234"
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			answered <- rec.Code
		}()
	}
	for i := range burst {
		select {
		case code := <-answered:
			if code != http.StatusOK {
				t.Fatalf("a request in the burst answered %d", code)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of %d requests answered: the pool is held by "+
				"work waiting on the relay", i, burst)
		}
	}

	// every one of those sends is now stuck in the relay; the application
	// must still answer, and answer FROM THE DATABASE
	done := make(chan int, 1)
	go func() {
		code, _ := newClient(t, srv).call(http.MethodGet, "/api/config", nil)
		done <- code
	}()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Errorf("/api/config answered %d with %d sends waiting on the relay",
				code, burst)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("/api/config never answered with %d sends waiting on the "+
			"relay: the pool is held by work that is waiting on the outside "+
			"world", burst)
	}
}

func TestASignInLinkOpensASessionExactlyOnce(t *testing.T) {
	s, srv := testServer(t)
	withMailer(t, s, "https://campagne.exemple.fr")
	const email = "marie@exemple.fr"
	createAccount(t, s, email, RoleVolunteer, nil)

	c := newClient(t, srv)
	token := askForLink(t, s, c, email)

	code, body := redeem(t, c, token)
	if code != http.StatusOK {
		t.Fatalf("redeeming a fresh link: %d %v", code, body)
	}
	account, _ := body["account"].(map[string]any)
	if account["email"] != email {
		t.Errorf("the session opened for %v", account["email"])
	}
	// the cookie is really set: the session exists, it is not just a 200
	if code, _ := c.call(http.MethodGet, "/api/me", nil); code != http.StatusOK {
		t.Errorf("GET /api/me after redeeming a link: %d", code)
	}

	// Single use. A link travels through an inbox, a phone backup and a
	// corporate scanner: it opens one session, and the second attempt is a
	// stranger holding a copy.
	second := newClient(t, srv)
	if code, _ := redeem(t, second, token); code != http.StatusUnauthorized {
		t.Errorf("the same token opened a second session: %d", code)
	}
}

func TestAnExpiredLinkIsRefused(t *testing.T) {
	s, srv := testServer(t)
	withMailer(t, s, "https://campagne.exemple.fr")
	const email = "marie@exemple.fr"
	createAccount(t, s, email, RoleVolunteer, nil)

	c := newClient(t, srv)
	token := askForLink(t, s, c, email)

	// The server's clock, not PostgreSQL's: that is what makes an expiry
	// something a test can demonstrate rather than wait for.
	later := time.Now().Add(signInLinkLife + time.Minute)
	s.now = func() time.Time { return later }
	if code, _ := redeem(t, c, token); code != http.StatusUnauthorized {
		t.Errorf("a link %s old was accepted: %d", signInLinkLife+time.Minute, code)
	}
}

// Asking for a new link invalidates the one before it. Which is also what
// bounds the table: one live row per address, whatever a script does.
func TestANewLinkKillsThePreviousOne(t *testing.T) {
	s, srv := testServer(t)
	withMailer(t, s, "https://campagne.exemple.fr")
	const email = "marie@exemple.fr"
	createAccount(t, s, email, RoleVolunteer, nil)

	c := newClient(t, srv)
	first := askForLink(t, s, c, email)
	second := askForLink(t, s, c, email)
	if first == second {
		t.Fatal("the same token was drawn twice")
	}
	if code, _ := redeem(t, c, first); code != http.StatusUnauthorized {
		t.Errorf("the superseded link still opened a session: %d", code)
	}
	if code, _ := redeem(t, newClient(t, srv), second); code != http.StatusOK {
		t.Errorf("the current link was refused: %d", code)
	}
	var live int
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"SELECT count(*) FROM login_tokens WHERE email=$1", email).Scan(&live); err != nil {
			t.Fatal(err)
		}
	})
	if live != 0 {
		t.Errorf("%d rows left after the link was used", live)
	}
}

// A deactivated account is in the same branch as an address nobody bears,
// and for the reason deactivation exists: the person holding the credential
// may be the attacker, and "your account is switched off" confirms the
// address to them.
func TestADeactivatedAccountGetsNoLinkAndHearsNothing(t *testing.T) {
	s, srv := testServer(t)
	mails := withMailer(t, s, "https://campagne.exemple.fr")
	const email = "partie@exemple.fr"
	createAccount(t, s, email, RoleVolunteer, nil)

	c := newClient(t, srv)
	// a link drawn while the account was live
	token := askForLink(t, s, c, email)
	execAsMaintenance(t, s, "UPDATE accounts SET active=false WHERE email=$1", email)

	if code, _ := redeem(t, c, token); code != http.StatusUnauthorized {
		t.Errorf("a link drawn before deactivation still opened a session: %d", code)
	}
	code, body := c.call(http.MethodPost, "/api/session/link",
		map[string]string{"email": email})
	s.outbound.Wait()
	if code != http.StatusOK {
		t.Errorf("a deactivated address was answered %d: %v", code, body)
	}
	if len(mails.all()) != 1 {
		t.Errorf("%d messages went out: a deactivated account was written to",
			len(mails.all()))
	}
}

// A REFUSED redemption spends the token too.
//
// The DELETE lives in the request's transaction, and a refusal that returns
// without committing rolls it back: the link comes back, still live. The
// branch that made it exploitable is the deactivated account — refused
// today, and opening a session the day the account is switched back on,
// for whoever kept a copy of the link. Seven days, for an invitation.
func TestARefusedRedemptionStillSpendsTheToken(t *testing.T) {
	s, srv := testServer(t)
	withMailer(t, s, "https://campagne.exemple.fr")
	const email = "marie@exemple.fr"
	createAccount(t, s, email, RoleVolunteer, nil)

	c := newClient(t, srv)
	token := askForLink(t, s, c, email)
	execAsMaintenance(t, s, "UPDATE accounts SET active=false WHERE email=$1", email)

	if code, _ := redeem(t, c, token); code != http.StatusUnauthorized {
		t.Fatalf("a deactivated account was let in: %d", code)
	}
	var left int
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"SELECT count(*) FROM login_tokens WHERE email=$1", email).
			Scan(&left); err != nil {
			t.Fatal(err)
		}
	})
	if left != 0 {
		t.Errorf("the refused token is still in the table: it was handed back "+
			"by the rollback, and it opens a session the day the account is "+
			"reactivated (%d row(s))", left)
	}

	// and the proof of what that costs: reactivate, and the same link must
	// no longer work
	execAsMaintenance(t, s, "UPDATE accounts SET active=true WHERE email=$1", email)
	if code, _ := redeem(t, newClient(t, srv), token); code != http.StatusUnauthorized {
		t.Errorf("the refused link opened a session after reactivation: %d", code)
	}
}

// Spending a token is ATOMIC: nothing that happens after it can hand the
// link back.
//
// It used to be deleted in the request's transaction, so everything that
// followed could undo it — a read that failed, a client that hung up
// mid-query, a commit that came back as a rollback. The route answered 500
// and the link was live again, for whoever held a copy. Here the account
// read is made to fail AFTER the token has been read, which is exactly that
// window.
func TestATokenSpentStaysSpentEvenWhenTheRequestThenFails(t *testing.T) {
	s, srv := testServer(t)
	withMailer(t, s, "https://campagne.exemple.fr")
	const email = "marie@exemple.fr"
	createAccount(t, s, email, RoleVolunteer, nil)

	c := newClient(t, srv)
	token := askForLink(t, s, c, email)

	// The account row is made unreadable in the way a real failure would be:
	// the column readAccount selects stops being a column.
	execAsMaintenance(t, s, "ALTER TABLE accounts RENAME COLUMN personal_note TO personal_note_x")
	code, _ := redeem(t, c, token)
	execAsMaintenance(t, s, "ALTER TABLE accounts RENAME COLUMN personal_note_x TO personal_note")
	if code != http.StatusInternalServerError {
		t.Fatalf("the broken read answered %d, so this test is no longer "+
			"exercising the window it was written for", code)
	}

	var left int
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"SELECT count(*) FROM login_tokens WHERE email=$1", email).
			Scan(&left); err != nil {
			t.Fatal(err)
		}
	})
	if left != 0 {
		t.Errorf("the token came back after the request failed: it was "+
			"presented, and it still opens a session for whoever kept a copy "+
			"(%d row(s))", left)
	}
	if code, _ := redeem(t, newClient(t, srv), token); code != http.StatusUnauthorized {
		t.Errorf("the token opened a session after the failed attempt: %d", code)
	}
}

// Redeeming takes NO second connection.
//
// The request already holds one, from inScope. Asking the pool for another
// while holding the first is how enough simultaneous redemptions all end up
// waiting for a connection none of them will release: measured, eight at
// once against a pool of four hung until they timed out — from a public
// route, with valid links, on the small VPS the deployment doc recommends.
func TestConcurrentRedemptionsDoNotStarveThePool(t *testing.T) {
	s, srv := testServer(t)
	withMailer(t, s, "https://campagne.exemple.fr")

	// MORE at once than the pool has connections, whatever this machine
	// decided: pgx sizes it from NumCPU.
	burst := int(s.pool.Config().MaxConns) + 4
	tokens := make([]string, burst)
	org := orgID(t, s, testSlug)
	for i := range burst {
		email := fmt.Sprintf("ensemble-%d@exemple.fr", i)
		createAccount(t, s, email, RoleVolunteer, nil)
		token, err := s.mintDetached(context.Background(), org, email)
		if err != nil {
			t.Fatal(err)
		}
		tokens[i] = token
	}

	done := make(chan int, burst)
	for i := range burst {
		go func() {
			code, _ := redeem(t, newClient(t, srv), tokens[i])
			done <- code
		}()
	}
	for i := range burst {
		select {
		case code := <-done:
			if code != http.StatusOK {
				t.Errorf("a redemption in the burst answered %d", code)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("only %d of %d redemptions answered: they are waiting on "+
				"a pool connection each other is holding", i, burst)
		}
	}
}

// ONE live link per address, whatever arrives together.
//
// The DELETE that precedes the INSERT cannot see a row a neighbouring
// transaction has not committed, so two requests in the same instant both
// inserted: two links in one inbox, and the older one already dead because
// the last mint deleted it. The unique index is what makes the promise true.
func TestConcurrentRequestsLeaveOneLiveLink(t *testing.T) {
	s, srv := testServer(t)
	withMailer(t, s, "https://campagne.exemple.fr")
	const email = "marie@exemple.fr"
	createAccount(t, s, email, RoleVolunteer, nil)

	// straight at the minting, which is where the race is; the route's own
	// per-address ceiling is three, and it is not what is under test
	org := orgID(t, s, testSlug)
	const racers = 6
	var wg sync.WaitGroup
	tokens := make([]string, racers)
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := s.mintDetached(context.Background(), org, email)
			if err == nil {
				tokens[i] = token
			}
		}()
	}
	wg.Wait()

	var live int
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"SELECT count(*) FROM login_tokens WHERE org_id=$1 AND email=$2",
			org, email).Scan(&live); err != nil {
			t.Fatal(err)
		}
	})
	if live != 1 {
		t.Errorf("%d live links for one address: the recipient gets several, "+
			"and all but one are dead on arrival", live)
	}
	// and exactly one of the tokens drawn actually opens a session
	opened := 0
	for _, token := range tokens {
		if token == "" {
			continue
		}
		if code, _ := redeem(t, newClient(t, srv), token); code == http.StatusOK {
			opened++
		}
	}
	if opened != 1 {
		t.Errorf("%d of the %d tokens drawn opened a session", opened, racers)
	}
}

// The two kinds of link do not compete.
//
// An invitation lives seven days and its recipient did not ask for it; a
// sign-in link lives fifteen minutes and is asked for from the screen. While
// both were one row per address, the second destroyed the first — so a new
// volunteer who clicked « mot de passe oublié » before opening their
// invitation found the invitation dead, days later, with nothing to say why.
func TestASignInLinkDoesNotDestroyAPendingInvitation(t *testing.T) {
	s, srv := testServer(t)
	withMailer(t, s, "https://campagne.exemple.fr")
	const email = "nouvelle@exemple.fr"
	createAccount(t, s, email, RoleVolunteer, nil)
	org := orgID(t, s, testSlug)

	var invitation string
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		var err error
		if invitation, err = s.mintInvitation(context.Background(), tx, org,
			email); err != nil {
			t.Fatal(err)
		}
	})
	// the same person then asks for a sign-in link
	askForLink(t, s, newClient(t, srv), email)

	// and their invitation still opens the account
	if code, _ := redeem(t, newClient(t, srv), invitation); code != http.StatusOK {
		t.Errorf("the invitation was destroyed by a sign-in request: %d", code)
	}
}

// What reaches an inbox exists nowhere on this side. A dump of this table
// opens no account.
func TestTheRawTokenIsNowhereInTheDatabase(t *testing.T) {
	s, srv := testServer(t)
	withMailer(t, s, "https://campagne.exemple.fr")
	const email = "marie@exemple.fr"
	createAccount(t, s, email, RoleVolunteer, nil)
	token := askForLink(t, s, newClient(t, srv), email)

	var stored string
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"SELECT token_hash FROM login_tokens WHERE email=$1", email).
			Scan(&stored); err != nil {
			t.Fatal(err)
		}
	})
	if stored == token {
		t.Fatal("the token is stored in the clear")
	}
	if stored != hashLinkToken(token) {
		t.Errorf("the stored form is not this token's hash: %q", stored)
	}
}

// The link's origin comes from the configuration. Built from the request's
// Host header, this route would send — to a real volunteer, over the
// campaign's own name — a link to a server of the caller's choosing.
func TestTheLinkIgnoresAPoisonedHostHeader(t *testing.T) {
	s, srv := testServer(t)
	mails := withMailer(t, s, "https://campagne.exemple.fr")
	const email = "marie@exemple.fr"
	createAccount(t, s, email, RoleVolunteer, nil)

	// single-campaign: EVERY Host resolves the bootstrap campaign, which is
	// exactly what makes the header untrustworthy here
	c := clientOn(t, srv, "chez-moi.exemple.net")
	if code, _ := c.call(http.MethodPost, "/api/session/link",
		map[string]string{"email": email}); code != http.StatusOK {
		t.Fatalf("asking for a link: %d", code)
	}
	s.outbound.Wait()

	body := mails.only(t).body
	if !strings.Contains(body, "https://campagne.exemple.fr/connexion#jeton=") {
		t.Errorf("the link does not point at the configured origin:\n%s", body)
	}
	if strings.Contains(body, "chez-moi.exemple.net") {
		t.Errorf("the poisoned host reached the message:\n%s", body)
	}
}

// The token is in the FRAGMENT: never sent to a server, so never in an
// access log, and invisible to the URL scanners that would otherwise spend a
// one-shot link before its recipient clicks it.
func TestTheTokenTravelsInTheFragment(t *testing.T) {
	s, srv := testServer(t)
	mails := withMailer(t, s, "https://campagne.exemple.fr")
	const email = "marie@exemple.fr"
	createAccount(t, s, email, RoleVolunteer, nil)
	askForLink(t, s, newClient(t, srv), email)

	body := mails.only(t).body
	link := "https://campagne.exemple.fr/connexion"
	before, after, _ := strings.Cut(body, link)
	_ = before
	if !strings.HasPrefix(after, "#jeton=") {
		t.Errorf("the token does not follow the fragment marker: %q",
			after[:min(40, len(after))])
	}
	if strings.Contains(body, "?jeton=") || strings.Contains(body, "/connexion/") {
		t.Errorf("the token travels somewhere a server would see it:\n%s", body)
	}
}

// Two campaigns, one instance: a token minted on one is not a credential on
// the other. The predicate that says so is the same one every other query
// carries.
func TestALinkOfOneCampaignIsRefusedByAnother(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	withMailer(t, s, "https://paraphe.test")
	b := createOrg(t, s, "other", "Other campaign")
	const email = "marie@exemple.fr"
	createAccount(t, s, email, RoleVolunteer, nil)
	// the SAME address on the neighbour, which is legitimate: one person can
	// volunteer in two campaigns hosted here
	createAccountIn(t, s, b, email, RoleVolunteer, nil)

	onA := clientOn(t, srv, testSlug+".paraphe.test")
	token := askForLink(t, s, onA, email)

	onB := clientOn(t, srv, "other.paraphe.test")
	if code, body := redeem(t, onB, token); code != http.StatusUnauthorized {
		t.Fatalf("a token of campaign %q opened a session on its neighbour: "+
			"%d %v", testSlug, code, body)
	}
	// and it still works where it belongs: the refusal above is the wall,
	// not a token that was broken from the start
	if code, _ := redeem(t, onA, token); code != http.StatusOK {
		t.Errorf("the token was refused by its own campaign too: %d", code)
	}
}

// The link points at the campaign's own subdomain, not at the apex: a
// volunteer who lands on the apex has no campaign to sign into.
func TestAMultiCampaignLinkPointsAtItsOwnSubdomain(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	mails := withMailer(t, s, "https://paraphe.test")
	const email = "marie@exemple.fr"
	createAccount(t, s, email, RoleVolunteer, nil)

	askForLink(t, s, clientOn(t, srv, testSlug+".paraphe.test"), email)
	if body := mails.only(t).body; !strings.Contains(body,
		"https://"+testSlug+".paraphe.test/connexion#jeton=") {
		t.Errorf("the link does not point at the campaign's subdomain:\n%s", body)
	}
}

// Opening an account sends its invitation, and the invitation WORKS: the
// volunteer signs in without anyone reading a password down a telephone.
func TestOpeningAnAccountSendsAnInvitationThatOpensIt(t *testing.T) {
	s, srv := testServer(t)
	mails := withMailer(t, s, "https://campagne.exemple.fr")
	coordination := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn("coord@exemple.fr", coordination); code != http.StatusOK {
		t.Fatal("coordination sign-in")
	}

	const invited = "nouvelle@exemple.fr"
	code, body := c.call(http.MethodPost, "/api/team/account", map[string]any{
		"email": invited, "name": "Nouvelle Bénévole", "role": RoleVolunteer,
	})
	if code != http.StatusCreated {
		t.Fatalf("opening an account: %d %v", code, body)
	}
	if body["invitation_sent"] != true {
		t.Errorf("invitation_sent = %v", body["invitation_sent"])
	}
	// The password is still there, and that is deliberate: the relay may be
	// down tomorrow, and reading it out is the path that always worked.
	if password, _ := body["password"].(string); password == "" {
		t.Error("the generated password left the answer")
	}

	sent := mails.only(t)
	if sent.to != invited {
		t.Fatalf("the invitation went to %q", sent.to)
	}
	if !strings.Contains(sent.body, "Nouvelle Bénévole") {
		t.Errorf("the invitation does not name its recipient:\n%s", sent.body)
	}
	// the proof: the token in that message opens the account
	fresh := newClient(t, srv)
	code, session := redeem(t, fresh, tokenIn(t, sent.body))
	if code != http.StatusOK {
		t.Fatalf("the invitation's link did not open the account: %d %v", code, session)
	}
	account, _ := session["account"].(map[string]any)
	if account["email"] != invited {
		t.Errorf("the invitation opened a session for %v", account["email"])
	}
}

// An address carrying a header break is refused where the ROW is written.
//
// Accepted there, the account was broken for good: the mailer rightly
// refuses that address at send time, so no invitation and no sign-in link
// could ever reach it — and the address is the primary key, so nobody could
// correct it either. `normalizeEmail` only trims the edges, and a
// `Contains(email, "@")` says nothing about what sits between them.
func TestAnAddressNoMessageCouldReachIsRefusedAtCreation(t *testing.T) {
	s, srv := testServer(t)
	withMailer(t, s, "https://campagne.exemple.fr")
	coordination := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn("coord@exemple.fr", coordination); code != http.StatusOK {
		t.Fatal("coordination sign-in")
	}
	for _, bad := range []string{
		"nouvelle@exemple.fr\r\nBcc: ailleurs@exemple.fr",
		"nouvelle@exemple.fr\nX-Injected: 1",
		"nouvelle\x7f@exemple.fr",
	} {
		code, body := c.call(http.MethodPost, "/api/team/account", map[string]any{
			"email": bad, "name": "Nouvelle", "role": RoleVolunteer,
		})
		if code != http.StatusBadRequest {
			t.Errorf("%q was accepted (%d %v): the account exists and no "+
				"message will ever reach it", bad, code, body)
		}
	}
	var created int
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"SELECT count(*) FROM accounts WHERE email LIKE 'nouvelle%'").
			Scan(&created); err != nil {
			t.Fatal(err)
		}
	})
	if created != 0 {
		t.Errorf("%d unreachable account(s) were written", created)
	}
}

// An invitation lives long enough to be read the next morning. Fifteen
// minutes is a link nobody ever opens.
func TestAnInvitationOutlivesASignInLink(t *testing.T) {
	s, srv := testServer(t)
	withMailer(t, s, "https://campagne.exemple.fr")
	coordination := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn("coord@exemple.fr", coordination); code != http.StatusOK {
		t.Fatal("coordination sign-in")
	}
	if code, body := c.call(http.MethodPost, "/api/team/account", map[string]any{
		"email": "nouvelle@exemple.fr", "name": "Nouvelle", "role": RoleVolunteer,
	}); code != http.StatusCreated {
		t.Fatalf("opening an account: %d %v", code, body)
	}
	mails := s.mailer.(*captureMailer)

	tomorrow := time.Now().Add(24 * time.Hour)
	s.now = func() time.Time { return tomorrow }
	if code, _ := redeem(t, newClient(t, srv),
		tokenIn(t, mails.only(t).body)); code != http.StatusOK {
		t.Errorf("an invitation was dead the next day: %d", code)
	}
}

// A relay that refuses must not lose the account: it is created, its
// password is returned, and the caller is TOLD to pass it on. The opposite
// of the sign-in link, and for the opposite reason — here the caller is
// authenticated and there is no existence left to protect.
func TestARefusedRelayStillOpensTheAccountAndSaysSo(t *testing.T) {
	s, srv := testServer(t)
	mails := withMailer(t, s, "https://campagne.exemple.fr")
	mails.fail = errNoRelay
	coordination := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn("coord@exemple.fr", coordination); code != http.StatusOK {
		t.Fatal("coordination sign-in")
	}

	code, body := c.call(http.MethodPost, "/api/team/account", map[string]any{
		"email": "nouvelle@exemple.fr", "name": "Nouvelle", "role": RoleVolunteer,
	})
	if code != http.StatusCreated {
		t.Fatalf("a refused relay lost the account: %d %v", code, body)
	}
	if body["invitation_sent"] != false {
		t.Errorf("invitation_sent = %v with a relay that refuses", body["invitation_sent"])
	}
	if warning, _ := body["invitation_error"].(string); warning == "" {
		t.Error("nothing told the lead the invitation had not left")
	}
	if password, _ := body["password"].(string); password == "" {
		t.Error("no password to pass on, and no invitation either")
	}
	var exists bool
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"SELECT true FROM accounts WHERE email=$1",
			"nouvelle@exemple.fr").Scan(&exists); err != nil {
			t.Fatal(err)
		}
	})
	if !exists {
		t.Error("the account was rolled back because a message could not leave")
	}
}

var errNoRelay = errors.New("relay refused the message")

// No relay is a state, and every surface says the same thing about it: the
// configuration answers false, and both routes refuse instead of accepting a
// request whose effect would never arrive.
func TestWithoutARelayTheLinkRoutesRefuseAndTheConfigurationSaysSo(t *testing.T) {
	_, srv := testServer(t)
	c := newClient(t, srv)

	_, config := c.call(http.MethodGet, "/api/config", nil)
	if config["magic_link"] != false {
		t.Errorf("magic_link = %v with no relay configured", config["magic_link"])
	}
	code, body := c.call(http.MethodPost, "/api/session/link",
		map[string]string{"email": "marie@exemple.fr"})
	if code != http.StatusServiceUnavailable {
		t.Errorf("asking for a link with no relay: %d %v", code, body)
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "mot de passe") {
		t.Errorf("the refusal does not point at the path that still works: %q", message)
	}
}

// With one, it says so too — the button appears only where it can work.
func TestWithARelayTheConfigurationOffersTheLink(t *testing.T) {
	s, srv := testServer(t)
	withMailer(t, s, "https://campagne.exemple.fr")
	_, config := newClient(t, srv).call(http.MethodGet, "/api/config", nil)
	if config["magic_link"] != true {
		t.Errorf("magic_link = %v with a relay configured", config["magic_link"])
	}
}

// The per-address ceiling is what keeps this route from being a mail bomber
// aimed at an inbox its operator happens to know.
func TestAskingRepeatedlyStopsSendingToTheSameAddress(t *testing.T) {
	s, srv := testServer(t)
	mails := withMailer(t, s, "https://campagne.exemple.fr")
	const email = "marie@exemple.fr"
	createAccount(t, s, email, RoleVolunteer, nil)

	c := newClient(t, srv)
	for i := 1; i <= limitMagicLinkAccount.events; i++ {
		if code, _ := c.call(http.MethodPost, "/api/session/link",
			map[string]string{"email": email}); code == http.StatusTooManyRequests {
			t.Fatalf("429 at attempt %d, below the ceiling of %d",
				i, limitMagicLinkAccount.events)
		}
	}
	code, _ := c.call(http.MethodPost, "/api/session/link",
		map[string]string{"email": email})
	if code != http.StatusTooManyRequests {
		t.Fatalf("attempt %d was still admitted: %d",
			limitMagicLinkAccount.events+1, code)
	}
	s.outbound.Wait()
	if len(mails.all()) != limitMagicLinkAccount.events {
		t.Errorf("%d messages went out for a ceiling of %d",
			len(mails.all()), limitMagicLinkAccount.events)
	}
}
