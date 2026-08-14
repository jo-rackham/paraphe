package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"regexp"
	"slices"
	"strconv"
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
	// The OPENING tag of a PostgreSQL dollar-quoted string. `$1` is not one:
	// a tag never starts with a digit.
	dollarOpen = regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*\$|\$\$`)
	sqlSpaces  = regexp.MustCompile(`\s+`)
	// A predicate whose RIGHT-hand side is bounded. Checking the equality
	// alone accepted `org_id = org_id`, `org_id = COALESCE($1, org_id)` and
	// `org_id = ANY(SELECT id FROM orgs)` — three ways of writing "every
	// campaign" that read as a filter.
	orgPredicate = regexp.MustCompile(
		`(?:([A-Z][A-Z0-9_]*)\s*\.\s*)?ORG_ID(?:::[A-Z]+)?\s*=\s*` +
			`(\$SUB\d+|\$\d+|%\[?\d*\]?S|(?:([A-Z][A-Z0-9_]*)\s*\.\s*)?ORG_ID)`)
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
	// Literals FIRST. Run the other way round, the block-comment pattern
	// spanned two strings — `WHERE a='/*' AND org_id=$1 AND b='*/'` — and ate
	// the real predicate between them.
	sql = stripDollarQuoted(sql)
	sql = sqlStringLit.ReplaceAllString(sql, "''")
	sql = sqlBlockComment.ReplaceAllString(sql, " ")
	sql = sqlLineComment.ReplaceAllString(sql, " ")
	return sqlSpaces.ReplaceAllString(strings.ToUpper(sql), " ")
}

// sqlTextMarked is sqlText with a difference that matters: an expression it
// cannot resolve becomes the marker `$?` instead of vanishing. Dropped, a
// bound parameter written `"org_id="+req.p(org)` left the text reading
// `ORG_ID=` — no right-hand side — and a predicate that IS bound looked like
// one that is not. The marker counts as bound; a right-hand side the
// resolver CAN read is judged on its merits.
func sqlTextMarked(expr ast.Expr, values map[string]string) string {
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		return sqlTextMarked(e.X, values) + sqlTextMarked(e.Y, values)
	case *ast.ParenExpr:
		return sqlTextMarked(e.X, values)
	case *ast.CallExpr:
		// fmt.Sprintf and friends: the format string plus its arguments is
		// as close to the statement as source can get.
		var out string
		for _, a := range e.Args {
			out += sqlTextMarked(a, values)
		}
		if out == "" {
			return "$?"
		}
		return out
	}
	if txt, ok := resolveString(expr, values); ok {
		return txt
	}
	return "$?"
}

// stripDollarQuoted removes PostgreSQL dollar-quoted strings. RE2 has no
// backreferences, so "the same tag again" cannot be written as a pattern —
// the scan is spelled out instead. Left standing, `$$fake org_id = $1$$`
// made a string comparison read as a filter.
func stripDollarQuoted(sql string) string {
	for {
		loc := dollarOpen.FindStringIndex(sql)
		if loc == nil {
			return sql
		}
		tag := sql[loc[0]:loc[1]]
		rest := sql[loc[1]:]
		end := strings.Index(rest, tag)
		if end < 0 {
			// unterminated: not valid SQL, and nothing to strip
			return sql
		}
		sql = sql[:loc[0]] + "''" + rest[end+len(tag):]
	}
}

// levels splits a statement by nesting: each parenthesised group becomes a
// level of its own, and the text around it keeps a `$SUB` where the group
// stood.
//
// Two things follow, and both were findings. A table named only inside a
// subquery — `SELECT x.email FROM (SELECT email FROM accounts) x` — is
// checked at ITS level instead of disappearing with the fold. And a
// predicate whose right-hand side IS a subquery — `WHERE org_id = (SELECT id
// FROM orgs WHERE slug=$1)` — reads as `ORG_ID = $SUB`, which is bounded,
// instead of looking like a predicate with nothing on its right.
func levels(sql string) []string {
	inner := regexp.MustCompile(`\(([^()]*)\)`)
	var out []string
	for {
		m := inner.FindStringSubmatchIndex(sql)
		if m == nil {
			break
		}
		out = append(out, sql[m[2]:m[3]])
		sql = sql[:m[0]] + fmt.Sprintf(" $SUB%d ", len(out)-1) + sql[m[1]:]
	}
	return append(out, sql)
}

// bounded: does this level carry a value from outside the statement? A
// parenthesised group counted as a bounded right-hand side whatever it held,
// so `WHERE org_id = (org_id)` — a tautology — and `WHERE org_id = (SELECT
// 1)` — a constant naming whichever campaign is number one — both read as
// filters. A group is bounding only if something in it is.
func bounded(level string) bool {
	if boundValue.MatchString(level) {
		return true
	}
	// a group holding another group: follow it
	for _, m := range subRef.FindAllStringSubmatch(level, -1) {
		if i, err := strconv.Atoi(m[1]); err == nil && i < len(currentLevels) {
			if bounded(currentLevels[i]) {
				return true
			}
		}
	}
	return false
}

var (
	boundValue = regexp.MustCompile(`\$\d+|%\[?\d*\]?S`)
	subRef     = regexp.MustCompile(`\$SUB(\d+)`)
	// the levels of the statement being checked, so bounded() can follow a
	// $SUB into the group it stands for
	currentLevels []string
)

// assignments: the SET clause of an UPDATE. `SET org_id = $1` moves a row to
// a campaign; it does not restrict which rows are touched, and counting it
// as a filter let `UPDATE accounts SET org_id=$1 WHERE id=$2` relabel any
// account of any campaign.
var assignments = regexp.MustCompile(`\bSET\b.*?(?:\bWHERE\b|\bRETURNING\b|$)`)

// tableRef matches a walled table however it is written: schema-qualified,
// quoted, aliased with or without AS, and after USING as well as FROM/JOIN.
//
// The schema and the table are quoted INDEPENDENTLY. Accepting `public.x` and
// `"x"` but not `"public"."x"` did not merely miss a spelling: an unmatched
// reference is no reference at all, so the query carried no walled table and
// was passed over — invisible reading as compliant, one more time.
// TestEverySpellingOfAWalledTableIsSeen pins the shapes down.
func tableRef(table string) *regexp.Regexp {
	return regexp.MustCompile(
		`(FROM|JOIN|INTO|USING|UPDATE|,)\s+(?:ONLY\s+)?` +
			`(?:(?:"[A-Z_]+"|[A-Z_]+)\s*\.\s*)?"?` + table +
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
						if txt := sqlTextMarked(a.Rhs[0], values); txt != "" &&
							txt != "$?" {
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
					if sqlText(arg, scoped) == "" {
						continue
					}
					out = append(out, statement{name, fn.Name.Name,
						normaliseSQL(sqlTextMarked(arg, scoped))})
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
		`"?\s*\$SUB(\d+)`)
}

// insertNamesCampaign: the row written carries the campaign, AND — when the
// write may fall back onto an existing row — the conflict key names it too.
// `ON CONFLICT (name) DO UPDATE SET …` reaches another campaign's row, and
// the SET clause is not scanned for predicates, so nothing else would see it.
func insertNamesCampaign(table, level string) bool {
	m := insertNaming(table).FindStringSubmatch(level)
	if m == nil {
		return false
	}
	cols, err := strconv.Atoi(m[1])
	if err != nil || cols >= len(currentLevels) ||
		!strings.Contains(currentLevels[cols], "ORG_ID") {
		return false
	}
	key := conflictKey.FindStringSubmatch(level)
	if key == nil {
		return true // no fallback: only the row being written is touched
	}
	i, err := strconv.Atoi(key[1])
	return err == nil && i < len(currentLevels) &&
		strings.Contains(currentLevels[i], "ORG_ID")
}

// conflictKey: `ON CONFLICT (…)` names the columns the write may fall back
// onto. If it does not name the campaign, the DO UPDATE reaches rows of
// another one — and the SET clause is not scanned for predicates, so nothing
// else would have caught it.
var conflictKey = regexp.MustCompile(`ON CONFLICT\s*\$SUB(\d+)`)

// qualifiers: every name under which a predicate may address this table
// reference. With an alias, that alias and nothing else. Without one, either
// no qualifier at all or the table's own name — `WHERE accounts.org_id = $1`
// is how the predicate is written when no alias was declared, and refusing it
// sent the next author to write AROUND the canary rather than through it.
func qualifiers(alias, table string) []string {
	if alias != "" {
		return []string{alias}
	}
	return []string{"", table}
}

// boundEverywhere: EVERY disjunct must bound this reference on its own. A
// branch that does not is a branch through which the statement returns another
// campaign's rows, whatever the other branches say.
func boundEverywhere(perDisjunct [][][]string, chain [][]string, names []string) bool {
	for _, preds := range perDisjunct {
		if !boundAlias(preds, chain, names, map[string]bool{}) {
			return false
		}
	}
	return true
}

// boundAlias: is this reference bound to a value from OUTSIDE the statement —
// directly, or along a chain of joins that ends on one?
//
// `a.org_id = t.org_id` carries the campaign from t to a, but ONLY if t is
// itself bound. Asking no further, the canary accepted `a.org_id = t.org_id
// AND t.org_id = a.org_id`: each table vouched for the other, the chain closed
// on nothing, and a privileged role served every campaign that owns a team.
// `seen` cuts the cycle, so a chain proves something only when it reaches a
// parameter.
//
// `preds` are this disjunct's, at this nesting level: that is where the
// reference must be bound. `chain` holds the whole statement's, because a
// CORRELATED subquery is bound by its outer query — `(SELECT count(*) FROM
// accounts c WHERE c.org_id = g.org_id)` under `FROM teams g WHERE
// g.org_id = $1` is walled, and looking for g at the inner level alone
// refused it.
func boundAlias(preds, chain [][]string, names []string, seen map[string]bool) bool {
	key := strings.Join(names, "\x00")
	if seen[key] {
		return false
	}
	seen[key] = true
	for _, p := range preds {
		if !slices.Contains(names, p[1]) {
			continue
		}
		rhs := p[2]
		if m := subRef.FindStringSubmatch(rhs); m != nil {
			i, err := strconv.Atoi(m[1])
			if err == nil && i < len(currentLevels) && bounded(currentLevels[i]) {
				return true
			}
			// An unbounded group proves nothing, but the predicates are
			// AND-ed: another one may still bind. Returning here let the
			// first group met decide for the whole statement.
			continue
		}
		if strings.HasPrefix(rhs, "$") || strings.HasPrefix(rhs, "%") {
			return true
		}
		// <other>.ORG_ID: a join between two walled tables, which is how the
		// campaign travels from one to the other. The SAME alias on both
		// sides says nothing at all.
		if p[3] != "" && !slices.Contains(names, p[3]) &&
			boundAlias(chain, chain, []string{p[3]}, seen) {
			return true
		}
	}
	return false
}

func aliasOrTable(alias, table string) string {
	if alias == "" {
		return table
	}
	return alias
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
		print := fingerprint(st.SQL)
		if _, allowed := crossesCampaigns[print]; allowed {
			used[print] = true
			continue
		}
		where := st.File + ":" + st.Func
		// One Exec can carry several statements. Without the split, the
		// first one's `org_id = $1` counted as the second one's filter.
		for _, one := range strings.Split(st.SQL, ";") {
			currentLevels = levels(one)
			// Where a join chain may look for the OTHER alias's own binding:
			// anywhere in the statement, because a correlated subquery is
			// bound by the query enclosing it. The reference itself is still
			// judged at its own level, in its own disjunct, below.
			var chain [][]string
			for _, l := range currentLevels {
				chain = append(chain, orgPredicate.FindAllStringSubmatch(
					assignments.ReplaceAllString(l, " WHERE "), -1)...)
			}
			for _, level := range currentLevels {
				// The SET clause moves a row to a campaign; it never says
				// WHICH rows are touched.
				scanned := assignments.ReplaceAllString(level, " WHERE ")
				// Every disjunct, separately, and ALL of them. `WHERE
				// org_id=$1 OR volunteer=$2` names the campaign once and lets
				// the other branch through: a filter that holds on one side of
				// an OR holds on neither.
				//
				// Checking only the disjuncts AFTER the first left the first
				// one unexamined — `WHERE TRUE OR org_id=$1` read as bounded,
				// and `neutralised` did not save it either, looking only to
				// the RIGHT of the OR. And pouring every disjunct's predicates
				// into ONE bag said the same of `a.org_id=$1 OR b.org_id=$2`,
				// where neither branch bounds both tables. Hence one bag per
				// disjunct, and each must stand alone.
				var perDisjunct [][][]string
				for _, d := range strings.Split(scanned, " OR ") {
					perDisjunct = append(perDisjunct,
						orgPredicate.FindAllStringSubmatch(d, -1))
				}
				for _, table := range tables {
					for _, m := range refs[table].FindAllStringSubmatch(level, -1) {
						if neutralised.MatchString(level) {
							t.Errorf("%s: this query on %s carries an "+
								"always-true disjunction, which cancels "+
								"whatever else it says:\n\t%s",
								where, table, st.SQL)
							continue
						}
						alias := m[2]
						if notAnAlias[alias] {
							alias = ""
						}
						// An INSERT is never bounded by a WHERE. That clause
						// restricts what its SELECT source READS; it says
						// nothing about which campaign the new row lands in.
						// Consulting the column list only as a fallback,
						// `INSERT INTO assignments (insee_code, status) SELECT
						// … FROM notes WHERE org_id=$1` read as compliant,
						// and the canary's promise — the row written carries
						// the campaign — was left to a NOT NULL constraint.
						if strings.EqualFold(strings.TrimSpace(m[1]), "INTO") {
							if insertNamesCampaign(table, level) {
								continue
							}
							t.Errorf("%s: this INSERT into %s does not name "+
								"the campaign in its column list (and, when it "+
								"may fall back on an existing row, in its ON "+
								"CONFLICT key). A WHERE in the source SELECT "+
								"does not stand in for it:\n\t%s",
								where, table, st.SQL)
							continue
						}
						if boundEverywhere(perDisjunct, chain,
							qualifiers(alias, table)) {
							continue
						}
						t.Errorf("%s: this reference to %s is not bounded to "+
							"the campaign (%s.org_id = $n, at its own nesting "+
							"level and outside any SET). A privileged database "+
							"role would serve one campaign's work to another. "+
							"If the crossing is deliberate, add %q to "+
							"crossesCampaigns:\n\t%s",
							where, table, aliasOrTable(alias, table), print, st.SQL)
					}
				}
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

// A walled table the canary does not RECOGNISE is a table it does not guard,
// and it says nothing about it — the same silence as a query it cannot read.
// `FROM "public"."accounts"` was spelt in a way tableRef did not match, so the
// statement carried no walled table at all and went through unexamined.
//
// Every spelling PostgreSQL accepts for the same table must be seen. Adding a
// form here before supporting it is the point: the list is the specification.
func TestEverySpellingOfAWalledTableIsSeen(t *testing.T) {
	for _, spelling := range []string{
		`SELECT X FROM %[1]s`,
		`SELECT X FROM "%[1]s"`,
		`SELECT X FROM PUBLIC.%[1]s`,
		`SELECT X FROM PUBLIC. %[1]s`,
		`SELECT X FROM "PUBLIC".%[1]s`,
		`SELECT X FROM PUBLIC."%[1]s"`,
		`SELECT X FROM "PUBLIC"."%[1]s"`,
		`SELECT X FROM ONLY %[1]s`,
		`SELECT X FROM %[1]s A`,
		`SELECT X FROM %[1]s AS A`,
		`SELECT X FROM T, %[1]s`,
		`SELECT X FROM T JOIN %[1]s ON TRUE`,
		`DELETE FROM T USING %[1]s`,
		`UPDATE %[1]s SET X=1`,
		`INSERT INTO %[1]s $SUB0 VALUES $SUB1`,
	} {
		for _, table := range walledTablesUpper() {
			sql := fmt.Sprintf(spelling, table)
			if !tableRef(table).MatchString(sql) {
				t.Errorf("the canary does not see %s written this way, so a "+
					"query on it would carry no walled table and be passed "+
					"over in silence:\n\t%s", table, sql)
			}
		}
	}
}

// setOrgScope declares which campaign the transaction speaks for, and RLS
// takes its word for it. Called from a handler with a number the client chose,
// it moves the whole request into ANOTHER campaign — and every other guard
// here stays green while it happens: the SQL still reads `org_id = $1`, and
// app.org_id agrees with it. The two walls were checking each other against
// the same lie.
//
// Which campaign a request speaks for is decided in ONE place, from the Host
// header, plus maintenance at startup. The list below is that decision.
func TestOnlyTheScopeItselfDeclaresTheCampaign(t *testing.T) {
	allowed := map[string]bool{
		// resolves the campaign from the subdomain, and from nothing else
		"scope.go:openScope": true,
		// its own definition, and the helper that restores the previous scope
		"multiorg.go:setOrgScope":  true,
		"multiorg.go:withOrgScope": true,
		// the import at startup, which crosses every campaign by design
		"db.go:InitDatabase": true,
	}

	seen := map[string]bool{}
	for name, file := range apiPackage(t) {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			where := name + ":" + fn.Name.Name
			ast.Inspect(fn, func(n ast.Node) bool {
				declares := false
				switch v := n.(type) {
				case *ast.Ident:
					declares = v.Name == "setOrgScope"
				case *ast.BasicLit:
					// The helper can be bypassed by writing the statement
					// out; the string is what PostgreSQL ends up reading.
					declares = strings.Contains(v.Value, "app.org_id")
				}
				if !declares {
					return true
				}
				seen[where] = true
				if !allowed[where] {
					t.Errorf("%s declares the transaction's campaign. Only "+
						"the scope may: from a handler, with a value the "+
						"request carries, this walks into another campaign "+
						"with every wall left standing — the SQL still names "+
						"a campaign, and RLS still agrees with it. If this "+
						"is deliberate, say so by name here.", where)
				}
				return true
			})
		}
	}
	// A permission that covers nothing is a claim about code that has moved,
	// and the next function written under that name would inherit it.
	for where := range allowed {
		if !seen[where] {
			t.Errorf("%s no longer declares the campaign — drop it from the "+
				"list rather than leave a standing permission", where)
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

// `scoped(r)` is the only way to open a query on a walled table, and this is
// what makes `org_id=$1` mean what it says.
//
// Written `req := &query{}` followed by three bindings, $1 is whichever
// parameter happened to be bound first. Reordering two of those lines was
// enough for `WHERE org_id=$1` to filter on a team identifier instead of the
// campaign — with every other guard green, because the SQL still reads
// `org_id=$1`. No canary that looks at SQL can see that; the constructor is
// what carries the guarantee.
func TestTheCampaignIsBoundByTheConstructorAlone(t *testing.T) {
	files := apiPackage(t)
	var offenders []string
	seen := 0
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
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				id, ok := lit.Type.(*ast.Ident)
				if !ok || id.Name != "query" {
					return true
				}
				seen++
				// `scoped` itself has to build one
				if name == "auth.go" && fn.Name.Name == "scoped" {
					return true
				}
				offenders = append(offenders, name+":"+fn.Name.Name)
				return true
			})
		}
	}
	if seen == 0 {
		t.Fatal("no query builder found at all: this canary is reading nothing")
	}
	if len(offenders) > 0 {
		t.Errorf("these build a query without binding the campaign first, so "+
			"$1 is whatever they bind first — use scoped(r):\n\t%s",
			strings.Join(offenders, "\n\t"))
	}
}
