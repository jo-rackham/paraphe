package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A team's departments bound what it DRAWS and what it WRITES ON. They do not
// bound what it reads: the whole country is on every screen, whatever the
// perimeter — which is what lets a volunteer look up a commune somebody
// mentioned, see that a neighbouring department is uncovered, and say so.
//
// Nothing held that. Adding `m.department = ANY(mine)` to the list query —
// for symmetry with /batch, which is exactly how it would be argued — is one
// line that nothing else refuses. Both halves are pinned here: widening
// everything passes the read half and fails the write half, narrowing
// everything does the reverse.

const (
	perimeterTeamDept = "01"
	perimeterOutside  = "02"
)

// perimeterSetup: a campaign with mayors in two departments, and a volunteer
// whose team covers only the first.
func perimeterSetup(t *testing.T) (*Server, *client) {
	t.Helper()
	s, srv := testServer(t)
	seedMayors(t, s, 3, perimeterTeamDept)
	seedMayors(t, s, 3, perimeterOutside)
	team := createTeam(t, s, "Équipe 01", perimeterTeamDept)
	password := createAccountIn(t, s, orgID(t, s, testSlug),
		"benevole@exemple.fr", RoleVolunteer, &team)
	c := newClient(t, srv)
	if code := c.signIn("benevole@exemple.fr", password); code != http.StatusOK {
		t.Fatalf("volunteer sign-in: %d", code)
	}
	return s, c
}

func TestATeamReadsTheWholeCountryWhateverItsPerimeter(t *testing.T) {
	_, c := perimeterSetup(t)

	code, body := c.call(http.MethodGet, "/api/mayors?rank=has_endorsed", nil)
	if code != http.StatusOK {
		t.Fatalf("listing: %d", code)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	// a commune of the department the team does NOT cover
	if !strings.Contains(string(raw), "Commune "+perimeterOutside+"000") {
		t.Fatalf("department %s is absent from the list: the perimeter has "+
			"started bounding what the team READS", perimeterOutside)
	}

	// and the card itself opens, which is what "access" means on this screen
	if code, _ := c.call(http.MethodGet,
		"/api/mayors/"+perimeterOutside+"000", nil); code != http.StatusOK {
		t.Fatalf("opening a card outside the perimeter: %d, want 200", code)
	}

	// the filter offers it too: a list that carries the department behind a
	// dropdown that does not is a list nobody can reach
	code, facets := c.call(http.MethodGet, "/api/facets", nil)
	if code != http.StatusOK {
		t.Fatalf("facets: %d", code)
	}
	depts, _ := facets["departments"].([]any)
	found := false
	for _, d := range depts {
		if s, _ := d.(string); s == perimeterOutside {
			found = true
		}
	}
	if !found {
		t.Fatalf("department %s is missing from /api/facets (%v): the filter "+
			"cannot reach what the list carries", perimeterOutside, depts)
	}
}

// A card the NATIONAL SCOPE has taken is one no team has a name for: team 0
// is a real scope, held by coordination and by every account without a team,
// and it has no row in `teams` — so `team_name` comes back NULL exactly as it
// does for a card nobody is on. Read through the name alone, a batch the
// coordination had taken showed as « personne n'est encore dessus » on every
// local team's screen, and the volunteer who worked it was the second caller.
// `taken_by` is the answer, and it travels as TEXT for the same reason
// `updated_by_team` does: `0` is falsy on the other side.
func TestANationalScopeCardDoesNotReadAsFree(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 2, perimeterTeamDept)
	// coordination carries no team, hence the national scope
	pwC := createAccountIn(t, s, orgID(t, s, testSlug), "coord@exemple.fr",
		RoleCoordination, nil)
	cc := newClient(t, srv)
	cc.signIn("coord@exemple.fr", pwC)
	if code, rep := cc.call(http.MethodPost, "/api/batch",
		map[string]any{}); code != http.StatusOK {
		t.Fatalf("batch: %d %v", code, rep)
	}
	taken := scalar[string](t, s,
		"SELECT insee_code FROM assignments WHERE org_id=$1 AND volunteer=$2 "+
			"LIMIT 1", orgID(t, s, testSlug), "coord@exemple.fr")

	team := createTeam(t, s, "Équipe locale", perimeterTeamDept)
	pwV := createAccountIn(t, s, orgID(t, s, testSlug), "benevole@exemple.fr",
		RoleVolunteer, &team)
	cv := newClient(t, srv)
	cv.signIn("benevole@exemple.fr", pwV)

	code, rep := cv.call(http.MethodGet, "/api/mayors/"+taken, nil)
	if code != http.StatusOK {
		t.Fatalf("opening the card: %d", code)
	}
	card, _ := rep["mayor"].(map[string]any)
	if card["taken_by"] != "0" {
		t.Fatalf("taken_by is %v, want \"0\": the screen cannot tell a card "+
			"the national scope holds from one nobody is on", card["taken_by"])
	}
	// and the person is still masked, national scope or not
	if card["volunteer"] != nil {
		t.Errorf("the national scope's card names its volunteer: %v", card["volunteer"])
	}
}

// The other half, in the same file so that widening one cannot quietly widen
// the other: the perimeter still bounds the ONE act it exists to bound, which
// is where a team is handed its work. It bounds no reading and no recording —
// `TestAPerimeterBoundsTheDrawAndNotTheRecord` holds that end.
func TestAPerimeterStillBoundsTheDraw(t *testing.T) {
	s, c := perimeterSetup(t)

	if code, _ := c.call(http.MethodPost, "/api/batch", map[string]any{}); code !=
		http.StatusOK {
		t.Fatalf("drawing a batch: %d", code)
	}
	// everything drawn is inside the perimeter, whatever the scores say —
	// the outside department was seeded with the same ones
	outside := scalar[int](t, s,
		"SELECT COUNT(*) FROM assignments t JOIN mayors m ON m.insee_code=t.insee_code "+
			"WHERE t.org_id=$1 AND t.volunteer=$2 AND m.department=$3",
		orgID(t, s, testSlug), "benevole@exemple.fr", perimeterOutside)
	if outside != 0 {
		t.Fatalf("%d card(s) drawn outside the team's perimeter", outside)
	}
}
