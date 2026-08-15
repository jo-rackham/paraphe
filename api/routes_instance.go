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
	if name == "" || requester == "" || !strings.Contains(email, "@") {
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
	// A request is never deleted, so this is the only thing bounding what
	// the table can grow to.
	var waiting int
	if err := s.tx(r).QueryRow(r.Context(),
		"SELECT count(*) FROM hosting_requests WHERE state=$1",
		RequestPending).Scan(&waiting); err != nil {
		s.failure(w, err)
		return
	}
	if waiting >= maxPendingRequests {
		errorJSON(w, http.StatusServiceUnavailable,
			"Trop de demandes sont en attente de modération sur cette "+
				"instance. Écrivez à son administration : elle peut créer "+
				"votre campagne sans passer par ce formulaire.")
		return
	}

	campaign, err := json.Marshal(completeCampaign(d.Campaign))
	if err != nil {
		s.failure(w, err)
		return
	}
	var id int64
	if err := s.tx(r).QueryRow(r.Context(),
		"INSERT INTO hosting_requests(slug, name, campaign, requester_email, "+
			"requester_name, message, state, ts) "+
			"VALUES($1,$2,$3::jsonb,$4,$5,$6,$7,$8) RETURNING id",
		slug, name, string(campaign), email, requester,
		strings.TrimSpace(d.Message), RequestPending,
		shortTimestamp()).Scan(&id); err != nil {
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
	const queueColumns = "id, slug, name, requester_email, requester_name, " +
		"message, state, COALESCE(reason,'') AS reason, COALESCE(ts,'') AS ts, " +
		"COALESCE(decided_at,'') AS decided_at, " +
		"COALESCE(decided_by,'') AS decided_by FROM hosting_requests "
	requests, err := s.rows(r,
		"SELECT "+queueColumns+"WHERE state='pending' ORDER BY id DESC LIMIT $1",
		maxPendingRequests)
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

// createCampaign creates the organisation and, in the same stroke, its
// coordination account — without which the address opens for nobody. The
// password is returned to be shown ONCE and is never stored in the clear.
// Approving a hosting request and the administration's direct creation both
// end here: one implementation, so the two doors cannot drift apart.
func createCampaign(ctx context.Context, tx pgx.Tx, slug, name string,
	campaign []byte, coordinationEmail, coordinationName, createdBy string,
) (int, string, error) {
	var orgID int
	if err := tx.QueryRow(ctx,
		"INSERT INTO orgs(slug, name, campaign, batch_size, state, created_at) "+
			"VALUES($1,$2,$3::jsonb,$4,$5,$6) RETURNING id",
		slug, name, string(campaign), defaultBatchSize, OrgActive,
		shortTimestamp()).Scan(&orgID); err != nil {
		return 0, "", err
	}
	password, err := ReadablePassword()
	if err != nil {
		return 0, "", err
	}
	hashed, err := HashPassword(password)
	if err != nil {
		return 0, "", err
	}
	// the account is born INSIDE the created campaign, which is what org_id
	// names here: creation is the one place the instance scope writes into
	// a campaign, and it writes exactly one row
	if _, err := tx.Exec(ctx,
		"INSERT INTO accounts(org_id, email, name, password_hash, role, created_at, created_by) "+
			"VALUES($1,$2,$3,$4,$5,$6,$7)",
		orgID, coordinationEmail, coordinationName, hashed, RoleCoordination,
		shortTimestamp(), createdBy); err != nil {
		return 0, "", err
	}
	return orgID, password, nil
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
		if err := s.tx(r).QueryRow(ctx,
			"SELECT requester_email, requester_name FROM hosting_requests WHERE id=$1",
			id).Scan(&requesterEmail, &requesterName); err != nil {
			s.failure(w, err)
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
		orgID, password, err := createCampaign(ctx, s.tx(r), slug, name,
			blank, requesterEmail, requesterName, accountOf(r).Email)
		if err != nil {
			s.failure(w, err)
			return
		}
		response["organisation"] = orgID
		response["address"] = fmt.Sprintf("%s.%s", slug, BaseDomain())
		response["coordination"] = requesterEmail
		// returned ONCE, never stored in the clear: the administrator
		// passes it on
		response["password"] = password
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
	if name == "" || coordination == "" || !strings.Contains(email, "@") {
		errorJSON(w, http.StatusBadRequest,
			"Le nom de la campagne, le nom et l'email de sa coordination sont requis.")
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
			"Le nom de la campagne et celui de sa coordination ne doivent pas "+
				"dépasser 200 caractères.")
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
	orgID, password, err := createCampaign(ctx, s.tx(r), slug, name, campaign,
		email, coordination, accountOf(r).Email)
	if err != nil {
		s.failure(w, err)
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	s.securityEvent(r, slog.LevelInfo, "campaign_created",
		"slug", slug, "by", s.accountPseudonym(accountOf(r).Email))
	replyJSON(w, http.StatusCreated, map[string]any{
		"organisation": orgID, "slug": slug,
		"address":      fmt.Sprintf("%s.%s", slug, BaseDomain()),
		"coordination": email,
		// returned ONCE, never stored in the clear: the administrator
		// passes it on
		"password": password,
	})
}

// GET /api/campaigns — the public directory of hosted campaigns, on the
// apex. Name and address only, active campaigns only: both facts are
// already public by construction — the subdomain answers, and its public
// campaign endpoint says the name.
func (s *Server) routeCampaignDirectory(w http.ResponseWriter, r *http.Request) {
	campaigns, err := s.rows(r,
		"SELECT slug, name FROM orgs WHERE state=$1 ORDER BY name, slug",
		OrgActive)
	if err != nil {
		s.failure(w, err)
		return
	}
	replyJSON(w, http.StatusOK, map[string]any{
		"campaigns": campaigns, "base_domain": BaseDomain(),
	})
}
