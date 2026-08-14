package main

import (
	"encoding/json"
	"log"
	"net/http"
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
		if !contains(CampaignKeys, k) {
			unknown = append(unknown, k)
			continue
		}
		// the acceptance path copies a campaign already bounded at
		// maxCampaignRunes; the edit path bounded nothing, and orgs.name
		// follows `candidat` into every unauthenticated /api/config
		if utf8.RuneCountInString(v) > maxCampaignRunes {
			errorJSON(w, http.StatusBadRequest,
				"Le champ « %s » ne doit pas dépasser %d caractères.",
				k, maxCampaignRunes)
			return
		}
		values[k] = strings.TrimSpace(v)
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
	// happens to hold: `id=$1` where $1 is what app.org_id carries, so the
	// application and RLS cannot come to disagree about which campaign is
	// being written. `orgs` is the one per-campaign table RLS lets everyone
	// READ — resolving a subdomain requires it — so its writes are the place
	// where the two walls must say the same thing.
	req := scoped(r)
	if _, err := s.tx(r).Exec(r.Context(),
		// The name follows the candidate: it is what /api/config returns as
		// organisation.name and what the multi-campaign apex lists. Left
		// out, it stayed at the template value for the campaign's whole
		// life, however many times coordination filled the form.
		"UPDATE orgs SET campaign="+req.p(string(raw))+"::jsonb, "+
			"batch_size="+req.p(batchSize)+", name=COALESCE(NULLIF("+
			req.p(strings.TrimSpace(values["candidat"]))+",''), name) "+
			"WHERE id=$1",
		req.args...); err != nil {
		s.failure(w, err)
		return
	}
	// Approval is the only point where an instance administrator sees a
	// campaign's name, and nothing here sends it back for review: a
	// campaign validated under one name can rename itself afterwards, and
	// the apex lists the new one. Traced, so the rename is at least
	// visible to whoever operates the instance.
	if name := strings.TrimSpace(values["candidat"]); name != "" && name != org.Name {
		log.Printf("campaign %q renamed itself from %q to %q",
			org.Slug, org.Name, name)
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	replyJSON(w, http.StatusOK, map[string]any{
		"campaign": values, "batch_size": batchSize,
		"unfilled": UnfilledKeys(values),
	})
}

// GET /api/campaign/public — the campaign, and nothing else.
//
// It exists for the browser version, which is published on another origin
// (GitHub Pages) and has no server of its own: a volunteer opening it with
// ?org=<slug> gets the campaign filled in rather than retyping nine fields
// that must match their colleagues' exactly.
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
	// A campaign still at its template values must not pre-fill anything:
	// it would spread "Prénom NOM" to volunteers who have no way to know.
	// Which keys are still at their template value is operational detail,
	// and this body is readable from any origin: the refusal says that
	// there is nothing to pre-fill, not what is missing.
	if len(UnfilledKeys(org.Campaign)) > 0 {
		errorJSON(w, http.StatusConflict,
			"Cette campagne n'est pas encore configurée : rien à pré-remplir.")
		return
	}
	campaign := map[string]string{}
	for _, k := range CampaignKeys {
		campaign[k] = org.Campaign[k]
	}
	replyJSON(w, http.StatusOK, map[string]any{
		"slug": org.Slug, "name": org.Name, "campaign": campaign})
}
