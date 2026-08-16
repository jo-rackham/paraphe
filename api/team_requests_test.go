package main

import (
	"encoding/json"
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

// The ceiling bounds STORAGE and hides NOTHING — that is what the constant's
// comment promises the coordination. A ceiling read and then written to in
// two statements lets concurrent inserts past it, and a queue that reads the
// newest 200 then drops the OLDEST pending requests off the only screen that
// can accept them: the legitimate early ones, in favour of whatever arrived
// last. The invariant is asserted where it can be: on the read.
func TestTheQueueHidesNoPendingRequestEvenOverTheCeiling(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)
	password := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)

	// past the ceiling, as a race is able to leave it
	const over = maxPendingTeamRequests + 25
	for i := range over {
		execAsMaintenance(t, s,
			"INSERT INTO team_requests(org_id, name, departments, requester_email, "+
				"requester_name, message, state, ts) "+
				"VALUES($1,$2,'','qui@exemple.fr','Qui','','pending','2026-01-01T00:00')",
			org, fmt.Sprintf("Équipe %04d", i))
	}

	coord := newClient(t, srv)
	if code := coord.signIn("coord@exemple.fr", password); code != http.StatusOK {
		t.Fatalf("coordination sign-in: %d", code)
	}
	code, payload := coord.call(http.MethodGet, "/api/team", nil)
	if code != http.StatusOK {
		t.Fatalf("/api/team: %d", code)
	}
	requests, _ := payload["requests"].([]any)
	if len(requests) != over {
		t.Fatalf("the queue shows %d of %d pending requests: %d are hidden from "+
			"the only screen that can accept them, and the oldest go first",
			len(requests), over, over-len(requests))
	}
	// and the earliest one — the one a flood pushes off the page — is there
	raw, err := json.Marshal(requests)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Équipe 0000") {
		t.Error("the oldest pending request is not on the screen")
	}
}

// The two early refusals told a stranger WHICH door they hit: a real team, or
// somebody else's pending request. Team names are internal to a campaign —
// neither /api/config nor /api/campaign/public carries them — so the pair was
// an enumeration oracle, ten a source per hour, for a visitor with no account.
func TestTheFormRefusesATakenNameWithoutSayingWhatHoldsIt(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)
	createTeamIn(t, s, org, "Équipe qui existe", "01")
	c := newClient(t, srv)

	if code, _ := c.call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe demandée", "01")); code != http.StatusCreated {
		t.Fatal("the public form did not accept the first request")
	}
	_, onATeam := c.call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe qui existe", "01"))
	_, onARequest := c.call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe demandée", "01"))

	team, _ := onATeam["error"].(string)
	request, _ := onARequest["error"].(string)
	if team == "" || request == "" {
		t.Fatalf("no refusal came back: %q / %q", team, request)
	}
	// the NAME may be quoted — the sender typed it. What must not differ is
	// what the sentence says about the campaign's own rows.
	if strings.ReplaceAll(team, "Équipe qui existe", "X") !=
		strings.ReplaceAll(request, "Équipe demandée", "X") {
		t.Errorf("the two refusals differ, so they tell a stranger which door "+
			"a name hit:\n\tteam:    %s\n\trequest: %s", team, request)
	}
}

// A name is read by the coordination before it decides. What is refused is
// what makes the screen disagree with the row: an override reverses the
// display without changing a byte, a separator turns one label into two.
func TestTheFormRefusesANameThatReordersOrBreaks(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)
	c := newClient(t, srv)

	// ESCAPED, never written literally: a source file carrying an override
	// reads reversed in the editor of whoever opens it next, which is the
	// very thing being refused here.
	for _, probe := range []struct{ what, name, requester string }{
		{"a right-to-left override", "Innocent\u202eiliaM", "Qui"},
		{"a bidi isolate", "\u2066Équipe\u2069", "Qui"},
		{"an ANSI escape", "Équipe\x1b[31m", "Qui"},
		{"a line break", "Équipe\nAutre ligne", "Qui"},
		// the two characters that ARE Unicode's line separators, and which a
		// blanket Cc/Cf test let straight through
		{"a line separator", "Équipe\u2028Autre ligne", "Qui"},
		{"a paragraph separator", "Équipe\u2029Autre ligne", "Qui"},
		{"a byte-order mark", "\ufeffÉquipe", "Qui"},
		{"an override in the requester's name", "Équipe", "Qui\u202emoc"},
	} {
		body := teamRequestBody(probe.name, "01")
		body["requester_name"] = probe.requester
		if code, _ := c.call(http.MethodPost, "/api/team/request", body); code !=
			http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", probe.what, code)
		}
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM team_requests WHERE org_id=$1",
		org); n != 0 {
		t.Fatalf("%d request(s) carrying a reordering character were stored", n)
	}
}

// …and what it must NOT refuse. A guard on the only door an outsider has
// costs as much when it turns away a real name as when it lets an attack
// through, and it is far harder to notice: the person is told no, and leaves.
// Every name below was refused by the blanket Cc/Cf test this replaced.
func TestTheFormAcceptsNamesThatMerelyShapeTheirGlyphs(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)
	c := newClient(t, srv)

	for _, probe := range []struct{ what, name, requester string }{
		// Persian orthography REQUIRES the zero-width non-joiner: without it
		// the two words of this given name merge into one
		{"a Persian compound given name", "Équipe de Lyon",
			"میر\u200cحسین"},
		// Devanagari and Sinhala conjuncts are written with the joiner
		{"a Devanagari conjunct", "Équipe क्\u200dष", "Qui"},
		// and so is every emoji built by joining
		{"a family emoji",
			"Familles \U0001f468\u200d\U0001f469\u200d\U0001f467", "Qui"},
		// the directional MARKS hold a Latin fragment in place inside an
		// Arabic name; they reorder nothing
		{"an Arabic name with a directional mark", "Équipe",
			"أحمد\u200f"},
		{"a soft hyphen from a word processor", "Jean\u00adPaul", "Qui"},
	} {
		body := teamRequestBody(probe.name, "01")
		body["requester_name"] = probe.requester
		if code, rep := c.call(http.MethodPost, "/api/team/request", body); code !=
			http.StatusCreated {
			t.Errorf("%s was turned away: %d %v", probe.what, code, rep)
		}
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM team_requests WHERE org_id=$1",
		org); n != 5 {
		t.Fatalf("%d of 5 legitimate names got through", n)
	}
}

// The same door, one level up: the instance's hosting form is as anonymous,
// and an administrator reads its name before approving it. The refusal lives
// in one helper, so the two forms cannot drift.
func TestTheHostingFormRefusesInvisibleCharactersToo(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	c := clientOn(t, srv, "paraphe.test")

	code, _ := c.call(http.MethodPost, "/api/request", map[string]any{
		"slug": "nouvelle", "name": "Campagne\u202eeénnoisrevni",
		"requester_name": "Qui", "requester_email": "qui@exemple.fr",
	})
	if code != http.StatusBadRequest {
		t.Errorf("a right-to-left override in a campaign name: %d, want 400", code)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM hosting_requests"); n != 0 {
		t.Fatalf("%d hosting request(s) carrying an override were stored", n)
	}
}

// …and every other reading the campaign's form applies, because the two doors
// are the same door one level apart. Hardening one and not the other is the
// mistake this file exists to refuse: an administrator reads these rows on
// the only screen that can approve them.
func TestTheHostingFormReadsItsNamesLikeTheCampaignsForm(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	c := clientOn(t, srv, "paraphe.test")

	// THREE probes, one per guard, and no more: `limitHostingIP` is 3 an hour
	// and a fourth would be refused for a reason that has nothing to do with
	// what is asserted here.
	for _, probe := range []struct {
		what, name, requester, email string
	}{
		// `visible`
		{"a name of nothing but zero-width runes", "\u200b\u2060", "Qui",
			"qui@exemple.fr"},
		// `legible`, and ONLY it: U+2028 is `Zl`, so neither the `Cf` sweep
		// of `storableEmail` nor the `< 0x20` of `safeAddress` sees it
		{"a line separator in the address", "Campagne", "Qui",
			"victime\u2028@exemple.fr"},
		// `storableEmail`: reads as « admin@exemple.fr » on the screen that
		// approves it, and is stored as something else
		{"a zero-width space in the address", "Campagne", "Qui",
			"admin\u200b@exemple.fr"},
	} {
		code, _ := c.call(http.MethodPost, "/api/request", map[string]any{
			"slug": "nouvelle", "name": probe.name,
			"requester_name": probe.requester, "requester_email": probe.email,
		})
		if code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", probe.what, code)
		}
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM hosting_requests"); n != 0 {
		t.Fatalf("%d hosting request(s) the administrator cannot read were stored", n)
	}
}

// An address is not a name. `legible` must allow the format characters a name
// needs — the Persian zero-width non-joiner, the Devanagari joiner — and an
// address needs none of them: one carrying a zero-width space reads as
// `admin@exemple.fr` and is stored as something else, so the account the
// coordination believes it is opening is not the one it opens.
func TestAnAddressCarriesNoFormatCharacter(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)
	c := newClient(t, srv)

	for _, probe := range []struct{ what, email string }{
		{"a zero-width space", "admin\u200bistrateur@exemple.fr"},
		{"a zero-width non-joiner", "admin\u200c@exemple.fr"},
		{"a zero-width joiner", "admin\u200d@exemple.fr"},
		{"a word joiner", "admin\u2060@exemple.fr"},
		{"a soft hyphen", "admin\u00ad@exemple.fr"},
		{"a left-to-right mark", "admin\u200e@exemple.fr"},
	} {
		body := teamRequestBody("Équipe "+probe.what, "01")
		body["requester_email"] = probe.email
		if code, _ := c.call(http.MethodPost, "/api/team/request", body); code !=
			http.StatusBadRequest {
			t.Errorf("%s in an address: %d, want 400", probe.what, code)
		}
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM team_requests WHERE org_id=$1",
		org); n != 0 {
		t.Fatalf("%d request(s) with a look-alike address were stored", n)
	}
	// and a plain address still gets through: this refuses a character class,
	// not an alphabet
	if code, rep := c.call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe du Nord", "01")); code != http.StatusCreated {
		t.Fatalf("an ordinary address was turned away: %d %v", code, rep)
	}
}

// A name can be present, non-empty, legible — and still show the moderator
// nothing. `strings.TrimSpace` trims spaces, and the zero-width runes are not
// spaces: they are `Cf`, and a name made only of them reached the queue as a
// blank row indistinguishable from the next blank row.
func TestTheFormRefusesANameNothingCanBeSeenIn(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)
	c := newClient(t, srv)

	for _, probe := range []struct{ what, name, requester string }{
		{"a zero-width space", "\u200b", "Qui"},
		{"a word joiner", "\u2060", "Qui"},
		{"a soft hyphen", "\u00ad", "Qui"},
		{"a zero-width non-joiner", "\u200c", "Qui"},
		{"several invisible runes", "\u200b\u2060\u00ad", "Qui"},
		{"invisible runes around a space", "\u200b \u2060", "Qui"},
		// an Ogham space mark is a SPACE (Zs), so a name of nothing but that
		// is a name of nothing but spaces
		{"an Ogham space mark", "\u1680", "Qui"},
		{"the same in the requester's name", "Équipe", "\u200b\u2060"},
	} {
		body := teamRequestBody(probe.name, "01")
		body["requester_name"] = probe.requester
		if code, _ := c.call(http.MethodPost, "/api/team/request", body); code !=
			http.StatusBadRequest {
			t.Errorf("%s as the whole name: %d, want 400", probe.what, code)
		}
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM team_requests WHERE org_id=$1",
		org); n != 0 {
		t.Fatalf("%d request(s) with nothing visible in them were stored", n)
	}

	// The line held here is "carries something graphic", not "carries no rune
	// that renders blank" — that second list has no end. U+3164 is a letter
	// and U+2800 a symbol: they pass, deliberately, and telling two labels
	// apart stays a matter of comparison. Pinned so that tightening it later
	// is a decision someone takes, not a side effect.
	for _, probe := range []struct{ what, name string }{
		{"a Hangul filler", "\u3164"},
		{"a blank Braille pattern", "\u2800"},
	} {
		if code, rep := c.call(http.MethodPost, "/api/team/request",
			teamRequestBody(probe.what+" "+probe.name, "01")); code !=
			http.StatusCreated {
			t.Errorf("%s was turned away: %d %v", probe.what, code, rep)
		}
	}
}

// The address is not only read by the coordination: on acceptance it BECOMES
// the primary key of the lead's account. A carriage return in it opens a team
// whose lead can never type their own login, and the screen that shows the
// address renders it differently from what is stored.
func TestTheFormRefusesAnAddressThatCannotBeTypedBack(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)
	c := newClient(t, srv)

	for _, probe := range []struct{ what, email string }{
		{"a carriage return", "victime\r@exemple.fr"},
		{"a line feed", "victime\n@exemple.fr"},
		{"a line separator", "victime\u2028@exemple.fr"},
		{"a right-to-left override", "victime\u202e@exemple.fr"},
		{"a byte-order mark", "\ufeffvictime@exemple.fr"},
	} {
		body := teamRequestBody("Équipe "+probe.what, "01")
		body["requester_email"] = probe.email
		if code, _ := c.call(http.MethodPost, "/api/team/request", body); code !=
			http.StatusBadRequest {
			t.Errorf("%s in the address: %d, want 400", probe.what, code)
		}
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM team_requests WHERE org_id=$1",
		org); n != 0 {
		t.Fatalf("%d request(s) with an unusable address were stored", n)
	}
}

// `team_requests.id` is ONE sequence for the whole table, hence for every
// campaign on the instance. Handed back to an anonymous visitor, the gap
// between two of them counts what the neighbouring campaigns received — a
// number of the neighbour's, which is exactly what no campaign may see of
// another. The coordination finds the request in its queue instead.
func TestThePublicFormReturnsNoInstanceWideIdentity(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	c := newClient(t, srv)

	code, rep := c.call(http.MethodPost, "/api/team/request",
		teamRequestBody("Équipe du Nord", "01"))
	if code != http.StatusCreated {
		t.Fatalf("the public form: %d %v", code, rep)
	}
	for _, forbidden := range []string{"id", "org_id", "ts"} {
		if _, present := rep[forbidden]; present {
			t.Errorf("the anonymous answer carries %q: %v", forbidden, rep)
		}
	}
	// and the row exists all the same: nothing was traded for the silence
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM team_requests WHERE "+
		"org_id=$1 AND state='pending'", orgID(t, s, testSlug)); n != 1 {
		t.Fatalf("%d pending requests, want 1", n)
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
// Accepting a request opens an account, and an account nobody can be told
// about is an account nobody enters. The password comes back ONCE, in this
// answer: a closed tab was the whole story, and the coordination's only
// recourse was to open the access again. The direct creation of an access and
// the hosting approval both send an invitation; this door was written before
// there was a relay to send with, and it did not.
func TestAcceptingATeamRequestInvitesTheLeadItJustOpened(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	mails := withMailer(t, s, "https://campagne.exemple.fr")
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
		map[string]any{"decision": RequestAccepted})
	if code != http.StatusOK {
		t.Fatalf("accepting: %d %v", code, body)
	}
	if body["invitation_sent"] != true {
		t.Errorf("invitation_sent = %v: the lead was opened and never written to",
			body["invitation_sent"])
	}
	// and the password stays, deliberately: the relay may be down tomorrow,
	// and reading it out is the path that has always worked
	if p, _ := body["password"].(string); p == "" {
		t.Error("the one-time password left the answer")
	}

	sent := mails.only(t)
	if sent.to != requesterAddress {
		t.Fatalf("the invitation went to %q, not to the person who asked", sent.to)
	}
	if !strings.Contains(sent.body, "/connexion#jeton=") {
		t.Errorf("the invitation carries no link:\n%s", sent.body)
	}
}

// Hardening the FORM hardens what it writes from now on, and nothing that is
// already in the table. Rows filed before it are still pending on a live
// instance, and this is the only place left to catch one: the address becomes
// the account's primary key, so a moderator who accepts it opens a login
// nobody can ever type. Refusing the request stays open.
func TestAcceptingARowFiledBeforeTheGuardsRefusesRatherThanOpensIt(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
	org := orgID(t, s, testSlug)
	password := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)

	// written straight to the table, as the form used to allow: the address
	// reads as « ancienne@exemple.fr » and is stored as something else
	execAsMaintenance(t, s,
		"INSERT INTO team_requests(org_id, name, departments, requester_email, "+
			"requester_name, message, state, ts) "+
			"VALUES($1,'Équipe ancienne','01',$2,'Qui','','pending','2026-01-01T00:00')",
		org, "ancienne\u200b@exemple.fr")
	id := scalar[int64](t, s, "SELECT id FROM team_requests WHERE org_id=$1", org)

	coord := newClient(t, srv)
	if code := coord.signIn("coord@exemple.fr", password); code != http.StatusOK {
		t.Fatalf("coordination sign-in: %d", code)
	}
	code, body := coord.call(http.MethodPost, fmt.Sprintf("/api/team/requests/%d", id),
		map[string]any{"decision": RequestAccepted})
	if code != http.StatusConflict {
		t.Fatalf("accepting a row written before the guards: %d %v, want 409",
			code, body)
	}
	// asserted on the CODE first: a handler answering 409 writes nothing, and
	// « no account exists » would hold for that reason alone
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM accounts WHERE org_id=$1 AND "+
		"email LIKE 'ancienne%'", org); n != 0 {
		t.Errorf("%d account(s) opened on an address nobody can type", n)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM teams WHERE org_id=$1", org); n != 0 {
		t.Errorf("%d team(s) opened for a request that was refused", n)
	}
	// and the request is untouched, so the coordination can still refuse it
	if st := scalar[string](t, s, "SELECT state FROM team_requests WHERE id=$1",
		id); st != RequestPending {
		t.Errorf("the request is %q: it must stay refusable", st)
	}
}

// The coordination EDITS the name as it accepts, and that name becomes
// `teams.name`, read by every volunteer of the campaign. The public form
// checks it; this door did not.
func TestTheEditedTeamNameIsReadLikeTheSubmittedOne(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 3, "01")
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
	for _, probe := range []struct{ what, name string }{
		{"a right-to-left override", "Équipe\u202edroN ud"},
		{"nothing visible at all", "\u200b\u2060"},
	} {
		code, body := coord.call(http.MethodPost,
			fmt.Sprintf("/api/team/requests/%d", id),
			map[string]any{"decision": RequestAccepted, "name": probe.name})
		if code != http.StatusBadRequest {
			t.Errorf("%s in the edited name: %d %v, want 400", probe.what, code, body)
		}
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM teams WHERE org_id=$1", org); n != 0 {
		t.Fatalf("%d team(s) opened under a name the volunteers cannot read", n)
	}
}

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
