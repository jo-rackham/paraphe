package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The way back into a campaign nobody can enter. The instance administration
// GRANTS an access; it never reads the work behind it, and it never takes
// over an account somebody already holds.

const (
	grantApex  = "paraphe.test"
	grantSlug  = "orpheline"
	grantAdmin = "admin@paraphe.test"
)

// grantSetup: an instance with an administration, and a campaign whose
// coordination is out of reach.
func grantSetup(t *testing.T) (*Server, *httptest.Server, *client, int) {
	t.Helper()
	t.Setenv("PARAPHE_BASE_DOMAIN", grantApex)
	s, srv := testServer(t)
	password := createAccountIn(t, s, OrgInstance, grantAdmin,
		RoleAdministration, nil)
	org := createOrg(t, s, grantSlug, "Campagne orpheline")
	admin := clientOn(t, srv, grantApex)
	if code := admin.signIn(grantAdmin, password); code != http.StatusOK {
		t.Fatalf("administration sign-in: %d", code)
	}
	return s, srv, admin, org
}

func grantPath(slug string) string {
	return "/api/admin/campaigns/" + slug + "/coordination"
}

// The whole point: the door opens, and what comes out of it signs in on the
// campaign. A password returned by a route nobody can use is a password that
// only looks like a remedy.
func TestAnOpenedCoordinationSignsInOnItsCampaign(t *testing.T) {
	s, srv, admin, org := grantSetup(t)
	code, body := admin.call(http.MethodPost, grantPath(grantSlug),
		map[string]string{"email": "secours@exemple.fr", "name": "Camille Secours"})
	if code != http.StatusCreated {
		t.Fatalf("opening a coordination access: %d, want 201 (%v)", code, body)
	}
	password, _ := body["password"].(string)
	if password == "" {
		t.Fatal("no password in the answer: the administrator has nothing to pass on")
	}

	role := scalar[string](t, s,
		"SELECT role FROM accounts WHERE org_id=$1 AND email=$2",
		org, "secours@exemple.fr")
	if role != RoleCoordination {
		t.Fatalf("the account was born %q, want %q", role, RoleCoordination)
	}
	// It is born in the NAMED campaign and nowhere else — the instance scope
	// is where the request runs, not where the row belongs.
	if n := scalar[int](t, s,
		"SELECT COUNT(*) FROM accounts WHERE org_id=$1 AND email=$2",
		OrgInstance, "secours@exemple.fr"); n != 0 {
		t.Fatalf("%d copies of the account landed in the instance scope", n)
	}

	campaignSide := clientOn(t, srv, grantSlug+"."+grantApex)
	if code := campaignSide.signIn("secours@exemple.fr", password); code != http.StatusOK {
		t.Fatalf("the opened access does not sign in on its own campaign: %d", code)
	}
}

// The act is visible to the campaign it was done to. An access granted by the
// host and indistinguishable from one the campaign created itself is a door
// opened behind somebody's back.
func TestAnOpenedCoordinationNamesWhoOpenedIt(t *testing.T) {
	s, _, admin, org := grantSetup(t)
	if code, body := admin.call(http.MethodPost, grantPath(grantSlug),
		map[string]string{"email": "secours@exemple.fr", "name": "Camille Secours"}); code !=
		http.StatusCreated {
		t.Fatalf("opening a coordination access: %d (%v)", code, body)
	}
	by := scalar[string](t, s,
		"SELECT created_by FROM accounts WHERE org_id=$1 AND email=$2",
		org, "secours@exemple.fr")
	if by != grantAdmin {
		t.Fatalf("created_by is %q: the campaign cannot tell who opened this "+
			"access, want %q", by, grantAdmin)
	}
}

// An address that already has an account there is refused, and the account it
// names is untouched — not promoted, not repassworded, not switched on. The
// host opens a door; it does not take somebody's.
func TestAnExistingAccountIsNeverTakenOverFromTheInstance(t *testing.T) {
	s, _, admin, org := grantSetup(t)
	createAccountIn(t, s, org, "benevole@exemple.fr", RoleVolunteer, nil)
	before := scalar[string](t, s,
		"SELECT password_hash FROM accounts WHERE org_id=$1 AND email=$2",
		org, "benevole@exemple.fr")

	code, _ := admin.call(http.MethodPost, grantPath(grantSlug),
		map[string]string{"email": "benevole@exemple.fr", "name": "Quelqu'un d'autre"})
	if code != http.StatusConflict {
		t.Fatalf("naming an address that already has an account: %d, want 409", code)
	}
	role := scalar[string](t, s,
		"SELECT role FROM accounts WHERE org_id=$1 AND email=$2",
		org, "benevole@exemple.fr")
	if role != RoleVolunteer {
		t.Fatalf("the refused call promoted the account to %q", role)
	}
	if after := scalar[string](t, s,
		"SELECT password_hash FROM accounts WHERE org_id=$1 AND email=$2",
		org, "benevole@exemple.fr"); after != before {
		t.Fatal("the refused call rewrote the account's password")
	}
}

// Deactivation is the lever a campaign keeps against its own host: a
// coordination switches off a phished account, and the address is the one
// thing an administrator would naturally reuse. Seeding has the same refusal
// for the same reason.
func TestADeactivatedAccountIsNotSwitchedBackOnFromTheInstance(t *testing.T) {
	s, _, admin, org := grantSetup(t)
	createAccountIn(t, s, org, "compromis@exemple.fr", RoleCoordination, nil)
	execAsMaintenance(t, s,
		"UPDATE accounts SET active=FALSE WHERE org_id=$1 AND email=$2",
		org, "compromis@exemple.fr")

	code, _ := admin.call(http.MethodPost, grantPath(grantSlug),
		map[string]string{"email": "compromis@exemple.fr", "name": "Compromis"})
	if code != http.StatusConflict {
		t.Fatalf("naming a deactivated address: %d, want 409", code)
	}
	if active := scalar[bool](t, s,
		"SELECT active FROM accounts WHERE org_id=$1 AND email=$2",
		org, "compromis@exemple.fr"); active {
		t.Fatal("the deactivated account was switched back on from the instance")
	}
}

// A campaign that answers 503 on every route, sign-in included, is one where
// a minted credential opens nothing. Saying so beats handing over a password
// that looks like an answer.
func TestASuspendedCampaignRefusesTheDoorRatherThanOpenNothing(t *testing.T) {
	s, _, admin, org := grantSetup(t)
	execAsMaintenance(t, s, "UPDATE orgs SET state=$1 WHERE id=$2",
		OrgSuspended, org)

	code, _ := admin.call(http.MethodPost, grantPath(grantSlug),
		map[string]string{"email": "secours@exemple.fr", "name": "Camille"})
	if code != http.StatusConflict {
		t.Fatalf("opening an access on a suspended campaign: %d, want 409", code)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM accounts WHERE org_id=$1",
		org); n != 0 {
		t.Fatalf("%d account(s) created on a suspended campaign", n)
	}
}

func TestAnUnknownCampaignIsNotACampaignToOpen(t *testing.T) {
	_, _, admin, _ := grantSetup(t)
	code, _ := admin.call(http.MethodPost, grantPath("nexistepas"),
		map[string]string{"email": "secours@exemple.fr", "name": "Camille"})
	if code != http.StatusNotFound {
		t.Fatalf("opening an access on an unknown campaign: %d, want 404", code)
	}
}

// The promise the whole design rests on: the host GRANTS, it does not READ.
// The answer to a granting call must carry nothing of the campaign's work —
// no card, no volunteer, no counter. Stated as a test because the cheapest
// way to break it is to add a helpful field to the reply.
func TestGrantingAnAccessReadsNoneOfTheCampaignsWork(t *testing.T) {
	s, _, admin, org := grantSetup(t)
	// work that a reply must not learn of: a card, a volunteer, a note
	team := createTeamIn(t, s, org, "Équipe locale", "")
	createAccountIn(t, s, org, "benevole@exemple.fr", RoleVolunteer, &team)
	execAsMaintenance(t, s,
		"INSERT INTO assignments(org_id, insee_code, team_id, volunteer, status, updated_at) "+
			"VALUES($1,'01001',$2,'benevole@exemple.fr','signed','2026-01-01T00:00')",
		org, team)
	execAsMaintenance(t, s,
		"INSERT INTO notes(org_id, insee_code, team_id, volunteer, status, note, ts) "+
			"VALUES($1,'01001',$2,'benevole@exemple.fr','signed','NOTE CONFIDENTIELLE','2026-01-01T00:00')",
		org, team)

	code, body := admin.call(http.MethodPost, grantPath(grantSlug),
		map[string]string{"email": "secours@exemple.fr", "name": "Camille"})
	if code != http.StatusCreated {
		t.Fatalf("opening a coordination access: %d (%v)", code, body)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{
		"NOTE CONFIDENTIELLE", "benevole@exemple.fr", "Équipe locale",
		"01001", "signed",
	} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("the granting answer carries %q: the host read the "+
				"campaign's work on its way through", leak)
		}
	}
}
