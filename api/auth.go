package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type contextKey int

const accountKey contextKey = 0

type Account struct {
	Email        string  `json:"email"`
	Name         string  `json:"name"`
	Role         string  `json:"role"`
	TeamID       *int    `json:"team_id"`
	Active       bool    `json:"active"`
	PersonalNote string  `json:"personal_note"`
	CreatedAt    string  `json:"created_at"`
	CreatedBy    string  `json:"created_by"`
	TeamName     *string `json:"team_name"`
	// This volunteer's own answer to « do I telephone the mayors I write
	// to », or nil when they have not answered — in which case the
	// campaign's own setting applies, AS IT CHANGES rather than as it stood
	// the day the account was made. Three states, and a bool could hold only
	// two.
	PhoneOutreach *bool `json:"phone_outreach"`
	// When the password last changed, and `json:"-"` because it is nobody's
	// business but this server's: it is compared against the session token's
	// own instant, never displayed. NULL for an account that has never
	// changed one, which is every account older than the column.
	PasswordChangedAt *time.Time `json:"-"`
}

// Password hashes never leave the database: the selection is explicit,
// never `SELECT *` — a newly added column must not travel to the browser
// because nobody re-read the query. Shared by every account read, so the
// promise lives in one place.
const accountColumns = "c.email, c.name, c.role, c.team_id, c.active, " +
	"COALESCE(c.personal_note,'') AS personal_note, COALESCE(c.created_at,'') AS created_at, " +
	"COALESCE(c.created_by,'') AS created_by, g.name AS team"

// MyTeam: 0 for an account without a team ("national" scope).
func (c *Account) MyTeam() int {
	if c == nil || c.TeamID == nil {
		return NationalTeam
	}
	return *c.TeamID
}

func (c *Account) Coordination() bool { return c != nil && c.Role == RoleCoordination }

// Administration: instance administrator. Has NO power inside a campaign:
// its organisation is the instance scope, which owns no campaign row.
func (c *Account) Administration() bool {
	return c != nil && c.Role == RoleAdministration
}

func (c *Account) MayManage() bool {
	return c != nil && (c.Role == RoleCoordination || c.Role == RoleLead)
}

func accountOf(r *http.Request) *Account {
	c, _ := r.Context().Value(accountKey).(*Account)
	return c
}

// query accumulates arguments and returns the $n placeholders in order. The
// filters on these screens compose (search, status, department, rank, team
// scope): numbering by hand is the surest way to shift an argument and
// query the wrong column.
type query struct{ args []any }

func (q *query) p(v any) string {
	q.args = append(q.args, v)
	return fmt.Sprintf("$%d", len(q.args))
}

// scoped opens a query already bound to the campaign, which is therefore
// ALWAYS $1 — so the SQL can name it literally.
//
// Bound anywhere else, the placeholder would exist only at run time: read
// from the source the predicate would be `org_id=` with nothing on its right,
// and the canary would have to accept "a right-hand side I cannot read" as
// bounded — which any one-line helper could then satisfy with a tautology.
// Here the campaign is $1 by construction, and there is nothing to trust.
func scoped(r *http.Request) *query {
	q := &query{}
	q.p(scopeOrg(r)) // $1, and the SQL below says so in full
	return q
}

// readAccount re-reads the account from the database. Called on EVERY
// request: that is what makes deactivation immediate, with no session table
// and no denylist. The read goes through the request's transaction, hence
// by its own predicate: it cannot return another campaign's account, even a
// namesake.
func (s *Server) readAccount(r *http.Request, email string) (*Account, error) {
	var c Account
	// password_changed_at is NOT in accountColumns, and that is deliberate:
	// the two other readers of that list hand their rows to the browser as
	// maps (queries.go, RowToMap), so a column added there travels to every
	// manager's screen without anyone re-reading the query — which is the
	// exact promise written above the list. Named here, at the one site that
	// scans into a struct and needs it.
	err := s.tx(r).QueryRow(r.Context(),
		"SELECT "+accountColumns+", c.password_changed_at, c.phone_outreach"+
			" FROM accounts c LEFT JOIN teams g "+
			"ON g.id = c.team_id AND g.org_id = c.org_id "+
			"WHERE c.org_id=$1 AND c.email=$2 AND c.active", scopeOrg(r), email).
		Scan(&c.Email, &c.Name, &c.Role, &c.TeamID, &c.Active, &c.PersonalNote,
			&c.CreatedAt, &c.CreatedBy, &c.TeamName, &c.PasswordChangedAt,
			&c.PhoneOutreach)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("re-reading the account: %w", err)
	}
	return &c, nil
}

// signedIn requires a valid session and an active account. It also opens
// the scope: no authenticated route can run without a declared scope, which
// makes the omission impossible rather than unlikely.
func (s *Server) signedIn(next http.HandlerFunc) http.HandlerFunc {
	return s.inScope(func(w http.ResponseWriter, r *http.Request) {
		email, org, issued, ok := s.sessions.Read(r, s.now())
		if !ok {
			s.sessions.Clear(w)
			errorJSON(w, http.StatusUnauthorized, "Session absente ou expirée.")
			return
		}
		// The cookie names one campaign. Cookies are already partitioned by
		// host (no Domain attribute is set), but the check costs nothing and
		// survives the day someone adds one to share a session across
		// subdomains.
		if org != currentOrg(r) {
			s.sessions.Clear(w)
			errorJSON(w, http.StatusUnauthorized,
				"Cette session appartient à une autre campagne. Reconnectez-vous.")
			return
		}
		c, err := s.readAccount(r, email)
		if err != nil {
			s.failure(w, err)
			return
		}
		if c == nil {
			// account deleted or deactivated during the session
			s.sessions.Clear(w)
			errorJSON(w, http.StatusUnauthorized,
				"Ce compte n'est plus actif. Voyez votre référent.")
			return
		}
		// A PASSWORD CHANGE SIGNS THE OTHER SESSIONS OUT, and this is the
		// whole of the mechanism: a token minted before the change is not a
		// token this account still recognises. The commonest reason to change
		// a password is believing it has leaked, and without this the session
		// of whoever took it outlives the change — a remedy discovered by
		// paying for it.
		//
		// Free, because this handler already re-reads the account on every
		// request: it is one more column in a SELECT that was happening
		// anyway, compared in Go.
		//
		// The instant is stored truncated to the SECOND and the token's `iat`
		// is Unix seconds, so the cookie minted by the change itself — same
		// second, same clock — is not before it and survives. Both come from
		// s.now(): PostgreSQL's clock is a different one, and a skew of a
		// second between them would sign a volunteer out of the very session
		// they just re-secured.
		if c.PasswordChangedAt != nil && issued.Before(*c.PasswordChangedAt) {
			s.sessions.Clear(w)
			errorJSON(w, http.StatusUnauthorized,
				"Le mot de passe de ce compte a changé. Reconnectez-vous.")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), accountKey, c)))
	})
}

// currentOrg: the identifier of the scope being served — a campaign's, or
// OrgInstance on the apex.
func currentOrg(r *http.Request) int {
	if o := orgOf(r); o != nil {
		return o.ID
	}
	return OrgInstance
}

// inCampaign: the work routes. Served from the apex they have no campaign
// to query — better to say so than to dereference an absent scope.
func (s *Server) inCampaign(next http.HandlerFunc) http.HandlerFunc {
	return s.signedIn(func(w http.ResponseWriter, r *http.Request) {
		if orgOf(r) == nil {
			errorJSON(w, http.StatusNotFound,
				"Cette page appartient à une campagne. Rejoignez celle de votre "+
					"équipe : <campagne>.%s", BaseDomain())
			return
		}
		next(w, r)
	})
}

// managers: coordination and team leads only.
func (s *Server) managers(next http.HandlerFunc) http.HandlerFunc {
	return s.inCampaign(func(w http.ResponseWriter, r *http.Request) {
		if !accountOf(r).MayManage() {
			errorJSON(w, http.StatusForbidden,
				"Seuls la coordination et les référents gèrent les accès.")
			return
		}
		next(w, r)
	})
}

// coordinationOnly: team creation, overview.
func (s *Server) coordinationOnly(next http.HandlerFunc) http.HandlerFunc {
	return s.inCampaign(func(w http.ResponseWriter, r *http.Request) {
		if !accountOf(r).Coordination() {
			errorJSON(w, http.StatusForbidden, "Réservé à la coordination.")
			return
		}
		next(w, r)
	})
}

// administrationOnly: moderating hosting requests. The role can only be
// obtained through bootstrap (validRole deliberately ignores it) and only
// lives in the instance scope — a campaign's coordination can therefore
// neither grant it to itself nor reach it from its subdomain.
func (s *Server) administrationOnly(next http.HandlerFunc) http.HandlerFunc {
	return s.signedIn(func(w http.ResponseWriter, r *http.Request) {
		if orgOf(r) != nil || !accountOf(r).Administration() {
			errorJSON(w, http.StatusForbidden,
				"Réservé à l'administration de l'instance.")
			return
		}
		next(w, r)
	})
}

// jsonOnly: second anti-CSRF barrier, after SameSite=Lax. A form on another
// site cannot post application/json without a CORS preflight, which the API
// does not allow.
func jsonOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		if i := strings.IndexByte(ct, ';'); i >= 0 {
			ct = ct[:i]
		}
		if strings.TrimSpace(ct) != "application/json" {
			errorJSON(w, http.StatusUnsupportedMediaType,
				"Content-Type: application/json attendu.")
			return
		}
		// The body is read HERE, before anything downstream takes a
		// database connection. inScope opens its transaction before the
		// handler calls readBody, so a caller dribbling one byte held a
		// pool connection idle in transaction for ReadTimeout: four such
		// sockets took the whole pool of a small VPS (pgx defaults to
		// max(4, NumCPU)) and every authenticated request hung — for zero
		// CPU and zero memory on the attacker's side.
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodySize))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				// distinguishable from invalid JSON: the client can shorten
				// what it sent instead of hunting a syntax error
				errorJSON(w, http.StatusRequestEntityTooLarge,
					"Le contenu envoyé dépasse %d Ko.", maxBodySize/1024)
				return
			}
			errorJSON(w, http.StatusBadRequest, "Corps de requête illisible : %v", err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		next(w, r)
	}
}

// maxBodySize: a volunteer's note or a nine-field campaign, not a file.
// jsonOnly reads the body before authentication, so this is also what an
// anonymous socket can make the process hold: at 1 MiB, 64 sockets held
// 73 MiB and the server answered `100 Continue` to a caller with no
// session at all. The largest legitimate body is a campaign — nine
// fields bounded at 2 000 runes — which fits ten times over.
const maxBodySize = 128 << 10 // 128 KiB

// Text ceilings, in RUNES — the messages that announce them promise
// characters. Everything a human types into this application goes through
// one of them; what is not bounded here is bounded by nothing, since no
// row is ever deleted.
const (
	maxNoteRunes     = 5000 // a call note, and the public form's message
	maxCampaignRunes = 2000 // one campaign value, whoever writes it
	maxNameRunes     = 200  // a team name, a campaign name, a person's name
	maxEmailRunes    = 254  // the RFC ceiling
)

// bufferBody drains the request body into memory, bounded, and replaces it
// with the buffered copy. openScope calls it BEFORE acquiring a pooled
// connection, so a client sending its body slowly cannot hold a connection
// open while it dribbles.
//
// Idempotent and cheap on the paths that already buffered: a body jsonOnly
// read is re-read from memory here, and a request with no body returns at once.
// The 413 mirrors jsonOnly's, so a caller learns to shorten what it sent
// whichever guard buffers first.
func bufferBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodySize))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			errorJSON(w, http.StatusRequestEntityTooLarge,
				"Le contenu envoyé dépasse %d Ko.", maxBodySize/1024)
			return false
		}
		errorJSON(w, http.StatusBadRequest, "Corps de requête illisible : %v", err)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return true
}

func readBody(w http.ResponseWriter, r *http.Request, target any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySize))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		errorJSON(w, http.StatusBadRequest, "Corps de requête illisible : %v", err)
		return false
	}
	// One refusal for every write route: PostgreSQL rejects U+0000 in any
	// text value (22021) and jsonb rejects its escape (22P05), so a NUL in
	// a note, a team name or the public request form answered « erreur
	// interne » on the sender's own screen. Guarding each handler was
	// tried: the first pass covered one write path out of ten.
	if carriesNul(reflect.ValueOf(target)) {
		errorJSON(w, http.StatusBadRequest,
			"Le texte envoyé contient un caractère invalide (octet nul).")
		return false
	}
	return true
}

// carriesNul walks every string a decoded body carries.
func carriesNul(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return strings.ContainsRune(v.String(), 0)
	case reflect.Pointer, reflect.Interface:
		return !v.IsNil() && carriesNul(v.Elem())
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if carriesNul(v.Index(i)) {
				return true
			}
		}
	case reflect.Map:
		for iter := v.MapRange(); iter.Next(); {
			if carriesNul(iter.Key()) || carriesNul(iter.Value()) {
				return true
			}
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if carriesNul(v.Field(i)) {
				return true
			}
		}
	}
	return false
}

func replyJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Every API answer is either personal to a session or freshly computed;
	// none is for a cache. Without the header, a shared proxy is ALLOWED to
	// cache a 200 heuristically — and these bodies carry nominative data.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// the header is already gone: nothing more can be said to the
		// client, but the operator must see it
		slog.Error("truncated JSON response", "error", err)
	}
}

func errorJSON(w http.ResponseWriter, code int, format string, args ...any) {
	replyJSON(w, code, map[string]string{"error": fmt.Sprintf(format, args...)})
}

// failure: an internal error is logged in full and not narrated to the
// client — the details of a SQL query have no business in a browser.
func (s *Server) failure(w http.ResponseWriter, err error) {
	slog.Error("internal error", "error", err)
	errorJSON(w, http.StatusInternalServerError,
		"Erreur interne. Si elle persiste, prévenez la coordination.")
}
