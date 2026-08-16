package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// normalizeEmail: addresses are stored and compared lowercase, trimmed —
// one form everywhere, or a volunteer signing in as Jo@… would not match
// the account their lead created as jo@….
func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// splitDepartments: a `;`-joined perimeter as a list. Empty means the whole
// country, which is what a team with no departments draws from.
//
// One reader for one writer: `strings.Join(list, ";")` writes this column in
// `teams` and in `team_requests`, and every place that reads it back comes
// here. Two copies of the loop drift the day one of them starts trimming.
func splitDepartments(raw string) []string {
	out := []string{}
	for _, d := range strings.Split(raw, ";") {
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

// departmentLabels: the departments the COMMON mayor list carries, in the
// order every screen shows them.
//
// Read by four routes — the configuration the sign-in screen loads, the
// facets, the team screen and the perimeter check of a team request — and
// written by none: `mayors` is public, identical for every campaign, so
// there is no campaign to name here.
//
// It costs a sequential pass over 34 826 rows (measured: 1 152 buffers,
// 4.5 ms warm) to answer with ~102 labels, and /api/config runs it on every
// page load. An index on `department` does NOT fix that: PostgreSQL 17 has
// no skip scan for DISTINCT and picks the sequential scan anyway — forced
// onto the index it reads 33 buffers instead of 1 152, so the index would
// pay for itself only if the planner ever chose it. The real fix is to hold
// the list in the process: `mayors` is written once, by the startup import.
// One reader here is what makes that a one-line change.
func (s *Server) departmentLabels(r *http.Request) ([]string, error) {
	return s.column(r, "SELECT DISTINCT department FROM mayors "+
		"ORDER BY department")
}

// text: CSV rendering of a value coming from PostgreSQL. NULL is written
// empty, not "<nil>" — volunteers open this file in a spreadsheet.
func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprint(x)
	}
}

// csvSafe neutralises a spreadsheet formula in a CSV cell. Excel and
// LibreOffice evaluate any cell whose first character is one of = + - @ (or a
// leading tab/CR), so a volunteer name a lead chose — "=HYPERLINK(...)",
// "=WEBSERVICE(...)" — runs as a formula when coordination opens the export.
// A leading apostrophe is the tools' own "this is text" marker and disarms it.
// Applied to every exported cell: the mayor list is public, but the work
// columns carry text a lead types.
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// integer: reads a nullable INTEGER as returned by pgx. The second return
// value tells 0 (the national team) apart from NULL (unassigned card) —
// conflating them would reopen the shared pool to cards already taken.
func integer(v any) (int, bool) {
	switch x := v.(type) {
	case nil:
		return 0, false
	case int16:
		return int(x), true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case int:
		return x, true
	}
	return 0, false
}
