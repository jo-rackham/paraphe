package main

import (
	"fmt"
	"strconv"
	"strings"
)

// normalizeEmail: addresses are stored and compared lowercase, trimmed —
// one form everywhere, or a volunteer signing in as Jo@… would not match
// the account their lead created as jo@….
func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// storableEmail: an address this application will still be able to USE.
//
// normalizeEmail only trims the edges, so a CR or an LF in the middle
// survives it, and `strings.Contains(email, "@")` passes. The row was then
// written and the account was broken for good: safeAddress refuses that
// address at send time — rightly, it is how a header gets a second recipient
// — so no invitation and no sign-in link could ever reach it, and the
// address is the primary key, so it cannot be corrected either.
//
// Checked where the row is WRITTEN, which is the only place the mistake can
// still be told to whoever is making it.
func storableEmail(email string) bool {
	return strings.Contains(email, "@") && safeAddress(email) == nil
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
