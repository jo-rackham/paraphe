package main

import (
	"go/ast"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// THE CARD CROSSES, THE PERSON DOES NOT — kept by something, not by memory.
//
// `hidePerson` states the rule; three call sites applied it. A fourth card
// query — a leaderboard, a « recently statused » tab, an admin view — would
// have selected `mayorSelection`, handed the rows to `replyJSON`, and shipped
// every other team's volunteer addresses with the whole suite green. That is
// the failure the team wall used to have, arriving through the door that
// replaced it.
//
// So the rule has a reader, and it reads what the DRIVER runs.
//
// Its first shape walked the identifier `mayorSelection` and accepted any
// identifier named `hidePerson` or `cards`. Both halves were one line from
// useless, and an adversarial round walked past each in a minute:
//
//	const cardColumns = mayorSelection    // the marker, renamed away
//	cards, err := s.rows(r, …)            // a LOCAL named cards, and the
//	                                      // guard reads itself satisfied
//
// So: the marker is the SQL a query call actually RUNS, resolved by the same
// reader the isolation canary uses — which follows a const alias — and the
// requirement is a CALL, not a name. `personColumn` matches the person's
// columns where they are SELECTED, never where they are tested: the join says
// `= t.volunteer AND`, `mayorAvailable` says `t.volunteer IS NULL`, and the
// dashboard's per-volunteer counter says `COALESCE(c.name, t.volunteer) AS
// who` — bounded to the reader's own team, and naming nobody else. A canary
// that cried on those is one the next author would route around.
var personColumn = regexp.MustCompile(
	`AS VOLUNTEER_NAME|T\.VOLUNTEER\s*(,|AS\b|FROM\b)`)

func TestEveryCardQueryMasksThePerson(t *testing.T) {
	files := apiPackage(t)
	values := stringValues(files)
	seen := 0
	for name, file := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			scoped := localScope(values, fn)
			reads := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !queryCalls[calledName(call)] {
					return true
				}
				for _, arg := range call.Args {
					if personColumn.MatchString(
						strings.ToUpper(sqlText(arg, scoped))) {
						reads = true
					}
				}
				return true
			})
			if !reads {
				continue
			}
			seen++
			if !masksThePerson(fn) {
				t.Errorf("%s:%s runs a statement selecting the person on a "+
					"card and neither goes through s.cards nor calls "+
					"hidePerson: every other team's volunteer addresses go "+
					"out with them", name, fn.Name.Name)
			}
		}
	}
	// A walk that matched nothing would pass in silence, which is the one
	// result this test must never produce. Four sites read the person today:
	// the dashboard's own cards, the list, one card, and the export.
	if seen < 4 {
		t.Fatalf("only %d card queries walked: the reader found nothing to "+
			"judge, so its silence means nothing", seen)
	}
}

// masksThePerson: a CALL to `hidePerson`, or to the `cards` METHOD. A name is
// not a call and a local is not a method — that distinction is the whole
// difference between a guard and a variable named after one.
func masksThePerson(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if fun.Name == "hidePerson" {
				found = true
			}
		case *ast.SelectorExpr:
			if fun.Sel.Name == "cards" {
				found = true
			}
		}
		return true
	})
	return found
}

// And the rule itself, driven rather than read: a card another team is
// working comes back without the person and with the team. `hidePerson` is
// pure, so this needs no database.
func TestHidePersonKeepsTheTeamAndDropsThePerson(t *testing.T) {
	team := func(id int) *Account {
		return &Account{Role: RoleVolunteer, TeamID: &id}
	}
	card := func(owner any) map[string]any {
		return map[string]any{
			"team_id": owner, "volunteer": "qui@exemple.fr",
			"volunteer_name": "Qui", "team_name": "Nord",
		}
	}

	other := hidePerson(team(2), card(7))
	if other["volunteer"] != nil || other["volunteer_name"] != nil {
		t.Errorf("another team's card names the person: %v", other)
	}
	if other["team_name"] != "Nord" {
		t.Error("the team went with the person: nothing then says the card is " +
			"being worked, which is the whole replacement for the old wall")
	}

	mine := hidePerson(team(7), card(7))
	if mine["volunteer"] != "qui@exemple.fr" {
		t.Errorf("my own team's card lost its person: %v", mine)
	}

	free := hidePerson(team(2), map[string]any{"team_id": nil})
	if _, hidden := free["volunteer"]; hidden {
		t.Error("a card nobody is on was masked")
	}

	// The national scope is team 0 — a real scope with no row in `teams`, and
	// the value a truthiness test reads as « nobody ».
	national := hidePerson(team(2), card(0))
	if national["volunteer"] != nil {
		t.Error("the national scope's card handed its volunteer to another team")
	}

	// Coordination sees everything, as it does everywhere else.
	coord := hidePerson(&Account{Role: RoleCoordination}, card(7))
	if coord["volunteer"] != "qui@exemple.fr" {
		t.Errorf("coordination cannot see its own campaign's work: %v", coord)
	}
}

// The export drives the rule end to end. The walk above proves it CALLS
// hidePerson; this proves the bytes that leave are masked — the export is the
// one reader that streams positionally, so what it hands the rule and what it
// writes back are two more places to be wrong.
func TestTheExportMasksThePersonToo(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 2, "04")
	north := createTeam(t, s, "Nord", "04")
	south := createTeam(t, s, "Sud", "04")
	pwN := createAccount(t, s, "n@exemple.fr", RoleVolunteer, &north)
	pwS := createAccount(t, s, "s@exemple.fr", RoleVolunteer, &south)
	cn, cs := newClient(t, srv), newClient(t, srv)
	cn.signIn("n@exemple.fr", pwN)
	cs.signIn("s@exemple.fr", pwS)
	if code, _ := cn.call(http.MethodPost, "/api/batch", map[string]any{}); code !=
		http.StatusOK {
		t.Fatalf("batch: %d", code)
	}

	resp, err := cs.http.Do(cs.request(http.MethodGet, "/api/export.csv", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := make([]byte, 1<<20)
	n, _ := resp.Body.Read(body)
	csv := string(body[:n])
	if strings.Contains(csv, "n@exemple.fr") {
		t.Error("the export names the other team's volunteer")
	}
	if !strings.Contains(csv, "Nord") {
		t.Error("the export does not say which team is on those cards")
	}
}
