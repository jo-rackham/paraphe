package main

import (
	"fmt"
	"net/http"
	"testing"
)

// Every door that writes a team or a role reads it the same way.
//
// Four of them disagreed, and each disagreement was a defect of its own.
// Coordination's own door — the one most used — never checked the perimeter
// against the mayor list, while the public form and the acceptance both did:
// a label no mayor bears is a team that draws zero cards for ever, and
// nothing downstream says why. Accepting a request checked the perimeter
// only when coordination EDITED it, so a row filed before the guard existed
// opened a dead team on a screen that said nothing. A lead could be created
// with no team at all, and MyTeam() answers NationalTeam — zero — for such an
// account, so two of them shared every lead-side filter and each could
// deactivate the other's volunteers. And a coordination account could carry a
// team, which routeChangeRole strips, so the lead of that team read a
// coordinator as one of their own members.
func TestEveryDoorThatWritesATeamReadsItTheSameWay(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)
	pw := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn("coord@exemple.fr", pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}

	// the perimeter, at coordination's own door
	if code, rep := c.call(http.MethodPost, "/api/team/group", map[string]any{
		"name": "Équipe fantôme", "departments": []string{"Nord-Pas-de-Calais"},
	}); code != http.StatusBadRequest {
		t.Errorf("a perimeter no mayor bears opened a team (%d %v): it draws "+
			"zero cards for ever and nothing says why", code, rep)
	}

	// the perimeter STORED on a request, accepted without an edit
	execAsMaintenance(t, s,
		"INSERT INTO team_requests(org_id, name, departments, requester_email, "+
			"requester_name, message, state, ts) "+
			"VALUES($1,'Équipe héritée','Nord-Pas-de-Calais','qui@exemple.fr',"+
			"'Qui','','pending','2026-01-01T00:00')", org)
	id := scalar[int64](t, s,
		"SELECT id FROM team_requests WHERE org_id=$1 ORDER BY id DESC LIMIT 1", org)
	if code, rep := c.call(http.MethodPost,
		fmt.Sprintf("/api/team/requests/%d", id),
		map[string]any{"decision": RequestAccepted}); code != http.StatusBadRequest {
		t.Errorf("a stored perimeter no mayor bears was accepted (%d %v)", code, rep)
	}

	// a lead without a team: every lead-side filter would read team_id=0
	if code, rep := c.call(http.MethodPost, "/api/team/account", map[string]any{
		"email": "referent@exemple.fr", "name": "Referent", "role": RoleLead,
	}); code != http.StatusBadRequest {
		t.Errorf("a lead was opened with no team (%d %v): two of them share "+
			"the national scope, and each can deactivate the other's "+
			"volunteers", code, rep)
	}

	// a coordination account carries no team, whichever door writes it
	team := createTeam(t, s, "Ain", "01")
	if code, rep := c.call(http.MethodPost, "/api/team/account", map[string]any{
		"email": "coord2@exemple.fr", "name": "Coord Deux",
		"role": RoleCoordination, "team_id": team,
	}); code != http.StatusCreated {
		t.Fatalf("creating a coordinator: %d %v", code, rep)
	}
	if got := scalar[*int](t, s,
		"SELECT team_id FROM accounts WHERE org_id=$1 AND email=$2",
		org, "coord2@exemple.fr"); got != nil {
		t.Errorf("a coordinator carries team_id=%d: the lead of that team "+
			"reads them as one of their own members, and routeChangeRole "+
			"strips it — the two doors must not disagree", *got)
	}
}
