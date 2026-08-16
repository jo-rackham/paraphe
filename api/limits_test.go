package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Canaries over the SOURCE, not over a running server.
//
// A test that exercises today's routes says nothing about the route someone
// adds next month, and a defect fixed only where it is noticed
// comes back somewhere else. These read the code and refuse
// the shape itself.
//
// They have now been broken twice, and each break taught the same thing:
// a canary keyed on how code LOOKS is bypassed by writing it differently.
// A regular expression missed a `FROM` sitting in a constant; reading
// constants missed one sitting in a local variable; a naming convention
// ("…Runes") missed the ceiling someone called "…Chars". So they key on
// what the code DOES: an expression compared against a text length is a
// ceiling whatever it is named, and a query is whatever text reaches the
// driver, assembled from wherever.
//
// A canary that certifies without seeing is worse than none.

// ceilings accounted for in the arithmetic of
// TestTheBodyCeilingHoldsBothEdges. A ceiling missing from that sum makes
// maxBodySize silently too small for a request the application invites.
//
// TEXT ceilings only, counted in runes. `maxLogoBytes` is deliberately
// absent: it bounds an image with len(), in bytes, and listing it here
// would make the rule below — "a rune ceiling applied to bytes refuses
// fewer characters than its message promises" — fire on the one place
// where bytes are the honest unit. Its own arithmetic sits beside this
// one, in TestTheBodyCeilingHoldsBothEdges.
var accountedCeilings = map[string]bool{
	"maxNoteRunes": true, "maxCampaignRunes": true,
	"maxNameRunes": true, "maxEmailRunes": true,
}

func apiPackage(t *testing.T) map[string]*ast.File {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(f os.FileInfo) bool {
		return !strings.HasSuffix(f.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, ok := pkgs["main"]
	if !ok {
		t.Fatal("package main not found: the canary is reading the wrong " +
			"directory and would pass whatever the code says")
	}
	files := map[string]*ast.File{}
	for path, file := range pkg.Files {
		files[filepath.Base(path)] = file
	}
	if len(files) < 5 {
		t.Fatalf("only %d source files parsed: the canary would pass whatever "+
			"the code says", len(files))
	}
	return files
}

// stringValues: every identifier bound to a string at PACKAGE level —
// constants and variables, declared at the top of a file and nowhere else.
//
// It used to walk each file whole, learning from every assignment it met,
// including those inside function bodies. Two consequences, and both were
// holes rather than imprecision:
//
//   - It kept the LAST value it saw for a name, wherever that was. A package
//     binding holding unbounded SQL, plus a function NEVER CALLED reassigning
//     it to a bounded form, made the canary read the bounded one while the
//     driver ran the other.
//   - The map is keyed on the name alone, so a `var` local to a dead function
//     overwrote the package binding of the same name.
//
// What is written inside a function belongs to that function: localScope
// learns it there, from the function's own body, where a dead sibling cannot
// reach it.
func stringValues(files map[string]*ast.File) map[string]string {
	known := map[string]string{}
	learn := func(names []*ast.Ident, values []ast.Expr) {
		if len(names) != len(values) {
			return
		}
		for i, name := range names {
			// best effort, like the queries themselves: a binding whose
			// value is `"SELECT … " + filter + " …"` is only PARTLY
			// readable, and requiring it whole is what let the query be
			// hidden in a local variable — the exact break this collection
			// was added to close.
			if text := sqlText(values[i], known); text != "" {
				known[name.Name] = text
			}
		}
	}
	// several passes: one binding may be built from another
	for range 3 {
		for _, file := range files {
			// file.Decls and not ast.Inspect: a `var` inside a function body
			// is a GenDecl too, and Inspect reaches it. Only what is declared
			// at the top of a file is visible to the whole package.
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
					continue
				}
				for _, spec := range gen.Specs {
					if value, ok := spec.(*ast.ValueSpec); ok {
						learn(value.Names, value.Values)
					}
				}
			}
		}
		// …and a FUNCTION whose whole body is `return <string>` is a binding
		// too, under its own name.
		//
		// Without this, `assignmentJoin("$1")` read as `"$1"` — the argument
		// and nothing else. That call carries the most-used join in the
		// package, `FROM mayors m LEFT JOIN assignments t ON … AND t.org_id
		// = …`, and the canary saw a statement with no FROM clause at all:
		// no walled table, no rule, silence. Deleting the org_id condition
		// from the join would have gone entirely unnoticed, and that
		// condition IS the wall for every screen that lists mayors.
		for _, file := range files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || len(fn.Body.List) != 1 ||
					fn.Recv != nil {
					continue
				}
				ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
				if !ok || len(ret.Results) != 1 {
					continue
				}
				// The body is resolved with each PARAMETER standing for
				// itself, `$ARG1` for the first and so on, and the call site
				// puts the caller's argument back in its place.
				//
				// Resolved against the package map alone, a parameter is
				// unknown and contributes NOTHING: `return "… org_id=" + ph`
				// came out as `… ORG_ID=`, a predicate with an empty right
				// side, and the canary refused a helper written the most
				// natural way there is. A refusal costs what a hole costs —
				// it sends the next author around the guard.
				//
				// `$ARGn` is not a shape orgPredicate accepts on its right,
				// so a helper whose parameter is never substituted stays
				// refused rather than passing on a marker.
				scoped := map[string]string{}
				for k, v := range known {
					scoped[k] = v
				}
				position := 0
				if fn.Type.Params != nil {
					for _, field := range fn.Type.Params.List {
						for _, name := range field.Names {
							position++
							scoped[name.Name] = fmt.Sprintf("$ARG%d", position)
						}
					}
				}
				if text := sqlText(ret.Results[0], scoped); text != "" {
					known[fn.Name.Name] = text
				}
			}
		}
	}
	// A package binding REASSIGNED IN init() is not what its declaration
	// says. Dead code reassigning one changes nothing and must be ignored
	// — that is TestDeadCodeCannotDecideWhatTheCanaryReads — but init runs
	// at startup, before the first request, and its value is the one the
	// driver executes. Reading the declaration instead credited a query
	// with a campaign predicate that production never carried.
	//
	// The name is DROPPED rather than relearned: which of the two texts
	// wins is exactly what cannot be decided by reading, and a guess is
	// how a hole gets declared compliant. Unresolvable, the call site
	// becomes unreadable, and an unreadable site is a finding —
	// TestNoQueryIsInvisibleToTheCanary says so and names it.
	//
	// `init()` is not the only thing that runs before the first request: a
	// package-level `var x = func() … { … }()` initialiser runs BEFORE init,
	// and `main` runs after it. All three reach production; a reassignment in
	// any of them is what the driver executes.
	// Everything a startup body CALLS runs at startup too. `init() {
	// helper() }` and `var _ = fn()` reach a reassignment through one hop,
	// and reading only the bodies themselves missed all of them. The closure
	// is transitive and bounded by the package: a function is visited once.
	funcs := map[string]*ast.FuncDecl{}
	for _, file := range files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name != nil {
				// methods included: `init() { P{}.method() }` reaches one
				funcs[fn.Name.Name] = fn
			}
		}
	}
	var reached func(ast.Node, map[string]bool) []ast.Node
	reached = func(n ast.Node, seen map[string]bool) []ast.Node {
		out := []ast.Node{n}
		ast.Inspect(n, func(x ast.Node) bool {
			call, ok := x.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := calledName(call)
			if name == "" || seen[name] {
				return true
			}
			seen[name] = true
			if fn, ok := funcs[name]; ok && fn.Body != nil {
				out = append(out, reached(fn, seen)...)
			}
			return true
		})
		return out
	}
	startupBodies := func(file *ast.File) []ast.Node {
		var bodies []ast.Node
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && (d.Name.Name == "init" || d.Name.Name == "main") {
					bodies = append(bodies, reached(d, map[string]bool{})...)
				}
			case *ast.GenDecl:
				if d.Tok != token.VAR {
					continue
				}
				// the body of any function literal in a var initialiser
				for _, spec := range d.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					// the initialiser ITSELF, so `var _ = fn()` reaches fn
					for _, v := range value.Values {
						bodies = append(bodies, reached(v, map[string]bool{})...)
						ast.Inspect(v, func(n ast.Node) bool {
							if lit, ok := n.(*ast.FuncLit); ok {
								bodies = append(bodies, reached(lit, map[string]bool{})...)
							}
							return true
						})
					}
				}
			}
		}
		return bodies
	}
	for _, file := range files {
		for _, fn := range startupBodies(file) {
			ast.Inspect(fn, func(n ast.Node) bool {
				a, ok := n.(*ast.AssignStmt)
				if !ok || a.Tok == token.DEFINE {
					return true // `:=` declares a local, it shadows nothing
				}
				for _, lhs := range a.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						delete(known, id.Name)
					}
				}
				return true
			})
		}
	}
	return known
}

// sqlText reads a query expression BEST EFFORT: the parts it can resolve
// are concatenated, the parts it cannot (a WHERE clause built at run time)
// leave a gap. Giving up on the whole expression skips the card history's
// query entirely, since its filter is a variable — and that query is what
// this canary exists for.
//
// A LIMIT hidden inside such a gap is reported as missing. That is the
// right bias: a ceiling on an append-only table is worth writing where it
// can be read.
func sqlText(expr ast.Expr, values map[string]string) string {
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return ""
		}
		return sqlText(e.X, values) + sqlText(e.Y, values)
	case *ast.ParenExpr:
		return sqlText(e.X, values)
	case *ast.CallExpr:
		// fmt.Sprintf("… %s …", x): the literal parts still carry the shape
		var joined strings.Builder
		for _, arg := range e.Args {
			joined.WriteString(sqlText(arg, values))
		}
		return joined.String()
	}
	text, _ := resolveString(expr, values)
	return text
}

// resolveString reads an expression as the string it will be at run time,
// as far as known bindings allow.
func resolveString(expr ast.Expr, values map[string]string) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(e.Value)
		return text, err == nil
	case *ast.Ident:
		text, ok := values[e.Name]
		return text, ok
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, okLeft := resolveString(e.X, values)
		right, okRight := resolveString(e.Y, values)
		return left + right, okLeft && okRight
	case *ast.ParenExpr:
		return resolveString(e.X, values)
	}
	return "", false
}

// Every exemption below describes the WHOLE statement, anchored, and none of
// them is a substring test. A substring is satisfied by ADDING text: written
// as `strings.Contains(sql, "STATE='PENDING'")`, the pending exemption was
// granted to `WHERE state='pending' OR state='refused'` — refused rows are
// never deleted, so that is the unbounded response the guard exists to refuse
// — and to `WHERE NOT (state='pending')`, and to the same substring sitting
// in a COMMENT. Anchored, anything extra falls outside the shape and the
// LIMIT is required again, comments included: they break it CLOSED.
//
// oneRow: a query whose result cannot grow. An aggregate over the whole table
// returns exactly one row — refusing those blocked legitimate reads (the
// highest id since the last poll) for nothing. GROUP BY is what makes that
// false: `SELECT MAX(id), note FROM notes GROUP BY note` opens with an
// aggregate and returns one row per distinct note.
var oneRow = regexp.MustCompile(
	`^SELECT\s+(COUNT|MAX|MIN|SUM|AVG)\(`)

// pendingQueue: the ONE shape the insert's ceiling bounds — the pending set
// and nothing else. Anything ANDed, ORed or negated onto it reads rows the
// ceiling does not count. The state may be written as a literal or bound as
// a parameter; what matters is that it is the only predicate.
var pendingQueue = regexp.MustCompile(
	`\bWHERE\s+STATE\s*=\s*(?:'PENDING'|\$\d+)\s*` +
		`(?:ORDER\s+BY[A-Z0-9_,. ]*)?$`)

// singleRowByID: one row by primary key. Anchored for the same reason —
// `WHERE id=$1 OR ts > $2` carries the substring and reads the table — but
// the anchor has to leave room for what a single row is legitimately written
// with: a table alias, the campaign predicate that walls it, and any of
// PostgreSQL's locking clauses. Refusing `WHERE id=$1 AND org_id=$2` would
// refuse the most natural way to read one row of a WALLED table, and send
// its author to add a decoy LIMIT 1 or around the guard.
var singleRowByID = regexp.MustCompile(
	`\bWHERE\s+(?:[A-Z][A-Z0-9_]*\.)?ID\s*=\s*\$\d+` +
		`(?:\s+AND\s+(?:[A-Z][A-Z0-9_]*\.)?ORG_ID\s*=\s*\$\d+)?` +
		lockingClause + `\s*;?\s*$`)

// lockingClause: what PostgreSQL allows a query to end on. Declared once,
// because singleRowByID accepted it and limitClause did not — and an
// asymmetry between two shapes of one rule is what most of this project's
// criticals have been.
const lockingClause = `(?:\s+FOR\s+(?:UPDATE|SHARE|NO\s+KEY\s+UPDATE|` +
	`KEY\s+SHARE)(?:\s+SKIP\s+LOCKED|\s+NOWAIT)?)?\s*`

// existsOnly: the statement IS an EXISTS, it does not merely CONTAIN one.
// `SELECT EXISTS(…)` answers one boolean; `… WHERE EXISTS(…)` is a predicate
// on a read that returns whatever it matches, and a `'EXISTS('` in a selected
// value is not SQL at all. The last exemption left as a substring while its
// neighbours were being anchored for exactly this reason.
var existsOnly = regexp.MustCompile(`^SELECT\s+EXISTS\s*\(`)

// limitClause: a bound the OUTERMOST query carries. At the END of the text,
// where it binds the statement rather than a CTE inside it — `WITH b AS
// (SELECT id FROM notes LIMIT 1) SELECT * FROM notes` carries one and reads
// everything. `LIMIT ALL` and `LIMIT NULL` are not bounds either: PostgreSQL
// reads both as every row.
var limitClause = regexp.MustCompile(
	`\bLIMIT\s+(?:\d+|\$\d+)(?:\s+OFFSET\s+(?:\d+|\$\d+))?` +
		lockingClause + `;?\s*$` +
		`|\bFETCH\s+FIRST\s+(?:\d+|\$\d+)?\s*ROWS?\s+ONLY` +
		lockingClause + `;?\s*$`)

// sqlWithoutComments: what the server parses, minus what it ignores.
//
// The last link of the chain was a `strings.Contains(sql, "LIMIT")`, so
// `/* LIMIT 200 */` bounded a statement that read the whole table — the same
// lesson as the anchored exemptions, one line further down than they were.
//
// QUOTE-AWARE, because the two obvious orders are each wrong. Stripping
// comments first lets a `/*` inside a literal open one that closes at the
// next literal's `*/`, eating the real SQL between them: `WHERE
// slug='foo/*bar' … LIMIT 100` lost its bound and was refused. Stripping
// literals first — what normaliseSQL does, and for its own good reason —
// takes `'pending'` with them, and that is what the pending shape is
// recognised by. So this walks the text once and strips only what stands
// OUTSIDE a literal, which is what the server does too.
func sqlWithoutComments(sql string) string {
	var out strings.Builder
	for i := 0; i < len(sql); {
		switch {
		case sql[i] == '\'':
			j := i + 1
			for j < len(sql) {
				switch {
				case sql[j] == '\\' && j+1 < len(sql): // E'…\'…'
					j += 2
				case sql[j] == '\'' && j+1 < len(sql) && sql[j+1] == '\'':
					j += 2 // '' is one quote, not the end
				case sql[j] == '\'':
					j++
					goto closed
				default:
					j++
				}
			}
		closed:
			out.WriteString(sql[i:j])
			i = j
		case strings.HasPrefix(sql[i:], "--"):
			if n := strings.IndexByte(sql[i:], '\n'); n < 0 {
				i = len(sql)
			} else {
				i += n
			}
			out.WriteByte(' ')
		case strings.HasPrefix(sql[i:], "/*"):
			depth, j := 1, i+2
			for j < len(sql) && depth > 0 {
				switch {
				case strings.HasPrefix(sql[j:], "/*"):
					depth, j = depth+1, j+2
				case strings.HasPrefix(sql[j:], "*/"):
					depth, j = depth-1, j+2
				default:
					j++
				}
			}
			i = j
			out.WriteByte(' ')
		default:
			out.WriteByte(sql[i])
			i++
		}
	}
	return sqlSpaces.ReplaceAllString(out.String(), " ")
}

// aggregateOver: the same one row, read as a SUBQUERY rather than as the
// whole statement — `… WHERE (SELECT count(*) FROM t WHERE …) < $n`, which
// is how a ceiling is applied by the INSERT that it bounds. oneRow anchors
// at the start of the text and cannot see it. Counted reference by
// reference below, so a statement that reads the table BOTH ways is still
// judged on the read that can grow.
func aggregateOver(table string) *regexp.Regexp {
	return regexp.MustCompile(
		`SELECT\s+(?:COUNT|MAX|MIN|SUM|AVG)\([^()]*\)\s+FROM\s+` + table + `\b`)
}

// decodesAName: a body's `"name"` lands in this field.
//
// Read the way encoding/json reads it, not as a substring. `json:"name"` was
// looked for with its closing quote, so `json:"name,omitempty"` — the most
// ordinary spelling in Go — carried a name past a canary written to find one.
// And an untagged field decodes it too: the match on a field name is
// CASE-INSENSITIVE, so a body's `"name"` fills `Name` with nothing said.
//
// EXPORTED only, and that is not a detail: encoding/json fills nothing else,
// so `limitClass{name string}` — a settings record no request ever touches —
// read as a request type carrying a name the moment the untagged case was
// added, and dragged a handler that never decodes one into the canary.
func decodesAName(f *ast.Field) bool {
	byFieldName := func() bool {
		for _, id := range f.Names {
			if id.IsExported() && strings.EqualFold(id.Name, "name") {
				return true
			}
		}
		return false
	}
	if f.Tag == nil {
		return byFieldName()
	}
	tag, err := strconv.Unquote(f.Tag.Value)
	if err != nil {
		return false
	}
	spelt, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
	switch spelt {
	case "-":
		return false
	case "":
		// tagged for something else, or `json:",omitempty"`: the field name
		// is what decodes, case-insensitively
		return byFieldName()
	}
	return byFieldName() || (isExported(f) && strings.EqualFold(spelt, "name"))
}

// embedIgnored: `json:"-"` on an embedded field, which encoding/json obeys —
// the type embedded there promotes nothing, so a name inside it decodes
// nothing and demanding a ceiling for it refuses a handler that needs none.
func embedIgnored(f *ast.Field) bool {
	if f.Tag == nil {
		return false
	}
	tag, err := strconv.Unquote(f.Tag.Value)
	if err != nil {
		return false
	}
	spelt, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
	return spelt == "-"
}

// isExported: a tag renames a field for the decoder, it does not export it.
func isExported(f *ast.Field) bool {
	for _, id := range f.Names {
		if id.IsExported() {
			return true
		}
	}
	return false
}

// embeddedIdent: the type name an anonymous struct field embeds — `Inner`,
// `*Inner`, and the qualified forms, whose fields encoding/json promotes into
// the outer type just the same.
func embeddedIdent(e ast.Expr) (string, bool) {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name, true
	case *ast.StarExpr:
		return embeddedIdent(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name, true
	}
	return "", false
}

// readsTable: this statement names the table where rows come FROM. A comma
// counts, because a second table hides behind one.
func readsTable(sql, table string) bool {
	return regexp.MustCompile(`(FROM|JOIN|,)\s+` + table + `\b`).
		MatchString(sql)
}

// boundedRead: this statement's result cannot grow with the table, so it
// needs no LIMIT of its own.
//
// One function, so the guard below and the test that walks the shapes read
// the SAME decision. Two copies of it would be two things that must agree,
// which is the shape most of this project's criticals have had.
func boundedRead(sql, table string) bool {
	sql = sqlWithoutComments(sql)
	trimmed := strings.TrimSpace(sql)
	refs := regexp.MustCompile(`(FROM|JOIN|,)\s+`+table+`\b`).
		FindAllString(sql, -1)
	// A set operation is as many statements as it has branches, and the shape
	// of the first says nothing about the others: `SELECT count(*) FROM notes
	// UNION SELECT id FROM notes` opens with an aggregate and returns a row
	// per note.
	combined := regexp.MustCompile(`\b(?:UNION|INTERSECT|EXCEPT)\b`).
		MatchString(sql)
	// A bound at the END binds the whole statement, set operation included:
	// `A UNION B LIMIT 100` returns a hundred rows.
	if limitClause.MatchString(trimmed) {
		return true
	}
	// Everything below describes ONE query, and a set operation is as many as
	// it has branches: the aggregate shapes read the first, the anchored ones
	// read the last, and the branch beside it reads the table whole. Gating
	// only the aggregates left `A UNION B WHERE id=$1` calling itself bounded.
	if combined {
		return false
	}
	switch {
	// every reference to it here is an aggregate: one row, whichever way the
	// statement is shaped
	case len(refs) > 0 &&
		len(aggregateOver(table).FindAllString(sql, -1)) == len(refs):
		return true
	case oneRow.MatchString(trimmed) && !strings.Contains(sql, "GROUP BY"):
		return true
	case existsOnly.MatchString(trimmed):
		return true
	case singleRowByID.MatchString(trimmed):
		return true
	// The PENDING set alone is bounded by the INSERT: the ceiling is applied
	// there, so the rows that exist are already the bound. A LIMIT here would
	// bound the read by a number the insert only approximates, and past the
	// cap — where a race is able to leave the table — it cut off the OLDEST,
	// the legitimate early requests, on the one screen that can accept them.
	// Whatever is not pending has no such bound and keeps a LIMIT of its own.
	case pendingQueue.MatchString(trimmed):
		return true
	}
	return false
}

// An exemption describes the WHOLE statement, and these are the shapes that
// proved it has to. Every one of them was granted the pending exemption when
// it was a `strings.Contains`, and each returns rows the ceiling does not
// count — the refused ones are never deleted either.
func TestAnExemptionDescribesTheWholeStatement(t *testing.T) {
	const q = "HOSTING_REQUESTS"
	for _, sql := range []string{
		// what must STAY exempt: the queue as it is written
		"SELECT ID, SLUG FROM HOSTING_REQUESTS WHERE STATE='PENDING' " +
			"ORDER BY ID DESC",
		"SELECT COUNT(*) FROM HOSTING_REQUESTS WHERE STATE=$1",
		"SELECT SLUG, NAME FROM HOSTING_REQUESTS WHERE ID=$1 FOR UPDATE",
		"SELECT EXISTS(SELECT 1 FROM HOSTING_REQUESTS WHERE SLUG=$1)",
		// the shape the queue actually carries
		"SELECT ID FROM HOSTING_REQUESTS ORDER BY ID DESC LIMIT 200",
		"SELECT ID FROM HOSTING_REQUESTS ORDER BY ID DESC LIMIT $1 OFFSET $2",
		"SELECT ID FROM HOSTING_REQUESTS FETCH FIRST 100 ROWS ONLY",
		// the state bound as a parameter rather than written out
		"SELECT ID FROM HOSTING_REQUESTS WHERE STATE=$1 ORDER BY ID DESC",
		// one row of a WALLED table, written the way the wall requires, and
		// with the locking clauses PostgreSQL spells differently
		"SELECT ID FROM HOSTING_REQUESTS H WHERE H.ID=$1 AND H.ORG_ID=$2",
		"SELECT ID FROM HOSTING_REQUESTS WHERE ID=$1 FOR NO KEY UPDATE",
		// a bound with the locking clause its sister shape already accepts,
		// and a statement written with its terminator
		"SELECT ID FROM HOSTING_REQUESTS ORDER BY ID LIMIT 10 FOR UPDATE",
		"SELECT ID FROM HOSTING_REQUESTS LIMIT 10 FOR UPDATE SKIP LOCKED",
		"SELECT ID FROM HOSTING_REQUESTS ORDER BY ID DESC LIMIT 100;",
		// a literal that carries what a comment opens with: the text before
		// it is real SQL and the bound after it is real too
		"SELECT ID FROM HOSTING_REQUESTS WHERE SLUG='FOO/*BAR' " +
			"ORDER BY ID DESC LIMIT 100",
		// the union its LIMIT genuinely bounds, all branches at once
		"SELECT ID FROM HOSTING_REQUESTS UNION " +
			"SELECT ID FROM HOSTING_REQUESTS LIMIT 100",
	} {
		if !boundedRead(sql, q) {
			t.Errorf("a bounded read is refused, which is the false positive "+
				"that sends the next author around the guard:\n\t%s", sql)
		}
	}
	for _, sql := range []string{
		// pending OR something the ceiling does not count
		"SELECT ID, SLUG FROM HOSTING_REQUESTS WHERE STATE='PENDING' OR " +
			"STATE='REFUSED' ORDER BY ID DESC",
		// the negation of it
		"SELECT ID FROM HOSTING_REQUESTS WHERE NOT (STATE='PENDING')",
		// the exempting text in a comment, and in a trailing one
		"SELECT ID FROM HOSTING_REQUESTS /* STATE='PENDING' */",
		"SELECT ID FROM HOSTING_REQUESTS WHERE STATE<>'PENDING' " +
			"-- STATE='PENDING'",
		// one row by key, with something ORed onto it
		"SELECT ID FROM HOSTING_REQUESTS WHERE ID=$1 OR TS > $2",
		// …and the word LIMIT where it bounds nothing. PostgreSQL reads both
		// of these as every row.
		"SELECT ID FROM HOSTING_REQUESTS LIMIT ALL",
		"SELECT ID FROM HOSTING_REQUESTS LIMIT NULL",
		// in a comment, leading and trailing
		"/* LIMIT 200 */ SELECT ID FROM HOSTING_REQUESTS",
		"SELECT ID FROM HOSTING_REQUESTS -- LIMIT 200\n",
		// in a value the statement selects
		"SELECT 'LIMIT 200', ID FROM HOSTING_REQUESTS",
		// bounding a CTE and not the statement that reads the table
		"WITH B AS (SELECT ID FROM HOSTING_REQUESTS LIMIT 1) " +
			"SELECT ID FROM HOSTING_REQUESTS",
		// …and one bounding an OFFSET with no ceiling above it
		"SELECT ID FROM HOSTING_REQUESTS OFFSET 200",
		// EXISTS is one row only when the statement IS one, not when it
		// contains one — the last exemption left as a substring while its
		// neighbours were anchored
		"SELECT ID FROM HOSTING_REQUESTS WHERE EXISTS(SELECT 1 FROM ORGS)",
		"SELECT 'EXISTS(' AS MARKER, ID FROM HOSTING_REQUESTS",
		// a set operation is as many statements as it has branches, and the
		// shapes anchored at the END describe the last one alone
		"SELECT ID FROM HOSTING_REQUESTS UNION " +
			"SELECT ID FROM HOSTING_REQUESTS WHERE ID=$1",
		"SELECT ID FROM HOSTING_REQUESTS UNION " +
			"SELECT ID FROM HOSTING_REQUESTS WHERE STATE=$1",
	} {
		if boundedRead(sql, q) {
			t.Errorf("this reads rows the ceiling does not count and the "+
				"guard calls it bounded:\n\t%s", sql)
		}
	}
	// A set operation is as many statements as it has branches: the first
	// one's shape says nothing about the others.
	if boundedRead("SELECT COUNT(*) FROM NOTES UNION SELECT ID FROM NOTES",
		"NOTES") {
		t.Error("an aggregate UNIONed with a full read is called bounded by " +
			"the shape of its first branch")
	}
	// …and the aggregate GROUP BY makes unbounded: one row per distinct note
	if boundedRead("SELECT MAX(ID), NOTE FROM NOTES GROUP BY NOTE", "NOTES") {
		t.Error("an aggregate under GROUP BY returns one row per group, and " +
			"the guard reads the SELECT list alone")
	}
}

// Tables the application only ever adds to. `notes` grows by one row per
// status write, `hosting_requests` by one row per public form, and nothing
// deletes from either — so a SELECT over them without a ceiling is a
// response that grows without one. The card history is re-read on EVERY
// status write, which made one volunteer's 300 posts hold 386 MB of heap.
func TestEveryReadOfAnAppendOnlyTableIsBounded(t *testing.T) {
	appendOnly := []string{"NOTES", "HOSTING_REQUESTS"}
	files := apiPackage(t)
	values := stringValues(files)
	selects := 0
	// Per FUNCTION, through localScope, and not over the file with the
	// package map: stringValues holds package bindings alone, so a query
	// built in a local variable — which is most of them — resolves nowhere
	// else. Read with the package map only, this walked the whole package
	// and found almost nothing to check, in silence.
	for name, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			scoped := localScope(values, fn)
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				for _, arg := range call.Args {
					// SQL is case-insensitive and its whitespace is free; the
					// canary must be both, or `select … from  notes` walks
					// straight past it and no anchored shape ever matches.
					sql := sqlSpaces.ReplaceAllString(
						strings.ToUpper(sqlText(arg, scoped)), " ")
					if !strings.Contains(sql, "SELECT ") {
						continue
					}
					selects++
					for _, table := range appendOnly {
						if !readsTable(sql, table) || boundedRead(sql, table) {
							continue
						}
						t.Errorf("%s: a SELECT over %s is not bounded — that "+
							"table is only ever appended to:\n\t%s",
							name, table, strings.TrimSpace(sql))
					}
				}
				return true
			})
		}
	}
	// A floor, because every failure mode of this guard is silence: resolve
	// nothing and it reports nothing, which reads exactly like a package
	// where every read is bounded.
	if selects < 20 {
		t.Fatalf("only %d SELECT statements resolved: this guard is not "+
			"reading the package, and would pass whatever it says", selects)
	}
}

// textLength: a call measuring how long a piece of TEXT is. `len` is
// deliberately not here — it measures slices and maps far more often than
// strings, and every `len(rows) > 0` would drown the report. The byte/rune
// confusion it can hide is caught below instead, where it matters: a `len`
// compared against one of the ceilings, which are counted in runes because
// their messages promise characters.
func textLength(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	fn, ok := call.Fun.(*ast.SelectorExpr)
	return ok && (fn.Sel.Name == "RuneCountInString" || fn.Sel.Name == "RuneCount")
}

// byteLength: `len(x)`, which counts BYTES.
func byteLength(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	fn, ok := call.Fun.(*ast.Ident)
	return ok && fn.Name == "len"
}

// Ceilings on text are declared once, in auth.go, and every one of them is
// accounted for in the sum checked against maxBodySize. A literal recopied
// into a handler diverges from the message beside it; a ceiling under a
// name nobody thought of escapes the sum and makes a legitimate request
// answer 413.
//
// Keyed on the COMPARISON, not on a naming convention: whatever an
// expression is called, if it bounds a text length it is a ceiling.
func TestTextCeilingsAreNamedAndAccountedFor(t *testing.T) {
	for name, file := range apiPackage(t) {
		ast.Inspect(file, func(n ast.Node) bool {
			cmp, ok := n.(*ast.BinaryExpr)
			if !ok {
				return true
			}
			switch cmp.Op {
			case token.GTR, token.GEQ, token.LSS, token.LEQ, token.EQL, token.NEQ:
			default:
				return true
			}
			// either side may hold the measurement
			bound := cmp.Y
			measured := cmp.X
			if !textLength(cmp.X) && !byteLength(cmp.X) {
				bound, measured = cmp.X, cmp.Y
			}
			if byteLength(measured) {
				// a ceiling counted in runes, applied to bytes: it refuses
				// fewer characters than its own message announces
				if limit, ok := bound.(*ast.Ident); ok && accountedCeilings[limit.Name] {
					t.Errorf("%s: len() counts bytes and %s is a ceiling in "+
						"runes — an accented value is refused far below the "+
						"limit the message promises", name, limit.Name)
				}
				return true
			}
			if !textLength(measured) {
				return true
			}
			switch limit := bound.(type) {
			case *ast.BasicLit:
				if limit.Kind == token.INT {
					t.Errorf("%s: a text ceiling written as the literal %s. Use "+
						"one of the named ceilings in auth.go — they are what "+
						"the body limit is checked against", name, limit.Value)
				}
			case *ast.Ident:
				if !accountedCeilings[limit.Name] {
					t.Errorf("%s: %s bounds a text length and is not in the sum "+
						"checked against maxBodySize in "+
						"TestTheBodyCeilingHoldsBothEdges: a request at every "+
						"ceiling would answer 413", name, limit.Name)
				}
			}
			return true
		})
	}
}

// zeroByte: an expression denoting NUL, written any of the ways Go allows.
func zeroByte(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		switch e.Kind {
		case token.INT:
			return e.Value == "0"
		case token.CHAR:
			r, _, _, err := strconv.UnquoteChar(strings.Trim(e.Value, "'"), '\'')
			return err == nil && r == 0
		case token.STRING:
			text, err := strconv.Unquote(e.Value)
			return err == nil && strings.ContainsRune(text, 0)
		}
	case *ast.CallExpr:
		// rune(0), byte(0), []byte{0} written as a conversion
		if len(e.Args) == 1 {
			return zeroByte(e.Args[0])
		}
	case *ast.CompositeLit:
		for _, item := range e.Elts {
			if zeroByte(item) {
				return true
			}
		}
	}
	return false
}

// Anything a human types reaches PostgreSQL, and PostgreSQL refuses a NUL
// byte and malformed UTF-8 in any text value: unrefused, they answer 500
// on the sender's own screen. The refusal lives at the two points every
// input crosses — never in a handler, where the first attempt covered one
// write path out of ten.
// A route that decodes a `name` bounds it.
//
// Four routes write a name into a TEXT column — a team's, a campaign's, a
// hosting requester's, a person's — and three of them checked
// maxNameRunes. The fourth was routeCreateAccount, the one that writes a
// PERSON: a 128 KiB body against a ceiling of 120 writes a minute put
// megabytes into an unindexed column, and nothing else stood in the way.
//
// Read from the SOURCE, because the fifth route is the one this matters
// for. A handler that decodes a struct carrying `json:"name"` must name the
// ceiling in the same function — where a reader of that handler sees it.
func TestEveryRouteThatDecodesANameBoundsIt(t *testing.T) {
	files := apiPackage(t)

	// Which request types carry a name at all — EMBEDDED ONES INCLUDED.
	//
	// encoding/json promotes the fields of an embedded struct, so a type that
	// embeds one carrying `json:"name"` decodes a name exactly like a type
	// that declares it. Reading the direct fields alone, this collector missed
	// every such type, and the handler that decoded it was invisible to a
	// canary written to find precisely that. Resolved to a fixpoint, because
	// an embedded type may itself embed one.
	carriesName := map[string]bool{}
	// stands for: a name that IS another type. An embed and an alias reach
	// the same place — `type Alias = Inner` has a TypeSpec whose Type is an
	// identifier, not a struct, so it left the collector at the door and
	// every type embedding it stayed unmarked.
	standsFor := map[string][]string{}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := spec.Type.(*ast.StructType)
			if !ok {
				if id, ok := embeddedIdent(spec.Type); ok {
					standsFor[spec.Name.Name] = append(
						standsFor[spec.Name.Name], id)
				}
				return true
			}
			for _, f := range st.Fields.List {
				if decodesAName(f) {
					carriesName[spec.Name.Name] = true
				}
				// an anonymous field: its own name is the type it embeds —
				// unless the tag says the decoder ignores it, which `json:"-"`
				// does and the fixpoint below used to walk straight past
				if len(f.Names) == 0 && !embedIgnored(f) {
					if id, ok := embeddedIdent(f.Type); ok {
						standsFor[spec.Name.Name] = append(
							standsFor[spec.Name.Name], id)
					}
				}
			}
			return true
		})
	}
	for changed := true; changed; {
		changed = false
		for outer, inners := range standsFor {
			if carriesName[outer] {
				continue
			}
			for _, inner := range inners {
				if carriesName[inner] {
					carriesName[outer] = true
					changed = true
				}
			}
		}
	}
	if len(carriesName) == 0 {
		t.Fatal("no request type carries a name: this canary is looking at " +
			"the wrong thing and would pass whatever the handlers did")
	}

	checked := 0
	for name, file := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// Does this function DECODE one of those types? Declaring a
			// variable of the type is not enough: ReadOrg and readAccount
			// declare one and fill it from the database, where the length is
			// whatever was stored — bounded on the way IN, which is here.
			//
			// EVERY way of naming the type counts. Reading `var d req` alone
			// let `d := req{}` and `d := new(req)` — the more idiomatic
			// spellings — walk past a canary written to catch exactly the
			// route that forgot. A guard that knows one syntax of three
			// guards the syntax its author happened to use.
			declares, reads := false, false
			mentions := func(e ast.Expr) {
				switch t := e.(type) {
				case *ast.Ident: // var d req
					if carriesName[t.Name] {
						declares = true
					}
				case *ast.CompositeLit: // d := req{}
					if id, ok := t.Type.(*ast.Ident); ok && carriesName[id.Name] {
						declares = true
					}
				case *ast.CallExpr: // d := new(req)
					if id, ok := t.Fun.(*ast.Ident); ok && id.Name == "new" &&
						len(t.Args) == 1 {
						if arg, ok := t.Args[0].(*ast.Ident); ok && carriesName[arg.Name] {
							declares = true
						}
					}
				}
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CallExpr:
					if id, ok := node.Fun.(*ast.Ident); ok && id.Name == "readBody" {
						reads = true
					}
				case *ast.AssignStmt:
					for _, rhs := range node.Rhs {
						mentions(rhs)
					}
				case *ast.DeclStmt:
					gen, ok := node.Decl.(*ast.GenDecl)
					if !ok {
						return true
					}
					for _, sp := range gen.Specs {
						vs, ok := sp.(*ast.ValueSpec)
						if !ok {
							continue
						}
						if vs.Type != nil {
							mentions(vs.Type)
						}
						for _, v := range vs.Values {
							mentions(v)
						}
					}
				}
				return true
			})
			if !declares || !reads {
				continue
			}
			checked++
			bounded := false
			ast.Inspect(fn, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && id.Name == "maxNameRunes" {
					bounded = true
				}
				return true
			})
			if !bounded {
				t.Errorf("%s:%s decodes a name and never names maxNameRunes: "+
					"the body ceiling is 128 KiB and this column is text, so "+
					"the only bound left is how fast the rate limiter lets the "+
					"caller repeat", name, fn.Name.Name)
			}
		}
	}
	if checked < 3 {
		t.Errorf("only %d handler(s) matched: the canary stopped finding the "+
			"routes it exists for", checked)
	}
}

func TestUnstorableTextIsRefusedAtTheEntryPoints(t *testing.T) {
	files := apiPackage(t)
	router, auth := files["router.go"], files["auth.go"]
	if router == nil || auth == nil {
		t.Fatal("router.go or auth.go missing: the entry points cannot be checked")
	}
	if !callsFunction(router, "refuseUnstorableText") {
		t.Error("router.go: the path and query refusal no longer wraps the mux — " +
			"a NUL in ?department= answers 500 again")
	}
	if !callsFunction(auth, "carriesNul") {
		t.Error("auth.go: readBody no longer walks the decoded body — a NUL in " +
			"any request field answers 500 again")
	}

	// And no handler re-implements it: a local copy is the one that gets
	// forgotten, and that is how the first fix covered one path of ten.
	//
	// Exempted BY FUNCTION, not by file: a whole-file exemption let a
	// second copy sit beside the legitimate one unnoticed. The one
	// legitimate site — the pagination cursor is base64, so what the entry
	// points see is the encoding, not the three text fields inside it.
	exempt := map[string]bool{"cursor.go:decodeCursor": true}
	seen := map[string]bool{}
	for name, file := range files {
		if name == "router.go" || name == "auth.go" {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			where := name + ":" + fn.Name.Name
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) < 2 {
					return true
				}
				fun, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !strings.HasPrefix(fun.Sel.Name, "Contains") &&
					!strings.HasPrefix(fun.Sel.Name, "Index") {
					return true
				}
				if !zeroByte(call.Args[1]) {
					return true
				}
				if exempt[where] {
					seen[where] = true
					return true
				}
				t.Errorf("%s: a per-handler NUL check. The entry points do this "+
					"for every route; a local copy is the one that gets "+
					"forgotten", where)
				return true
			})
		}
	}
	for where := range exempt {
		if !seen[where] {
			t.Errorf("%s no longer carries the NUL check it was exempted for: "+
				"either it moved, or this exemption is stale", where)
		}
	}
}

// callsFunction: does this file call the named package function anywhere?
func callsFunction(file *ast.File, name string) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == name {
				found = true
			}
		}
		return !found
	})
	return found
}
