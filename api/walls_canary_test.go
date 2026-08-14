package main

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"regexp"
	"strings"
	"testing"
)

// The lint half of the campaign wall: every query naming a walled table must
// also name the campaign.
//
// Its first version asked whether the string "org_id" appeared anywhere in
// the statement. An adversarial pass wrote twelve queries that satisfied it
// while leaking — the word in a SELECT list, in a comment, next to `OR TRUE`,
// a table written `public.accounts` or `"accounts"`, a `DELETE … USING`, and
// SQL the canary could not read at all and passed over IN SILENCE. Each is
// closed below, and each has its case in canaryCases.
//
// What it still cannot see: whether a predicate in a SUBQUERY constrains the
// outer statement. That needs a real SQL parser; TestWallsHoldWithoutRLS is
// what covers it, by running the application with RLS switched off.

// The list comes from multiorg.go, not from a copy: a table put under RLS
// there is covered here the same day, without anyone remembering to.
func walledTablesUpper() []string {
	out := make([]string, 0, len(walledTables))
	for _, t := range walledTables {
		out = append(out, strings.ToUpper(t))
	}
	return out
}

var (
	sqlLineComment  = regexp.MustCompile(`--[^\n]*`)
	sqlBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	sqlStringLit    = regexp.MustCompile(`'[^']*'`)
	sqlSpaces       = regexp.MustCompile(`\s+`)
	// A predicate, not a mention. The right-hand side is deliberately not
	// pinned: it is `$1`, or a `%[1]s` placeholder in a format string, or
	// another table's org_id in a join, or an expression the resolver could
	// not read. What matters is the EQUALITY — `SELECT org_id, email FROM
	// accounts` passed the version that only looked for the word.
	orgPredicate = regexp.MustCompile(`([A-Z][A-Z0-9_]*\.)?ORG_ID\s*=`)
	// Predicates that are always true, whatever else the statement says.
	neutralised = regexp.MustCompile(`\bOR\s+(TRUE|1\s*=\s*1)\b`)
	// A statement whose TABLE is a format verb: which table it reads cannot
	// be known from the source, so neither can whether it names the campaign.
	dynamicTable = regexp.MustCompile(`(?:FROM|JOIN|INTO|USING|UPDATE)\s+%`)
	// What makes a resolved string a statement rather than a value. Session
	// commands count: `SET lock_timeout` reads no table, but it IS the
	// statement of its call, and treating it as a value made three innocent
	// sites look invisible.
	sqlVerb = regexp.MustCompile(
		`\b(SELECT|INSERT|UPDATE|DELETE|WITH|SET|COPY|LOCK|CREATE|ALTER|DROP|GRANT|TRUNCATE)\b`)
)

// normaliseSQL: what the canary reads. Comments and string literals are
// REMOVED — "org_id" inside either is not a filter, and both were used to
// walk past the first version.
func normaliseSQL(sql string) string {
	sql = sqlBlockComment.ReplaceAllString(sql, " ")
	sql = sqlLineComment.ReplaceAllString(sql, " ")
	sql = sqlStringLit.ReplaceAllString(sql, "''")
	return sqlSpaces.ReplaceAllString(strings.ToUpper(sql), " ")
}

// tableRef matches a walled table however it is written: schema-qualified,
// quoted, aliased with or without AS, and after USING as well as FROM/JOIN.
func tableRef(table string) *regexp.Regexp {
	return regexp.MustCompile(
		`(?:FROM|JOIN|INTO|USING|UPDATE|,)\s+(?:[A-Z_]+\.)?"?` + table +
			`"?(?:\s+(?:AS\s+)?([A-Z][A-Z0-9_]*))?`)
}

// Words that follow a table name without being an alias.
var notAnAlias = map[string]bool{
	"ON": true, "WHERE": true, "SET": true, "VALUES": true, "GROUP": true,
	"ORDER": true, "LIMIT": true, "RETURNING": true, "LEFT": true,
	"RIGHT": true, "INNER": true, "OUTER": true, "FULL": true, "CROSS": true,
	"JOIN": true, "AND": true, "OR": true, "SELECT": true, "FROM": true,
	"USING": true, "HAVING": true, "UNION": true, "EXCEPT": true,
	"INTERSECT": true, "ON CONFLICT": true, "WITH": true, "AS": true,
}

// Statements allowed to cross campaigns, keyed by the SHA-256 of their
// NORMALISED text — not by the function they live in. Keying on the function
// meant a second query written into it inherited the exemption in silence,
// which is exactly what the old comment promised would not happen.
var crossesCampaigns = map[string]string{
	// db.go:removeStale — the mayors row is SHARED. Deleting a target that
	// left the list must not strip another campaign of its history, so
	// "already worked on" is asked across every campaign, from the
	// maintenance scope, the only place that can.
	"7cbd5bd6f79d9c4b": "removeStale: the shared mayor list spans every campaign",
}

// Query calls whose SQL the canary must be able to read. A call it cannot
// read is not a pass — see TestNoQueryIsInvisibleToTheCanary.
var queryCalls = map[string]bool{
	"Query": true, "QueryRow": true, "Exec": true,
	"rows": true, "column": true, "counters": true, "orderedCounters": true,
	"textColumn": true,
}

type statement struct{ File, Func, SQL string }

// sqlStatements walks the package and yields every SQL string reaching a
// call, with the file and enclosing function it was written in.
func sqlStatements(t *testing.T) []statement {
	t.Helper()
	files := apiPackage(t)
	values := stringValues(files)
	var out []statement
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
			// one lent its value to another and produced a query nobody had
			// written.
			scoped := map[string]string{}
			for k, v := range values {
				scoped[k] = v
			}
			// A parameter named like some other function's local is NOT that
			// local. `sql` is a parameter of rows/column/counters and a local
			// of routeBatch: without this, every helper was reported carrying
			// the batch INSERT, which it never sees as text.
			if fn.Type.Params != nil {
				for _, field := range fn.Type.Params.List {
					for _, id := range field.Names {
						delete(scoped, id.Name)
					}
				}
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				if a, ok := n.(*ast.AssignStmt); ok &&
					len(a.Lhs) == 1 && len(a.Rhs) == 1 {
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
				return true
			})
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				for _, arg := range call.Args {
					if sql := sqlText(arg, scoped); sql != "" {
						out = append(out, statement{name, fn.Name.Name,
							normaliseSQL(sql)})
					}
				}
				return true
			})
		}
	}
	return out
}

// insertNaming: `INSERT INTO <table>(ORG_ID, …` — the row being written is
// stamped with the campaign, which is how a write names it.
func insertNaming(table string) *regexp.Regexp {
	return regexp.MustCompile(`INSERT INTO\s+(?:[A-Z_]+\.)?"?` + table +
		`"?\s*\(\s*ORG_ID\b`)
}

func fingerprint(sql string) string {
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:])[:16]
}

func TestEveryQueryOnAWalledTableNamesTheCampaign(t *testing.T) {
	statements := sqlStatements(t)
	if len(statements) < 20 {
		t.Fatalf("only %d SQL statements found: the canary is not reading the "+
			"package, and would pass over anything", len(statements))
	}

	tables := walledTablesUpper()
	refs := map[string]*regexp.Regexp{}
	for _, table := range tables {
		refs[table] = tableRef(table)
	}

	used := map[string]bool{}
	for _, st := range statements {
		for _, table := range tables {
			m := refs[table].FindStringSubmatch(st.SQL)
			if m == nil {
				continue
			}
			print := fingerprint(st.SQL)
			if _, allowed := crossesCampaigns[print]; allowed {
				used[print] = true
				continue
			}
			where := st.File + ":" + st.Func
			if neutralised.MatchString(st.SQL) {
				t.Errorf("%s: this query on %s carries an always-true "+
					"disjunction, which cancels whatever else it says:\n\t%s",
					where, table, st.SQL)
				continue
			}
			want := "ORG_ID"
			if alias := m[1]; alias != "" && !notAnAlias[alias] {
				want = alias + ".ORG_ID"
			}
			found := false
			for _, p := range orgPredicate.FindAllString(st.SQL, -1) {
				if strings.HasPrefix(p, want) {
					found = true
					break
				}
			}
			// An INSERT names the campaign by writing it: the column list
			// carries org_id and the value is bound.
			if !found && insertNaming(table).MatchString(st.SQL) {
				found = true
			}
			if !found {
				t.Errorf("%s: this query touches %s and never FILTERS on the "+
					"campaign (looked for %s = $n). A privileged database role "+
					"would serve one campaign's work to another. If the "+
					"crossing is deliberate, add %q to crossesCampaigns:\n\t%s",
					where, table, want, print, st.SQL)
			}
		}
	}

	// An exemption that no longer covers anything is a stale claim, and the
	// next query written where it used to apply would inherit it.
	for print, why := range crossesCampaigns {
		if !used[print] {
			t.Errorf("the exemption %q (%s) matches no statement any more — "+
				"drop it", print, why)
		}
	}
}

// The canary reads SQL out of the syntax tree. Written in a shape it cannot
// resolve — a helper's return value, a %s placeholder holding the table name,
// a range over a slice of statements — a query became INVISIBLE to it, and
// invisible read as compliant. Silence is now a failure of its own.
func TestNoQueryIsInvisibleToTheCanary(t *testing.T) {
	// Sites whose SQL cannot be resolved statically and that are known not to
	// touch a walled table. Each is a promise, and an unused one is an error.
	allowed := map[string]bool{
		// DDL built by iterating over walledTables itself: the statements
		// CREATE the policies rather than querying through them.
		"multiorg.go:orgSchema": true,
		"multiorg.go:enableRLS": true,
		"db.go:schema":          true,
	}

	files := apiPackage(t)
	values := stringValues(files)
	seen := map[string]bool{}
	var invisible []string
	for name, file := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
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
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !queryCalls[sel.Sel.Name] || len(call.Args) < 2 {
					return true
				}
				// Only the STATEMENT argument counts. Accepting any readable
				// argument meant `tx.Exec(ctx, q, "x")` passed for read
				// because the value "x" resolved — while `q`, the query,
				// stayed invisible. The statement sits at index 1, or at 2
				// for the helpers that take (ctx, tx, sql, …).
				readable := false
				for _, i := range []int{1, 2} {
					if i >= len(call.Args) {
						continue
					}
					txt := sqlText(call.Args[i], values)
					if txt == "" || !sqlVerb.MatchString(strings.ToUpper(txt)) {
						continue
					}
					if dynamicTable.MatchString(normaliseSQL(txt)) {
						continue
					}
					readable = true
				}
				where := name + ":" + fn.Name.Name
				if readable {
					return true
				}
				seen[where] = true
				if !allowed[where] {
					invisible = append(invisible, where)
				}
				return true
			})
		}
	}
	if len(invisible) > 0 {
		t.Errorf("the canary cannot read the SQL of these calls, so it cannot "+
			"tell whether they name the campaign — write the query where it "+
			"can be read, or declare the site:\n\t%s",
			strings.Join(invisible, "\n\t"))
	}
	for where := range allowed {
		if !seen[where] {
			t.Errorf("%s is declared unreadable but every query in it is "+
				"readable now — drop the declaration", where)
		}
	}
}
