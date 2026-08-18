package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Integration tests: they require a DISPOSABLE PostgreSQL database,
// designated by PARAPHE_TEST_DATABASE_URL — they drop the tables at start.
//
// They cover what cannot be re-read from the code: concurrent allocation,
// the wall between teams, the wall between campaigns, role escalation —
// the class of defect that only shows under execution, against a real
// database, under the role production connects with.

const testSlug = "campaign"

func testConfig() *Config {
	campaign := map[string]string{}
	for _, k := range CampaignKeys {
		campaign[k] = "test value"
	}
	return &Config{
		Campaign: campaign, BatchSize: 2, Unfilled: []string{}, Complete: true,
	}
}

// The tests' application role. It is UNPRIVILEGED, and that is the whole
// point: the official PostgreSQL image makes the administration account a
// superuser. Tests run under
// that account would verify a walling that, in production, would only hold
// through the routes' WHERE clauses.
const (
	testRole         = "paraphe_rls"
	testRolePassword = "paraphe_rls"
)

// databaseName: the path of a DSN, without its leading slash.
func databaseName(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}

func testDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("PARAPHE_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("PARAPHE_TEST_DATABASE_URL not set: a disposable database is required")
	}
	// These tests DROP the tables. `task db` prints the DSN of the working
	// database for local trials, and handing it here suspends the campaign
	// and empties the accounts — which is exactly what happened. Refuse
	// anything that does not name itself disposable.
	if name := databaseName(dsn); !strings.Contains(name, "test") &&
		!strings.Contains(name, "e2e") {
		t.Fatalf("PARAPHE_TEST_DATABASE_URL points at %q: these tests drop the "+
			"tables, so the database must name itself disposable (…_test, …_e2e)",
			name)
	}
	ctx := context.Background()
	admin, err := OpenDatabase(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	// the tables may have been created by another role in a previous run:
	// cleanup happens under the administration account
	if _, err := admin.Exec(ctx, "DROP TABLE IF EXISTS mayors, notes, accounts, "+
		"teams, settings, assignments, orgs, hosting_requests, team_requests, "+
		"login_tokens"); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		fmt.Sprintf("DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE "+
			"rolname='%s') THEN CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER "+
			"NOBYPASSRLS; END IF; END $$", testRole, testRole, testRolePassword),
		fmt.Sprintf("GRANT CREATE, USAGE ON SCHEMA public TO %s", testRole),
	} {
		if _, err := admin.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}

	pool := applicationPool(t, dsn)
	asMaintenance(t, pool, func(tx pgx.Tx) {
		if _, err := schema(ctx, tx, testConfig(), testSlug); err != nil {
			t.Fatal(err)
		}
	})
	return pool
}

// applicationPool reopens the same database under the unprivileged role,
// which becomes owner of the tables: that is the production configuration,
// and the only one where FORCE ROW LEVEL SECURITY means anything.
func applicationPool(t *testing.T, adminDSN string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.User = testRole
	cfg.ConnConfig.Password = testRolePassword
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// asMaintenance opens a transaction for seeding, across campaigns.
func asMaintenance(t *testing.T, pool *pgxpool.Pool, fn func(pgx.Tx)) {
	t.Helper()
	asOrg(t, pool, OrgMaintenance, fn)
}

// asOrg opens a transaction for a fixture to write through. The campaign is
// named by the rows themselves — org_id is a column, and every query in the
// package says so — so there is nothing to declare here.
func asOrg(t *testing.T, pool *pgxpool.Pool, org int, fn func(pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	fn(tx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func execAsMaintenance(t *testing.T, s *Server, sql string, args ...any) {
	t.Helper()
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if _, err := tx.Exec(context.Background(), sql, args...); err != nil {
			t.Fatal(err)
		}
	})
}

// scalar reads ONE value in the maintenance scope. The work tables are
// per-campaign: a read that forgot to name one would draw the wrong
// conclusion.
func scalar[T any](t *testing.T, s *Server, sql string, args ...any) T {
	t.Helper()
	var v T
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
			t.Fatal(err)
		}
	})
	return v
}

func maintenanceColumn(t *testing.T, s *Server, sql string, args ...any) []string {
	t.Helper()
	var out []string
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		rows, err := tx.Query(context.Background(), sql, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			out = append(out, v)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	})
	return out
}

func orgID(t *testing.T, s *Server, slug string) int {
	t.Helper()
	var id int
	if err := s.pool.QueryRow(context.Background(),
		"SELECT id FROM orgs WHERE slug=$1", slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	pool := testDatabase(t)
	decoy, err := HashPassword("nonexistent-account")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		pool:          pool,
		cfg:           testConfig(),
		sessions:      NewSessions([]byte("test key")),
		bootstrapSlug: testSlug,
		decoyHash:     decoy,
		now:           time.Now,
		webDir:        t.TempDir(),
		// the process store, like production without valkey_url: each test
		// server counts alone, and its counters die with it
		limiter: newRateLimiter([]byte("test key"), nil, time.Now),
		logKey:  deriveKey([]byte("test key"), "paraphe:log-pseudonyms:v1"),
	}
	// The object store when the suite was given one, nil otherwise — which
	// is exactly the shape of an instance whose operator configured none,
	// and the shape most of these tests want. The routes that need it say
	// so themselves (mediaUnavailable), so nothing here has to pretend.
	if os.Getenv("PARAPHE_TEST_MEDIA_ENDPOINT") != "" {
		for _, v := range []string{"ENDPOINT", "BUCKET", "ACCESS_KEY",
			"SECRET_KEY", "PUBLIC_URL"} {
			t.Setenv("PARAPHE_MEDIA_"+v, os.Getenv("PARAPHE_TEST_MEDIA_"+v))
		}
		media, err := NewMediaStore()
		if err != nil {
			t.Fatal(err)
		}
		s.media = media
	}
	// TLS, because the session cookie is Secure and Go's cookie jar applies
	// RFC 6265 to the letter: it will not send such a cookie over http://,
	// with none of the browser's exception for localhost. Over plain HTTP
	// every authenticated request in this suite answered 401.
	//
	// Which is the right way round: the suite now exercises the shape a
	// deployment runs in, instead of one no deployment is allowed to.
	srv := httptest.NewTLSServer(securityHeaders(s.routes()))
	t.Cleanup(srv.Close)
	return s, srv
}

// test mayors: the score drives the allocation order, the rank drives the
// message template. `mayors` is shared by every campaign:
// it is written without a scope.
func seedMayors(t *testing.T, s *Server, count int, dept string) {
	t.Helper()
	for i := range count {
		insee := fmt.Sprintf("%s%03d", dept, i)
		if _, err := s.pool.Exec(context.Background(),
			"INSERT INTO mayors(insee_code, rank, score, department, commune, "+
				"last_name, first_name, title, democratic_theme_endorsement) "+
				"VALUES($1,'has_endorsed',$2,$3,$4,'MARTIN','Camille','Mme','oui')",
			insee, fmt.Sprint(100-i), dept, "Commune "+insee); err != nil {
			t.Fatal(err)
		}
	}
}

func createTeamIn(t *testing.T, s *Server, org int, name, departments string) int {
	t.Helper()
	var id int
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"INSERT INTO teams(org_id, name, departments, created_at) "+
				"VALUES($1,$2,$3,'2026-01-01T00:00') RETURNING id",
			org, name, departments).Scan(&id); err != nil {
			t.Fatal(err)
		}
	})
	return id
}

func createTeam(t *testing.T, s *Server, name, departments string) int {
	t.Helper()
	return createTeamIn(t, s, orgID(t, s, testSlug), name, departments)
}

func createAccountIn(t *testing.T, s *Server, org int, email, role string,
	team *int) string {
	t.Helper()
	// the password depends on the CAMPAIGN as much as on the address: the
	// same person can volunteer in two campaigns hosted here, and a shared
	// secret would hide the fact that one campaign accepts the other's
	// password
	password := fmt.Sprintf("mot-de-passe-%d-%s", org, email)
	hashed, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	execAsMaintenance(t, s,
		"INSERT INTO accounts(org_id, email, name, password_hash, role, team_id, created_at, created_by) "+
			"VALUES($1,$2,$3,$4,$5,$6,'2026-01-01T00:00','test')",
		org, email, "Name of "+email, hashed, role, team)
	return password
}

func createAccount(t *testing.T, s *Server, email, role string, team *int) string {
	t.Helper()
	return createAccountIn(t, s, orgID(t, s, testSlug), email, role, team)
}

type client struct {
	t    *testing.T
	http *http.Client
	base string
	// host: the Host header to present. Empty = the URL's. It is what
	// designates the campaign when the instance hosts several.
	host string
}

func newClient(t *testing.T, srv *httptest.Server) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	// A NEW client, sharing only the transport. srv.Client() returns the
	// SAME client on every call, so assigning a jar to it gave every caller
	// one jar between them: two clients signed in as different people, the
	// second session overwrote the first, and the wall tests read a 403
	// where they expected their own campaign.
	//
	// The transport carries the throwaway certificate httptest generated for
	// this server, so the chain verifies rather than being skipped.
	return &client{
		t:    t,
		http: &http.Client{Transport: srv.Client().Transport, Jar: jar},
		base: srv.URL,
	}
}

func clientOn(t *testing.T, srv *httptest.Server, host string) *client {
	t.Helper()
	c := newClient(t, srv)
	c.host = host
	return c
}

func (c *client) request(method, path string, body any) *http.Request {
	c.t.Helper()
	var reader *strings.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = strings.NewReader(string(raw))
	} else {
		reader = strings.NewReader("")
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.host != "" {
		req.Host = c.host
	}
	return req
}

// cookie: what the jar holds for the host this client presents. Go keys the
// jar on the Host header, so a client that changes campaigns mid-test sends
// NOTHING rather than sending the neighbour a real session — which reads as
// a wall and is not one.
func (c *client) cookie(name string) *http.Cookie {
	c.t.Helper()
	host := c.host
	if host == "" {
		u, err := url.Parse(c.base)
		if err != nil {
			c.t.Fatal(err)
		}
		host = u.Host
	}
	u, err := url.Parse("https://" + host)
	if err != nil {
		c.t.Fatal(err)
	}
	for _, k := range c.http.Jar.Cookies(u) {
		if k.Name == name {
			return k
		}
	}
	return nil
}

// callWith presents cookies EXPLICITLY, whatever the jar would have said.
// It is how a session obtained on one campaign is carried onto another.
func (c *client) callWith(method, path string, cookies ...*http.Cookie) (
	int, map[string]any,
) {
	c.t.Helper()
	req := c.request(method, path, nil)
	for _, k := range cookies {
		req.AddCookie(k)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (c *client) call(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	resp, err := c.http.Do(c.request(method, path, body))
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (c *client) signIn(email, password string) int {
	c.t.Helper()
	code, _ := c.call(http.MethodPost, "/api/session",
		map[string]string{"email": email, "password": password})
	return code
}

// An account created before argon2id carries a scrypt hash, and the password
// is known at exactly one moment: a successful sign-in. If it is not replaced
// there it is never replaced at all — a volunteer's password is handed out
// once by a lead and nobody changes it afterwards.
func TestSignInUpgradesAnOlderHash(t *testing.T) {
	s, srv := testServer(t)
	const email = "marie@exemple.fr"
	createAccount(t, s, email, RoleVolunteer, nil)
	// the werkzeug scrypt hash pinned in password_test.go, and its password
	execAsMaintenance(t, s,
		"UPDATE accounts SET password_hash=$1 WHERE email=$2", scryptHash, email)

	read := func() string {
		var stored string
		asMaintenance(t, s.pool, func(tx pgx.Tx) {
			if err := tx.QueryRow(context.Background(),
				"SELECT password_hash FROM accounts WHERE email=$1", email,
			).Scan(&stored); err != nil {
				t.Fatal(err)
			}
		})
		return stored
	}
	if before := read(); !strings.HasPrefix(before, "scrypt:") {
		t.Fatalf("the fixture did not take: %s", before)
	}

	c := newClient(t, srv)
	if code := c.signIn(email, referencePassword); code != http.StatusOK {
		t.Fatalf("the old hash no longer opens a session: %d", code)
	}
	after := read()
	if !strings.HasPrefix(after, "argon2id:") {
		t.Errorf("the hash was not upgraded on sign-in, so it never will "+
			"be: %s", after)
	}
	if NeedsRehash(after) {
		t.Errorf("the upgraded hash is already stale: %s", after)
	}
	// …and the password still works against what replaced it
	if code := c.signIn(email, referencePassword); code != http.StatusOK {
		t.Errorf("the upgraded hash refuses the same password: %d", code)
	}
}

func TestSignInAndImmediateRevocation(t *testing.T) {
	s, srv := testServer(t)
	pw := createAccount(t, s, "marie@exemple.fr", RoleVolunteer, nil)
	c := newClient(t, srv)

	if code := c.signIn("marie@exemple.fr", "pas le bon"); code != http.StatusUnauthorized {
		t.Errorf("wrong password accepted: %d", code)
	}
	if code := c.signIn("inconnue@exemple.fr", pw); code != http.StatusUnauthorized {
		t.Errorf("nonexistent account accepted: %d", code)
	}
	if code := c.signIn("marie@exemple.fr", pw); code != http.StatusOK {
		t.Fatalf("legitimate sign-in refused: %d", code)
	}
	if code, _ := c.call(http.MethodGet, "/api/me", nil); code != http.StatusOK {
		t.Fatalf("/api/me refused after sign-in: %d", code)
	}

	// deactivation takes effect on the next request: that is the point of
	// re-reading the account from the database on every call
	execAsMaintenance(t, s, "UPDATE accounts SET active=FALSE WHERE email='marie@exemple.fr'")
	if code, _ := c.call(http.MethodGet, "/api/me", nil); code != http.StatusUnauthorized {
		t.Errorf("deactivated account still admitted: %d", code)
	}
}

// The central invariant: two volunteers must never receive the same mayor.
// The allocation is the insert itself — without the ON CONFLICT condition,
// this test reports duplicates.
func TestBatchNeverGivesSameMayorTwice(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 40, "01")
	const volunteers, rounds = 8, 5

	clients := make([]*client, volunteers)
	for i := range clients {
		email := fmt.Sprintf("b%d@exemple.fr", i)
		pw := createAccount(t, s, email, RoleVolunteer, nil)
		clients[i] = newClient(t, srv)
		if code := clients[i].signIn(email, pw); code != http.StatusOK {
			t.Fatalf("sign-in of %s: %d", email, code)
		}
	}

	// The sum of the ANNOUNCED batches is what must be measured: a card
	// allocated twice still has a single volunteer column (the second write
	// overwrites the first), so counting rows would reveal nothing. What
	// shows is two volunteers told "you have 2 mayors" for the same card —
	// and one of them writing to an elected official who is not theirs.
	var announced int64
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				code, rep := c.call(http.MethodPost, "/api/batch",
					map[string]any{"rank": "has_endorsed"})
				if code != http.StatusOK {
					t.Errorf("batch refused: %d %v", code, rep)
					return
				}
				if n, ok := rep["taken"].(float64); ok {
					atomic.AddInt64(&announced, int64(n))
				}
			}
		}()
	}
	wg.Wait()

	assigned := scalar[int](t, s,
		"SELECT count(*) FROM assignments WHERE volunteer IS NOT NULL")
	if assigned != 40 {
		t.Errorf("%d mayors assigned out of 40: the pool is not drained "+
			"cleanly", assigned)
	}
	if int(announced) != assigned {
		t.Errorf("%d allocations announced for %d cards: %d mayor(s) promised "+
			"to two volunteers", announced, assigned, int(announced)-assigned)
	}
}

// Two volunteers of the SAME team can aim at the same card: the team wall
// is not enough, the condition on volunteer is required.
// Writing a status TELLS, it does not TAKE — and a reservation still holds.
//
// The two halves are one decision. Recording a status used to claim the card
// on the way past, so a note taken in passing removed the mayor from
// everyone else's list for good; what stops two volunteers contacting the
// same person is now the status being visible to the next one who opens the
// card. Reserving stays a deliberate act, through /api/batch, and a card
// somebody HAS taken is still refused to anyone else.
func TestAStatusTellsWithoutTakingAndNoCardIsHeldAgainstAnybody(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "02")
	gid := createTeam(t, s, "Ain", "02")

	pwA := createAccount(t, s, "a@exemple.fr", RoleVolunteer, &gid)
	pwB := createAccount(t, s, "b@exemple.fr", RoleVolunteer, &gid)
	ca, cb := newClient(t, srv), newClient(t, srv)
	ca.signIn("a@exemple.fr", pwA)
	cb.signIn("b@exemple.fr", pwB)

	if code, rep := ca.call(http.MethodPost, "/api/mayors/02000/status",
		map[string]string{"status": "email_sent", "note": "écrit"}); code != http.StatusOK {
		t.Fatalf("status refused for the first volunteer: %d %v", code, rep)
	}
	// the card belongs to nobody, so the second volunteer may write on it…
	if v := scalar[*string](t, s,
		"SELECT volunteer FROM assignments WHERE org_id=$1 AND insee_code=$2",
		orgID(t, s, testSlug), "02000"); v != nil {
		t.Errorf("writing a status took the card for %q: it leaves everyone "+
			"else's list and no screen gives it back", *v)
	}
	// …and sees what the first one recorded before doing anything
	code, rep := cb.call(http.MethodGet, "/api/mayors/02000", nil)
	if code != http.StatusOK {
		t.Fatalf("the card is unreadable to the second volunteer: %d %v", code, rep)
	}
	card, _ := rep["mayor"].(map[string]any)
	if card["status"] != "email_sent" {
		t.Errorf("the second volunteer does not see what the first recorded "+
			"(%v): the information is what replaces the lock", card["status"])
	}
	// …and writes on it, announcing what it was showing. Announcing nothing
	// is refused on purpose: that is a screen which has not read this card
	// since somebody recorded on it.
	if code, rep := cb.call(http.MethodPost, "/api/mayors/02000/status",
		map[string]string{"status": "refused", "note": "doublon"}); code != http.StatusConflict {
		t.Errorf("a write that announced no reading was accepted over a "+
			"status somebody recorded: %d %v", code, rep)
	}
	if code, rep := cb.call(http.MethodPost, "/api/mayors/02000/status",
		map[string]string{"status": "refused", "note": "vu, je complète",
			"seen": "email_sent"}); code != http.StatusOK {
		t.Errorf("a card nobody has taken was refused to a second writer "+
			"who had read it: %d %v", code, rep)
	}

	// …AND a card somebody has taken is not theirs to hold. A batch hands a
	// volunteer cards to work through; it is not a claim on the mayor. The
	// write lock that used to sit here made every screen a lie — « prise par
	// X » beside a save button answering 409 — and it refused the one thing
	// worth recording: somebody made the call.
	if code, rep := ca.call(http.MethodPost, "/api/batch",
		map[string]any{}); code != http.StatusOK {
		t.Fatalf("batch refused: %d %v", code, rep)
	}
	taken := scalar[string](t, s,
		"SELECT insee_code FROM assignments WHERE org_id=$1 AND volunteer=$2 "+
			"LIMIT 1", orgID(t, s, testSlug), "a@exemple.fr")
	if code, rep := cb.call(http.MethodPost, "/api/mayors/"+taken+"/status",
		map[string]string{"status": "refused", "note": "je l'ai eu aussi",
			"seen": "to_contact"}); code != http.StatusOK {
		t.Errorf("a card somebody had taken refused another volunteer's "+
			"record: %d %v", code, rep)
	}
	// and the ONE refusal left applies to that card like any other: it is
	// about the state read, not about who holds it
	if code, rep := cb.call(http.MethodPost, "/api/mayors/"+taken+"/status",
		map[string]string{"status": "signed", "note": "à l'aveugle",
			"seen": "to_contact"}); code != http.StatusConflict {
		t.Errorf("a write announcing a state nobody read was accepted: %d %v",
			code, rep)
	}
	// the taker keeps the card on their own board — informative, not a claim
	if v := scalar[string](t, s,
		"SELECT volunteer FROM assignments WHERE org_id=$1 AND insee_code=$2",
		orgID(t, s, testSlug), taken); v != "a@exemple.fr" {
		t.Errorf("the second writer took the card from %q: recording still "+
			"must not claim", v)
	}
}

// THE CARD CROSSES, THE PERSON DOES NOT — on the screen, and in the file
// that bypasses it. Every team of a campaign reads every card: nothing is
// hidden, nothing is refused, and a card another team is working says so by
// naming the TEAM. What never crosses is the individual.
//
// The export is the half that matters, because downloading it used to be the
// way round whatever the page decided.
func TestACardCrossesTeamsAndThePersonOnItDoesNot(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 6, "03")
	north := createTeam(t, s, "Nord", "03")
	south := createTeam(t, s, "Sud", "03")

	pwN := createAccount(t, s, "nord@exemple.fr", RoleVolunteer, &north)
	pwS := createAccount(t, s, "sud@exemple.fr", RoleVolunteer, &south)
	cn, cs := newClient(t, srv), newClient(t, srv)
	cn.signIn("nord@exemple.fr", pwN)
	cs.signIn("sud@exemple.fr", pwS)

	if code, rep := cn.call(http.MethodPost, "/api/batch",
		map[string]any{}); code != http.StatusOK {
		t.Fatalf("batch refused: %d %v", code, rep)
	}
	taken := maintenanceColumn(t, s,
		"SELECT insee_code FROM assignments WHERE volunteer='nord@exemple.fr'")
	if len(taken) == 0 {
		t.Fatal("no mayor taken")
	}

	// the card OPENS, and says which team is on it, and names nobody
	code, card := cs.call(http.MethodGet, "/api/mayors/"+taken[0], nil)
	if code != http.StatusOK {
		t.Fatalf("a card another team is working: %d, want 200 — no card of a "+
			"campaign is hidden from a team of it", code)
	}
	raw, _ := json.Marshal(card)
	if strings.Contains(string(raw), "nord@exemple.fr") {
		t.Errorf("the card carries the other team's volunteer: %s", raw)
	}
	if !strings.Contains(string(raw), "Nord") {
		t.Errorf("the card does not say which team is on it: %s", raw)
	}

	// and the list carries every mayor, not the leftovers
	if code, rep := cs.call(http.MethodGet, "/api/mayors", nil); code == http.StatusOK {
		if total := int(rep["total"].(float64)); total != 6 {
			t.Errorf("the Sud team's list shows %d mayors of 6: cards are being "+
				"hidden from a team of the same campaign", total)
		}
	}

	resp, err := cs.http.Do(cs.request(http.MethodGet, "/api/export.csv", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := csv.NewReader(resp.Body)
	reader.Comma = ';'
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 7 {
		t.Errorf("the export has %d rows for 6 mayors and a header", len(rows))
	}
	body := ""
	for _, row := range rows[1:] {
		body += strings.Join(row, ";") + "\n"
	}
	for _, insee := range taken {
		if !strings.Contains(body, insee) {
			t.Errorf("the export drops %s because another team is on it", insee)
		}
	}
	if strings.Contains(body, "nord@exemple.fr") {
		t.Error("the export names the other team's volunteer — the download is " +
			"the way round what the screen does not show")
	}
	if !strings.Contains(body, "Nord") {
		t.Error("the export does not say which team is on those cards")
	}
}

// Coordination sees everything, names included: it is the one role the
// campaign's own privacy line does not run through, and the export is where
// that is worth stating — a coordination reading it is reading its own
// campaign's work.
func TestCoordinationReadsTheNamesTheTeamsDoNot(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "03")
	north := createTeam(t, s, "Nord", "03")
	pwN := createAccount(t, s, "nord@exemple.fr", RoleVolunteer, &north)
	pwC := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	cn, cc := newClient(t, srv), newClient(t, srv)
	cn.signIn("nord@exemple.fr", pwN)
	cc.signIn("coord@exemple.fr", pwC)
	if code, rep := cn.call(http.MethodPost, "/api/batch",
		map[string]any{}); code != http.StatusOK {
		t.Fatalf("batch refused: %d %v", code, rep)
	}
	taken := maintenanceColumn(t, s,
		"SELECT insee_code FROM assignments WHERE volunteer='nord@exemple.fr'")
	if len(taken) == 0 {
		t.Fatal("no mayor taken")
	}
	_, card := cc.call(http.MethodGet, "/api/mayors/"+taken[0], nil)
	raw, _ := json.Marshal(card)
	if !strings.Contains(string(raw), "nord@exemple.fr") {
		t.Errorf("coordination cannot see who is on a card of its own "+
			"campaign: %s", raw)
	}
}

// A team lead only opens volunteer accounts, and only in THEIR team:
// otherwise they craft themselves a coordination in one call.
func TestLeadCannotEscalateNorLeaveTheirTeam(t *testing.T) {
	s, srv := testServer(t)
	own := createTeam(t, s, "Sien", "04")
	other := createTeam(t, s, "Autre", "05")
	pw := createAccount(t, s, "ref@exemple.fr", RoleLead, &own)
	c := newClient(t, srv)
	c.signIn("ref@exemple.fr", pw)

	code, rep := c.call(http.MethodPost, "/api/team/account", map[string]any{
		"email": "promu@exemple.fr", "name": "Promu",
		"role": RoleCoordination, "team_id": other})
	if code != http.StatusCreated {
		t.Fatalf("creation refused: %d %v", code, rep)
	}
	if rep["role"] != RoleVolunteer {
		t.Errorf("a team lead created a %q account", rep["role"])
	}
	var role string
	var gid *int
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"SELECT role, team_id FROM accounts WHERE email='promu@exemple.fr'").
			Scan(&role, &gid); err != nil {
			t.Fatal(err)
		}
	})
	if role != RoleVolunteer || gid == nil || *gid != own {
		t.Errorf("account created with role=%q team=%v: outside the lead's scope",
			role, gid)
	}

	// and they cannot create a team
	if code, _ := c.call(http.MethodPost, "/api/team/group",
		map[string]any{"name": "Mon empire"}); code != http.StatusForbidden {
		t.Errorf("a team lead created a team: %d", code)
	}
}

// A lead cannot declare themselves instance administrator either: the role
// does not exist for validRole, and it does not live inside a campaign.
func TestAdministrationRoleUnreachableFromCampaign(t *testing.T) {
	s, srv := testServer(t)
	pw := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	c := newClient(t, srv)
	c.signIn("coord@exemple.fr", pw)

	code, rep := c.call(http.MethodPost, "/api/team/account", map[string]any{
		"email": "moi@exemple.fr", "name": "Moi", "role": RoleAdministration})
	if code != http.StatusBadRequest {
		t.Errorf("administration role accepted from a campaign: %d %v", code, rep)
	}
	// and the moderation routes are closed to them
	if code, _ := c.call(http.MethodGet, "/api/admin/requests", nil); code == http.StatusOK {
		t.Error("a campaign's coordination reads the moderation queue")
	}
}

func TestLivenessDoesNotTouchDatabase(t *testing.T) {
	s, srv := testServer(t)
	s.pool.Close() // simulated database failure
	resp, err := srv.Client().Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/sante answers %d while the database is down: the liveness "+
			"probe would turn the failure into CrashLoopBackOff", resp.StatusCode)
	}
}

// Second anti-CSRF barrier after SameSite: a form on another site cannot
// post application/json without a CORS preflight.
func TestFormPostRefused(t *testing.T) {
	s, srv := testServer(t)
	pw := createAccount(t, s, "marie@exemple.fr", RoleVolunteer, nil)
	c := newClient(t, srv)
	c.signIn("marie@exemple.fr", pw)

	resp, err := c.http.Post(srv.URL+"/api/batch",
		"application/x-www-form-urlencoded", strings.NewReader("rank=has_endorsed"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("form POST accepted: %d", resp.StatusCode)
	}
}

// An unknown /api/ route returns JSON: otherwise the front end receives
// HTML and shows "unexpected token <", which tells nobody anything.
func TestUnknownAPIRouteReturnsJSON(t *testing.T) {
	_, srv := testServer(t)
	resp, err := srv.Client().Get(srv.URL + "/api/n-importe-quoi")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("code %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestProtectedRoutesWithoutSession(t *testing.T) {
	_, srv := testServer(t)
	for _, path := range []string{
		"/api/me", "/api/dashboard", "/api/mayors", "/api/mayors/01000",
		"/api/export.csv", "/api/team", "/api/facets",
		"/api/admin/requests",
	} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s accessible without a session: %d", path, resp.StatusCode)
		}
	}
}

// A PERIMETER SAYS WHERE A TEAM DRAWS ITS WORK. It is not a claim on the
// mayors inside it, and recording a status is not an annexation — writing one
// takes nothing. So `/api/batch` stays inside the departments a team was
// given, and `/status` refuses nobody: a volunteer who has actually spoken to
// a mayor records it, whichever department that mayor is in, and the status
// names the team that wrote it.
//
// Refusing it stopped the one thing this application exists to write down.
func TestAPerimeterBoundsTheDrawAndNotTheRecord(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "59")
	seedMayors(t, s, 3, "13")
	north := createTeam(t, s, "Nord", "59")
	south := createTeam(t, s, "Sud", "13")

	pwN := createAccount(t, s, "nord@exemple.fr", RoleVolunteer, &north)
	pwS := createAccount(t, s, "sud@exemple.fr", RoleVolunteer, &south)
	cn, cs := newClient(t, srv), newClient(t, srv)
	cn.signIn("nord@exemple.fr", pwN)
	cs.signIn("sud@exemple.fr", pwS)

	// drawing outside the perimeter is still refused: a batch is what the
	// team was given to work through
	if code, _ := cn.call(http.MethodPost, "/api/batch",
		map[string]any{"department": "13"}); code != http.StatusForbidden {
		t.Errorf("/batch outside the perimeter accepted: %d", code)
	}
	// recording is not
	if code, rep := cn.call(http.MethodPost, "/api/mayors/13000/status",
		map[string]string{"status": "email_sent", "note": "je l'ai eu au téléphone"}); code !=
		http.StatusOK {
		t.Errorf("a status outside the perimeter: %d %v, want 200 — a call "+
			"that happened must be recordable", code, rep)
	}
	// and it names the team that wrote it, which is what makes it readable
	// to everybody else
	if by := scalar[int](t, s, "SELECT updated_by_team FROM assignments "+
		"WHERE org_id=$1 AND insee_code=$2", orgID(t, s, testSlug), "13000"); by != north {
		t.Errorf("the status names team %d, want %d: a record nobody can "+
			"attribute is the reason the refusal existed", by, north)
	}
	// the team whose department it is is not locked out by that record
	if code, rep := cs.call(http.MethodPost, "/api/mayors/13001/status",
		map[string]string{"status": "email_sent", "note": "chez moi"}); code != http.StatusOK {
		t.Errorf("the team of that department turned away: %d %v", code, rep)
	}
	// a team without a perimeter (national) draws from the whole country
	pw := createAccount(t, s, "national@exemple.fr", RoleVolunteer, nil)
	c := newClient(t, srv)
	c.signIn("national@exemple.fr", pw)
	if code, rep := c.call(http.MethodPost, "/api/mayors/59001/status",
		map[string]string{"status": "email_sent", "note": ""}); code != http.StatusOK {
		t.Errorf("national team restricted: %d %v", code, rep)
	}
}

// Notes carry their own team_id. Trusting it rather than the card's
// current owner is what keeps a nominative note from leaking when a card
// returns to the shared pool.
// A note names a team, always — and the DATABASE is what says so.
//
// Its reader is `team_id IS NULL OR team_id = mine`, so a note with a real
// null is one every team of the campaign reads, which is the opposite of
// what NationalTeam was introduced for. Nothing the application writes is
// null (MyTeam() is 0 at the least), and a note that predates the column is
// backfilled to the national scope rather than left readable by everyone.
//
// Its sibling `assignments.team_id` stays nullable ON PURPOSE: there, a null
// is how an operator puts a stuck card back in the shared pool, and the test
// below is what holds the notes behind when that happens.
func TestANoteAlwaysNamesATeam(t *testing.T) {
	s, _ := testServer(t)
	seedMayors(t, s, 1, "60")
	org := orgID(t, s, testSlug)
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		ctx := context.Background()
		// a savepoint: the refusal aborts the transaction it happens in, and
		// the outer one still has to commit
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = sp.Rollback(ctx) }()
		if _, err := sp.Exec(ctx,
			"INSERT INTO notes(org_id, insee_code, volunteer, status, note, "+
				"ts, team_id) VALUES($1,'60000','x@exemple.fr','refused',"+
				"'secret','2026-01-01T00:00',NULL)", org); err == nil {
			t.Error("a note was written with no team at all: its reader " +
				"treats that as every team's, so one campaign's volunteers " +
				"read each other's calls")
		}
	})
}

func TestNotesDoNotFollowReleasedCardToAnotherTeam(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 2, "60")
	north := createTeam(t, s, "Nord", "")
	south := createTeam(t, s, "Sud", "")
	pwN := createAccount(t, s, "n@exemple.fr", RoleVolunteer, &north)
	pwS := createAccount(t, s, "s@exemple.fr", RoleVolunteer, &south)
	cn, cs := newClient(t, srv), newClient(t, srv)
	cn.signIn("n@exemple.fr", pwN)
	cs.signIn("s@exemple.fr", pwS)

	const secret = "a dit non, très remonté contre X"
	if code, rep := cn.call(http.MethodPost, "/api/mayors/60000/status",
		map[string]string{"status": "refused", "note": secret}); code != http.StatusOK {
		t.Fatalf("status refused: %d %v", code, rep)
	}
	// the card returns to the shared pool (operations remediation)
	execAsMaintenance(t, s,
		"UPDATE assignments SET volunteer=NULL, team_id=NULL WHERE insee_code='60000'")
	code, rep := cs.call(http.MethodGet, "/api/mayors/60000", nil)
	if code != http.StatusOK {
		t.Fatalf("released card inaccessible: %d %v", code, rep)
	}
	notes, _ := rep["notes"].([]any)
	if len(notes) != 0 {
		t.Errorf("the Sud team reads %d note(s) of the Nord team: %v", len(notes), notes)
	}
	// coordination, on the other hand, sees everything
	pwC := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	cc := newClient(t, srv)
	cc.signIn("coord@exemple.fr", pwC)
	_, repC := cc.call(http.MethodGet, "/api/mayors/60000", nil)
	if n, _ := repC["notes"].([]any); len(n) != 1 {
		t.Errorf("coordination does not see the note: %v", repC["notes"])
	}
}

// The import path had no test at all, and it is the only one that DELETES
// rows from `mayors` — a table shared by every campaign on the instance.
// Two destructive mutations survived the whole suite: lowering the
// truncated-CSV floor, and dropping the "already worked on" filter.
func TestImportRefusesATruncatedList(t *testing.T) {
	s, _ := testServer(t)
	seedMayors(t, s, 3, "01")

	short := filepath.Join(t.TempDir(), "court.csv")
	writeMayorCSV(t, short, 999)
	err := withTx(t, s, func(tx pgx.Tx) error {
		return importList(context.Background(), tx, short)
	})
	if err == nil {
		t.Fatal("a 999-row CSV was imported: an incomplete file empties the list")
	}
	if !strings.Contains(err.Error(), "import refused") {
		t.Errorf("refused, but not for the right reason: %v", err)
	}
	// and the existing list is untouched
	if n := countMayors(t, s); n != 3 {
		t.Errorf("%d mayors left after the refusal, expected 3", n)
	}
}

// The absolute floor is 1,000 rows against a list of ~34,800: three per
// cent. A file cut anywhere above it passed, and removeStale then deleted
// every mayor missing from it and flagged the cards the team had worked with
// « parrainage attribué à tort » — a claim about the crossing that nothing
// had re-run, made to the volunteer who wrote the notes.
func TestImportRefusesAListThatLostATenthOfItself(t *testing.T) {
	s, _ := testServer(t)
	full := filepath.Join(t.TempDir(), "complete.csv")
	writeMayorCSV(t, full, 2000)
	if err := withTx(t, s, func(tx pgx.Tx) error {
		return importList(context.Background(), tx, full)
	}); err != nil {
		t.Fatalf("the complete list was refused: %v", err)
	}
	if n := countMayors(t, s); n != 2000 {
		t.Fatalf("%d mayors imported, expected 2000", n)
	}

	// well above the absolute floor, and missing a quarter of the list
	truncated := filepath.Join(t.TempDir(), "tronquee.csv")
	writeMayorCSV(t, truncated, 1500)
	err := withTx(t, s, func(tx pgx.Tx) error {
		return importList(context.Background(), tx, truncated)
	})
	if err == nil {
		t.Fatal("a list that lost a quarter of its rows was imported: the " +
			"missing mayors are deleted and the worked-on ones are told " +
			"their endorsement was wrongly attributed")
	}
	if !strings.Contains(err.Error(), "import refused") {
		t.Errorf("refused, but not for the right reason: %v", err)
	}
	if n := countMayors(t, s); n != 2000 {
		t.Errorf("%d mayors left after the refusal, expected 2000", n)
	}

	// …and a list that merely shrinks a little still goes through: the RNE
	// loses mayors between elections, and refusing that would freeze the
	// list at its largest size for ever
	slightly := filepath.Join(t.TempDir(), "un-peu-moins.csv")
	writeMayorCSV(t, slightly, 1900)
	if err := withTx(t, s, func(tx pgx.Tx) error {
		return importList(context.Background(), tx, slightly)
	}); err != nil {
		t.Fatalf("a list 5%% shorter was refused: %v", err)
	}
	if n := countMayors(t, s); n != 1900 {
		t.Errorf("%d mayors after the legitimate shrink, expected 1900", n)
	}
}

func TestImportKeepsWhatTheTeamHasWorkedOn(t *testing.T) {
	s, _ := testServer(t)
	seedMayors(t, s, 4, "01")
	org := orgID(t, s, testSlug)
	// "Worked on" is a DISJUNCTION, and each half alone is a mass DELETE
	// from a table every campaign shares. Seeding a card that satisfies
	// both left either half deletable without a test going red — and the
	// first of them, a card reserved but not yet contacted, is the state
	// every card is in the moment a batch is taken.
	execAsMaintenance(t, s,
		"INSERT INTO assignments(org_id, insee_code, volunteer, status) "+
			"VALUES($1,'01000','benevole@exemple.fr','email_sent')", org)
	execAsMaintenance(t, s,
		"INSERT INTO assignments(org_id, insee_code, volunteer, status) "+
			"VALUES($1,'01002','benevole@exemple.fr','to_contact')", org)
	execAsMaintenance(t, s,
		"INSERT INTO assignments(org_id, insee_code, volunteer, status) "+
			"VALUES($1,'01003',NULL,'refused')", org)

	full := filepath.Join(t.TempDir(), "complet.csv")
	writeMayorCSV(t, full, 1000)
	if err := withTx(t, s, func(tx pgx.Tx) error {
		return importList(context.Background(), tx, full)
	}); err != nil {
		t.Fatal(err)
	}

	// the intact one is gone, the worked-on one stays and says why
	var priority, rank, candidate string
	if err := s.pool.QueryRow(context.Background(),
		"SELECT priority, rank, recent_candidate FROM mayors WHERE insee_code='01000'").
		Scan(&priority, &rank, &candidate); err != nil {
		t.Fatalf("the worked-on card was deleted with its history: %v", err)
	}
	if priority != "RETIRÉ" {
		t.Errorf("priority = %q, expected RETIRÉ", priority)
	}
	// the message template follows the rank: left alone, it would keep
	// regenerating "you presented X"
	if rank != "no_signal" || candidate != "" {
		t.Errorf("rank=%q recent_candidate=%q: the thank-you message survives",
			rank, candidate)
	}
	// reserved but not yet contacted, and contacted without a reservation:
	// each satisfies ONE half of the disjunction, and each must survive
	for _, insee := range []string{"01002", "01003"} {
		var priority string
		if err := s.pool.QueryRow(context.Background(),
			"SELECT priority FROM mayors WHERE insee_code=$1", insee).
			Scan(&priority); err != nil {
			t.Fatalf("%s was deleted with the team's work on it: %v", insee, err)
		}
		if priority != "RETIRÉ" {
			t.Errorf("%s: priority = %q, expected RETIRÉ", insee, priority)
		}
	}

	var gone int
	if err := s.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM mayors WHERE insee_code='01001'").Scan(&gone); err != nil {
		t.Fatal(err)
	}
	if gone != 0 {
		t.Error("an untouched target removed from the CSV was kept")
	}
}

// The production shape: both variables set, as the chart wires them from a
// Secret, and the seeded account deactivated during an incident. Seeding
// refreshes its password — the operator may be rotating it — but must not
// switch it back on: the stolen password IS the environment value, so a
// rolling update would hand the account straight back. Startup then refuses,
// because an instance nobody can enter is not a running instance.
func TestSeedingDoesNotReactivateADeactivatedAccount(t *testing.T) {
	pool := testDatabase(t)
	t.Setenv("PARAPHE_ADMIN_EMAIL", "coord@exemple.fr")
	t.Setenv("PARAPHE_ADMIN_PASSWORD", "mot-de-passe-d-amorçage")
	ctx := context.Background()
	org := orgOfSlug(t, pool, testSlug)
	var err error
	var active bool
	asMaintenance(t, pool, func(tx pgx.Tx) {
		if _, e := tx.Exec(ctx, "DELETE FROM accounts"); e != nil {
			t.Fatal(e)
		}
		if _, e := tx.Exec(ctx,
			"INSERT INTO accounts(org_id, email, name, password_hash, role, active) "+
				"VALUES($1,'coord@exemple.fr','Coord','x','coordination',FALSE)",
			org); e != nil {
			t.Fatal(e)
		}
		err = bootstrap(ctx, tx, org)
		if e := tx.QueryRow(ctx,
			"SELECT active FROM accounts WHERE org_id=$1 AND email='coord@exemple.fr'",
			org).Scan(&active); e != nil {
			t.Fatal(e)
		}
	})
	if active {
		t.Error("seeding switched a deactivated account back on: the password " +
			"an incident revoked opens the account again at the next restart")
	}
	if err == nil {
		t.Error("started with a deactivated coordination and both variables " +
			"set: the campaign is served and nobody can ever enter it")
	}
}

// A campaign whose only coordination account has been DEACTIVATED is just
// as unenterable as one with no account at all, and the `active` half of the
// predicate goes untested.
func TestStartupRefusesADeactivatedCoordination(t *testing.T) {
	pool := testDatabase(t)
	t.Setenv("PARAPHE_ADMIN_EMAIL", "")
	t.Setenv("PARAPHE_ADMIN_PASSWORD", "")
	ctx := context.Background()
	org := orgOfSlug(t, pool, testSlug)
	var err error
	asMaintenance(t, pool, func(tx pgx.Tx) {
		if _, e := tx.Exec(ctx, "DELETE FROM accounts"); e != nil {
			t.Fatal(e)
		}
		if _, e := tx.Exec(ctx,
			"INSERT INTO accounts(org_id, email, name, password_hash, role, active) "+
				"VALUES($1,'coord@exemple.fr','Coord','x','coordination',FALSE)",
			org); e != nil {
			t.Fatal(e)
		}
		err = bootstrap(ctx, tx, org)
	})
	if err == nil {
		t.Fatal("started with a deactivated coordination: the campaign is " +
			"served and nobody can ever enter it")
	}
}

// "Take a batch" hands out the BEST-SCORED first — that is what the whole
// score exists for (A=2, B=1, +1 for a repeat). Reversing the ORDER BY
// changed nothing in any suite.
func TestBatchServesTheBestScoresFirst(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 40, "01") // scores 100 down to 61
	email := "trieuse@exemple.fr"
	pw := createAccount(t, s, email, RoleVolunteer, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}

	code, body := c.call(http.MethodPost, "/api/batch", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("batch: %d %v", code, body)
	}
	code, board := c.call(http.MethodGet, "/api/dashboard", nil)
	if code != http.StatusOK {
		t.Fatalf("dashboard: %d", code)
	}
	mine, _ := board["mine"].([]any)
	if len(mine) == 0 {
		t.Fatal("no card assigned")
	}
	best := 100
	for _, row := range mine {
		m, _ := row.(map[string]any)
		score, err := strconv.Atoi(text(m["score"]))
		if err != nil {
			t.Fatalf("unreadable score %v", m["score"])
		}
		// the batch is the top slice: the lowest score served must still be
		// above every score left behind
		if score <= best-len(mine) {
			t.Errorf("%v was served with score %d, below the top %d",
				m["insee_code"], score, len(mine))
		}
	}
}

// A list the API would accept: the exact columns of api/vocabulary.go, and
// INSEE codes that do NOT overlap the seeded ones — the point of these
// tests is what happens to a target the CSV stops carrying.
func writeMayorCSV(t *testing.T, path string, rows int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test fixture
	if _, err := f.WriteString("\uFEFF"); err != nil {
		t.Fatal(err)
	}
	w := csv.NewWriter(f)
	w.Comma = ';'
	if err := w.Write(Cols); err != nil {
		t.Fatal(err)
	}
	for i := range rows {
		record := make([]string, len(Cols))
		for j, c := range Cols {
			switch c {
			case "insee_code":
				record[j] = fmt.Sprintf("90%03d", i)
			case "rank":
				record[j] = "has_endorsed"
			case "score":
				record[j] = "1"
			case "department":
				record[j] = "99"
			case "commune":
				record[j] = fmt.Sprintf("Commune %d", i)
			default:
				record[j] = ""
			}
		}
		if err := w.Write(record); err != nil {
			t.Fatal(err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatal(err)
	}
}

// withTx runs one function in the maintenance scope and RETURNS its error,
// where asMaintenance fails the test: here the error is the assertion.
func withTx(t *testing.T, s *Server, fn func(pgx.Tx) error) error {
	t.Helper()
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func countMayors(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM mayors").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// `cp .env.exemple .env` leaves PARAPHE_ADMIN_PASSWORD empty. The
// application then started, served every route, and offered a sign-in form
// that no password opens — with nothing in the logs. DEPLOYMENT.md and
// CLAUDE.md both promise it refuses to open instead.
func TestStartupRefusesACampaignNobodyCanEnter(t *testing.T) {
	pool := testDatabase(t)
	t.Setenv("PARAPHE_ADMIN_EMAIL", "coordination@exemple.fr")
	t.Setenv("PARAPHE_ADMIN_PASSWORD", "")

	ctx := context.Background()
	var err error
	asMaintenance(t, pool, func(tx pgx.Tx) {
		if _, e := tx.Exec(ctx, "DELETE FROM accounts"); e != nil {
			t.Fatal(e)
		}
		err = bootstrap(ctx, tx, orgOfSlug(t, pool, testSlug))
	})
	if err == nil {
		t.Fatal("started with no account: the campaign is served and " +
			"unreachable, which is what the documentation promises never happens")
	}
	if !strings.Contains(err.Error(), "PARAPHE_ADMIN_PASSWORD") {
		t.Errorf("refused without naming the variable to set: %v", err)
	}
	// and the empty password is never echoed back
	if strings.Contains(err.Error(), "\"\"") &&
		!strings.Contains(err.Error(), "an empty password") {
		t.Errorf("the message shows the secret rather than describing it: %v", err)
	}
}

func orgOfSlug(t *testing.T, pool *pgxpool.Pool, slug string) int {
	t.Helper()
	var id int
	if err := pool.QueryRow(context.Background(),
		"SELECT id FROM orgs WHERE slug=$1", slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Skipping the import when the row count is unchanged would skip precisely
// the routine change, which is count-preserving: a corrected email, a
// revised score, a false positive whose rank drops. The commune-not-
// merged fix is exactly that shape: 34 826 rows before and after, one
// mayor stops being thanked for an endorsement that is not theirs. On a
// count check, that correction never reaches a running instance and the
// volunteer keeps sending it.
func TestImportRefreshesWhenTheCountDoesNotChange(t *testing.T) {
	s, _ := testServer(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "avant.csv")
	writeMayorCSV(t, first, 1000)
	if err := withTx(t, s, func(tx pgx.Tx) error {
		return importList(context.Background(), tx, first)
	}); err != nil {
		t.Fatal(err)
	}

	// same number of rows, one of them corrected: the mayor at 90000 no
	// longer endorsed anyone
	second := filepath.Join(dir, "apres.csv")
	writeMayorCSV(t, second, 1000)
	rewriteCSV(t, second, "90000", map[string]string{
		"rank": "no_signal", "recent_candidate": "", "email": "corrige@mairie.fr",
	})
	if err := withTx(t, s, func(tx pgx.Tx) error {
		return importList(context.Background(), tx, second)
	}); err != nil {
		t.Fatal(err)
	}

	var rank, candidate, email string
	if err := s.pool.QueryRow(context.Background(),
		"SELECT rank, recent_candidate, email FROM mayors WHERE insee_code='90000'").
		Scan(&rank, &candidate, &email); err != nil {
		t.Fatal(err)
	}
	if rank != "no_signal" || candidate != "" {
		t.Errorf("rank=%q recent_candidate=%q: the correction never landed, and "+
			"the volunteer keeps thanking a mayor who endorsed nobody",
			rank, candidate)
	}
	if email != "corrige@mairie.fr" {
		t.Errorf("email=%q: a corrected address never reaches the instance", email)
	}

	// and an unchanged file is still skipped — the replica optimisation the
	// count check exists for
	if err := withTx(t, s, func(tx pgx.Tx) error {
		return importList(context.Background(), tx, second)
	}); err != nil {
		t.Fatal(err)
	}
}

// rewriteCSV rewrites one row of a list produced by writeMayorCSV.
func rewriteCSV(t *testing.T, path, insee string, changes map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(raw), "\uFEFF")))
	r.Comma = ';'
	records, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	index := map[string]int{}
	for i, name := range records[0] {
		index[name] = i
	}
	for _, record := range records[1:] {
		if record[index["insee_code"]] != insee {
			continue
		}
		for column, value := range changes {
			record[index[column]] = value
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test fixture
	if _, err := f.WriteString("\uFEFF"); err != nil {
		t.Fatal(err)
	}
	w := csv.NewWriter(f)
	w.Comma = ';'
	if err := w.WriteAll(records); err != nil {
		t.Fatal(err)
	}
}

// OFFSET pages over a set the team is modifying: a card leaving the filter
// between two pages shifted every following offset by one, and a mayor was
// SKIPPED. In a campaign whose object is coverage, a skipped mayor is one
// nobody ever contacts. The keyset cursor is anchored to the last row
// served, so a set that shrinks behind it changes nothing.
func TestPagingSkipsNobodyWhileTheTeamWorks(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 120, "01")
	email := "lectrice@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}

	// filtered on the status, so recording one REMOVES a card from the set
	seen := map[string]bool{}
	after := ""
	for page := 0; page < 10; page++ {
		path := "/api/mayors?status=to_contact"
		if after != "" {
			path += "&after=" + url.QueryEscape(after)
		}
		code, body := c.call(http.MethodGet, path, nil)
		if code != http.StatusOK {
			t.Fatalf("page %d: %d %v", page, code, body)
		}
		rows, _ := body["rows"].([]any)
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			m, _ := row.(map[string]any)
			seen[text(m["insee_code"])] = true
		}
		// a teammate records a status on the FIRST card of this page: it
		// leaves the filtered set, and everything behind it shifts
		first, _ := rows[0].(map[string]any)
		if code, out := c.call(http.MethodPost,
			"/api/mayors/"+text(first["insee_code"])+"/status",
			map[string]any{"status": "email_sent", "note": ""}); code != http.StatusOK {
			t.Fatalf("recording: %d %v", code, out)
		}
		next, ok := body["next"].(string)
		if !ok || next == "" {
			break
		}
		after = next
	}

	// every seeded mayor is served at least once
	var missing []string
	for i := range 120 {
		insee := fmt.Sprintf("01%03d", i)
		if !seen[insee] {
			missing = append(missing, insee)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d mayor(s) never appeared on any page, so nobody will ever "+
			"contact them: %v", len(missing), missing[:min(5, len(missing))])
	}
}

// An unreadable cursor is REFUSED, not silently served as page 1: serving
// page 1 returns page 1's own cursor, so the browser asks again with the
// same token, forever. A commune carrying the separator reaches this
// without anyone forging anything.
func TestAnUnreadableCursorIsRefused(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	email := "lectrice@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	// well-formed but poisoned: a NUL byte in any of the three text fields
	// raises 22021 in PostgreSQL, and the volunteer read « erreur interne »
	// for a bookmark
	nulled := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"score":0,"department":"\u0000","commune":"","insee":""}`))
	for _, token := range []string{
		"pas du base64 !!", "b25seR90d28", "MR8yHzMfNB81", nulled,
	} {
		code, body := c.call(http.MethodGet,
			"/api/mayors?after="+url.QueryEscape(token), nil)
		if code != http.StatusBadRequest {
			t.Errorf("cursor %q: %d %v — served as a page, so the browser "+
				"receives page 1's own cursor and asks again for ever",
				token, code, body)
		}
	}
}

// The digest that decides "the list is unchanged" must cover what the
// binary READS, not only the file: a release that starts reading a column
// the file already carried would otherwise skip the import and leave that
// column NULL on every row of an updated-in-place instance, silently.
func TestTheDigestCoversWhatTheBinaryReads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "liste.csv")
	writeMayorCSV(t, path, 3)
	before, err := fileDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	saved := Cols
	defer func() { Cols = saved }()
	Cols = append(append([]string{}, Cols...), "extra_column")
	after, err := fileDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Error("same digest for a binary that reads one more column: " +
			"the import is skipped and the column stays NULL everywhere")
	}
	// identity, not count: a digest hashing len(Cols) passes the case
	// above and still skips the import when a column is SWAPPED — the
	// exact shape of adding contact_form in place of a retired column
	Cols = append(append([]string{}, saved[:len(saved)-1]...), "swapped_col")
	swapped, err := fileDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	if swapped == before {
		t.Error("same digest for a binary reading a DIFFERENT column of the " +
			"same count: the import is skipped and the new column stays NULL")
	}
}

// A mayor whose score cannot be read must still be SERVED — invisible is
// never contacted — and must not take the whole list down with a 500.
func TestAnUnreadableScoreHidesNobody(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 60, "01")
	if _, err := s.pool.Exec(context.Background(),
		"INSERT INTO mayors(insee_code, rank, score, department, commune, "+
			"last_name, first_name, title) "+
			"VALUES('01999','has_endorsed',NULL,'01','Sans Score','X','Y','M.'), "+
			"('01998','has_endorsed','','01','Score Vide','X','Y','M.')"); err != nil {
		t.Fatal(err)
	}
	email := "lectrice@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}

	seen := map[string]bool{}
	after := ""
	for page := 0; page < 10; page++ {
		path := "/api/mayors"
		if after != "" {
			path += "?after=" + url.QueryEscape(after)
		}
		code, body := c.call(http.MethodGet, path, nil)
		if code != http.StatusOK {
			t.Fatalf("page %d: %d %v — one unreadable score took the whole "+
				"list down, and with it every screen", page, code, body)
		}
		rows, _ := body["rows"].([]any)
		for _, row := range rows {
			m, _ := row.(map[string]any)
			seen[text(m["insee_code"])] = true
		}
		next, ok := body["next"].(string)
		if !ok || next == "" {
			break
		}
		after = next
	}
	for _, insee := range []string{"01999", "01998"} {
		if !seen[insee] {
			t.Errorf("%s appeared on no page: an unreadable score makes a "+
				"mayor invisible, hence never contacted", insee)
		}
	}
}

// The batch size and the public name are edited in "Mon équipe" like the
// rest of the campaign, and the restart put them back to the file.
func TestRestartKeepsTheBatchSizeAndTheName(t *testing.T) {
	s, _ := testServer(t)
	org := orgID(t, s, testSlug)
	execAsMaintenance(t, s,
		"UPDATE orgs SET batch_size=25, name='Camille Réel' WHERE id=$1", org)

	cfg := testConfig() // BatchSize 2, no override
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if _, err := ensureOrg(context.Background(), tx, testSlug, cfg); err != nil {
			t.Fatal(err)
		}
	})
	var size int
	var name string
	if err := s.pool.QueryRow(context.Background(),
		"SELECT batch_size, name FROM orgs WHERE id=$1", org).
		Scan(&size, &name); err != nil {
		t.Fatal(err)
	}
	if size != 25 {
		t.Errorf("batch size %d after restart: volunteers take %d cards where "+
			"coordination asked for 25, and nothing says so", size, size)
	}
	if name != "Camille Réel" {
		t.Errorf("name %q after restart: the campaign's public name went back "+
			"to the file's template", name)
	}
}

// A score that is not an integer takes every listing query down with a 500
// — every screen of every volunteer, on one bad row. It is refused at
// import, by name, rather than discovered in production.
func TestImportRefusesAScoreThatIsNotANumber(t *testing.T) {
	for _, c := range []struct{ name, score string }{
		{"a word", "trois"},
		// the listing negates the score, so the lowest int32 overflows on
		// the way OUT: a value ParseInt accepts still answers "integer out
		// of range" on every page
		{"the lowest int32", "-2147483648"},
		// bitSize 32, not 64: Atoi accepts this, PostgreSQL's `int` does
		// not, and one such row answered 500 on every listing
		{"above int32", "3000000000"},
		// U+00A0: Go's TrimSpace strips it, PostgreSQL's `int` input does
		// not — a guard that trims the Go way accepts a value that still
		// raises at query time
		{"a no-break space", "\u00a05"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, _ := testServer(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "liste.csv")
			writeMayorCSV(t, path, 1000)
			rewriteCSV(t, path, "90000", map[string]string{"score": c.score})
			err := withTx(t, s, func(tx pgx.Tx) error {
				return importList(context.Background(), tx, path)
			})
			if err == nil {
				t.Fatal("imported an unreadable score: the list is now a 500 for everyone")
			}
			if !strings.Contains(err.Error(), "90000") {
				t.Errorf("refused without naming the row: %v", err)
			}
		})
	}
}

// An unknown pool silently dropped the filter and widened the list without
// a word, where an unknown status already answered 400.
func TestAnUnknownPoolIsRefused(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	email := "lectrice@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	if code, body := c.call(http.MethodGet, "/api/mayors?rank=autre", nil); code != http.StatusBadRequest {
		t.Errorf("rank=autre: %d %v — the filter was dropped, not refused", code, body)
	}
}

// A CAMPAIGN IS NOT ITS CANDIDATE: it presents one.
//
// The public name used to be copied from `candidat` at every save, so a
// campaign approved as « Alliance écologiste » renamed itself to « Marie
// Dupont » the first time its coordination filled the form — in its own
// header, and on the apex's PUBLIC directory, where the name an
// administrator moderated is the only thing anyone recognises it by.
//
// It is now a field of its own, and `nil` leaves it alone: a client that
// says nothing about the name must not blank it, which is the same rule as
// the directory listing beside it.
func TestTheCampaignNameIsItsOwnAndTheCandidateDoesNotMoveIt(t *testing.T) {
	s, srv := testServer(t)
	email := "coord@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	nameOf := func() string {
		var name string
		if err := s.pool.QueryRow(context.Background(),
			"SELECT name FROM orgs WHERE slug=$1", testSlug).Scan(&name); err != nil {
			t.Fatal(err)
		}
		return name
	}
	execAsMaintenance(t, s, "UPDATE orgs SET name=$1 WHERE slug=$2",
		"Alliance écologiste", testSlug)

	campaign := map[string]any{}
	for _, k := range CampaignKeys {
		campaign[k] = "valeur réelle et remplie"
	}
	campaign["candidat"] = "Camille Réel"
	if code, body := c.call(http.MethodPost, "/api/campaign",
		map[string]any{"campaign": campaign}); code != http.StatusOK {
		t.Fatalf("saving the campaign: %d %v", code, body)
	}
	if got := nameOf(); got != "Alliance écologiste" {
		t.Errorf("orgs.name = %q after a save naming a candidate: the campaign "+
			"renamed itself behind the name an administrator approved", got)
	}

	// and it is edited by naming it
	code, body := c.call(http.MethodPost, "/api/campaign",
		map[string]any{"campaign": campaign, "name": "  Alliance verte  "})
	if code != http.StatusOK {
		t.Fatalf("renaming the campaign: %d %v", code, body)
	}
	if got := nameOf(); got != "Alliance verte" {
		t.Errorf("orgs.name = %q: the name a coordination typed did not take", got)
	}
	if body["name"] != "Alliance verte" {
		t.Errorf("the answer carries name=%v: the screen would show the old one "+
			"until the next load", body["name"])
	}
	// a name of nothing but zero-width runes survives TrimSpace and would
	// reach the apex directory as a blank line. WRITTEN AS ESCAPES: such a
	// rune typed into the source is invisible to whoever reads the test —
	// which is the very property under test — and staticcheck (ST1018)
	// refuses one inside a literal for that reason.
	if code, _ := c.call(http.MethodPost, "/api/campaign",
		map[string]any{"campaign": campaign, "name": "\u200b\u200b"}); code != http.StatusBadRequest {
		t.Errorf("a name of invisible runes: %d, want 400", code)
	}
	if got := nameOf(); got != "Alliance verte" {
		t.Errorf("orgs.name = %q after a refusal", got)
	}
}

// PostgreSQL refuses U+0000 in any text value (22021) and jsonb refuses
// its escape (22P05): a NUL pasted anywhere answered « erreur interne »
// on the sender's own screen. The first fix guarded ONE write path of
// ten; the refusal now lives at the two entry points every input crosses
// (readBody for bodies, refuseUnstorableText for path and query), and
// this table probes one representative of each family. The class is
// wider than the NUL: malformed UTF-8 raises the same 22021, and %C0%80
// is the NUL itself in overlong form — refusing the rune alone still let
// the byte through. (Bodies need only the NUL case: encoding/json
// replaces malformed bytes with U+FFFD, which PostgreSQL stores.)
func TestUnstorableTextIsRefusedEverywhere(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	email := "coord@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	for _, probe := range []struct {
		name, method, path string
		body               any
	}{
		{"a campaign value (jsonb)", http.MethodPost, "/api/campaign",
			map[string]any{"campaign": map[string]any{"candidat": "Camille\x00Réel"}}},
		{"a note, the most frequent write", http.MethodPost,
			"/api/mayors/01000/status",
			map[string]any{"status": "to_call_back", "note": "rappeler\x00demain"}},
		{"a team name", http.MethodPost, "/api/team/group",
			map[string]any{"name": "Nord\x00", "departments": []string{"59"}}},
		{"a nested department", http.MethodPost, "/api/team/group",
			map[string]any{"name": "Nord", "departments": []string{"5\x009"}}},
		{"the department filter (query)", http.MethodGet,
			"/api/mayors?department=%00", nil},
		{"an INSEE in the path", http.MethodGet, "/api/mayors/01%00001", nil},
		{"a malformed byte in a query", http.MethodGet,
			"/api/mayors?department=%FF", nil},
		{"a malformed byte in the search", http.MethodGet,
			"/api/mayors?q=%FF", nil},
		{"a malformed byte in the path", http.MethodGet,
			"/api/mayors/01%FF001/status", nil},
		{"the overlong NUL %C0%80", http.MethodGet,
			"/api/mayors?department=%C0%80", nil},
		{"a lone surrogate", http.MethodGet,
			"/api/mayors?department=%ED%A0%80", nil},
	} {
		t.Run(probe.name, func(t *testing.T) {
			code, body := c.call(probe.method, probe.path, probe.body)
			if code != http.StatusBadRequest {
				t.Errorf("%d %v — PostgreSQL raises 22021/22P05 and the sender "+
					"reads « erreur interne »", code, body)
			}
		})
	}
}

// Both columns are btree-indexed (teams_org_name, accounts_pkey): past
// ~2 690 bytes of pasted text PostgreSQL refuses the index row (54000)
// and the lead read « erreur interne » for a paste.
func TestOversizedNameAndEmailAreRefused(t *testing.T) {
	s, srv := testServer(t)
	email := "coord@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	// INCOMPRESSIBLE, and only just over the bound: PostgreSQL compresses
	// index tuples, so 3 000 repeated 'b' fit happily — a test built on
	// repeated text stayed green with the bound raised to 2 000, where
	// random runes of the same count raise 54000 for real.
	code, body := c.call(http.MethodPost, "/api/team/group", map[string]any{
		"name": randomRunes(t, 201), "departments": []string{"01"},
	})
	if code != http.StatusBadRequest {
		t.Errorf("201-rune team name: %d %v — over the announced bound", code, body)
	}
	code, body = c.call(http.MethodPost, "/api/team/account", map[string]any{
		"email": "a@" + randomRunes(t, 253) + ".fr",
		"name":  "Jeanne", "role": RoleCoordination,
	})
	if code != http.StatusBadRequest {
		t.Errorf("255-rune email: %d %v — over the announced bound", code, body)
	}
	// and the bounds still accept what a real campaign types. The department
	// is one the seeded mayors bear: this door reads a perimeter like every
	// other now, so a label nobody carries is refused here too — which is
	// what the test below asserts on purpose.
	seedMayors(t, s, 2, "71")
	code, body = c.call(http.MethodPost, "/api/team/group", map[string]any{
		"name": "Équipe Saône-et-Loire — secteur nord", "departments": []string{"71"},
	})
	if code != http.StatusCreated {
		t.Errorf("an ordinary accented team name was refused: %d %v", code, body)
	}
}

// randomRunes: incompressible text of an exact rune count. Multi-byte on
// purpose — a bound counted in bytes would refuse fewer characters than
// its message announces, and index compression hides a repeated payload.
func randomRunes(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	out := make([]rune, n)
	for i, x := range b {
		out[i] = rune(0x0100 + int(x)) // Latin Extended-A onwards: 2 bytes each
	}
	return string(out)
}

// The public form's email BECOMES the primary key of `accounts` on
// acceptance, and that column is btree-indexed. Unbounded, a request
// could be filed that no administrator can ever accept (54000 on their
// own screen) — and which holds the slug `pending` for good, the very
// squat moderation exists to prevent.
func TestAPublicRequestCannotSquatWithAnUnacceptableEmail(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	_, srv := testServer(t)
	c := clientOn(t, srv, "paraphe.test")
	code, body := c.call(http.MethodPost, "/api/request", map[string]any{
		"slug": "camille2027", "name": "Camille Réel",
		"requester_email": "a@" + randomRunes(t, 3000) + ".fr",
		"requester_name":  "Jeanne Bénévole",
	})
	if code != http.StatusBadRequest {
		t.Fatalf("oversized requester email: %d %v — the acceptance would "+
			"answer 500 and the slug would stay pending for ever", code, body)
	}
	// nothing is filed, so the name is still available to its campaign
	code, body = c.call(http.MethodPost, "/api/request", map[string]any{
		"slug": "camille2027", "name": "Camille Réel",
		"requester_email": "camille@exemple.fr",
		"requester_name":  "Camille Réel",
	})
	if code != http.StatusCreated {
		t.Errorf("the real campaign could not request its own name: %d %v", code, body)
	}
}

// The admission gate must sit BEFORE the pool connection is taken: queued
// on the scrypt gate INSIDE inScope, an anonymous attempt held a
// PostgreSQL connection for nothing — 200 of them measured 8.8 ms → 2.17 s
// on every authenticated request. Saturating the admission by hand, a
// sign-in must hang at the door while an authenticated request still
// answers instantly: the queue holds no connection.
func TestSignInWaitsOutsideTheDatabase(t *testing.T) {
	s, srv := testServer(t)
	email := "coord@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	for range cap(signInAdmission) {
		signInAdmission <- struct{}{}
	}
	defer func() {
		for range cap(signInAdmission) {
			<-signInAdmission
		}
	}()
	// hangs at the door: a client that gives up must get nothing, not a 401
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		srv.URL+"/api/session",
		strings.NewReader(`{"email":"x@exemple.fr","password":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if resp, err := srv.Client().Do(req); err == nil {
		resp.Body.Close() //nolint:errcheck // test
		t.Fatalf("sign-in answered %d through a saturated admission gate: "+
			"it waited INSIDE the database again", resp.StatusCode)
	}
	// while the whole pool stays available to everyone signed in
	if code, body := c.call(http.MethodGet, "/api/config", nil); code != http.StatusOK {
		t.Errorf("/api/config: %d %v — the queue is holding connections", code, body)
	}
}

// A body arriving one byte at a time must not hold a database connection.
// inScope opens its transaction BEFORE the handler calls readBody, so a
// dribbling caller sat idle in transaction until ReadTimeout (30 s): four
// such sockets took the whole pool of a small VPS — pgx defaults MaxConns
// to max(4, NumCPU) — and every authenticated request hung, for zero CPU
// and zero memory on the attacker's side.
func TestADribblingBodyHoldsNoConnection(t *testing.T) {
	// the apex hosting form: public, and the one anonymous POST with no
	// admission gate in front of it — /api/session is bounded by
	// admitSignIn, which would hide the defect being tested
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	email := "coord@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	c := clientOn(t, srv, testSlug+".paraphe.test")
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	// Raw sockets, so they must speak TLS themselves: the point is to hold
	// connections open with an unfinished body, and a plaintext socket to a
	// TLS server never reaches the handler at all — it dies in the
	// handshake, and the test would prove nothing while staying green.
	url := strings.TrimPrefix(srv.URL, "https://")
	tlsConfig := srv.Client().Transport.(*http.Transport).TLSClientConfig
	var open []net.Conn
	defer func() {
		for _, conn := range open {
			conn.Close() //nolint:errcheck // test
		}
	}()
	// MORE sockets than the pool has connections, whatever this machine
	// decided: pgx sizes MaxConns from NumCPU, so a fixed count proves
	// nothing on a big builder and everything on a small VPS
	for range int(s.pool.Config().MaxConns) + 4 {
		conn, err := tls.Dial("tcp", url, tlsConfig)
		if err != nil {
			t.Fatal(err)
		}
		open = append(open, conn)
		if _, err := fmt.Fprintf(conn, "POST /api/request HTTP/1.1\r\n"+
			"Host: paraphe.test\r\nContent-Type: application/json\r\n"+
			"Content-Length: 60\r\n\r\n{"); err != nil {
			t.Fatal(err)
		}
	}
	// the announced bodies never arrive; the API must still answer
	done := make(chan int, 1)
	go func() {
		code, _ := c.call(http.MethodGet, "/api/config", nil)
		done <- code
	}()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Errorf("/api/config answered %d while bodies dribbled", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("/api/config never answered: the dribbling sockets are holding " +
			"pool connections idle in transaction")
	}
}

// The campaign is nine free values, the largest field of the public form
// — and bounding the four scalars around it while leaving this one open
// let one anonymous client write 89 MB in under four seconds. Nothing
// ever deletes a hosting request, and a full disk takes down every
// campaign on the instance.
func TestAPublicRequestCannotFillTheDisk(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	_, srv := testServer(t)
	c := clientOn(t, srv, "paraphe.test")
	code, body := c.call(http.MethodPost, "/api/request", map[string]any{
		"slug": "camille2027", "name": "Camille Réel",
		"requester_email": "camille@exemple.fr", "requester_name": "Camille Réel",
		// incompressible, or PostgreSQL's TOAST hides the payload
		"campaign": map[string]any{"candidat": randomRunes(t, 2001)},
	})
	if code != http.StatusBadRequest {
		t.Errorf("oversized campaign value: %d %v — 9 unbounded fields per "+
			"anonymous request, and no request is ever deleted", code, body)
	}
	// and an ordinary presentation still goes through
	code, body = c.call(http.MethodPost, "/api/request", map[string]any{
		"slug": "camille2027", "name": "Camille Réel",
		"requester_email": "camille@exemple.fr", "requester_name": "Camille Réel",
		"campaign": map[string]any{
			"candidat_description_longue": strings.Repeat("Une phrase. ", 100),
		},
	})
	if code != http.StatusCreated {
		t.Errorf("an ordinary long presentation was refused: %d %v", code, body)
	}
}

// The moderation screen reads the queue with LIMIT 200 and no search: a
// real campaign's request being pushed off the only page that can accept
// it by 200 anonymous requests on distinct slugs.
func TestTheModerationQueueCannotBeDrowned(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	c := clientOn(t, srv, "paraphe.test")
	filed := 0
	for i := range maxPendingRequests + 10 {
		code, _ := c.call(http.MethodPost, "/api/request", map[string]any{
			"slug": fmt.Sprintf("squat-%d", i), "name": "Campagne",
			"requester_email": "x@exemple.fr", "requester_name": "X",
		})
		if code == http.StatusCreated {
			filed++
		}
	}
	if filed > maxPendingRequests {
		t.Errorf("%d requests filed with a cap of %d: the storage is "+
			"unbounded, and no request is ever deleted", filed, maxPendingRequests)
	}
	// The invariant that matters is not « a cap exists » — the first
	// a cap on storage is not one. What is guaranteed is that a filed
	// request is NEVER hidden from the only screen that can accept it.
	// the administration lives in the INSTANCE scope, not in a campaign
	email := "admin@paraphe.test"
	execAsMaintenance(t, s,
		"INSERT INTO accounts(org_id, email, name, password_hash, role) "+
			"VALUES($1,$2,$3,$4,$5)",
		OrgInstance, email, "Administration",
		testHash(t, "mot-de-passe-admin"), RoleAdministration)
	admin := clientOn(t, srv, "paraphe.test")
	if code := admin.signIn(email, "mot-de-passe-admin"); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	code, body := admin.call(http.MethodGet, "/api/admin/requests", nil)
	if code != http.StatusOK {
		t.Fatalf("moderation queue: %d %v", code, body)
	}
	queue, _ := body["requests"].([]any)
	shown := map[string]bool{}
	for _, row := range queue {
		if m, ok := row.(map[string]any); ok {
			shown[text(m["slug"])] = true
		}
	}
	for i := range filed {
		if slug := fmt.Sprintf("squat-%d", i); !shown[slug] {
			t.Fatalf("%s was filed and does not appear on the moderation "+
				"screen: it holds its slug and nobody can decide on it", slug)
		}
	}
}

// …and it hides none once the table is PAST the ceiling, which is the only
// state where the read's own LIMIT could bite.
//
// The twin of TestTheQueueHidesNoPendingRequestEvenOverTheCeiling, written
// twice because the lesson was learned on the team form and not here. The
// ceiling was read before the insert, so two clients both saw 999 and both
// wrote; the read then carried a LIMIT of its own, showed its newest
// thousand, and dropped the OLDEST — the legitimate early requests — off the
// only screen that can accept them, with no way for those campaigns to know
// and no decision ever bringing them back.
//
// The rows go in directly, as a race is able to leave them: through the
// route, limitHostingIP refuses the fourth probe of the hour.
func TestTheHostingQueueHidesNoPendingRequestEvenOverTheCeiling(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	const over = maxPendingRequests + 25
	execAsMaintenance(t, s,
		"INSERT INTO hosting_requests(slug, name, campaign, requester_email, "+
			"requester_name, message, state, ts, listed) "+
			"SELECT 'squat-'||i, 'Campagne', '{}'::jsonb, 'x@exemple.fr', "+
			"'X', '', 'pending', '2026-01-01T00:00', true "+
			"FROM generate_series(1,$1) AS i", over)

	// the administration lives in the INSTANCE scope, not in a campaign
	email := "admin@paraphe.test"
	execAsMaintenance(t, s,
		"INSERT INTO accounts(org_id, email, name, password_hash, role) "+
			"VALUES($1,$2,$3,$4,$5)",
		OrgInstance, email, "Administration",
		testHash(t, "mot-de-passe-admin"), RoleAdministration)
	admin := clientOn(t, srv, "paraphe.test")
	if code := admin.signIn(email, "mot-de-passe-admin"); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	code, body := admin.call(http.MethodGet, "/api/admin/requests", nil)
	if code != http.StatusOK {
		t.Fatalf("moderation queue: %d %v", code, body)
	}
	queue, _ := body["requests"].([]any)
	if len(queue) != over {
		t.Fatalf("the queue shows %d of %d pending requests: %d are hidden "+
			"from the only screen that can accept them, and the oldest go "+
			"first", len(queue), over, over-len(queue))
	}
	// and the earliest one — the one a flood pushes off the page — is there
	shown := map[string]bool{}
	for _, row := range queue {
		if m, ok := row.(map[string]any); ok {
			shown[text(m["slug"])] = true
		}
	}
	if !shown["squat-1"] {
		t.Error("the oldest pending request is not on the screen")
	}
}

// A body over the ceiling is distinguishable from invalid JSON: the
// sender can shorten what they wrote instead of hunting a syntax error.
func TestAnOversizedBodyIsRefusedAsTooLarge(t *testing.T) {
	s, srv := testServer(t)
	email := "coord@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	code, body := c.call(http.MethodPost, "/api/campaign", map[string]any{
		"campaign": map[string]any{"candidat": strings.Repeat("a", maxBodySize+1)},
	})
	if code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: %d %v, expected 413", code, body)
	}
}

// jsonOnly reads the body BEFORE authentication, so this ceiling is also
// what an anonymous socket can make the process hold — at 1 MiB, 64 of
// them held 73 MB and the server answered `100 Continue` to a caller with
// no session. It must also stay above every legitimate body, and the
// public form is the largest: this test walks both edges, so raising the
// ceiling or a route's own limits cannot silently break the other.
func TestTheBodyCeilingHoldsBothEdges(t *testing.T) {
	if maxBodySize > 128<<10 {
		t.Errorf("maxBodySize = %d: an anonymous socket holds that much "+
			"before anyone is authenticated", maxBodySize)
	}
	// Arithmetic, not just the one body below: raising any ceiling without
	// raising this one refuses a legitimate request in 413, and nothing
	// would say why. 4 bytes per rune is the worst case UTF-8 allows.
	const utf8Worst = 4
	widest := utf8Worst * (len(CampaignKeys)*maxCampaignRunes + maxNoteRunes +
		2*maxNameRunes + maxEmailRunes)
	if widest >= maxBodySize {
		t.Errorf("the route's own ceilings add up to %d bytes, over a body "+
			"limit of %d: a request this application invites cannot be sent",
			widest, maxBodySize)
	}
	// The logo route, whose ceiling is counted in BYTES and whose body is
	// the image in base64. Its own arithmetic, because it is its own
	// request: the campaign form and the upload are two calls precisely
	// because their sum does not fit.
	base64Grows := (maxLogoBytes + 2) / 3 * 4
	envelope := len(`{"data_uri":"data:image/svg+xml;base64,"}`)
	if widestLogo := base64Grows + envelope; widestLogo >= maxBodySize {
		t.Errorf("a logo at maxLogoBytes (%d) travels as %d bytes of base64 "+
			"plus %d of envelope, over a body limit of %d: the largest logo "+
			"the application invites answers 413",
			maxLogoBytes, base64Grows, envelope, maxBodySize)
	}
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	_, srv := testServer(t)
	c := clientOn(t, srv, "paraphe.test")
	// a request at EVERY ceiling the route allows, in 4-byte runes
	campaign := map[string]any{}
	for _, k := range CampaignKeys {
		campaign[k] = strings.Repeat("𝄞", maxCampaignRunes)
	}
	code, body := c.call(http.MethodPost, "/api/request", map[string]any{
		"slug": "camille2027", "name": strings.Repeat("é", maxNameRunes),
		"requester_email": "a@" + strings.Repeat("b", maxEmailRunes-8) + ".fr",
		"requester_name":  strings.Repeat("é", maxNameRunes),
		"message":         strings.Repeat("é", maxNoteRunes),
		"campaign":        campaign,
	})
	if code != http.StatusCreated {
		t.Errorf("a request at every ceiling the route itself allows was "+
			"refused: %d %v — the body ceiling is below the sum of them",
			code, body)
	}
}

// One row per status write, never deleted, and re-read on every write:
// this is the most frequent write in the app. Unbounded, 300 posts from one
// ordinary volunteer hold 386 MB of heap.
func TestACardHistoryIsBounded(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 1, "01")
	email := "benevole@exemple.fr"
	pw := createAccount(t, s, email, RoleVolunteer, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	// a note nobody could type, refused by its own length
	code, body := c.call(http.MethodPost, "/api/mayors/01000/status",
		map[string]any{
			"status": "to_call_back",
			"note":   strings.Repeat("é", maxNoteRunes+1),
		})
	if code != http.StatusBadRequest {
		t.Errorf("oversized note: %d %v — one row per write, never deleted",
			code, body)
	}

	// and the history a card carries is bounded whatever is written
	org := orgID(t, s, testSlug)
	for i := range 250 {
		execAsMaintenance(t, s,
			"INSERT INTO notes(org_id, insee_code, volunteer, status, note, ts) "+
				"VALUES($1,'01000',$2,'to_call_back',$3,'2026-01-01')",
			org, email, fmt.Sprintf("note %d", i))
	}
	code, body = c.call(http.MethodGet, "/api/mayors/01000", nil)
	if code != http.StatusOK {
		t.Fatalf("card: %d %v", code, body)
	}
	notes, _ := body["notes"].([]any)
	if len(notes) > 200 {
		t.Errorf("the card carries %d notes: this history is re-read on "+
			"every status write, and each write pays for all of it", len(notes))
	}
	if len(notes) == 0 {
		t.Error("no history at all: the bound must cap it, not hide it")
	}
}

// The teams column is int4: beyond its range pgx cannot even encode the
// argument, so MaxInt32 answered a clean 400 and MaxInt32+1 an « erreur
// interne » on coordination's own screen.
func TestATeamIdBeyondInt4IsRefused(t *testing.T) {
	s, srv := testServer(t)
	email := "coord@exemple.fr"
	pw := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	for _, id := range []int64{math.MaxInt32 + 1, math.MinInt32 - 1} {
		code, body := c.call(http.MethodPost, "/api/team/account", map[string]any{
			"email": "benevole@exemple.fr", "name": "Jeanne",
			"role": RoleVolunteer, "team_id": id,
		})
		if code != http.StatusBadRequest {
			t.Errorf("team_id=%d: %d %v — pgx cannot encode it into int4",
				id, code, body)
		}
	}
}

// The one endpoint another origin may read. It exists so a volunteer of a
// hosted campaign does not retype nine fields, and it must carry ONLY what
// already travels in every message to a mayor.
func TestThePublicCampaignLeaksNothingOperational(t *testing.T) {
	// Without a base domain EVERY host resolves to the bootstrap campaign,
	// without it the host below is decorative and the apex branch is covered
	// by nothing.
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	// a campaign at its template values pre-fills nothing
	c := clientOn(t, srv, testSlug+".paraphe.test")
	execAsMaintenance(t, s,
		`UPDATE orgs SET campaign = campaign || '{"candidat":"Prénom NOM"}'::jsonb`)
	if code, _ := c.call(http.MethodGet, "/api/campaign/public", nil); code != http.StatusConflict {
		t.Errorf("an unconfigured campaign answered %d: it would spread "+
			"\"Prénom NOM\" to volunteers with no way to know", code)
	}

	execAsMaintenance(t, s,
		`UPDATE orgs SET campaign = campaign || '{"candidat":"Camille Réel"}'::jsonb`)
	code, body := c.call(http.MethodGet, "/api/campaign/public", nil)
	if code != http.StatusOK {
		t.Fatalf("configured campaign: %d %v", code, body)
	}
	campaign, _ := body["campaign"].(map[string]any)
	if campaign["candidat"] != "Camille Réel" {
		t.Errorf("candidat = %v", campaign["candidat"])
	}
	// nothing beyond the campaign: no account state, no batch size, no
	// list of keys still unfilled
	for _, forbidden := range []string{"no_account", "batch_size", "unfilled",
		"statuses", "ranks", "source_url"} {
		if _, present := body[forbidden]; present {
			t.Errorf("%q is served across origins: operational detail, not "+
				"campaign", forbidden)
		}
	}
	for k := range campaign {
		if !slices.Contains(CampaignKeys, k) {
			t.Errorf("campaign carries %q, which is not a campaign key", k)
		}
	}
}

func TestThePublicCampaignAllowsTheOtherOrigin(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	execAsMaintenance(t, s,
		`UPDATE orgs SET campaign = campaign || '{"candidat":"Camille Réel"}'::jsonb`)
	c := clientOn(t, srv, testSlug+".paraphe.test")
	resp, err := c.http.Do(c.request(http.MethodGet, "/api/campaign/public", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test
	// the published browser version lives on another origin, and without
	// this header the pre-fill cannot work at all
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, expected *", got)
	}

	// And on the REFUSALS too: without it the browser discards the body, so
	// a typo'd slug or the apex surfaces as "Failed to fetch" instead of
	// the sentence the API took care to write.
	for _, host := range []string{"paraphe.test", "inconnue.paraphe.test",
		"www.paraphe.test"} {
		other := clientOn(t, srv, host)
		refusal, err := other.http.Do(
			other.request(http.MethodGet, "/api/campaign/public", nil))
		if err != nil {
			t.Fatal(err)
		}
		defer refusal.Body.Close() //nolint:errcheck // test
		if refusal.StatusCode == http.StatusOK {
			t.Errorf("%s served a campaign", host)
		}
		if got := refusal.Header.Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("%s refused without the header (%q): the browser will "+
				"say \"Failed to fetch\"", host, got)
		}
	}

	// A poisoned query is refused by the MIDDLEWARE, before the wrapper
	// that usually sets this header ever runs: the refusal must carry the
	// header itself, or the browser discards the body here too.
	poisoned, err := c.http.Do(c.request(http.MethodGet,
		"/api/campaign/public?org=%00", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer poisoned.Body.Close() //nolint:errcheck // test
	if poisoned.StatusCode != http.StatusBadRequest {
		t.Errorf("poisoned query: %d, expected 400", poisoned.StatusCode)
	}
	if got := poisoned.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("the middleware refusal lost the header (%q): the one route "+
			"built to be read elsewhere answers \"Failed to fetch\"", got)
	}
}

// One row out of 34 826 with an empty score answered 500 on "take a batch",
// for the whole campaign: the import guard let the empty string through
// while the allocating route cast it without NULLIF. Both sides now.
func TestAnEmptyScoreBreaksNoRoute(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 5, "01")
	if _, err := s.pool.Exec(context.Background(),
		"UPDATE mayors SET score='' WHERE insee_code='01002'"); err != nil {
		t.Fatal(err)
	}
	email := "benevole@exemple.fr"
	pw := createAccount(t, s, email, RoleVolunteer, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	if code, body := c.call(http.MethodPost, "/api/batch", map[string]any{}); code != http.StatusOK {
		t.Errorf("take a batch: %d %v — one bad row and nobody in the "+
			"campaign can reserve anything", code, body)
	}
	if code, body := c.call(http.MethodGet, "/api/dashboard", nil); code != http.StatusOK {
		t.Errorf("dashboard: %d %v", code, body)
	}

	// and the import refuses it rather than leaving it there
	dir := t.TempDir()
	path := filepath.Join(dir, "liste.csv")
	writeMayorCSV(t, path, 1000)
	rewriteCSV(t, path, "90000", map[string]string{"score": ""})
	if err := withTx(t, s, func(tx pgx.Tx) error {
		return importList(context.Background(), tx, path)
	}); err == nil {
		t.Error("imported an empty score: a crossing that went wrong, kept")
	}
}

// The listing route refused an unknown pool and the ALLOCATING one did not:
// the volunteer believes they are drawing from the priority list and is
// handed the whole file.
func TestTakingABatchRefusesAnUnknownPool(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	email := "benevole@exemple.fr"
	pw := createAccount(t, s, email, RoleVolunteer, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	code, body := c.call(http.MethodPost, "/api/batch", map[string]any{"rank": "autre"})
	if code != http.StatusBadRequest {
		t.Errorf("batch with rank=autre: %d %v — the filter was dropped, and "+
			"the whole file was drawn from", code, body)
	}
}
