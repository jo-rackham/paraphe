package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// privilegedPool: the same database, reached as the ADMINISTRATION role —
// a superuser, so RLS does not apply to it. This is the configuration the
// application refuses to start on in multi-campaign mode, and the one a
// careless deployment lands in: the official PostgreSQL image makes
// POSTGRES_USER a superuser.
func privilegedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("PARAPHE_TEST_DATABASE_URL"))
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// The whole point of scoping every query: with RLS gone, the walls still
// hold. Without this test, the application-level filters are a claim; the
// only proof is to remove the database's wall and look.
func TestWallsHoldWithoutRLS(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	seedMayors(t, s, 6, "01")
	a := orgID(t, s, testSlug)
	b := createOrg(t, s, "other", "Other campaign")

	const neighbourVolunteer = "b-only@exemple.fr"
	const neighbourNote = "NOTE QUI APPARTIENT A LA CAMPAGNE B"
	execAsMaintenance(t, s,
		"INSERT INTO assignments(org_id, insee_code, volunteer, status) "+
			"VALUES($1,'01001',$2,'signed')", b, neighbourVolunteer)
	execAsMaintenance(t, s,
		"INSERT INTO notes(org_id, insee_code, volunteer, status, note, ts) "+
			"VALUES($1,'01001',$2,'signed',$3,'2026-01-01T00:00')",
		b, neighbourVolunteer, neighbourNote)
	execAsMaintenance(t, s,
		"INSERT INTO accounts(org_id, email, name, password_hash, role, active) "+
			"VALUES($1,$2,'Bénévole de B','x','volunteer',true)",
		b, neighbourVolunteer)
	// Three more of B's rows, PROMISED and SIGNED: a leak in a counter carries
	// none of the strings above, and the first version of this test grepped
	// for strings alone. A mutation making the promised-departments filter
	// always true passed it, while leaking B's coverage into A's dashboard.
	for i, st := range []string{"promised", "signed", "signed"} {
		execAsMaintenance(t, s,
			"INSERT INTO assignments(org_id, insee_code, volunteer, status) "+
				"VALUES($1,$2,$3,$4)",
			b, fmt.Sprintf("01%03d", i+2), neighbourVolunteer, st)
	}
	// The same address in BOTH campaigns, under different names: the export
	// and /api/me join accounts on the email alone.
	const shared = "commun@exemple.fr"
	for _, org := range []int{a, b} {
		name := "Moi chez A"
		if org == b {
			name = "Homonyme chez B"
		}
		execAsMaintenance(t, s,
			"INSERT INTO accounts(org_id, email, name, password_hash, role, active) "+
				"VALUES($1,$2,$3,'x','volunteer',true)", org, shared, name)
	}

	// an account in A, to sign in with
	const mine = "a-only@exemple.fr"
	hash, err := HashPassword("motdepasse-de-test-1234")
	if err != nil {
		t.Fatal(err)
	}
	execAsMaintenance(t, s,
		"INSERT INTO accounts(org_id, email, name, password_hash, role, active) "+
			"VALUES($1,$2,'Coordination A',$3,'coordination',true)", a, mine, hash)

	// RLS OFF from here on.
	s.pool = privilegedPool(t)

	// Said out loud rather than inferred: if the role stopped being
	// privileged, every assertion below would pass for the wrong reason.
	var role string
	var privileged bool
	if err := s.pool.QueryRow(context.Background(),
		"SELECT rolname, rolsuper OR rolbypassrls FROM pg_roles "+
			"WHERE rolname = current_user").Scan(&role, &privileged); err != nil {
		t.Fatal(err)
	}
	if !privileged {
		t.Fatalf("role %q is neither SUPERUSER nor BYPASSRLS: RLS would still "+
			"be doing the work, and this test would prove nothing", role)
	}

	// Witness. Without it this test would pass just as well with RLS still
	// enforcing everything, and would prove nothing about the application.
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := setOrgScope(ctx, tx, a); err != nil {
		t.Fatal(err)
	}
	var visible int
	if err := tx.QueryRow(ctx,
		"SELECT count(*) FROM assignments WHERE org_id <> $1", a).Scan(&visible); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	if visible == 0 {
		t.Fatal("the privileged role still sees nothing of the neighbour: RLS " +
			"is still applying, so this test proves nothing about the " +
			"application's own filters")
	}

	c := clientOn(t, srv, testSlug+".paraphe.test")
	if code := c.signIn(mine, "motdepasse-de-test-1234"); code != http.StatusOK {
		t.Fatalf("sign-in on campaign A: %d", code)
	}

	leaks := func(what string, body string) {
		t.Helper()
		if strings.Contains(body, neighbourVolunteer) {
			t.Errorf("%s: the neighbouring campaign's volunteer appears", what)
		}
		if strings.Contains(body, neighbourNote) {
			t.Errorf("%s: the neighbouring campaign's note appears", what)
		}
		if strings.Contains(body, "Bénévole de B") {
			t.Errorf("%s: the neighbouring campaign's account appears", what)
		}
		if strings.Contains(body, "Homonyme chez B") {
			t.Errorf("%s: the same address exists in both campaigns, and the "+
				"neighbour's name is the one that came back", what)
		}
	}

	// The dashboard answers in numbers. B holds one promise and two
	// signatures in department 01: if any of them counts here, the wall
	// leaked without a single one of B's strings appearing.
	code, dash := c.call(http.MethodGet, "/api/dashboard", nil)
	if code != http.StatusOK {
		t.Fatalf("/api/dashboard: %d", code)
	}
	stats, _ := dash["stats"].(map[string]any)
	for _, st := range []string{"promised", "signed"} {
		if n, _ := stats[st].(float64); n != 0 {
			t.Errorf("/api/dashboard: stats[%q] = %v, and every one of them "+
				"belongs to the neighbouring campaign", st, n)
		}
	}
	if n, _ := dash["departments_covered"].(float64); n != 0 {
		t.Errorf("/api/dashboard: departments_covered = %v, counted from the "+
			"neighbour's promises", n)
	}
	if promised, _ := dash["departments_with_promise"].([]any); len(promised) != 0 {
		t.Errorf("/api/dashboard: departments_with_promise = %v", promised)
	}

	// Writes. A campaign must not be able to touch its neighbour's rows, and
	// the read-only version of this test could not have told.
	before := neighbourRows(t, s, b)
	c.call(http.MethodPost, "/api/mayors/01001/status",
		map[string]any{"status": "to_call_back", "note": "note de A"})
	c.call(http.MethodPost, "/api/batch", map[string]any{})
	toggle, _ := c.call(http.MethodPost,
		"/api/team/account/"+neighbourVolunteer+"/active", nil)
	if toggle == http.StatusOK {
		t.Errorf("A deactivated an account that belongs to B")
	}
	if after := neighbourRows(t, s, b); after != before {
		t.Errorf("B held %d work rows before A wrote, %d after", before, after)
	}

	for _, path := range []string{
		"/api/dashboard", "/api/mayors", "/api/team", "/api/config", "/api/me",
		"/api/facets", "/api/campaign/public",
		"/api/mayors/01001", "/api/export.csv",
	} {
		resp, err := c.http.Do(c.request(http.MethodGet, path, nil))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", path, resp.StatusCode)
			continue
		}
		leaks(path, string(raw))
	}
}

// neighbourRows: how many work rows the other campaign holds, read from the
// scope that crosses campaigns — the only place able to check that a write
// left them alone.
func neighbourRows(t *testing.T, s *Server, org int) int {
	t.Helper()
	var n int
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"SELECT count(*) FROM assignments WHERE org_id=$1", org).Scan(&n); err != nil {
			t.Fatal(err)
		}
	})
	return n
}
