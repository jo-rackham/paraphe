package main

import "testing"

// csvSafe disarms a spreadsheet formula that a lead could plant in a volunteer
// name: the export is opened by coordination, and =HYPERLINK/=WEBSERVICE run on
// open. Every cell whose first byte is =, +, -, @ (or a leading tab/CR) is
// prefixed; ordinary data is untouched.
func TestCsvSafeDisarmsFormulas(t *testing.T) {
	dangerous := []string{
		`=HYPERLINK("http://evil","x")`,
		`+1+2`,
		`-2+3`,
		`@SUM(A1:A9)`,
		"\ttab-led",
		"\rcr-led",
	}
	for _, in := range dangerous {
		if got := csvSafe(in); got != "'"+in {
			t.Errorf("csvSafe(%q) = %q, want a leading apostrophe", in, got)
		}
	}
	// legitimate data must pass through byte for byte
	for _, in := range []string{"", "Rieux-en-Val", "M. Xavier BEDOS",
		"mairie@example.fr", "04 68 24 08 62", "0"} {
		if got := csvSafe(in); got != in {
			t.Errorf("csvSafe(%q) altered legitimate data to %q", in, got)
		}
	}
}
