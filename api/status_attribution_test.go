package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A status is read by every team of the campaign — that is what keeps two of
// them off the same mayor now that writing one no longer claims the card —
// and it was attributable to NOBODY: `assignments.team_id` names who
// reserved, and it is null on a card that carries only a status. One team
// watched « signé » become « refusé » and could ask no one.
//
// What crosses is the TEAM. Not the address, not the name, not the note: a
// team's members are its own, and the campaign's counters have always been
// visible to all WITHOUT names. The team is the granularity that answers
// « who put that there » without breaking that.
func TestAStatusNamesTheTeamThatWroteItAndNobodyInIt(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 2, "60")
	north := createTeam(t, s, "Nord", "")
	south := createTeam(t, s, "Sud", "")
	pwN := createAccount(t, s, "n@exemple.fr", RoleVolunteer, &north)
	pwS := createAccount(t, s, "s@exemple.fr", RoleVolunteer, &south)
	cn, cs := newClient(t, srv), newClient(t, srv)
	cn.signIn("n@exemple.fr", pwN)
	cs.signIn("s@exemple.fr", pwS)

	const secret = "a dit non, très remonté contre X"
	if code, rep := cn.call(http.MethodPost, "/api/mayors/60000/status",
		map[string]string{"status": "refused", "note": secret}); code != http.StatusOK {
		t.Fatalf("status refused: %d %v", code, rep)
	}

	code, rep := cs.call(http.MethodGet, "/api/mayors/60000", nil)
	if code != http.StatusOK {
		t.Fatalf("a card carrying only a status is reserved by nobody and "+
			"must stay readable: %d %v", code, rep)
	}
	m, _ := rep["mayor"].(map[string]any)
	if got := text(m["status"]); got != "refused" {
		t.Fatalf("the other team does not even read the status: %q", got)
	}
	if got := text(m["updated_by_team_name"]); got != "Nord" {
		t.Errorf("the status is attributable to nobody: "+
			"updated_by_team_name = %q, want %q", got, "Nord")
	}

	// …and nothing of the person who wrote it. Asserted on the WHOLE answer
	// rather than on the fields this test knows: a column added to the
	// selection tomorrow is exactly how an address gets back in.
	body, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"n@exemple.fr", "Name of n@exemple.fr", secret,
	} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("the Sud team reads %q, which belongs to the Nord team",
				forbidden)
		}
	}

	// The attribution MOVES with the status. Every write after the first
	// takes the ON CONFLICT path, and an attribution the INSERT alone sets
	// names, for ever, whoever happened to touch the card first.
	if code, rep := cs.call(http.MethodPost, "/api/mayors/60000/status",
		map[string]string{
			"status": "to_call_back", "note": "", "seen": "refused",
		}); code != http.StatusOK {
		t.Fatalf("a card reserved by nobody refused a second team: %d %v",
			code, rep)
	}
	_, rep = cn.call(http.MethodGet, "/api/mayors/60000", nil)
	m, _ = rep["mayor"].(map[string]any)
	if got := text(m["updated_by_team_name"]); got != "Sud" {
		t.Errorf("the attribution stayed on the first team to write: "+
			"updated_by_team_name = %q, want %q", got, "Sud")
	}
}

// NationalTeam is zero and has no row in `teams`, so it has no name — and a
// missing name is NOT a missing answer. The two are told apart by the
// column being null or not: null is a card statused before the column
// existed, 0 is the scope every account carrying no team writes from.
func TestTheNationalScopeIsAnAnswerAndNotAnAbsence(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 2, "60")
	south := createTeam(t, s, "Sud", "")
	pwC := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	pwS := createAccount(t, s, "s@exemple.fr", RoleVolunteer, &south)
	cc, cs := newClient(t, srv), newClient(t, srv)
	cc.signIn("coord@exemple.fr", pwC)
	cs.signIn("s@exemple.fr", pwS)

	if code, rep := cc.call(http.MethodPost, "/api/mayors/60000/status",
		map[string]string{"status": "email_sent", "note": ""}); code != http.StatusOK {
		t.Fatalf("status refused: %d %v", code, rep)
	}

	_, rep := cs.call(http.MethodGet, "/api/mayors/60000", nil)
	m, _ := rep["mayor"].(map[string]any)
	// text, like every other column of a card — and « 0 » rather than absent,
	// which is what tells the national scope from a card nobody wrote on
	if got := m["updated_by_team"]; got != "0" {
		t.Errorf("a write from the national scope left no trace: "+
			"updated_by_team = %#v, want %q", got, "0")
	}
	if name := m["updated_by_team_name"]; name != nil {
		t.Errorf("the national scope has no team row, so it can carry no "+
			"name: %v", name)
	}

	// The card nobody wrote on says so, and says it differently.
	_, rep = cs.call(http.MethodGet, "/api/mayors/60001", nil)
	m, _ = rep["mayor"].(map[string]any)
	if m["updated_by_team"] != nil {
		t.Errorf("a card nobody statused claims a writer: %v",
			m["updated_by_team"])
	}
}

// The attribution rides on `mayorSelection`, which every mayor listing is
// built from — so every listing carries it, not only the card that displays
// it today. Pinned here because the reverse reasoning is the tempting one:
// « only the card shows this, so the selection does not need it ».
func TestTheListCarriesTheAttributionTheCardShows(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 2, "60")
	north := createTeam(t, s, "Nord", "")
	south := createTeam(t, s, "Sud", "")
	pwN := createAccount(t, s, "n@exemple.fr", RoleVolunteer, &north)
	pwS := createAccount(t, s, "s@exemple.fr", RoleVolunteer, &south)
	cn, cs := newClient(t, srv), newClient(t, srv)
	cn.signIn("n@exemple.fr", pwN)
	cs.signIn("s@exemple.fr", pwS)

	if code, rep := cn.call(http.MethodPost, "/api/mayors/60000/status",
		map[string]string{"status": "refused", "note": ""}); code != http.StatusOK {
		t.Fatalf("status refused: %d %v", code, rep)
	}

	code, rep := cs.call(http.MethodGet, "/api/mayors?rank=has_endorsed", nil)
	if code != http.StatusOK {
		t.Fatalf("listing: %d %v", code, rep)
	}
	rows, _ := rep["rows"].([]any)
	found := false
	for _, row := range rows {
		r, _ := row.(map[string]any)
		if text(r["insee_code"]) != "60000" {
			continue
		}
		found = true
		if got := text(r["updated_by_team_name"]); got != "Nord" {
			t.Errorf("the list shows a status it attributes to nobody: %q", got)
		}
	}
	if !found {
		t.Fatal("the statused card left the list: it is reserved by nobody " +
			"and every team must still read it")
	}
}
