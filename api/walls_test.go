package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// THE walling test. Every query on a per-campaign table names the campaign,
// and this is what proves it end to end: two campaigns on one instance, one
// of them signed in, every read and every write exercised, and NOTHING of the
// neighbour coming back — neither a row, nor a count, nor a string.
//
// It runs as production does, under the same role, and what it proves is the
// whole guarantee: there is no second wall behind it.
func TestNoCampaignSeesAnother(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	seedMayors(t, s, 6, "01")
	a := orgID(t, s, testSlug)
	b := createOrg(t, s, "other", "Other campaign")

	const neighbourVolunteer = "b-only@exemple.fr"
	const neighbourNote = "NOTE QUI APPARTIENT A LA CAMPAGNE B"
	// Two names A will POST back on its own host, so neither can be a leak
	// marker: what A legitimately holds cannot prove a leak. The requester and
	// the message are B's alone, never written by A, and they are what the
	// queue would hand over.
	const neighbourTeamName = "ÉQUIPE QUI EXISTE CHEZ B"
	const neighbourRequestName = "ÉQUIPE DEMANDÉE CHEZ B"
	const neighbourRequester = "demandeur-b@exemple.fr"
	const neighbourRequestMessage = "MOTIVATION DE LA DEMANDE CHEZ B"
	// B's own configuration, marked and DIFFERENT from A's in every column
	// /api/campaign writes. Reading the name back alone left the other two
	// unwatched: a write that stayed row-bound on the name while pouring A's
	// campaign JSONB into every other campaign passed this test — and that
	// JSONB is the candidate, the contacts and the signatory, quoted verbatim
	// in every message sent to a mayor.
	const neighbourCandidate = "CANDIDATE DE LA CAMPAGNE B"
	const neighbourBatch = 7
	// B's logo, three more columns on `orgs` that the campaign screen writes.
	// The key is what builds a public URL, so a leak here does not merely
	// show a neighbour's picture: it publishes which campaigns this instance
	// hosts, to a campaign that was told nothing of the sort.
	const neighbourLogoKey = "logos/other/beefbeefbeefbeef.png"
	execAsMaintenance(t, s,
		"UPDATE orgs SET campaign=jsonb_build_object('candidat',$1::text), "+
			"batch_size=$2, logo_key=$4, logo_type='image/png' WHERE id=$3",
		neighbourCandidate, neighbourBatch, b, neighbourLogoKey)
	execAsMaintenance(t, s,
		"INSERT INTO assignments(org_id, insee_code, volunteer, status) "+
			"VALUES($1,'01001',$2,'signed')", b, neighbourVolunteer)
	// Its identifier is kept: `notes.id` is one sequence for the whole table,
	// so A can NAME a row of B's by number, which is the only thing the two
	// note routes need to reach one.
	var neighbourNoteID int64
	asMaintenanceRow(t, s, "INSERT INTO notes(org_id, insee_code, volunteer, "+
		"status, note, ts) VALUES($1,'01001',$2,'signed',$3,'2026-01-01T00:00') "+
		"RETURNING id", &neighbourNoteID, b, neighbourVolunteer, neighbourNote)
	execAsMaintenance(t, s,
		"INSERT INTO accounts(org_id, email, name, password_hash, role, active) "+
			"VALUES($1,$2,'Bénévole de B','x','volunteer',true)",
		b, neighbourVolunteer)
	// Three more of B's rows, PROMISED and SIGNED: a leak in a counter carries
	// none of the strings above, so the numeric assertions below are what
	// catch a filter that stops filtering.
	for i, st := range []string{"promised", "signed", "signed"} {
		execAsMaintenance(t, s,
			"INSERT INTO assignments(org_id, insee_code, volunteer, status) "+
				"VALUES($1,$2,$3,$4)",
			b, fmt.Sprintf("01%03d", i+2), neighbourVolunteer, st)
	}
	// The same address in BOTH campaigns, under different names: the export
	// and /api/me join accounts on the email alone.
	hash, err := HashPassword("motdepasse-de-test-1234")
	if err != nil {
		t.Fatal(err)
	}
	const shared = "commun@exemple.fr"
	const neighbourOwnNote = "TOUCHE PERSONNELLE DE LA CAMPAGNE B"
	for _, org := range []int{a, b} {
		name := "Moi chez A"
		if org == b {
			name = "Homonyme chez B"
		}
		execAsMaintenance(t, s,
			"INSERT INTO accounts(org_id, email, name, password_hash, role, active) "+
				"VALUES($1,$2,$3,$4,'volunteer',true)", org, shared, name, hash)
	}
	// B's note carries a marker. A route that wrote across the wall would
	// replace it — but only if A signs in under an address B ALSO holds:
	// signed in as an address unique to A, the write could not reach B even
	// with no wall at all, and the assertion would pass on nothing.
	execAsMaintenance(t, s,
		"UPDATE accounts SET personal_note=$1 WHERE org_id=$2 AND email=$3",
		neighbourOwnNote, b, shared)
	// …and give that address work in A. Without it no join reaches the
	// account: the card, the notes and the export all join accounts through
	// `assignments.volunteer`, and only /api/team would exercise the homonym.
	// `email_sent`, not `signed`: this row exists to make the joins reach the
	// account, not to give A coverage — the counters below assert that every
	// promise and every signature visible to A belongs to B.
	execAsMaintenance(t, s,
		"INSERT INTO assignments(org_id, insee_code, volunteer, status) "+
			"VALUES($1,'01005',$2,'email_sent')", a, shared)

	// A team request pending in B, and a team B already holds. The public
	// form's two early refusals — « that name is taken » and « a request is
	// already pending » — are reads on walled tables, and unwalled they answer
	// 409 on A's host: a campaign would learn its neighbours' team names by
	// trying them, and could freeze any name it guessed.
	execAsMaintenance(t, s,
		"INSERT INTO team_requests(org_id, name, departments, requester_email, "+
			"requester_name, message, state, ts) "+
			"VALUES($1,$2,'01',$3,'Demandeur de B',$4,'pending','2026-01-01T00:00')",
		b, neighbourRequestName, neighbourRequester, neighbourRequestMessage)
	execAsMaintenance(t, s,
		"INSERT INTO teams(org_id, name, departments) VALUES($1,$2,'01')",
		b, neighbourTeamName)

	// an account in A, to sign in with
	const mine = "a-only@exemple.fr"
	execAsMaintenance(t, s,
		"INSERT INTO accounts(org_id, email, name, password_hash, role, active) "+
			"VALUES($1,$2,'Coordination A',$3,'coordination',true)", a, mine, hash)

	c := clientOn(t, srv, testSlug+".paraphe.test")
	if code := c.signIn(mine, "motdepasse-de-test-1234"); code != http.StatusOK {
		t.Fatalf("sign-in on campaign A: %d", code)
	}

	leaks := func(what string, body string) {
		t.Helper()
		if strings.Contains(body, neighbourVolunteer) {
			t.Errorf("%s: the neighbouring campaign's volunteer appears", what)
		}
		if strings.Contains(body, neighbourNote) {
			t.Errorf("%s: the neighbouring campaign's note appears", what)
		}
		if strings.Contains(body, "Bénévole de B") {
			t.Errorf("%s: the neighbouring campaign's account appears", what)
		}
		if strings.Contains(body, "Homonyme chez B") {
			t.Errorf("%s: the same address exists in both campaigns, and the "+
				"neighbour's name is the one that came back", what)
		}
		// Everything else B holds that a route could hand over. The setup
		// writes each of them, so each needs a marker: B's candidate, its
		// personal note, its name and its slug all travel through
		// /api/config or /api/me.
		for marker, what2 := range map[string]string{
			neighbourCandidate:      "candidate",
			neighbourOwnNote:        "personal note",
			"Other campaign":        "name",
			"other":                 "slug",
			"beefbeefbeefbeef":      "logo",
			neighbourRequester:      "team requester",
			neighbourRequestMessage: "team request message",
		} {
			if strings.Contains(body, marker) {
				t.Errorf("%s: the neighbouring campaign's %s appears", what, what2)
			}
		}
	}

	// The dashboard answers in numbers. B holds one promise and two
	// signatures in department 01: if any of them counts here, the wall
	// leaked without a single one of B's strings appearing.
	code, dash := c.call(http.MethodGet, "/api/dashboard", nil)
	if code != http.StatusOK {
		t.Fatalf("/api/dashboard: %d", code)
	}
	stats, _ := dash["stats"].(map[string]any)
	for _, st := range []string{"promised", "signed"} {
		if n, _ := stats[st].(float64); n != 0 {
			t.Errorf("/api/dashboard: stats[%q] = %v, and every one of them "+
				"belongs to the neighbouring campaign", st, n)
		}
	}
	if n, _ := dash["departments_covered"].(float64); n != 0 {
		t.Errorf("/api/dashboard: departments_covered = %v, counted from the "+
			"neighbour's promises", n)
	}
	if promised, _ := dash["departments_with_promise"].([]any); len(promised) != 0 {
		t.Errorf("/api/dashboard: departments_with_promise = %v", promised)
	}

	// Writes. A campaign must not be able to touch its neighbour's rows, and
	// the read-only version of this test could not have told.
	before := neighbourRows(t, s, b)
	// The return codes are asserted, not discarded: a handler answering 400
	// writes nothing, the neighbour's rows are trivially unchanged, and the
	// check certifies a write that never happened.
	if code, rep := c.call(http.MethodPost, "/api/mayors/01001/status",
		map[string]any{"status": "to_call_back", "note": "note de A"}); code != http.StatusOK {
		t.Fatalf("A can no longer write a status on its own card: %d %v", code, rep)
	}
	if code, rep := c.call(http.MethodPost, "/api/batch",
		map[string]any{}); code != http.StatusOK {
		t.Fatalf("A can no longer draw a batch: %d %v", code, rep)
	}
	// An EMPTY BODY, not nil: `client.request` only sets Content-Type when a
	// body is given, and jsonOnly then answers 415 — which would make the
	// assertion below true of a request that never wrote anything.
	toggle, _ := c.call(http.MethodPost,
		"/api/team/account/"+neighbourVolunteer+"/active", map[string]any{})
	if toggle != http.StatusNotFound && toggle != http.StatusForbidden {
		t.Errorf("deactivating an account of B answered %d; expected 404 or "+
			"403 — anything else means the route did not even look", toggle)
	}
	// Three more writes on walled tables, none of them exercised before: a
	// team, an account bearing an address that ALSO exists in B, and the
	// campaign's own configuration — `orgs` carries no org_id, so nothing
	// protected it and its WHERE clause is the only wall there is.
	// The code, asserted like every other probe here: refused, this handler
	// writes nothing, and "the neighbour is untouched" then holds for a
	// reason that has nothing to do with a wall.
	if code, rep := c.call(http.MethodPost, "/api/team/group",
		map[string]any{"name": "Équipe de A",
			"departments": []string{"01"}}); code != http.StatusCreated {
		t.Fatalf("A can no longer create a team of its own: %d %v", code, rep)
	}
	// An address held by NEITHER campaign, and the code asserted. Aimed at
	// the shared address it answers 409 — the account already exists in A —
	// so nothing is written and the unique key masks whatever the wall does.
	if code, rep := c.call(http.MethodPost, "/api/team/account",
		map[string]any{"email": "nouveau-chez-a@exemple.fr", "name": "Nouveau",
			"role": "volunteer"}); code != http.StatusCreated {
		t.Fatalf("A can no longer create an account of its own: %d %v", code, rep)
	}
	// The public team request form, ANONYMOUS on A's host, once against the
	// name of a TEAM B holds and once against the name of a REQUEST pending in
	// B. Each drives one of the handler's two early refusals, and each must go
	// through: what B holds is invisible here.
	anon := clientOn(t, srv, testSlug+".paraphe.test")
	for what, name := range map[string]string{
		"a team B holds":         neighbourTeamName,
		"a request pending in B": neighbourRequestName,
	} {
		if code, rep := anon.call(http.MethodPost, "/api/team/request",
			map[string]any{"name": name, "departments": []string{"01"},
				"requester_name":  "Demandeur de A",
				"requester_email": "demandeur-a@exemple.fr"}); code != http.StatusCreated {
			t.Fatalf("A's public form refused the name of %s: %d %v — the "+
				"conflict was read across the wall", what, code, rep)
		}
	}
	// B's note, NAMED BY NUMBER on A's host. `notes.id` is one sequence for
	// the whole table, so the identifier is guessable by counting — and both
	// routes rewrite or destroy a row that exists. The 404 is what says the
	// row is bounded by the campaign, hence does not exist here; the readback
	// at the end of this test is what says nothing happened anyway.
	if code, _ := c.call(http.MethodPost,
		fmt.Sprintf("/api/mayors/01001/notes/%d", neighbourNoteID),
		map[string]any{"note": "réécrite depuis A"}); code != http.StatusNotFound {
		t.Errorf("A named B's note to rewrite it: %d, want 404", code)
	}
	if code, _ := c.call(http.MethodDelete,
		fmt.Sprintf("/api/mayors/01001/notes/%d", neighbourNoteID),
		nil); code != http.StatusNotFound {
		t.Errorf("A named B's note to delete it: %d, want 404", code)
	}
	// The return code, asserted: a handler that refuses writes nothing, and
	// "B is untouched" then holds for a reason that has nothing to do with a
	// wall. Three probes in this test have already passed that way.
	if code, rep := c.call(http.MethodPost, "/api/campaign",
		map[string]any{"campaign": map[string]string{
			"candidat": "Candidat de A"}}); code != http.StatusOK {
		t.Fatalf("A can no longer write its own campaign: %d %v", code, rep)
	}
	// The logo, which writes three more columns of `orgs` — the same table
	// and the same shape of WHERE clause, so the same thing to prove. Only
	// when the suite was given an object store: without one the handler
	// answers 501 and writes nothing, and "B is untouched" would then hold
	// for a reason that has nothing to do with a wall.
	if s.media != nil {
		if code, rep := c.call(http.MethodPost, "/api/campaign/logo",
			map[string]any{"data_uri": dataURI("image/png", rasterPNG(t, 12, 12))},
		); code != http.StatusOK {
			t.Fatalf("A can no longer set its own logo: %d %v", code, rep)
		}
		if code, rep := c.call(http.MethodDelete, "/api/campaign/logo",
			map[string]any{}); code != http.StatusOK {
			t.Fatalf("A can no longer remove its own logo: %d %v", code, rep)
		}
	} else {
		t.Log("no object store configured: the logo routes were NOT exercised " +
			"against the wall. Set PARAPHE_TEST_MEDIA_* (task garage) to cover them")
	}
	// And the personal note: an UPDATE on accounts that no test called. A
	// mutation adding `OR EXISTS(SELECT 1 FROM orgs WHERE id <> $2)` to it
	// overwrote the note of EVERY account of EVERY campaign, with all four
	// tests green.
	sharedClient := clientOn(t, srv, testSlug+".paraphe.test")
	if code := sharedClient.signIn(shared, "motdepasse-de-test-1234"); code != http.StatusOK {
		t.Fatalf("sign-in on A under the shared address: %d", code)
	}
	if code, rep := sharedClient.call(http.MethodPost, "/api/me/personal_note",
		map[string]any{"personal_note": "touche de A"}); code != http.StatusOK {
		t.Fatalf("A can no longer write its own personal note: %d %v", code, rep)
	}
	// Who the shared address IS, on A. readAccount runs on every authenticated
	// request and decides the name, the role and the team; unfiltered, it
	// returns B's account under A's subdomain. No other route this client
	// calls answers with any of the three, so nothing else here would notice.
	if code, rep := sharedClient.call(http.MethodGet, "/api/me", nil); code != http.StatusOK {
		t.Fatalf("/api/me on the shared address: %d %v", code, rep)
	} else {
		raw, err := json.Marshal(rep)
		if err != nil {
			t.Fatal(err)
		}
		leaks("/api/me on the shared address", string(raw))
	}

	after := neighbourRows(t, s, b)
	for table, n := range before {
		if after[table] != n {
			t.Errorf("B held %d rows in %s before A wrote, %d after",
				n, table, after[table])
		}
	}
	// A count is blind to an UPDATE. The neighbour's notes are read back.
	var strayed int
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"SELECT count(*) FROM notes WHERE org_id=$1 AND note LIKE '%de A%'",
			b).Scan(&strayed); err != nil {
			t.Fatal(err)
		}
	})
	if strayed != 0 {
		t.Errorf("%d note(s) written by A landed in campaign B", strayed)
	}
	// Counts move on INSERT and DELETE, never on UPDATE. What A could have
	// overwritten in B is read back by value.
	var bNote, bName, bCampaign string
	var bLogoKey, bLogoType string
	var bActive bool
	var bBatch int
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"SELECT COALESCE(personal_note,''), active FROM accounts "+
				"WHERE org_id=$1 AND email=$2", b, shared).Scan(&bNote, &bActive); err != nil {
			t.Fatal(err)
		}
		// All three columns /api/campaign writes, not just the one.
		if err := tx.QueryRow(context.Background(),
			"SELECT name, campaign::text, batch_size FROM orgs WHERE id=$1", b).
			Scan(&bName, &bCampaign, &bBatch); err != nil {
			t.Fatal(err)
		}
		// …and the three the logo writes. A DELETE that lost its WHERE
		// clause blanks these across every campaign, and no row count moves.
		if err := tx.QueryRow(context.Background(),
			"SELECT logo_key, logo_type FROM orgs WHERE id=$1", b).
			Scan(&bLogoKey, &bLogoType); err != nil {
			t.Fatal(err)
		}
	})
	if bLogoKey != neighbourLogoKey || bLogoType != "image/png" {
		t.Errorf("A's logo write reached B's row: key=%q type=%q",
			bLogoKey, bLogoType)
	}
	if bNote != neighbourOwnNote {
		t.Errorf("A's personal note reached B's account: %q", bNote)
	}
	if !bActive {
		t.Errorf("A deactivated an account of B")
	}
	if bName != "Other campaign" {
		t.Errorf("A rewrote B's campaign configuration: name is now %q", bName)
	}
	if !strings.Contains(bCampaign, neighbourCandidate) {
		t.Errorf("A rewrote B's campaign configuration: %s", bCampaign)
	}
	if strings.Contains(bCampaign, "Candidat de A") {
		t.Errorf("A's candidate landed in B's configuration: %s", bCampaign)
	}
	if bBatch != neighbourBatch {
		t.Errorf("A rewrote B's batch size: %d instead of %d", bBatch, neighbourBatch)
	}
	// Row counts move on INSERT and DELETE. Everything B holds can be
	// REWRITTEN in place without moving one — proven by an UPDATE that
	// rewrote B's card entirely while every count and every readback above
	// stayed as expected.
	var bStatus, bVolunteer, bTeamName, bCardNote string
	var bTeam *int
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"SELECT status, volunteer, team_id FROM assignments "+
				"WHERE org_id=$1 AND insee_code='01001'", b).
			Scan(&bStatus, &bVolunteer, &bTeam); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(context.Background(),
			"SELECT note FROM notes WHERE org_id=$1 AND insee_code='01001'", b).
			Scan(&bCardNote); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(context.Background(),
			"SELECT name FROM teams WHERE org_id=$1 ORDER BY id LIMIT 1", b).
			Scan(&bTeamName); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
	})
	if bStatus != "signed" || bVolunteer != neighbourVolunteer || bTeam != nil {
		t.Errorf("A rewrote B's card: status=%q volunteer=%q team=%v",
			bStatus, bVolunteer, bTeam)
	}
	if bCardNote != neighbourNote {
		t.Errorf("A rewrote B's note: %q", bCardNote)
	}
	_ = bTeamName

	for _, path := range []string{
		"/api/dashboard", "/api/mayors", "/api/team", "/api/config", "/api/me",
		"/api/facets", "/api/campaign/public",
		"/api/mayors/01001", "/api/export.csv",
	} {
		resp, err := c.http.Do(c.request(http.MethodGet, path, nil))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", path, resp.StatusCode)
			continue
		}
		leaks(path, string(raw))
	}
}

// neighbourRows: how many work rows the other campaign holds, read from the
// scope that crosses campaigns — the only place able to check that a write
// left them alone.
// neighbourRows: what the other campaign holds, table by table, read from
// the scope that crosses campaigns. Counting `assignments` alone let a note
// written by A land in B without a word.
func neighbourRows(t *testing.T, s *Server, org int) map[string]int {
	t.Helper()
	counts := map[string]int{}
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		for _, table := range walledTables {
			var n int
			if err := tx.QueryRow(context.Background(),
				"SELECT count(*) FROM "+table+" WHERE org_id=$1", org).Scan(&n); err != nil {
				t.Fatal(err)
			}
			counts[table] = n
		}
	})
	return counts
}
