package main

import (
	"net/http"
	"testing"
	"time"
)

// Changing one's own password, and what the change is WORTH.
//
// The route itself is three lines of SQL. What has to hold is everything
// around it: that the current password is proved before the new one is
// taken, that a mistyped one does not throw a volunteer out of a live
// session, and — the reason this feature is not just a form — that the
// change signs the OTHER sessions out. Somebody changes their password
// because they think it has leaked; if the session of whoever took it
// survives, the remedy is one discovered by paying for it.

const newPassword = "colline-verger-tilleul-42"

func TestChangingThePasswordTakesTheNewOneAndRefusesTheOld(t *testing.T) {
	s, srv := testServer(t)
	email := "benevole@exemple.fr"
	old := createAccount(t, s, email, RoleVolunteer, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, old); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}

	code, body := c.call(http.MethodPost, "/api/me/password",
		map[string]string{"current": old, "new": newPassword})
	if code != http.StatusOK {
		t.Fatalf("changing the password: %d %v", code, body)
	}

	// A FRESH client each way: the point is what the stored credential is
	// now, not what this session still carries.
	if code := newClient(t, srv).signIn(email, newPassword); code != http.StatusOK {
		t.Errorf("the new password does not open the account: %d", code)
	}
	if code := newClient(t, srv).signIn(email, old); code != http.StatusUnauthorized {
		t.Errorf("the OLD password still opens the account: %d — the change "+
			"wrote nothing, and its owner believes it did", code)
	}
}

// The current password is the whole of the proof. Without it a session
// cookie — a bearer token with twelve hours on it, picked up off a shared
// computer — becomes permanent ownership of the account, with its owner
// locked out.
func TestChangingThePasswordRequiresTheCurrentOne(t *testing.T) {
	s, srv := testServer(t)
	email := "benevole@exemple.fr"
	old := createAccount(t, s, email, RoleVolunteer, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, old); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}

	code, body := c.call(http.MethodPost, "/api/me/password",
		map[string]string{"current": "pas-le-bon-mot-de-passe", "new": newPassword})
	// 403 and NOT 401: the interface reads a 401 from an authenticated route
	// as « your session is gone », fires SESSION_LOST and returns the
	// volunteer to the sign-in form — so a typo would throw them out of a
	// session that is perfectly alive, with their work behind it.
	if code != http.StatusForbidden {
		t.Fatalf("a wrong current password: %d %v, want 403", code, body)
	}
	// …and the account is untouched: the old password still opens it, and
	// the new one does not
	if code := newClient(t, srv).signIn(email, old); code != http.StatusOK {
		t.Errorf("the refused attempt changed something: %d", code)
	}
	if code := newClient(t, srv).signIn(email, newPassword); code != http.StatusUnauthorized {
		t.Errorf("the password was taken despite the refusal: %d", code)
	}
	// and the session that made the attempt is STILL LIVE — the whole reason
	// for the 403
	if code, _ := c.call(http.MethodGet, "/api/me", nil); code != http.StatusOK {
		t.Errorf("a mistyped password ended the session: %d", code)
	}
}

// THE OTHER SESSIONS FALL. Two clients hold a session on the same account;
// one changes the password, and the other is not this account any more.
func TestChangingThePasswordSignsTheOtherSessionsOut(t *testing.T) {
	s, srv := testServer(t)
	email := "benevole@exemple.fr"
	old := createAccount(t, s, email, RoleVolunteer, nil)

	mine, theirs := newClient(t, srv), newClient(t, srv)
	for _, c := range []*client{mine, theirs} {
		if code := c.signIn(email, old); code != http.StatusOK {
			t.Fatalf("sign-in: %d", code)
		}
	}
	// both live, or what follows would prove nothing
	for _, c := range []*client{mine, theirs} {
		if code, _ := c.call(http.MethodGet, "/api/me", nil); code != http.StatusOK {
			t.Fatalf("a session was not open to begin with: %d", code)
		}
	}
	// The tokens carry Unix SECONDS. Signed in and changed inside the same
	// second, the neighbour's token is not "before" the change and survives
	// — which is the deliberate one-second grace that keeps the caller's own
	// fresh cookie alive. A real interval is what this test is about.
	s.now = func() time.Time { return time.Now().Add(2 * time.Second) }

	if code, body := mine.call(http.MethodPost, "/api/me/password",
		map[string]string{"current": old, "new": newPassword}); code != http.StatusOK {
		t.Fatalf("changing the password: %d %v", code, body)
	}

	if code, _ := theirs.call(http.MethodGet, "/api/me", nil); code != http.StatusUnauthorized {
		t.Errorf("the other session survived the change: %d — somebody who "+
			"took this password keeps the account for twelve more hours, and "+
			"changing it is the very thing its owner did about that", code)
	}
	// …and MINE does not: signing myself out of my own session is how a
	// volunteer concludes the change failed and tries again.
	if code, body := mine.call(http.MethodGet, "/api/me", nil); code != http.StatusOK {
		t.Errorf("the session that made the change was signed out too: %d %v",
			code, body)
	}
}

// A floor under a chosen password, because every password before this route
// was drawn at 39.8 bits and a person choosing one is where two letters can
// arrive.
func TestAChosenPasswordHasAFloor(t *testing.T) {
	s, srv := testServer(t)
	email := "benevole@exemple.fr"
	old := createAccount(t, s, email, RoleVolunteer, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, old); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}

	for _, short := range []string{"", "court", "onze-signe"} {
		code, _ := c.call(http.MethodPost, "/api/me/password",
			map[string]string{"current": old, "new": short})
		if code != http.StatusBadRequest {
			t.Errorf("a %d-rune password: %d, want 400", len([]rune(short)), code)
		}
	}
	// Counted in RUNES: a French passphrase spends several bytes on a
	// letter, and a byte count refuses what it should accept.
	accented := "prés-très-mûr"
	if len(accented) < minPasswordRunes {
		t.Fatal("fixture assumption broken: this must be short in BYTES")
	}
	if code, body := c.call(http.MethodPost, "/api/me/password",
		map[string]string{"current": old, "new": accented}); code != http.StatusOK {
		t.Errorf("a 13-rune accented passphrase was refused: %d %v — counted "+
			"in bytes it looks 17 long, counted in runes it is what the "+
			"message promises", code, body)
	}
}

// Reusing the same password answers nothing and would still sign every other
// session out: said, rather than reported as a change that happened.
func TestTheNewPasswordMustDifferFromTheOld(t *testing.T) {
	s, srv := testServer(t)
	email := "benevole@exemple.fr"
	old := createAccount(t, s, email, RoleVolunteer, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, old); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	if code, _ := c.call(http.MethodPost, "/api/me/password",
		map[string]string{"current": old, "new": old}); code != http.StatusBadRequest {
		t.Errorf("reusing the same password: %d, want 400", code)
	}
}

// The instance administration holds an account like anybody else, and a
// credential nobody can rotate is one that stays wherever it has already
// been read out loud. Its scope owns no campaign row, which is exactly the
// shape a campaign-only route would have refused.
func TestTheInstanceAdministrationChangesItsOwnPassword(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	s, srv := testServer(t)
	email := "administration@exemple.fr"
	old := createAccountIn(t, s, OrgInstance, email, RoleAdministration, nil)

	c := clientOn(t, srv, "paraphe.test")
	if code := c.signIn(email, old); code != http.StatusOK {
		t.Fatalf("sign-in on the apex: %d", code)
	}
	if code, body := c.call(http.MethodPost, "/api/me/password",
		map[string]string{"current": old, "new": newPassword}); code != http.StatusOK {
		t.Fatalf("the administration cannot change its password: %d %v", code, body)
	}
	if code := clientOn(t, srv, "paraphe.test").
		signIn(email, newPassword); code != http.StatusOK {
		t.Errorf("the new password does not open the administration: %d", code)
	}
}

// A lead draws a new password for a volunteer of their team who lost theirs
// — the door the sign-in screen has promised all along and that no route
// answered.
func TestAManagerDrawsANewPasswordForSomebodyWhoLostTheirs(t *testing.T) {
	s, srv := testServer(t)
	team := createTeam(t, s, "Nord", "59;62")
	lead := "referente@exemple.fr"
	leadPassword := createAccount(t, s, lead, RoleLead, &team)
	volunteer := "benevole@exemple.fr"
	oldVolunteer := createAccount(t, s, volunteer, RoleVolunteer, &team)

	// the volunteer is signed in when it happens: a password is regenerated
	// precisely when nobody knows who still holds the previous one
	stale := newClient(t, srv)
	if code := stale.signIn(volunteer, oldVolunteer); code != http.StatusOK {
		t.Fatalf("the volunteer's sign-in: %d", code)
	}
	s.now = func() time.Time { return time.Now().Add(2 * time.Second) }

	c := newClient(t, srv)
	if code := c.signIn(lead, leadPassword); code != http.StatusOK {
		t.Fatalf("the lead's sign-in: %d", code)
	}
	code, body := c.call(http.MethodPost,
		"/api/team/account/"+volunteer+"/password", nil)
	if code != http.StatusOK {
		t.Fatalf("drawing a new password: %d %v", code, body)
	}
	drawn, _ := body["password"].(string)
	if drawn == "" {
		t.Fatal("no password in the answer: it is shown once and stored " +
			"nowhere in the clear, so an answer without it is a lost account")
	}

	if code := newClient(t, srv).signIn(volunteer, drawn); code != http.StatusOK {
		t.Errorf("the drawn password does not open the account: %d", code)
	}
	if code := newClient(t, srv).signIn(volunteer, oldVolunteer); code != http.StatusUnauthorized {
		t.Errorf("the old password still opens it: %d", code)
	}
	// and the session opened under the old password is gone, for the same
	// reason the change route ends the others
	if code, _ := stale.call(http.MethodGet, "/api/me", nil); code != http.StatusUnauthorized {
		t.Errorf("the session opened under the replaced password survived: %d", code)
	}
}

// One's OWN password does not go through that door: it would show a drawn
// password on screen instead of taking one the person chose, and sign the
// session doing it out.
func TestAManagerIsSentToTheirProfileForTheirOwnPassword(t *testing.T) {
	s, srv := testServer(t)
	email := "coord@exemple.fr"
	password := createAccount(t, s, email, RoleCoordination, nil)
	c := newClient(t, srv)
	if code := c.signIn(email, password); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	code, body := c.call(http.MethodPost, "/api/team/account/"+email+"/password", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("a coordinator resetting themselves: %d %v, want 400", code, body)
	}
	if code := newClient(t, srv).signIn(email, password); code != http.StatusOK {
		t.Errorf("the refusal changed the password anyway: %d", code)
	}
}
