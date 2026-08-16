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

// The verbs no predicate can bound, with the table NAMED and with the table
// replaced by a marker, must both be findings.
//
// Three rounds running produced a critical of the same shape: a table
// position one rule knew and its neighbour did not. `TABLE t` reached
// sqlVerb and tableRef and not unreadableTable; a comma reached tableRef and
// not unreadableTable; and destructiveRef knew seven verbs of which
// unreadableTable knew none — so `"TRUNCATE "+t` and `"GRANT SELECT ON "+t`
// read as statements touching no walled table at all.
//
// Both patterns are built from destructiveVerbs, and this walks that list:
// adding a verb to the guard without teaching it its unreadable form goes
// red here, rather than three months later in a round nobody ran.
func TestEveryDestructiveVerbHasAnUnreadableForm(t *testing.T) {
	// …over the same axes as every other table position. Walked on the verb
	// alone, this certified an agreement that did not hold: the schema
	// qualifier was carried by all three destructive alternatives and checked
	// on none of them, so an edit dropping it went green while
	// `TRUNCATE public.`+t went back to reading as a statement touching no
	// walled table.
	qualifiers := []string{"", "PUBLIC.", `"PUBLIC".`, "PARAPHE.PUBLIC."}
	for _, verb := range strings.Split(destructiveVerbs, "|") {
		for _, modifier := range []string{"", "ONLY "} {
			for _, schema := range qualifiers {
				named := verb + " " + modifier + schema + "ACCOUNTS"
				if !destructiveRef("ACCOUNTS").MatchString(named) {
					t.Errorf("destructiveRef does not know %q, so nothing "+
						"refuses it with the table named:\n\t%s", verb, named)
				}
				// …and the same statement with the name taken out
				for _, marker := range []string{"%S", "$?"} {
					unreadable := verb + " " + modifier + schema + marker
					if _, found := unreadablePosition(unreadable, true); !found {
						t.Errorf("%q names a table nobody can read and no rule "+
							"says so — the statement reads as touching no "+
							"walled table:\n\t%s", verb, unreadable)
					}
				}
			}
		}
	}
	// The privilege and object-governing shapes, which name their table after
	// an ON rather than after the verb — over the same qualifiers.
	for _, schema := range qualifiers {
		for _, unreadable := range []string{
			`GRANT SELECT ON ` + schema + `%S TO PUBLIC`,
			`REVOKE ALL ON ` + schema + `$? FROM PARAPHE`,
			`CREATE POLICY P ON ` + schema + `%S USING $SUB0`,
			`DROP POLICY P ON ` + schema + `$?`,
			`CREATE TRIGGER T AFTER DELETE ON ` + schema +
				`%S FOR EACH ROW EXECUTE F$SUB0`,
		} {
			if _, found := unreadablePosition(unreadable, true); !found {
				t.Errorf("the table this governs cannot be read and no rule "+
					"says so:\n\t%s", unreadable)
			}
		}
	}
	// And what must NOT be a finding: a message is not a statement. `sqlVerb`
	// lets error text through on purpose — a query hidden in a helper is
	// still a query — and a marker is something format strings are full of,
	// where a table NAME is not. These two are real messages from db.go, and
	// refusing them is a false positive, which in a guard that BLOCKS costs
	// what a hole costs.
	for _, message := range []string{
		`LOCK %D UNAVAILABLE AFTER %S: ANOTHER INSTANCE HOLDS IT`,
		`TAKING LOCK %D: %W`,
		`COPY OF THE LIST REFUSED AFTER %D ROWS`,
	} {
		if seen, found := unreadablePosition(message, false); found {
			t.Errorf("an error message was refused as a statement, on %q:"+
				"\n\t%s", seen, message)
		}
	}
}

// Every place a table can stand, read by BOTH rules.
//
// tableRef looks for a NAME there; unreadablePosition looks for a MARKER.
// Four rounds running found a place one of them knew and the other did not —
// the TABLE shorthand, the comma, the destructive verbs, and the ONLY
// modifier — and each time the fix taught the one rule and each time the next
// round found the next square of the same grid.
//
// So the grid is walked here. tablePositions and tableModifier are the lists
// both rules are built from; adding a keyword or a modifier to one of them
// without teaching the other goes red on this test, in the round that adds
// it.
func TestEveryTablePositionIsReadByBothRules(t *testing.T) {
	modifiers := []string{"", "ONLY "}
	// The dotted prefixes a name may carry. The fifth round of this class was
	// exactly this axis: the grid walked keyword × modifier, both rules
	// agreed, and both were blind — `FROM public.`+t named a walled table
	// nothing looked at, and a three-part LITERAL name escaped even the rule
	// that reads names. A grid missing an axis certifies an agreement.
	schemas := []string{"", "PUBLIC.", `"PUBLIC".`, "PARAPHE.PUBLIC."}
	for _, keyword := range strings.Split(tablePositions, "|") {
		for _, modifier := range modifiers {
			for _, schema := range schemas {
				named := "SELECT X " + keyword + " " + modifier + schema + "ACCOUNTS"
				if !tableRef("ACCOUNTS").MatchString(named) {
					t.Errorf("tableRef does not read a table after %q, so a "+
						"walled table written there is invisible to every rule "+
						"built on it:\n\t%s", keyword, named)
				}
				unreadable := "SELECT X " + keyword + " " + modifier + schema + "%S"
				if _, found := unreadablePosition(unreadable, true); !found {
					t.Errorf("a marker after %q is a table nobody can read and "+
						"no rule says so:\n\t%s", keyword, unreadable)
				}
			}
		}
	}
	// UPDATE stands apart in unreadableTable — `FOR UPDATE` ends a statement
	// and is a locking clause, not a table position — so it is walked here
	// rather than left out of the grid.
	for _, modifier := range modifiers {
		for _, schema := range schemas {
			named := "UPDATE " + modifier + schema + "ACCOUNTS SET X=1"
			if !tableRef("ACCOUNTS").MatchString(named) {
				t.Errorf("tableRef does not read the table of %q", named)
			}
			unreadable := "UPDATE " + modifier + schema + "%S SET X=1"
			if _, found := unreadablePosition(unreadable, true); !found {
				t.Errorf("a marker after UPDATE is a table nobody can read "+
					"and no rule says so:\n\t%s", unreadable)
			}
		}
	}
	// …and the comma position, which tableRef has read since the day a second
	// table hid behind one — walked over the SAME axes as the keywords above,
	// schemas included. Walked at the keyword positions and not at this one,
	// the grid certified an agreement that did not hold: `FROM accounts a,
	// public.`+t named a walled table tableRef read and unreadableTable did
	// not, and PostgreSQL cross-joined every campaign's rows into the answer.
	for _, modifier := range modifiers {
		for _, schema := range schemas {
			named := "SELECT X FROM TEAMS G, " + modifier + schema + "ACCOUNTS C"
			if !tableRef("ACCOUNTS").MatchString(named) {
				t.Errorf("tableRef does not read the table of %q", named)
			}
			unreadable := "SELECT X FROM TEAMS G, " + modifier + schema + "%S"
			if _, found := unreadablePosition(unreadable, true); !found {
				t.Errorf("a marker after a comma is a table nobody can read "+
					"and no rule says so:\n\t%s", unreadable)
			}
		}
	}
}

// A text seen at two call sites is recorded by the STRICTEST of them.
//
// The destructive rule judges only statements something RUNS — telling
// `"TRUNCATE "+t` from `lock %d in progress` by their words cannot be done,
// and whether anything executes them can. The recording deduplicates by
// text, and it kept the FIRST call visited: one line logging the statement
// before the line that runs it — `fmt.Errorf("about to run: %s", sql)` — and
// the rule never ran on a TRUNCATE PostgreSQL then ran.
//
// On a fixture rather than on the package, because the shape has to be
// written down to be tested and it must not ship.
func TestATextThatIsLoggedAndThenRunCountsAsRun(t *testing.T) {
	const src = `package main

func logsThenRuns(ctx C, tx T, table string) {
	sql := "TRUNCATE " + table
	_ = errorf("about to run: %s", sql)
	tx.Exec(ctx, sql)
}

func runsThenLogs(ctx C, tx T, table string) {
	sql := "TRUNCATE " + table
	tx.Exec(ctx, sql)
	_ = errorf("just ran: %s", sql)
}

func onlyLogs(table string) {
	_ = errorf("would run: %s", "TRUNCATE "+table)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	executed := map[string]bool{}
	for _, st := range statementsIn(map[string]*ast.File{"fixture.go": file}) {
		if strings.Contains(st.SQL, "TRUNCATE") {
			executed[st.Func] = executed[st.Func] || st.Executed
		}
	}
	for _, fn := range []string{"logsThenRuns", "runsThenLogs"} {
		if !executed[fn] {
			t.Errorf("%s runs the statement and it was recorded as not run: "+
				"the rule that judges executed statements never sees it", fn)
		}
	}
	// …and the other direction, or the gate would be no gate: a text nothing
	// runs stays unexecuted, which is what keeps five ordinary error messages
	// from being refused as SQL.
	if executed["onlyLogs"] {
		t.Error("a text that is only ever logged was recorded as run: the " +
			"gate is open, and prose goes back to being refused as SQL")
	}
}

// A statement built from a LOCAL DECLARATION is read like any other.
//
// localScope learned every local ASSIGNMENT and no local declaration, and a
// `const` holding half a statement is this package's own idiom, not an
// exotic shape. `const columns = "id, email FROM accounts "` followed by
// `"SELECT "+columns+"WHERE …"` resolved to a text with no FROM in it: the
// statement named no walled table, so no rule applied to it and the canary
// passed it in silence — while the same query written inline is refused.
//
// Not a table position two rules read differently, which is what seven
// rounds of this class had been: a whole statement neither of them ever saw.
// Both `const` and `var` are walked, and so is the shadowing direction — a
// local declaration that hides a package binding must not leave the package
// text standing in its place.
//
// On a fixture, because the shape has to be written down to be tested and it
// must not ship.
func TestAStatementBuiltFromALocalDeclarationIsRead(t *testing.T) {
	const src = `package main

var columns = "id, name FROM notes "

func fromConst() {
	const cols = "id, email FROM accounts "
	run("SELECT " + cols + "WHERE role = $1")
}

func fromVar() {
	var cols = "id, name FROM teams "
	run("SELECT " + cols + "WHERE id = $1")
}

func shadowsThePackage() {
	const columns = "id FROM assignments "
	run("SELECT " + columns + "WHERE insee_code = $1")
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	seen := map[string]string{}
	for _, st := range statementsIn(map[string]*ast.File{"fixture.go": file}) {
		seen[st.Func] = st.SQL
	}
	for fn, table := range map[string]string{
		"fromConst":         "ACCOUNTS",
		"fromVar":           "TEAMS",
		"shadowsThePackage": "ASSIGNMENTS",
	} {
		if !strings.Contains(seen[fn], table) {
			t.Errorf("%s builds its statement from a local declaration and the "+
				"canary reads %q: %s is named nowhere, so no rule applies and "+
				"an unbounded query passes in silence",
				fn, seen[fn], table)
		}
	}
	// …and the shadowed package text is NOT what stands in its place
	if strings.Contains(seen["shadowsThePackage"], "NOTES") {
		t.Errorf("a local declaration shadowing a package binding was read as "+
			"the package's: %s", seen["shadowsThePackage"])
	}
}

// A branch that DECLARES is a path of its own, like a branch that assigns.
//
// localScopeVariants enumerates one reading per path, so a wall added inside
// a branch cannot vouch for the path that skips it. It counted ASSIGNMENTS
// only — and the round that taught the reader about declarations taught the
// forgetting pass and the learning pass, not this third one. A branch
// shadowing with `const sql = "…org_id = $1…"` produced no variant at all, so
// no branch-not-taken was read; the sequential pass, which visits in source
// order and knows no block scope, then overwrote the outer text with the
// branch's. The canary judged the statement the driver runs by a decoy it
// never runs, and an unbounded outer passed behind a bounded one. Written
// `sql = "…"`, the same shape was caught throughout.
func TestABranchThatDeclaresIsAPathOfItsOwn(t *testing.T) {
	const src = `package main

func shadowsInABranch(cond bool) {
	sql := "SELECT id, note FROM notes"
	if cond {
		const sql = "SELECT id, note FROM notes WHERE org_id = $1"
		_ = sql
	}
	run(sql)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	files := map[string]*ast.File{"fixture.go": file}
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if f, ok := decl.(*ast.FuncDecl); ok {
			fn = f
		}
	}
	if fn == nil {
		t.Fatal("no function in the fixture")
	}
	// the reading the driver executes when the branch is not taken must be
	// among the variants, or nothing ever judges it
	var read []string
	for _, scope := range localScopeVariants(stringValues(files), fn) {
		sql := strings.ToUpper(scope["sql"])
		read = append(read, sql)
		if strings.Contains(sql, "NOTES") && !strings.Contains(sql, "ORG_ID") {
			return
		}
	}
	t.Errorf("no variant reads the statement the driver runs when the branch "+
		"is skipped, so a declaration inside it vouched for the path that "+
		"never executes it; what was read: %q", read)
}

// Every branching where SOME path does not bind the name is one path more.
//
// The enumeration asked what shape the branching had, not whether a path
// existed on which none of the binding branches runs — and those are
// different questions for the commonest shapes of all. A `switch` with a
// `default` set mayTakeNone to false, so a wall written in one case vouched
// for the default. A `select` never set it at all. A `for` and a `range`
// were not in the list, though a loop over nothing runs its body no time.
// And an `if` INITIALISER always runs but binds only inside the statement,
// so `if sql := "…org_id=$1…"; cond {}` left the outer sql standing for
// every use after the brace while the reader had taken the inner one.
//
// Each of these read as a bounded statement while the driver ran an
// unbounded one, on a table the campaign wall protects.
func TestEveryPathThatSkipsTheWallIsRead(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"range over nothing", `
	sql := "SELECT id FROM notes"
	for range items {
		sql += " WHERE org_id = $1"
	}
	run(sql)`},
		{"for that may not run", `
	sql := "SELECT id FROM notes"
	for i := 0; i < n; i++ {
		sql += " WHERE org_id = $1"
	}
	run(sql)`},
		{"switch with a default", `
	sql := "SELECT id FROM notes"
	switch n {
	case 1:
		sql += " WHERE org_id = $1"
	default:
	}
	run(sql)`},
		{"select whose other clause binds nothing", `
	sql := "SELECT id FROM notes"
	select {
	case <-items:
		sql += " WHERE org_id = $1"
	case <-other:
	}
	run(sql)`},
		{"an initialiser bound to the statement", `
	sql := "SELECT id FROM notes"
	if sql := sql + " WHERE org_id = $1"; sql != "" {
		_ = sql
	}
	run(sql)`},
	} {
		src := "package main\n\nfunc probe(n int, items, other chan int) {" +
			tc.body + "\n}\n"
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "fixture.go", src, 0)
		if err != nil {
			t.Fatalf("%s: parsing the fixture: %v", tc.name, err)
		}
		files := map[string]*ast.File{"fixture.go": file}
		fn := file.Decls[0].(*ast.FuncDecl)
		unwalled := false
		var read []string
		for _, scope := range localScopeVariants(stringValues(files), fn) {
			sql := strings.ToUpper(scope["sql"])
			read = append(read, sql)
			if strings.Contains(sql, "NOTES") && !strings.Contains(sql, "ORG_ID") {
				unwalled = true
			}
		}
		if !unwalled {
			t.Errorf("%s: no variant reads the statement the driver runs on "+
				"the path that skips the wall; what was read: %q",
				tc.name, read)
		}
	}
}

// A procedural body is refused whatever leads it.
//
// `DO $$…$$` is the one shape no rule after it can read: stripDollarQuoted
// empties the body before anything looks, so `procedural` is the whole
// defence. Anchored on the beginning of the text alone, ANY statement in
// front walked it past — `SELECT 1; DO $$ BEGIN TRUNCATE notes; END $$`
// reached pgx and took two campaigns' rows to none, while the canary stayed
// green: sqlVerb searches anywhere and found the SELECT, and the `;`-split
// that follows never re-asks whether a half is procedural.
//
// The axis is "what leads the semicolon", so the axis is walked — every verb
// sqlVerb knows, and a couple it does not. Adding a verb to that gate cannot
// quietly reopen this.
func TestAProceduralBodyIsRefusedWhateverLeadsIt(t *testing.T) {
	const body = " DO $$ BEGIN TRUNCATE ACCOUNTS; END $$"
	verbs := append(strings.Split(
		strings.Trim(sqlVerb.String(), `\b()`), "|"), "MERGE", "CALL")
	for _, verb := range verbs {
		verb = strings.TrimSuffix(strings.TrimPrefix(verb, `\b(`), `)\b`)
		if verb == "" {
			continue
		}
		lead := verb + " 1;"
		if !procedural.MatchString(normaliseSQL(lead + body)) {
			t.Errorf("a procedural body led by %q is not refused, and nothing "+
				"after it can read the body:\n\t%s", verb, lead+body)
		}
	}
	// …and on its own, which is where it started.
	if !procedural.MatchString(normaliseSQL(body)) {
		t.Error("a bare procedural body is not refused")
	}
	// The refusal must NOT reach the `DO UPDATE` of every upsert in the
	// package — the reason the anchor exists at all. A refusal here costs
	// what a hole costs.
	for _, ordinary := range []string{
		`INSERT INTO ACCOUNTS $SUB0 VALUES $SUB1 ON CONFLICT $SUB2 DO UPDATE SET X=1`,
		`INSERT INTO LOGIN_TOKENS $SUB0 VALUES $SUB1 ON CONFLICT $SUB2 DO NOTHING`,
		`SELECT X FROM ACCOUNTS WHERE ORG_ID=$1`,
	} {
		if procedural.MatchString(normaliseSQL(ordinary)) {
			t.Errorf("an ordinary upsert is refused as procedural:\n\t%s", ordinary)
		}
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
		// PostgreSQL's `TABLE t` shorthand is a table position like FROM,
		// and it reached sqlVerb and tableRef before it reached this rule:
		// recognised as SQL, its table unresolvable, matching nothing — the
		// silence this whole rule exists to break, on all five walled tables
		// at once.
		`TABLE %S`,
		`TABLE $?`,
		`TABLE ,`,
		`TABLE`,
		`WITH X AS (TABLE $?) SELECT * FROM X`,
		// The SECOND position of a comma list is a table position too —
		// tableRef reads it for literals, and this rule did not check it for
		// markers. `FROM accounts a, `+t cross-joins every campaign.
		`SELECT X FROM ACCOUNTS A, %S N WHERE A.ORG_ID=$1`,
		`SELECT X FROM ACCOUNTS , %S`,
		`SELECT X FROM ACCOUNTS A, $? N`,
		`SELECT X FROM ACCOUNTS A,`,
		`DELETE FROM T USING ACCOUNTS A, %S`,
		`SELECT X FROM PUBLIC.ACCOUNTS AS A, TEAMS G, %S`,
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
		// TABLE followed by a name IS readable, and the DDL spellings that
		// merely contain the word are not table positions at all. Listing
		// TABLE among the unreadable positions must not turn the schema this
		// application creates at startup into a finding.
		`TABLE ACCOUNTS`,
		`CREATE TABLE IF NOT EXISTS LOGIN_TOKENS $SUB0`,
		`ALTER TABLE ONLY PUBLIC.ACCOUNTS ADD COLUMN X INT`,
		`DROP TABLE IF EXISTS ACCOUNTS`,
		`REINDEX TABLE ACCOUNTS`,
		`GRANT SELECT ON TABLE ACCOUNTS TO SOMEONE`,
		`ALTER DEFAULT PRIVILEGES IN SCHEMA PUBLIC GRANT SELECT ON TABLES TO PUBLIC`,
		// A comma that is NOT in a table list. The rule walks the FROM list
		// one reference at a time for exactly this reason: a wildcard between
		// FROM and the comma would swallow every dynamic column in the query
		// and refuse ordinary SQL — which sends the next author around the
		// canary, and costs what a hole costs.
		`SELECT ID, %S FROM T WHERE ORG_ID=$1`,
		`SELECT X FROM ACCOUNTS ORDER BY NAME, %S`,
		`SELECT X FROM ACCOUNTS A GROUP BY A.NAME, %S`,
		`SELECT X FROM ACCOUNTS WHERE ORG_ID=$1 ORDER BY A, B, %S LIMIT %S`,
		`SELECT COALESCE(A, %S) FROM ACCOUNTS WHERE ORG_ID=$1`,
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

// R11. `query{}` and `&query{}` are composite literals. `new(query)` is a
// CALL, and the constructor guard read only the first shape — so a builder
// could be made that inherits nothing from scoped(r), and its first bound
// value becomes $1. `WHERE org_id=$1` then filters on a team identifier
// with every guard green: the same break scoped(r) exists to prevent.
func TestNewQueryIsAQueryBuilderToo(t *testing.T) {
	const src = `package main

func bypass(r *http.Request) *query {
	req := new(query)
	req.p(r.URL.Query().Get("team"))
	return req
}

func alsoBypass() *query { return &query{} }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "builder.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	found := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			if buildsAQuery(n) {
				found[fn.Name.Name] = true
			}
			return true
		})
	}
	if !found["bypass"] {
		t.Error("`new(query)` builds a query the constructor guard cannot see: " +
			"$1 becomes whatever the caller binds first")
	}
	if !found["alsoBypass"] {
		t.Error("the composite-literal shape is no longer recognised")
	}
}

// R11. CopyFrom takes its table as a pgx.Identifier, so no argument of it is
// ever SQL: `sqlStatements` summed the text of the arguments and found
// nothing, and the call was not in queryCalls either. An entire pgx API
// streamed rows into any table with no statement for any rule to read.
func TestCopyFromIsAQueryCall(t *testing.T) {
	if !queryCalls["CopyFrom"] {
		t.Fatal("CopyFrom is not a query call: a write that names its table " +
			"as an identifier reaches every table unseen")
	}
	// and it can never be READ, so a site using it has to be declared
	const src = `package main

func stream(ctx context.Context, tx pgx.Tx, rows [][]any) error {
	_, err := tx.CopyFrom(ctx, pgx.Identifier{"accounts"},
		[]string{"org_id", "email"}, pgx.CopyFromRows(rows))
	return err
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "copy.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == "stream" {
			fn = f
		}
	}
	if fn == nil {
		t.Fatal("the fixture has no stream")
	}
	scoped := localScope(map[string]string{}, fn)
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "CopyFrom" {
			return true
		}
		for _, arg := range call.Args {
			if txt := sqlText(arg, scoped); txt != "" &&
				sqlVerb.MatchString(strings.ToUpper(txt)) {
				t.Errorf("an argument of CopyFrom read as SQL (%q): the site "+
					"would count as readable and escape declaration", txt)
			}
		}
		return true
	})
}

// R11. A METHOD and a FREE FUNCTION are different AST nodes. The invisible
// -query test read only selectors, so a package helper called by its bare
// name carried SQL nobody resolved: invisible to that test, and invisible to
// the walls canary too, which needs readable text before it has a rule to
// apply. Both spellings must reach the same check.
func TestAFreeFunctionCallIsAQueryCallToo(t *testing.T) {
	const src = `package main

func viaMethod(ctx context.Context, tx pgx.Tx) { tx.Query(ctx, "SELECT 1") }
func viaFreeFunction(ctx context.Context, tx pgx.Tx) { textColumn(ctx, tx, "SELECT 1") }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "calls.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	seen := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if queryCalls[calledName(call)] {
				seen[fn.Name.Name] = true
			}
			return true
		})
	}
	if !seen["viaMethod"] {
		t.Error("a method call is no longer recognised as a query call")
	}
	if !seen["viaFreeFunction"] {
		t.Error("a query helper called by its bare name is invisible: its SQL " +
			"is never read, so no rule ever runs on it")
	}
}

// R12. `[]query{{…}}` and `map[string]query{…}` ELIDE the element type on the
// inner literal — its Type is nil — so a guard reading only `query{…}` saw
// neither, and a builder made that way inherits nothing from scoped(r).
func TestAQueryBuiltInACollectionIsStillABuilder(t *testing.T) {
	const src = `package main

func viaSlice() []query   { return []query{{}} }
func viaMap() map[string]query { return map[string]query{"a": {}} }
func viaArray() [1]query  { return [1]query{{}} }
func plain() *query       { return &query{} }
func notAQuery() []string { return []string{"x"} }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "collections.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	seen := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			if buildsAQuery(n) {
				seen[fn.Name.Name] = true
			}
			return true
		})
	}
	for _, name := range []string{"viaSlice", "viaMap", "viaArray", "plain"} {
		if !seen[name] {
			t.Errorf("%s builds a query the constructor guard cannot see: $1 "+
				"becomes whatever the caller binds first", name)
		}
	}
	if seen["notAQuery"] {
		t.Error("a []string reads as a query builder: the guard would refuse " +
			"code that binds nothing")
	}
}

// R13. A query built across an if/else is not ONE query. Read sequentially,
// the two branches concatenate, the text carries `ORG_ID=$1` from the first,
// and the reference counts as bounded — while the driver runs one of the two,
// and the second is the very query the canary refuses when written alone.
// Each branch is now resolved with the others removed, and every variant is
// judged.
func TestEachBranchOfAQueryIsJudgedOnItsOwn(t *testing.T) {
	const src = `package main

func handler(mode int) {
	sql := "SELECT email FROM notes"
	if mode == 1 {
		sql += " WHERE org_id=$1"
	} else {
		sql += " WHERE parent_id=$1"
	}
	run(sql)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "branch.go", src, 0)
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
	variants := localScopeVariants(map[string]string{}, fn)
	if len(variants) < 2 {
		t.Fatalf("%d variant(s): the branches are not enumerated", len(variants))
	}
	unwalled := false
	for _, v := range variants {
		up := strings.ToUpper(v["sql"])
		if strings.Contains(up, "PARENT_ID=$1") &&
			!orgPredicate.MatchString(normaliseSQL(v["sql"])) {
			unwalled = true
		}
	}
	if !unwalled {
		t.Error("no variant carries the unwalled branch on its own: the " +
			"canary reads the two concatenated and calls it bounded, while " +
			"the driver runs one of them")
	}
}

// R14. A branching that may take NO branch is the one that matters most: the
// wall then exists only INSIDE the branch, and the base alone is what the
// driver runs the rest of the time. `if` without `else` and `switch` without
// `default` were not enumerated at all — one assigning branch was not
// considered a fork — and `select` was not a branching statement to begin
// with. All four spellings are enumerated here, and the enumeration is what
// the guard uses, so removing a case turns this red.
func TestABranchingThatMayTakeNoBranchIsEnumerated(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"if sans else", `package main
func handler(restricted bool) {
	sql := "SELECT email FROM accounts"
	if restricted { sql += " WHERE org_id=$1" }
	run(sql)
}`},
		{"switch sans default", `package main
func handler(mode string) {
	sql := "SELECT email FROM accounts"
	switch mode {
	case "restricted":
		sql += " WHERE org_id=$1"
	}
	run(sql)
}`},
		{"switch avec default", `package main
func handler(mode string) {
	sql := "SELECT email FROM accounts"
	switch mode {
	case "a":
		sql += " WHERE org_id=$1"
	default:
		sql += " WHERE team_id=$1"
	}
	run(sql)
}`},
		{"type switch", `package main
func handler(v any) {
	sql := "SELECT email FROM accounts"
	switch v.(type) {
	case string:
		sql += " WHERE org_id=$1"
	case int:
		sql += " WHERE team_id=$1"
	}
	run(sql)
}`},
		{"select", `package main
func handler(a, b chan int) {
	sql := "SELECT email FROM accounts"
	select {
	case <-a:
		sql += " WHERE org_id=$1"
	case <-b:
		sql += " WHERE team_id=$1"
	}
	run(sql)
}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "b.go", c.src, 0)
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
			variants := localScopeVariants(map[string]string{}, fn)
			if len(variants) < 2 {
				t.Fatalf("%d variant(s): this branching is not enumerated, so "+
					"its branches are read concatenated and one of them carries "+
					"a campaign predicate for all the others", len(variants))
			}
			// at least one variant must be the reading with NO campaign
			// predicate — the one the driver runs
			bare := false
			for _, v := range variants {
				if !orgPredicate.MatchString(normaliseSQL(v["sql"])) {
					bare = true
				}
			}
			if !bare {
				t.Error("every variant carries the wall: the branch that does " +
					"not add it is never read on its own")
			}
		})
	}
}

// R13. Containers NEST. `[][]query{{{…}}}` and `map[K][]query{…}` put another
// container where the element type goes, so matching the immediate element
// caught the one-level shapes and missed every deeper one.
func TestANestedCollectionOfQueriesIsStillABuilder(t *testing.T) {
	const src = `package main

func nested() [][]query        { return [][]query{{{}}} }
func mapOfSlices() map[string][]query { return map[string][]query{"a": {{}}} }
func pointers() []*query       { return []*query{{}} }
func notAQuery() [][]string    { return [][]string{{"x"}} }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "nested.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	seen := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			if buildsAQuery(n) {
				seen[fn.Name.Name] = true
			}
			return true
		})
	}
	for _, name := range []string{"nested", "mapOfSlices", "pointers"} {
		if !seen[name] {
			t.Errorf("%s builds queries the constructor guard cannot see", name)
		}
	}
	if seen["notAQuery"] {
		t.Error("a [][]string reads as a query builder")
	}
}

// R13. What a startup body CALLS runs at startup too: `init() { helper() }`
// and `var _ = fn()` reach a reassignment through one hop, and reading only
// the bodies themselves missed all of them.
func TestAReassignmentOneHopFromStartupIsStillLive(t *testing.T) {
	const src = `package main

var viaHelper = "SELECT email FROM accounts WHERE org_id=$1"
var viaInitialiser = "SELECT id FROM teams WHERE org_id=$1"

func helper() { viaHelper = "SELECT email FROM accounts" }
func initialise() bool { viaInitialiser = "SELECT id FROM teams"; return true }

func init() { helper() }

var _ = initialise()

func handler() { run(viaHelper); run(viaInitialiser) }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "hop.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	known := stringValues(map[string]*ast.File{"hop.go": file})
	for _, name := range []string{"viaHelper", "viaInitialiser"} {
		if got, present := known[name]; present {
			t.Errorf("the canary reads %q for %s, which a function called at "+
				"startup reassigns: the declaration is not what runs", got, name)
		}
	}
}

// R13. A `+=` on a PACKAGE binding must start from what the package says.
// Reset to nothing, the canary read the appended fragment alone — no SQL
// verb, so the statement was dropped and the disjunction in it never judged.
func TestAnAppendOnAPackageBindingKeepsItsBase(t *testing.T) {
	const src = `package main

func handler() {
	pkgSQL += " OR TRUE"
	run(pkgSQL)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "pkgappend.go", src, 0)
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
	base := "SELECT email FROM notes WHERE org_id=$1"
	got := localScope(map[string]string{"pkgSQL": base}, fn)["pkgSQL"]
	if !strings.Contains(strings.ToUpper(got), "FROM NOTES") {
		t.Errorf("the base declared by the package is lost, so the statement "+
			"carries no table and no rule ever runs on it:\n\tread: %q", got)
	}
	if !strings.Contains(strings.ToUpper(got), "OR TRUE") {
		t.Errorf("the appended text is lost: %q", got)
	}
}

// R12. `init()` is not the only thing that runs before the first request. A
// package-level `var _ = func() bool { … }()` initialiser runs BEFORE init,
// and reassigns just as effectively — the canary read the declaration and
// the driver ran the other string.
func TestAStartupInitialiserCanNotDecideEither(t *testing.T) {
	const src = `package main

var poisoned = "SELECT email FROM accounts WHERE org_id=$1"

// runs at package initialisation, before init()
var _ = func() bool { poisoned = "SELECT email FROM accounts"; return true }()

func handler() { run(poisoned) }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "startup.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	if got, present := stringValues(map[string]*ast.File{
		"startup.go": file,
	})["poisoned"]; present {
		t.Errorf("the canary reads %q for a binding a package initialiser "+
			"reassigns at startup: the declaration is not what runs", got)
	}
}

// R12. The bound on `+=` has to be per STATEMENT. Per NAME, the second append
// on one variable was dropped; with no bound at all, a `+=` whose `:=` is not
// replayed every pass — `var sql string`, `sql, err := build()`, a parameter,
// a struct field — accumulated once per pass, and the canary refused a
// legitimate query on text nobody wrote.
func TestAnAppendWithoutADeclarationIsNotTripled(t *testing.T) {
	const src = `package main

func handler() {
	var sql string
	sql += "SELECT id FROM notes WHERE org_id=$1"
	run(sql)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "novar.go", src, 0)
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
	if n := strings.Count(strings.ToUpper(got), "SELECT"); n != 1 {
		t.Errorf("the canary reads the statement %d times over: it judges a "+
			"query nobody wrote, and refuses a legitimate one\n\tread: %q",
			n, got)
	}
}

// R11. The counterpart of the dead-code shape, and its opposite conclusion.
// A package SQL binding reassigned in `init()` IS what the driver runs: init
// executes at startup, before the first request. stringValues read the
// DECLARATION, so a query could be declared with a campaign predicate and
// shipped without one, with every guard green.
//
// The name is dropped rather than relearned — deciding which of the two
// texts wins is exactly what reading cannot do — and the call site then
// falls to TestNoQueryIsInvisibleToTheCanary, which names it.
func TestInitCanNotDecideWhatTheCanaryReads(t *testing.T) {
	const src = `package main

// what the declaration says
var poisoned = "SELECT email FROM accounts WHERE org_id=$1"

// …and what actually runs, from the first request onwards
func init() { poisoned = "SELECT email FROM accounts" }

func handler() { run(poisoned) }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "init.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	got, present := stringValues(map[string]*ast.File{"init.go": file})["poisoned"]
	if present {
		t.Errorf("the canary reads %q for a binding init() reassigns: the "+
			"declaration is not what the driver runs, and reading it credits "+
			"the query with a predicate production never carried", got)
	}
	// the dead-code shape still resolves: only init is live
	const dead = `package main

var alsoPoisoned = "SELECT email FROM accounts"

func neverCalled() { alsoPoisoned = "SELECT email FROM accounts WHERE org_id=$1" }

func handler2() { run(alsoPoisoned) }
`
	deadFile, err := parser.ParseFile(fset, "dead.go", dead, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	if got := stringValues(map[string]*ast.File{
		"dead.go": deadFile,
	})["alsoPoisoned"]; got != "SELECT email FROM accounts" {
		t.Errorf("dead code now decides what the canary reads, or the binding "+
			"stopped being read at all: %q", got)
	}
}

// R11. TWO `+=` on the same variable. The map bounding the appending was
// keyed by NAME, not by statement, so the first was applied and every one
// after it dropped in silence — while ast.Inspect already visits each node
// exactly once per pass, which is all the bounding that was ever needed.
// The canary read `base + a`; the driver ran `base + a + b`. An always-true
// disjunction written in the second one therefore never reached
// `neutralised`, the rule that exists to refuse it.
func TestEveryAppendReachesTheCanary(t *testing.T) {
	const src = `package main

func handler() {
	sql := "SELECT email FROM accounts WHERE org_id=$1"
	sql += " AND role='volunteer'"
	sql += " OR TRUE"
	run(sql)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "appends.go", src, 0)
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
	got := strings.ToUpper(localScope(map[string]string{}, fn)["sql"])
	if !strings.Contains(got, "ROLE=") && !strings.Contains(got, "ROLE ") {
		t.Errorf("the first append is gone: %q", got)
	}
	if !strings.Contains(got, "OR TRUE") {
		t.Errorf("every append after the first is dropped, so the canary reads "+
			"a walled query the driver never runs:\n\tread: %q\n\truns: %s",
			got, "…WHERE ORG_ID=$1 AND ROLE='VOLUNTEER' OR TRUE")
	}
	// …and the text is not counted twice by the resolution passes
	if strings.Count(got, "OR TRUE") != 1 {
		t.Errorf("the appended text is applied %d times: the canary reads a "+
			"query nobody wrote (%q)", strings.Count(got, "OR TRUE"), got)
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

// R15. Every form that WRITES an address must judge it with the same
// predicate. `strings.Contains(email, "@")` passes an address carrying a CR
// or an LF in its middle — normalizeEmail only trims the edges — and that
// address becomes the primary key of `accounts`. The person who typed the
// clean form of it can never sign in; safeAddress refuses it at send time,
// so no invitation and no link ever arrives; and the coordination approving
// the moderation screen reads one identity and accepts another.
//
// Three of the four public forms used storableEmail and the fourth did not:
// the helper came from one branch, the form from another, both landed in one
// merge and nothing wired them together. No conflict marker, no failing
// test, no diff to read — which is why the rule is checked here rather than
// remembered.
func TestEveryFormJudgesAnAddressTheSameWay(t *testing.T) {
	files := apiPackage(t)
	var loose []string
	seen := 0
	for name, file := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// normalizing an address is READING it — signing in, looking an
			// account up. What this rule is about is the address that gets
			// WRITTEN: an INSERT in the same handler is what makes it a key
			// nobody can correct afterwards.
			normalises, inserts, strict := false, false, false
			ast.Inspect(fn, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok &&
					strings.Contains(strings.ToUpper(lit.Value), "INSERT INTO") {
					inserts = true
				}
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch calledName(call) {
				case "normalizeEmail":
					normalises = true
				case "storableEmail":
					strict = true
				}
				return true
			})
			writesAddress := normalises && inserts
			if writesAddress && !strict {
				loose = append(loose, name+":"+fn.Name.Name)
			}
			if strict {
				seen++
			}
		}
	}
	if seen == 0 {
		t.Fatal("no handler checks an address with storableEmail: this guard " +
			"is reading nothing")
	}
	if len(loose) > 0 {
		t.Errorf("these read an address and do not judge it with "+
			"storableEmail, so a CR or an LF in the middle reaches the "+
			"column it becomes the key of:\n\t%s", strings.Join(loose, "\n\t"))
	}
}
