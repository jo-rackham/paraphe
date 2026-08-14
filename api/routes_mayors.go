package main

import (
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const mayorsPerPage = 50

// Bound on allocation rounds: enough to absorb a rush of volunteers on the
// best-scored mayors, few enough that a request never drags on.
const maxBatchRounds = 8

// `mayors` is the public list, shared by every campaign and read-only;
// `assignments` carries what a campaign did with it. The join is therefore
// OUTER: a mayor my campaign did nothing with has no work row, and must
// appear as available.
//
// Another campaign's `assignments` and `accounts` rows are invisible (RLS): the
// outer join naturally renders them "free" here, which is exactly the
// intended meaning — other campaigns' work does not exist for this one.
//
// The `volunteer` column carries an ADDRESS (the account's unique
// identifier), not a name: two namesake volunteers on the same team would
// otherwise share ownership of a card, and duplicate contact is precisely
// what the application exists to prevent. The readable name comes from the
// join.
const (
	mayorSelection = "m.*, t.volunteer, COALESCE(t.status,'to_contact') AS status, " +
		"t.updated_at, t.team_id, c.name AS volunteer_name"
	// mayors is the common, read-only list: it carries no org_id. The work
	// rows do, and the campaign is named in the JOIN CONDITION, never in a
	// WHERE: `WHERE t.org_id = …` would turn these outer joins into inner
	// ones and drop every mayor nobody has taken yet — that is, exactly the
	// ones `mayorAvailable` exists to find.
	assignmentJoinFmt = " FROM mayors m " +
		"LEFT JOIN assignments t ON t.insee_code = m.insee_code AND t.org_id = %[1]s " +
		"LEFT JOIN accounts c ON c.email = t.volunteer AND c.org_id = %[1]s"
	// Available: no work row, or a row nobody took and on which nothing was
	// done.
	mayorAvailable = "(t.insee_code IS NULL OR " +
		"(t.volunteer IS NULL AND t.status = 'to_contact'))"
)

// assignmentJoin binds the join to one campaign. It takes the placeholder
// rather than the value so that callers numbering their parameters as they
// go keep doing so — and so that no caller can use the join without having
// said which campaign it is about.
func assignmentJoin(orgPlaceholder string) string {
	return fmt.Sprintf(assignmentJoinFmt, orgPlaceholder)
}

// GET /api/tableau — the home screen: where the campaign stands, where I
// stand.
func (s *Server) routeDashboard(w http.ResponseWriter, r *http.Request) {
	c := accountOf(r)

	// whole-campaign stats: volumes only, no names — everyone sees where
	// the collective effort stands without seeing other people's work
	stats := map[string]int{}
	for _, st := range Statuses {
		stats[st.Key] = 0
	}
	byStatus, err := s.counters(r, "SELECT COALESCE(t.status,'to_contact'), "+
		"COUNT(*)"+assignmentJoin("$1")+" GROUP BY 1", scopeOrg(r))
	if err != nil {
		s.failure(w, err)
		return
	}
	total := 0
	for status, n := range byStatus {
		if _, known := stats[status]; known {
			stats[status] = n
		}
		total += n
	}

	promisedDepts, err := s.orderedCounters(r,
		"SELECT m.department, COUNT(*) FROM assignments t "+
			"JOIN mayors m ON m.insee_code = t.insee_code "+
			"WHERE t.org_id=$1 AND t.status IN ('promised','signed') "+
			"GROUP BY m.department ORDER BY COUNT(*) DESC, m.department",
		scopeOrg(r))
	if err != nil {
		s.failure(w, err)
		return
	}

	mine, err := s.rows(r, "SELECT "+mayorSelection+assignmentJoin("$1")+
		" WHERE t.volunteer=$2 AND t.team_id IS NOT DISTINCT FROM $3 "+
		"ORDER BY CASE t.status WHEN 'to_call_back' THEN 0 WHEN 'to_contact' "+
		"THEN 1 ELSE 2 END, COALESCE(NULLIF(m.score,'')::int, 0) DESC",
		scopeOrg(r), c.Email, c.MyTeam())
	if err != nil {
		s.failure(w, err)
		return
	}

	// my team's work (minus mine), to split the effort without duplicates
	team := []map[string]any{}
	if c.MyTeam() != NationalTeam {
		team, err = s.rows(r,
			"SELECT COALESCE(c.name, t.volunteer) AS who, COUNT(*) AS n, "+
				"COUNT(*) FILTER (WHERE t.status <> 'to_contact') AS done "+
				"FROM assignments t LEFT JOIN accounts c "+
				"ON c.email = t.volunteer AND c.org_id = t.org_id "+
				"WHERE t.org_id=$1 AND t.team_id IS NOT NULL AND t.team_id=$2 "+
				"AND t.volunteer IS NOT NULL GROUP BY who ORDER BY n DESC",
			scopeOrg(r), c.MyTeam())
		if err != nil {
			s.failure(w, err)
			return
		}
	}

	myDepts, err := s.teamDepartments(r, c)
	if err != nil {
		s.failure(w, err)
		return
	}
	available, err := s.column(r, "SELECT DISTINCT m.department"+
		assignmentJoin("$1")+" WHERE "+mayorAvailable+" ORDER BY m.department",
		scopeOrg(r))
	if err != nil {
		s.failure(w, err)
		return
	}
	departments := []string{}
	for _, d := range available {
		if len(myDepts) == 0 || contains(myDepts, d) {
			departments = append(departments, d)
		}
	}

	byRank, err := s.counters(r, "SELECT rank, COUNT(*) FROM mayors GROUP BY rank")
	if err != nil {
		s.failure(w, err)
		return
	}

	replyJSON(w, http.StatusOK, map[string]any{
		"stats": stats, "total": total, "departments_with_promise": promisedDepts,
		"departments_covered": len(promisedDepts), "mine": mine, "team": team,
		"departments": departments, "by_rank": byRank, "batch_size": orgOf(r).BatchSize,
	})
}

// GET /api/facettes — departments and headcounts per rank. Separate from
// the list: these are two full table scans, useless on every page of the
// infinite scroll.
func (s *Server) routeFacets(w http.ResponseWriter, r *http.Request) {
	departments, err := s.column(r,
		"SELECT DISTINCT department FROM mayors ORDER BY department")
	if err != nil {
		s.failure(w, err)
		return
	}
	byRank, err := s.counters(r, "SELECT rank, COUNT(*) FROM mayors GROUP BY rank")
	if err != nil {
		s.failure(w, err)
		return
	}
	replyJSON(w, http.StatusOK, map[string]any{
		"departments": departments, "by_rank": byRank})
}

// GET /api/mayors — filtered, paginated list.
func (s *Server) routeMayors(w http.ResponseWriter, r *http.Request) {
	c := accountOf(r)
	v := r.URL.Query()
	// unstorable text (NUL, malformed UTF-8) is refused at the middleware:
	// nothing to strip here anymore
	q := strings.TrimSpace(v.Get("q"))

	// Keyset, not OFFSET. The team modifies the very set being paged: a card
	// entering or leaving the filter between two pages shifted every
	// following offset by one, so a mayor was SKIPPED — and in a campaign
	// whose object is coverage, a skipped mayor is one nobody ever contacts.
	// The cursor carries the last row of the previous page.
	after, err := decodeCursor(v.Get("after"))
	if err != nil {
		errorJSON(w, http.StatusBadRequest, "Curseur de pagination illisible.")
		return
	}

	req := scoped(r)
	where := []string{"1=1"}
	if q != "" {
		pattern := req.p("%" + q + "%")
		where = append(where, fmt.Sprintf(
			"(m.commune ILIKE %s OR m.last_name ILIKE %s OR m.first_name ILIKE %s)",
			pattern, pattern, pattern))
	}
	if status := v.Get("status"); status != "" {
		if !validStatus(status) {
			errorJSON(w, http.StatusBadRequest, "Statut inconnu : %q.", status)
			return
		}
		where = append(where, "COALESCE(t.status,'to_contact')="+req.p(status))
	}
	if dept := v.Get("department"); dept != "" {
		where = append(where, "m.department="+req.p(dept))
	}
	if v.Get("democracy") != "" {
		where = append(where, "m.democratic_theme_endorsement='oui'")
	}
	// default filter: the known endorsers. Widening it is explicit and
	// displayed, never suffered.
	rank := v.Get("rank")
	if rank == "" {
		rank = "has_endorsed"
	}
	// Refused, not ignored: silently dropping the filter widened the pool
	// without a word, where an unknown status already answers 400.
	if !validRank(rank) {
		errorJSON(w, http.StatusBadRequest, "Vivier inconnu : %q.", rank)
		return
	}
	where = append(where, "m.rank="+req.p(rank))
	where = append(where, teamScope(c, req))
	filter := strings.Join(where, " AND ")

	// One placeholder for both queries below: they share `req`, so binding
	// the campaign twice left the first binding referenced by no SQL at all,
	// and PostgreSQL cannot type a parameter a query never mentions.
	join := assignmentJoin("$1")

	var total int
	if err := s.tx(r).QueryRow(r.Context(),
		"SELECT COUNT(*)"+join+" WHERE "+filter,
		req.args...).Scan(&total); err != nil {
		s.failure(w, err)
		return
	}

	// The sort must be a TOTAL order over columns the team cannot change:
	// score, department and commune alone leave ties, and PostgreSQL is
	// free to order those differently between two queries — which shows up
	// as a duplicate or, worse, a hole. The INSEE code settles every tie.
	// Ascending on a negated score so a single row comparison expresses the
	// whole cursor.
	if after != nil {
		filter += fmt.Sprintf(
			" AND (-COALESCE(NULLIF(m.score,'')::int, 0), m.department, "+
				"m.commune, m.insee_code) > (%s,%s,%s,%s)",
			req.p(after.Score), req.p(after.Department), req.p(after.Commune),
			req.p(after.Insee))
	}
	rows, err := s.rows(r, fmt.Sprintf(
		"SELECT %s%s WHERE %s ORDER BY "+
			"-COALESCE(NULLIF(m.score,'')::int, 0), m.department, "+
			"m.commune, m.insee_code LIMIT %s",
		mayorSelection, join, filter, req.p(mayorsPerPage)), req.args...)
	if err != nil {
		s.failure(w, err)
		return
	}

	var next *string
	if len(rows) == mayorsPerPage {
		last := rows[len(rows)-1]
		token := encodeCursor(text(last["score"]), text(last["department"]),
			text(last["commune"]), text(last["insee_code"]))
		next = &token
	}
	replyJSON(w, http.StatusOK, map[string]any{
		"total": total, "rows": rows, "next": next, "rank": rank})
}

// GET /api/mayors/{insee} — the card, with the team's shared history.
func (s *Server) routeCard(w http.ResponseWriter, r *http.Request) {
	insee := r.PathValue("insee")
	card, ok := s.cardAndNotes(w, r, insee)
	if !ok {
		return
	}
	replyJSON(w, http.StatusOK, card)
}

// cardAndNotes returns the card and its history, or answers the error
// itself.
func (s *Server) cardAndNotes(w http.ResponseWriter, r *http.Request,
	insee string) (map[string]any, bool) {
	m, ok := s.loadMayor(w, r, insee)
	if !ok {
		return nil, false
	}
	// Notes carry their own team_id, and IT is authoritative: trusting
	// the card's current owner would leak a team's nominative notes as soon
	// as a card returns to the shared pool — which the orphan-owner
	// remediation does precisely.
	req := scoped(r)
	filter := "n.insee_code=" + req.p(insee)
	if !accountOf(r).Coordination() {
		filter += fmt.Sprintf(" AND (n.team_id IS NULL OR n.team_id=%s)",
			req.p(accountOf(r).MyTeam()))
	}
	// LIMIT: this history is re-read on EVERY status write, so an unbounded
	// one is paid again at each POST — 800 long notes answered a 96 MB body
	// and took the server's heap from 1 to 320 MB for a single request.
	// Nobody has contacted one mayor 200 times.
	notes, err := s.rows(r,
		"SELECT COALESCE(c.name, n.volunteer) AS volunteer, n.status, n.note, n.ts "+
			"FROM notes n LEFT JOIN accounts c "+
			"ON c.email = n.volunteer AND c.org_id = n.org_id "+
			"WHERE n.org_id=$1 AND "+filter+
			" ORDER BY n.id DESC LIMIT 200", req.args...)
	if err != nil {
		s.failure(w, err)
		return nil, false
	}
	return map[string]any{"mayor": m, "notes": notes}, true
}

type statusRequest struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

// POST /api/mayors/{insee}/status — setting a status claims the card.
func (s *Server) routeStatus(w http.ResponseWriter, r *http.Request) {
	insee := r.PathValue("insee")
	m, ok := s.loadMayor(w, r, insee)
	if !ok {
		return
	}
	c := accountOf(r)
	// Setting a status on a FREE card claims it: the geographic scope must
	// apply here as it applies to /lot. Without this check, a team annexes
	// another department's cards one by one, and the legitimate team gets
	// turned away (403) from an elected official of their own.
	// A card ALREADY reserved by the team is not concerned: a scope
	// narrowed after the fact must not lock up work in progress.
	if _, reserved := integer(m["team_id"]); !reserved {
		myDepts, err := s.teamDepartments(r, c)
		if err != nil {
			s.failure(w, err)
			return
		}
		dept := text(m["department"])
		if len(myDepts) > 0 && !contains(myDepts, dept) {
			errorJSON(w, http.StatusForbidden,
				"%s n'est pas dans le périmètre de votre équipe.", dept)
			return
		}
	}
	var d statusRequest
	if !readBody(w, r, &d) {
		return
	}
	if !validStatus(d.Status) {
		errorJSON(w, http.StatusBadRequest, "Statut inconnu : %q.", d.Status)
		return
	}
	// One row per write, never deleted, and re-read on every write: this is
	// the most frequent write in the app and the only unbounded one left
	// behind authentication. 300 posts held 386 MB of heap. What a call is
	// worth noting fits in 5 000 characters — the same ceiling as the
	// public form's message.
	if utf8.RuneCountInString(d.Note) > maxNoteRunes {
		errorJSON(w, http.StatusBadRequest,
			"Une note ne doit pas dépasser %d caractères.", maxNoteRunes)
		return
	}
	ctx := r.Context()

	// Without team_id, the card would stay in the shared pool and
	// another team could read its notes and overwrite its status. The team
	// wall is not enough: two volunteers of the SAME team can aim at the
	// same card, and duplicate contact is what is prevented here.
	tag, err := s.tx(r).Exec(ctx,
		"INSERT INTO assignments(org_id, insee_code, team_id, volunteer, status, updated_at) "+
			"VALUES($1,$2,$3,$4,$5,$6) "+
			"ON CONFLICT (org_id, insee_code) DO UPDATE SET "+
			"status=excluded.status, updated_at=excluded.updated_at, "+
			"volunteer=COALESCE(assignments.volunteer, excluded.volunteer), "+
			"team_id=COALESCE(assignments.team_id, excluded.team_id) "+
			"WHERE assignments.volunteer IS NULL OR assignments.volunteer=excluded.volunteer",
		orgOf(r).ID, insee, c.MyTeam(), c.Email, d.Status, shortTimestamp())
	if err != nil {
		s.failure(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		errorJSON(w, http.StatusConflict,
			"Ce maire vient d'être attribué à quelqu'un d'autre de votre "+
				"équipe. Rafraîchissez la fiche avant d'écrire : deux personnes "+
				"ne doivent pas contacter le même élu.")
		return
	}
	if _, err := s.tx(r).Exec(ctx,
		"INSERT INTO notes(org_id, insee_code, volunteer, status, note, ts, team_id) "+
			"VALUES($1,$2,$3,$4,$5,$6,$7)",
		orgOf(r).ID, insee, c.Email, d.Status, strings.TrimSpace(d.Note),
		shortTimestamp(), c.MyTeam()); err != nil {
		s.failure(w, err)
		return
	}
	// the card is re-read INSIDE the transaction, before its commit: the
	// answer then describes exactly what was recorded, and a failing commit
	// can still be reported — after answering 200, it no longer could.
	card, ok := s.cardAndNotes(w, r, insee)
	if !ok {
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	replyJSON(w, http.StatusOK, card)
}

type batchRequest struct {
	Department string `json:"department"`
	Rank       string `json:"rank"`
	Democracy  bool   `json:"democracy"`
}

// POST /api/batch — reserves a batch of mayors for oneself.
func (s *Server) routeBatch(w http.ResponseWriter, r *http.Request) {
	var d batchRequest
	if !readBody(w, r, &d) {
		return
	}
	c := accountOf(r)
	myDepts, err := s.teamDepartments(r, c)
	if err != nil {
		s.failure(w, err)
		return
	}
	// a local team only draws from its geographic scope
	if d.Department != "" && len(myDepts) > 0 && !contains(myDepts, d.Department) {
		errorJSON(w, http.StatusForbidden,
			"%s n'est pas dans le périmètre de votre équipe.", d.Department)
		return
	}
	departments := myDepts
	if d.Department != "" {
		departments = []string{d.Department}
	}

	rank := d.Rank
	if rank == "" {
		rank = "has_endorsed"
	}
	// Refused here as on the listing route, and it matters more here: a
	// pool silently dropped means the volunteer believes they are drawing
	// from the priority list and is handed the whole file.
	if !validRank(rank) {
		errorJSON(w, http.StatusBadRequest, "Vivier inconnu : %q.", rank)
		return
	}
	// The same criteria feed two queries with different parameters:
	// PostgreSQL refuses being handed more parameters than the statement
	// references, so each numbers its own.
	criteria := func(q *query) []string {
		f := []string{mayorAvailable}
		if len(departments) > 0 {
			f = append(f, "m.department = ANY("+q.p(departments)+")")
		}
		if d.Democracy {
			f = append(f, "m.democratic_theme_endorsement='oui'")
		}
		// refused above, so it always applies here
		f = append(f, "m.rank="+q.p(rank))
		return f
	}

	req := &query{}
	org, team, me := req.p(orgOf(r).ID), req.p(c.MyTeam()), req.p(c.Email)
	filters := criteria(req)
	remaining := req.p(0) // replaced every round: the batch's balance

	availReq := scoped(r)
	availSQL := fmt.Sprintf("SELECT EXISTS(SELECT 1%s WHERE %s)",
		assignmentJoin("$1"),
		strings.Join(criteria(availReq), " AND "))

	// The allocation is the INSERT itself, not a read followed by a write:
	// two volunteers clicking at the same moment on different instances aim
	// at the same key (org, insee), and PostgreSQL lets only one through.
	// The other falls onto DO UPDATE … WHERE, whose condition is now false:
	// their row is not counted, and they are not told they own a mayor who
	// is not theirs.
	sql := fmt.Sprintf(`
		INSERT INTO assignments(org_id, insee_code, team_id, volunteer, status)
		SELECT %s, m.insee_code, %s, %s, 'to_contact'%s
		WHERE %s
		ORDER BY COALESCE(NULLIF(m.score,'')::int, 0) DESC, m.insee_code
		LIMIT %s
		ON CONFLICT (org_id, insee_code) DO UPDATE
		  SET volunteer=excluded.volunteer, team_id=excluded.team_id
		  WHERE assignments.volunteer IS NULL AND assignments.status='to_contact'`,
		org, team, me, assignmentJoin(org), strings.Join(filters, " AND "), remaining)

	// Volunteers all aim at the best-scored: on a simultaneous click, seven
	// losers out of eight would walk away empty-handed with "the pool is
	// exhausted", while hundreds of cards remain. So we replay until the
	// batch is full. Each statement takes a fresh snapshot (READ
	// COMMITTED): the next round sees the cards the winners just took, and
	// falls back on the following ones.
	//
	// A round that allocates nothing does NOT prove the pool is empty: it
	// may have seen, in its snapshot, only cards taken in the meantime. The
	// question is then asked explicitly — otherwise the volunteer reads
	// "the pool is exhausted" in front of a full pool.
	taken := 0
	exhausted := false
	for round := 0; round < maxBatchRounds && taken < orgOf(r).BatchSize; round++ {
		req.args[len(req.args)-1] = orgOf(r).BatchSize - taken
		tag, err := s.tx(r).Exec(r.Context(), sql, req.args...)
		if err != nil {
			s.failure(w, err)
			return
		}
		n := int(tag.RowsAffected())
		taken += n
		if n > 0 {
			continue
		}
		var left bool
		if err := s.tx(r).QueryRow(r.Context(), availSQL,
			availReq.args...).Scan(&left); err != nil {
			s.failure(w, err)
			return
		}
		if !left {
			exhausted = true
			break
		}
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	// otherwise the volunteer clicks, nothing moves, and they believe it
	// worked
	message := ""
	switch {
	case taken == 0 && exhausted:
		message = "Aucun maire disponible avec ces critères — le vivier est " +
			"épuisé dans votre périmètre. Essayez un autre vivier ou un autre " +
			"département."
	case taken == 0:
		message = "Plusieurs bénévoles servent le même vivier en ce moment et " +
			"les fiches sont parties entre-temps. Réessayez : il en reste."
	}
	replyJSON(w, http.StatusOK, map[string]any{"taken": taken, "message": message})
}

// GET /api/export.csv — the export follows the same wall as the screen:
// otherwise downloading it would suffice to read who, in the other teams,
// contacted whom.
func (s *Server) routeExport(w http.ResponseWriter, r *http.Request) {
	req := scoped(r)
	filter := teamScope(accountOf(r), req)
	cols := append(append([]string{}, Cols...), "volunteer", "status", "updated_at")
	selection := make([]string, 0, len(cols)+1)
	for _, c := range Cols {
		selection = append(selection, "m."+c)
	}
	selection = append(selection, "t.volunteer",
		"COALESCE(t.status,'to_contact')", "t.updated_at", "COALESCE(c.name, t.volunteer)")

	rows, err := s.tx(r).Query(r.Context(), fmt.Sprintf(
		"SELECT %s%s WHERE %s ORDER BY m.department, m.commune",
		strings.Join(selection, ","), assignmentJoin("$1"), filter),
		req.args...)
	if err != nil {
		s.failure(w, err)
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=suivi_maires.csv")
	// BOM: Excel and LibreOffice otherwise open UTF-8 as latin-1
	if _, err := w.Write([]byte("\uFEFF")); err != nil {
		return
	}
	writer := csv.NewWriter(w)
	writer.Comma = ';'
	if err := writer.Write(append(append([]string{}, cols...), "volunteer_name")); err != nil {
		truncatedExport(err)
		return
	}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			truncatedExport(err)
			return
		}
		row := make([]string, len(values))
		for i, v := range values {
			row[i] = text(v)
		}
		if err := writer.Write(row); err != nil {
			truncatedExport(err)
			return
		}
	}
	if err := rows.Err(); err != nil {
		truncatedExport(err)
		return
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		truncatedExport(err)
	}
}

// The HTTP header is already gone: no error can reach the client. The file
// will arrive truncated — the operator must be able to know.
func truncatedExport(err error) {
	log.Printf("truncated CSV export: %v", err)
}

// loadMayor applies the team wall: a card reserved elsewhere is refused,
// not merely hidden.
func (s *Server) loadMayor(w http.ResponseWriter, r *http.Request,
	insee string) (map[string]any, bool) {
	rows, err := s.tx(r).Query(r.Context(),
		"SELECT "+mayorSelection+assignmentJoin("$1")+" WHERE m.insee_code=$2",
		scopeOrg(r), insee)
	if err != nil {
		s.failure(w, err)
		return nil, false
	}
	m, err := pgx.CollectOneRow(rows, pgx.RowToMap)
	if errors.Is(err, pgx.ErrNoRows) {
		errorJSON(w, http.StatusNotFound, "Aucun maire pour le code INSEE %q.", insee)
		return nil, false
	}
	if err != nil {
		s.failure(w, err)
		return nil, false
	}
	c := accountOf(r)
	// 0 = the national team: a plain truthiness test would treat it as
	// unreserved
	if owner, reserved := integer(m["team_id"]); reserved && !c.Coordination() &&
		owner != c.MyTeam() {
		errorJSON(w, http.StatusForbidden,
			"Cette fiche est réservée par une autre équipe. Rapprochez-vous "+
				"de la coordination si besoin.")
		return nil, false
	}
	return m, true
}

func (s *Server) rows(r *http.Request, sql string, args ...any) ([]map[string]any, error) {
	res, err := s.tx(r).Query(r.Context(), sql, args...)
	if err != nil {
		return nil, err
	}
	rows, err := pgx.CollectRows(res, pgx.RowToMap)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return rows, nil
}

func (s *Server) column(r *http.Request, sql string, args ...any) ([]string, error) {
	rows, err := s.tx(r).Query(r.Context(), sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v *string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		if v != nil {
			out = append(out, *v)
		}
	}
	return out, rows.Err()
}

func (s *Server) counters(r *http.Request, sql string, args ...any) (map[string]int, error) {
	rows, err := s.tx(r).Query(r.Context(), sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var key *string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, err
		}
		if key != nil {
			out[*key] = n
		}
	}
	return out, rows.Err()
}

// orderedCounters keeps the SQL order, which a Go map would lose.
type counter struct {
	Key string `json:"key"`
	N   int    `json:"n"`
}

func (s *Server) orderedCounters(r *http.Request, sql string, args ...any) ([]counter, error) {
	rows, err := s.tx(r).Query(r.Context(), sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []counter{}
	for rows.Next() {
		var key *string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, err
		}
		if key != nil {
			out = append(out, counter{*key, n})
		}
	}
	return out, rows.Err()
}

// cursor: the last row of a page, in the sort's own order. Opaque to the
// client — it is a position in a result set, not something to compose.
type cursor struct {
	Score      int
	Department string
	Commune    string
	Insee      string
}

func encodeCursor(score, department, commune, insee string) string {
	n, _ := strconv.Atoi(strings.TrimSpace(score)) // unreadable sorts as 0, like the query
	raw, _ := json.Marshal(cursor{Score: -n, Department: department,
		Commune: commune, Insee: insee})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// A cursor that does not decode is REFUSED, not silently treated as "start
// from the beginning": serving page 1 in its place returns page 1's own
// cursor, so the browser asks again, forever.
//
// JSON rather than a separator: a commune is free to contain any byte, and
// a delimiter that data can carry is a delimiter that will be carried.
func decodeCursor(raw string) (*cursor, error) {
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("cursor is not base64: %w", err)
	}
	var c cursor
	dec := json.NewDecoder(strings.NewReader(string(decoded)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("cursor is not a position: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("cursor carries more than one position")
	}
	// pgx encodes it as int4: outside that range the query raises, and the
	// volunteer reads "erreur interne" for a bookmark
	if c.Score < math.MinInt32 || c.Score > math.MaxInt32 {
		return nil, fmt.Errorf("cursor score %d is out of range", c.Score)
	}
	// same refusal as the NUL stripped from `q`: PostgreSQL rejects the
	// byte in any text value, and these three travel the same query
	if strings.ContainsRune(c.Department+c.Commune+c.Insee, 0) {
		return nil, fmt.Errorf("cursor carries a NUL byte")
	}
	return &c, nil
}
