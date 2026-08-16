package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// The instance landing page: requesting the hosting of a campaign, and
// moderating those requests.
//
// The form is public, but it creates NOTHING: without moderation, the first
// abuse is squatting a candidate's name, and the squatted campaign would
// have no recourse since the subdomain would already be taken. A request
// therefore waits for an instance administrator to approve it, and it is
// that approval which creates the organisation.

const (
	RequestPending  = "pending"
	RequestAccepted = "accepted"
	RequestRefused  = "refused"
)

// maxPendingRequests bounds STORAGE, nothing else: a request is never
// deleted, and one costs at most ~92 KB now that its fields are bounded.
// A ceiling low enough to sit below the queue screen's LIMIT would turn
// « a request can be pushed off the page » into « no request can be filed
// at all », closable by an attacker for 9 KB. The queue returns every
// pending request instead, so nothing is hidden and this ceiling is only
// reached by an attack that has to pay 10x more for it.
const maxPendingRequests = 1000

type hostingRequest struct {
	Slug           string            `json:"slug"`
	Name           string            `json:"name"`
	RequesterEmail string            `json:"requester_email"`
	RequesterName  string            `json:"requester_name"`
	Message        string            `json:"message"`
	Campaign       map[string]string `json:"campaign"`
	// nil = listed: the directory is the default, discretion the choice
	Listed *bool `json:"listed"`
}

// POST /api/demande — public form to host a campaign.
func (s *Server) routeHostingRequest(w http.ResponseWriter, r *http.Request) {
	var d hostingRequest
	if !readBody(w, r, &d) {
		return
	}
	slug := strings.ToLower(strings.TrimSpace(d.Slug))
	name := strings.TrimSpace(d.Name)
	email := normalizeEmail(d.RequesterEmail)
	requester := strings.TrimSpace(d.RequesterName)

	if !ValidSlug(slug) {
		errorJSON(w, http.StatusBadRequest,
			"L'adresse demandée doit tenir en 2 à 63 caractères, en minuscules, "+
				"chiffres et tirets, et ne pas être un nom réservé : %q.", slug)
		return
	}
	// `visible`, not `== ""`: a name of zero-width runes survives TrimSpace
	// and reaches the moderation queue as a blank row. The campaign's own
	// form reads its names the same way — two public doors, one reading.
	if !visible(name) || !visible(requester) || !storableEmail(email) {
		errorJSON(w, http.StatusBadRequest,
			"Le nom de la campagne, votre nom et votre adresse email sont requis.")
		return
	}
	// This email BECOMES the primary key of `accounts` on acceptance, and
	// that column is btree-indexed: unbounded, a public form could file a
	// request that no administrator can ever accept (54000 on their own
	// screen) — and which holds the slug in `pending` for good, the very
	// squat moderation exists to prevent. Same ceilings as the team forms.
	if utf8.RuneCountInString(email) > maxEmailRunes {
		errorJSON(w, http.StatusBadRequest,
			"Cette adresse email est trop longue (254 caractères maximum).")
		return
	}
	if utf8.RuneCountInString(name) > maxNameRunes ||
		utf8.RuneCountInString(requester) > maxNameRunes {
		errorJSON(w, http.StatusBadRequest,
			"Le nom de la campagne et votre nom ne doivent pas dépasser "+
				"200 caractères.")
		return
	}
	// The administration READS these three before it approves them, and this
	// form is as anonymous as the campaign's own. The address is read like
	// the rest: `storableEmail` above asks whether a message can still leave
	// for it, this asks whether what the administrator reads is what is
	// stored — a bidi override passes the first and fails the second.
	if !legible(name) || !legible(requester) || !legible(email) {
		errorJSON(w, http.StatusBadRequest,
			"Le nom de la campagne, votre nom et votre adresse email ne doivent "+
				"contenir ni retour à la ligne ni caractère invisible.")
		return
	}
	if utf8.RuneCountInString(d.Message) > maxNoteRunes {
		errorJSON(w, http.StatusBadRequest,
			"Votre message ne doit pas dépasser 5000 caractères.")
		return
	}
	// The campaign is nine free values and the largest field of the form:
	// bounding the four scalars around it and leaving this one open let a
	// single anonymous client write 89 MB in under four seconds, and
	// nothing ever deletes a hosting request. A full disk takes down every
	// campaign on the instance.
	prefilled := false
	for k, v := range d.Campaign {
		if utf8.RuneCountInString(v) > maxCampaignRunes {
			errorJSON(w, http.StatusBadRequest,
				"Le champ « %s » de la campagne ne doit pas dépasser "+
					"2000 caractères.", k)
			return
		}
		if strings.TrimSpace(v) != "" {
			prefilled = true
		}
	}
	// The public form does not fill these values, and approval no longer
	// carries them into the campaign. A client that sends them anyway is
	// trying to open a campaign under an identity nobody moderated: the
	// attempt is inert, and it is said out loud.
	if prefilled {
		s.securityEvent(r, slog.LevelWarn, "hosting_request_prefilled",
			"slug", slug)
	}

	// An already-taken slug is refused EARLY: the requester can pick
	// another one right away, instead of learning it after moderation.
	taken, err := slugTaken(r.Context(), s.tx(r), slug)
	if err != nil {
		s.failure(w, err)
		return
	}
	if taken {
		errorJSON(w, http.StatusConflict,
			"L'adresse %s.%s est déjà utilisée par une campagne.", slug, BaseDomain())
		return
	}
	var pending bool
	if err := s.tx(r).QueryRow(r.Context(),
		"SELECT EXISTS(SELECT 1 FROM hosting_requests WHERE slug=$1 AND state=$2)",
		slug, RequestPending).Scan(&pending); err != nil {
		s.failure(w, err)
		return
	}
	if pending {
		errorJSON(w, http.StatusConflict,
			"Une demande porte déjà sur l'adresse %s.%s et attend une réponse.",
			slug, BaseDomain())
		return
	}
	campaign, err := json.Marshal(completeCampaign(d.Campaign))
	if err != nil {
		s.failure(w, err)
		return
	}
	// A request is never deleted, so the ceiling is the only thing bounding
	// what the table can grow to — and it is applied BY THE INSERT, never by
	// a count read before it. Read separately, two clients both saw 999 and
	// both wrote; the queue then dropped the oldest, legitimate requests off
	// the only screen that can accept them. No row comes back when the
	// instance is full.
	var id int64
	err = s.tx(r).QueryRow(r.Context(),
		"INSERT INTO hosting_requests(slug, name, campaign, requester_email, "+
			"requester_name, message, state, ts, listed) "+
			"SELECT $1,$2,$3::jsonb,$4,$5,$6,$7,$8,$9 WHERE (SELECT count(*) "+
			"FROM hosting_requests WHERE state=$7) < $10 RETURNING id",
		slug, name, string(campaign), email, requester,
		strings.TrimSpace(d.Message), RequestPending,
		shortTimestamp(), d.Listed == nil || *d.Listed,
		maxPendingRequests).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		errorJSON(w, http.StatusServiceUnavailable,
			"Trop de demandes sont en attente de modération sur cette "+
				"instance. Écrivez à son administration : elle peut créer "+
				"votre campagne sans passer par ce formulaire.")
		return
	}
	if err != nil {
		// The loser of the race against the partial unique index: the check
		// above read « none pending » a moment before the other insert
		// committed. Same answer as that check gives, not a 500.
		if isUniqueViolation(err) {
			errorJSON(w, http.StatusConflict,
				"Une demande porte déjà sur l'adresse %s.%s et attend une réponse.",
				slug, BaseDomain())
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
		"id": id, "slug": slug,
		"message": fmt.Sprintf("Demande enregistrée. Elle sera examinée par "+
			"l'administration de %s, qui vous répondra à %s.", BaseDomain(), email),
	})
}

// GET /api/admin/requests — the moderation queue, and the campaigns in place.
func (s *Server) routeHostingQueue(w http.ResponseWriter, r *http.Request) {
	// the campaign configuration is NOT returned here, and approval does not
	// carry it into the created campaign either: an administrator can only
	// weigh what they are shown, and nine values they never see are nine
	// values they cannot moderate. Coordination fills them in itself, on the
	// screen that already says which ones are still empty.
	//
	// EVERY pending request, and only then the last decided ones. A single
	// LIMIT over both would let 200 anonymous requests push a real
	// campaign's off the only screen that can accept it, with no way for
	// that campaign to know.
	//
	// The pending read carries NO ceiling of its own either. Bounded at
	// maxPendingRequests it was bounded by a number the insert only
	// approximated: past the cap — where a race is able to leave the table —
	// this showed its newest thousand and dropped the OLDEST, the legitimate
	// early requests, below the cut, while the flood sat on top. No decision
	// ever brought them back, and nobody knew they were there. The insert
	// bounds what exists, this shows all of it.
	const queueColumns = "id, slug, name, requester_email, requester_name, " +
		"message, state, listed, COALESCE(reason,'') AS reason, COALESCE(ts,'') AS ts, " +
		"COALESCE(decided_at,'') AS decided_at, " +
		"COALESCE(decided_by,'') AS decided_by FROM hosting_requests "
	requests, err := s.rows(r,
		"SELECT "+queueColumns+"WHERE state='pending' ORDER BY id DESC")
	if err != nil {
		s.failure(w, err)
		return
	}
	decided, err := s.rows(r,
		"SELECT "+queueColumns+"WHERE state<>'pending' ORDER BY id DESC LIMIT 200")
	if err != nil {
		s.failure(w, err)
		return
	}
	requests = append(requests, decided...)
	orgs, err := s.rows(r,
		"SELECT id, slug, name, state, COALESCE(created_at,'') AS created_at "+
			"FROM orgs ORDER BY slug")
	if err != nil {
		s.failure(w, err)
		return
	}
	replyJSON(w, http.StatusOK, map[string]any{
		"requests": requests, "organisations": orgs,
		"base_domain": BaseDomain(),
	})
}

// newCampaign: what creating one produced. The password and the invitation
// token are both one-shot secrets — they exist here, travel once, and are
// stored nowhere in the clear.
type newCampaign struct {
	org      int
	password string
	token    string
}

// createCampaign creates the organisation and, in the same stroke, its
// coordination account — without which the address opens for nobody — and
// the invitation that lets it in without anyone relaying a password.
// Approving a hosting request and the administration's direct creation both
// end here: one implementation, so the two doors cannot drift apart.
func (s *Server) createCampaign(ctx context.Context, tx pgx.Tx, slug, name string,
	campaign []byte, coordinationEmail, coordinationName, createdBy string,
	listed bool,
) (newCampaign, error) {
	var out newCampaign
	if err := tx.QueryRow(ctx,
		"INSERT INTO orgs(slug, name, campaign, batch_size, state, created_at, listed) "+
			"VALUES($1,$2,$3::jsonb,$4,$5,$6,$7) RETURNING id",
		slug, name, string(campaign), defaultBatchSize, OrgActive,
		shortTimestamp(), listed).Scan(&out.org); err != nil {
		return newCampaign{}, err
	}
	password, err := ReadablePassword()
	if err != nil {
		return newCampaign{}, err
	}
	hashed, err := HashPassword(password)
	if err != nil {
		return newCampaign{}, err
	}
	// the account is born INSIDE the created campaign, which is what org_id
	// names here: creation is the one place the instance scope writes into
	// a campaign, and it writes exactly one row
	if _, err := tx.Exec(ctx,
		"INSERT INTO accounts(org_id, email, name, password_hash, role, created_at, created_by) "+
			"VALUES($1,$2,$3,$4,$5,$6,$7)",
		out.org, coordinationEmail, coordinationName, hashed, RoleCoordination,
		shortTimestamp(), createdBy); err != nil {
		return newCampaign{}, err
	}
	// The same crossing, one row further: the invitation belongs to the
	// campaign just created, not to the instance scope this request runs in.
	// It is what spares an administrator from relaying a password to somebody
	// they have never spoken to.
	token, err := s.mintInvitation(ctx, tx, out.org, coordinationEmail)
	if err != nil {
		return newCampaign{}, err
	}
	out.password, out.token = password, token
	return out, nil
}

type hostingDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// POST /api/admin/requests/{id} — accept (hence create the campaign) or
// refuse.
func (s *Server) routeDecideHosting(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(pathParam(r, "id"), 10, 64)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, "Identifiant de demande illisible.")
		return
	}
	var d hostingDecision
	if !readBody(w, r, &d) {
		return
	}
	if d.Decision != RequestAccepted && d.Decision != RequestRefused {
		errorJSON(w, http.StatusBadRequest,
			"Décision inconnue : %q (accepted ou refused).", d.Decision)
		return
	}
	ctx := r.Context()

	// FOR UPDATE: two administrators can process the same request at the
	// same moment, and accepting twice would create two organisations for
	// one subdomain — or fail on uniqueness, saying nothing useful.
	var slug, name, state string
	err = s.tx(r).QueryRow(ctx,
		"SELECT slug, name, state FROM hosting_requests WHERE id=$1 "+
			"FOR UPDATE", id).Scan(&slug, &name, &state)
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

	response := map[string]any{"id": id, "slug": slug, "decision": d.Decision}
	// Filled on acceptance, sent once the transaction has closed: a link must
	// not arrive before the campaign it opens exists.
	var invite invitation
	if d.Decision == RequestAccepted {
		taken, err := slugTaken(ctx, s.tx(r), slug)
		if err != nil {
			s.failure(w, err)
			return
		}
		if taken {
			errorJSON(w, http.StatusConflict,
				"L'adresse %s a été prise entre-temps : refusez la demande en "+
					"l'expliquant, la campagne pourra en redemander une autre.", slug)
			return
		}
		var requesterEmail, requesterName string
		var listed bool
		if err := s.tx(r).QueryRow(ctx,
			"SELECT requester_email, requester_name, listed FROM hosting_requests WHERE id=$1",
			id).Scan(&requesterEmail, &requesterName, &listed); err != nil {
			s.failure(w, err)
			return
		}
		// The row was written by a public form and is read back HERE to open a
		// coordination account from it. Hardening that form does not harden what
		// is already in the table, and rows filed before it are still pending on
		// a live instance: one can carry an address that renders as somebody
		// else's, which then becomes a primary key nobody can correct. Refusing
		// the request stays open to the administrator.
		if !storableEmail(requesterEmail) || !legible(requesterEmail) ||
			!visible(requesterName) || !legible(requesterName) {
			errorJSON(w, http.StatusConflict,
				"Cette demande a été enregistrée avant que la saisie ne soit "+
					"durcie : son adresse ou son nom ne peuvent pas ouvrir de "+
					"compte. Refusez-la en l'expliquant.")
			return
		}
		// EMPTY, whatever the request carried: what the administrator
		// approved is a name and an address, not an identity. Reading the
		// submitted values back here would open a campaign under a candidate
		// nobody moderated — the very squat this queue exists to refuse.
		// Coordination fills the nine values itself, and until it does, every
		// page says so and the mass mailing refuses to run.
		blank, err := json.Marshal(completeCampaign(nil))
		if err != nil {
			s.failure(w, err)
			return
		}
		created, err := s.createCampaign(ctx, s.tx(r), slug, name,
			blank, requesterEmail, requesterName, accountOf(r).Email, listed)
		if err != nil {
			s.failure(w, err)
			return
		}
		response["organisation"] = created.org
		response["address"] = fmt.Sprintf("%s.%s", slug, BaseDomain())
		response["coordination"] = requesterEmail
		// returned ONCE, never stored in the clear: the administrator
		// passes it on — unless the invitation below did it for them
		response["password"] = created.password
		invite = invitation{
			email: requesterEmail, name: requesterName,
			by: accountOf(r).Name, campaign: name, slug: slug,
			token: created.token,
		}
	}

	if _, err := s.tx(r).Exec(ctx,
		"UPDATE hosting_requests SET state=$1, reason=$2, decided_at=$3, decided_by=$4 "+
			"WHERE id=$5", d.Decision, strings.TrimSpace(d.Reason),
		shortTimestamp(), accountOf(r).Email, id); err != nil {
		s.failure(w, err)
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	s.release(r) // before the relay, which may take thirty seconds
	// The SAME shape as the two other doors, always: a key that appears only
	// sometimes is a key the interface reads by accident when it is there and
	// by luck when it is not.
	if d.Decision == RequestAccepted {
		sent, warning := s.sendInvitation(invite)
		response["invitation_sent"] = sent
		if warning != "" {
			response["invitation_error"] = warning
		}
	}
	// the slug is on its way to being a public subdomain — not a secret;
	// the moderator, like every account in these logs, is a pseudonym
	s.securityEvent(r, slog.LevelInfo, "hosting_decided",
		"slug", slug, "decision", d.Decision,
		"by", s.accountPseudonym(accountOf(r).Email))
	replyJSON(w, http.StatusOK, response)
}

type campaignCreation struct {
	Slug              string            `json:"slug"`
	Name              string            `json:"name"`
	CoordinationEmail string            `json:"coordination_email"`
	CoordinationName  string            `json:"coordination_name"`
	Campaign          map[string]string `json:"campaign"`
	// nil = listed, like the public form
	Listed *bool `json:"listed"`
}

// POST /api/admin/campaigns — the administration opens a campaign without a
// hosting request: its own campaigns, one arranged off-form, or the way out
// the queue's ceiling message promises when moderation is flooded. A pending
// request on the same slug stays untouched: approving it later answers 409
// and tells the moderator to refuse it with a word of explanation.
func (s *Server) routeCreateCampaign(w http.ResponseWriter, r *http.Request) {
	var d campaignCreation
	if !readBody(w, r, &d) {
		return
	}
	slug := strings.ToLower(strings.TrimSpace(d.Slug))
	name := strings.TrimSpace(d.Name)
	email := normalizeEmail(d.CoordinationEmail)
	coordination := strings.TrimSpace(d.CoordinationName)

	// the same bounds as the public form: both doors write the same columns
	if !ValidSlug(slug) {
		errorJSON(w, http.StatusBadRequest,
			"L'adresse demandée doit tenir en 2 à 63 caractères, en minuscules, "+
				"chiffres et tirets, et ne pas être un nom réservé : %q.", slug)
		return
	}
	if !visible(name) || !visible(coordination) || !storableEmail(email) {
		errorJSON(w, http.StatusBadRequest,
			"Le nom de la campagne, ainsi que le nom et l'adresse email du "+
				"compte de coordination, sont requis.")
		return
	}
	// the campaign name goes on the apex's PUBLIC directory, and the
	// coordination's name on its own screens: same reading as the form above
	if !legible(name) || !legible(coordination) || !legible(email) {
		errorJSON(w, http.StatusBadRequest,
			"Le nom de la campagne, votre nom et l'adresse email ne doivent "+
				"contenir ni retour à la ligne ni caractère invisible.")
		return
	}
	if utf8.RuneCountInString(email) > maxEmailRunes {
		errorJSON(w, http.StatusBadRequest,
			"Cette adresse email est trop longue (254 caractères maximum).")
		return
	}
	if utf8.RuneCountInString(name) > maxNameRunes ||
		utf8.RuneCountInString(coordination) > maxNameRunes {
		errorJSON(w, http.StatusBadRequest,
			"Le nom de la campagne et celui du compte de coordination ne "+
				"doivent pas dépasser 200 caractères.")
		return
	}
	for k, v := range d.Campaign {
		if utf8.RuneCountInString(v) > maxCampaignRunes {
			errorJSON(w, http.StatusBadRequest,
				"Le champ « %s » de la campagne ne doit pas dépasser "+
					"2000 caractères.", k)
			return
		}
	}

	ctx := r.Context()
	taken, err := slugTaken(ctx, s.tx(r), slug)
	if err != nil {
		s.failure(w, err)
		return
	}
	if taken {
		errorJSON(w, http.StatusConflict,
			"L'adresse %s.%s est déjà utilisée par une campagne.", slug, BaseDomain())
		return
	}
	campaign, err := json.Marshal(completeCampaign(d.Campaign))
	if err != nil {
		s.failure(w, err)
		return
	}
	created, err := s.createCampaign(ctx, s.tx(r), slug, name, campaign,
		email, coordination, accountOf(r).Email, d.Listed == nil || *d.Listed)
	// The slug check above is a plain SELECT, so two administrators can both
	// read "free" and both proceed. PostgreSQL settles it on the unique
	// index; what an operator must not read is "erreur interne, prévenez la
	// coordination" for a race that resolves itself by picking another name.
	if isUniqueViolation(err) {
		errorJSON(w, http.StatusConflict,
			"L'adresse %s.%s a été prise entre-temps.", slug, BaseDomain())
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
	s.release(r) // before the relay, which may take thirty seconds
	sent, warning := s.sendInvitation(invitation{
		email: email, name: coordination, by: accountOf(r).Name,
		campaign: name, slug: slug, token: created.token,
	})
	s.securityEvent(r, slog.LevelInfo, "campaign_created",
		"slug", slug, "by", s.accountPseudonym(accountOf(r).Email))
	reply := map[string]any{
		"organisation": created.org, "slug": slug,
		"address":      fmt.Sprintf("%s.%s", slug, BaseDomain()),
		"coordination": email,
		// returned ONCE, never stored in the clear: the administrator
		// passes it on — unless the invitation did it for them
		"password":        created.password,
		"invitation_sent": sent,
	}
	if warning != "" {
		reply["invitation_error"] = warning
	}
	replyJSON(w, http.StatusCreated, reply)
}

// GET /api/campaigns — the public directory of hosted campaigns, on the
// apex. Name and address only, active campaigns only: both facts are
// already public by construction — the subdomain answers, and its public
// campaign endpoint says the name.
func (s *Server) routeCampaignDirectory(w http.ResponseWriter, r *http.Request) {
	rows, err := s.rows(r,
		"SELECT slug, name FROM orgs WHERE state=$1 AND listed ORDER BY name, slug",
		OrgActive)
	if err != nil {
		s.failure(w, err)
		return
	}
	// A name still at the shipped template ("Prénom NOM") is not an identity
	// to advertise: the campaign appears once its coordination has named it —
	// the same doctrine as the banner and the mass mailing's refusal. The
	// bootstrap campaign is born in that state; a moderated one arrives with
	// the name its hosting request carried.
	campaigns := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		name, _ := c["name"].(string)
		if templateValue(name) {
			continue
		}
		campaigns = append(campaigns, c)
	}
	replyJSON(w, http.StatusOK, map[string]any{
		"campaigns": campaigns, "base_domain": BaseDomain(),
	})
}
