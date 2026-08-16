package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Asking a campaign to open a local team.
//
// The form is public on a campaign's own address, and like the instance's
// hosting form it creates NOTHING: a team carries a work perimeter, and
// whoever leads it signs in, draws cards and reads the notes of everyone in
// it. Both are the coordination's to decide, so the request waits for it —
// and the coordination corrects the name and the perimeter as it accepts,
// because the person filling the form knows their department, not the
// campaign's map.

// maxPendingTeamRequests bounds STORAGE, per campaign: a request is never
// deleted. The queue screen returns every pending one, so nothing is ever
// hidden behind this ceiling — reaching it means an attack, and the sentence
// that refuses points at the coordination, who can open the team directly.
const maxPendingTeamRequests = 200

// nameNotAvailable: the ONE refusal both early checks give. Two sentences —
// « a team already bears it » and « a request is pending on it » — answered a
// question nobody asked: an anonymous visitor learned which of the two a name
// hit, and a campaign's team names are in no public route.
const nameNotAvailable = "Le nom « %s » n'est pas disponible dans cette " +
	"campagne. Choisissez-en un autre."

type teamRequestForm struct {
	Name           string   `json:"name"`
	Departments    []string `json:"departments"`
	RequesterEmail string   `json:"requester_email"`
	RequesterName  string   `json:"requester_name"`
	Message        string   `json:"message"`
}

// POST /api/team/request — public form, on a campaign.
func (s *Server) routeTeamRequest(w http.ResponseWriter, r *http.Request) {
	var d teamRequestForm
	if !readBody(w, r, &d) {
		return
	}
	name := strings.TrimSpace(d.Name)
	requester := strings.TrimSpace(d.RequesterName)
	email := normalizeEmail(d.RequesterEmail)

	// `visible`, not `== ""`: a name of zero-width runes survives TrimSpace
	// and lands in the queue as a blank row the coordination cannot tell from
	// the next blank row.
	//
	// `storableEmail`, the same reader the team form uses: an address is
	// refused here or it becomes an account this application can never write
	// to, and the address is the primary key, so it cannot be corrected after.
	if !visible(name) || !visible(requester) || !storableEmail(email) {
		errorJSON(w, http.StatusBadRequest,
			"Le nom de l'équipe, votre nom et votre adresse email sont requis.")
		return
	}
	// This address BECOMES the primary key of `accounts` if the request is
	// accepted, and the name the primary key of `teams`: both columns are
	// btree-indexed, and past ~2 690 bytes PostgreSQL refuses the index row
	// (54000) — on the COORDINATION's screen, for text an anonymous form
	// wrote. Same ceilings as the team forms, for the same reason.
	if utf8.RuneCountInString(email) > maxEmailRunes {
		errorJSON(w, http.StatusBadRequest,
			"Cette adresse email est trop longue (254 caractères maximum).")
		return
	}
	if utf8.RuneCountInString(name) > maxNameRunes ||
		utf8.RuneCountInString(requester) > maxNameRunes {
		errorJSON(w, http.StatusBadRequest,
			"Le nom de l'équipe et votre nom ne doivent pas dépasser "+
				"200 caractères.")
		return
	}
	// The coordination READS these three before it decides on them, and the
	// address is read like the rest: `storableEmail` above asks whether the
	// application can still WRITE to it — control characters in a header —
	// and this asks whether what the moderator reads is what is stored. A
	// bidi override and a byte-order mark pass the first and fail the second.
	if !legible(name) || !legible(requester) || !legible(email) {
		errorJSON(w, http.StatusBadRequest,
			"Le nom de l'équipe, votre nom et votre adresse email ne doivent "+
				"contenir ni retour à la ligne ni caractère invisible.")
		return
	}
	if utf8.RuneCountInString(d.Message) > maxNoteRunes {
		errorJSON(w, http.StatusBadRequest,
			"Votre message ne doit pas dépasser 5000 caractères.")
		return
	}
	departments, unknown, err := s.knownDepartments(r, d.Departments)
	if err != nil {
		s.failure(w, err)
		return
	}
	if unknown != "" {
		errorJSON(w, http.StatusBadRequest,
			"« %s » ne correspond à aucun département de la liste.", unknown)
		return
	}

	// A name already spoken for is refused EARLY, as the hosting form refuses
	// a taken address: the requester picks another one now instead of
	// learning it after moderation.
	//
	// ONE sentence for both doors, deliberately. Saying « a team already
	// bears that name » where the other says « a request is pending » told a
	// visitor with no account which of the two a name hit — and a campaign's
	// team names appear in no public route. The hosting form may distinguish
	// them because a slug is public by construction: every subdomain answers.
	var taken bool
	if err := s.tx(r).QueryRow(r.Context(),
		"SELECT EXISTS(SELECT 1 FROM teams WHERE org_id=$1 AND name=$2)",
		scopeOrg(r), name).Scan(&taken); err != nil {
		s.failure(w, err)
		return
	}
	if !taken {
		if err := s.tx(r).QueryRow(r.Context(),
			"SELECT EXISTS(SELECT 1 FROM team_requests WHERE org_id=$1 AND name=$2 "+
				"AND state=$3)", scopeOrg(r), name, RequestPending).Scan(&taken); err != nil {
			s.failure(w, err)
			return
		}
	}
	if taken {
		errorJSON(w, http.StatusConflict, nameNotAvailable, name)
		return
	}

	// The ceiling rides IN the insert rather than in a count read before it —
	// but it is a bound with slack, not an invariant, and the difference
	// matters enough to write down.
	//
	// Under READ COMMITTED, which is what every request runs at, the count
	// subquery is evaluated on the transaction's own snapshot: concurrent
	// writers each see the same total and each pass the test. Merging the
	// read into the write narrows the window and closes nothing.
	//
	// What bounds the overshoot is how many transactions can hold a snapshot
	// at once — `max_connections`, not any constant. Measured: 95 dedicated
	// connections released together against a table at 199 leave it at 294.
	// Through the pool the API actually uses, the same test overshoots by one
	// or two, which is the number this comment used to state and it was the
	// wrong one to reason from.
	//
	// It is left that way deliberately. An exact cap needs
	// `pg_advisory_xact_lock` per campaign or SERIALIZABLE with a retry, and
	// a blocking lock on an anonymous form makes every request queue while
	// holding a pool connection — the failure this codebase buffers request
	// bodies to avoid (auth.go, jsonOnly). What the slack costs is a heavier
	// moderation payload (1.6 MiB measured at 294 pending, served in 250 ms);
	// what it must NOT cost is a request the coordination never sees, and
	// that is why the queue below reads without a LIMIT.
	var id int64
	err = s.tx(r).QueryRow(r.Context(),
		"INSERT INTO team_requests(org_id, name, departments, requester_email, "+
			"requester_name, message, state, ts) "+
			"SELECT $1,$2,$3,$4,$5,$6,$7,$8 WHERE (SELECT count(*) FROM "+
			"team_requests WHERE org_id=$1 AND state=$7) < $9 RETURNING id",
		orgOf(r).ID, name, strings.Join(departments, ";"), email, requester,
		strings.TrimSpace(d.Message), RequestPending, shortTimestamp(),
		maxPendingTeamRequests).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		errorJSON(w, http.StatusServiceUnavailable,
			"Trop de demandes attendent la coordination de cette campagne. "+
				"Écrivez-lui : elle peut ouvrir votre équipe sans passer par "+
				"ce formulaire.")
		return
	}
	if err != nil {
		// The loser of the race against the partial unique index: the check
		// above read « free » a moment before the other insert committed.
		// Same answer as that check gives, not a 500.
		if isUniqueViolation(err) {
			errorJSON(w, http.StatusConflict, nameNotAvailable, name)
			return
		}
		s.failure(w, err)
		return
	}
	// The recipients of the notice, read while the transaction is open: the
	// coordination this request lands on is the one of this snapshot.
	var recipients []string
	if s.mailer != nil {
		rows, err := s.tx(r).Query(r.Context(),
			"SELECT email FROM accounts WHERE org_id=$1 AND role=$2 AND active",
			scopeOrg(r), RoleCoordination)
		if err != nil {
			s.failure(w, err)
			return
		}
		for rows.Next() {
			var e string
			if err := rows.Scan(&e); err != nil {
				rows.Close()
				s.failure(w, err)
				return
			}
			recipients = append(recipients, e)
		}
		if err := rows.Err(); err != nil {
			s.failure(w, err)
			return
		}
	}
	campaign, slug := campaignName(r), campaignSlug(r)
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	// the pool connection has no business waiting on a relay
	s.release(r)
	// The identity is NOT returned. `team_requests.id` is one sequence for the
	// whole table, hence for every campaign on the instance: handed to an
	// anonymous visitor, the gap between two of them counts what the
	// neighbouring campaigns received. The coordination finds the request in
	// its queue; the visitor has no route that takes an identity.
	replyJSON(w, http.StatusCreated, map[string]any{
		"name": name,
		"message": "Demande enregistrée. La coordination de la campagne " +
			"l'examinera et vous répondra à " + email + ".",
	})
	// Detached (s.detach): the visitor observes neither the relay's slowness
	// nor its existence.
	s.notifyTeamRequest(campaign, slug, name, strings.Join(departments, ", "),
		requester, email, recipients)
}

// knownDepartments keeps what the mayor list actually carries and names the
// first label matching nothing.
//
// A perimeter of labels no mayor bears is a team that draws zero cards for
// ever, and nothing anywhere says why: the batch route simply finds no
// available mayor in « Nord-Pas-de-Calais » and answers that the pool is
// empty. The hosting form has no equivalent because a slug is checked
// against itself; a department is checked against data.
func (s *Server) knownDepartments(r *http.Request, wanted []string) ([]string, string, error) {
	var asked []string
	for _, x := range wanted {
		if x = strings.TrimSpace(x); x != "" && !slices.Contains(asked, x) {
			asked = append(asked, x)
		}
	}
	if len(asked) == 0 {
		return nil, "", nil
	}
	known, err := s.departmentLabels(r)
	if err != nil {
		return nil, "", err
	}
	for _, x := range asked {
		if !slices.Contains(known, x) {
			return nil, x, nil
		}
	}
	return asked, "", nil
}

// teamRequestColumns: what the coordination's queue shows. The requester's
// address is among them — it is who the campaign is about to hand a team to,
// and moderating without seeing it would be moderating nothing.
const teamRequestColumns = "id, name, COALESCE(departments,'') AS departments, " +
	"requester_email, requester_name, message, state, " +
	"COALESCE(reason,'') AS reason, COALESCE(ts,'') AS ts, " +
	"COALESCE(decided_at,'') AS decided_at, " +
	"COALESCE(decided_by,'') AS decided_by FROM team_requests "

// teamRequests: the queue served inside /api/team, to the coordination only.
//
// EVERY pending request, and only then the last decided ones. A single LIMIT
// over both would let a flood of requests push a real team's off the only
// screen that can accept it, with nobody able to tell.
//
// The pending read carries NO ceiling of its own either. Bounded at
// maxPendingTeamRequests it was bounded by a number the insert only
// approximated: a race past the cap left the oldest — the legitimate early
// requests — below the cut, invisible on the one screen that can accept them,
// while the flood sat on top. The insert now applies the ceiling itself, and
// this read shows whatever got through it.
func (s *Server) teamRequests(r *http.Request) ([]map[string]any, error) {
	pending, err := s.rows(r, "SELECT "+teamRequestColumns+
		"WHERE org_id=$1 AND state='pending' ORDER BY id DESC", scopeOrg(r))
	if err != nil {
		return nil, err
	}
	decided, err := s.rows(r, "SELECT "+teamRequestColumns+
		"WHERE org_id=$1 AND state<>'pending' ORDER BY id DESC LIMIT 50",
		scopeOrg(r))
	if err != nil {
		return nil, err
	}
	return append(pending, decided...), nil
}

type teamDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	// What the coordination actually opens. Absent, the request's own values
	// stand; present, they replace them — the person who filled the form
	// knows their department, not the campaign's map, and correcting a
	// perimeter must not mean refusing and asking them to type it again.
	Name        *string  `json:"name"`
	Departments []string `json:"departments"`
}

// POST /api/team/requests/{id} — accept (hence create the team and its lead)
// or refuse.
func (s *Server) routeDecideTeamRequest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(pathParam(r, "id"), 10, 64)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, "Identifiant de demande illisible.")
		return
	}
	var d teamDecision
	if !readBody(w, r, &d) {
		return
	}
	if d.Decision != RequestAccepted && d.Decision != RequestRefused {
		errorJSON(w, http.StatusBadRequest,
			"Décision inconnue : %q (accepted ou refused).", d.Decision)
		return
	}
	ctx := r.Context()
	me := accountOf(r)

	// FOR UPDATE, and the campaign named in the same breath: two members of
	// the coordination can process the same request at the same moment, and
	// accepting twice would open two teams for one request — or collide on
	// the name index, saying nothing useful.
	var name, departments, requesterEmail, requesterName, state string
	err = s.tx(r).QueryRow(ctx,
		"SELECT name, COALESCE(departments,''), requester_email, requester_name, "+
			"state FROM team_requests WHERE org_id=$1 AND id=$2 FOR UPDATE",
		scopeOrg(r), id).
		Scan(&name, &departments, &requesterEmail, &requesterName, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		errorJSON(w, http.StatusNotFound, "Aucune demande n'a l'identifiant %d.", id)
		return
	}
	if err != nil {
		s.failure(w, err)
		return
	}
	if state != RequestPending {
		errorJSON(w, http.StatusConflict,
			"Cette demande a déjà été traitée (%s).", state)
		return
	}

	response := map[string]any{"id": id, "decision": d.Decision}
	var invitationToken string
	if d.Decision == RequestAccepted {
		// The row was written by a public form and is read back HERE to open
		// an account from it. Hardening that form does not harden what is
		// already in the table, and rows filed before it are still pending on
		// a live instance: one can carry an address that renders as somebody
		// else's, which then becomes a primary key nobody can correct. This
		// is the last place it can be caught, and refusing the request is
		// still open to the coordination.
		if !storableEmail(requesterEmail) || !legible(requesterEmail) ||
			!visible(requesterName) || !legible(requesterName) {
			errorJSON(w, http.StatusConflict,
				"Cette demande a été enregistrée avant que la saisie ne soit "+
					"durcie : son adresse ou son nom ne peuvent pas ouvrir de "+
					"compte. Refusez-la en l'expliquant.")
			return
		}
		if d.Name != nil {
			name = strings.TrimSpace(*d.Name)
			if utf8.RuneCountInString(name) > maxNameRunes {
				errorJSON(w, http.StatusBadRequest,
					"Le nom de l'équipe ne doit pas dépasser 200 caractères.")
				return
			}
		}
		// Whether it was edited here or read from the row, this name becomes
		// `teams.name` and every volunteer of the campaign reads it.
		if !visible(name) {
			errorJSON(w, http.StatusBadRequest, "Le nom de l'équipe est requis.")
			return
		}
		if !legible(name) {
			errorJSON(w, http.StatusBadRequest,
				"Le nom de l'équipe ne doit contenir ni retour à la ligne ni "+
					"caractère invisible.")
			return
		}

		// The perimeter that will be WRITTEN is read, whether coordination
		// edited it or accepted the stored one. Checking only the edit left
		// the stored labels of a row filed before this guard existed — or
		// before a department left the list — to open a team that draws zero
		// cards for ever, on a screen that said nothing.
		wanted := d.Departments
		if wanted == nil {
			wanted = splitDepartments(departments)
		}
		perimeter, unknown, err := s.knownDepartments(r, wanted)
		if err != nil {
			s.failure(w, err)
			return
		}
		if unknown != "" {
			errorJSON(w, http.StatusBadRequest,
				"« %s » ne correspond à aucun département de la liste. "+
					"Corrigez le périmètre avant d'accepter.", unknown)
			return
		}
		// An account already exists under this address: it has a role and a
		// team of its own, and silently making it the lead of a new one would
		// move somebody without a word. Said, and refused.
		var exists bool
		if err := s.tx(r).QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM accounts WHERE org_id=$1 AND email=$2)",
			scopeOrg(r), requesterEmail).Scan(&exists); err != nil {
			s.failure(w, err)
			return
		}
		if exists {
			errorJSON(w, http.StatusConflict,
				"Un compte existe déjà pour %s. Créez l'équipe vous-même et "+
					"rattachez-lui ce compte, ou refusez la demande en "+
					"l'expliquant.", requesterEmail)
			return
		}
		teamID, err := insertTeam(ctx, s.tx(r), orgOf(r).ID, name, perimeter)
		if isUniqueViolation(err) {
			errorJSON(w, http.StatusConflict,
				"Une équipe nommée « %s » existe déjà : renommez celle-ci en "+
					"l'acceptant, ou refusez la demande.", name)
			return
		}
		if err != nil {
			s.failure(w, err)
			return
		}
		password, err := insertAccount(ctx, s.tx(r), orgOf(r).ID, requesterEmail,
			requesterName, RoleLead, &teamID, me.Email)
		if isUniqueViolation(err) {
			// the check above read « no account » a moment before another
			// insert committed; same answer, not a 500
			errorJSON(w, http.StatusConflict,
				"Un compte existe déjà pour %s.", requesterEmail)
			return
		}
		if err != nil {
			s.failure(w, err)
			return
		}
		// Minted in the SAME transaction as the account: an invitation whose
		// account rolled back opens nothing, and an account whose token
		// vanished is a lead nobody wrote to. Without it, the password shown
		// once in this answer was the ONLY way in — a closed tab and the team
		// had a lead who could never sign in. The direct creation of an
		// access and the hosting approval both send one; this door did not,
		// because it was written before there was a relay to send with.
		invitationToken, err = s.mintInvitation(ctx, s.tx(r), scopeOrg(r),
			requesterEmail)
		if err != nil {
			s.failure(w, err)
			return
		}
		response["team"] = teamID
		response["name"] = name
		response["departments"] = perimeter
		response["lead"] = requesterEmail
		// returned ONCE, never stored in the clear: the coordination passes
		// it on, as it already does for every account it opens
		response["password"] = password
	}

	req := scoped(r)
	if _, err := s.tx(r).Exec(ctx,
		"UPDATE team_requests SET state="+req.p(d.Decision)+
			", reason="+req.p(strings.TrimSpace(d.Reason))+
			", decided_at="+req.p(shortTimestamp())+
			", decided_by="+req.p(me.Email)+
			" WHERE org_id=$1 AND id="+req.p(id), req.args...); err != nil {
		s.failure(w, err)
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	if invitationToken != "" {
		// The database is done with; the relay may take thirty seconds, and a
		// pool connection has no business waiting on it.
		s.release(r)
		// Sent once the account exists, and its outcome is told. The password
		// stays in the answer either way — relay down, the coordination reads
		// it out as it always has.
		sent, warning := s.sendInvitation(invitation{
			email: requesterEmail, name: requesterName, by: me.Name,
			campaign: campaignName(r), slug: campaignSlug(r),
			token: invitationToken,
		})
		response["invitation_sent"] = sent
		if warning != "" {
			response["invitation_error"] = warning
		}
	}
	// the team name is the campaign's own vocabulary, not a person; the
	// requester and the moderator are pseudonyms, like every account in
	// these logs
	s.securityEvent(r, slog.LevelInfo, "team_request_decided",
		"team", name, "decision", d.Decision,
		"requester", s.accountPseudonym(requesterEmail),
		"by", s.accountPseudonym(me.Email))
	replyJSON(w, http.StatusOK, response)
}

// insertTeam and insertAccount are shared by the direct creation
// (routes_team.go) and by the approval above, so the two doors cannot write
// different columns — the reason createCampaign exists in one copy for the
// instance's two.

func insertTeam(ctx context.Context, tx pgx.Tx, orgID int, name string,
	departments []string,
) (int, error) {
	var id int
	err := tx.QueryRow(ctx,
		"INSERT INTO teams(org_id, name, departments, created_at) "+
			"VALUES($1,$2,$3,$4) RETURNING id",
		orgID, name, strings.Join(departments, ";"), shortTimestamp()).Scan(&id)
	return id, err
}

// insertAccount opens an account and returns its password, generated here
// and hashed before it is written: the clear text exists in this answer and
// nowhere else.
func insertAccount(ctx context.Context, tx pgx.Tx, orgID int,
	email, name, role string, teamID *int, createdBy string,
) (string, error) {
	password, err := ReadablePassword()
	if err != nil {
		return "", err
	}
	hashed, err := HashPassword(password)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO accounts(org_id, email, name, password_hash, role, team_id, "+
			"created_at, created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)",
		orgID, email, name, hashed, role, teamID, shortTimestamp(),
		createdBy); err != nil {
		return "", err
	}
	return password, nil
}
