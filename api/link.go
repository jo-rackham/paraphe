package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
)

// Signing in by email: a link carrying a one-shot token.
//
// It ADDS a path, it replaces none. The password stays — read down a
// telephone line, it is what still works when the relay is down, and
// PARAPHE_ADMIN_PASSWORD bootstraps an instance before any email could
// leave it. What the link fixes is the hole beside it: there was no
// recovery at all, so a forgotten password meant a lead reopening the
// account.
//
// Two purposes, one token, one table. They differ in one thing, their life,
// and the purpose is carried so that the SECURITY EVENT can name it — the
// row is deleted when the link is used, so that log line is the only place
// an operator sees whether a campaign's invitations are being taken up or
// whether people are recovering forgotten passwords:
//   - signin, 15 minutes: asked for from the sign-in screen;
//   - invitation, 7 days: sent when an account is opened. A volunteer reads
//     their mail the next morning, and a fifteen-minute invitation is an
//     invitation that never arrives.

const (
	purposeSignIn     = "signin"
	purposeInvitation = "invitation"

	signInLinkLife     = 15 * time.Minute
	invitationLinkLife = 7 * 24 * time.Hour
)

// linkTokenBytes: 32 bytes of crypto/rand, 256 bits. Nothing to guess and
// nothing to search, which is also why the stored form is a plain SHA-256
// and not argon2id: a memory-hard hash buys its cost against a HUMAN secret,
// and here it would only put a 32 MiB derivation behind hashGate on a public
// route — the very amplifier hashGate exists to bound (password.go).
const linkTokenBytes = 32

func hashLinkToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newLinkToken draws a token and keeps only its hash. The raw one is
// returned once, to be put in an email, and exists nowhere else.
//
// Free-standing, and told which campaign it writes for, because two callers
// need it from either side of the wall: the routes, through mintLink, which
// binds the campaign being served; and campaign creation, which runs in the
// instance scope and writes into the campaign it has just made — the same
// crossing, and the same single row, as the coordination account beside it.
//
// The delete first is three properties in one statement: asking for a new
// link invalidates the previous one, the table cannot grow under a loop (one
// live row per address), and expired rows go in passing — no background task
// to own, no goroutine whose lifecycle somebody has to remember.
func newLinkToken(ctx context.Context, tx pgx.Tx, org int, email, purpose string,
	now time.Time, life time.Duration) (string, error) {
	raw := make([]byte, linkTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("drawing a sign-in token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	if _, err := tx.Exec(ctx,
		"DELETE FROM login_tokens WHERE org_id=$1 AND "+
			"((email=$2 AND purpose=$4) OR expires_at<=$3)",
		org, email, now, purpose); err != nil {
		return "", fmt.Errorf("clearing the previous sign-in links: %w", err)
	}
	// ON CONFLICT, because the DELETE above cannot see a row another
	// transaction has not committed yet: two requests arriving together both
	// inserted, and the recipient got two links of which one was already
	// dead. The unique index on (org_id, email, purpose) is what makes this
	// the last word, and the conflict key names the campaign like every
	// other write in this package.
	//
	// The PURPOSE is part of both, above and here: the two kinds do not
	// compete. Asking for a sign-in link used to destroy a pending
	// invitation — one the invitee had not asked about, and would find dead
	// days later with nothing to tell them why.
	if _, err := tx.Exec(ctx,
		"INSERT INTO login_tokens(org_id, token_hash, email, purpose, expires_at, created_at) "+
			"VALUES($1,$2,$3,$4,$5,$6) "+
			"ON CONFLICT (org_id, email, purpose) DO UPDATE SET "+
			"token_hash=EXCLUDED.token_hash, expires_at=EXCLUDED.expires_at, "+
			"created_at=EXCLUDED.created_at",
		org, hashLinkToken(token), email, purpose,
		now.Add(life), shortTimestamp()); err != nil {
		return "", fmt.Errorf("recording a sign-in link: %w", err)
	}
	return token, nil
}

// redeemLink consumes a token, or answers that there was nothing to consume.
//
// DELETE … RETURNING is what makes it single-use: PostgreSQL lets exactly
// one caller through, whatever else arrives at the same instant, and it
// leaves no consumed row whose existence would then have to be told apart
// from a token that never was — a distinction this package refuses to make
// anyway.
//
// It commits IMMEDIATELY (s.renew), so that "this function returned an
// address" and "the row is gone for good" are the same event.
//
// Committed at the END of the route they were not: everything in between —
// reading the account, reading its departments, the commit itself — can
// fail, and a failed commit rolls the DELETE back. The link came back live
// after a 500, and after a client that hung up mid-query. The shape after
// that took a SECOND pool connection to make the spend independent, and
// deadlocked the pool under simultaneous redemptions. Renewing the
// request's own transaction costs neither.
//
// The expiry is compared against a bound parameter and not against
// PostgreSQL's now(): the server's clock is injectable (s.now), and a test
// that cannot move time cannot demonstrate an expiry.
func (s *Server) redeemLink(r *http.Request, token string) (string, string, error) {
	var email, purpose string
	err := s.tx(r).QueryRow(r.Context(),
		"DELETE FROM login_tokens WHERE org_id=$1 AND token_hash=$2 "+
			"AND expires_at>$3 RETURNING email, purpose",
		scopeOrg(r), hashLinkToken(token), s.now()).Scan(&email, &purpose)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("consuming a sign-in link: %w", err)
	}
	// Committed HERE, on the request's own connection, and a new transaction
	// opened behind it for the reads that follow. A commit that fails leaves
	// the token unspent and this function returns no address: spent-and-
	// committed or untouched, never one told as the other.
	if err := s.renew(r); err != nil {
		// …and untouched is not good enough, because a commit fails for
		// reasons of its own — a cluster rolling, a node evicted, a stall past
		// the bound — and none of them is a reason to hand back a link that
		// has just been presented. The transaction carrying the DELETE aborted,
		// so the row is there again, live for its whole remaining life: seven
		// days, for an invitation.
		//
		// The request's connection goes back FIRST, so this never holds two at
		// once: that shape deadlocked the pool. Nothing is read after this
		// point — the route answers 500 whatever happens here — and the second
		// attempt is best effort by construction: against a database that is
		// simply gone, nothing can be promised, and nothing is.
		org := scopeOrg(r)
		s.release(r)
		if again := s.spendAlone(r.Context(), org, token); again != nil {
			slog.Error("a sign-in link was presented and could not be spent: "+
				"it stays live until it expires", "error", again)
		}
		return "", "", fmt.Errorf("spending a sign-in link: %w", err)
	}
	return email, purpose, nil
}

// spendAlone deletes a token on a connection that owes nothing to the
// request — used when the request's own commit did not land.
//
// Detached from the request's context for the same reason the commit is: the
// caller who hung up is one of the ways to get here, and their cancellation
// must not decide whether the link they presented survives.
func (s *Server) spendAlone(ctx context.Context, org int, token string) error {
	ctx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring a connection to spend it: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx,
		"DELETE FROM login_tokens WHERE org_id=$1 AND token_hash=$2",
		org, hashLinkToken(token)); err != nil {
		return fmt.Errorf("spending it on its own connection: %w", err)
	}
	return nil
}

// linkURL: the address that goes into the email.
//
// The token travels in the FRAGMENT, and that buys two things no care in the
// handler could:
//
//   - A fragment is never sent to a server. It is in no ingress access log,
//     in no Referer, in no proxy's history: the token exists in the
//     recipient's browser and nowhere else on the wire.
//   - It is invisible to the URL scanners corporate mail systems run
//     (Outlook Safe Links and its kind), which FETCH every link they see. A
//     one-shot token in a query string is spent by the antivirus before the
//     recipient clicks, and what they get is a dead link. Here the scanner
//     receives /connexion — a page, and nothing else.
//
// It is therefore consumed by an explicit POST from the page, token in the
// BODY, which keeps it out of the route parameters on the way back too.
//
// The origin comes from PARAPHE_PUBLIC_URL, and the campaign is named by its
// SLUG rather than read off the request: an invitation is not always for the
// campaign being served — approving a hosting request runs on the apex and
// invites into the campaign it has just created.
func (s *Server) linkURL(slug, token string) string {
	return campaignURL(s.publicURL, slug).String() + linkPath + "#jeton=" + token
}

// linkPath: the page that reads the fragment. Any extension-less path is
// served the interface (pages.go), so this needs no routing on either side.
const linkPath = "/connexion"

// campaignURL: the origin of ONE campaign.
//
// Multi-campaign, the slug is prefixed to the configured apex, keeping its
// scheme and port — which is what makes `task try-instance` work on
// http://paraphe.test:8047 as well as production on https://paraphe.org.
// Single-campaign, and on the apex itself, the configured origin IS the
// answer.
func campaignURL(public *url.URL, slug string) *url.URL {
	out := *public
	if BaseDomain() == "" || slug == "" {
		return &out
	}
	host := slug + "." + public.Hostname()
	if port := public.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	}
	out.Host = host
	return &out
}

// campaignName: what the message calls the campaign. The apex has no
// campaign, and says the instance's name instead.
func campaignName(r *http.Request) string {
	if o := orgOf(r); o != nil && o.Name != "" {
		return o.Name
	}
	return "Paraphe"
}

// campaignSlug: the subdomain of the campaign being served, empty on the
// apex — which is exactly what campaignURL wants for the apex.
func campaignSlug(r *http.Request) string {
	if o := orgOf(r); o != nil {
		return o.Slug
	}
	return ""
}

// The subjects are CONSTANTS of this package, and the campaign's name — the
// one string in these messages a human typed — stays in the body, where
// base64 makes a header break unspellable. See buildMessage.
const (
	signInSubject     = "Votre lien de connexion"
	invitationSubject = "Votre accès à l'application de campagne"
)

func signInBody(campaign, url string) string {
	return fmt.Sprintf(`Bonjour,

Vous avez demandé un lien pour vous connecter à %s.

Ouvrez celui-ci dans les 15 minutes qui viennent :

%s

Il ne fonctionne qu'une seule fois.

Si vous n'avez rien demandé, ignorez ce message : votre compte est intact,
et ce lien expirera tout seul.

--
%s
Message automatique.
`, campaign, url, campaign)
}

func invitationBody(campaign, name, by, url string) string {
	return fmt.Sprintf(`Bonjour %s,

%s vous a ouvert un accès à %s, l'application avec laquelle l'équipe
contacte les maires.

Ouvrez ce lien pour entrer :

%s

Il est valable 7 jours et ne fonctionne qu'une seule fois. Votre session
reste ensuite ouverte 12 heures sur cet ordinateur.

--
%s
Message automatique.
`, name, by, campaign, url, campaign)
}

// --- routes ----------------------------------------------------------------

type linkRequest struct {
	Email string `json:"email"`
}

// The one answer POST /api/session/link gives. Constant on purpose: whether
// the address names an account, names a deactivated one, or names nothing at
// all, the caller reads this and learns nothing — the same promise the decoy
// hash makes on the password path.
const linkRequested = "Si un compte existe à cette adresse, un lien de " +
	"connexion vient d'y être envoyé. Il est valable 15 minutes."

// mailerOff: the refusal when no relay is configured. It says an instance
// property, not an account's — there is nothing to withhold here, and a
// volunteer clicking a button that quietly does nothing is worse than a
// sentence telling them to use their password.
func (s *Server) mailerOff(w http.ResponseWriter) bool {
	if s.mailer != nil {
		return false
	}
	errorJSON(w, http.StatusServiceUnavailable,
		"La connexion par email n'est pas configurée sur cette instance. "+
			"Utilisez votre mot de passe, ou demandez-en un à votre référent.")
	return true
}

// POST /api/session/link — ask for a sign-in link.
func (s *Server) routeRequestLink(w http.ResponseWriter, r *http.Request) {
	if s.mailerOff(w) {
		return
	}
	var d linkRequest
	if !readBody(w, r, &d) {
		return
	}
	email := normalizeEmail(d.Email)
	if email == "" {
		// The one refusal this route makes, and it tells nothing: an empty
		// field is a mistake in the form, not an answer about an account.
		errorJSON(w, http.StatusBadRequest, "Indiquez votre adresse email.")
		return
	}
	pseudonym := s.accountPseudonym(email)
	s.securityEvent(r, slog.LevelInfo, "signin_link_requested", "account", pseudonym)

	// Bounded to the campaign being served, like the sign-in query: an
	// address that exists in a neighbouring campaign is, from here, unknown.
	var name string
	var active bool
	err := s.tx(r).QueryRow(r.Context(),
		"SELECT name, active FROM accounts WHERE org_id=$1 AND email=$2",
		scopeOrg(r), email).Scan(&name, &active)
	switch {
	case errors.Is(err, pgx.ErrNoRows), err == nil && !active:
		// Nothing minted, nothing sent, and the SAME answer below. A
		// deactivated account is deliberately in this branch: it is exactly
		// the situation deactivation exists for — a phished account whose
		// holder is the attacker — and "your account is switched off" would
		// confirm the address to them.
		replyJSON(w, http.StatusOK, map[string]string{"message": linkRequested})
		return
	case err != nil:
		s.failure(w, err)
		return
	}

	// Everything that DIFFERS between the two branches happens after the
	// answer, and that is not a preference — it is the rest of the promise.
	//
	// Detaching the send alone was not enough: minting a token is a DELETE,
	// an INSERT and a COMMIT, and an account that exists paid for them
	// before its reply. Measured, the known address answered in ~1.6 ms and
	// the unknown one in ~0.3 ms — a stopwatch handing back exactly the
	// roster the constant sentence withholds, three to six times over. Both
	// branches now reply on the same SELECT, and everything else is on the
	// other side of it.
	org, slug, campaign := scopeOrg(r), campaignSlug(r), campaignName(r)
	replyJSON(w, http.StatusOK, map[string]string{"message": linkRequested})
	s.detach(smtpDialTimeout+smtpIOTimeout, func(ctx context.Context) {
		// The one failure path in this package that is not answered, and the
		// reason is the whole design of this route: "we could not do it"
		// means "this account exists". The operator reads these lines; the
		// caller reads the same sentence as everybody else.
		token, err := s.mintDetached(ctx, org, email)
		if err != nil {
			slog.Error("sign-in link not drawn", "account", pseudonym, "error", err)
			return
		}
		body := signInBody(campaign, s.linkURL(slug, token))
		// The relay's own words go into the log, and a refusal often quotes
		// the recipient back: the address comes out of them first.
		if err := s.mailer.Send(ctx, email, signInSubject, body); err != nil {
			slog.Error("sign-in link not sent", "account", pseudonym,
				"error", s.withoutAddress(err, email))
		}
	})
}

// mintDetached draws a token outside any request, on a connection of its own.
//
// The request's transaction is gone by the time this runs — it was rolled
// back with the answer, which is the point: the caller must not be able to
// time the writing of a row that only exists for an account that exists.
func (s *Server) mintDetached(ctx context.Context, org int, email string) (string, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return "", fmt.Errorf("acquiring a connection: %w", err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("opening a transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	token, err := newLinkToken(ctx, tx, org, email, purposeSignIn, s.now(),
		signInLinkLife)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("committing the sign-in link: %w", err)
	}
	return token, nil
}

type redeemRequest struct {
	Token string `json:"token"`
}

// POST /api/session/link/redeem — exchange a link's token for a session.
func (s *Server) routeRedeemLink(w http.ResponseWriter, r *http.Request) {
	var d redeemRequest
	if !readBody(w, r, &d) {
		return
	}
	// One refusal for every way a link fails to work: expired, already used,
	// never existed, or naming an account that has since been switched off.
	// There is nothing to learn from the difference, so there is nothing to
	// say about it.
	refuse := func() {
		s.securityEvent(r, slog.LevelInfo, "signin_link_failed")
		errorJSON(w, http.StatusUnauthorized,
			"Ce lien n'est plus valable. Demandez-en un nouveau depuis "+
				"l'écran de connexion.")
	}
	if d.Token == "" {
		refuse()
		return
	}
	email, purpose, err := s.redeemLink(r, d.Token)
	if err != nil {
		s.failure(w, err)
		return
	}
	if email == "" {
		// nothing matched, so nothing was deleted and there is nothing to keep
		refuse()
		return
	}

	// The token is SPENT by now — redeemLink committed it on its own
	// connection — so nothing below can hand it back, whichever way this
	// ends. A link presented against a DEACTIVATED account is refused AND
	// gone: it used to be refused and restored, and whoever held a copy only
	// had to wait for the account to be switched back on. Seven days, for an
	// invitation.
	c, err := s.readAccount(r, email)
	if err != nil {
		s.failure(w, err)
		return
	}
	if c == nil {
		refuse()
		return
	}
	departments, err := s.teamDepartments(r, c)
	if err != nil {
		s.failure(w, err)
		return
	}
	// The purpose travels into the log and nowhere else, and that is what it
	// is for: the row is gone by now, so this line is the only place an
	// operator can see whether a campaign's invitations are being taken up
	// or whether people are recovering forgotten passwords.
	s.openSession(w, r, c, departments, "signin_link_succeeded",
		"link", purpose)
}

// --- invitations -----------------------------------------------------------

// invitation: everything one needs to be sent. Gathered while the
// transaction is open and sent once it has closed, and carrying its campaign
// EXPLICITLY — the campaign an invitation belongs to is not always the one
// the request is served for.
type invitation struct {
	email, name, by, campaign, slug, token string
}

// mintInvitation draws the invitation token IN THE TRANSACTION that creates
// the account. The two live or die together: an invitation whose account
// rolled back opens nothing, and an account whose token vanished is a
// volunteer nobody wrote to.
//
// An empty token when no relay is configured — there is nothing to carry it,
// and a row nobody can deliver is a row for nothing.
func (s *Server) mintInvitation(ctx context.Context, tx pgx.Tx, org int,
	email string) (string, error) {
	if s.mailer == nil {
		return "", nil
	}
	return newLinkToken(ctx, tx, org, email, purposeInvitation, s.now(),
		invitationLinkLife)
}

// sendInvitation sends it, SYNCHRONOUSLY, and reports what happened. Call it
// AFTER the commit: a link must not arrive before the account it opens.
//
// The opposite rule from the sign-in link, for the opposite reason: the
// caller here is authenticated and has just created the account themselves,
// so there is no existence left to protect and every reason to tell them
// whether the message left. The generated password stays on their screen
// either way — relay down, the lead reads it out as they always have, and
// nothing about today's path gets worse.
func (s *Server) sendInvitation(inv invitation) (bool, string) {
	if inv.token == "" || s.mailer == nil {
		return false, ""
	}
	// NOT the request's context. It is cancelled the moment the caller's
	// browser hangs up, and by this point the account is committed while its
	// password exists in one place only — the answer being written. A
	// disconnect there left a volunteer with an account nobody can open: the
	// invitation cancelled mid-flight, the password gone with the response,
	// and a retry answering "this address already has an account".
	ctx, cancel := context.WithTimeout(context.Background(),
		smtpDialTimeout+smtpIOTimeout)
	defer cancel()
	by := inv.by
	if by == "" {
		by = "La coordination"
	}
	if err := s.mailer.Send(ctx, inv.email, invitationSubject,
		invitationBody(inv.campaign, inv.name, by,
			s.linkURL(inv.slug, inv.token))); err != nil {
		slog.Error("invitation not sent",
			"account", s.accountPseudonym(inv.email),
			"error", s.withoutAddress(err, inv.email))
		return false, "L'invitation n'a pas pu partir : communiquez " +
			"le mot de passe ci-dessus par un autre moyen."
	}
	return true, ""
}
