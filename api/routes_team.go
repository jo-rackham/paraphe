package main

import (
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
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
	// read like every other name a person will read: the volunteers of this
	// campaign see it on their own screen, and the public forms are checked
	// this way — one door left unchecked is how the class comes back
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
	// …and the perimeter is read like every other perimeter, which this door
	// was the only one not to do. A label no mayor bears is a team that draws
	// zero cards for ever, and nothing downstream ever says why: the request
	// form checks it, accepting a request checks it, and coordination's own
	// door — the one most used — did not. The comment above says a door left
	// unchecked is how the class comes back; it came back one field along.
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

// POST /api/team/group/{id} — coordination corrects a team's name or its
// perimeter.
//
// Both were frozen at creation, and neither is a decision that stays right: a
// team accepted under the name its requester typed, a perimeter drawn before
// a neighbouring department joined, a typo read by every volunteer of the
// campaign on their own screen. The only way round was to open a second team
// and move its people by hand — which loses the name, and leaves the first
// one in every list.
//
// SAME RULES AS CREATION, and they are the rules for a reason: a name no
// human can read reaches every screen of the campaign, and a perimeter of
// labels no mayor bears is a team that draws zero cards for ever with nothing
// downstream saying why. Written through the same helpers so the two doors
// cannot drift apart — this is the third door onto a team's perimeter, and
// the comment above records what happened when the second one skipped it.
//
// DELETION IS NOT HERE, deliberately. A team is named by the accounts that
// belong to it, by the assignments it reserved and by every status its
// members wrote — « enregistré par l'équipe Nord », which is the one
// attribution a status carries across a campaign. Dropping the row would
// leave those pointing at nothing, and the lever a campaign already has is
// the right one: deactivate the accesses, which is immediate and gives the
// untouched cards back to the pool.
func (s *Server) routeUpdateTeam(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(pathParam(r, "id"))
	if err != nil || id <= 0 {
		// the same answer a team of another campaign gets: an identifier that
		// is not a number names no team here either
		errorJSON(w, http.StatusNotFound, "Cette équipe n'existe pas.")
		return
	}
	var d teamRequest
	if !readBody(w, r, &d) {
		return
	}
	name := strings.TrimSpace(d.Name)
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
	if utf8.RuneCountInString(name) > maxNameRunes {
		errorJSON(w, http.StatusBadRequest,
			"Le nom de l'équipe ne doit pas dépasser 200 caractères.")
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
	req := scoped(r)
	tag, err := s.tx(r).Exec(r.Context(),
		"UPDATE teams SET name="+req.p(name)+", departments="+
			req.p(departments)+" WHERE org_id=$1 AND id="+req.p(id),
		req.args...)
	if isUniqueViolation(err) {
		errorJSON(w, http.StatusConflict,
			"Une équipe nommée « %s » existe déjà.", name)
		return
	}
	if err != nil {
		s.failure(w, err)
		return
	}
	// A team of another campaign, or one that no longer exists: the same
	// answer, because telling them apart would say whether the identifier
	// names a team somewhere else.
	if tag.RowsAffected() == 0 {
		errorJSON(w, http.StatusNotFound, "Cette équipe n'existe pas.")
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	replyJSON(w, http.StatusOK, map[string]any{
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
	if email == "" || !storableEmail(email) || !visible(name) {
		errorJSON(w, http.StatusBadRequest, "Nom et adresse email sont requis.")
		return
	}
	// the name goes on the access list every lead and coordinator reads
	if !legible(name) || !legible(email) {
		errorJSON(w, http.StatusBadRequest,
			"Le nom et l'adresse ne doivent contenir ni retour à la ligne ni "+
				"caractère invisible.")
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
	// The name is bounded like every other name this application stores — a
	// team's, a campaign's, a requester's. This route was the one that was
	// not, and it is the one that WRITES A PERSON: 128 KiB of body per
	// request against a ceiling of 120 writes a minute puts megabytes into an
	// unindexed column, and no other ceiling stands between.
	if utf8.RuneCountInString(name) > maxNameRunes {
		errorJSON(w, http.StatusBadRequest,
			"Le nom ne doit pas dépasser 200 caractères.")
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
		if role == RoleCoordination {
			// the rule routeChangeRole already applies, and the two doors
			// disagreed: coordination sees the whole campaign, so a team on
			// the account only pretends to bound it — and the lead of that
			// team then reads the coordinator as one of their own members.
			team = nil
		}
		if role == RoleLead && team == nil {
			errorJSON(w, http.StatusBadRequest, leadNeedsATeam)
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
	// Minted in the SAME transaction as the account: an invitation whose
	// account rolled back opens nothing, and an account whose token vanished
	// is a volunteer nobody wrote to.
	token, err := s.mintInvitation(r.Context(), s.tx(r), scopeOrg(r), email)
	if err != nil {
		s.failure(w, err)
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	// The database is done with; the relay may take thirty seconds, and a
	// pool connection has no business waiting on it.
	s.release(r)
	// Sent once the account exists, and its outcome is told: the caller is
	// authenticated and created this account, so there is no existence left
	// to protect here. The password stays in the answer either way — relay
	// down, the lead reads it out as they always have.
	sent, warning := s.sendInvitation(invitation{
		email: email, name: name, by: me.Name, campaign: campaignName(r),
		slug: campaignSlug(r), token: token,
	})
	reply := map[string]any{
		"email": email, "name": name, "role": role, "password": password,
		"invitation_sent": sent,
	}
	if warning != "" {
		reply["invitation_error"] = warning
	}
	replyJSON(w, http.StatusCreated, reply)
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

	// Deactivating the last active coordinator locks the campaign out just as
	// a self-demotion would, and this route had no guard: its blind UPDATE
	// raced routeChangeRole's FOR UPDATE and both won. The same lock closes
	// it — a lead never reaches a coordinator (the filter below refuses it),
	// so only a coordinator's call needs it.
	if me.Coordination() {
		sole, err := s.soleActiveCoordinator(r, target)
		if err != nil {
			s.failure(w, err)
			return
		}
		if sole {
			errorJSON(w, http.StatusConflict, lastCoordinatorMessage)
			return
		}
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
	// A CARD NOBODY WILL WORK GOES BACK IN THE POOL.
	//
	// `/api/batch` draws where `assignments.volunteer IS NULL`, and nothing
	// cleared it when an access closed: every card the departing volunteer had
	// been handed and not yet touched stayed reserved to an account that can no
	// longer sign in. It came up in no other batch, for anybody, for ever — a
	// batch of ten per departure, silently out of circulation in a campaign
	// that needs five hundred signatures. The remedy documented until now was
	// an UPDATE typed against production.
	//
	// ONLY the untouched ones — `status = 'to_contact'`, which is exactly what
	// `mayorAvailable` calls free. A card they emailed, called or got a refusal
	// on keeps its status, its notes and the team that wrote them: the work is
	// not theirs to lose by leaving, and the next volunteer reads it. That is
	// also why `team_id` goes with `volunteer`: what is released is released to
	// the campaign, and the notes stay behind with the team that took them.
	//
	// Deactivation only. Reactivating gives an account back its access, not the
	// cards somebody else may have worked in between.
	if !active {
		released := scoped(r)
		if _, err := s.tx(r).Exec(r.Context(),
			"UPDATE assignments SET volunteer=NULL, team_id=NULL "+
				"WHERE org_id=$1 AND volunteer="+released.p(target)+
				" AND status='to_contact'", released.args...); err != nil {
			s.failure(w, err)
			return
		}
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

// POST /api/team/account/{email}/password — draws a new one-time password
// for somebody who has lost theirs.
//
// The sign-in screen has promised this all along — « le mot de passe n'est
// affiché qu'une fois à sa création : s'il est perdu, il faut en regénérer
// un » — and no route did it: opening an access again answers 409, and on an
// instance with no relay there was no other door. A campaign whose volunteer
// lost their password had to have the account deactivated and re-created
// under another address.
//
// WHO may do it is the filter routeToggleAccount already draws, and it is
// the same filter for the same reason: a lead reaches the volunteers of
// THEIR team and nobody else. Without it a lead would mint a password for a
// coordinator and take the campaign — and the 404/403 distinction would tell
// them which addresses exist in the other teams.
func (s *Server) routeResetPassword(w http.ResponseWriter, r *http.Request) {
	me := accountOf(r)
	// UNESCAPED, like its sibling: chi matches on the raw path, so an address
	// arrives as `someone%40example.fr` and matches no account.
	target := normalizeEmail(pathParam(r, "email"))
	if target == me.Email {
		// Not a refusal of the ACT — everyone may change their own password —
		// but of this door: it would show a drawn password on screen instead
		// of taking one they chose, and sign this very session out.
		errorJSON(w, http.StatusBadRequest,
			"Pour votre propre mot de passe, passez par « Mon profil ».")
		return
	}

	password, err := ReadablePassword()
	if err != nil {
		s.failure(w, err)
		return
	}
	hashed, err := HashPassword(password)
	if err != nil {
		s.failure(w, err)
		return
	}
	// The same instant the session guard compares against, from the same
	// clock: a reset is a password change like any other, so the sessions
	// opened under the old one fall. That is the point rather than a side
	// effect — a password is regenerated precisely when nobody knows who
	// still holds the previous one.
	changedAt := s.now().Truncate(time.Second)

	req := scoped(r)
	filter := "org_id=$1 AND email=" + req.p(target)
	if !me.Coordination() {
		filter += " AND team_id=" + req.p(me.MyTeam()) +
			" AND role=" + req.p(RoleVolunteer)
	}
	var name string
	err = s.tx(r).QueryRow(r.Context(),
		"UPDATE accounts SET password_hash="+req.p(hashed)+
			", password_changed_at="+req.p(changedAt)+
			" WHERE "+filter+" RETURNING name", req.args...).Scan(&name)
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
	s.securityEvent(r, slog.LevelInfo, "password_reset",
		"account", s.accountPseudonym(target),
		"by", s.accountPseudonym(me.Email))
	// Shown once and stored nowhere in the clear, exactly like the password
	// an account is opened with. No invitation is sent: this is the door for
	// an instance whose relay is down or absent, and the lead reads it out.
	replyJSON(w, http.StatusOK, map[string]any{
		"email": target, "name": name, "password": password,
	})
}

const lastCoordinatorMessage = "Impossible : ce compte est le dernier accès " +
	"de coordination actif de la campagne."

// A lead without a team is a contradiction the code cannot hold: the role IS
// « opens volunteer access for THEIR team », and MyTeam() answers
// NationalTeam — zero — for an account carrying none. Every lead-side filter
// then reads `team_id=0`, and TWO such leads see and deactivate each other's
// volunteers, having created them under that same zero. Refused where the
// role is set, both doors, rather than papered over in the filters.
const leadNeedsATeam = "Un référent conduit une équipe : choisissez-en une. " +
	"Sans équipe, il partagerait ses bénévoles avec tous les autres référents " +
	"sans équipe."

// soleActiveCoordinator locks the campaign's active coordinators and reports
// whether `target` is one of them AND the only one. Both routes that can
// take a coordinator out of the active set — a self-demotion
// (routeChangeRole) and a deactivation (routeToggleAccount) — call this and
// so lock the SAME rows: they serialise against each other, and the second
// to arrive re-evaluates on the committed state and is refused. Without the
// shared lock, one route's FOR UPDATE guards nothing against the other's
// blind UPDATE, and a campaign races itself down to zero coordinators — a
// state no route can leave, since validRole refuses to mint one.
func (s *Server) soleActiveCoordinator(r *http.Request, target string) (bool, error) {
	rows, err := s.tx(r).Query(r.Context(),
		"SELECT email FROM accounts WHERE org_id=$1 AND role=$2 AND active "+
			"FOR UPDATE", scopeOrg(r), RoleCoordination)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var targetIsCoord bool
	others := 0
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return false, err
		}
		if e == target {
			targetIsCoord = true
		} else {
			others++
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return targetIsCoord && others == 0, nil
}

// POST /api/team/account/{email}/role — moves an existing account to
// another campaign role. Coordination only, and the door multi-coordination
// walks through: promoting a trusted account is how a campaign stops
// depending on a single person.
func (s *Server) routeChangeRole(w http.ResponseWriter, r *http.Request) {
	var d struct {
		Role   string `json:"role"`
		TeamID *int   `json:"team_id"`
	}
	if !readBody(w, r, &d) {
		return
	}
	me := accountOf(r)
	// unescaped, like the toggle above: chi matches the raw path
	target := normalizeEmail(pathParam(r, "email"))
	// validRole knows only the campaign roles: that is what keeps this door
	// from minting an instance administrator.
	if !validRole(d.Role) {
		errorJSON(w, http.StatusBadRequest, "Rôle inconnu : %q.", d.Role)
		return
	}
	team := d.TeamID
	if d.Role == RoleCoordination {
		// coordination sees the whole campaign: a team on the account would
		// only pretend to bound it
		team = nil
	} else if d.Role == RoleLead && team == nil {
		errorJSON(w, http.StatusBadRequest, leadNeedsATeam)
		return
	} else if team != nil {
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

	if d.Role != RoleCoordination {
		// The LAST active coordination access never leaves the role: nobody
		// could open, close or promote anything afterwards, and only whoever
		// operates the deployment could repair it (the bootstrap variables).
		sole, err := s.soleActiveCoordinator(r, target)
		if err != nil {
			s.failure(w, err)
			return
		}
		if sole {
			errorJSON(w, http.StatusConflict, lastCoordinatorMessage)
			return
		}
	}

	req := scoped(r)
	var role string
	err := s.tx(r).QueryRow(r.Context(),
		"UPDATE accounts SET role="+req.p(d.Role)+", team_id="+req.p(team)+
			" WHERE org_id=$1 AND email="+req.p(target)+" RETURNING role",
		req.args...).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		errorJSON(w, http.StatusNotFound, "Aucun compte %s.", target)
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
	s.securityEvent(r, slog.LevelInfo, "account_role_changed",
		"account", s.accountPseudonym(target),
		"by", s.accountPseudonym(me.Email), "role", role)
	replyJSON(w, http.StatusOK, map[string]any{
		"email": target, "role": role, "team_id": team,
	})
}

func isUniqueViolation(err error) bool {
	var pge *pgconn.PgError
	return errors.As(err, &pge) && pge.Code == "23505"
}
