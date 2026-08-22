package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"
)

// The campaign configuration lives in the database, per organisation.
//
// A campaign born from a hosting request arrives with not a word filled in:
// without this screen, its coordination would have no way to fill it, and
// the application would send "Prénom NOM" to mayors. The PARAPHE_*
// variables now only bootstrap an instance's first campaign.

type campaignRequest struct {
	Campaign  map[string]string `json:"campaign"`
	BatchSize *int              `json:"batch_size"`
	// nil = untouched: the apex directory listing is edited from the same
	// screen as the campaign, and a save must not flip it by omission
	Listed *bool `json:"listed"`
	// Whether the campaign telephones the mayors it writes to — nil =
	// untouched, for the reason above. Opt-in and campaign-wide: a volunteer
	// answers for themselves on their own account, and only falls back to
	// this when they have not.
	PhoneOutreach *bool `json:"phone_outreach"`
	// The campaign's own name, and nil = untouched for the same reason. It
	// USED to be `candidat`, copied into orgs.name at every save — so a
	// campaign approved as « Alliance écologiste » renamed itself to
	// « Marie Dupont » the first time its coordination filled the form, on
	// the apex's public directory as well as in its own header. A campaign
	// is not its candidate: it presents one.
	Name *string `json:"name"`
}

// POST /api/campaign — coordination fills in its campaign.
func (s *Server) routeUpdateCampaign(w http.ResponseWriter, r *http.Request) {
	var d campaignRequest
	if !readBody(w, r, &d) {
		return
	}
	org := orgOf(r)

	// only known keys are taken: an invented key would fill no
	// {placeholder} and would only bloat the configuration unseen
	values := completeCampaign(org.Campaign)
	var unknown []string
	for k, v := range d.Campaign {
		if !slices.Contains(CampaignKeys, k) {
			unknown = append(unknown, k)
			continue
		}
		// the acceptance path copies a campaign already bounded at
		// maxCampaignRunes; the edit path bounded nothing, and every one of
		// these values reaches an unauthenticated /api/config
		if utf8.RuneCountInString(v) > maxCampaignRunes {
			errorJSON(w, http.StatusBadRequest,
				"Le champ « %s » ne doit pas dépasser %d caractères.",
				k, maxCampaignRunes)
			return
		}
		values[k] = strings.TrimSpace(v)
	}
	// Both reach an unauthenticated /api/config, and the NAME goes further
	// still: the apex prints it in its PUBLIC directory of hosted campaigns.
	// A right-to-left override there reverses a neighbouring campaign's line
	// on a page no campaign owns — the one place a campaign's own text leaves
	// its walls.
	if candidate := values["candidat"]; candidate != "" && !legible(candidate) {
		errorJSON(w, http.StatusBadRequest,
			"Le nom du candidat ne doit contenir ni retour à la ligne ni "+
				"caractère invisible.")
		return
	}
	// nil = untouched. A name is allowed to be EMPTIED — an unnamed campaign
	// is a state the directory already knows, and it is where every campaign
	// bootstrapped without one starts — but a name that is not empty has to
	// be one a human reads: `visible` refuses the zero-width runes TrimSpace
	// leaves behind, the same reading the two public forms use.
	name := org.Name
	if d.Name != nil {
		name = strings.TrimSpace(*d.Name)
		if utf8.RuneCountInString(name) > maxNameRunes {
			errorJSON(w, http.StatusBadRequest,
				"Le nom de la campagne ne doit pas dépasser %d caractères.",
				maxNameRunes)
			return
		}
		if name != "" && (!legible(name) || !visible(name)) {
			errorJSON(w, http.StatusBadRequest,
				"Le nom de la campagne ne doit contenir ni retour à la ligne "+
					"ni caractère invisible.")
			return
		}
	}
	if len(unknown) > 0 {
		errorJSON(w, http.StatusBadRequest,
			"Clés de campagne inconnues : %s. Attendues : %s.",
			strings.Join(unknown, ", "), strings.Join(CampaignKeys, ", "))
		return
	}

	batchSize := org.BatchSize
	if d.BatchSize != nil {
		batchSize = *d.BatchSize
		// A batch of 0 never returns anything and a huge batch drains the
		// pool in one click, with nobody able to give the cards back.
		if batchSize < 1 || batchSize > 100 {
			errorJSON(w, http.StatusBadRequest,
				"La taille de lot doit tenir entre 1 et 100 (reçu %d).", batchSize)
			return
		}
	}

	raw, err := json.Marshal(values)
	if err != nil {
		s.failure(w, err)
		return
	}
	// The row edited is the one the SCOPE names, not the one this handler
	// happens to hold: `id=$1`, bound by the constructor, so the row written
	// is the one the request speaks for by construction. `orgs` is read by
	// every campaign — resolving a subdomain requires it — which is why its
	// WRITES are where naming the campaign matters most.
	listed := org.Listed
	if d.Listed != nil {
		listed = *d.Listed
	}
	phone := org.PhoneOutreach
	if d.PhoneOutreach != nil {
		phone = *d.PhoneOutreach
	}
	req := scoped(r)
	if _, err := s.tx(r).Exec(r.Context(),
		"UPDATE orgs SET campaign="+req.p(string(raw))+"::jsonb, "+
			"batch_size="+req.p(batchSize)+", listed="+req.p(listed)+", "+
			"phone_outreach="+req.p(phone)+", "+
			"name="+req.p(name)+" WHERE id=$1",
		req.args...); err != nil {
		s.failure(w, err)
		return
	}
	// Approval is the only point where an instance administrator sees a
	// campaign's name, and nothing here sends it back for review: a
	// campaign validated under one name can rename itself afterwards, and
	// the apex lists the new one. Traced, so the rename is at least
	// visible to whoever operates the instance.
	if name != org.Name {
		slog.Info("campaign renamed itself",
			"slug", org.Slug, "from", org.Name, "to", name)
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	replyJSON(w, http.StatusOK, map[string]any{
		"campaign": values, "batch_size": batchSize, "listed": listed,
		"phone_outreach": phone,
		"unfilled":       UnfilledKeys(values), "name": name,
	})
}

type logoRequest struct {
	DataURI string `json:"data_uri"`
}

// logoOf: what /api/config and /api/campaign/public say about a campaign's
// logo — an absolute URL the browser can use, or nothing. The URL is built
// HERE and not in the interface: the key layout is this file's business, and
// the origin is a deployment setting the page cannot know.
func (s *Server) logoOf(org *Org) map[string]string {
	if org == nil || org.LogoKey == "" || s.media == nil {
		return nil
	}
	return map[string]string{
		"url":  s.media.URL(org.LogoKey),
		"type": org.LogoType,
	}
}

// mediaUnavailable answers the one case a coordination can hit through no
// fault of its own: an instance whose operator configured no object store.
// Said plainly, because the alternative is a button that fails with "erreur
// interne" and a volunteer who retries.
func (s *Server) mediaUnavailable(w http.ResponseWriter) bool {
	if s.media != nil {
		return false
	}
	errorJSON(w, http.StatusNotImplemented,
		"Cette instance n'a pas de stockage d'images configuré : le logo "+
			"n'est pas disponible. C'est à l'opérateur de l'instance de le "+
			"mettre en place.")
	return true
}

// POST /api/campaign/logo — coordination uploads or replaces the logo.
//
// The image travels as a data URI in JSON rather than as multipart, so
// `jsonOnly` — the second anti-CSRF barrier behind SameSite=Lax — keeps
// covering every write route without an exception carved for this one.
func (s *Server) routeUploadLogo(w http.ResponseWriter, r *http.Request) {
	if s.mediaUnavailable(w) {
		return
	}
	var d logoRequest
	if !readBody(w, r, &d) {
		return
	}
	org := orgOf(r)
	logo, code, refusal := readLogo(org.Slug, d.DataURI)
	if logo == nil {
		errorJSON(w, code, "%s", refusal)
		return
	}

	// The connection goes back to the pool BEFORE the store is called, and
	// is asked for again after. Nothing is written yet, so there is nothing
	// to commit; what matters is that the round trip below holds no
	// connection. Held, a store that stops answering takes every connection
	// the instance has and the readiness probe with them — measured, six
	// probes out of six.
	s.release(r)

	// The object goes in BEFORE the pointer: the other order publishes a URL
	// that answers 404 for as long as the write takes, and for ever if it
	// fails. An object nobody points at is invisible; a pointer to nothing
	// is a broken image on every screen.
	if err := s.media.Put(r.Context(), logo.Key, logo.ContentType, logo.Raw); err != nil {
		slog.Error("logo not stored", "slug", org.Slug, "error", err)
		errorJSON(w, http.StatusBadGateway,
			"Le stockage d'images n'a pas accepté le fichier. "+
				"Réessayez ; si cela persiste, prévenez l'opérateur de l'instance.")
		return
	}

	if err := s.reacquire(r); err != nil {
		// The object is written and nothing points at it: an orphan of a few
		// kilobytes, which the next backup copies for nothing. Said, not
		// hidden — the alternative is a pointer to an object that may not be
		// there.
		slog.Error("logo stored but the pointer could not be moved",
			"slug", org.Slug, "key", logo.Key, "error", err)
		s.failure(w, err)
		return
	}

	// Read and replaced under the SAME lock, so `previous` is what this
	// request is superseding and nothing else. The lock spans two local
	// statements now — microseconds — where it used to span the store call.
	previous, ok := s.lockLogo(w, r)
	if !ok {
		return
	}
	req := scoped(r)
	if _, err := s.tx(r).Exec(r.Context(),
		"UPDATE orgs SET logo_key="+req.p(logo.Key)+", "+
			"logo_type="+req.p(logo.ContentType)+" WHERE id=$1",
		req.args...); err != nil {
		s.failure(w, err)
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}

	// Only once the pointer has MOVED, and never blocking the answer: a
	// leftover object costs a few kilobytes, a deletion racing a rollback
	// costs the campaign its logo.
	s.forgetLogo(previous)
	replyJSON(w, http.StatusOK, map[string]any{
		"logo": map[string]string{
			"url": s.media.URL(logo.Key), "type": logo.ContentType,
		},
	})
}

// DELETE /api/campaign/logo — coordination removes it.
//
// No jsonOnly here, and for the same reason DELETE /api/session has none: a
// cross-site DELETE cannot be issued by a form, only by fetch, which needs a
// CORS preflight this API does not answer.
func (s *Server) routeDeleteLogo(w http.ResponseWriter, r *http.Request) {
	if s.mediaUnavailable(w) {
		return
	}
	previous, ok := s.lockLogo(w, r)
	if !ok {
		return
	}
	req := scoped(r)
	if _, err := s.tx(r).Exec(r.Context(),
		"UPDATE orgs SET logo_key='', logo_type='' "+
			"WHERE id=$1", req.args...); err != nil {
		s.failure(w, err)
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	s.forgetLogo(previous)
	replyJSON(w, http.StatusOK, map[string]any{"logo": nil})
}

// lockLogo takes the campaign's row for the rest of the transaction and
// returns the key it currently points at.
//
// The LOCK is what makes `previous` mean something: reading `org.LogoKey`
// off the scope is a value from before this request's transaction, so two
// writers could each read the same predecessor and one of them would delete
// an object the other had just published. Read and replaced under the same
// lock, each writer supersedes exactly one key.
//
// It spans two LOCAL statements and nothing else. It used to span the call
// to the store as well, because a key derived from the content alone can be
// written back by a concurrent upload of the same image — the eight random
// bytes in every key ended that, and with it the need to hold a database
// connection behind a machine that can stop answering.
func (s *Server) lockLogo(w http.ResponseWriter, r *http.Request) (string, bool) {
	req := scoped(r)
	var key string
	if err := s.tx(r).QueryRow(r.Context(),
		"SELECT logo_key FROM orgs WHERE id=$1 FOR UPDATE",
		req.args...).Scan(&key); err != nil {
		s.failure(w, err)
		return "", false
	}
	return key, true
}

// forgetLogo drops an object the database no longer points at.
//
// DETACHED, and with a context of its own. The request's context is
// cancelled the moment the response goes out, so a deletion carried on it
// would be cut off half the time; carried BEFORE the response, it puts a
// round-trip — up to mediaTimeout of one — between a coordination's click
// and their answer, for a few kilobytes of housekeeping.
//
// And the pointer is read again UNDER THE SAME LOCK the writers take,
// immediately before the deletion. A key is a digest of the CONTENT, so
// removing a logo and putting the same image back produces the very key
// this was about to delete. Detached without the lock, two overlapping
// requests — a second tab, a double click — had the stale deletion land
// after the fresh write and destroy an object every screen still named: 14
// rounds out of 15 against a store on the same machine, and the window only
// widens with the distance to it. A check without the lock narrows that
// window; it does not close it, because the writer commits after the read.
// forgetLogo removes the object a committed pointer no longer names.
//
// It reads NOTHING back, takes no lock and no connection, and that is what
// the unique suffix in every key buys. A key cannot come back: no upload
// can produce this one again, so once the pointer has moved there is no
// state under which deleting it destroys what anybody points at. The
// version before it re-read the pointer under a row lock and held that lock
// across the store call — correct, and it put a database connection behind
// a machine that can stop answering.
//
// Detached because the campaign's logo is already right: the only thing
// left is a few kilobytes, and nobody should wait for them.
func (s *Server) forgetLogo(previous string) {
	if s.media == nil || previous == "" {
		return
	}
	// Through `detach` rather than a bare goroutine: it counts the work in
	// s.outbound, which shutdown drains. Started on its own, a deletion in
	// flight when SIGTERM lands is simply cut, and the object it was
	// removing stays in the bucket for ever with nothing naming it. Same
	// mechanism as a message leaving for the relay, for the same reason.
	s.detach(mediaTimeout, func(ctx context.Context) {
		if err := s.media.Delete(ctx, previous); err != nil {
			// Said, not raised: the campaign's logo is already correct, and
			// the only cost is an orphan the next `task backup-media`
			// copies for nothing.
			slog.Warn("previous logo not removed from the store",
				"key", previous, "error", err)
		}
	})
}

// GET /api/campaign/public — the campaign, and nothing else.
//
// It exists for the browser version, which has no server of its own: rather
// than retyping nine fields that must match their colleagues' exactly, a
// volunteer gets the campaign filled in. Two callers, and the cross-origin
// header below is for the first only — a build published elsewhere (GitHub
// Pages) opened with ?org=<slug>, and the build this instance serves under
// /navigateur/, which asks this route at the root of its OWN origin to learn
// which campaign served it.
//
// It returns ONLY what already travels in every message to a mayor — the
// candidate, the contacts, the signature — never the operational detail
// /api/config carries (whether accounts exist, the batch size, which keys
// are still at their template value). Public data, so the wall it crosses
// is deliberate and narrow.
func (s *Server) routePublicCampaign(w http.ResponseWriter, r *http.Request) {
	// BEFORE the refusals, not after: without the header the browser
	// discards the body, and a typo'd slug or the apex surfaced as "Failed
	// to fetch" instead of the sentence the API took care to write.
	// Public by nature, so no credentials are involved and "*" is the
	// honest answer: restricting it to one origin would only pretend.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	org := orgOf(r)
	if org == nil {
		errorJSON(w, http.StatusNotFound, "Aucune campagne à cette adresse.")
		return
	}
	// THE CAMPAIGN'S OWN TEXTS travel too, so the account-less version writes
	// what this campaign writes rather than what the image ships. Without
	// them a campaign that rewrote its letter had two voices — one for the
	// volunteers with an account, one for the volunteers without — and
	// nothing on either screen saying which.
	//
	// Public, like the nine values beside them and for the same reason: this
	// is the body an account-less volunteer adopts, and these texts are what
	// the campaign already sends to five hundred elected officials. Only the
	// CAMPAIGN's layer travels — a team's overlay is its team's, and this
	// mode has no team.
	templates, err := s.campaignTemplates(r)
	if err != nil {
		s.failure(w, err)
		return
	}
	body := map[string]any{
		"slug": org.Slug, "name": org.Name,
		"templates": templates, "logo": s.logoOf(org)}
	// A campaign still at its template values must not pre-fill the NINE:
	// it would spread "Prénom NOM" to volunteers who have no way to know,
	// so the `campaign` block is OMITTED — not refused wholesale. Refused
	// with a 409 it took the campaign's own TEXTS down with it: a campaign
	// that had rewritten its email but not finished its nine fields served
	// nothing at all, and its browser version spoke the image's words while
	// the team version spoke its own — measured on production, reported as
	// « je n'ai toujours pas par défaut le template que j'ai enregistré ».
	// Which keys are still at their template value stays unsaid: that is
	// operational detail, and this body is readable from any origin.
	// The logo above travels like the texts, whatever the fields say: it is
	// the campaign's own mark, carrying no template value to spread. The
	// adopting build downloads it ONCE and keeps a data URI — it promises
	// that nothing leaves the browser, and a remote URL in its header would
	// make that false at every load.
	if len(UnfilledKeys(org.Campaign)) == 0 {
		campaign := map[string]string{}
		for _, k := range CampaignKeys {
			campaign[k] = org.Campaign[k]
		}
		body["campaign"] = campaign
	}
	replyJSON(w, http.StatusOK, body)
}
