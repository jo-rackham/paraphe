package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// A note is what a volunteer wrote down while somebody was on the phone. It
// was DEFINITIVE: a typo, a note taken on the wrong commune, a word that has
// no business in a register the whole campaign reads — the only remedy was an
// UPDATE typed against production, which is the one kind of access nobody can
// audit.
//
// Two doors now, and they are not the same door. The AUTHOR corrects their own
// words; the COORDINATION removes any note and rewrites none — putting
// different words under somebody else's name is « whoever sends it is whoever
// signs it », one register down.

// noteRows: the history of a card as the API hands it over.
func noteRows(t *testing.T, rep map[string]any) []map[string]any {
	t.Helper()
	raw, _ := rep["notes"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, n := range raw {
		row, ok := n.(map[string]any)
		if !ok {
			t.Fatalf("a history line is not an object: %#v", n)
		}
		out = append(out, row)
	}
	return out
}

// noteIDOf: the identifier of the history line carrying `note`, which is what
// both routes need to name one.
func noteIDOf(t *testing.T, rep map[string]any, note string) int64 {
	t.Helper()
	for _, row := range noteRows(t, rep) {
		if text(row["note"]) != note {
			continue
		}
		n, ok := row["id"].(float64)
		if !ok {
			t.Fatalf("the history carries no usable id: %#v", row["id"])
		}
		return int64(n)
	}
	t.Fatalf("no history line reads %q", note)
	return 0
}

// write records a status through the route that produces notes, so the ids
// and the head this file reasons about are the real ones.
func write(t *testing.T, c *client, insee, status, note, seen string) map[string]any {
	t.Helper()
	code, rep := c.call(http.MethodPost, "/api/mayors/"+insee+"/status",
		map[string]string{"status": status, "note": note, "seen": seen})
	if code != http.StatusOK {
		t.Fatalf("recording %q: %d %v", status, code, rep)
	}
	return rep
}

func notePath(insee string, id int64) string {
	return fmt.Sprintf("/api/mayors/%s/notes/%d", insee, id)
}

// Correcting a note touches the WORDS and nothing else. `status` is what the
// campaign reads to decide whether to call this person, and `ts` is when the
// contact happened: neither is a spelling. Somebody who wants to say something
// else about the contact records a new status, which is the control directly
// under this history.
func TestCorrectingANoteMovesItsTextAndNoOtherColumn(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 1, "60")
	team := createTeam(t, s, "Nord", "")
	pw := createAccount(t, s, "n@exemple.fr", RoleVolunteer, &team)
	c := newClient(t, srv)
	c.signIn("n@exemple.fr", pw)

	rep := write(t, c, "60000", "to_call_back", "aple demain", "")
	id := noteIDOf(t, rep, "aple demain")
	before := scalar[string](t, s, "SELECT ts FROM notes WHERE id=$1", id)

	code, rep := c.call(http.MethodPost, notePath("60000", id),
		map[string]string{"note": "rappeler demain"})
	if code != http.StatusOK {
		t.Fatalf("the author correcting their own note: %d %v", code, rep)
	}
	if got := scalar[string](t, s, "SELECT note FROM notes WHERE id=$1",
		id); got != "rappeler demain" {
		t.Fatalf("the correction did not land: %q", got)
	}
	if got := scalar[string](t, s, "SELECT status FROM notes WHERE id=$1",
		id); got != "to_call_back" {
		t.Errorf("the correction moved the status the note recorded: %q", got)
	}
	if got := scalar[string](t, s, "SELECT ts FROM notes WHERE id=$1",
		id); got != before {
		t.Errorf("the correction moved when the contact happened: %q, was %q",
			got, before)
	}
	if got := scalar[string](t, s, "SELECT status FROM assignments "+
		"WHERE insee_code='60000'"); got != "to_call_back" {
		t.Errorf("the correction moved the card's status to %q", got)
	}
	// A shared register rewritten with nothing saying so is what « one team
	// watched signé become refusé and could ask whom » already cost once.
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM notes WHERE id=$1 "+
		"AND edited_at IS NOT NULL", id); n != 1 {
		t.Error("the corrected note carries no mark saying it was corrected")
	}

	// …and the answer describes what is recorded, rather than what the screen
	// hoped for: it is the card, re-read inside the same transaction.
	for _, row := range noteRows(t, rep) {
		if int64(row["id"].(float64)) != id {
			continue
		}
		if text(row["note"]) != "rappeler demain" {
			t.Errorf("the answer still carries the old text: %q", row["note"])
		}
		if text(row["edited_at"]) == "" {
			t.Error("the answer does not say the note was corrected")
		}
	}
}

// A note left untouched carries NO mark. Written the other way round —
// stamping `edited_at` at the insert, or on every write — the sentence
// « modifiée le … » would appear under every note in the campaign and mean
// nothing.
func TestANoteNobodyCorrectedSaysSo(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 1, "60")
	pw := createAccount(t, s, "n@exemple.fr", RoleVolunteer, nil)
	c := newClient(t, srv)
	c.signIn("n@exemple.fr", pw)

	rep := write(t, c, "60000", "to_call_back", "appelé", "")
	for _, row := range noteRows(t, rep) {
		if row["edited_at"] != nil {
			t.Errorf("a note nobody touched claims a correction: %v",
				row["edited_at"])
		}
	}
}

// The author, and only the author. A colleague of the same team reads this
// note — every team of a campaign reads every card of it — and reading is not
// rewriting.
func TestOnlyTheAuthorRewritesTheirOwnWords(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 1, "60")
	team := createTeam(t, s, "Nord", "")
	pwA := createAccount(t, s, "a@exemple.fr", RoleVolunteer, &team)
	pwB := createAccount(t, s, "b@exemple.fr", RoleVolunteer, &team)
	pwC := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	ca, cb, cc := newClient(t, srv), newClient(t, srv), newClient(t, srv)
	ca.signIn("a@exemple.fr", pwA)
	cb.signIn("b@exemple.fr", pwB)
	cc.signIn("coord@exemple.fr", pwC)

	const original = "a demandé à réfléchir"
	rep := write(t, ca, "60000", "to_call_back", original, "")
	id := noteIDOf(t, rep, original)

	for who, c := range map[string]*client{
		"a colleague of the same team": cb,
		"the campaign's coordination":  cc,
	} {
		code, _ := c.call(http.MethodPost, notePath("60000", id),
			map[string]string{"note": "réécrite par " + who})
		if code != http.StatusNotFound {
			t.Errorf("%s rewriting somebody else's note: %d, want 404 — a 403 "+
				"would say the note exists", who, code)
		}
		if got := scalar[string](t, s, "SELECT note FROM notes WHERE id=$1",
			id); got != original {
			t.Fatalf("%s rewrote the note: %q", who, got)
		}
	}
}

// `mine` is what puts the « Modifier » button on a line and takes it off the
// next. It travels as a BOOLEAN and never as the address it is computed from:
// what crosses between colleagues of one campaign is the card, never the
// person.
func TestTheHistorySaysWhichLinesAreYoursWithoutNamingAnybody(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 1, "60")
	team := createTeam(t, s, "Nord", "")
	pwA := createAccount(t, s, "a@exemple.fr", RoleVolunteer, &team)
	pwB := createAccount(t, s, "b@exemple.fr", RoleVolunteer, &team)
	ca, cb := newClient(t, srv), newClient(t, srv)
	ca.signIn("a@exemple.fr", pwA)
	cb.signIn("b@exemple.fr", pwB)

	write(t, ca, "60000", "to_call_back", "note de A", "")
	rep := write(t, cb, "60000", "refused", "note de B", "to_call_back")

	for _, row := range noteRows(t, rep) {
		want := text(row["note"]) == "note de B"
		if row["mine"] != want {
			t.Errorf("%q reads mine=%v for B, want %v",
				row["note"], row["mine"], want)
		}
	}
	// …and the address it is computed from stays behind. Asserted VALUE by
	// VALUE over the whole row rather than on the fields this test knows: a
	// column added to the selection tomorrow — `n.volunteer AS author` — is
	// exactly how an address gets back in. Not a substring search on the
	// body: these fixtures name an account « Name of a@exemple.fr », so a
	// contains-test would fail on the display name it is right to send.
	for _, row := range noteRows(t, rep) {
		for key, value := range row {
			for _, address := range []string{"a@exemple.fr", "b@exemple.fr"} {
				if value == address {
					t.Errorf("the history carries %s=%q: what crosses is the "+
						"card, never the person", key, address)
				}
			}
		}
	}
}

// THE HISTORY IS THE REGISTER AND `assignments` IS ITS HEAD.
//
// Left alone, a card whose last note has just gone keeps announcing « signé »
// to the whole campaign with nothing on record saying so — and emptying the
// history entirely left a status nobody ever wrote.
func TestRemovingTheLastNoteRollsTheCardBackToWhatRemains(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 1, "60")
	team := createTeam(t, s, "Nord", "")
	pw := createAccount(t, s, "n@exemple.fr", RoleVolunteer, &team)
	c := newClient(t, srv)
	c.signIn("n@exemple.fr", pw)

	write(t, c, "60000", "email_sent", "courriel parti", "")
	rep := write(t, c, "60000", "to_call_back", "appelé", "email_sent")
	middle := noteIDOf(t, rep, "appelé")
	rep = write(t, c, "60000", "refused", "refus", "to_call_back")
	head := noteIDOf(t, rep, "refus")

	// A note from the MIDDLE: the head is the row it already was, so nothing
	// about the card moves. Written as « recompute only when the head goes »
	// this case would pass for a reason that never runs.
	if code, rep := c.call(http.MethodDelete, notePath("60000", middle),
		nil); code != http.StatusOK {
		t.Fatalf("removing one's own note: %d %v", code, rep)
	}
	if got := scalar[string](t, s, "SELECT status FROM assignments "+
		"WHERE insee_code='60000'"); got != "refused" {
		t.Fatalf("removing a note from the middle moved the card to %q", got)
	}

	// The head: the card goes back to what the history now says, with the
	// timestamp and the team of the line that decides.
	headTS := scalar[string](t, s, "SELECT ts FROM notes WHERE note='courriel parti'")
	if code, rep := c.call(http.MethodDelete, notePath("60000", head),
		nil); code != http.StatusOK {
		t.Fatalf("removing the head note: %d %v", code, rep)
	}
	if got := scalar[string](t, s, "SELECT status FROM assignments "+
		"WHERE insee_code='60000'"); got != "email_sent" {
		t.Fatalf("the card still announces %q with nothing on record saying "+
			"so", got)
	}
	if got := scalar[string](t, s, "SELECT updated_at FROM assignments "+
		"WHERE insee_code='60000'"); got != headTS {
		t.Errorf("the card is dated %q and the line that decides it %q",
			got, headTS)
	}
	if got := scalar[int](t, s, "SELECT updated_by_team FROM assignments "+
		"WHERE insee_code='60000'"); got != team {
		t.Errorf("the card attributes its status to team %d, and the line "+
			"that decides it to %d", got, team)
	}

	// The last one: a card nobody has contacted, which is what an empty
	// history says.
	last := noteIDOf(t, mustCard(t, c, "60000"), "courriel parti")
	if code, rep := c.call(http.MethodDelete, notePath("60000", last),
		nil); code != http.StatusOK {
		t.Fatalf("removing the last note: %d %v", code, rep)
	}
	if got := scalar[string](t, s, "SELECT status FROM assignments "+
		"WHERE insee_code='60000'"); got != StatusToContact {
		t.Fatalf("an empty history left the card at %q", got)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM assignments "+
		"WHERE insee_code='60000' AND updated_by_team IS NULL"); n != 1 {
		t.Error("a card nobody has contacted still attributes its status to " +
			"a team")
	}
}

// The head the recompute reads is the campaign's, not the reader's. Filtered
// by team as the CARD is, a volunteer's deletion would roll the status back
// past a colleague's work they cannot even see — and the card the whole
// campaign reads would then say something no note supports.
func TestTheHeadTheCardGoesBackToMayBelongToAnotherTeam(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 1, "60")
	north := createTeam(t, s, "Nord", "")
	south := createTeam(t, s, "Sud", "")
	pwN := createAccount(t, s, "n@exemple.fr", RoleVolunteer, &north)
	pwS := createAccount(t, s, "s@exemple.fr", RoleVolunteer, &south)
	cn, cs := newClient(t, srv), newClient(t, srv)
	cn.signIn("n@exemple.fr", pwN)
	cs.signIn("s@exemple.fr", pwS)

	write(t, cn, "60000", "to_call_back", "appel de Nord", "")
	rep := write(t, cs, "60000", "refused", "refus vu par Sud", "to_call_back")
	head := noteIDOf(t, rep, "refus vu par Sud")

	if code, rep := cs.call(http.MethodDelete, notePath("60000", head),
		nil); code != http.StatusOK {
		t.Fatalf("Sud removing its own note: %d %v", code, rep)
	}
	if got := scalar[string](t, s, "SELECT status FROM assignments "+
		"WHERE insee_code='60000'"); got != "to_call_back" {
		t.Fatalf("the card went to %q: the recompute read only the notes Sud "+
			"can see, and Nord's call is not one of them", got)
	}
	if got := scalar[int](t, s, "SELECT updated_by_team FROM assignments "+
		"WHERE insee_code='60000'"); got != north {
		t.Errorf("the card attributes Nord's call to team %d", got)
	}
}

// READING THE HEAD AND REWRITING THE CARD IS ONE CRITICAL SECTION.
//
// The recompute is a read-then-write on a register the whole campaign reads —
// the same shape as a ceiling read before an insert instead of applied by it.
// Unlocked, a status somebody records in between is answered 200 and then
// overwritten by a head read before it existed, and `assignments` ends up
// announcing a status no note supports: a mayor everybody skips, or two
// volunteers calling the same person.
//
// Driven through the real server, concurrently, and asserted on the INVARIANT
// rather than on a timing: whatever the two requests answer, the card and its
// newest note must agree. Fifteen rounds, because it is a race — five of them
// corrupted before the row lock went in, none since.
func TestARemovalRacingAStatusWriteLeavesTheCardAndItsHistoryAgreeing(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 1, "60")
	pwA := createAccount(t, s, "a@exemple.fr", RoleVolunteer, nil)
	pwB := createAccount(t, s, "b@exemple.fr", RoleVolunteer, nil)
	ca, cb := newClient(t, srv), newClient(t, srv)
	ca.signIn("a@exemple.fr", pwA)
	cb.signIn("b@exemple.fr", pwB)

	const insee = "60000"
	for round := range 15 {
		// a card with two lines: an older one, and the head about to go
		write(t, ca, insee, "email_sent", "vieux", "")
		rep := write(t, ca, insee, "signed", "tête", "email_sent")
		head := noteIDOf(t, rep, "tête")

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			ca.call(http.MethodDelete, notePath(insee, head), nil)
		}()
		go func() {
			defer wg.Done()
			cb.call(http.MethodPost, "/api/mayors/"+insee+"/status",
				map[string]string{"status": "refused", "note": "en même temps",
					"seen": "signed"})
		}()
		wg.Wait()

		stored := scalar[string](t, s, "SELECT status FROM assignments "+
			"WHERE insee_code=$1", insee)
		newest := scalar[string](t, s, "SELECT status FROM notes "+
			"WHERE insee_code=$1 ORDER BY id DESC LIMIT 1", insee)
		if stored != newest {
			t.Fatalf("round %d: the card announces %q and its newest note "+
				"reads %q — the register describes a note nobody wrote",
				round, stored, newest)
		}
		execAsMaintenance(t, s, "DELETE FROM notes WHERE insee_code=$1", insee)
		execAsMaintenance(t, s, "DELETE FROM assignments WHERE insee_code=$1", insee)
	}
}

// The same defect with no status write at all: a coordination working through
// a card's history removes two lines at once, both recomputes read the head
// before either wrote, and the card ends up on the status of a note that is
// no longer there.
func TestTwoRemovalsAtOnceLeaveTheCardOnANoteThatIsStillThere(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 1, "60")
	pw := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	c := newClient(t, srv)
	c.signIn("coord@exemple.fr", pw)

	const insee = "60000"
	for round := range 15 {
		write(t, c, insee, "email_sent", "un", "")
		write(t, c, insee, "to_call_back", "deux", "email_sent")
		rep := write(t, c, insee, "signed", "trois", "to_call_back")
		second := noteIDOf(t, rep, "deux")
		third := noteIDOf(t, rep, "trois")

		var wg sync.WaitGroup
		wg.Add(2)
		for _, id := range []int64{third, second} {
			go func() {
				defer wg.Done()
				c.call(http.MethodDelete, notePath(insee, id), nil)
			}()
		}
		wg.Wait()

		stored := scalar[string](t, s, "SELECT status FROM assignments "+
			"WHERE insee_code=$1", insee)
		newest := scalar[string](t, s, "SELECT COALESCE((SELECT status FROM notes "+
			"WHERE insee_code=$1 ORDER BY id DESC LIMIT 1), 'to_contact')", insee)
		if stored != newest {
			t.Fatalf("round %d: the card announces %q and its history says %q",
				round, stored, newest)
		}
		execAsMaintenance(t, s, "DELETE FROM notes WHERE insee_code=$1", insee)
		execAsMaintenance(t, s, "DELETE FROM assignments WHERE insee_code=$1", insee)
	}
}

// A coordination removes words it must not carry — a note written by an
// account since closed would otherwise stay for ever.
func TestACoordinationRemovesANoteItDidNotWrite(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 1, "60")
	team := createTeam(t, s, "Nord", "")
	pwV := createAccount(t, s, "v@exemple.fr", RoleVolunteer, &team)
	pwC := createAccount(t, s, "coord@exemple.fr", RoleCoordination, nil)
	cv, cc := newClient(t, srv), newClient(t, srv)
	cv.signIn("v@exemple.fr", pwV)
	cc.signIn("coord@exemple.fr", pwC)

	rep := write(t, cv, "60000", "refused", "propos qui n'ont rien à faire là", "")
	id := noteIDOf(t, rep, "propos qui n'ont rien à faire là")

	if code, rep := cc.call(http.MethodDelete, notePath("60000", id),
		nil); code != http.StatusOK {
		t.Fatalf("the coordination removing a note of its campaign: %d %v",
			code, rep)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM notes WHERE id=$1",
		id); n != 0 {
		t.Fatal("the note is still there")
	}
}

// A lead leads a team; that is not a licence over what its members wrote. The
// narrow line `routeToggleAccount` already draws, one register over.
func TestALeadIsNotACoordination(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 1, "60")
	team := createTeam(t, s, "Nord", "")
	pwV := createAccount(t, s, "v@exemple.fr", RoleVolunteer, &team)
	pwL := createAccount(t, s, "lead@exemple.fr", RoleLead, &team)
	cv, cl := newClient(t, srv), newClient(t, srv)
	cv.signIn("v@exemple.fr", pwV)
	cl.signIn("lead@exemple.fr", pwL)

	rep := write(t, cv, "60000", "to_call_back", "note du bénévole", "")
	id := noteIDOf(t, rep, "note du bénévole")

	if code, _ := cl.call(http.MethodDelete, notePath("60000", id),
		nil); code != http.StatusNotFound {
		t.Errorf("a lead removing a note of their team: %d, want 404", code)
	}
	if n := scalar[int](t, s, "SELECT COUNT(*) FROM notes WHERE id=$1",
		id); n != 1 {
		t.Fatal("a lead removed a note they did not write")
	}
}

// The INSEE code in the path is part of the predicate, not decoration. A note
// named from the wrong card is a note nobody is looking at — and a screen that
// posted the right id against the wrong card would silently edit a line the
// volunteer cannot see.
func TestANoteIsNamedFromTheCardItIsOn(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 2, "60")
	pw := createAccount(t, s, "n@exemple.fr", RoleVolunteer, nil)
	c := newClient(t, srv)
	c.signIn("n@exemple.fr", pw)

	rep := write(t, c, "60000", "to_call_back", "sur la bonne fiche", "")
	id := noteIDOf(t, rep, "sur la bonne fiche")

	if code, _ := c.call(http.MethodPost, notePath("60001", id),
		map[string]string{"note": "réécrite depuis l'autre fiche"}); code !=
		http.StatusNotFound {
		t.Errorf("rewriting a note from another card: %d, want 404", code)
	}
	if code, _ := c.call(http.MethodDelete, notePath("60001", id),
		nil); code != http.StatusNotFound {
		t.Errorf("removing a note from another card: %d, want 404", code)
	}
	if got := scalar[string](t, s, "SELECT note FROM notes WHERE id=$1",
		id); got != "sur la bonne fiche" {
		t.Fatalf("the note was reached from another card: %q", got)
	}
}

// The same ceiling as writing one, and refused BEFORE anything is written:
// this row is re-read on every status write, and 300 posts of unbounded notes
// held 386 MB of heap.
func TestACorrectionIsBoundedLikeTheNoteItReplaces(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 1, "60")
	pw := createAccount(t, s, "n@exemple.fr", RoleVolunteer, nil)
	c := newClient(t, srv)
	c.signIn("n@exemple.fr", pw)

	rep := write(t, c, "60000", "to_call_back", "court", "")
	id := noteIDOf(t, rep, "court")

	if code, _ := c.call(http.MethodPost, notePath("60000", id),
		map[string]string{"note": strings.Repeat("é", maxNoteRunes+1)}); code !=
		http.StatusBadRequest {
		t.Errorf("a correction past the ceiling: %d, want 400", code)
	}
	if got := scalar[string](t, s, "SELECT note FROM notes WHERE id=$1",
		id); got != "court" {
		t.Fatal("the refused correction was written anyway")
	}
	// runes and not bytes, or a French note is refused for being long in a
	// unit nobody typed in
	if code, _ := c.call(http.MethodPost, notePath("60000", id),
		map[string]string{"note": strings.Repeat("é", maxNoteRunes)}); code !=
		http.StatusOK {
		t.Errorf("a correction of exactly %d runes: %d, want 200",
			maxNoteRunes, code)
	}
}

// An identifier past int4 answered 500 once, one table over. `notes.id` is a
// BIGINT and the parse is what stands between a URL and the driver.
func TestAnUnreadableNoteIdentifierIsRefusedAndNotCrashed(t *testing.T) {
	s, srv := testServer(t)
	seedMayors(t, s, 1, "60")
	pw := createAccount(t, s, "n@exemple.fr", RoleVolunteer, nil)
	c := newClient(t, srv)
	c.signIn("n@exemple.fr", pw)

	for _, id := range []string{"abc", "9999999999999999999999", "-", "1.5"} {
		if code, _ := c.call(http.MethodPost,
			"/api/mayors/60000/notes/"+id,
			map[string]string{"note": "x"}); code != http.StatusBadRequest {
			t.Errorf("note id %q: %d, want 400", id, code)
		}
	}
	// …and one that parses and names nothing is a 404, not a 400: it is a
	// note that does not exist, which is a different sentence.
	if code, _ := c.call(http.MethodDelete, "/api/mayors/60000/notes/999999",
		nil); code != http.StatusNotFound {
		t.Errorf("an identifier naming no note: %d, want 404", code)
	}
}

func mustCard(t *testing.T, c *client, insee string) map[string]any {
	t.Helper()
	code, rep := c.call(http.MethodGet, "/api/mayors/"+insee, nil)
	if code != http.StatusOK {
		t.Fatalf("reading the card: %d %v", code, rep)
	}
	return rep
}
