package main

// The multi-coordination door: POST /api/team/account/{email}/role moves an
// existing account between the campaign roles. Pinned here: promotion works
// and UNBINDS the team, the last active coordination access cannot leave the
// role, and the door is coordination's alone. Below them, the other half of
// the pair: a public team request writes to every active coordination inbox,
// and to no other.

import (
	"net/http"
	"strings"
	"testing"
)

func TestACoordinatorPromotesAnAccountAndTheTeamUnbinds(t *testing.T) {
	s, srv := testServer(t)
	org := orgID(t, s, testSlug)
	password := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	seven := 7
	createAccount(t, s, "benevole@exemple.fr", RoleVolunteer, &seven)

	c := newClient(t, srv)
	if code := c.signIn("coord@exemple.fr", password); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	code, body := c.call(http.MethodPost,
		"/api/team/account/benevole@exemple.fr/role",
		map[string]any{"role": RoleCoordination, "team_id": 7})
	if code != http.StatusOK {
		t.Fatalf("promoting: %d %v", code, body)
	}
	if body["role"] != RoleCoordination {
		t.Fatalf("the answer says role %v, want coordination", body["role"])
	}
	if got := scalar[string](t, s, "SELECT role FROM accounts WHERE org_id=$1 "+
		"AND email=$2", org, "benevole@exemple.fr"); got != RoleCoordination {
		t.Fatalf("stored role %q, want coordination", got)
	}
	// coordination sees the whole campaign: the team the caller sent along
	// must NOT survive the promotion
	if !scalar[bool](t, s, "SELECT team_id IS NULL FROM accounts WHERE "+
		"org_id=$1 AND email=$2", org, "benevole@exemple.fr") {
		t.Fatal("a promoted coordinator kept a team: the bound would only pretend")
	}
}

func TestTheLastActiveCoordinationAccessKeepsItsRole(t *testing.T) {
	s, srv := testServer(t)
	org := orgID(t, s, testSlug)
	password := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	// an INACTIVE second coordinator must not count as a remaining one
	createAccount(t, s, "partie@exemple.fr", RoleCoordination, nil)
	execAsMaintenance(t, s,
		"UPDATE accounts SET active=FALSE WHERE org_id=$1 AND email=$2",
		org, "partie@exemple.fr")

	c := newClient(t, srv)
	if code := c.signIn("coord@exemple.fr", password); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	code, body := c.call(http.MethodPost,
		"/api/team/account/coord@exemple.fr/role",
		map[string]any{"role": RoleVolunteer})
	if code != http.StatusConflict {
		t.Fatalf("demoting the last coordinator: %d %v, want 409", code, body)
	}
	if got := scalar[string](t, s, "SELECT role FROM accounts WHERE org_id=$1 "+
		"AND email=$2", org, "coord@exemple.fr"); got != RoleCoordination {
		t.Fatalf("the refusal still changed the role to %q", got)
	}
}

func TestACoordinatorStepsDownWhenAnotherRemains(t *testing.T) {
	s, srv := testServer(t)
	org := orgID(t, s, testSlug)
	password := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	createAccount(t, s, "coord2@exemple.fr", RoleCoordination, nil)

	c := newClient(t, srv)
	if code := c.signIn("coord@exemple.fr", password); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	code, body := c.call(http.MethodPost,
		"/api/team/account/coord@exemple.fr/role",
		map[string]any{"role": RoleVolunteer})
	if code != http.StatusOK {
		t.Fatalf("stepping down with another coordinator active: %d %v", code, body)
	}
	if got := scalar[string](t, s, "SELECT role FROM accounts WHERE org_id=$1 "+
		"AND email=$2", org, "coord@exemple.fr"); got != RoleVolunteer {
		t.Fatalf("stored role %q, want volunteer", got)
	}
}

func TestTheRoleDoorRefusesWhatItMust(t *testing.T) {
	s, srv := testServer(t)
	password := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	createAccount(t, s, "coord2@exemple.fr", RoleCoordination, nil)
	leadPassword := createAccount(t, s, "chef@exemple.fr", RoleLead, nil)

	c := newClient(t, srv)
	if code := c.signIn("coord@exemple.fr", password); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}

	// a role validRole does not know — administration among them, which is
	// exactly what this door must never mint
	code, _ := c.call(http.MethodPost, "/api/team/account/coord2@exemple.fr/role",
		map[string]any{"role": RoleAdministration})
	if code != http.StatusBadRequest {
		t.Fatalf("minting administration: %d, want 400", code)
	}
	// a team no row carries
	code, _ = c.call(http.MethodPost, "/api/team/account/coord2@exemple.fr/role",
		map[string]any{"role": RoleVolunteer, "team_id": 999})
	if code != http.StatusBadRequest {
		t.Fatalf("an unknown team: %d, want 400", code)
	}
	// an address no account carries
	code, _ = c.call(http.MethodPost, "/api/team/account/personne@exemple.fr/role",
		map[string]any{"role": RoleVolunteer})
	if code != http.StatusNotFound {
		t.Fatalf("an unknown account: %d, want 404", code)
	}
	// the door is coordination's alone
	lead := newClient(t, srv)
	if code := lead.signIn("chef@exemple.fr", leadPassword); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	code, _ = lead.call(http.MethodPost, "/api/team/account/coord2@exemple.fr/role",
		map[string]any{"role": RoleVolunteer})
	if code != http.StatusForbidden {
		t.Fatalf("a lead changing roles: %d, want 403", code)
	}
}

// --- the notice a public request sends -------------------------------------

func TestATeamRequestWritesToEveryActiveCoordinationAndNobodyElse(t *testing.T) {
	s, srv := testServer(t)
	mails := withMailer(t, s, "https://campagne.exemple.fr")
	org := orgID(t, s, testSlug)
	createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	createAccount(t, s, "coord2@exemple.fr", RoleCoordination, nil)
	createAccount(t, s, "endormie@exemple.fr", RoleCoordination, nil)
	execAsMaintenance(t, s,
		"UPDATE accounts SET active=FALSE WHERE org_id=$1 AND email=$2",
		org, "endormie@exemple.fr")
	createAccount(t, s, "chef@exemple.fr", RoleLead, nil)

	code, body := newClient(t, srv).call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe des Landes"))
	if code != http.StatusCreated {
		t.Fatalf("the public form: %d %v", code, body)
	}
	s.outbound.Wait()

	sent := mails.all()
	got := map[string]bool{}
	for _, m := range sent {
		got[m.to] = true
		if m.subject != teamRequestSubject {
			t.Errorf("subject %q, want the constant one — visitor text belongs "+
				"in no header", m.subject)
		}
		for _, needle := range []string{"Équipe des Landes",
			"Personne qui demande", requesterAddress,
			"https://campagne.exemple.fr"} {
			if !strings.Contains(m.body, needle) {
				t.Errorf("the notice to %s does not carry %q:\n%s", m.to, needle, m.body)
			}
		}
	}
	if len(sent) != 2 || !got["coord@exemple.fr"] || !got["coord2@exemple.fr"] {
		t.Fatalf("the notice went to %v: want exactly the two ACTIVE "+
			"coordination accesses", got)
	}
}

func TestARelaylessInstanceStillTakesTeamRequests(t *testing.T) {
	s, srv := testServer(t)
	org := orgID(t, s, testSlug)
	createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)

	code, body := newClient(t, srv).call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe sans relais"))
	if code != http.StatusCreated {
		t.Fatalf("without a relay: %d %v", code, body)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM team_requests WHERE "+
		"org_id=$1 AND state='pending'", org); n != 1 {
		t.Fatalf("%d pending requests, want 1", n)
	}
}

func TestARelayFailureLeavesTheRequestInTheQueue(t *testing.T) {
	s, srv := testServer(t)
	mails := withMailer(t, s, "https://campagne.exemple.fr")
	mails.fail = errNoRelay
	org := orgID(t, s, testSlug)
	createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)

	code, body := newClient(t, srv).call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe malgré tout"))
	if code != http.StatusCreated {
		t.Fatalf("a dead relay must not refuse the request: %d %v", code, body)
	}
	s.outbound.Wait()
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM team_requests WHERE "+
		"org_id=$1 AND state='pending'", org); n != 1 {
		t.Fatalf("%d pending requests, want 1", n)
	}
}
