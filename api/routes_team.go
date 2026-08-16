package main

import (
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// GET /api/team — the accounts. Coordination sees everyone; a team lead,
// only their own team.
func (s *Server) routeTeam(w http.ResponseWriter, r *http.Request) {
	c := accountOf(r)

	var accounts []map[string]any
	var err error
	if c.Coordination() {
		accounts, err = s.rows(r, "SELECT "+accountColumns+" FROM accounts c "+
			"LEFT JOIN teams g ON g.id=c.team_id AND g.org_id=c.org_id "+
			"WHERE c.org_id=$1 ORDER BY c.active DESC, g.name, c.name", scopeOrg(r))
	} else {
		accounts, err = s.rows(r, "SELECT "+accountColumns+" FROM accounts c "+
			"LEFT JOIN teams g ON g.id=c.team_id AND g.org_id=c.org_id "+
			"WHERE c.org_id=$1 AND c.team_id=$2 ORDER BY c.active DESC, c.name",
			scopeOrg(r), c.MyTeam())
	}
	if err != nil {
		s.failure(w, err)
		return
	}

	// The moderation queue rides along with the screen that answers it, rather
	// than on a route of its own: a team lead sees neither, and the
	// coordination reloads this payload after every decision anyway.
	requests := []map[string]any{}
	if c.Coordination() {
		if requests, err = s.teamRequests(r); err != nil {
			s.failure(w, err)
			return
		}
	}

	teams := []map[string]any{}
	if c.Coordination() {
		teams, err = s.rows(r,
			"SELECT g.id, g.name, COALESCE(g.departments,'') AS departments, "+
				"COALESCE(g.created_at,'') AS created_at, "+
				"(SELECT COUNT(*) FROM accounts c WHERE c.org_id=g.org_id "+
				"AND c.team_id=g.id AND c.active) AS members, "+
				"(SELECT COUNT(*) FROM assignments t WHERE t.org_id=g.org_id "+
				"AND t.team_id=g.id) AS reserved "+
				"FROM teams g WHERE g.org_id=$1 ORDER BY g.name", scopeOrg(r))
		if err != nil {
			s.failure(w, err)
			return
		}
	}

	departments, err := s.departmentLabels(r)
	if err != nil {
		s.failure(w, err)
		return
	}
	replyJSON(w, http.StatusOK, map[string]any{
		"accounts": accounts, "teams": teams, "departments": departments,
		"requests": requests})
}

type teamRequest struct {
	Name        string   `json:"name"`
	Departments []string `json:"departments"`
}

// POST /api/team/group — creating a local team (coordination only).
func (s *Server) routeCreateTeam(w http.ResponseWriter, r *http.Request) {
	var d teamRequest
	if !readBody(w, r, &d) {
		return
	}
	name := strings.TrimSpace(d.Name)
	if name == "" {
		errorJSON(w, http.StatusBadRequest, "Le nom de l'équipe est requis.")
		return
	}
	// the column is btree-indexed (teams_org_name): past ~2 690 bytes of
	// pasted text PostgreSQL refuses the index row (54000) with an
	// « erreur interne » on the lead's own screen. Runes, not bytes: the
	// message promises characters, and 200 runes stay far below the
	// index ceiling even at 4 bytes each.
	if utf8.RuneCountInString(name) > maxNameRunes {
		errorJSON(w, http.StatusBadRequest,
			"Le nom de l'équipe ne doit pas dépasser 200 caractères.")
		return
	}
	var departments []string
	for _, x := range d.Departments {
		if x = strings.TrimSpace(x); x != "" {
			departments = append(departments, x)
		}
	}
	id, err := insertTeam(r.Context(), s.tx(r), orgOf(r).ID, name, departments)
	if isUniqueViolation(err) {
		errorJSON(w, http.StatusConflict, "Une équipe nommée « %s » existe déjà.", name)
		return
	}
	if err != nil {
		s.failure(w, err)
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	replyJSON(w, http.StatusCreated, map[string]any{
		"id": id, "name": name, "departments": departments})
}

type accountRequest struct {
	Email  string `json:"email"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	TeamID *int   `json:"team_id"`
}

// POST /api/team/account — opens an account. The password is returned
// once, never stored in the clear nor put in the session.
func (s *Server) routeCreateAccount(w http.ResponseWriter, r *http.Request) {
	var d accountRequest
	if !readBody(w, r, &d) {
		return
	}
	me := accountOf(r)
	email := normalizeEmail(d.Email)
	name := strings.TrimSpace(d.Name)
	if email == "" || !strings.Contains(email, "@") || name == "" {
		errorJSON(w, http.StatusBadRequest, "Nom et adresse email sont requis.")
		return
	}
	// email is the primary key, so it is btree-indexed: past ~2 690 bytes
	// PostgreSQL refuses the index row (54000). 254 is the RFC ceiling —
	// in runes, which still keeps the index row far below its limit.
	if utf8.RuneCountInString(email) > maxEmailRunes {
		errorJSON(w, http.StatusBadRequest,
			"Cette adresse email est trop longue (254 caractères maximum).")
		return
	}

	role, team := d.Role, d.TeamID
	if me.Coordination() {
		if role == "" {
			role = RoleVolunteer
		}
		if !validRole(role) {
			errorJSON(w, http.StatusBadRequest, "Rôle inconnu : %q.", role)
			return
		}
		if team != nil {
			// the column is int4: beyond its range pgx cannot even encode
			// the argument, and coordination read « erreur interne » for a
			// team that simply does not exist
			if *team > math.MaxInt32 || *team < math.MinInt32 {
				errorJSON(w, http.StatusBadRequest,
					"Aucune équipe n'a l'identifiant %d.", *team)
				return
			}
			var exists bool
			err := s.tx(r).QueryRow(r.Context(),
				"SELECT TRUE FROM teams WHERE org_id=$1 AND id=$2",
				scopeOrg(r), *team).Scan(&exists)
			if errors.Is(err, pgx.ErrNoRows) {
				errorJSON(w, http.StatusBadRequest,
					"Aucune équipe n'a l'identifiant %d.", *team)
				return
			}
			if err != nil {
				s.failure(w, err)
				return
			}
		}
	} else {
		// a team lead only opens volunteer accounts, in their OWN team
		g := me.MyTeam()
		role, team = RoleVolunteer, &g
	}

	password, err := insertAccount(r.Context(), s.tx(r), orgOf(r).ID, email, name,
		role, team, me.Email)
	if isUniqueViolation(err) {
		errorJSON(w, http.StatusConflict, "Un compte existe déjà pour %s.", email)
		return
	}
	if err != nil {
		s.failure(w, err)
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	replyJSON(w, http.StatusCreated, map[string]any{
		"email": email, "name": name, "role": role, "password": password})
}

// POST /api/team/account/{email}/active — activates or deactivates an
// account.
func (s *Server) routeToggleAccount(w http.ResponseWriter, r *http.Request) {
	me := accountOf(r)
	// UNESCAPED: chi matches on the raw path when it differs from the decoded
	// one, so an address arrives as `someone%40example.fr`. Compared as is, it
	// matches no account and the call answers 404 while looking like it worked.
	target := normalizeEmail(pathParam(r, "email"))
	if target == me.Email {
		errorJSON(w, http.StatusBadRequest, "On ne désactive pas son propre compte.")
		return
	}

	// A team lead only searches within their OWN team: otherwise the
	// 404/403 distinction would tell them which addresses exist in other
	// teams.
	req := scoped(r)
	filter := "org_id=$1 AND email=" + req.p(target)
	if !me.Coordination() {
		filter += " AND team_id=" + req.p(me.MyTeam()) +
			" AND role=" + req.p(RoleVolunteer)
	}
	var active bool
	err := s.tx(r).QueryRow(r.Context(),
		"UPDATE accounts SET active = NOT active WHERE "+filter+" RETURNING active",
		req.args...).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		errorJSON(w, http.StatusNotFound,
			"Aucun compte %s que vous puissiez gérer.", target)
		return
	}
	if err != nil {
		s.failure(w, err)
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	s.securityEvent(r, slog.LevelInfo, "account_toggled",
		"account", s.accountPseudonym(target),
		"by", s.accountPseudonym(me.Email), "active", active)
	replyJSON(w, http.StatusOK, map[string]any{"email": target, "active": active})
}

func isUniqueViolation(err error) bool {
	var pge *pgconn.PgError
	return errors.As(err, &pge) && pge.Code == "23505"
}
