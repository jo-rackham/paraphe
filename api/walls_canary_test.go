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

// The campaign wall, read from the source: every query naming a per-campaign
// table must also name the campaign.
//
// This is a regular-expression heuristic over the package's AST, not a SQL
// parser, and has to be read as one: every rule below exists because some
// shape of valid SQL slips past a simpler one, and says which shape.
//
// What it cannot see: whether a predicate in a SUBQUERY constrains the outer
// statement. TestNoCampaignSeesAnother covers that, by exercising every route
// against two campaigns.

// The list comes from multiorg.go, not from a copy, and the database is
// asked whether it is complete — TestEveryPerCampaignTableIsWalled. A table
// carrying org_id and missing from it would be checked by nobody.
func walledTablesUpper() []string {
	out := make([]string, 0, len(walledTables))
	for _, t := range walledTables {
		out = append(out, strings.ToUpper(t))
	}
	return out
}

var (
	sqlLineComment = regexp.MustCompile(`--[^\n]*`)
	// The OPENING tag of a PostgreSQL dollar-quoted string. `$1` is not one:
	// a tag never starts with a digit.
	dollarOpen = regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*\$|\$\$`)
	sqlSpaces  = regexp.MustCompile(`\s+`)
	// A predicate whose RIGHT-hand side is bounded. The equality alone is not
	// enough: `org_id = org_id`, `org_id = COALESCE($1, org_id)` and
	// `org_id = ANY(SELECT id FROM orgs)` all mean "every campaign".
	//
	// `\bORG_ID\b`, and the word boundary is not decoration: without the
	// leading one, `WHERE parent_org_id = $1` matched on its tail and read
	// as an unqualified campaign predicate — a column that names a parent
	// row, vouching for the wall. orgIDWord, three declarations below, has
	// always had it.
	orgPredicate = regexp.MustCompile(
		`(?:([A-Z][A-Z0-9_]*)\s*\.\s*)?\bORG_ID\b(?:::[A-Z]+)?\s*=\s*` +
			`(\$SUB\d+|\$\d+|%\[?\d*\]?S|(?:([A-Z][A-Z0-9_]*)\s*\.\s*)?\bORG_ID\b)`)
	// Predicates that are always true, whatever else the statement says.
	neutralised = regexp.MustCompile(`\bOR\s+(TRUE|1\s*=\s*1)\b`)
	// A statement whose TABLE is a format verb: which table it reads cannot
	// be known from the source, so neither can whether it names the campaign.
	dynamicTable = regexp.MustCompile(`(?:FROM|JOIN|INTO|USING|UPDATE)\s+%`)
	// A table POSITION whose operand the canary could not read as a name.
	//
	// This is the inversion the eighth round argued for, and it is the one
	// rule that closes a whole class rather than a shape. Until it existed,
	// a reference the canary failed to resolve simply was not a reference:
	// the statement carried no walled table, matched no rule, and was passed
	// over in silence — "I could not read this" answered as "nothing to see
	// here". Three of the four criticals were only that, dressed
	// differently:
	//
	//	FROM %      a format verb — the table is chosen at run time
	//	FROM $?     an expression sqlTextMarked gave up on
	//	FROM ,      a name that VANISHED: `strings.Join(tables, ",")` left
	//	            its separator behind and nothing else
	//	FROM        the text ends where the table should be
	//
	// A statement matching this touches a table and does not say which, so
	// nothing can be concluded about the campaign — which is a finding, not
	// a pass. Either the query becomes readable, or the site is declared in
	// readsNoWalledTable.
	unreadableTable = regexp.MustCompile(
		`\b(?:FROM|JOIN|INTO|USING)\s*(?:%|\$\?|,|;|$)` +
			// UPDATE is listed apart, and without the end-of-text case: the
			// row locking this application allocates cards with ends its
			// statement on `FOR UPDATE`, which is a locking clause and not a
			// table position. Only an operand it could not read counts.
			`|\bUPDATE\s*(?:%|\$\?|,)`)
	// What makes a resolved string a statement rather than a value. Session
	// commands count: `SET lock_timeout` reads no table, but it IS the
	// statement of its call.
	//
	// It is also the GATE of the main test, so every verb the rules below
	// know must be here: destructiveRef learned REINDEX, CLUSTER and REVOKE
	// and this pattern had not, so those three statements were dropped
	// before the rule that exists for them ever ran. The rule passed its own
	// unit test and guarded nothing. TestEveryDestructiveVerbReachesTheRules
	// ties the two together.
	sqlVerb = regexp.MustCompile(
		`\b(SELECT|INSERT|UPDATE|DELETE|WITH|SET|COPY|LOCK|CREATE|ALTER|DROP` +
			`|GRANT|REVOKE|TRUNCATE|REINDEX|CLUSTER|REFRESH|COMMENT|DO)\b`)
)

// normaliseSQL: what the canary reads. Comments and string literals are
// REMOVED — "org_id" inside either is not a filter.
func normaliseSQL(sql string) string {
	// Literals FIRST: the other way round, a block-comment pattern spans two
	// strings — `WHERE a='/*' AND org_id=$1 AND b='*/'` — and eats the real
	// predicate between them.
	sql = stripDollarQuoted(sql)
	sql = stripStringLiterals(sql)
	sql = stripBlockComments(sql)
	sql = sqlLineComment.ReplaceAllString(sql, " ")
	return sqlSpaces.ReplaceAllString(strings.ToUpper(sql), " ")
}

// stripStringLiterals removes single-quoted strings, ESCAPES INCLUDED. A
// pattern closing on the first quote it meets leaves `ORG_ID=$1` standing
// outside a string PostgreSQL reads as one — the E'…' form escapes a quote
// with a backslash, and any string escapes one by doubling it.
func stripStringLiterals(sql string) string {
	var out strings.Builder
	for i := 0; i < len(sql); {
		if sql[i] != '\'' {
			out.WriteByte(sql[i])
			i++
			continue
		}
		extended := i > 0 && (sql[i-1] == 'E' || sql[i-1] == 'e')
		i++
		for i < len(sql) {
			if extended && sql[i] == '\\' && i+1 < len(sql) {
				i += 2
				continue
			}
			if sql[i] == '\'' {
				if i+1 < len(sql) && sql[i+1] == '\'' {
					i += 2 // a doubled quote is one quote, not the end
					continue
				}
				i++
				break
			}
			i++
		}
		out.WriteString("''")
	}
	return out.String()
}

// stripBlockComments removes /* … */ COUNTING THE NESTING, which PostgreSQL
// does and a non-greedy pattern does not. `/* /* */ org_id=$1 */` is entirely
// a comment to the server, so reading a predicate out of it credits the query
// with a filter the database never applies.
func stripBlockComments(sql string) string {
	var out strings.Builder
	depth := 0
	for i := 0; i < len(sql); {
		switch {
		case strings.HasPrefix(sql[i:], "/*"):
			depth++
			i += 2
		case depth > 0 && strings.HasPrefix(sql[i:], "*/"):
			depth--
			i += 2
			out.WriteByte(' ')
		default:
			if depth == 0 {
				out.WriteByte(sql[i])
			}
			i++
		}
	}
	return out.String()
}

// sqlTextMarked is sqlText with one difference: an expression it cannot
// resolve becomes the marker `$?` instead of vanishing. Dropped, the gap
// closes and the text reads as something nobody wrote — `"… FROM " +
// join(tables)` became `FROM `, a statement with no table, hence no walled
// table, hence nothing to check.
//
// The marker does NOT count as bounded, and this comment used to say the
// opposite of what the code did. `ORG_ID = $?` says the canary could not
// read the right-hand side, and orgPredicate does not accept it: an
// unreadable value may be a bound parameter or the string a caller passed
// in, and telling them apart is the whole question. A marker is what the
// reader failed to do, never what the code proved — either the predicate
// becomes readable, or the crossing is declared.
func sqlTextMarked(expr ast.Expr, values map[string]string) string {
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		return sqlTextMarked(e.X, values) + sqlTextMarked(e.Y, values)
	case *ast.ParenExpr:
		return sqlTextMarked(e.X, values)
	case *ast.CallExpr:
		// A call to a package function whose whole body returns a string IS
		// that string. `assignmentJoin("$1")` read as its argument alone,
		// and the join it builds — the wall of every mayor listing —
		// vanished from what the canary examined.
		//
		// The body was learned with each parameter standing as `$ARGn`, and
		// the caller's argument goes back where it belongs: a helper writing
		// `"… org_id=" + placeholder` then reads as `… ORG_ID=$1` at the site
		// that passes "$1", and as an unbounded predicate at a site that
		// passes something else. Substituting nothing would judge every
		// caller by the helper's shape rather than by what it was handed.
		if id, ok := e.Fun.(*ast.Ident); ok {
			if text, known := values[id.Name]; known && text != "" {
				for i, arg := range e.Args {
					text = strings.ReplaceAll(text, fmt.Sprintf("$ARG%d", i+1),
						sqlTextMarked(arg, values))
				}
				return text
			}
		}
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

// stripDollarQuoted removes PostgreSQL dollar-quoted strings: left standing,
// `$$fake org_id = $1$$` makes a string comparison read as a filter. RE2 has
// no backreferences, so "the same tag again" cannot be a pattern and the scan
// is spelled out.
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
// Two things follow. A table named only inside a subquery — `SELECT x.email
// FROM (SELECT email FROM accounts) x` — is checked at ITS level instead of
// disappearing with the fold. And a predicate whose right-hand side IS a
// subquery — `WHERE org_id = (SELECT id FROM orgs WHERE slug=$1)` — reads as
// `ORG_ID = $SUB`, which is bounded, rather than as a predicate with nothing
// on its right.
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
// parenthesised group is bounding only if something IN it is — `(org_id)` is
// a tautology and `(SELECT 1)` names whichever campaign is number one.
func bounded(level string) bool {
	// A group that can fall back ON THE COLUMN bounds nothing: it holds a
	// parameter, but `org_id = (COALESCE($1, org_id))` is `org_id = org_id`
	// for every row whenever $1 is null.
	if orgIDWord.MatchString(level) {
		return false
	}
	// A SUBQUERY bounds only if the parameter picks the row. Anywhere else —
	// an OFFSET, a LIMIT, an ORDER BY — it chooses which campaign comes back
	// without being compared to anything: `= (SELECT id FROM orgs LIMIT 1
	// OFFSET $1)` serves the caller whichever one they ask for.
	if selectWord.MatchString(level) {
		where := subqueryWhere.FindStringSubmatch(level)
		return where != nil && boundValue.MatchString(where[1])
	}
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
	// The column, not a column whose name merely starts with it: a substring
	// test accepts `org_id_backup` in an INSERT's column list and in an ON
	// CONFLICT key, reading a campaign where none is written.
	orgIDWord = regexp.MustCompile(`\bORG_ID\b`)
	// Statements whose body is code, not text: stripping the dollar quotes
	// leaves nothing to check, and running them touches everything.
	// `DO` only where a statement STARTS: as a word it also opens the
	// `ON CONFLICT … DO UPDATE` of every upsert in the package.
	procedural = regexp.MustCompile(
		`^\s*DO\b|\bCREATE\s+(?:OR\s+REPLACE\s+)?(?:FUNCTION|PROCEDURE)\b`)
	// A parenthesised group holding a SELECT is a SUBQUERY: its predicates
	// constrain what IT reads, never the statement around it. Any other group
	// is an expression belonging to the level that wrote it.
	selectWord = regexp.MustCompile(`\bSELECT\b`)
	orWord     = regexp.MustCompile(`\bOR\b`)
	// A group, and whether a NOT stands in front of it. RE2 has no
	// look-behind, so the negation is captured with the marker.
	negatedGroup = regexp.MustCompile(`(\bNOT\s*)?\$SUB(\d+)`)
	// The parameter of a subquery bounds the statement only when it decides
	// WHICH ROW comes back. `= (SELECT id FROM orgs LIMIT 1 OFFSET $1)` holds
	// one and lets the caller walk the campaigns by choosing it.
	subqueryWhere = regexp.MustCompile(`(?s)\bWHERE\b(.*)$`)
	// The branches of a set operation are independent statements sharing one
	// result: each must name the campaign on its own.
	setOperation = regexp.MustCompile(`\b(?:UNION ALL|UNION|INTERSECT|EXCEPT)\b`)
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
		// A keyword needs the space that separates it from the name; a comma
		// does not, and `FROM teams g,accounts c` hid the second table
		// entirely.
		`((?:FROM|JOIN|INTO|USING|UPDATE)\s+|,\s*)(?:ONLY\s+)?` +
			`(?:(?:"[A-Z_]+"|[A-Z_]+)\s*\.\s*)?"?` + table +
			`"?(?:\s+(?:AS\s+)?([A-Z][A-Z0-9_]*))?`)
}

// destructiveRef: the commands no predicate can bound. TRUNCATE empties a
// table whatever any WHERE says; LOCK visits no row; DROP and ALTER are DDL,
// and `ALTER TABLE … DISABLE ROW LEVEL SECURITY` is how the wall comes down
// in one statement. None of them can be bounded by a `WHERE org_id = $n`, so
// no predicate could ever make them acceptable: naming a walled table at all
// is the finding.
func destructiveRef(table string) *regexp.Regexp {
	const qualified = `(?:(?:"[A-Z_]+"|[A-Z_]+)\s*\.\s*)?"?`
	return regexp.MustCompile(
		// the table right after the verb…
		`\b(TRUNCATE|LOCK|DROP|ALTER|COPY|REINDEX|CLUSTER)\s+(?:TABLE\s+)?` +
			`(?:IF\s+EXISTS\s+)?(?:ONLY\s+)?` + qualified + table + `"?\b` +
			// …or behind an object that governs it. `DROP POLICY p ON
			// accounts` takes the wall down for good, `CREATE POLICY p ON
			// accounts USING (true)` adds a permissive one that ORs with it,
			// and a trigger on a walled table runs on every row of every
			// campaign.
			`|\b(CREATE|DROP|ALTER)\s+(?:POLICY|TRIGGER|RULE|INDEX|CONSTRAINT)\b` +
			`[^;]*?\bON\s+(?:ONLY\s+)?` + qualified + table + `"?\b` +
			// …or named as the OBJECT of a privilege change. `GRANT SELECT ON
			// accounts TO public` is one line, needs no disguise, and hands
			// the table to every role in the cluster — including whichever
			// one another campaign's deployment connects with. It bounds
			// nothing and no WHERE can follow it.
			`|\b(GRANT|REVOKE)\b[^;]*?\bON\s+(?:TABLE\s+)?(?:ALL\s+TABLES\s+IN\s+SCHEMA\s+)?` +
			`(?:ONLY\s+)?` + qualified + table + `"?\b`)
}

// schemaWide: statements that reach every walled table at once WITHOUT
// naming one. `DROP SCHEMA public CASCADE` takes them all, and a pattern
// built from a table name can never see it — there is no table name to see.
// `GRANT … ON ALL TABLES IN SCHEMA public` is the same shape for privileges.
//
// Checked once per statement rather than once per table, because what it
// matches is the absence of a table.
var schemaWide = regexp.MustCompile(
	`\b(?:DROP|ALTER|CREATE)\s+SCHEMA\b` +
		`|\bON\s+ALL\s+TABLES\s+IN\s+SCHEMA\b` +
		`|\bALTER\s+DEFAULT\s+PRIVILEGES\b`)

// Words that follow a table name without being an alias.
//
// Every one missing from this list is a FALSE POSITIVE, and in a guard that
// blocks the build a false positive costs as much as a hole: it sends the
// next author around the canary rather than through it. `FROM accounts
// NATURAL JOIN teams` read NATURAL as the alias, so the perfectly walled
// `WHERE accounts.org_id=$1` addressed a name that did not exist and the
// query was refused.
var notAnAlias = map[string]bool{
	"ON": true, "WHERE": true, "SET": true, "VALUES": true, "GROUP": true,
	"ORDER": true, "LIMIT": true, "RETURNING": true, "LEFT": true,
	"RIGHT": true, "INNER": true, "OUTER": true, "FULL": true, "CROSS": true,
	"JOIN": true, "AND": true, "OR": true, "SELECT": true, "FROM": true,
	"USING": true, "HAVING": true, "UNION": true, "EXCEPT": true,
	"INTERSECT": true, "ON CONFLICT": true, "WITH": true, "AS": true,
	// join decorations and clause openers, none of which name the table
	"NATURAL": true, "LATERAL": true, "TABLESAMPLE": true, "WINDOW": true,
	// `SELECT … FROM assignments FOR UPDATE SKIP LOCKED` — the row locking
	// this application allocates cards with
	"FOR": true, "OFFSET": true, "FETCH": true, "DO": true,
}

// Statements allowed to cross campaigns, keyed by the SHA-256 of their
// NORMALISED text — not by the function they live in. Keying on the function
// meant a second query written into it inherited the exemption in silence,
// which is exactly what the old comment promised would not happen.
var crossesCampaigns = map[string]string{
	// import.go:removeStale — the mayors row is SHARED. Deleting a target that
	// left the list must not strip another campaign of its history, so
	// "already worked on" is asked across every campaign, from the
	// maintenance scope, the only place that can.
	"7cbd5bd6f79d9c4b": "removeStale: the shared mayor list spans every campaign",
}

// Statements that stand where a table name should be unreadable, and touch
// no walled table. Keyed by the SHA-256 of their normalised text, like
// crossesCampaigns and for the same reason: keyed by function, a second
// statement written into it would inherit the promise in silence.
//
// Each is a claim that the canary cannot check, so each is a liability. An
// entry matching nothing is an error, not a leftover.
var readsNoWalledTable = map[string]string{}

// Query calls whose SQL the canary must be able to read. A call it cannot
// read is not a pass — see TestNoQueryIsInvisibleToTheCanary.
// calledName: the name a call names, whichever way it is spelt. A METHOD
// (`tx.Query`, `s.rows`) is a selector; a FREE FUNCTION (`textColumn`) is a
// bare identifier. Reading only selectors let a package query helper be
// called by its own name and carry SQL nobody resolved — invisible to the
// test that hunts unreadable queries, and invisible to the walls canary
// after it, which needs readable text before any rule can run.
//
// Named rather than inlined so the guard and the shape that pins it read the
// SAME code: a check written twice is a check that can be weakened once.
func calledName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fun.Sel.Name
	case *ast.Ident:
		return fun.Name
	}
	return ""
}

var queryCalls = map[string]bool{
	"Query": true, "QueryRow": true, "Exec": true,
	"rows": true, "column": true, "counters": true, "orderedCounters": true,
	"textColumn": true,
	// CopyFrom names its table as a pgx.Identifier, not as SQL: there is no
	// statement for any rule to read, so it streams rows into whatever table
	// it is given with nothing bounding the campaign. Declared here it can
	// never be READ, which is the point — it lands among the invisible and
	// has to be justified site by site.
	"CopyFrom": true,
}

// localScope: the string bindings as seen INSIDE one function.
//
// stringValues resolves by name across the whole package, and `sql` is a
// local in one file and a package-level binding in another. A name leaking
// across that boundary makes the canary read a query nobody wrote. So: a
// parameter, or ANY name this function assigns, stops meaning what the
// package said, whether or not the new value can be read here — `sql, _ :=
// build()` puts two names on the left, which the collector below ignores.
// localScopeVariants: one scope per BRANCH, because a query built across an
// if/else is not one query.
//
//	sql := "SELECT email FROM notes"
//	if mode == 1 { sql += " WHERE org_id=$1" } else { sql += " WHERE parent_id=$1" }
//
// Read sequentially, that text carries `ORG_ID=$1` and the reference counts
// as bounded — while the driver runs ONE of the two, and the second is the
// query the canary refuses when it is written on its own. The branches are
// mutually exclusive, so each is resolved with the OTHERS REMOVED, and every
// variant is judged. What the driver can execute, the canary must read.
//
// Only branching statements that assign are enumerated, and only one at a
// time: a query assembled across two independent conditionals would need
// their product, which no site here builds and which explodes.
func localScopeVariants(
	values map[string]string, fn *ast.FuncDecl,
) []map[string]string {
	assigns := func(n ast.Node) bool {
		found := false
		ast.Inspect(n, func(x ast.Node) bool {
			if a, ok := x.(*ast.AssignStmt); ok && len(a.Lhs) == 1 {
				if id, ok := a.Lhs[0].(*ast.Ident); ok && id.Name != "_" {
					found = true
				}
			}
			return true
		})
		return found
	}
	// the branch bodies of every conditional that assigns
	hasDefault := func(body *ast.BlockStmt) bool {
		for _, c := range body.List {
			if cc, ok := c.(*ast.CaseClause); ok && cc.List == nil {
				return true
			}
		}
		return false
	}
	type group struct {
		branches []ast.Node
		// a branching that may take NO branch at all: `if` without `else`,
		// `switch` without `default`. The base alone is then a reading the
		// driver can execute, and it is the one a wall added INSIDE the
		// branch does not cover.
		mayTakeNone bool
	}
	var groups []group
	ast.Inspect(fn, func(n ast.Node) bool {
		var branches []ast.Node
		none := false
		switch s := n.(type) {
		case *ast.IfStmt:
			branches = append(branches, s.Body)
			if s.Else != nil {
				branches = append(branches, s.Else)
			} else {
				none = true
			}
		case *ast.SwitchStmt:
			for _, c := range s.Body.List {
				branches = append(branches, c)
			}
			none = !hasDefault(s.Body)
		case *ast.TypeSwitchStmt:
			for _, c := range s.Body.List {
				branches = append(branches, c)
			}
			none = !hasDefault(s.Body)
		case *ast.SelectStmt:
			// every communication clause is a branch, and exactly one runs
			for _, c := range s.Body.List {
				branches = append(branches, c)
			}
		default:
			return true
		}
		var assigning []ast.Node
		for _, b := range branches {
			if assigns(b) {
				assigning = append(assigning, b)
			}
		}
		// ONE assigning branch is enough when the branching may take none:
		// `sql := base; if x { sql += " WHERE org_id=$1" }` read as one text
		// carries the predicate, and the driver runs `base` alone whenever x
		// is false.
		if len(assigning) > 1 || (len(assigning) == 1 && none) {
			groups = append(groups, group{assigning, none})
		}
		return true
	})
	var out []map[string]string
	for _, g := range groups {
		for _, keep := range g.branches {
			skip := map[ast.Node]bool{}
			for _, other := range g.branches {
				if other != keep {
					skip[other] = true
				}
			}
			out = append(out, localScopeSkipping(values, fn, skip))
		}
		if g.mayTakeNone {
			skip := map[ast.Node]bool{}
			for _, b := range g.branches {
				skip[b] = true
			}
			out = append(out, localScopeSkipping(values, fn, skip))
		}
	}
	return out
}

func localScope(values map[string]string, fn *ast.FuncDecl) map[string]string {
	return localScopeSkipping(values, fn, nil)
}

func localScopeSkipping(
	values map[string]string, fn *ast.FuncDecl, skip map[ast.Node]bool,
) map[string]string {
	scoped := map[string]string{}
	for k, v := range values {
		scoped[k] = v
	}
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			for _, id := range field.Names {
				delete(scoped, id.Name)
			}
		}
	}
	ast.Inspect(fn, func(n ast.Node) bool {
		if skip[n] {
			return false
		}
		if a, ok := n.(*ast.AssignStmt); ok {
			for _, lhs := range a.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
					delete(scoped, id.Name)
				}
			}
		}
		return true
	})
	// …then learn back the ones this function sets to something readable.
	//
	// Several passes, resolving against `scoped` and not against the package
	// map: since stringValues stopped learning inside function bodies, this
	// is the ONLY place a local binding is read, and one local is regularly
	// built from another — `base := "SELECT …"` then `sql := base + filter`.
	// Resolving against the package map alone left the second unreadable, and
	// an unreadable statement is one the canary passes over.
	// Every name that `+=` accumulates into. Each pass must start it from
	// nothing: the `:=` before it is replayed and resets it, but `var sql
	// string`, a parameter or a multi-name `:=` leaves nothing to replay,
	// and the text then piled up once per pass — the canary judged a
	// statement three times over and refused a legitimate query.
	accumulated := map[string]bool{}
	ast.Inspect(fn, func(n ast.Node) bool {
		if a, ok := n.(*ast.AssignStmt); ok && a.Tok.String() == "+=" &&
			len(a.Lhs) == 1 {
			if id, ok := a.Lhs[0].(*ast.Ident); ok {
				accumulated[id.Name] = true
			}
		}
		return true
	})
	for range 3 {
		// back to what the PACKAGE says, not to nothing: deleting the name
		// outright lost the base of `var pkgSQL = "…walled…"` followed by
		// `pkgSQL += " OR TRUE"`, and the canary then read the appended
		// fragment alone — no SQL verb, statement dropped, disjunction unseen.
		for name := range accumulated {
			if base, ok := values[name]; ok {
				scoped[name] = base
			} else {
				delete(scoped, name)
			}
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			if skip[n] {
				return false // a branch this variant does not take
			}
			a, ok := n.(*ast.AssignStmt)
			if !ok || len(a.Lhs) != 1 || len(a.Rhs) != 1 {
				return true
			}
			id, ok := a.Lhs[0].(*ast.Ident)
			if !ok || id.Name == "_" {
				return true
			}
			txt := sqlTextMarked(a.Rhs[0], scoped)
			if txt == "" || txt == "$?" {
				return true
			}
			if a.Tok.String() == "+=" {
				// `+=` accumulates, and what bounds it is the reset at the head
				// of each pass — ast.Inspect visits each node exactly once, so
				// nothing here can apply twice.
				//
				// Bounding by NAME instead dropped the second `+=` on one
				// variable in silence: the canary read `base+a` while the driver
				// ran `base+a+b`, and an `OR TRUE` written in the second walked
				// past `neutralised`. A per-STATEMENT map was then added on top
				// and guarded nothing at all — a defence the comment claimed and
				// the code could not perform.
				scoped[id.Name] += txt
				return true
			}
			scoped[id.Name] = txt
			return true
		})
	}
	return scoped
}

type statement struct{ File, Func, SQL string }

// sqlStatements walks the package and yields every SQL string reaching a
// call, with the file and enclosing function it is written in.
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
			// the sequential reading, PLUS one per mutually exclusive
			// branch: what the driver can execute, the canary must read
			scopes := append([]map[string]string{localScope(values, fn)},
				localScopeVariants(values, fn)...)
			seen := map[string]bool{}
			for _, scoped := range scopes {
				ast.Inspect(fn, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					for _, arg := range call.Args {
						if sqlText(arg, scoped) == "" {
							continue
						}
						sql := normaliseSQL(sqlTextMarked(arg, scoped))
						// one variant per branch means the same statement is
						// resolved several times; report each text once
						key := name + "\x00" + fn.Name.Name + "\x00" + sql
						if seen[key] {
							continue
						}
						seen[key] = true
						out = append(out, statement{name, fn.Name.Name, sql})
					}
					return true
				})
			}
		}
	}
	return out
}

// insertNaming: `INSERT INTO <table>(ORG_ID, …` — the row being written is
// stamped with the campaign, which is how a write names it.
//
// The alias is optional and it is not decoration: `INSERT INTO accounts AS a
// (org_id, email) … ON CONFLICT … DO UPDATE` is how an upsert refers to the
// existing row, and refusing it made the canary reject a write that names
// the campaign in the only place that counts.
func insertNaming(table string) *regexp.Regexp {
	return regexp.MustCompile(`INSERT INTO\s+(?:[A-Z_]+\.)?"?` + table +
		`"?\s*(?:AS\s+[A-Z][A-Z0-9_]*\s*)?\$SUB(\d+)`)
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
		!orgIDWord.MatchString(currentLevels[cols]) {
		return false
	}
	key := conflictKey.FindStringSubmatch(level)
	if key == nil {
		return true // no fallback: only the row being written is touched
	}
	i, err := strconv.Atoi(key[1])
	return err == nil && i < len(currentLevels) &&
		orgIDWord.MatchString(currentLevels[i])
}

// conflictKey: `ON CONFLICT (…)` names the columns the write may fall back
// onto. If it does not name the campaign, the DO UPDATE reaches rows of
// another one — and the SET clause is not scanned for predicates, so nothing
// else would have caught it.
var conflictKey = regexp.MustCompile(`ON CONFLICT\s*\$SUB(\d+)`)

// qualifiers: every name under which a predicate may address this table
// reference. With an alias, that alias and nothing else. Without one, either
// no qualifier at all or the table's own name — `WHERE accounts.org_id = $1`
// is how the predicate is written when no alias is declared.
// The empty qualifier counts too: `SELECT a.email FROM accounts a WHERE
// org_id=$1` is legal and correctly walled, since declaring an alias does not
// oblige the predicate to use it. In a guard that blocks the build, a false
// positive costs as much as a hole — it sends the next author around it.
func qualifiers(alias, table string, sole bool) []string {
	names := []string{table}
	if alias != "" {
		names = []string{alias}
	}
	// …but only when it can mean ONE table. With two walled tables at the
	// same level, `FROM accounts a, notes n WHERE org_id=$1` would have a
	// single unqualified predicate vouching for both, leaving whichever one
	// PostgreSQL does not resolve it to scanned unbounded.
	if sole {
		names = append(names, "")
	}
	return names
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

// inlineGroups: a parenthesised CONJUNCTION belongs to the level that wrote
// it. `WHERE (org_id=$1 AND active)` is that level's filter, but levels() had
// moved it out of sight, leaving the table with no predicate at all —
// ordinary SQL, refused.
//
// Two groups stay where they are. A SELECT, because a subquery's predicates
// constrain what IT reads, not the statement around it. And anything holding
// an OR, because flattening it CHANGES THE PRECEDENCE: `org_id=$1 AND (a OR
// b)` became `org_id=$1 AND a OR b`, which binds nothing on its second
// disjunct — refusing the card-and-notes query, walled since the first day.
// A third group stays put: one preceded by NOT. Unwrapped, `WHERE NOT
// (org_id = $1)` reads as `WHERE NOT ORG_ID = $1`, the predicate matches,
// and the canary calls walled a query that returns every OTHER campaign.
// NOT is not in its vocabulary, so the parentheses are what it must keep.
func inlineGroups(level string, depth int) string {
	if depth > 8 {
		return level
	}
	return negatedGroup.ReplaceAllStringFunc(level, func(ref string) string {
		m := negatedGroup.FindStringSubmatch(ref)
		if m[1] != "" {
			return ref // negated: the group says the opposite of what it holds
		}
		i, err := strconv.Atoi(m[2])
		if err != nil || i >= len(currentLevels) ||
			selectWord.MatchString(currentLevels[i]) ||
			orWord.MatchString(currentLevels[i]) {
			return ref
		}
		return " " + inlineGroups(currentLevels[i], depth+1) + " "
	})
}

// ancestors: the levels a correlated subquery may be bound BY — itself and
// the ones enclosing it. Pouring every level into one bag let a SIBLING
// subquery vouch for an alias it shares a name with and nothing else.
func ancestors(li int, parent []int) []int {
	out := []int{li}
	for p := parent[li]; p >= 0; p = parent[p] {
		out = append(out, p)
	}
	return out
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
	destructive := map[string]*regexp.Regexp{}
	for _, table := range tables {
		refs[table] = tableRef(table)
		destructive[table] = destructiveRef(table)
	}

	used := map[string]bool{}
	for _, st := range statements {
		print := fingerprint(st.SQL)
		if _, allowed := crossesCampaigns[print]; allowed {
			used[print] = true
			continue
		}
		where := st.File + ":" + st.Func
		// A body written in dollar quotes is stripped like any literal — but
		// PostgreSQL EXECUTES this one. `DO $$ BEGIN … END $$` reached every
		// walled table while the canary saw the four characters `DO ''`.
		// Nothing here needs procedural SQL, so the shape is refused outright
		// rather than half-read.
		if procedural.MatchString(st.SQL) {
			t.Errorf("%s: this statement carries a body the canary cannot "+
				"read — a dollar-quoted block is stripped like a literal, and "+
				"PostgreSQL runs it. Write the SQL where it can be read:"+
				"\n\t%s", where, st.SQL)
			continue
		}
		// sqlStatements yields every readable string reaching a call, which
		// is deliberate — a query hidden in a helper is still a query — but
		// it also yields error messages and log lines. The rules below judge
		// the SHAPE of a statement rather than a table name in it, so they
		// need something that is one: "columns missing from %s" carries a
		// FROM and no verb, and blaming it teaches the reader to distrust
		// the canary.
		if !sqlVerb.MatchString(st.SQL) {
			continue
		}
		// A statement that reaches every walled table at once by naming none
		// of them. No per-table pattern can see it: there is no table name
		// in it to match.
		if schemaWide.MatchString(st.SQL) {
			t.Errorf("%s: this statement acts on the SCHEMA, so it reaches "+
				"every walled table at once without naming one. No predicate "+
				"bounds it and no per-table rule can see it:\n\t%s",
				where, st.SQL)
			continue
		}
		// Where a table should stand, something the canary could not read as
		// a name. Refused rather than passed over: an unresolved reference
		// used to mean the statement carried no walled table, which is how
		// three of the four criticals of the eighth round read as compliant.
		if loc := unreadableTable.FindStringIndex(st.SQL); loc != nil {
			if _, declared := readsNoWalledTable[print]; declared {
				used[print] = true
			} else {
				t.Errorf("%s: the canary cannot read which table this "+
					"statement touches (%q). Not being able to read a "+
					"reference is not the same as there being none, and it "+
					"cannot be told whether the campaign is named. Write the "+
					"table where it can be read, or — if it touches no walled "+
					"table — declare %q in readsNoWalledTable:\n\t%s",
					where, strings.TrimSpace(st.SQL[loc[0]:loc[1]]), print, st.SQL)
				continue
			}
		}
		// One Exec can carry several statements. Without the split, the
		// first one's `org_id = $1` counted as the second one's filter.
		for _, one := range strings.Split(st.SQL, ";") {
			currentLevels = levels(one)
			// Which level encloses which, so a join chain can look for the
			// other alias's binding in the levels that ACTUALLY bind it.
			parent := make([]int, len(currentLevels))
			for i := range parent {
				parent[i] = -1
			}
			for i, l := range currentLevels {
				for _, m := range subRef.FindAllStringSubmatch(l, -1) {
					// `j < i` only: groups are numbered innermost first, so a
					// level's own marker always has a LOWER number than the
					// level holding it. Without that, a `$SUB0` written into
					// the SQL by hand — inside a quoted identifier, which
					// survives normalisation — made level 0 its own parent and
					// ancestors() walked it for ever. A hung test hangs the CI
					// job, and release.yml goes through the CI.
					if j, err := strconv.Atoi(m[1]); err == nil && j < i {
						parent[j] = i
					}
				}
			}
			levelPreds := make([][][]string, len(currentLevels))
			for i, l := range currentLevels {
				levelPreds[i] = orgPredicate.FindAllStringSubmatch(
					assignments.ReplaceAllString(inlineGroups(l, 0), " WHERE "), -1)
			}
			for li, level := range currentLevels {
				// Where this level's join chain may look for the OTHER alias's
				// own binding: here and in the levels ENCLOSING it, because a
				// correlated subquery is bound by the query around it — and
				// only those. The reference itself is still judged at its own
				// level, in its own disjunct, below.
				var chain [][]string
				for _, up := range ancestors(li, parent) {
					chain = append(chain, levelPreds[up]...)
				}
				// The branches of a set operation are independent statements
				// that happen to share a result. Judged together, one bounded
				// branch covered for an unbounded one: `SELECT … WHERE
				// org_id=$1 UNION ALL SELECT … FROM accounts` read as walled.
				for _, branch := range setOperation.Split(level, -1) {
					// The SET clause moves a row to a campaign; it never says
					// WHICH rows are touched.
					scanned := assignments.ReplaceAllString(
						inlineGroups(branch, 0), " WHERE ")
					// Every disjunct, separately, and ALL of them. `WHERE
					// org_id=$1 OR volunteer=$2` names the campaign once and
					// lets the other branch through: a filter that holds on
					// one side of an OR holds on neither.
					//
					// Checking only the disjuncts AFTER the first left the
					// first one unexamined — `WHERE TRUE OR org_id=$1` read as
					// bounded, and `neutralised` did not save it either,
					// looking only to the RIGHT of the OR. And pouring every
					// disjunct's predicates into ONE bag said the same of
					// `a.org_id=$1 OR b.org_id=$2`, where neither branch bounds
					// both tables. Hence one bag per disjunct, each standing
					// alone.
					var perDisjunct [][][]string
					for _, d := range strings.Split(scanned, " OR ") {
						perDisjunct = append(perDisjunct,
							orgPredicate.FindAllStringSubmatch(d, -1))
					}
					level := branch
					// How many walled tables share this branch: it decides whether an
					// unqualified predicate can be meant for one of them.
					walledHere := 0
					for _, table := range tables {
						walledHere += len(refs[table].FindAllStringSubmatch(branch, -1))
					}
					for _, table := range tables {
						for _, m := range destructive[table].FindAllStringSubmatch(level, -1) {
							// group 1 is the first shape's verb, group 2 the
							// second's. Reading a group the pattern does not
							// have PANICS, and a panicking guard examines
							// nothing after it — it does not even fail loudly
							// on what it already found.
							verb := m[1]
							if verb == "" {
								verb = m[2]
							}
							t.Errorf("%s: %s on %s reaches every campaign at once. "+
								"Row-level security does not cover it — a policy is "+
								"never consulted for TRUNCATE, COPY streams whatever "+
								"it is handed, LOCK visits no row, and CREATE/DROP "+
								"POLICY is the wall itself — so no predicate can "+
								"make it acceptable:\n\t%s",
								where, strings.TrimSpace(verb), table, st.SQL)
						}
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
							// An INSERT is never bounded by a WHERE: that clause
							// restricts what its SELECT source READS and says
							// nothing about which campaign the new row lands in.
							// The column list is the only thing that does, so it
							// is required and not consulted as a fallback.
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
								qualifiers(alias, table, walledHere == 1)) {
								continue
							}
							t.Errorf("%s: this reference to %s is not bounded to "+
								"the campaign (%s.org_id = $n, at its own nesting "+
								"level and outside any SET). It would serve one "+
								"campaign's work to another. "+
								"If the crossing is deliberate, add %q to "+
								"crossesCampaigns:\n\t%s",
								where, table, aliasOrTable(alias, table), print, st.SQL)
						}
					}
				}
			}
		}
	}

	// An exemption covering nothing is a stale claim, and the next query
	// written where it applied would inherit it.
	for print, why := range crossesCampaigns {
		if !used[print] {
			t.Errorf("the exemption %q (%s) matches no statement any more — "+
				"drop it", print, why)
		}
	}
	for print, why := range readsNoWalledTable {
		if !used[print] {
			t.Errorf("the declaration %q (%s) matches no statement any more — "+
				"drop it", print, why)
		}
	}
}

// A walled table the canary does not RECOGNISE is a table it does not guard,
// and it says nothing about it — the same silence as a query it cannot read.
// A spelling tableRef does not match, such as `FROM "public"."accounts"`,
// makes the statement carry no walled table at all.
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

// The canary reads SQL out of the syntax tree. Written in a shape it cannot
// resolve — a helper's return value, a %s placeholder holding the table name,
// a range over a slice of statements — a query is INVISIBLE to it, which
// would otherwise read as compliant. Silence is a failure of its own.
func TestNoQueryIsInvisibleToTheCanary(t *testing.T) {
	// Sites whose SQL cannot be resolved statically and that are known not to
	// touch a walled table. Each is a promise, and an unused one is an error.
	allowed := map[string]bool{
		// DDL built by iterating over walledTables itself: the statements
		// CREATE the policies rather than querying through them.
		"multiorg.go:orgSchema": true,
		"db.go:schema":          true,
		// Pass-through helpers: the statement is their PARAMETER, so it can
		// never be read here — it is read at the call site, which is itself
		// one of queryCalls and judged there. They looked readable only
		// because a package-level `sql` from another file leaked in; with
		// that pollution gone, the promise has to be made explicitly.
		"queries.go:rows":            true,
		"queries.go:column":          true,
		"queries.go:counters":        true,
		"queries.go:orderedCounters": true,
		"import.go:textColumn":       true,
		// Not SQL at all: valkey-go's Lua Exec, running the rate-limit
		// counter script against Valkey. No PostgreSQL driver is in reach of
		// that call, so there is no walled table it could touch.
		"limiter_valkey.go:count": true,
		// CopyFrom names its table as a pgx.Identifier: never SQL, never
		// readable. This one streams the CSV into `import_maires`, the
		// staging table the UPSERT then reads — common data, no org_id, no
		// campaign to name. It ran invisible to every guard until CopyFrom
		// was declared a query call; the promise is that this stays the
		// ONLY one, and that its target stays unwalled.
		"import.go:importList": true,
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
			scoped := localScope(values, fn)
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if !queryCalls[calledName(call)] || len(call.Args) < 2 {
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
					txt := sqlText(call.Args[i], scoped)
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
// buildsAQuery: every way this package can produce a `query` builder.
// `query{}` and `&query{}` are composite literals; `new(query)` is a call
// expression and reads nothing like them. Kept beside the test that uses it
// so a shape added here is a shape the guard sees.
func buildsAQuery(n ast.Node) bool {
	// `[]query{{…}}` and `map[string]query{…}` elide the element type on the
	// INNER literal — its Type is nil — so reading only `query{…}` saw
	// neither. The container is what names the type, and it is what is
	// matched here.
	// RECURSIVE: `[][]query{{{…}}}` and `map[K][]query{…}` nest the container,
	// so the element of the outer one is another container and not the name.
	// Matching only the immediate element saw the one-level shapes and missed
	// every deeper one.
	var namesQuery func(ast.Expr) bool
	namesQuery = func(e ast.Expr) bool {
		switch t := e.(type) {
		case *ast.Ident:
			return t.Name == "query"
		case *ast.ArrayType:
			return namesQuery(t.Elt)
		case *ast.MapType:
			return namesQuery(t.Value)
		case *ast.StarExpr:
			return namesQuery(t.X)
		}
		return false
	}
	switch e := n.(type) {
	case *ast.CompositeLit:
		switch t := e.Type.(type) {
		case *ast.Ident:
			return t.Name == "query"
		case *ast.ArrayType: // []query{…} and [N]query{…}
			return namesQuery(t.Elt)
		case *ast.MapType: // map[K]query{…}
			return namesQuery(t.Value)
		}
		return false
	case *ast.CallExpr:
		fn, ok := e.Fun.(*ast.Ident)
		if !ok || fn.Name != "new" || len(e.Args) != 1 {
			return false
		}
		arg, ok := e.Args[0].(*ast.Ident)
		return ok && arg.Name == "query"
	}
	return false
}

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
				// `query{}` and `&query{}` are composite literals; `new(query)`
				// is a CALL, and reading only the first shape let a builder be
				// made that inherits nothing from scoped(r) — its first bound
				// value becomes $1, so `WHERE org_id=$1` filters on whatever
				// the caller happened to bind first.
				if !buildsAQuery(n) {
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
