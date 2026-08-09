package main

import (
	"errors"
	"log"
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
		"SELECT 1 FROM accounts WHERE active").Scan(&one)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		noAccount = true
	case err != nil:
		s.failure(w, err)
		return
	}

	org := orgOf(r)
	if org == nil {
		replyJSON(w, http.StatusOK, map[string]any{
			"mode":          "instance",
			"base_domain":   BaseDomain(),
			"source_url":    s.cfg.SourceURL,
			"no_account":    noAccount,
			"campaign_keys": CampaignKeys,
		})
		return
	}
	campaign := completeCampaign(org.Campaign)
	replyJSON(w, http.StatusOK, map[string]any{
		"mode":         "team",
		"campaign":     campaign,
		"batch_size":   org.BatchSize,
		"unfilled":     UnfilledKeys(campaign),
		"source_url":   s.cfg.SourceURL,
		"statuses":     Statuses,
		"ranks":        Ranks,
		"no_account":   noAccount,
		"organisation": map[string]any{"slug": org.Slug, "name": org.Name},
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
	email := strings.ToLower(strings.TrimSpace(d.Email))

	var stored string
	var active bool
	// RLS bounds the lookup to the campaign being served: an address that
	// exists in another campaign hosted here is, seen from here, unknown.
	err := s.tx(r).QueryRow(r.Context(),
		"SELECT password_hash, active FROM accounts WHERE email=$1", email).Scan(&stored, &active)
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
		// it is logged in full. Telling the client would reveal that the
		// account exists.
		log.Printf("unusable hash for %q: %v", email, verifyErr)
		good = false
	}
	if !found || !good {
		errorJSON(w, http.StatusUnauthorized, "Adresse ou mot de passe incorrect.")
		return
	}
	if !active {
		errorJSON(w, http.StatusForbidden,
			"Ce compte a été désactivé. Voyez votre référent.")
		return
	}

	c, err := s.readAccount(r, email)
	if err != nil {
		s.failure(w, err)
		return
	}
	if c == nil {
		// deactivated between the two reads
		errorJSON(w, http.StatusForbidden,
			"Ce compte a été désactivé. Voyez votre référent.")
		return
	}
	if err := s.sessions.Set(w, email, currentOrg(r), s.now()); err != nil {
		s.failure(w, err)
		return
	}
	s.replyMe(w, r, c)
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
		"SELECT COALESCE(departments,'') FROM teams WHERE id=$1",
		c.MyTeam()).Scan(&raw)
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
		"UPDATE accounts SET personal_note=$1 WHERE email=$2",
		strings.TrimSpace(d.PersonalNote), c.Email); err != nil {
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
