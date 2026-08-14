package main

import (
	"context"
	"go/ast"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The campaigns' wall, said twice.
//
// PostgreSQL enforces it with RLS, and TestRLSHoldsWithoutApplicationFilter
// proves that alone. But RLS only applies to a role that is neither
// SUPERUSER nor BYPASSRLS, and that is NOT the default: the official
// PostgreSQL image makes POSTGRES_USER a superuser. A deployment on a
// database whose role was handed over privileged would run with the wall
// gone, and the application would have nothing of its own to fall back on.
//
// So every query naming a walled table also names the campaign. That is a
// property nobody can hold by discipline over 30 call sites — hence this
// canary, and hence TestWallsHoldWithoutRLS, which runs the whole thing as a
// PRIVILEGED role and checks that nothing crosses.

// The list comes from multiorg.go, not from a copy: a table put under RLS
// there is covered here the same day, without anyone remembering to.
func walledTablesUpper() []string {
	out := make([]string, 0, len(walledTables))
	for _, t := range walledTables {
		out = append(out, strings.ToUpper(t))
	}
	return out
}

// Queries allowed to cross campaigns, by file:function — never a whole file.
// Each one is a deliberate crossing, and each is named here so that adding a
// second query to an exempted function does not inherit the exemption in
// silence.
var crossesCampaigns = map[string]string{
	// The mayors row is SHARED. Deleting a target that left the list must not
	// strip another campaign of its history, so "already worked on" is asked
	// across every campaign — from the maintenance scope, the only place that
	// can. db.go says the same at the query itself.
	"db.go:removeStale": "the shared mayor list: deletion must consider every campaign",
}

// sqlStatements walks the package and yields every SQL string reaching a
// call, with the file and enclosing function it was written in.
func sqlStatements(t *testing.T) []struct{ File, Func, SQL string } {
	t.Helper()
	files := apiPackage(t)
	values := stringValues(files)
	var out []struct{ File, Func, SQL string }
	for name, file := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			// Locals FIRST. stringValues resolves by name across the whole
			// package, and `filter` is a local in four different functions:
			// one of them lent its value to another and produced a query
			// nobody ever wrote. A local keeps its literal fragments even
			// when a call sits between them — which is all this canary reads.
			scoped := map[string]string{}
			for k, v := range values {
				scoped[k] = v
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				switch a := n.(type) {
				case *ast.AssignStmt:
					if len(a.Lhs) == 1 && len(a.Rhs) == 1 {
						if id, ok := a.Lhs[0].(*ast.Ident); ok {
							if txt := sqlText(a.Rhs[0], values); txt != "" {
								if a.Tok.String() == "+=" {
									scoped[id.Name] += txt
								} else {
									scoped[id.Name] = txt
								}
							}
						}
					}
				}
				return true
			})
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				for _, arg := range call.Args {
					sql := strings.ToUpper(sqlText(arg, scoped))
					if sql == "" {
						continue
					}
					out = append(out, struct{ File, Func, SQL string }{
						name, fn.Name.Name, sql})
				}
				return true
			})
		}
	}
	return out
}

func TestEveryQueryOnAWalledTableNamesTheCampaign(t *testing.T) {
	statements := sqlStatements(t)
	if len(statements) < 20 {
		t.Fatalf("only %d SQL statements found: the canary is not reading the "+
			"package, and would pass over anything", len(statements))
	}

	// FROM, JOIN, INTO, UPDATE or a comma list, capturing the ALIAS: a
	// statement that joins two walled tables and scopes only one of them
	// still contains "org_id", and a canary looking for the mere word passed
	// it — proven by mutation.
	tables := walledTablesUpper()
	touched := map[string]*regexp.Regexp{}
	for _, table := range tables {
		touched[table] = regexp.MustCompile(
			`(?:FROM|JOIN|INTO|UPDATE|,)\s+` + table + `(?:\s+(?:AS\s+)?([A-Z][A-Z0-9_]*))?`)
	}
	// words that follow a table name without being an alias
	notAnAlias := map[string]bool{
		"ON": true, "WHERE": true, "SET": true, "VALUES": true, "GROUP": true,
		"ORDER": true, "LIMIT": true, "RETURNING": true, "LEFT": true,
		"JOIN": true, "AND": true, "OR": true, "SELECT": true, "FROM": true,
	}

	seen := map[string]bool{}
	for _, st := range statements {
		key := st.File + ":" + st.Func
		for _, table := range tables {
			m := touched[table].FindStringSubmatch(st.SQL)
			if m == nil {
				continue
			}
			seen[key] = true
			if _, allowed := crossesCampaigns[key]; allowed {
				continue
			}
			want := "ORG_ID"
			if alias := m[1]; alias != "" && !notAnAlias[alias] {
				want = alias + ".ORG_ID"
			}
			if !strings.Contains(st.SQL, want) {
				t.Errorf("%s: this query touches %s and never names the "+
					"campaign for it (looked for %q). A privileged database "+
					"role would serve one campaign's work to another:\n\t%s",
					key, table, want, strings.TrimSpace(st.SQL))
			}
		}
	}

	// An exemption that no longer covers anything is a stale claim: it would
	// silently cover the next query written into that function.
	for key := range crossesCampaigns {
		if !seen[key] {
			t.Errorf("%s is exempted from the campaign wall but touches no "+
				"walled table any more — drop the exemption", key)
		}
	}
}

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
	}

	for _, path := range []string{
		"/api/dashboard", "/api/mayors", "/api/team",
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
