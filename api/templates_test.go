package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// The Go copy of the engine's vocabulary against the file both languages
// answer to. The TypeScript canary beside it (`the placeholder manifest,
// shared with the API`) holds that file to what `fields()` actually produces,
// so the chain is: fields() → placeholders.json → this package.
//
// The direction that matters: a name HERE that the engine does not produce
// accepts a template the engine will refuse at render — which is the failure
// this whole validation exists to prevent, discovered by a volunteer whose
// card shows an error instead of a message.
func TestThePlaceholderVocabularyMatchesTheSharedFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "noyau", "placeholders.json"))
	if err != nil {
		t.Fatal(err)
	}
	var shared struct {
		Common      []string `json:"common"`
		HasEndorsed []string `json:"has_endorsed"`
		Discovery   []string `json:"discovery"`
	}
	if err := json.Unmarshal(raw, &shared); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		what   string
		mine   map[string]bool
		theirs []string
	}{
		{"common", commonPlaceholders, shared.Common},
		{"has_endorsed", endorserPlaceholders, shared.HasEndorsed},
		{"discovery", discoveryPlaceholders, shared.Discovery},
	} {
		expected := map[string]bool{}
		for _, k := range c.theirs {
			expected[k] = true
		}
		if len(expected) == 0 {
			t.Fatalf("placeholders.json carries no %s set to answer for", c.what)
		}
		if !reflect.DeepEqual(c.mine, expected) {
			t.Errorf("the %s placeholders and placeholders.json disagree:"+
				"\n Go:     %v\n shared: %v", c.what, keysOf(c.mine), c.theirs)
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// THE PROJECT'S CARDINAL MISTAKE, refused at save. Choosing the file by rank
// is not enough once campaigns edit the files: pasting the thank-you sentence
// into the discovery template printed « En , vous avez présenté . » to 32 866
// mayors, in silence.
func TestATemplateCannotBorrowTheOtherRanksPlaceholders(t *testing.T) {
	for _, c := range []struct{ file, placeholder string }{
		{"email_decouverte.txt", "candidat_recent"},
		{"email_decouverte.txt", "annee_recente"},
		{"courrier_decouverte.txt", "parrainages"},
		{"telephone_decouverte.txt", "candidat_recent"},
		// and symmetrically: the discovery context has nothing to say to
		// somebody who did endorse
		{"email.txt", "contexte"},
		{"telephone.txt", "contexte_tel"},
	} {
		body := "OBJET: x\nEn {" + c.placeholder + "}.\n"
		err := validateTemplate(c.file, body)
		if err == nil {
			t.Errorf("%s accepted {%s}, which belongs to the other rank",
				c.file, c.placeholder)
			continue
		}
		// BY NAME, and saying which audience: told only that the placeholder
		// is unknown, whoever pasted it looks for a typo in a word that is
		// spelt correctly
		if !strings.Contains(err.Error(), c.placeholder) {
			t.Errorf("%s: refusal does not name {%s}: %v",
				c.file, c.placeholder, err)
		}
	}
}

func TestAnUnknownPlaceholderIsRefusedWithTheAvailableOnes(t *testing.T) {
	// « {prénom} » is what a French speaker writes spontaneously, and `\w`
	// does not cover the é: it walked through an earlier guard and went out
	// in the clear in 1 953 emails.
	for _, hole := range []string{"prénom", "code-postal", "inconnu", "Nom"} {
		err := validateTemplate("courrier.txt", "Bonjour {"+hole+"},\n")
		if err == nil {
			t.Fatalf("{%s} was accepted", hole)
		}
		if !strings.Contains(err.Error(), "prenom") {
			t.Errorf("{%s}: the refusal should list the available fields: %v",
				hole, err)
		}
	}
	// a REAL field spelt with spaces around it designates that field, exactly
	// as the engine reads it
	if err := validateTemplate("courrier.txt", "{ commune }\n"); err != nil {
		t.Errorf("« { commune } » designates the commune: %v", err)
	}
}

// The engine reads the subject off the first line and throws without it, so a
// saved email template with no OBJET: is a campaign whose email channel is
// down until somebody edits it back.
func TestAnEmailTemplateWithoutItsSubjectIsRefused(t *testing.T) {
	for _, file := range []string{"email.txt", "email_decouverte.txt"} {
		if err := validateTemplate(file, "Bonjour {salutation},\n"); err == nil {
			t.Errorf("%s was accepted with no OBJET: line", file)
		}
		if err := validateTemplate(file, "\n  OBJET: x\ny\n"); err != nil {
			t.Errorf("%s: a blank line before OBJET: is trimmed by the engine "+
				"and must not be refused here: %v", file, err)
		}
	}
	// the other four carry no subject and must not be asked for one
	for _, file := range []string{"courrier.txt", "telephone_decouverte.txt"} {
		if err := validateTemplate(file, "Bonjour {salutation},\n"); err != nil {
			t.Errorf("%s does not carry a subject: %v", file, err)
		}
	}
}

func TestATemplateIsRefusedWhenItIsUnreadableOrTooLong(t *testing.T) {
	// a bidi override reorders what the volunteer proof-reads on screen
	// exactly as it reorders a label
	if err := validateTemplate("courrier.txt", "Bonjour\u202emonde\n"); err == nil {
		t.Error("a right-to-left override was accepted")
	}
	// but a template is nothing without its line breaks, and `legible` — the
	// reader for LABELS — refuses every control character there is
	if err := validateTemplate("courrier.txt", "un\ndeux\n\ttrois\n"); err != nil {
		t.Errorf("a template is written in paragraphs: %v", err)
	}
	if err := validateTemplate("courrier.txt",
		strings.Repeat("é", maxTemplateRunes+1)); err == nil {
		t.Error("a template past the ceiling was accepted")
	}
	// RUNES and not bytes, or a French template is refused for being long in
	// a unit nobody wrote it in
	if err := validateTemplate("courrier.txt",
		strings.Repeat("é", maxTemplateRunes)); err != nil {
		t.Errorf("%d runes is the ceiling, not %d bytes: %v",
			maxTemplateRunes, maxTemplateRunes, err)
	}
}

// AN EMPTY VALUE REMOVES THE OVERRIDE. That is what a textarea sends when
// somebody selects all and deletes, and it is exactly what « revenir au texte
// fourni » means — stored literally it would be a campaign whose letter
// renders as one blank page, five hundred times.
func TestAnEmptiedTemplateRemovesTheOverrideRatherThanStoringNothing(t *testing.T) {
	stored, err := storableTemplates(map[string]string{
		"email.txt":    "OBJET: x\ny\n",
		"courrier.txt": "   \n  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, still := stored["courrier.txt"]; still {
		t.Error("an emptied template was stored instead of removed")
	}
	if len(stored) != 1 {
		t.Errorf("expected the one real override, got %v", stored)
	}
	// CRLF folded at the door: a browser on Windows sends it, the engine folds
	// it at every render, and a column holding both spellings of one text is a
	// column where « did the coordination change this? » cannot be answered
	// by comparing.
	stored, err = storableTemplates(map[string]string{
		"courrier.txt": "un\r\ndeux\r\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := stored["courrier.txt"]; got != "un\ndeux\n" {
		t.Errorf("line endings not folded: %q", got)
	}
}

func TestATemplateNameOutsideTheSixIsRefused(t *testing.T) {
	// a name nothing renders is configuration a campaign believes it has
	// written and no mayor will ever read
	if _, err := storableTemplates(map[string]string{
		"emial.txt": "OBJET: x\ny\n",
	}); err == nil {
		t.Error("an unknown template name was accepted")
	}
	// AND when its value is empty, which is the shape that skips validation:
	// a client clearing « emial.txt » never wrote to a real template, and
	// saying so is how it finds out
	if _, err := storableTemplates(map[string]string{"emial.txt": ""}); err == nil {
		t.Error("an unknown template name was accepted when emptied")
	}
	// the six themselves, each with a body the engine accepts
	for _, file := range templateFiles {
		body := "Bonjour {salutation},\n"
		if strings.HasPrefix(file, "email") {
			body = "OBJET: x\n" + body
		}
		if _, err := storableTemplates(map[string]string{file: body}); err != nil {
			t.Errorf("%s is one of the six and was refused: %v", file, err)
		}
	}
}

// End to end, on the two screens that write them: a campaign rewrites its
// texts, one of its teams rewrites one again, and each account is told the two
// layers that apply to IT.
func TestACampaignAndItsTeamEachRewriteTheirOwnTemplates(t *testing.T) {
	s, srv := testServer(t)
	north := createTeam(t, s, "Nord", "01")
	south := createTeam(t, s, "Sud", "02")
	leadPw := createAccount(t, s, "nord@exemple.fr", RoleLead, &north)
	southPw := createAccount(t, s, "sud@exemple.fr", RoleVolunteer, &south)

	adminPw := createAccount(t, s, "coordination@exemple.fr", RoleCoordination, nil)
	admin := newClient(t, srv)
	if code := admin.signIn("coordination@exemple.fr", adminPw); code != http.StatusOK {
		t.Fatalf("coordination sign-in: %d", code)
	}
	const campaignLetter = "Texte de la campagne pour {salutation}.\n"
	if code, body := admin.call(http.MethodPost, "/api/campaign/templates",
		map[string]any{"templates": map[string]string{
			"courrier.txt": campaignLetter,
		}}); code != http.StatusOK {
		t.Fatalf("saving the campaign's templates: %d %v", code, body)
	}

	lead := newClient(t, srv)
	if code := lead.signIn("nord@exemple.fr", leadPw); code != http.StatusOK {
		t.Fatalf("lead sign-in: %d", code)
	}
	const teamLetter = "Texte de l'équipe Nord pour {salutation}.\n"
	if code, body := lead.call(http.MethodPost, "/api/team/templates",
		map[string]any{"templates": map[string]string{
			"courrier.txt": teamLetter,
		}}); code != http.StatusOK {
		t.Fatalf("saving the team's templates: %d %v", code, body)
	}

	// the lead's own account sees BOTH layers, told apart — the screen that
	// edits them has to know which text its team wrote and which it inherits
	campaign, team := layersOf(t, lead)
	if campaign["courrier.txt"] != campaignLetter {
		t.Errorf("the lead does not see the campaign layer: %v", campaign)
	}
	if team["courrier.txt"] != teamLetter {
		t.Errorf("the lead does not see its own layer: %v", team)
	}

	// a volunteer of ANOTHER team inherits the campaign's and not the Nord
	// team's: an override is one team's, and it does not cross
	other := newClient(t, srv)
	if code := other.signIn("sud@exemple.fr", southPw); code != http.StatusOK {
		t.Fatalf("volunteer sign-in: %d", code)
	}
	campaign, team = layersOf(t, other)
	if campaign["courrier.txt"] != campaignLetter {
		t.Errorf("the campaign's layer did not reach the other team: %v", campaign)
	}
	if len(team) != 0 {
		t.Errorf("another team's override crossed: %v", team)
	}

	// COORDINATION HAS NO TEAM, so it has no team layer and no row to write
	// one into — its texts are the campaign's, one route over
	campaign, team = layersOf(t, admin)
	if campaign["courrier.txt"] != campaignLetter || len(team) != 0 {
		t.Errorf("coordination: campaign=%v team=%v", campaign, team)
	}
	if code, _ := admin.call(http.MethodPost, "/api/team/templates",
		map[string]any{"templates": map[string]string{}}); code != http.StatusForbidden {
		t.Errorf("coordination writing a team layer: %d, want 403", code)
	}

	// and an override REMOVED goes back to inheriting, rather than to a blank
	// page: the whole point of a sparse overlay
	if code, _ := lead.call(http.MethodPost, "/api/team/templates",
		map[string]any{"templates": map[string]string{
			"courrier.txt": "",
		}}); code != http.StatusOK {
		t.Fatal("removing the team's override")
	}
	if _, team = layersOf(t, lead); len(team) != 0 {
		t.Errorf("the override survived its removal: %v", team)
	}
}

// A REFUSAL WRITES NOTHING, and the sentence names the file. Asserted on the
// STATUS first: a handler answering 400 stores nothing for reasons unrelated
// to any validation, so « the column is unchanged » would hold either way.
func TestATemplateTheEngineWouldRefuseIsNeverStored(t *testing.T) {
	s, srv := testServer(t)
	adminPw := createAccount(t, s, "coordination@exemple.fr", RoleCoordination, nil)
	admin := newClient(t, srv)
	if code := admin.signIn("coordination@exemple.fr", adminPw); code != http.StatusOK {
		t.Fatalf("coordination sign-in: %d", code)
	}
	good := map[string]string{"courrier.txt": "Bonjour {salutation}.\n"}
	if code, _ := admin.call(http.MethodPost, "/api/campaign/templates",
		map[string]any{"templates": good}); code != http.StatusOK {
		t.Fatal("the valid save should have gone through")
	}
	for _, c := range []struct {
		what      string
		templates map[string]string
	}{
		{"an unknown placeholder", map[string]string{
			"courrier.txt": "Bonjour {prénom}.\n"}},
		{"the other rank's placeholder", map[string]string{
			"courrier_decouverte.txt": "En {annee_recente}.\n"}},
		{"an email with no subject", map[string]string{
			"email.txt": "Bonjour {salutation}.\n"}},
		{"an unknown file", map[string]string{
			"courriel.txt": "Bonjour.\n"}},
	} {
		code, body := admin.call(http.MethodPost, "/api/campaign/templates",
			map[string]any{"templates": c.templates})
		if code != http.StatusBadRequest {
			t.Errorf("%s: answered %d, want 400", c.what, code)
			continue
		}
		if msg, _ := body["error"].(string); msg == "" {
			t.Errorf("%s: refused with no sentence to act on", c.what)
		}
		// and the campaign still sends what it sent before the refusal
		campaign, _ := layersOf(t, admin)
		if campaign["courrier.txt"] != good["courrier.txt"] {
			t.Fatalf("%s: the refusal overwrote the stored templates: %v",
				c.what, campaign)
		}
	}
}

// layersOf: the two template layers /api/me reports for this client.
func layersOf(t *testing.T, c *client) (map[string]string, map[string]string) {
	t.Helper()
	code, body := c.call(http.MethodGet, "/api/me", nil)
	if code != http.StatusOK {
		t.Fatalf("/api/me: %d", code)
	}
	layers, ok := body["templates"].(map[string]any)
	if !ok {
		t.Fatalf("/api/me carries no template layers: %v", body)
	}
	read := func(key string) map[string]string {
		out := map[string]string{}
		for k, v := range layers[key].(map[string]any) {
			out[k], _ = v.(string)
		}
		return out
	}
	return read("campaign"), read("team")
}

// THE THREE DOORS SAY THE SAME THING ABOUT THE ACCOUNT BEHIND THEM.
//
// /api/me, signing in and redeeming a link all answer the same shape, and it
// used to be written out at two of them. The templates were added to one: a
// volunteer who signed in and went straight to a card rendered from the
// image's texts while their campaign's own sat unused, until they happened to
// reload — and every unit test stayed green, because each door was asserted
// on its own.
//
// Compared by KEY and not by value: `departments` is legitimately the same
// here and the point is the shape, not the row.
func TestSigningInSaysTheSameThingAsMe(t *testing.T) {
	s, srv := testServer(t)
	team := createTeam(t, s, "Nord", "01")
	pw := createAccount(t, s, "referent@exemple.fr", RoleLead, &team)

	c := newClient(t, srv)
	code, signedIn := c.call(http.MethodPost, "/api/session",
		map[string]string{"email": "referent@exemple.fr", "password": pw})
	if code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	code, mine := c.call(http.MethodGet, "/api/me", nil)
	if code != http.StatusOK {
		t.Fatalf("/api/me: %d", code)
	}
	if !reflect.DeepEqual(keysIn(signedIn), keysIn(mine)) {
		t.Errorf("signing in and /api/me describe the account differently:"+
			"\n session: %v\n me:      %v", keysIn(signedIn), keysIn(mine))
	}
	// and the templates are actually there, or the comparison above would
	// hold just as well with both of them missing
	if _, ok := signedIn["templates"]; !ok {
		t.Error("signing in carries no templates")
	}

	// THE THIRD DOOR, and it is the one that would have been forgotten: a
	// volunteer invited by email arrives through the link, not the form.
	withMailer(t, s, "https://campagne.exemple.fr")
	link := newClient(t, srv)
	token := askForLink(t, s, link, "referent@exemple.fr")
	code, opened := redeem(t, link, token)
	if code != http.StatusOK {
		t.Fatalf("redeeming: %d %v", code, opened)
	}
	if !reflect.DeepEqual(keysIn(opened), keysIn(mine)) {
		t.Errorf("a link and /api/me describe the account differently:"+
			"\n link: %v\n me:   %v", keysIn(opened), keysIn(mine))
	}
}

func keysIn(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// A SAVE AT EVERY CEILING THE ROUTE ITSELF ALLOWS, in 4-byte runes.
//
// The arithmetic in TestTheBodyCeilingHoldsBothEdges says the six fit under
// `maxBodySize`; this is the request that proves it, and it is the one a
// campaign writing six long templates actually sends. Refused, it answers 413
// with nothing on screen saying which limit was the problem.
func TestSixTemplatesAtEveryCeilingStillFitInOneBody(t *testing.T) {
	s, srv := testServer(t)
	pw := createAccount(t, s, "coordination@exemple.fr", RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn("coordination@exemple.fr", pw); code != http.StatusOK {
		t.Fatalf("coordination sign-in: %d", code)
	}
	templates := map[string]string{}
	for _, file := range templateFiles {
		// 𝄞 is four bytes, the worst case UTF-8 allows, and it is graphic:
		// `legibleText` refuses controls and bidi reordering, not music.
		text := strings.Repeat("𝄞", maxTemplateRunes)
		if strings.HasPrefix(file, "email") {
			const subject = "OBJET: x\n"
			text = subject + strings.Repeat("𝄞",
				maxTemplateRunes-utf8.RuneCountInString(subject))
		}
		if n := utf8.RuneCountInString(text); n != maxTemplateRunes {
			t.Fatalf("%s: the fixture is %d runes, not the ceiling", file, n)
		}
		templates[file] = text
	}
	code, answer := c.call(http.MethodPost, "/api/campaign/templates",
		map[string]any{"templates": templates})
	if code != http.StatusOK {
		t.Fatalf("six templates at the ceiling were refused: %d %v — the body "+
			"limit is below the sum of the route's own ceilings", code, answer)
	}
	// …and all six were stored, or the request fit because something dropped
	campaign, _ := layersOf(t, c)
	if len(campaign) != len(templateFiles) {
		t.Errorf("stored %d of %d templates", len(campaign), len(templateFiles))
	}
}

// THE ACCOUNT-LESS VERSION ADOPTS THE CAMPAIGN'S TEXTS, and this is the wire
// it reads them off.
//
// Without them a campaign that had rewritten its letter spoke with two voices
// — one to the volunteers with an account, one to the volunteers without —
// and nothing on either screen said which. Only the CAMPAIGN's layer: a
// team's overlay is its team's, and that mode has no team.
func TestTheBrowserVersionIsOfferedTheCampaignsOwnTexts(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	org := orgID(t, s, testSlug)
	team := createTeamIn(t, s, org, "Nord", "01")
	execAsMaintenance(t, s,
		"UPDATE teams SET templates=$3::jsonb WHERE org_id=$1 AND id=$2",
		org, team, `{"courrier.txt":"Le texte de l'équipe Nord."}`)

	public := func() map[string]any {
		t.Helper()
		c := clientOn(t, srv, testSlug+".paraphe.test")
		code, body := c.call(http.MethodGet, "/api/campaign/public", nil)
		if code != http.StatusOK {
			t.Fatalf("/api/campaign/public: %d %v", code, body)
		}
		return body
	}

	// nothing rewritten: an empty overlay, which IS the shipped texts
	if got, _ := public()["templates"].(map[string]any); len(got) != 0 {
		t.Errorf("a campaign that rewrote nothing offers %v", got)
	}

	const own = "Le texte de la campagne pour {salutation}.\n"
	execAsMaintenance(t, s,
		"UPDATE orgs SET templates=$2::jsonb WHERE id=$1", org,
		`{"courrier.txt":`+strconv.Quote(own)+`}`)

	offered, _ := public()["templates"].(map[string]any)
	if offered["courrier.txt"] != own {
		t.Errorf("the campaign's own letter is not offered: %v", offered)
	}
	// and the TEAM's is not: it crossed no wall to get here, and this mode
	// has no team to be one of
	for _, text := range offered {
		if text == "Le texte de l'équipe Nord." {
			t.Error("a team's overlay reached the account-less version")
		}
	}
}

// A CARD NOBODY WILL WORK GOES BACK IN THE POOL when the access closes.
//
// `/api/batch` draws where `volunteer IS NULL`, and nothing cleared it: every
// card a departing volunteer had been handed and not touched stayed reserved
// to an account that can no longer sign in, and came up in no other batch for
// anybody, for ever. Ten per departure, in a campaign that needs five hundred
// signatures.
func TestClosingAnAccessGivesBackTheCardsNobodyWorked(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 4, "01")
	adminPw := createAccount(t, s, "coordination@exemple.fr", RoleCoordination, nil)
	pw := createAccount(t, s, "partante@exemple.fr", RoleVolunteer, nil)

	her := newClient(t, srv)
	if code := her.signIn("partante@exemple.fr", pw); code != http.StatusOK {
		t.Fatalf("volunteer sign-in: %d", code)
	}
	if code, body := her.call(http.MethodPost, "/api/batch", map[string]any{}); code != http.StatusOK {
		t.Fatalf("taking a batch: %d %v", code, body)
	}
	held := scalar[int](t, s,
		"SELECT COUNT(*) FROM assignments WHERE volunteer=$1", "partante@exemple.fr")
	if held == 0 {
		t.Fatal("the batch reserved nothing: this test would prove nothing")
	}
	// one of them WORKED, so it is not an untouched card any more
	var worked string
	asMaintenance(t, s.pool, func(tx pgx.Tx) {
		if err := tx.QueryRow(context.Background(),
			"UPDATE assignments SET status='email_sent' WHERE volunteer=$1 "+
				"AND insee_code=(SELECT MIN(insee_code) FROM assignments "+
				"WHERE volunteer=$1) RETURNING insee_code",
			"partante@exemple.fr").Scan(&worked); err != nil {
			t.Fatal(err)
		}
	})

	admin := newClient(t, srv)
	if code := admin.signIn("coordination@exemple.fr", adminPw); code != http.StatusOK {
		t.Fatalf("coordination sign-in: %d", code)
	}
	if code, body := admin.call(http.MethodPost,
		"/api/team/account/partante@exemple.fr/active", map[string]any{}); code != http.StatusOK {
		t.Fatalf("closing the access: %d %v", code, body)
	}

	// the untouched ones are free again…
	if n := scalar[int](t, s,
		"SELECT COUNT(*) FROM assignments WHERE volunteer=$1 AND status='to_contact'",
		"partante@exemple.fr"); n != 0 {
		t.Errorf("%d untouched cards are still reserved to a closed access", n)
	}
	// …and what she actually worked is untouched: the status is not hers to
	// lose by leaving, and the next volunteer reads it
	if st := scalar[string](t, s,
		"SELECT status FROM assignments WHERE insee_code=$1", worked); st != "email_sent" {
		t.Errorf("the worked card lost its status: %q", st)
	}
	if v := scalar[string](t, s,
		"SELECT COALESCE(volunteer,'') FROM assignments WHERE insee_code=$1",
		worked); v != "partante@exemple.fr" {
		t.Errorf("the worked card was released too: %q", v)
	}

	// and the pool has them back: a colleague draws them
	colleaguePw := createAccount(t, s, "reste@exemple.fr", RoleVolunteer, nil)
	other := newClient(t, srv)
	if code := other.signIn("reste@exemple.fr", colleaguePw); code != http.StatusOK {
		t.Fatalf("colleague sign-in: %d", code)
	}
	if code, _ := other.call(http.MethodPost, "/api/batch", map[string]any{}); code != http.StatusOK {
		t.Fatal("the colleague could not take a batch")
	}
	if n := scalar[int](t, s,
		"SELECT COUNT(*) FROM assignments WHERE volunteer=$1", "reste@exemple.fr"); n == 0 {
		t.Error("the released cards did not come back into the pool")
	}
}
