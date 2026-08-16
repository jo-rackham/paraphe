package main

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// GET /api/config — what the front end must know before signing in: the
// candidate's name (it shows on the login screen), the status and rank
// vocabulary, and the admission of a configuration still at its template.
//
// On the domain apex there is no campaign to describe: the answer says
// "instance", and the front end shows the public landing page — hosting
// request and administration sign-in.
func (s *Server) routeConfig(w http.ResponseWriter, r *http.Request) {
	// Whether the scope has any active account is NOT reported here. This body
	// is read by anyone, with no session: a boolean saying "this campaign has
	// no accounts yet" is an enumeration signal for the price of a GET. An
	// instance started without an administrator is told so where it belongs —
	// in the server logs, which name the variables to set (bootstrap.go).

	// The account-less alternative, offered beside the hosting form: same
	// tool, alone, nothing leaving the visitor's browser. When this image
	// serves its own build, the link needs no configuration at all.
	browserURL := strings.TrimSpace(Get("browser_version_url"))
	if browserURL == "" && s.browserDir != "" {
		browserURL = "/navigateur/"
	}
	// Whether this instance can send an email at all. Without it the screen
	// would offer "receive a link" on an instance with no relay, and the
	// button would refuse every time it was pressed.
	magicLink := s.mailer != nil
	org := orgOf(r)
	if org == nil {
		replyJSON(w, http.StatusOK, map[string]any{
			"mode":                "instance",
			"base_domain":         BaseDomain(),
			"source_url":          s.cfg.SourceURL,
			"browser_version_url": browserURL,
			"campaign_keys":       CampaignKeys,
			"magic_link":          magicLink,
		})
		return
	}
	// The only way the public team-request form on this very screen can offer
	// a perimeter instead of asking a visitor to guess how the register spells
	// « Côtes-d'Armor ».
	departments, err := s.departmentLabels(r)
	if err != nil {
		s.failure(w, err)
		return
	}
	campaign := completeCampaign(org.Campaign)
	replyJSON(w, http.StatusOK, map[string]any{
		"mode":       "team",
		"campaign":   campaign,
		"batch_size": org.BatchSize,
		"unfilled":   UnfilledKeys(campaign),
		"source_url": s.cfg.SourceURL,
		"statuses":   Statuses,
		"ranks":      Ranks,
		// The COMMON mayor list's departments — public open data, identical
		// for every campaign and already published beside the browser
		// version. Unlike the `no_account` boolean this body stopped
		// carrying, it says nothing about THIS campaign.
		"departments": departments,
		// null when the campaign has none, or when the instance has no
		// object store: either way the header shows the hexagon alone.
		"logo":       s.logoOf(org),
		"magic_link": magicLink,
		"organisation": map[string]any{
			"slug": org.Slug, "name": org.Name,
			// the toggle "Mon équipe" shows needs the current state
			"listed": org.Listed,
		},
	})
}

type signInRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// POST /api/session — sign in.
func (s *Server) routeSignIn(w http.ResponseWriter, r *http.Request) {
	var d signInRequest
	if !readBody(w, r, &d) {
		return
	}
	email := normalizeEmail(d.Email)

	var stored string
	var active bool
	// Bounded to the campaign being served: an address that exists in
	// another campaign hosted here is, seen from here, unknown. Stated in the
	// query, which is the one place the campaign is named.
	err := s.tx(r).QueryRow(r.Context(),
		"SELECT password_hash, active FROM accounts WHERE org_id=$1 AND email=$2",
		scopeOrg(r), email).Scan(&stored, &active)
	found := true
	if errors.Is(err, pgx.ErrNoRows) {
		// decoy hash when the account does not exist: without it, the
		// response is sixty times faster and reveals who is on the team
		found, stored = false, s.decoyHash
	} else if err != nil {
		s.failure(w, err)
		return
	}

	good, verifyErr := VerifyPassword(stored, d.Password)
	if verifyErr != nil {
		// an unreadable hash is an operations problem, not a typing error:
		// its reason is logged in full — the account only as a pseudonym,
		// like every subject in these logs. Telling the client would reveal
		// that the account exists.
		slog.Error("unusable password hash",
			"account", s.accountPseudonym(email), "error", verifyErr)
		good = false
	}
	if !found || !good {
		// One event whatever the cause: distinguishing "no such address"
		// from "wrong password" in the log would store exactly the
		// existence signal the decoy hash exists to withhold.
		s.securityEvent(r, slog.LevelInfo, "signin_failed",
			"account", s.accountPseudonym(email))
		errorJSON(w, http.StatusUnauthorized, "Adresse ou mot de passe incorrect.")
		return
	}
	if !active {
		// The SAME answer as a wrong password, reached only once the
		// password verified. Saying "deactivated" here confirmed to whoever
		// typed it that the credential is live — in exactly the situation
		// deactivation exists for, a phished account, where the person
		// holding the correct password is the attacker. The distinction
		// stays in the log, where the operator reads it and nobody else;
		// a volunteer switched off by their own lead learns it from them.
		s.securityEvent(r, slog.LevelInfo, "signin_refused_inactive",
			"account", s.accountPseudonym(email))
		errorJSON(w, http.StatusUnauthorized, "Adresse ou mot de passe incorrect.")
		return
	}

	c, err := s.readAccount(r, email)
	if err != nil {
		s.failure(w, err)
		return
	}
	if c == nil {
		// deactivated between the two reads — same answer as above, for the
		// same reason
		errorJSON(w, http.StatusUnauthorized, "Adresse ou mot de passe incorrect.")
		return
	}
	// Read BEFORE the upgrade below, because committing closes the
	// transaction and everything this answer needs must already be in hand.
	departments, err := s.teamDepartments(r, c)
	if err != nil {
		s.failure(w, err)
		return
	}

	// The password is known HERE and nowhere else, so this is the only
	// moment a hash written under an older scheme can be replaced. An
	// account created before argon2id would otherwise keep its scrypt hash
	// until someone changed the password — which, for a volunteer's account
	// handed out once by a lead, is never.
	//
	// A failure is logged and not answered: the sign-in itself succeeded,
	// and refusing it because an upgrade could not be written would turn an
	// improvement into an outage. The old hash still verifies.
	if NeedsRehash(stored) {
		account := s.accountPseudonym(email)
		if fresh, hashErr := HashPassword(d.Password); hashErr != nil {
			slog.Warn("password hash not upgraded", "account", account, "error", hashErr)
		} else if _, execErr := s.tx(r).Exec(r.Context(),
			"UPDATE accounts SET password_hash=$3 WHERE org_id=$1 AND email=$2",
			scopeOrg(r), email, fresh); execErr != nil {
			slog.Warn("password hash not upgraded", "account", account, "error", execErr)
		} else if commitErr := s.commit(r); commitErr != nil {
			slog.Warn("password hash not upgraded", "account", account, "error", commitErr)
		}
	}
	s.openSession(w, r, c, departments, limitSignInAccount,
		"signin_succeeded")
}

// openSession is what happens once a caller has PROVED who they are, by
// whichever door: the cookie, the counters, the event, the answer.
//
// One implementation, because the two doors must not drift. They already
// did in the writing: the link route forgot its own attempt counter until
// this was pulled together, and a volunteer who fumbled a password before
// asking for a link would have carried those failures into the next window.
//
// The account and its departments are read by the CALLER, before it
// commits — the transaction closes with that commit, and everything this
// answer needs must already be in hand.
func (s *Server) openSession(w http.ResponseWriter, r *http.Request,
	c *Account, departments []string, doorTaken limitClass, event string,
	extra ...any) {
	if err := s.sessions.Set(w, c.Email, currentOrg(r), s.now()); err != nil {
		s.failure(w, err)
		return
	}
	// The attempt is GIVEN BACK, not forgiven, and only on the door it was
	// spent at.
	//
	// Clearing the counter reads better and answers a question the rest of
	// this package refuses. The per-address ceiling is one an ANONYMOUS
	// caller fills for an address of their choosing, just by submitting it:
	// burn it, poll it, and its reopening says somebody has just signed in as
	// that address — so the address names one, which the constant sentence
	// and the decoy hash exist to withhold. Leaving it alone instead locks an
	// account out of its own password after ten legitimate sign-ins, because
	// the ceiling counts successes too; the end-to-end journeys found that
	// within a minute of trying it.
	//
	// A refund does both. Counted on arrival like every other event — which
	// is what still bounds a flood whose handlers never finish — and given
	// back once the attempt has proved legitimate, so the bucket ends exactly
	// where it stood. An attacker watching it sees the same thing whether the
	// owner signed in or not, and the owner's own sign-ins cost nothing.
	//
	// The OTHER door is not refunded: nothing was spent there, and a credit
	// for a link nobody asked for is the observable difference all over
	// again.
	if subject, ok := s.signInSubjectFor(r, c.Email); ok {
		s.limiter.refund(r.Context(), doorTaken, subject)
	}
	s.securityEvent(r, slog.LevelInfo, event,
		append([]any{"account", s.accountPseudonym(c.Email)}, extra...)...)
	replyJSON(w, http.StatusOK, map[string]any{
		"account":     c,
		"departments": departments,
		"may_manage":  c.MayManage(),
	})
}

// DELETE /api/session — sign out.
func (s *Server) routeSignOut(w http.ResponseWriter, _ *http.Request) {
	s.sessions.Clear(w)
	replyJSON(w, http.StatusOK, map[string]string{"state": "signed_out"})
}

// GET /api/me — who I am, and my team's geographic scope.
func (s *Server) routeMe(w http.ResponseWriter, r *http.Request) {
	s.replyMe(w, r, accountOf(r))
}

func (s *Server) replyMe(w http.ResponseWriter, r *http.Request, c *Account) {
	departments, err := s.teamDepartments(r, c)
	if err != nil {
		s.failure(w, err)
		return
	}
	replyJSON(w, http.StatusOK, map[string]any{
		"account":     c,
		"departments": departments,
		"may_manage":  c.MayManage(),
	})
}

// teamDepartments: my team's geographic scope (empty = everything).
func (s *Server) teamDepartments(r *http.Request, c *Account) ([]string, error) {
	if c.MyTeam() == NationalTeam {
		return []string{}, nil
	}
	var raw string
	err := s.tx(r).QueryRow(r.Context(),
		"SELECT COALESCE(departments,'') FROM teams WHERE org_id=$1 AND id=$2",
		scopeOrg(r), c.MyTeam()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return splitDepartments(raw), nil
}

type personalNoteRequest struct {
	PersonalNote string `json:"personal_note"`
}

// POST /api/me/personal_note — the volunteer's personal touch, inserted into
// their messages.
func (s *Server) routePersonalNote(w http.ResponseWriter, r *http.Request) {
	var d personalNoteRequest
	if !readBody(w, r, &d) {
		return
	}
	// this text goes into EVERY message the volunteer generates: a novel
	// in it is a novel in every email to a mayor
	if utf8.RuneCountInString(d.PersonalNote) > maxCampaignRunes {
		errorJSON(w, http.StatusBadRequest,
			"Votre touche personnelle ne doit pas dépasser %d caractères.",
			maxCampaignRunes)
		return
	}
	c := accountOf(r)
	if _, err := s.tx(r).Exec(r.Context(),
		"UPDATE accounts SET personal_note=$1 WHERE org_id=$2 AND email=$3",
		strings.TrimSpace(d.PersonalNote), scopeOrg(r), c.Email); err != nil {
		s.failure(w, err)
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	replyJSON(w, http.StatusOK, map[string]string{
		"personal_note": strings.TrimSpace(d.PersonalNote)})
}
