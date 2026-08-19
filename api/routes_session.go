package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
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
		// The same account-less version the apex offers, carrying THIS
		// campaign: the sign-in screen is where a volunteer with no account
		// stands, and it is the one place that knows which campaign they
		// came for.
		"browser_version_url": browserVersionFor(browserURL, org.Slug),
		// The COMMON mayor list's departments — public open data, identical
		// for every campaign and already published beside the browser
		// version. Unlike the `no_account` boolean this body stopped
		// carrying, it says nothing about THIS campaign.
		"departments": departments,
		// null when the campaign has none, or when the instance has no
		// object store: either way the header shows the hexagon alone.
		"logo":       s.logoOf(org),
		"magic_link": magicLink,
		// The campaign's DEFAULT answer to « do we telephone the mayors we
		// write to ». A volunteer who has answered for themselves overrides
		// it (see /api/me); one who has not follows it, and follows it as it
		// CHANGES rather than as it stood the day their account was made.
		"phone_outreach": org.PhoneOutreach,
		"organisation": map[string]any{
			"slug": org.Slug, "name": org.Name,
			// the toggle "Mon équipe" shows needs the current state
			"listed": org.Listed,
		},
	})
}

// browserVersionFor: where a volunteer with no account goes to work on THIS
// campaign, alone, in their browser.
//
// The `?org=<slug>` is what carries the campaign across — candidate,
// contacts, signature, logo — instead of nine fields retyped by hand, where
// a typo goes out to mayors under the campaign's name. The volunteer sees
// those values before anything is written (BrowserProposition.tsx).
//
// It is added ONLY where it resolves: the pre-fill asks
// `<slug>.<base domain>`, so a single-campaign instance — which has no
// subdomain space — gets the plain link. A parameter promising a pre-fill
// that lands on an empty campaign is a promise discovered by paying for it.
//
// The setting is refused at startup unless it is an http(s) URL or a path on
// this instance (validBrowserVersionURL), so there is no unparseable value
// to fall back from here.
func browserVersionFor(base, slug string) string {
	if base == "" || BaseDomain() == "" {
		return base
	}
	// Not a fallback for a failure — `validBrowserVersionURL` parsed this
	// same string at startup, so there is none to fall back from. It is a
	// guard against dereferencing the nil `url.Parse` returns beside an
	// error, on a path that check has already closed.
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	q.Set("org", slug)
	u.RawQuery = q.Encode()
	return u.String()
}

// validBrowserVersionURL: what PARAPHE_BROWSER_VERSION_URL may say.
//
// It becomes an `href` on the instance's home page and on every campaign's
// sign-in screen. The interface refuses anything that is not http(s) before
// rendering it, so a wrong value here shows as a link that is simply absent
// — an operator reading their own configuration back has no way to tell that
// from "this instance serves no browser version". Said at startup instead,
// where they are looking.
func validBrowserVersionURL(raw string) error {
	refuse := func(why string) error {
		return fmt.Errorf("PARAPHE_BROWSER_VERSION_URL = %q: %s. Give an "+
			"absolute http(s) URL, or a path on this instance such as "+
			"/navigateur/ — or leave it empty to offer the self-hosted one",
			raw, why)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return refuse(fmt.Sprintf("it is not a URL (%v)", err))
	}
	if u.Scheme == "" {
		// `//ailleurs.test/x` NAMES A HOST and starts with a slash, so a
		// "does it begin with /" test reads it as a path on this instance.
		// It is not: the browser resolves it against the page's scheme and
		// leaves the origin. The link sits on every campaign's sign-in
		// screen and in every footer, so the value would take volunteers off
		// the campaign under the campaign's own name — and neither this
		// check nor the interface's `httpUrl`, which tests the RESOLVED
		// protocol and sees `https:`, said a word about it.
		if strings.HasPrefix(raw, "//") {
			return refuse("`//host` names another host, not a path on this " +
				"instance — write the scheme out if that is what you mean")
		}
		if !strings.HasPrefix(raw, "/") {
			return refuse("a relative path resolves against whatever screen " +
				"the visitor is on")
		}
		return nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return refuse(fmt.Sprintf("%q is not a scheme a link may carry", u.Scheme))
	}
	if u.Host == "" {
		return refuse("it names no host")
	}
	return nil
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
	// The message templates are in there too, and they are the reason this
	// paragraph is not merely a nicety: read after the commit, the one
	// sign-in that commits — the hash upgrade — answered 500.
	body, err := s.meBodyFor(r, c)
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
	s.openSession(w, r, c, body, &limitSignInAccount, "signin_succeeded")
}

// openSession is what happens once a caller has PROVED who they are, by
// whichever door: the cookie, the counters, the event, the answer.
//
// One implementation, because the two doors must not drift. They already
// did in the writing: the link route forgot its own attempt counter until
// this was pulled together, and a volunteer who fumbled a password before
// asking for a link would have carried those failures into the next window.
//
// The whole ANSWER is built by the CALLER, before it commits — the
// transaction closes with that commit, and everything this reply needs must
// already be in hand. `meBody` is what builds it, so the shape stays in one
// place while the reading stays where the transaction is still open. Read
// here instead, the templates came back « tx is closed » on the one sign-in
// that commits: the hash upgrade.
// countedUnder names the account-keyed ceiling THIS ROUTE spent an event on,
// or nil when it spent none. Not the door the caller came through: redeeming
// a link carries a token and no address, so it counts nothing per account —
// and refunding the request ceiling there gave back an event nobody had
// spent, which is the same observable credit the refund exists to avoid.
func (s *Server) openSession(w http.ResponseWriter, r *http.Request,
	c *Account, body map[string]any, countedUnder *limitClass, event string,
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
	if countedUnder != nil {
		if subject, ok := s.signInSubjectFor(r, c.Email); ok {
			s.limiter.refund(r.Context(), *countedUnder, subject)
		}
	}
	s.securityEvent(r, slog.LevelInfo, event,
		append([]any{"account", s.accountPseudonym(c.Email)}, extra...)...)
	replyJSON(w, http.StatusOK, body)
}

// POST /api/me/password — changing one's OWN password.
//
// Open to every role and every scope, the instance administration included:
// whoever holds an account holds its password, and a credential nobody can
// rotate is one that stays wherever it has already been read out loud.
//
// THE CURRENT PASSWORD IS REQUIRED, and that is not ceremony. A session
// cookie is a bearer token with twelve hours on it; without this, whoever
// picked one up off a shared computer would turn a borrowed afternoon into
// permanent ownership of the account, and the owner would be the one locked
// out. Proving the password is what tells the two apart.
func (s *Server) routeChangePassword(w http.ResponseWriter, r *http.Request) {
	var d struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !readBody(w, r, &d) {
		return
	}
	me := accountOf(r)
	if utf8.RuneCountInString(d.New) < minPasswordRunes {
		errorJSON(w, http.StatusBadRequest,
			"Le nouveau mot de passe doit faire au moins %d caractères. "+
				"Trois ou quatre mots sans rapport font un bon mot de passe, "+
				"et se retiennent.", minPasswordRunes)
		return
	}
	if d.New == d.Current {
		errorJSON(w, http.StatusBadRequest,
			"Le nouveau mot de passe est identique à l'ancien.")
		return
	}

	var stored string
	if err := s.tx(r).QueryRow(r.Context(),
		"SELECT password_hash FROM accounts WHERE org_id=$1 AND email=$2",
		scopeOrg(r), me.Email).Scan(&stored); err != nil {
		s.failure(w, err)
		return
	}
	good, verifyErr := VerifyPassword(stored, d.Current)
	if verifyErr != nil {
		// an unreadable hash is an operations problem, not a typing error —
		// the same treatment as on the sign-in path, and the caller is told
		// only that it did not verify
		slog.Error("unusable password hash",
			"account", s.accountPseudonym(me.Email), "error", verifyErr)
		good = false
	}
	if !good {
		s.securityEvent(r, slog.LevelInfo, "password_change_refused",
			"account", s.accountPseudonym(me.Email))
		// 403 and NOT 401. A 401 from an authenticated route is what this
		// interface reads as « your session is gone » — it fires SESSION_LOST
		// and returns the volunteer to the sign-in form — so a mistyped
		// current password would have thrown them out of a session that is
		// perfectly alive, with their work behind it.
		errorJSON(w, http.StatusForbidden, "Mot de passe actuel incorrect.")
		return
	}

	hashed, err := HashPassword(d.New)
	if err != nil {
		s.failure(w, err)
		return
	}
	// Truncated to the second, and drawn from the SAME clock the session
	// tokens are minted by: see the comparison in auth.go. Written before
	// the new cookie is set, so the cookie is never older than the change it
	// carries out.
	changedAt := s.now().Truncate(time.Second)
	if _, err := s.tx(r).Exec(r.Context(),
		"UPDATE accounts SET password_hash=$3, password_changed_at=$4 "+
			"WHERE org_id=$1 AND email=$2",
		scopeOrg(r), me.Email, hashed, changedAt); err != nil {
		s.failure(w, err)
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	// THIS session survives, and every other one falls. Re-minted after the
	// commit: the token carries its own instant, and one issued before the
	// row was written would be refused by the very rule this route arms.
	if err := s.sessions.Set(w, me.Email, currentOrg(r), s.now()); err != nil {
		s.failure(w, err)
		return
	}
	s.securityEvent(r, slog.LevelInfo, "password_changed",
		"account", s.accountPseudonym(me.Email))
	replyJSON(w, http.StatusOK, map[string]any{"state": "password_changed"})
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
	body, err := s.meBodyFor(r, c)
	if err != nil {
		s.failure(w, err)
		return
	}
	replyJSON(w, http.StatusOK, body)
}

// meBody: what a client is told about ITSELF, written ONCE.
//
// Three routes answer this shape — /api/me, signing in, redeeming a link —
// and it used to be spelt out at two of them. Adding the message templates to
// one and not the other is exactly what happened: a volunteer who signed in
// and went straight to a card rendered from the templates the IMAGE carries
// while their campaign's own sat unused, until they happened to reload. The
// end-to-end journey found it, and `TestSigningInSaysTheSameThingAsMe` is
// what stops the next field doing the same.
//
// The TEMPLATES are here and not in /api/config: that body is public and has
// no account, and a team's overlay is its team's.
func (s *Server) meBodyFor(r *http.Request, c *Account) (
	map[string]any, error) {
	departments, err := s.teamDepartments(r, c)
	if err != nil {
		return nil, err
	}
	campaignTemplates, teamTemplates, err := s.templateLayers(r, c)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"account":     c,
		"departments": departments,
		"may_manage":  c.MayManage(),
		"templates": map[string]any{
			"campaign": campaignTemplates,
			"team":     teamTemplates,
		},
	}, nil
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
	// This volunteer's own answer to « do I telephone the mayors I write to »,
	// or nil for « whatever the campaign does ». Carried on the same route as
	// the personal touch because it is the same act — one screen, « Mon
	// profil », saved once.
	PhoneOutreach *bool `json:"phone_outreach"`
	// Whether the field above was MEANT. A client that says nothing about it
	// must not clear an answer the volunteer gave — the rule `listed` and
	// `name` already follow — and nil is itself a value here (« follow the
	// campaign »), so nil alone cannot tell « leave it alone » from « unset
	// it ».
	SetPhoneOutreach bool `json:"set_phone_outreach"`
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
	phone := c.PhoneOutreach
	if d.SetPhoneOutreach {
		phone = d.PhoneOutreach
	}
	if _, err := s.tx(r).Exec(r.Context(),
		"UPDATE accounts SET personal_note=$1, phone_outreach=$4 "+
			"WHERE org_id=$2 AND email=$3",
		strings.TrimSpace(d.PersonalNote), scopeOrg(r), c.Email,
		phone); err != nil {
		s.failure(w, err)
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	replyJSON(w, http.StatusOK, map[string]any{
		"personal_note":  strings.TrimSpace(d.PersonalNote),
		"phone_outreach": phone})
}
