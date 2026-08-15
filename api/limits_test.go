package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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
				if text := sqlText(ret.Results[0], known); text != "" {
					known[fn.Name.Name] = text
				}
			}
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

// oneRow: a query whose result cannot grow. An aggregate over the whole
// table returns exactly one row — refusing those blocked legitimate reads
// (the highest id since the last poll) for nothing.
var oneRow = regexp.MustCompile(
	`^SELECT\s+(COUNT|MAX|MIN|SUM|AVG)\(`)

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
					// SQL is case-insensitive; the canary must be too, or
					// `select … from notes` walks straight past it
					sql := strings.ToUpper(sqlText(arg, scoped))
					if !strings.Contains(sql, "SELECT ") {
						continue
					}
					selects++
					for _, table := range appendOnly {
						// FROM, JOIN or a comma list: the table is read the same
						// way whichever keyword introduces it
						named := regexp.MustCompile(`(FROM|JOIN|,)\s+` + table + `\b`)
						if !named.MatchString(sql) {
							continue
						}
						if oneRow.MatchString(strings.TrimSpace(sql)) ||
							strings.Contains(sql, "EXISTS(") ||
							strings.Contains(sql, "WHERE ID=$1") {
							continue
						}
						if !strings.Contains(sql, "LIMIT") {
							t.Errorf("%s: a SELECT over %s carries no LIMIT — that "+
								"table is only ever appended to:\n\t%s",
								name, table, strings.TrimSpace(sql))
						}
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
func TestUnstorableTextIsRefusedAtTheEntryPoints(t *testing.T) {
	files := apiPackage(t)
	main, auth := files["main.go"], files["auth.go"]
	if main == nil || auth == nil {
		t.Fatal("main.go or auth.go missing: the entry points cannot be checked")
	}
	if !callsFunction(main, "refuseUnstorableText") {
		t.Error("main.go: the path and query refusal no longer wraps the mux — " +
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
	exempt := map[string]bool{"routes_mayors.go:decodeCursor": true}
	seen := map[string]bool{}
	for name, file := range files {
		if name == "main.go" || name == "auth.go" {
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
