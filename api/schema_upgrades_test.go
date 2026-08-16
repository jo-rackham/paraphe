package main

// Every column added to a table that existed at the first release must
// exist TWICE: in the CREATE TABLE for fresh databases, and as an
// `ALTER TABLE … ADD COLUMN IF NOT EXISTS` for the databases that predate
// it. `CREATE TABLE IF NOT EXISTS` is a NO-OP on the table an upgrading
// instance already has — orgs.logo_key once shipped in the CREATE alone,
// and every campaign's /api/config on a pre-existing database answered 500
// the moment the release landed. No suite sees that class on its own:
// tests always start from a fresh schema.
//
// The founding lists are FROZEN at v0.1.0. Do not extend them when adding
// a column — add the ALTER instead. A table absent from the map postdates
// the first release: its CREATE reaches every upgrader, no ALTER needed.
// `mayors` is exempt: its columns follow the imported CSV, and the import
// path issues its own ALTERs at runtime (db.go).

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

var foundingColumns = map[string][]string{
	"orgs":             {"id", "slug", "name", "campaign", "batch_size", "state", "created_at"},
	"hosting_requests": {"id", "slug", "name", "campaign", "requester_email", "requester_name", "message", "state", "reason", "ts", "decided_at", "decided_by"},
	"assignments":      {"org_id", "insee_code", "team_id", "volunteer", "status", "updated_at"},
	"settings":         {"key", "value"},
	"notes":            {"id", "org_id", "insee_code", "volunteer", "status", "note", "ts", "team_id"},
	"teams":            {"id", "org_id", "name", "departments", "created_at"},
	"accounts":         {"org_id", "email", "name", "password_hash", "role", "team_id", "active", "personal_note", "created_at", "created_by"},
}

// the two files that own CREATE TABLE statements
var schemaSources = []string{"db.go", "multiorg.go"}

var (
	createRe = regexp.MustCompile("CREATE TABLE IF NOT EXISTS (\\w+)\\(([^`]*?)\\)`")
	// no line anchor: `ts TEXT, decided_at TEXT, decided_by TEXT` declares
	// three columns on one line, and an anchored pattern reads only the first
	columnRe = regexp.MustCompile(`\b([a-z_]+)\s+(?:TEXT|INTEGER|BIGINT|BOOLEAN|JSONB|%s)\b`)
	alterRe  = regexp.MustCompile(`ALTER TABLE (\w+) ADD COLUMN IF NOT EXISTS (\w+)`)
)

func readSchemaSources(t *testing.T) string {
	t.Helper()
	var src string
	for _, f := range schemaSources {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		src += string(raw)
	}
	return src
}

func TestEveryColumnYoungerThanTheFirstReleaseHasItsAlter(t *testing.T) {
	src := readSchemaSources(t)

	alters := map[string][]string{}
	for _, m := range alterRe.FindAllStringSubmatch(src, -1) {
		alters[m[1]] = append(alters[m[1]], m[2])
	}

	seen := map[string]bool{}
	for _, m := range createRe.FindAllStringSubmatch(src, -1) {
		table, body := m[1], m[2]
		seen[table] = true
		if table == "mayors" {
			continue // dynamic: columns follow the CSV, ALTERed at runtime
		}
		founding, existedAtFirstRelease := foundingColumns[table]
		if !existedAtFirstRelease {
			continue // the CREATE itself reaches every upgrader
		}
		for _, col := range columnRe.FindAllStringSubmatch(body, -1) {
			name := col[1]
			if slices.Contains(founding, name) {
				continue
			}
			if !slices.Contains(alters[table], name) {
				t.Errorf("%s.%s is younger than v0.1.0 and has no "+
					"`ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s`: a fresh "+
					"database gets it from the CREATE, the database an "+
					"upgrading instance already has never does",
					table, name, table, name)
			}
		}
		// The reverse guard: a founding column missing from the CREATE is a
		// rename or a drop, which no ALTER IF NOT EXISTS repairs — it needs
		// a deliberate migration, starting with this list.
		current := []string{}
		for _, col := range columnRe.FindAllStringSubmatch(body, -1) {
			current = append(current, col[1])
		}
		for _, name := range founding {
			if !slices.Contains(current, name) {
				t.Errorf("%s.%s existed at v0.1.0 and left the CREATE: an "+
					"upgraded database still has it, a fresh one no longer "+
					"does, and the two schemas silently diverge", table, name)
			}
		}
	}

	// every founding table must still be created — same divergence otherwise
	for table := range foundingColumns {
		if !seen[table] {
			t.Errorf("table %s existed at v0.1.0 and has no CREATE anymore", table)
		}
	}
}
