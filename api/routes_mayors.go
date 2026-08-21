package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
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
// Another campaign's `assignments` and `accounts` rows are never joined: the
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
		"t.updated_at, t.team_id, c.name AS volunteer_name, " +
		// The team WORKING the card, beside the team that last wrote a
		// status. Nothing is refused on the strength of it: it is what makes
		// « somebody is already on this one » a thing a volunteer can see
		// instead of a card that is not there.
		//
		// TWO columns, and ::text, for the reason `updated_by_team` below is
		// two columns and ::text — the same trap, one row over. `w.name` is
		// NULL for the national scope, which has no row in `teams`, so a
		// screen reading the NAME alone showed a card the coordination had
		// taken as « personne n'est encore dessus ». `taken_by` is the
		// answer: null means nobody, `"0"` means the national scope, and a
		// number means the team `team_name` names.
		"t.team_id::text AS taken_by, w.name AS team_name, " +
		// ::text because a card IS a dictionary of text on the other side —
		// it comes from a CSV as readily as from here. The cast keeps the
		// three answers apart, which is the whole point of the column: null
		// (nobody wrote), '0' (the national scope, which has no team row) and
		// an identifier. A number here would not fit the type, and the
		// obvious way to make it fit — dropping the id and comparing names —
		// loses the difference between « nobody » and « the national scope ».
		"t.updated_by_team::text AS updated_by_team, " +
		"u.name AS updated_by_team_name"
	// mayors is the common, read-only list: it carries no org_id. The work
	// rows do, and the campaign is named in the JOIN CONDITION, never in a
	// WHERE: `WHERE t.org_id = …` would turn these outer joins into inner
	// ones and drop every mayor nobody has taken yet — that is, exactly the
	// ones `mayorAvailable` exists to find.
	//
	// `teams u` names the team that WROTE the status, which is the one
	// attribution a status carries from one team of a campaign to another.
	// Joined on the primary key, so it multiplies no row and the COUNT(*)
	// built on this join still counts mayors.
	assignmentJoinFmt = " FROM mayors m " +
		"LEFT JOIN assignments t ON t.insee_code = m.insee_code AND t.org_id = %[1]s " +
		"LEFT JOIN accounts c ON c.email = t.volunteer AND c.org_id = %[1]s " +
		"LEFT JOIN teams u ON u.id = t.updated_by_team AND u.org_id = %[1]s " +
		"LEFT JOIN teams w ON w.id = t.team_id AND w.org_id = %[1]s"
	// Available: no work row, or a row nobody took and on which nothing was
	// done.
	mayorAvailable = "(t.insee_code IS NULL OR " +
		"(t.volunteer IS NULL AND t.status = 'to_contact'))"
)

// hidePerson: THE CARD CROSSES, THE PERSON DOES NOT.
//
// Every team of a campaign reads every card of it — nothing is hidden and
// nothing is refused. What does not cross is the individual: a card another
// team is working comes back carrying `team_name` and no address and no name,
// which is the line the campaign's counters have always drawn. Coordination
// sees the names, as it sees everything.
//
// Done HERE and not in the SELECT, deliberately. Written as a CASE in the
// query it made the statement a string the isolation canary could no longer
// read — and an unreadable statement is one nothing can say the campaign is
// named in. It also put a parameter in the SELECT list of a query whose
// COUNT(*) sibling does not mention it, which PostgreSQL refuses. One
// function over the row map costs neither, and it is the only place the rule
// is written.
func hidePerson(c *Account, m map[string]any) map[string]any {
	if c.Coordination() {
		return m
	}
	// 0 = the national scope, a real team every account without one belongs
	// to: a plain truthiness test would read it as « nobody », and hand a
	// national card's volunteer to any team that asked.
	if owner, worked := integer(m["team_id"]); worked && owner != c.MyTeam() {
		m["volunteer"] = nil
		m["volunteer_name"] = nil
	}
	return m
}

// cards: the ONE door that reads card rows. It runs the query and masks every
// row before handing it back, so a card query added tomorrow — a leaderboard,
// a « recently statused » tab — inherits the rule instead of having to
// remember it. Applied by hand at three sites, the rule was kept by nothing:
// the fourth site would have leaked other teams' volunteers with every test
// still green, which is the same silence the team wall used to fail in.
// `TestEveryCardQueryMasksThePerson` refuses that: a statement that SELECTS
// the person comes through here, or calls `hidePerson` itself.
func (s *Server) cards(r *http.Request, sql string, args ...any) (
	[]map[string]any, error,
) {
	rows, err := s.rows(r, sql, args...)
	if err != nil {
		return nil, err
	}
	c := accountOf(r)
	for _, m := range rows {
		hidePerson(c, m)
	}
	return rows, nil
}

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

	mine, err := s.cards(r, "SELECT "+mayorSelection+assignmentJoin("$1")+
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
		if len(myDepts) == 0 || slices.Contains(myDepts, d) {
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
	departments, err := s.departmentLabels(r)
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
	v := r.URL.Query()
	// unstorable text (NUL, malformed UTF-8) is refused at the middleware:
	// nothing to strip here anymore
	q := strings.TrimSpace(v.Get("q"))

	// Keyset, not OFFSET. The team modifies the very set being paged: a card
	// entering or leaving the filter between two pages shifts every following
	// offset by one, skipping a mayor — and in a campaign whose object is
	// coverage, a skipped mayor is one nobody ever contacts. The cursor
	// carries the last row of the previous page.
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
	// No team clause: every team of a campaign reads every card of it. What
	// varies by team is whether the PERSON on a card is named, and that is in
	// the selection, not in the filter — so the COUNT below counts the same
	// cards for everybody, which is what the screen's total has to mean.
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
	rows, err := s.cards(r, fmt.Sprintf(
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
	insee := pathParam(r, "insee")
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
	// `mine` and not the address: `volunteer` is already COALESCE'd to the
	// NAME, and an address of another team does not cross — the campaign's
	// counters have always been visible to all and nominative to nobody. It
	// is what the screen shows the « Modifier » button from; what REFUSES is
	// the predicate in the two routes below.
	mine := "(n.volunteer=" + req.p(accountOf(r).Email) + ") AS mine"
	// LIMIT: this history is re-read on EVERY status write, so an unbounded
	// one is paid again at each POST — 800 long notes make a 96 MB body and
	// take the server's heap from 1 to 320 MB for a single request. Nobody
	// has contacted one mayor 200 times.
	notes, err := s.rows(r,
		"SELECT n.id, COALESCE(c.name, n.volunteer) AS volunteer, n.status, "+
			"n.note, n.ts, n.edited_at, "+mine+" "+
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
	// Seen: the status the screen was SHOWING when this was written.
	//
	// Removing the claim from the write left nothing comparing what the
	// writer saw with what is stored, and the window that opens is not the
	// instant it was described as: a tab left open all morning wrote over a
	// status recorded since, silently, with no 409 and no trace of who. It
	// is what the lock used to give, given back without the lock.
	//
	// Absent, it means « à contacter » — the state of a card nobody has
	// touched. A client that does not send it therefore keeps working on
	// free cards and is refused on contested ones, which is the safe half of
	// the bargain rather than a hard break at deploy time.
	Seen string `json:"seen"`
}

// POST /api/mayors/{insee}/status — recording what a contact gave. It claims
// nothing: no card is any team's to hold.
func (s *Server) routeStatus(w http.ResponseWriter, r *http.Request) {
	insee := pathParam(r, "insee")
	// Existence only, for the 404: a status on an INSEE code the list does
	// not carry is a typo, not a record. `loadMayor` would answer the same
	// question through four outer joins and a row map, on the hottest write
	// path there is, to read nothing out of them.
	var exists bool
	if err := s.tx(r).QueryRow(r.Context(),
		"SELECT EXISTS(SELECT 1 FROM mayors WHERE insee_code=$1)", insee).
		Scan(&exists); err != nil {
		s.failure(w, err)
		return
	}
	if !exists {
		errorJSON(w, http.StatusNotFound, "Aucun maire pour le code INSEE %q.", insee)
		return
	}
	c := accountOf(r)
	// No geographic refusal. A perimeter says where a team DRAWS its work; it
	// is not a claim on the mayors inside it, and refusing a status outside
	// it stopped a volunteer recording a call somebody had actually made —
	// the one thing this application exists to write down. The status names
	// the team that wrote it, which is what makes an out-of-perimeter note
	// readable rather than anonymous.
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

	// Writing a status RECORDS something; it does not take the card.
	//
	// It used to claim it — volunteer and team_id stamped on the way past —
	// so a note taken in passing removed the mayor from everyone else's
	// list, permanently, with no way back short of an operator's UPDATE. The
	// intent was to stop two volunteers contacting the same person, and what
	// stops that is the status being SEEN: whoever opens the card next reads
	// « refusé » and moves on. Reserving is a deliberate act and it has its
	// own door, `/api/batch`, which is where a volunteer takes cards to work
	// on. The known limit of trading the lock for the information: two
	// people looking at the same card at the same moment can still both call,
	// where the lock made the second one lose the race and be told so.
	//
	// AND A RESERVATION REFUSES NOTHING EITHER. A batch hands a volunteer
	// cards to work through; it is not a claim on the mayor. Left standing,
	// the write lock made every screen a lie — « travaillée par l'équipe
	// Nord » beside a save button that answers 409 — and it stopped the one
	// thing worth writing down: somebody made the call, and could not record
	// it. What remains below is the `seen` clause, which refuses no person
	// and no team: it refuses writing over a state nobody read.
	seen := d.Seen
	if seen == "" {
		seen = StatusToContact
	}
	//
	// What it DOES record is the team that wrote it. Read by the whole
	// campaign and owned by nobody, a status was attributable to no one; the
	// team is the granularity that answers « who put that there » without a
	// name of another team's crossing to say it.
	tag, err := s.tx(r).Exec(ctx,
		"INSERT INTO assignments(org_id, insee_code, status, updated_at, updated_by_team) "+
			"VALUES($1,$2,$3,$4,$5) "+
			"ON CONFLICT (org_id, insee_code) DO UPDATE SET "+
			"status=excluded.status, updated_at=excluded.updated_at, "+
			"updated_by_team=excluded.updated_by_team "+
			"WHERE assignments.status=$6",
		orgOf(r).ID, insee, d.Status, shortTimestamp(), c.MyTeam(), seen)
	if err != nil {
		s.failure(w, err)
		return
	}
	// One refusal for the two ways the card moved under the writer: somebody
	// reserved it, or somebody recorded something on it. Telling them apart
	// would say who is working where, which no screen of this campaign says;
	// what the writer needs is the same either way — read it again first.
	if tag.RowsAffected() == 0 {
		errorJSON(w, http.StatusConflict,
			"Cette fiche a changé depuis que vous l'avez ouverte : quelqu'un "+
				"l'a prise ou y a enregistré quelque chose. Rafraîchissez-la "+
				"avant d'écrire — deux personnes ne doivent pas contacter le "+
				"même élu.")
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
	s.answerCard(w, r, insee)
}

type noteEdit struct {
	Note string `json:"note"`
}

// noteID reads the {id} of the two routes below, or answers the refusal
// itself. int64 and not int32: `notes.id` is a BIGINT, and an identifier past
// int4 has already answered 500 once, one table over.
func noteID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(pathParam(r, "id"), 10, 64)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, "Identifiant de note illisible.")
		return 0, false
	}
	return id, true
}

// POST /api/mayors/{insee}/notes/{id} — correcting the WORDS of one's own
// note.
//
// The text alone. `status`, `ts`, `volunteer` and `team_id` stay where they
// are: correcting a spelling is not rewriting what happened, and the status
// this note recorded is what the whole campaign reads to decide whether to
// call this person. Somebody who wants to say something else about the
// contact records a new status, which is the control directly under this
// history.
func (s *Server) routeEditNote(w http.ResponseWriter, r *http.Request) {
	insee := pathParam(r, "insee")
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	var d noteEdit
	if !readBody(w, r, &d) {
		return
	}
	// The same ceiling as writing one, and for the same reason: what a call
	// is worth noting fits in 5 000 characters, and this row is re-read on
	// every status write.
	if utf8.RuneCountInString(d.Note) > maxNoteRunes {
		errorJSON(w, http.StatusBadRequest,
			"Une note ne doit pas dépasser %d caractères.", maxNoteRunes)
		return
	}
	// ONE statement, not a read followed by a write: the authorization is the
	// predicate, so there is no window between deciding and writing. AND THE
	// AUTHOR IS THE ONLY ONE — a coordination may delete words it must not
	// carry, it may not put different ones under somebody else's name. That
	// is « whoever sends it is whoever signs it », one register down.
	req := scoped(r)
	tag, err := s.tx(r).Exec(r.Context(),
		"UPDATE notes SET note="+req.p(strings.TrimSpace(d.Note))+
			", edited_at="+req.p(shortTimestamp())+
			" WHERE org_id=$1 AND id="+req.p(id)+
			" AND insee_code="+req.p(insee)+
			" AND volunteer="+req.p(accountOf(r).Email), req.args...)
	if err != nil {
		s.failure(w, err)
		return
	}
	// TWO REFUSALS, because they are two different things to be told.
	//
	// One sentence for both said « seule la personne qui a écrit une note peut
	// en corriger le texte » to somebody correcting THEIR OWN note that a
	// colleague had just removed from the other tab — an authorization
	// refusal, about a note they wrote, which reads as a session gone wrong.
	// They try again, or they stop.
	//
	// The existence question is asked THROUGH THE READER'S OWN EYES: the same
	// filter the card applies, so nothing is revealed that the history does
	// not already show. A note this reader cannot see and a note that is gone
	// get the same answer, which is the one that is true for them.
	if tag.RowsAffected() == 0 {
		seen := scoped(r)
		visible := "n.insee_code=" + seen.p(insee) + " AND n.id=" + seen.p(id)
		if !accountOf(r).Coordination() {
			visible += fmt.Sprintf(" AND (n.team_id IS NULL OR n.team_id=%s)",
				seen.p(accountOf(r).MyTeam()))
		}
		var there bool
		if err := s.tx(r).QueryRow(r.Context(),
			"SELECT EXISTS(SELECT 1 FROM notes n WHERE n.org_id=$1 AND "+
				visible+")", seen.args...).Scan(&there); err != nil {
			s.failure(w, err)
			return
		}
		if !there {
			errorJSON(w, http.StatusNotFound,
				"Cette note n'est plus dans l'historique : quelqu'un l'a "+
					"supprimée depuis. Rafraîchissez la fiche.")
			return
		}
		// It is there and it is somebody else's. Saying so costs nothing: the
		// reader has the line in front of them, with the name beside it.
		errorJSON(w, http.StatusForbidden,
			"Cette note n'est pas de vous : seule la personne qui l'a écrite "+
				"peut en corriger le texte.")
		return
	}
	s.answerCard(w, r, insee)
}

// DELETE /api/mayors/{insee}/notes/{id} — removing a note, and putting the
// card back to what the history then says.
//
// Its author, or the campaign's coordination: a note written by an account
// that has since been closed would otherwise stay for ever, and the only
// remedy left would be an UPDATE typed against production — the one kind of
// access nobody can audit.
func (s *Server) routeDeleteNote(w http.ResponseWriter, r *http.Request) {
	insee := pathParam(r, "insee")
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	me := accountOf(r)
	req := scoped(r)
	// The author's predicate is omitted for a coordination and present for
	// everybody else — the same shape routeToggleAccount builds its filter
	// with, and for the same reason: the role decides which clause applies,
	// never which statement runs.
	mine := ""
	if !me.Coordination() {
		mine = " AND volunteer=" + req.p(me.Email)
	}
	// WHO WROTE IT comes back WITH the row, rather than being asked for in a
	// SELECT of its own: read beforehand, the answer would be about a row this
	// DELETE may well not remove. A note that predates the column being filled
	// carries NULL, and NULL is nobody — never this caller.
	var author *string
	err := s.tx(r).QueryRow(r.Context(),
		"DELETE FROM notes WHERE org_id=$1 AND id="+req.p(id)+
			" AND insee_code="+req.p(insee)+mine+
			" RETURNING volunteer", req.args...).Scan(&author)
	if errors.Is(err, pgx.ErrNoRows) {
		// ONE SENTENCE, and now it is true of BOTH — which is the reason it
		// was one in the first place. « une note se retire par la personne
		// qui l'a écrite » is a sentence about RIGHTS, and it was read by
		// somebody removing their own note that a colleague had taken away a
		// moment earlier: an author told they may not be the author doubts
		// their session. The correction next door splits 403 from 404 because
		// there the reader has the line in front of them with its author
		// beside it; here they may have nothing at all.
		errorJSON(w, http.StatusNotFound,
			"Aucune note à supprimer ici : elle a pu être retirée depuis, ou "+
				"elle n'est pas de vous.")
		return
	}
	if err != nil {
		s.failure(w, err)
		return
	}
	if !s.restoreHead(w, r, insee) {
		return
	}
	// A coordination removing words IT DID NOT WRITE is the act the campaign
	// is owed a trace of. Pseudonymised like every other event: what is logged
	// is that it happened, not to whom.
	//
	// Fired on its own notes as well, the line stopped marking anything: most
	// of what a coordination removes is its own — a typo it took during a call
	// — so the one event worth finding sat in a stream of identical ones, with
	// no author on the record to tell them apart by.
	if me.Coordination() && (author == nil || *author != me.Email) {
		s.securityEvent(r, slog.LevelInfo, "note_deleted",
			"by", s.accountPseudonym(me.Email))
	}
	s.answerCard(w, r, insee)
}

// restoreHead puts the card's status back to what its history now says.
//
// THE HISTORY IS THE REGISTER AND `assignments` IS ITS HEAD. Left alone, a
// card whose last note has just gone keeps announcing « signé » to the whole
// campaign with nothing on record saying so — and emptying the history
// entirely left a status nobody ever wrote. The newest remaining note
// decides; none left is a card nobody has contacted.
//
// Read WITHOUT the team filter the card applies: nothing here reaches the
// browser, and the note that now decides may well belong to another team —
// filtered, a volunteer's deletion would roll the card back past a colleague's
// work that they cannot see.
func (s *Server) restoreHead(w http.ResponseWriter, r *http.Request,
	insee string) bool {
	// THE CARD IS LOCKED BEFORE ITS HEAD IS READ, and this line is the whole
	// difference between a recompute and a silent overwrite.
	//
	// Reading the head and rewriting `assignments` is a read-then-write on a
	// register the whole campaign reads — the same shape as the ceiling this
	// project applies BY THE INSERT and never by a count read before it.
	// Unlocked, a status somebody recorded in between is answered 200 and then
	// overwritten by a head read before it existed: measured through the real
	// server, five rounds in fifteen left `assignments` announcing « email
	// envoyé » with the newest note reading « refus ». Two removals racing
	// each other do it too, without any status write at all.
	//
	// `routeStatus` takes this same row lock in its `ON CONFLICT DO UPDATE`,
	// so the two serialise whichever arrives first: locked here, its upsert
	// waits and then re-reads its own `seen` clause — a 409 rather than a lie;
	// locked there, this SELECT waits and the head read that follows is a new
	// statement, hence a snapshot that includes the note it just wrote.
	//
	// A card with notes always has an assignments row — `routeStatus` upserts
	// one in the transaction that inserts the note, and nothing deletes one —
	// so there is always a row here to lock when there is anything to protect.
	lock := scoped(r)
	if _, err := s.tx(r).Exec(r.Context(),
		"SELECT 1 FROM assignments WHERE org_id=$1 AND insee_code="+
			lock.p(insee)+" FOR UPDATE", lock.args...); err != nil {
		s.failure(w, err)
		return false
	}
	head := scoped(r)
	var status, ts *string
	var team *int
	err := s.tx(r).QueryRow(r.Context(),
		"SELECT status, ts, team_id FROM notes "+
			"WHERE org_id=$1 AND insee_code="+head.p(insee)+
			" ORDER BY id DESC LIMIT 1", head.args...).Scan(&status, &ts, &team)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.failure(w, err)
		return false
	}
	// No note left: the card goes back to what a card nobody has touched
	// says, and it moved NOW — `updated_at` is when the card last changed,
	// and this is a change.
	at := shortTimestamp()
	state := StatusToContact
	if status != nil {
		state = *status
	}
	if ts != nil {
		at = *ts
	}
	// Unconditional, and a no-op when a note from the MIDDLE of the history
	// was removed: the head is then the same row it already was. An
	// assignments row that does not exist is a card nobody has worked, and
	// there is nothing to put back.
	put := scoped(r)
	if _, err := s.tx(r).Exec(r.Context(),
		"UPDATE assignments SET status="+put.p(state)+
			", updated_at="+put.p(at)+", updated_by_team="+put.p(team)+
			" WHERE org_id=$1 AND insee_code="+put.p(insee),
		put.args...); err != nil {
		s.failure(w, err)
		return false
	}
	return true
}

// answerCard re-reads the card INSIDE the transaction and commits behind it,
// as routeStatus does: the answer then describes exactly what is recorded,
// and a failing commit can still be reported — which answering 200 first
// would make impossible.
func (s *Server) answerCard(w http.ResponseWriter, r *http.Request, insee string) {
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
	if d.Department != "" && len(myDepts) > 0 && !slices.Contains(myDepts, d.Department) {
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

	// scoped, not &query{}: the campaign is $1 by construction. Bound by
	// hand among other parameters, `org_id=$1` meant whichever value
	// happens to be first: reordering two of these lines is enough to filter
	// on a team identifier, with every guard still green.
	req := scoped(r)
	org, team, me := "$1", req.p(c.MyTeam()), req.p(c.Email)
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

// GET /api/export.csv — the whole campaign's cards, like the screen. And
// like the screen, the PERSON on a card another team is working is not in it:
// a download must not be the way round what the page does not show. The team
// is, in the column that replaces the name.
func (s *Server) routeExport(w http.ResponseWriter, r *http.Request) {
	me := accountOf(r)
	// `team_name` is a column of the file, not a substitution: whoever opens
	// it reads that a card is being worked and by which team, on the same row
	// as the empty volunteer cell rather than instead of it.
	cols := append(append([]string{}, Cols...),
		"volunteer", "status", "updated_at", "volunteer_name", "team_name")
	selection := make([]string, 0, len(cols)+1)
	for _, c := range Cols {
		selection = append(selection, "m."+c)
	}
	// ALIASED, because the masking below reads the row by column name — the
	// one rule `hidePerson` states, applied here too rather than written a
	// second time as a CASE that would drift from it.
	selection = append(selection, "t.volunteer AS volunteer",
		"COALESCE(t.status,'to_contact') AS status",
		"t.updated_at AS updated_at",
		"COALESCE(c.name, t.volunteer) AS volunteer_name",
		"w.name AS team_name",
		// read to decide, never written
		"t.team_id AS team_id")

	rows, err := s.tx(r).Query(r.Context(), fmt.Sprintf(
		"SELECT %s%s ORDER BY m.department, m.commune",
		strings.Join(selection, ","), assignmentJoin("$1")),
		scopeOrg(r))
	if err != nil {
		s.failure(w, err)
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=suivi_maires.csv")
	// nominative rows behind a session: never for a shared cache
	w.Header().Set("Cache-Control", "no-store")
	// BOM: Excel and LibreOffice otherwise open UTF-8 as latin-1
	if _, err := w.Write([]byte("\uFEFF")); err != nil {
		return
	}
	writer := csv.NewWriter(w)
	writer.Comma = ';'
	if err := writer.Write(cols); err != nil {
		truncatedExport(err)
		return
	}
	// Positions resolved ONCE, from the names the SELECT aliases: the columns
	// are found by name, so the file cannot silently shift the day one is
	// added, and the loop then costs no map and no hash. 34,826 rows through
	// a map per row is 34,826 allocations and some two million string hashes
	// for a lookup that never changes.
	at := map[string]int{}
	for i, f := range rows.FieldDescriptions() {
		at[f.Name] = i
	}
	position := make([]int, len(cols))
	for i, name := range cols {
		position[i] = at[name]
	}
	teamAt, volunteerAt := at["team_id"], at["volunteer"]
	nameAt := at["volunteer_name"]
	// ONE probe map, reused for the whole stream: `hidePerson` states the
	// rule, and a download must not be the way round it — but a map per row
	// is 34,826 allocations. Three assignments in, two out, no allocation
	// after the first. Restating the rule inline instead is what a second
	// implementation looks like on the day the first one changes.
	probe := make(map[string]any, 3)
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			truncatedExport(err)
			return
		}
		probe["team_id"] = values[teamAt]
		probe["volunteer"] = values[volunteerAt]
		probe["volunteer_name"] = values[nameAt]
		hidePerson(me, probe)
		values[volunteerAt] = probe["volunteer"]
		values[nameAt] = probe["volunteer_name"]
		row := make([]string, len(cols))
		for i, p := range position {
			row[i] = csvSafe(text(values[p]))
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
	slog.Error("truncated CSV export", "error", err)
}

// loadMayor: one card of THIS campaign, whoever in it is working on the
// mayor. It refuses on one thing only, and it is the wall that stayed: a
// campaign never reads its neighbour, because `assignmentJoin` names the
// campaign and `mayors` is common and public.
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
	// No refusal on the strength of another team having the card. It comes
	// back with `team_name` on it and without the person, which is what tells
	// a volunteer somebody is already there without turning the mayor into a
	// card that does not exist.
	return hidePerson(accountOf(r), m), true
}
