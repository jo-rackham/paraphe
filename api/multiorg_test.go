package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestScopeOfHost(t *testing.T) {
	const base = "paraphe.fr"
	cases := []struct {
		host     string
		slug     string
		instance bool
		ok       bool
	}{
		{"campaign.paraphe.fr", "campaign", false, true},
		{"CAMPAIGN.Paraphe.FR", "campaign", false, true},
		{"campaign.paraphe.fr:8443", "campaign", false, true},
		// absolute name: the trailing dot designates the SAME campaign
		{"campaign.paraphe.fr.", "campaign", false, true},
		{"paraphe.fr", "", true, true},
		{"www.paraphe.fr", "", true, true},
		// one level only: the wildcard certificate covers no deeper
		{"a.b.paraphe.fr", "", false, false},
		// a reserved slug designates no campaign
		{"api.paraphe.fr", "", false, false},
		{"other-domain.fr", "", false, false},
		{"paraphe.fr.attaquant.fr", "", false, false},
		// no campaign is served by IP: the splitting must not build a slug
		// out of the colons
		{"127.0.0.1:8047", "", false, false},
		{"[::1]:8047", "", false, false},
		{"", "", false, false},
	}
	for _, c := range cases {
		scope, ok := ScopeOfHost(c.host, base)
		if ok != c.ok || scope.Slug != c.slug || scope.Instance != c.instance {
			t.Errorf("ScopeOfHost(%q) = %+v, %v; expected {slug:%q instance:%v}, %v",
				c.host, scope, ok, c.slug, c.instance, c.ok)
		}
	}
	// without a base domain, no host designates anything: the caller is
	// what switches to single-campaign, not this function
	if _, ok := ScopeOfHost("campaign.paraphe.fr", ""); ok {
		t.Error("an empty base domain resolved a host")
	}
}

func TestValidSlug(t *testing.T) {
	good := []string{"campaign", "ma-campaign-2027", "c2027", "ab"}
	bad := []string{
		"a", "", "-debut", "fin-", "MAJUSCULE", "avec_underscore",
		"avec.point", "accentué", "api", "www", "admin",
	}
	for _, s := range good {
		if !ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = false", s)
		}
	}
	for _, s := range bad {
		if ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = true", s)
		}
	}
}

func createOrg(t *testing.T, s *Server, slug, name string) int {
	t.Helper()
	var id int
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"INSERT INTO orgs(slug, name, campaign, batch_size, state, created_at) "+
				"VALUES($1,$2,'{}'::jsonb,2,'active','2026-01-01T00:00') RETURNING id",
			slug, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
	})
	return id
}

// walledTables says which tables carry per-campaign rows, and everything
// downstream believes it: the canary checks those tables and no others, the
// walls test counts those rows and no others. Until now it ALSO decided which
// tables got a policy — so dropping a name from the list removed the wall and
// the check that the wall existed, in one edit, in silence.
//
// The database answers instead. A table with an `org_id` column carries rows
// that belong to a campaign; there is no judgement to make and no second
// place to keep in step.
func TestEveryPerCampaignTableIsWalled(t *testing.T) {
	s, _ := testServer(t)
	rows, err := s.pool.Query(context.Background(),
		"SELECT table_name FROM information_schema.columns "+
			"WHERE table_schema='public' AND column_name='org_id'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		found[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("no table carries an org_id column: the schema did not load, " +
			"and this test would agree with any list at all")
	}
	declared := map[string]bool{}
	for _, table := range walledTables {
		declared[table] = true
		if !found[table] {
			t.Errorf("walledTables names %q, which carries no org_id column", table)
		}
	}
	for table := range found {
		if !declared[table] {
			t.Errorf("%s carries an org_id column and is NOT in walledTables: "+
				"no query on it is checked for naming the campaign, and nothing "+
				"else would have said so", table)
		}
	}
}

func TestTwoCampaignsCannotSeeEachOther(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	seedMayors(t, s, 6, "01")
	a := orgID(t, s, testSlug)
	b := createOrg(t, s, "other", "Other campaign")

	pwA := createAccountIn(t, s, a, "moi@exemple.fr", RoleVolunteer, nil)
	pwB := createAccountIn(t, s, b, "moi@exemple.fr", RoleVolunteer, nil)
	ca := clientOn(t, srv, testSlug+".paraphe.test")
	cb := clientOn(t, srv, "other.paraphe.test")

	// the same address exists in both campaigns, with two different
	// passwords: each only accepts its own
	if code := ca.signIn("moi@exemple.fr", pwB); code != http.StatusUnauthorized {
		t.Errorf("the other campaign's password is accepted: %d", code)
	}
	if code := ca.signIn("moi@exemple.fr", pwA); code != http.StatusOK {
		t.Fatalf("sign-in refused on one's own campaign: %d", code)
	}
	if code := cb.signIn("moi@exemple.fr", pwB); code != http.StatusOK {
		t.Fatalf("sign-in refused on the other campaign: %d", code)
	}

	if code, rep := ca.call(http.MethodPost, "/api/batch", map[string]any{}); code != http.StatusOK {
		t.Fatalf("batch refused: %d %v", code, rep)
	}
	// campaign B sees the SAME available mayors: the list is shared, the
	// work is not
	code, rep := cb.call(http.MethodGet, "/api/mayors", nil)
	if code != http.StatusOK {
		t.Fatalf("list refused: %d %v", code, rep)
	}
	if total := int(rep["total"].(float64)); total != 6 {
		t.Errorf("campaign B sees %d available mayors out of 6: campaign A's "+
			"work spills over", total)
	}
	// and its dashboard counts no contact
	_, dash := cb.call(http.MethodGet, "/api/dashboard", nil)
	if mine, _ := dash["mine"].([]any); len(mine) != 0 {
		t.Errorf("campaign B has %d card(s) assigned without having done anything", len(mine))
	}

	// a host that matches nothing is not served at random
	unknown := clientOn(t, srv, "inexistante.paraphe.test")
	if code, _ := unknown.call(http.MethodGet, "/api/config", nil); code != http.StatusNotFound {
		t.Errorf("an unknown subdomain was served: %d", code)
	}
}

// The cookie is valid for ONE campaign: presented to another, it is worth
// nothing.
func TestSessionDoesNotCrossCampaigns(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	a := orgID(t, s, testSlug)
	b := createOrg(t, s, "other", "Other campaign")
	pw := createAccountIn(t, s, a, "moi@exemple.fr", RoleVolunteer, nil)
	createAccountIn(t, s, b, "moi@exemple.fr", RoleVolunteer, nil)

	c := clientOn(t, srv, testSlug+".paraphe.test")
	if code := c.signIn("moi@exemple.fr", pw); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	// same client, same cookie jar, other campaign
	c.host = "other.paraphe.test"
	if code, rep := c.call(http.MethodGet, "/api/me", nil); code != http.StatusUnauthorized {
		t.Errorf("one campaign's session is valid for another: %d %v", code, rep)
	}
}

// The apex serves the instance landing page, not a campaign.
func TestApexIsNotACampaign(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	a := orgID(t, s, testSlug)
	pw := createAccountIn(t, s, a, "moi@exemple.fr", RoleVolunteer, nil)

	apex := clientOn(t, srv, "paraphe.test")
	code, rep := apex.call(http.MethodGet, "/api/config", nil)
	if code != http.StatusOK || rep["mode"] != "instance" {
		t.Fatalf("/api/config on the apex: %d %v", code, rep)
	}
	// a campaign cannot sign in there: its accounts are not there
	if code := apex.signIn("moi@exemple.fr", pw); code != http.StatusUnauthorized {
		t.Errorf("a campaign account signed in on the apex: %d", code)
	}
	// and the work routes do not exist there
	if code, _ := apex.call(http.MethodGet, "/api/dashboard", nil); code == http.StatusOK {
		t.Error("the apex serves a campaign's dashboard")
	}
}

// The form is public, but it creates nothing: the approval is what creates
// the campaign, and with it its requester's access.
func TestRequestThenApprovalCreatesCampaign(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	execAsMaintenance(t, s,
		"INSERT INTO accounts(org_id, email, name, password_hash, role) VALUES($1,$2,$3,$4,$5)",
		OrgInstance, "admin@paraphe.test", "Administration",
		testHash(t, "mot-de-passe-admin"), RoleAdministration)

	apex := clientOn(t, srv, "paraphe.test")
	request := map[string]any{
		"slug": "nouvelle", "name": "Nouvelle campagne",
		"requester_email": "porteur@exemple.fr", "requester_name": "Porteur",
		"message": "on présente quelqu'un",
	}
	code, rep := apex.call(http.MethodPost, "/api/request", request)
	if code != http.StatusCreated {
		t.Fatalf("request refused: %d %v", code, rep)
	}
	// nothing is created until someone approves
	if code, _ := clientOn(t, srv, "nouvelle.paraphe.test").
		call(http.MethodGet, "/api/config", nil); code != http.StatusNotFound {
		t.Errorf("the campaign exists before moderation: %d", code)
	}
	// and squatting the same name is refused right away
	if code, _ := apex.call(http.MethodPost, "/api/request", request); code != http.StatusConflict {
		t.Errorf("second request on the same subdomain accepted: %d", code)
	}

	admin := clientOn(t, srv, "paraphe.test")
	if code := admin.signIn("admin@paraphe.test", "mot-de-passe-admin"); code != http.StatusOK {
		t.Fatalf("administration sign-in: %d", code)
	}
	code, queue := admin.call(http.MethodGet, "/api/admin/requests", nil)
	if code != http.StatusOK {
		t.Fatalf("moderation queue: %d %v", code, queue)
	}
	requests, _ := queue["requests"].([]any)
	if len(requests) != 1 {
		t.Fatalf("%d request(s) in the queue, 1 expected", len(requests))
	}
	id := int64(requests[0].(map[string]any)["id"].(float64))

	code, dec := admin.call(http.MethodPost,
		"/api/admin/requests/"+itoa(id), map[string]any{"decision": RequestAccepted})
	if code != http.StatusOK {
		t.Fatalf("approval refused: %d %v", code, dec)
	}
	pw, _ := dec["password"].(string)
	if pw == "" {
		t.Fatal("no password returned for the created coordination")
	}
	// the same decision twice does not create two campaigns
	if code, _ := admin.call(http.MethodPost, "/api/admin/requests/"+itoa(id),
		map[string]any{"decision": RequestAccepted}); code != http.StatusConflict {
		t.Errorf("request approved twice: %d", code)
	}

	// the campaign answers, and its requester enters as coordination
	fresh := clientOn(t, srv, "nouvelle.paraphe.test")
	code, cfg := fresh.call(http.MethodGet, "/api/config", nil)
	if code != http.StatusOK || cfg["mode"] != "team" {
		t.Fatalf("the approved campaign does not answer: %d %v", code, cfg)
	}
	if code := fresh.signIn("porteur@exemple.fr", pw); code != http.StatusOK {
		t.Fatalf("the requester cannot enter their campaign: %d", code)
	}
	code, me := fresh.call(http.MethodGet, "/api/me", nil)
	if code != http.StatusOK {
		t.Fatalf("/api/me: %d %v", code, me)
	}
	account, _ := me["account"].(map[string]any)
	if account["role"] != RoleCoordination {
		t.Errorf("the requester enters with role %v", account["role"])
	}
}

// The administration can open a campaign without a request: the queue's own
// ceiling message promises that door, and this is it.
func TestDirectCreationOpensCampaign(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	execAsMaintenance(t, s,
		"INSERT INTO accounts(org_id, email, name, password_hash, role) VALUES($1,$2,$3,$4,$5)",
		OrgInstance, "admin@paraphe.test", "Administration",
		testHash(t, "mot-de-passe-admin"), RoleAdministration)

	admin := clientOn(t, srv, "paraphe.test")
	if code := admin.signIn("admin@paraphe.test", "mot-de-passe-admin"); code != http.StatusOK {
		t.Fatalf("administration sign-in: %d", code)
	}
	body := map[string]any{
		"slug": "directe", "name": "Campagne directe",
		"coordination_email": "coord@exemple.fr", "coordination_name": "Coordination",
	}
	code, rep := admin.call(http.MethodPost, "/api/admin/campaigns", body)
	if code != http.StatusCreated {
		t.Fatalf("direct creation refused: %d %v", code, rep)
	}
	pw, _ := rep["password"].(string)
	if pw == "" {
		t.Fatal("no password returned for the created coordination")
	}
	// the same slug twice: a conflict, and still exactly one organisation
	if code, _ := admin.call(http.MethodPost, "/api/admin/campaigns", body); code != http.StatusConflict {
		t.Errorf("second creation on the same subdomain: %d, want 409", code)
	}
	if n := scalar[int](t, s, "SELECT count(*) FROM orgs WHERE slug='directe'"); n != 1 {
		t.Fatalf("%d organisation(s) for one slug", n)
	}
	// an unusable slug is refused before anything is written
	if code, _ := admin.call(http.MethodPost, "/api/admin/campaigns",
		map[string]any{"slug": "Pas Valide!", "name": "X",
			"coordination_email": "c@exemple.fr", "coordination_name": "C"}); code != http.StatusBadRequest {
		t.Errorf("invalid slug accepted: %d", code)
	}

	// the campaign answers, and its coordination enters with the returned
	// password
	fresh := clientOn(t, srv, "directe.paraphe.test")
	code, cfg := fresh.call(http.MethodGet, "/api/config", nil)
	if code != http.StatusOK || cfg["mode"] != "team" {
		t.Fatalf("the created campaign does not answer: %d %v", code, cfg)
	}
	if code := fresh.signIn("coord@exemple.fr", pw); code != http.StatusOK {
		t.Fatalf("the coordination cannot enter its campaign: %d", code)
	}
	code, me := fresh.call(http.MethodGet, "/api/me", nil)
	if code != http.StatusOK {
		t.Fatalf("/api/me: %d %v", code, me)
	}
	account, _ := me["account"].(map[string]any)
	if account["role"] != RoleCoordination {
		t.Errorf("the coordination enters with role %v", account["role"])
	}

	// a campaign role cannot open campaigns — the return code is asserted
	// first, then the absence of the write (house rule)
	if code, _ := fresh.call(http.MethodPost, "/api/admin/campaigns",
		map[string]any{"slug": "encore", "name": "Encore",
			"coordination_email": "e@exemple.fr",
			"coordination_name":  "E"}); code != http.StatusForbidden {
		t.Fatalf("campaign coordination on the administration door: %d, want 403", code)
	}
	if n := scalar[int](t, s, "SELECT count(*) FROM orgs WHERE slug='encore'"); n != 0 {
		t.Fatal("a campaign role created an organisation")
	}
}

// A suspended campaign keeps its work, but nobody gets in.
func TestSuspendedCampaignAcceptsNoRequest(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	// Suspending has no route: it is an operator's SQL, run from maintenance.
	// Without a declared scope the UPDATE now matches no row and changes
	// nothing — silently, which is why this is spelt out here.
	execAsMaintenance(t, s,
		"UPDATE orgs SET state=$1 WHERE slug=$2", OrgSuspended, testSlug)
	c := clientOn(t, srv, testSlug+".paraphe.test")
	if code, _ := c.call(http.MethodGet, "/api/config", nil); code != http.StatusServiceUnavailable {
		t.Errorf("a suspended campaign answers: %d", code)
	}
}

func testHash(t *testing.T, password string) string {
	t.Helper()
	h, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// The file configuration ALWAYS carries all nine campaign keys — a complete
// configuration requires them, and the shipped campagne.yaml fills them
// with template values. Reapplying it on restart reverted everything
// coordination had typed into "Mon équipe", down to re-arming the
// "campaign not configured" banner and regenerating "Prénom NOM" messages.
func TestRestartKeepsWhatCoordinationTyped(t *testing.T) {
	s, _ := testServer(t)
	org := orgID(t, s, testSlug)
	execAsMaintenance(t, s,
		`UPDATE orgs SET campaign = campaign || '{"candidat":"Camille Réel",`+
			`"contact_email":"camille@exemple.org"}'::jsonb WHERE id=$1`, org)

	// a restart with the shipped template, and nothing in the environment
	cfg := testConfig()
	cfg.Campaign["candidat"] = "Prénom NOM"
	cfg.Campaign["contact_email"] = "contact@exemple.fr"
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if _, err := ensureOrg(context.Background(), tx, testSlug, cfg); err != nil {
			t.Fatal(err)
		}
	})
	after := campaignOf(t, s, org)
	if after["candidat"] != "Camille Réel" {
		t.Errorf("candidat = %q after restart: the coordination's own campaign "+
			"was reverted to the template, and the messages with it",
			after["candidat"])
	}

	// but an explicit PARAPHE_* still fixes a campaign without touching the
	// database — that is what the override exists for
	cfg.Overrides = map[string]string{"candidat": "Corrigé Par L'Opérateur"}
	cfg.Campaign["candidat"] = "Corrigé Par L'Opérateur"
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if _, err := ensureOrg(context.Background(), tx, testSlug, cfg); err != nil {
			t.Fatal(err)
		}
	})
	after = campaignOf(t, s, org)
	if after["candidat"] != "Corrigé Par L'Opérateur" {
		t.Errorf("candidat = %q: an explicit PARAPHE_CANDIDATE no longer applies",
			after["candidat"])
	}
	// and it did NOT drag the rest of the file along with it
	if after["contact_email"] != "camille@exemple.org" {
		t.Errorf("contact_email = %q: a single override reverted the other keys",
			after["contact_email"])
	}
}

func campaignOf(t *testing.T, s *Server, org int) map[string]string {
	t.Helper()
	var campaign map[string]string
	if err := s.pool.QueryRow(context.Background(),
		"SELECT campaign FROM orgs WHERE id=$1", org).Scan(&campaign); err != nil {
		t.Fatal(err)
	}
	return campaign
}
