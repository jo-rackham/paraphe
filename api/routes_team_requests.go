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

	if name == "" || requester == "" || !strings.Contains(email, "@") {
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

	// An already-taken name is refused EARLY, as the hosting form refuses a
	// taken address: the requester picks another one now instead of learning
	// it after moderation.
	var taken bool
	if err := s.tx(r).QueryRow(r.Context(),
		"SELECT EXISTS(SELECT 1 FROM teams WHERE org_id=$1 AND name=$2)",
		scopeOrg(r), name).Scan(&taken); err != nil {
		s.failure(w, err)
		return
	}
	if taken {
		errorJSON(w, http.StatusConflict,
			"Une équipe nommée « %s » existe déjà dans cette campagne.", name)
		return
	}
	var pending bool
	if err := s.tx(r).QueryRow(r.Context(),
		"SELECT EXISTS(SELECT 1 FROM team_requests WHERE org_id=$1 AND name=$2 "+
			"AND state=$3)", scopeOrg(r), name, RequestPending).Scan(&pending); err != nil {
		s.failure(w, err)
		return
	}
	if pending {
		errorJSON(w, http.StatusConflict,
			"Une demande porte déjà sur l'équipe « %s » et attend une réponse.",
			name)
		return
	}
	var waiting int
	if err := s.tx(r).QueryRow(r.Context(),
		"SELECT count(*) FROM team_requests WHERE org_id=$1 AND state=$2",
		scopeOrg(r), RequestPending).Scan(&waiting); err != nil {
		s.failure(w, err)
		return
	}
	if waiting >= maxPendingTeamRequests {
		errorJSON(w, http.StatusServiceUnavailable,
			"Trop de demandes attendent la coordination de cette campagne. "+
				"Écrivez-lui : elle peut ouvrir votre équipe sans passer par "+
				"ce formulaire.")
		return
	}

	var id int64
	if err := s.tx(r).QueryRow(r.Context(),
		"INSERT INTO team_requests(org_id, name, departments, requester_email, "+
			"requester_name, message, state, ts) "+
			"VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id",
		orgOf(r).ID, name, strings.Join(departments, ";"), email, requester,
		strings.TrimSpace(d.Message), RequestPending,
		shortTimestamp()).Scan(&id); err != nil {
		// The loser of the race against the partial unique index: the check
		// above read « none pending » a moment before the other insert
		// committed. Same answer as that check gives, not a 500.
		if isUniqueViolation(err) {
			errorJSON(w, http.StatusConflict,
				"Une demande porte déjà sur l'équipe « %s » et attend une réponse.",
				name)
			return
		}
		s.failure(w, err)
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	replyJSON(w, http.StatusCreated, map[string]any{
		"id": id, "name": name,
		"message": "Demande enregistrée. La coordination de la campagne " +
			"l'examinera et vous répondra à " + email + ".",
	})
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
func (s *Server) teamRequests(r *http.Request) ([]map[string]any, error) {
	pending, err := s.rows(r, "SELECT "+teamRequestColumns+
		"WHERE org_id=$1 AND state='pending' ORDER BY id DESC LIMIT $2",
		scopeOrg(r), maxPendingTeamRequests)
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
	if d.Decision == RequestAccepted {
		if d.Name != nil {
			if name = strings.TrimSpace(*d.Name); name == "" {
				errorJSON(w, http.StatusBadRequest, "Le nom de l'équipe est requis.")
				return
			}
			if utf8.RuneCountInString(name) > maxNameRunes {
				errorJSON(w, http.StatusBadRequest,
					"Le nom de l'équipe ne doit pas dépasser 200 caractères.")
				return
			}
		}
		perimeter := splitDepartments(departments)
		if d.Departments != nil {
			corrected, unknown, err := s.knownDepartments(r, d.Departments)
			if err != nil {
				s.failure(w, err)
				return
			}
			if unknown != "" {
				errorJSON(w, http.StatusBadRequest,
					"« %s » ne correspond à aucun département de la liste.", unknown)
				return
			}
			perimeter = corrected
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
