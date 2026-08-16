package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Asking a campaign to open a local team, and the coordination deciding.
//
// The isolation of these routes is proved elsewhere (walls_test.go,
// idor_test.go). What is proved here is that the door works and that the
// public side of it creates nothing.

const requesterAddress = "candidate-lead@exemple.fr"

func teamRequestBody(name string, departments ...string) map[string]any {
	if departments == nil {
		departments = []string{}
	}
	return map[string]any{
		"name": name, "departments": departments,
		"requester_name": "Personne qui demande", "requester_email": requesterAddress,
		"message": "Nous sommes cinq et nous couvrons le département.",
	}
}

// The public side: a request writes ONE row and opens nothing. Asserted
// against the tables an acceptance would write, because "it answered 201" is
// exactly what a handler creating a team early would also answer.
func TestAPublicTeamRequestCreatesNothingByItself(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)

	code, body := newClient(t, srv).call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe du Nord", "01"))
	if code != http.StatusCreated {
		t.Fatalf("the public form: %d %v", code, body)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM team_requests WHERE "+
		"org_id=$1 AND state='pending'", org); n != 1 {
		t.Fatalf("%d pending requests, want 1", n)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM teams WHERE org_id=$1", org); n != 0 {
		t.Fatalf("the form opened %d team(s): a request must create nothing", n)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM accounts WHERE org_id=$1 "+
		"AND email=$2", org, requesterAddress); n != 0 {
		t.Fatal("the form opened an account: a request must create nothing")
	}
}

// A perimeter of labels no mayor bears is a team that draws zero cards for
// ever, and nothing downstream would ever say why.
func TestATeamRequestRefusesADepartmentTheListDoesNotCarry(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)

	code, body := newClient(t, srv).call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe fantôme", "01", "Nord-Pas-de-Calais"))
	if code != http.StatusBadRequest {
		t.Fatalf("an unknown department: %d %v, want 400", code, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "Nord-Pas-de-Calais") {
		t.Errorf("the refusal does not name the department it refused: %q", msg)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM team_requests WHERE org_id=$1",
		org); n != 0 {
		t.Fatal("the refused request was written anyway")
	}
}

// Both early refusals, and the ceiling on what one address can leave behind.
func TestATeamRequestRefusesANameAlreadySpokenFor(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)
	createTeamIn(t, s, org, "Équipe qui existe", "01")
	c := newClient(t, srv)

	if code, body := c.call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe qui existe", "01")); code != http.StatusConflict {
		t.Fatalf("a name an existing team holds: %d %v, want 409", code, body)
	}
	if code, body := c.call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe du Sud", "01")); code != http.StatusCreated {
		t.Fatalf("the first request: %d %v", code, body)
	}
	if code, body := c.call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe du Sud", "01")); code != http.StatusConflict {
		t.Fatalf("a name already pending: %d %v, want 409", code, body)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM team_requests WHERE org_id=$1",
		org); n != 1 {
		t.Fatalf("%d requests written for one accepted intent", n)
	}
}

// The name and the address become btree-indexed keys the day the request is
// accepted: unbounded, an anonymous form files a request no coordination can
// ever accept, and which holds the name pending for good.
func TestATeamRequestBoundsWhatAnAnonymousFormCanWrite(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)
	c := newClient(t, srv)

	long := teamRequestBody(strings.Repeat("é", maxNameRunes+1), "01")
	if code, _ := c.call(http.MethodPost, "/api/team/request", long); code !=
		http.StatusBadRequest {
		t.Errorf("a name of %d runes: %d, want 400", maxNameRunes+1, code)
	}
	address := teamRequestBody("Équipe longue", "01")
	address["requester_email"] = strings.Repeat("a", maxEmailRunes) + "@exemple.fr"
	if code, _ := c.call(http.MethodPost, "/api/team/request", address); code !=
		http.StatusBadRequest {
		t.Errorf("an address past %d runes: %d, want 400", maxEmailRunes, code)
	}
	message := teamRequestBody("Équipe bavarde", "01")
	message["message"] = strings.Repeat("x", maxNoteRunes+1)
	if code, _ := c.call(http.MethodPost, "/api/team/request", message); code !=
		http.StatusBadRequest {
		t.Errorf("a message of %d runes: %d, want 400", maxNoteRunes+1, code)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM team_requests WHERE org_id=$1",
		org); n != 0 {
		t.Fatalf("%d oversized request(s) written", n)
	}
}

// The queue is the coordination's, and a lead reads the same payload without
// it: /api/team answers both, and the walk that guards roles cannot see a
// field that is simply always present.
func TestTheTeamRequestQueueIsTheCoordinationsAlone(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)
	team := createTeamIn(t, s, org, "Équipe 01", "01")
	coordPassword := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	leadPassword := createAccount(t, s, "referent@exemple.fr", RoleLead, &team)

	if code, body := newClient(t, srv).call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe demandée", "01")); code != http.StatusCreated {
		t.Fatalf("the public form: %d %v", code, body)
	}

	coord := newClient(t, srv)
	if code := coord.signIn("coord@exemple.fr", coordPassword); code != http.StatusOK {
		t.Fatalf("coordination sign-in: %d", code)
	}
	code, payload := coord.call(http.MethodGet, "/api/team", nil)
	if code != http.StatusOK {
		t.Fatalf("/api/team as coordination: %d", code)
	}
	requests, _ := payload["requests"].([]any)
	if len(requests) != 1 {
		t.Fatalf("the coordination sees %d request(s), want 1", len(requests))
	}

	lead := newClient(t, srv)
	if code := lead.signIn("referent@exemple.fr", leadPassword); code != http.StatusOK {
		t.Fatalf("lead sign-in: %d", code)
	}
	code, payload = lead.call(http.MethodGet, "/api/team", nil)
	if code != http.StatusOK {
		t.Fatalf("/api/team as a lead: %d", code)
	}
	if requests, _ := payload["requests"].([]any); len(requests) != 0 {
		t.Fatalf("a lead reads %d request(s) of the moderation queue, want 0",
			len(requests))
	}
}

// Accepting: the team AND its lead, in one stroke, under the name and the
// perimeter the coordination settled on — not necessarily the ones asked for.
func TestAcceptingATeamRequestOpensTheTeamAndItsLead(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	seedMayors(t, s, 2, "02")
	org := orgID(t, s, testSlug)
	password := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)

	if code, body := newClient(t, srv).call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe du 01", "01")); code != http.StatusCreated {
		t.Fatalf("the public form: %d %v", code, body)
	}
	id := scalar[int64](t, s, "SELECT id FROM team_requests WHERE org_id=$1", org)

	coord := newClient(t, srv)
	if code := coord.signIn("coord@exemple.fr", password); code != http.StatusOK {
		t.Fatalf("coordination sign-in: %d", code)
	}
	code, body := coord.call(http.MethodPost, fmt.Sprintf("/api/team/requests/%d", id),
		map[string]any{"decision": RequestAccepted,
			"name": "Équipe Nord-Est", "departments": []string{"01", "02"}})
	if code != http.StatusOK {
		t.Fatalf("accepting: %d %v", code, body)
	}
	if p, _ := body["password"].(string); p == "" {
		t.Error("no password came back: the coordination has nothing to pass on")
	}

	perimeter := scalar[string](t, s, "SELECT departments FROM teams WHERE "+
		"org_id=$1 AND name='Équipe Nord-Est'", org)
	if perimeter != "01;02" {
		t.Errorf("the team's perimeter is %q, not the corrected one", perimeter)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM teams WHERE org_id=$1 AND "+
		"name='Équipe du 01'", org); n != 0 {
		t.Error("the team was also opened under the name the form asked for")
	}
	role := scalar[string](t, s, "SELECT role FROM accounts WHERE org_id=$1 AND "+
		"email=$2", org, requesterAddress)
	if role != RoleLead {
		t.Errorf("the requester's account carries role %q, want lead", role)
	}
	teamOfLead := scalar[int](t, s, "SELECT team_id FROM accounts WHERE org_id=$1 "+
		"AND email=$2", org, requesterAddress)
	if want := scalar[int](t, s, "SELECT id FROM teams WHERE org_id=$1 AND "+
		"name='Équipe Nord-Est'", org); teamOfLead != want {
		t.Errorf("the lead is attached to team %d, not to the one just opened (%d)",
			teamOfLead, want)
	}

	// A decision is taken once. The second call must find nothing to decide,
	// and above all must not open a second team.
	if code, _ := coord.call(http.MethodPost, fmt.Sprintf("/api/team/requests/%d", id),
		map[string]any{"decision": RequestAccepted}); code != http.StatusConflict {
		t.Errorf("deciding twice: %d, want 409", code)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM teams WHERE org_id=$1", org); n != 1 {
		t.Errorf("%d teams after one accepted request", n)
	}
}

// Refusing writes the reason and opens nothing — the state a queue screen
// shows, and the only trace the campaign keeps of a request it turned down.
func TestRefusingATeamRequestOpensNothing(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)
	password := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)

	if code, _ := newClient(t, srv).call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe refusée", "01")); code != http.StatusCreated {
		t.Fatal("the public form did not accept the request")
	}
	id := scalar[int64](t, s, "SELECT id FROM team_requests WHERE org_id=$1", org)

	coord := newClient(t, srv)
	if code := coord.signIn("coord@exemple.fr", password); code != http.StatusOK {
		t.Fatalf("coordination sign-in: %d", code)
	}
	code, body := coord.call(http.MethodPost, fmt.Sprintf("/api/team/requests/%d", id),
		map[string]any{"decision": RequestRefused, "reason": "Département déjà couvert"})
	if code != http.StatusOK {
		t.Fatalf("refusing: %d %v", code, body)
	}
	if _, given := body["password"]; given {
		t.Error("a refusal handed back a password")
	}
	state := scalar[string](t, s, "SELECT state FROM team_requests WHERE org_id=$1 "+
		"AND id=$2", org, id)
	if state != RequestRefused {
		t.Errorf("the request is in state %q after a refusal", state)
	}
	reason := scalar[string](t, s, "SELECT reason FROM team_requests WHERE org_id=$1 "+
		"AND id=$2", org, id)
	if reason != "Département déjà couvert" {
		t.Errorf("the reason was not kept: %q", reason)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM teams WHERE org_id=$1", org); n != 0 {
		t.Errorf("%d team(s) opened by a refusal", n)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM accounts WHERE org_id=$1 AND "+
		"email=$2", org, requesterAddress); n != 0 {
		t.Error("a refusal opened the requester's account")
	}
}

// The requester already has an account here: it carries a role and a team of
// its own, and making it the lead of a new one would move somebody without a
// word. Refused, and the team is not opened either — a half-applied
// acceptance would leave a team nobody leads.
func TestAcceptingRefusesWhenTheRequesterAlreadyHasAnAccount(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)
	team := createTeamIn(t, s, org, "Équipe 01", "01")
	createAccountIn(t, s, org, requesterAddress, RoleVolunteer, &team)
	password := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)

	if code, _ := newClient(t, srv).call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe du 01", "01")); code != http.StatusCreated {
		t.Fatal("the public form did not accept the request")
	}
	id := scalar[int64](t, s, "SELECT id FROM team_requests WHERE org_id=$1", org)

	coord := newClient(t, srv)
	if code := coord.signIn("coord@exemple.fr", password); code != http.StatusOK {
		t.Fatalf("coordination sign-in: %d", code)
	}
	code, body := coord.call(http.MethodPost, fmt.Sprintf("/api/team/requests/%d", id),
		map[string]any{"decision": RequestAccepted})
	if code != http.StatusConflict {
		t.Fatalf("accepting for an address that already has an account: %d %v, "+
			"want 409", code, body)
	}
	if role := scalar[string](t, s, "SELECT role FROM accounts WHERE org_id=$1 AND "+
		"email=$2", org, requesterAddress); role != RoleVolunteer {
		t.Errorf("the existing account was promoted to %q behind its owner's back", role)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM teams WHERE org_id=$1 AND "+
		"name='Équipe du 01'", org); n != 0 {
		t.Error("the team was opened while its lead could not be: a team nobody leads")
	}
	if state := scalar[string](t, s, "SELECT state FROM team_requests WHERE "+
		"org_id=$1 AND id=$2", org, id); state != RequestPending {
		t.Errorf("the request left 'pending' (%q) on a decision that failed", state)
	}
}
