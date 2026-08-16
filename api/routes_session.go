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
	var noAccount bool
	var one int
	err := s.tx(r).QueryRow(r.Context(),
		"SELECT 1 FROM accounts WHERE org_id=$1 AND active", scopeOrg(r)).Scan(&one)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		noAccount = true
	case err != nil:
		s.failure(w, err)
		return
	}

	// The account-less alternative, offered beside the hosting form: same
	// tool, alone, nothing leaving the visitor's browser. When this image
	// serves its own build, the link needs no configuration at all.
	browserURL := strings.TrimSpace(Get("browser_version_url"))
	if browserURL == "" && s.browserDir != "" {
		browserURL = "/navigateur/"
	}
	org := orgOf(r)
	if org == nil {
		replyJSON(w, http.StatusOK, map[string]any{
			"mode":                "instance",
			"base_domain":         BaseDomain(),
			"source_url":          s.cfg.SourceURL,
			"browser_version_url": browserURL,
			"no_account":          noAccount,
			"campaign_keys":       CampaignKeys,
		})
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
		"no_account": noAccount,
		// null when the campaign has none, or when the instance has no
		// object store: either way the header shows the hexagon alone.
		"logo": s.logoOf(org),
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
	if err := s.sessions.Set(w, email, currentOrg(r), s.now()); err != nil {
		s.failure(w, err)
		return
	}
	// The attempt counter served its purpose: a signed-in account starts
	// its next window clean, so a shared team box that fumbles a few times
	// and then succeeds is not carrying failures towards the ceiling.
	if subject, ok := s.signInSubjectFor(r, email); ok {
		s.limiter.forget(r.Context(), limitSignInAccount, subject)
	}
	s.securityEvent(r, slog.LevelInfo, "signin_succeeded",
		"account", s.accountPseudonym(email))
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
	departments := []string{}
	for _, d := range strings.Split(raw, ";") {
		if d != "" {
			departments = append(departments, d)
		}
	}
	return departments, nil
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
