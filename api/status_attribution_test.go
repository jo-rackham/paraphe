package main

import (
	"net/http"
	"testing"
)

// A status is read by every team; it must be attributable to somebody.
//
// Writing one stopped claiming the card, which is what makes it readable
// campaign-wide — and left it signed by nobody: `volunteer` is null on a card
// no one reserved, and the note that explains the status is filtered to the
// team that wrote it. Team A watched « signé » become « refusé » and could
// ask no one. `updated_by` answers that, and grants nothing: it is not
// ownership, it locks nothing, and the card stays free.
func TestAStatusSaysWhoWroteIt(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 2, "02")
	north := createTeam(t, s, "Nord", "02")
	south := createTeam(t, s, "Sud", "02")
	pwN := createAccount(t, s, "nord@exemple.fr", RoleVolunteer, &north)
	pwS := createAccount(t, s, "sud@exemple.fr", RoleVolunteer, &south)
	cn, cs := newClient(t, srv), newClient(t, srv)
	cn.signIn("nord@exemple.fr", pwN)
	cs.signIn("sud@exemple.fr", pwS)

	if code, rep := cn.call(http.MethodPost, "/api/mayors/02000/status",
		map[string]string{"status": "signed", "note": "a signé, très clair"}); code != http.StatusOK {
		t.Fatalf("status: %d %v", code, rep)
	}

	// the other team reads the status — that is the point of it travelling —
	// and now reads WHO
	code, rep := cs.call(http.MethodGet, "/api/mayors/02000", nil)
	if code != http.StatusOK {
		t.Fatalf("card: %d %v", code, rep)
	}
	card, _ := rep["mayor"].(map[string]any)
	if card["status"] != "signed" {
		t.Fatalf("the other team does not read the status: %v", card["status"])
	}
	if card["updated_by"] != "nord@exemple.fr" {
		t.Errorf("the status is signed by nobody (%v): the card carries no "+
			"volunteer and the note behind it belongs to another team, so "+
			"there is no one left to ask", card["updated_by"])
	}
	// …and it is attribution, not ownership: the card is still free, and the
	// note stays with the team that wrote it
	if card["volunteer"] != nil {
		t.Errorf("naming the writer took the card: volunteer=%v", card["volunteer"])
	}
	if notes, _ := rep["notes"].([]any); len(notes) != 0 {
		t.Errorf("the other team reads %d note(s) of the first one", len(notes))
	}
}
