package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// The IDOR canary: every route that lets a caller NAME an object must have a
// registered case where the object belongs to somebody else — another team,
// another campaign, another role's reach — and the case must show the
// refusal, then show the object untouched.
//
// The walk reads the router's own tree, so a route added tomorrow with an
// {identifier} and no declared case turns this test red by existing. That is
// the point: an authorization check is the kind of line that is only ever
// missing from the route nobody thought about.
//
// House rules apply (CLAUDE.md § guards): the RETURN CODE is asserted first
// — a 400 or a 415 writes nothing, and "the neighbour is untouched" would
// then hold for reasons unrelated to any wall — and the write cases re-read
// the database afterwards.

// idorFixture: two campaigns on one instance, two teams in the first, an
// object owned by each boundary the cases cross.
type idorFixture struct {
	s *Server

	orgA, orgB   int
	team1, team2 int
	teamB        int
	requestID    int64
	passwords    map[string]string
	// clients bound to the fixture's HTTP server
	newClientOn    func(host string) *client
	signedInClient func(t *testing.T, host, email string) *client
}

const (
	idorHostA = testSlug + ".paraphe.test"
	idorApex  = "paraphe.test"

	idorLead1 = "lead1@exemple.fr"
	idorVol1  = "vol1@exemple.fr"
	idorVol2  = "vol2@exemple.fr"
	idorCoord = "coord-a@exemple.fr"
	idorVolB  = "vol-b@exemple.fr"
	idorAdmin = "admin@exemple.fr"

	// the card team 2 owns: the object of every cross-team case
	idorOwnedCard = "02000"
)

func idorSetup(t *testing.T) *idorFixture {
	t.Helper()
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	seedMayors(t, s, 6, "01")
	seedMayors(t, s, 2, "02")

	f := &idorFixture{s: s, passwords: map[string]string{}}
	f.orgA = orgID(t, s, testSlug)
	f.orgB = createOrg(t, s, "other", "Other campaign")
	f.team1 = createTeamIn(t, s, f.orgA, "Équipe 01", "01")
	f.team2 = createTeamIn(t, s, f.orgA, "Équipe 02", "02")
	f.teamB = createTeamIn(t, s, f.orgB, "Équipe de B", "")

	f.passwords[idorLead1] = createAccountIn(t, s, f.orgA, idorLead1, RoleLead, &f.team1)
	f.passwords[idorVol1] = createAccountIn(t, s, f.orgA, idorVol1, RoleVolunteer, &f.team1)
	f.passwords[idorVol2] = createAccountIn(t, s, f.orgA, idorVol2, RoleVolunteer, &f.team2)
	f.passwords[idorCoord] = createAccountIn(t, s, f.orgA, idorCoord, RoleCoordination, nil)
	f.passwords[idorVolB] = createAccountIn(t, s, f.orgB, idorVolB, RoleVolunteer, nil)
	f.passwords[idorAdmin] = createAccountIn(t, s, OrgInstance, idorAdmin, RoleAdministration, nil)

	// team 2's work: a reserved card and a nominative note on it
	execAsMaintenance(t, s,
		"INSERT INTO assignments(org_id, insee_code, team_id, volunteer, status, updated_at) "+
			"VALUES($1,$2,$3,$4,'called','2026-01-01T00:00')",
		f.orgA, idorOwnedCard, f.team2, idorVol2)
	execAsMaintenance(t, s,
		"INSERT INTO notes(org_id, insee_code, volunteer, status, note, ts, team_id) "+
			"VALUES($1,$2,$3,'called','NOTE DE L''ÉQUIPE 02','2026-01-01T00:00',$4)",
		f.orgA, idorOwnedCard, idorVol2, f.team2)
	// campaign B's work on a card A also sees in its pool
	execAsMaintenance(t, s,
		"INSERT INTO assignments(org_id, insee_code, volunteer, status) "+
			"VALUES($1,'01001',$2,'signed')", f.orgB, idorVolB)
	// a pending hosting request: the object of the moderation cases
	asMaintenanceRow(t, s, "INSERT INTO hosting_requests(slug, name, campaign, "+
		"requester_email, requester_name, message, state, ts) "+
		"VALUES('newcampaign','New','{}'::jsonb,'req@exemple.fr','Req','', "+
		"'pending','2026-01-01T00:00') RETURNING id", &f.requestID)

	f.signedInClient = func(t *testing.T, host, email string) *client {
		t.Helper()
		c := clientOn(t, srv, host)
		if code := c.signIn(email, f.passwords[email]); code != http.StatusOK {
			t.Fatalf("sign-in as %s on %s: %d", email, host, code)
		}
		return c
	}
	f.newClientOn = func(host string) *client { return clientOn(t, srv, host) }
	return f
}

// asMaintenanceRow: one INSERT … RETURNING in the maintenance scope.
func asMaintenanceRow[T any](t *testing.T, s *Server, sql string, into *T, args ...any) {
	t.Helper()
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(), sql, args...).Scan(into); err != nil {
			t.Fatal(err)
		}
	})
}

func TestEveryRouteIdentifierHasAForeignRefusalCase(t *testing.T) {
	f := idorSetup(t)
	s := f.s

	// The cases, keyed by the route pattern they exercise. Adding a route
	// with an {identifier} and no entry here is the failure this test
	// exists to produce.
	cases := map[string][]struct {
		name string
		run  func(t *testing.T)
	}{
		"GET /api/mayors/{insee}": {
			{"another team's card is refused, not hidden", func(t *testing.T) {
				c := f.signedInClient(t, idorHostA, idorVol1)
				code, body := c.call(http.MethodGet, "/api/mayors/"+idorOwnedCard, nil)
				if code != http.StatusForbidden {
					t.Fatalf("read of another team's card: %d, want 403", code)
				}
				raw, _ := json.Marshal(body)
				if strings.Contains(string(raw), "NOTE DE L'ÉQUIPE 02") {
					t.Fatal("the refusal body carries the other team's note")
				}
			}},
			{"another campaign's work on the same card is invisible", func(t *testing.T) {
				c := f.signedInClient(t, idorHostA, idorVol1)
				code, body := c.call(http.MethodGet, "/api/mayors/01001", nil)
				if code != http.StatusOK {
					t.Fatalf("a card only the NEIGHBOUR campaign worked must look "+
						"free here: %d", code)
				}
				raw, _ := json.Marshal(body)
				if strings.Contains(string(raw), idorVolB) {
					t.Fatal("the neighbouring campaign's volunteer appears on the card")
				}
			}},
		},
		"POST /api/mayors/{insee}/status": {
			{"writing on another team's card is refused and writes nothing", func(t *testing.T) {
				c := f.signedInClient(t, idorHostA, idorVol1)
				code, _ := c.call(http.MethodPost, "/api/mayors/"+idorOwnedCard+"/status",
					map[string]string{"status": "signed", "note": "annexion"})
				if code != http.StatusForbidden {
					t.Fatalf("write on another team's card: %d, want 403", code)
				}
				owner := scalar[string](t, s, "SELECT volunteer FROM assignments "+
					"WHERE org_id=$1 AND insee_code=$2", f.orgA, idorOwnedCard)
				if owner != idorVol2 {
					t.Fatalf("the card changed hands to %q through a refused write", owner)
				}
				status := scalar[string](t, s, "SELECT status FROM assignments "+
					"WHERE org_id=$1 AND insee_code=$2", f.orgA, idorOwnedCard)
				if status != "called" {
					t.Fatalf("the card's status became %q through a refused write", status)
				}
				notes := scalar[int](t, s, "SELECT COUNT(*) FROM notes "+
					"WHERE org_id=$1 AND insee_code=$2", f.orgA, idorOwnedCard)
				if notes != 1 {
					t.Fatalf("%d notes on the card: the refused write left a trace", notes)
				}
			}},
		},
		"POST /api/team/account/{email}/active": {
			{"a lead cannot toggle an account outside their team", func(t *testing.T) {
				c := f.signedInClient(t, idorHostA, idorLead1)
				code, _ := c.call(http.MethodPost,
					"/api/team/account/"+idorVol2+"/active", map[string]any{})
				if code != http.StatusNotFound {
					t.Fatalf("toggling another team's account: %d, want 404 — a 403 "+
						"would say the address exists", code)
				}
				if !scalar[bool](t, s, "SELECT active FROM accounts WHERE org_id=$1 "+
					"AND email=$2", f.orgA, idorVol2) {
					t.Fatal("the other team's account was deactivated anyway")
				}
			}},
			{"a lead cannot toggle a privileged role", func(t *testing.T) {
				c := f.signedInClient(t, idorHostA, idorLead1)
				code, _ := c.call(http.MethodPost,
					"/api/team/account/"+idorCoord+"/active", map[string]any{})
				if code != http.StatusNotFound {
					t.Fatalf("a lead toggling coordination: %d, want 404", code)
				}
				if !scalar[bool](t, s, "SELECT active FROM accounts WHERE org_id=$1 "+
					"AND email=$2", f.orgA, idorCoord) {
					t.Fatal("coordination was deactivated by a lead")
				}
			}},
			{"coordination cannot reach another campaign's account", func(t *testing.T) {
				c := f.signedInClient(t, idorHostA, idorCoord)
				code, _ := c.call(http.MethodPost,
					"/api/team/account/"+idorVolB+"/active", map[string]any{})
				if code != http.StatusNotFound {
					t.Fatalf("toggling a neighbouring campaign's account: %d, want 404", code)
				}
				if !scalar[bool](t, s, "SELECT active FROM accounts WHERE org_id=$1 "+
					"AND email=$2", f.orgB, idorVolB) {
					t.Fatal("the neighbouring campaign's account was deactivated")
				}
			}},
		},
		"POST /api/admin/requests/{id}": {
			{"campaign coordination cannot moderate, even on the apex", func(t *testing.T) {
				c := f.signedInClient(t, idorApex, idorAdmin)
				_ = c // the admin client exists; the CASE is coordination
				coord := f.newClientOn(idorApex)
				// coordination's session belongs to campaign A: on the apex it
				// is a session for another scope, and signedIn already ends it.
				// Sign in ON the apex with A's coordination credentials to show
				// the account itself opens nothing there.
				code := coord.signIn(idorCoord, f.passwords[idorCoord])
				if code == http.StatusOK {
					t.Fatal("campaign coordination signed in on the apex: the " +
						"instance scope accepted a campaign account")
				}
				pending := scalar[string](t, s,
					"SELECT state FROM hosting_requests WHERE id=$1", f.requestID)
				if pending != RequestPending {
					t.Fatalf("the request left 'pending' (%q) with nobody "+
						"authorised having decided it", pending)
				}
			}},
			{"the administration role is inert on a campaign host", func(t *testing.T) {
				c := f.signedInClient(t, idorApex, idorAdmin)
				campaignSide := f.newClientOn(idorHostA)
				// same jar trick is not available across hosts: call the
				// campaign host with the admin's apex session absent, then
				// prove the direct decision path refuses on the campaign host
				code, _ := campaignSide.call(http.MethodPost,
					fmt.Sprintf("/api/admin/requests/%d", f.requestID),
					map[string]string{"decision": RequestRefused})
				if code != http.StatusUnauthorized {
					t.Fatalf("anonymous decision on a campaign host: %d, want 401", code)
				}
				// and with a session: the admin signs in on the campaign host —
				// which must refuse, because that account exists only in the
				// instance scope
				if code := campaignSide.signIn(idorAdmin, f.passwords[idorAdmin]); code == http.StatusOK {
					t.Fatal("the instance administrator signed in on a campaign " +
						"host: the roles crossed the scope wall")
				}
				if got := scalar[string](t, s,
					"SELECT state FROM hosting_requests WHERE id=$1", f.requestID); got != RequestPending {
					t.Fatalf("the request left 'pending' (%q)", got)
				}
				// the legitimate path still works — the guard guards, it does
				// not brick the feature (asserted last so the object stayed
				// pending for every case above)
				code, _ = c.call(http.MethodPost,
					fmt.Sprintf("/api/admin/requests/%d", f.requestID),
					map[string]string{"decision": RequestRefused, "reason": "test"})
				if code != http.StatusOK {
					t.Fatalf("the instance administrator's own decision: %d", code)
				}
			}},
		},
	}

	// Body-borne identifiers have no {parameter} for the walk to see; they
	// are declared here so the same file holds every crossing.
	bodyCases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"an account cannot be created into another campaign's team", func(t *testing.T) {
			c := f.signedInClient(t, idorHostA, idorCoord)
			code, _ := c.call(http.MethodPost, "/api/team/account", map[string]any{
				"email": "new@exemple.fr", "name": "New", "team_id": f.teamB})
			if code != http.StatusBadRequest {
				t.Fatalf("foreign team_id in the body: %d, want 400", code)
			}
			if n := scalar[int](t, s, "SELECT COUNT(*) FROM accounts WHERE "+
				"email='new@exemple.fr'"); n != 0 {
				t.Fatal("the account was created against a refused team")
			}
		}},
	}

	// The walk: collect every /api route that names an object, and demand
	// its cases exist. Executed afterwards so a missing declaration fails
	// even when every declared case passes.
	walked := 0
	err := chi.Walk(s.router(), func(method, route string, _ http.Handler,
		_ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		if !strings.HasPrefix(route, "/api") || !strings.Contains(route, "{") {
			return nil
		}
		walked++
		if len(cases[method+" "+route]) == 0 {
			t.Errorf("%s %s lets the caller name an object and has no foreign-"+
				"identifier case above: declare how it refuses somebody "+
				"else's object", method, route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if walked == 0 {
		t.Fatal("no parameterised /api route walked: the tree was not read, " +
			"and this canary would agree with anything")
	}
	for route, list := range cases {
		for _, tc := range list {
			t.Run(route+"/"+tc.name, tc.run)
		}
	}
	for _, tc := range bodyCases {
		t.Run("body/"+tc.name, tc.run)
	}
}
