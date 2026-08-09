package main

import (
	"encoding/json"
	"errors"
	"fmt"
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
// It was 100, chosen to sit below the queue screen's LIMIT — which turned
// « a request can be pushed off the page » into « no request can be filed
// at all », closable by an attacker for 9 KB. The queue now returns every
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
	email := strings.ToLower(strings.TrimSpace(d.RequesterEmail))
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
	for k, v := range d.Campaign {
		if utf8.RuneCountInString(v) > maxCampaignRunes {
			errorJSON(w, http.StatusBadRequest,
				"Le champ « %s » de la campagne ne doit pas dépasser "+
					"2000 caractères.", k)
			return
		}
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
	// the campaign configuration is NOT returned here: the moderation queue
	// is read on an administration screen, which only needs the request's
	// identity.
	//
	// EVERY pending request, and only then the last decided ones. A single
	// LIMIT over both pushed a real campaign's request off the only screen
	// that can accept it — 200 anonymous requests were enough, and the
	// campaign had no way to know.
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

type hostingDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// POST /api/admin/requests/{id} — accept (hence create the campaign) or
// refuse.
func (s *Server) routeDecideHosting(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
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
	var campaign []byte
	err = s.tx(r).QueryRow(ctx,
		"SELECT slug, name, campaign::text, state FROM hosting_requests WHERE id=$1 "+
			"FOR UPDATE", id).Scan(&slug, &name, &campaign, &state)
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
		var orgID int
		if err := s.tx(r).QueryRow(ctx,
			"INSERT INTO orgs(slug, name, campaign, batch_size, state, created_at) "+
				"VALUES($1,$2,$3::jsonb,$4,$5,$6) RETURNING id",
			slug, name, string(campaign), defaultBatchSize, OrgActive,
			shortTimestamp()).Scan(&orgID); err != nil {
			s.failure(w, err)
			return
		}
		// The coordination account is created with the campaign: without
		// it, the organisation exists but nobody can enter or open access,
		// and the requester receives an address that refuses to open.
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
		var requesterEmail, requesterName string
		if err := s.tx(r).QueryRow(ctx,
			"SELECT requester_email, requester_name FROM hosting_requests WHERE id=$1",
			id).Scan(&requesterEmail, &requesterName); err != nil {
			s.failure(w, err)
			return
		}
		// the account is born INSIDE the created campaign: the instance
		// scope cannot write there, and that is exactly the wanted default
		if err := withOrgScope(ctx, s.tx(r), orgID, OrgInstance, func() error {
			_, err := s.tx(r).Exec(ctx,
				"INSERT INTO accounts(org_id, email, name, password_hash, role, created_at, created_by) "+
					"VALUES($1,$2,$3,$4,$5,$6,$7)",
				orgID, requesterEmail, requesterName, hashed, RoleCoordination,
				shortTimestamp(), accountOf(r).Email)
			return err
		}); err != nil {
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
	replyJSON(w, http.StatusOK, response)
}
