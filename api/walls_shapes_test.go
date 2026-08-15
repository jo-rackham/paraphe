package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The shapes the eighth adversarial round walked past, pinned one by one.
//
// They are checked against the canary's RULES rather than against the
// package, because reproducing them needed a non-test source file in it —
// which is how they were found (a probe dropped in a worktree) and not
// something that can ship. Each test names the shape and what it cost.
//
// Adding a shape here before it is handled is the point: this list is the
// specification, and the canary is the implementation.

// F4. destructiveRef knew TRUNCATE, LOCK, DROP, ALTER and COPY, and wanted
// the table adjacent to the verb. The gravest miss took one line and no
// disguise: `GRANT SELECT ON accounts TO public` hands a walled table to
// every role in the cluster.
func TestCommandsNoPredicateCanBound(t *testing.T) {
	const table = "ACCOUNTS"
	for _, sql := range []string{
		`GRANT SELECT ON ACCOUNTS TO PUBLIC`,
		`GRANT ALL ON TABLE ACCOUNTS TO SOMEONE`,
		`REVOKE ALL ON ACCOUNTS FROM PARAPHE`,
		`REVOKE SELECT ON TABLE PUBLIC.ACCOUNTS FROM PUBLIC`,
		`REINDEX TABLE ACCOUNTS`,
		`CLUSTER ACCOUNTS USING SOME_INDEX`,
		// the ones that already worked, kept so a rewrite cannot lose them
		`TRUNCATE ACCOUNTS`,
		`DROP TABLE IF EXISTS ACCOUNTS`,
		`ALTER TABLE ONLY PUBLIC.ACCOUNTS ADD COLUMN X INT`,
		`LOCK ACCOUNTS IN ACCESS EXCLUSIVE MODE`,
		`COPY ACCOUNTS FROM STDIN`,
		`DROP INDEX SOMETHING ON ACCOUNTS`,
	} {
		if !destructiveRef(table).MatchString(sql) {
			t.Errorf("no predicate can bound this, and the canary says "+
				"nothing about it:\n\t%s", sql)
		}
	}
}

// The most-used join in the package is built by a helper — `assignmentJoin`
// returns `FROM mayors m LEFT JOIN assignments t ON … AND t.org_id = …` —
// and a call to it read as its ARGUMENT alone. The whole FROM clause
// vanished from what the canary examined: no walled table, no rule to
// apply, silence. Deleting the org_id condition, which IS the wall of every
// mayor listing, went entirely unnoticed.
//
// Checked on the real package rather than on a fixture: the point is that
// THIS helper resolves, not that some helper could.
func TestAHelperReturningSQLIsRead(t *testing.T) {
	values := stringValues(apiPackage(t))
	join := values["assignmentJoin"]
	if join == "" {
		t.Fatal("assignmentJoin resolves to nothing: every query built with " +
			"it carries no readable FROM clause, so the canary passes over " +
			"all of them")
	}
	for _, want := range []string{"assignments", "org_id"} {
		if !strings.Contains(join, want) {
			t.Errorf("the resolved join does not mention %q:\n\t%s", want, join)
		}
	}
	// …and every statement built from it is one the canary can see a table
	// in, rather than a statement with no FROM at all
	seen := 0
	for _, st := range sqlStatements(t) {
		if strings.Contains(st.SQL, "ASSIGNMENTS T") {
			seen++
		}
	}
	if seen == 0 {
		t.Error("no statement resolves to one naming `assignments t`: the " +
			"join is still invisible where it is used")
	}
}

// A helper that builds its predicate from a PARAMETER is the most natural
// way to compose a scoped query, and the canary refused it: resolved against
// the package map, the parameter is unknown and contributes nothing, so
// `"… org_id=" + placeholder` came out as `… ORG_ID=` — a predicate with an
// empty right side. A refusal costs what a hole costs; it sends the next
// author around the guard.
//
// The body is learned with each parameter standing as `$ARGn` and the call
// site puts the argument back. Which means the caller is judged on what it
// PASSES, not on the helper's shape: the same helper reads as walled at a
// site handing it "$1", and unbounded at a site handing it something else.
func TestAHelperIsJudgedOnWhatItsCallerPasses(t *testing.T) {
	const src = `package main

func scopedFilter(placeholder string) string {
	return " FROM accounts WHERE org_id=" + placeholder
}

func walled()   { run("SELECT email" + scopedFilter("$1")) }
func unwalled() { run("SELECT email" + scopedFilter("anything")) }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "helper.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	files := map[string]*ast.File{"helper.go": file}
	values := stringValues(files)

	if body := values["scopedFilter"]; !strings.Contains(body, "$ARG1") {
		t.Fatalf("the parameter did not become a marker, so the predicate "+
			"has no right-hand side: %q", body)
	}

	read := map[string]string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		scoped := localScope(values, fn)
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			for _, arg := range call.Args {
				if txt := sqlTextMarked(arg, scoped); strings.Contains(txt, "ORG_ID") ||
					strings.Contains(strings.ToUpper(txt), "ORG_ID") {
					read[fn.Name.Name] = normaliseSQL(txt)
				}
			}
			return true
		})
	}

	if got := read["walled"]; !orgPredicate.MatchString(got) {
		t.Errorf("a caller passing the placeholder is refused, which is the "+
			"false positive:\n\t%s", got)
	}
	if got := read["unwalled"]; orgPredicate.MatchString(got) {
		t.Errorf("a caller passing something that is NOT a placeholder reads "+
			"as walled — the helper's shape vouched for it:\n\t%s", got)
	}
}

// A rule that never runs guards nothing, and it passes its own unit test
// while doing it. The main test drops any statement `sqlVerb` does not
// recognise, BEFORE consulting the destructive rules — so the three verbs
// added for those rules (REINDEX, CLUSTER, REVOKE) were matched by
// destructiveRef, tested in isolation, and never reached.
//
// This ties the gate to what it gates: anything the rules recognise must get
// through the door.
func TestEveryDestructiveVerbReachesTheRules(t *testing.T) {
	for _, sql := range []string{
		`GRANT SELECT ON ACCOUNTS TO PUBLIC`,
		`REVOKE ALL ON ACCOUNTS FROM PARAPHE`,
		`REINDEX TABLE ACCOUNTS`,
		`CLUSTER ACCOUNTS USING SOME_INDEX`,
		`TRUNCATE ACCOUNTS`,
		`DROP TABLE ACCOUNTS`,
		`ALTER TABLE ACCOUNTS ADD COLUMN X INT`,
		`LOCK ACCOUNTS IN ACCESS EXCLUSIVE MODE`,
		`COPY ACCOUNTS FROM STDIN`,
		`DROP SCHEMA PUBLIC CASCADE`,
		`ALTER DEFAULT PRIVILEGES IN SCHEMA PUBLIC GRANT SELECT ON TABLES TO PUBLIC`,
	} {
		caught := destructiveRef("ACCOUNTS").MatchString(sql) ||
			schemaWide.MatchString(sql)
		if !caught {
			t.Errorf("no rule recognises this at all:\n\t%s", sql)
			continue
		}
		if !sqlVerb.MatchString(sql) {
			t.Errorf("a rule recognises this, and the main test drops it "+
				"before the rule runs — sqlVerb is the gate and does not "+
				"know its verb:\n\t%s", sql)
		}
	}
}

// F4, second half. `DROP SCHEMA public CASCADE` takes every walled table at
// once and names none of them, so a pattern built from a table name can
// never see it — there is no table name in it to see.
func TestStatementsThatReachEveryTableWithoutNamingOne(t *testing.T) {
	for _, sql := range []string{
		`DROP SCHEMA PUBLIC CASCADE`,
		`ALTER SCHEMA PUBLIC RENAME TO ELSEWHERE`,
		`GRANT SELECT ON ALL TABLES IN SCHEMA PUBLIC TO PUBLIC`,
		`ALTER DEFAULT PRIVILEGES IN SCHEMA PUBLIC GRANT SELECT ON TABLES TO PUBLIC`,
	} {
		if !schemaWide.MatchString(sql) {
			t.Errorf("this reaches every walled table at once and the canary "+
				"passes over it:\n\t%s", sql)
		}
	}
	// and it must not fire on the ordinary statements of the package, or it
	// blocks the build for nothing
	for _, sql := range []string{
		`SELECT EMAIL FROM ACCOUNTS WHERE ORG_ID=$1`,
		`INSERT INTO NOTES $SUB0 VALUES $SUB1`,
		`CREATE TABLE IF NOT EXISTS ACCOUNTS $SUB0`,
	} {
		if schemaWide.MatchString(sql) {
			t.Errorf("ordinary SQL refused as schema-wide:\n\t%s", sql)
		}
	}
}

// F3 and F5. Where a table should stand, something the canary could not read
// as a name. `strings.Join(tables, ",")` left its separator and nothing else,
// so the statement read `FROM ,`: no walled table, no rule matched, silence.
func TestAnUnreadableTableIsAFinding(t *testing.T) {
	for _, sql := range []string{
		`SELECT EMAIL FROM ,`,          // a composite literal that vanished
		`SELECT EMAIL FROM $?`,         // an expression the resolver gave up on
		`SELECT EMAIL FROM %S`,         // a format verb: the table is chosen at run time
		`SELECT X FROM T JOIN %S ON A`, // …in a join
		`INSERT INTO $? VALUES $SUB0`,
		`DELETE FROM T USING %S`,
		`UPDATE %S SET X=1`,
		`SELECT EMAIL FROM`, // the text ends where the table should be
	} {
		if !unreadableTable.MatchString(sql) {
			t.Errorf("the canary cannot say which table this touches, and "+
				"passes over it as though it touched none:\n\t%s", sql)
		}
	}
	// A false positive costs as much as a hole here: it sends the next author
	// around the canary. These are ordinary, readable statements.
	for _, sql := range []string{
		// the row locking cards are allocated with — `UPDATE` ends the text
		// and is a locking clause, not a table position
		`SELECT ID FROM ASSIGNMENTS WHERE ORG_ID=$1 FOR UPDATE`,
		`SELECT ID FROM ASSIGNMENTS WHERE ORG_ID=$1 FOR UPDATE SKIP LOCKED`,
		`INSERT INTO ASSIGNMENTS $SUB0 VALUES $SUB1 ON CONFLICT $SUB2 DO UPDATE SET X=1`,
		`SELECT A.X FROM ACCOUNTS A JOIN TEAMS T USING $SUB0`,
		`SELECT EMAIL FROM ACCOUNTS WHERE ORG_ID=$1`,
		`SELECT X FROM TEAMS G, ACCOUNTS C WHERE G.ORG_ID=$1 AND C.ORG_ID=$1`,
	} {
		if loc := unreadableTable.FindStringIndex(sql); loc != nil {
			t.Errorf("ordinary SQL refused, on %q:\n\t%s",
				sql[loc[0]:loc[1]], sql)
		}
	}
}

// A column whose name merely ENDS in org_id is not the campaign. Without a
// leading word boundary, `WHERE parent_org_id = $1` matched on its tail and
// read as an unqualified campaign predicate — one column vouching for the
// wall on behalf of another.
func TestOnlyTheCampaignColumnWalls(t *testing.T) {
	for _, sql := range []string{
		`SELECT X FROM ACCOUNTS WHERE PARENT_ORG_ID = $1`,
		`SELECT X FROM ACCOUNTS WHERE SUB_ORG_ID = $1`,
		`SELECT X FROM ACCOUNTS A WHERE A.OLD_ORG_ID = $1`,
	} {
		if orgPredicate.MatchString(sql) {
			t.Errorf("a column that is not the campaign reads as the "+
				"campaign:\n\t%s", sql)
		}
	}
	// …and the real thing still does, or nothing would ever be walled
	for _, sql := range []string{
		`SELECT X FROM ACCOUNTS WHERE ORG_ID = $1`,
		`SELECT X FROM ACCOUNTS A WHERE A.ORG_ID = $1`,
		`SELECT X FROM ACCOUNTS A WHERE A.ORG_ID::INT = $1`,
		`SELECT X FROM ACCOUNTS A JOIN TEAMS T ON A.ORG_ID = T.ORG_ID`,
	} {
		if !orgPredicate.MatchString(sql) {
			t.Errorf("a correctly walled query is refused:\n\t%s", sql)
		}
	}
}

// F7. `NATURAL` was read as the table's alias, so `WHERE accounts.org_id=$1`
// addressed a name that did not exist and a correctly walled query was
// refused.
func TestJoinDecorationsAreNotAliases(t *testing.T) {
	for _, sql := range []string{
		`SELECT X FROM ACCOUNTS NATURAL JOIN TEAMS`,
		`SELECT X FROM ACCOUNTS TABLESAMPLE BERNOULLI $SUB0`,
		`SELECT X FROM ACCOUNTS WINDOW W AS $SUB0`,
		`SELECT X FROM ACCOUNTS FOR UPDATE`,
		`SELECT X FROM ACCOUNTS OFFSET $1`,
	} {
		m := tableRef("ACCOUNTS").FindStringSubmatch(sql)
		if m == nil {
			t.Fatalf("the table is no longer seen at all:\n\t%s", sql)
		}
		if alias := m[2]; alias != "" && !notAnAlias[alias] {
			t.Errorf("%q read as the table's alias, so a predicate written "+
				"`accounts.org_id=$1` addresses a name that does not exist "+
				"and the query is refused:\n\t%s", alias, sql)
		}
	}
}

// F6. `INSERT INTO accounts AS a (org_id, …) … ON CONFLICT … DO UPDATE` is
// how an upsert names the existing row. The alias between the table and the
// column list made insertNaming miss, and the write — which DOES stamp the
// campaign — was refused.
func TestAnUpsertMayNameItsTarget(t *testing.T) {
	for _, sql := range []string{
		`INSERT INTO ACCOUNTS $SUB0 VALUES $SUB1`,
		`INSERT INTO ACCOUNTS AS A $SUB0 VALUES $SUB1`,
		`INSERT INTO PUBLIC.ACCOUNTS AS A $SUB0 VALUES $SUB1`,
	} {
		if !insertNaming("ACCOUNTS").MatchString(sql) {
			t.Errorf("this INSERT names its column list and is refused "+
				"anyway:\n\t%s", sql)
		}
	}
}

// F1 and F2. stringValues walked each file whole and kept the LAST value it
// met for a name, so a function NEVER CALLED could decide what the canary
// read. The map is keyed on the name alone, so a `var` local to that dead
// function overwrote the package binding too.
//
// Both are the same defect seen twice: what is written inside a function
// belongs to that function.
func TestDeadCodeCannotDecideWhatTheCanaryReads(t *testing.T) {
	const src = `package main

// the binding the driver actually runs
var poisoned = "SELECT email FROM accounts"

// never called by anything, and it says the opposite
func neverCalled() { poisoned = "SELECT email FROM accounts WHERE org_id=$1" }

// a LOCAL of the same name, also in dead code
func alsoDead() {
	var poisoned = "SELECT email FROM accounts WHERE org_id=$1"
	_ = poisoned
}

func handler() { run(poisoned) }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "poison.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	got := stringValues(map[string]*ast.File{"poison.go": file})["poisoned"]
	if strings.Contains(got, "ORG_ID") || strings.Contains(got, "org_id") {
		t.Errorf("the canary reads a bounded query that no caller runs, "+
			"because dead code reassigned the binding:\n\tread:  %s\n\truns:  "+
			"SELECT email FROM accounts", got)
	}
	if got != "SELECT email FROM accounts" {
		t.Errorf("the package binding is no longer read at all: %q", got)
	}
}

// `sql := base` followed by `sql += " OR TRUE"`. The accumulation is what
// the canary must see: an always-true disjunction cancels whatever else the
// query says, and `neutralised` is there to catch exactly that — but only if
// the text reaches it.
//
// It did not. The map bounding the `+=` to one application was kept ACROSS
// the resolution passes, so the second pass re-ran the `:=` that precedes it
// (overwriting the accumulated text) and then skipped the `+=` as already
// applied. The canary read a walled query; the driver ran an open one.
func TestAppendedSQLIsNotLostBetweenPasses(t *testing.T) {
	const src = `package main

func handler() {
	base := "SELECT email FROM accounts WHERE org_id=$1"
	sql := base
	sql += " OR TRUE"
	run(sql)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "append.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == "handler" {
			fn = f
		}
	}
	if fn == nil {
		t.Fatal("the fixture has no handler")
	}
	got := localScope(map[string]string{}, fn)["sql"]
	if !strings.Contains(strings.ToUpper(got), "OR TRUE") {
		t.Errorf("the canary reads a query the driver does not run — the "+
			"appended text is gone, and with it the always-true disjunction "+
			"`neutralised` exists to refuse:\n\tread: %q\n\truns: %q",
			got, "SELECT email FROM accounts WHERE org_id=$1 OR TRUE")
	}
}

// …and the counterpart: what a function DOES write must still be read, or
// every query built in a local variable becomes invisible — which is a hole
// of its own, and the reason stringValues learned function bodies to begin
// with.
func TestAFunctionsOwnBindingsAreStillRead(t *testing.T) {
	const src = `package main

func handler(org int) {
	base := "SELECT email FROM accounts"
	sql := base + " WHERE org_id=$1"
	run(sql)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "local.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == "handler" {
			fn = f
		}
	}
	if fn == nil {
		t.Fatal("the fixture has no handler")
	}
	scoped := localScope(map[string]string{}, fn)
	want := "SELECT email FROM accounts WHERE org_id=$1"
	if got := scoped["sql"]; got != want {
		t.Errorf("a query built from one local into another is unreadable, "+
			"so the canary passes over it:\n\tgot  %q\n\twant %q", got, want)
	}
}
