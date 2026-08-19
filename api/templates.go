package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// A campaign writes its own message templates, and each of its teams writes
// its own on top — this is the half of that which runs in Go.
//
// THE ENGINE IS TYPESCRIPT AND THIS FILE RENDERS NOTHING. `noyau/messages.ts`
// is the project's single implementation, shared by the interface and the mass
// mailing, and the API has never generated a message. What it does here is
// REFUSE, at the moment a template is saved, the texts that engine would
// refuse later.
//
// Why the refusal has to be here and not only in the browser: an invalid
// template is not a bad request, it is every message this campaign sends. The
// mass mailing discovers it on a Sunday evening with 1 960 letters not
// printed, and a volunteer discovers it as a card that shows an error where
// the message should be — neither of them wrote it, and neither can fix it.
// Saved through a form, through a script, or through a client that is one
// release behind, the text is judged the same way, once, in front of the
// person who typed it.
//
// The vocabulary is `noyau/placeholders.json`, the referee both languages
// answer to — the same dispositif as campaign-optional.json and
// campaign-env.json, for the same reason: the copy below is a copy, and a copy
// drifts. TestThePlaceholderVocabularyMatchesTheSharedFile holds them
// together, and the TypeScript canary beside it holds the shared file to what
// `fields()` actually produces.

// The six files a campaign sends. A name outside this list is refused rather
// than stored: `createEngine` looks templates up by name, so a seventh key
// would be text nothing ever renders — configuration a campaign believes it
// has written and no mayor will ever read.
var templateFiles = []string{
	"email.txt",
	"email_decouverte.txt",
	"courrier.txt",
	"courrier_decouverte.txt",
	"telephone.txt",
	"telephone_decouverte.txt",
}

// The placeholders available whatever the recipient's rank.
var commonPlaceholders = map[string]bool{
	"adresse": true, "argument_personnel": true, "candidat": true,
	"candidat_description": true, "candidat_description_longue": true,
	"civilite": true, "civilite_courte": true, "code_postal": true,
	"commune": true, "commune_de": true, "contact_email": true,
	"contact_tel": true, "date": true, "departement": true, "email": true,
	"horaires": true, "le_la": true, "nom": true, "prenom": true,
	"proposition_telephone": true, "relance_telephone": true,
	"salutation": true, "salutation_courte": true, "seul_e": true,
	"signataire": true, "signataire_qualite": true, "site": true,
	"sollicite_e": true, "telephone": true, "ville": true,
	"ville_envoi": true,
}

// THE PROJECT'S CARDINAL INVARIANT, enforced by two DISJOINT vocabularies.
//
// "you presented X" is said only to a proven endorser. Choosing the template
// file by rank is not enough on its own once campaigns edit the files: pasting
// « En {annee_recente}, vous avez présenté {candidat_recent}. » into the
// discovery template printed « En , vous avez présenté . » to 32 866 mayors,
// in silence. Because these two sets share no name, that paste is refused BY
// NAME here, and so is the reverse.
var endorserPlaceholders = map[string]bool{
	"annee_recente": true, "candidat_recent": true, "parrainages": true,
}

var discoveryPlaceholders = map[string]bool{
	"contexte": true, "contexte_tel": true,
}

// Nearly twice the longest template shipped (the phone script, 2 647 runes),
// and the ceiling is ARITHMETIC rather than taste: a save carries all six, a
// rune is at most four bytes, and 6 × 5 000 × 4 = 120 000 — under the 128 KiB
// a body may weigh. Any larger and a legitimate six-template save could be
// refused by `maxBodySize` instead, which answers about kilobytes to somebody
// who was writing a letter.
//
// It is also why these have a route of their own rather than a tenth field on
// POST /api/campaign: that body already weighs 94 616 bytes at its ceilings,
// exactly the reason the logo is not a field either.
const maxTemplateRunes = 5000

// The engine's own reading, and it must stay the engine's: `\w` covers only
// [A-Za-z0-9_], so a guard written with it let « {prénom} » through and 1 953
// emails went out carrying it in the clear.
var rxTemplatePlaceholder = regexp.MustCompile(`\{([^{}]+)\}`)

// placeholdersKnownTo: the vocabulary of ONE template file.
//
// The suffix is what decides, exactly as `createEngine` decides which file to
// render: `_decouverte` is written to somebody with no endorsement on record,
// anything else thanks an endorser.
func placeholdersKnownTo(file string) func(string) bool {
	byRank := endorserPlaceholders
	if strings.HasSuffix(file, "_decouverte.txt") {
		byRank = discoveryPlaceholders
	}
	return func(name string) bool {
		return commonPlaceholders[name] || byRank[name]
	}
}

// validateTemplate: everything about ONE template that can be known without a
// mayor. What is left to render time is data — an empty commune, a civility
// the register does not spell — and that has never been the template's fault.
func validateTemplate(file, text string) error {
	if !knownTemplateFile(file) {
		return fmt.Errorf("modèle inconnu : %q. Attendus : %s",
			file, strings.Join(templateFiles, ", "))
	}
	if n := utf8.RuneCountInString(text); n > maxTemplateRunes {
		return fmt.Errorf("le modèle « %s » dépasse %d caractères (%d)",
			file, maxTemplateRunes, n)
	}
	if !legibleText(text) {
		return fmt.Errorf("le modèle « %s » contient un caractère de contrôle "+
			"ou invisible qui réordonne le texte", file)
	}
	known := placeholdersKnownTo(file)
	for _, m := range rxTemplatePlaceholder.FindAllStringSubmatch(text, -1) {
		// the engine trims: « { commune } » designates the commune, and
		// writing it that way is not a mistake
		name := strings.TrimSpace(m[1])
		if known(name) {
			continue
		}
		// A placeholder of the OTHER rank is the mistake this whole split
		// exists for, and it deserves its own sentence: told merely that
		// « {candidat_recent} » is unknown, the person who pasted it looks for
		// a typo in a name that is spelt correctly.
		if commonPlaceholders[name] || endorserPlaceholders[name] ||
			discoveryPlaceholders[name] {
			return fmt.Errorf("le modèle « %s » s'adresse à %s, il ne peut pas "+
				"employer {%s}", file, audienceOf(file), name)
		}
		return fmt.Errorf("le modèle « %s » emploie {%s}, qui n'existe pas. "+
			"Champs disponibles : %s", file, name, availableIn(file))
	}
	// The engine reads the subject off the first line and throws without it,
	// so a saved email template that has none is a campaign whose email
	// channel is down until somebody edits it back.
	if strings.HasPrefix(file, "email") &&
		!strings.HasPrefix(strings.TrimLeft(text, " \t\n"), "OBJET:") {
		return fmt.Errorf("le modèle « %s » doit commencer par une ligne "+
			"« OBJET: … » : c'est l'objet de l'email", file)
	}
	return nil
}

func audienceOf(file string) string {
	if strings.HasSuffix(file, "_decouverte.txt") {
		return "un maire dont aucun parrainage n'est connu"
	}
	return "un maire qui a déjà parrainé"
}

func availableIn(file string) string {
	known := placeholdersKnownTo(file)
	var names []string
	for _, set := range []map[string]bool{
		commonPlaceholders, endorserPlaceholders, discoveryPlaceholders,
	} {
		for name := range set {
			if known(name) {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

func knownTemplateFile(name string) bool {
	for _, f := range templateFiles {
		if f == name {
			return true
		}
	}
	return false
}

// storableTemplates: what goes into the column, or the sentence that says why
// nothing does.
//
// AN EMPTY VALUE REMOVES THE OVERRIDE rather than storing a template of
// nothing. That is the shape a textarea sends when somebody selects all and
// deletes, and it is also exactly what « revenir au texte fourni » means —
// stored literally it would be a campaign whose letter renders as one blank
// page, five hundred times. `mergeTemplates` reads an empty layer the same way
// on the other side, so the two ends agree even about a row written before
// this function existed.
//
// The line endings are normalised HERE and not at render: a textarea in a
// browser on Windows sends CRLF, the engine folds it every time it renders,
// and a column holding both spellings of the same text is a column where
// « did the coordination change this? » cannot be answered by comparing.
func storableTemplates(in map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for file, text := range in {
		text = strings.ReplaceAll(text, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")
		if strings.TrimSpace(text) == "" {
			// still refused if the NAME is not one we render: a client
			// clearing « emial.txt » is a client that never wrote to a real
			// template, and saying so is how it finds out
			if !knownTemplateFile(file) {
				return nil, fmt.Errorf("modèle inconnu : %q. Attendus : %s",
					file, strings.Join(templateFiles, ", "))
			}
			continue
		}
		if err := validateTemplate(file, text); err != nil {
			return nil, err
		}
		out[file] = text
	}
	return out, nil
}

// templateLayers: what this account's messages are rendered from, over and
// above the templates the image carries.
//
// TWO LAYERS AND NOT ONE RESOLVED SET, because the screen that EDITS them has
// to tell them apart: a référent looking at « Mon équipe » needs to see which
// of these texts their team wrote and which it is inheriting, or « revenir au
// texte de la campagne » is a button nobody can aim. Merging them is one
// shared function away (`mergeTemplates` in noyau/), so the browser resolves
// them exactly as the mass mailing would.
//
// Both statements are written OUT, at the call that runs them, rather than
// handed to one helper that takes SQL: a statement the canary cannot read is a
// statement nothing can say the campaign is named in, and `teams` is walled.
// `campaignTemplates` is a helper of that kind and not of the other — it holds
// its own literal statement rather than being handed one.
func (s *Server) templateLayers(r *http.Request, c *Account) (
	map[string]string, map[string]string, error) {
	campaign, err := s.campaignTemplates(r)
	if err != nil {
		return nil, nil, err
	}
	// NationalTeam has no row in `teams` — it is every account carrying no
	// team, coordination included — so there is nothing to read and nothing to
	// override with.
	if c == nil || c.MyTeam() == NationalTeam {
		return campaign, map[string]string{}, nil
	}
	var raw string
	err = s.tx(r).QueryRow(r.Context(),
		"SELECT templates::text FROM teams WHERE org_id=$1 AND id=$2",
		scopeOrg(r), c.MyTeam()).Scan(&raw)
	team, err := decodeTemplates(raw, err)
	if err != nil {
		return nil, nil, err
	}
	return campaign, team, nil
}

// campaignTemplates: the overlay THIS campaign carries, over the image's six.
//
// Its own function because three callers want it — the two layers a signed-in
// account renders from, and the public route the account-less version asks
// when it adopts a campaign. The statement stays LITERAL here, at the call
// that runs it, so the isolation canary reads it: `orgs` IS the campaign, and
// the row is the scope itself, bound as $1.
func (s *Server) campaignTemplates(r *http.Request) (map[string]string, error) {
	var raw string
	err := s.tx(r).QueryRow(r.Context(),
		"SELECT templates::text FROM orgs WHERE id=$1", scopeOrg(r)).Scan(&raw)
	return decodeTemplates(raw, err)
}

// decodeTemplates: the overlay a row carries, or an empty one.
//
// A row that is NOT THERE is not an error here: a team deleted between the
// session and this read, or an account naming a team that never existed, has
// no overrides — which is its campaign's texts, the same answer
// `teamDepartments` gives to the same absence.
func decodeTemplates(raw string, err error) (map[string]string, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("unreadable templates: %w", err)
	}
	return out, nil
}

type templatesRequest struct {
	// The whole overlay, and it REPLACES: dropping a key is the only way to
	// say « revenir au texte fourni », so a partial body could not express it.
	// A missing field is an empty overlay for the same reason — this route
	// does one thing, and the `nil = untouched` rule its neighbours follow
	// exists for screens that save several settings at once.
	Templates map[string]string `json:"templates"`
}

// POST /api/campaign/templates — coordination rewrites the campaign's own
// messages.
//
// A ROUTE OF ITS OWN rather than a tenth field on POST /api/campaign, for the
// reason the logo has one: that body already weighs 94 616 bytes at its
// ceilings, and six templates do not fit in what is left.
func (s *Server) routeCampaignTemplates(w http.ResponseWriter, r *http.Request) {
	stored, raw, ok := s.acceptTemplates(w, r)
	if !ok {
		return
	}
	req := scoped(r)
	// `orgs` IS the campaign — the row is the one the scope names, bound as $1
	// by the constructor, so nothing here chooses which campaign is written.
	tag, err := s.tx(r).Exec(r.Context(),
		"UPDATE orgs SET templates="+req.p(raw)+"::jsonb WHERE id=$1",
		req.args...)
	s.finishTemplates(w, r, stored, tag, err)
}

// POST /api/team/templates — a référent rewrites their OWN team's messages.
//
// Their own, and there is no team identifier on this route for that reason: a
// lead reaches their own team and nobody else — `routeToggleAccount` draws the
// same line — and an identifier in the path is one more foreign identifier to
// have to refuse. It is also the split the two screens already make: « Ma
// campagne » holds the campaign, « Mon équipe » holds one team.
func (s *Server) routeTeamTemplates(w http.ResponseWriter, r *http.Request) {
	// A coordination account holds the campaign and no team, so there is no
	// row here to write. `leadOnly` already refuses it at the door; this is
	// the same fact stated where the identifier is used, because a role guard
	// and a scope are two different things and only one of them is in the SQL.
	team := accountOf(r).MyTeam()
	if team == NationalTeam {
		errorJSON(w, http.StatusBadRequest,
			"Ce compte n'appartient à aucune équipe : les modèles de la "+
				"campagne s'éditent depuis « Ma campagne ».")
		return
	}
	stored, raw, ok := s.acceptTemplates(w, r)
	if !ok {
		return
	}
	req := scoped(r)
	tag, err := s.tx(r).Exec(r.Context(),
		"UPDATE teams SET templates="+req.p(raw)+"::jsonb "+
			"WHERE org_id=$1 AND id="+req.p(team), req.args...)
	s.finishTemplates(w, r, stored, tag, err)
}

// acceptTemplates: read the body and REFUSE what the engine would refuse.
//
// The validation is the point of both routes, so it is written once — a rule
// stated a second time is the copy that stops refusing something.
func (s *Server) acceptTemplates(w http.ResponseWriter, r *http.Request) (
	map[string]string, string, bool) {
	var d templatesRequest
	if !readBody(w, r, &d) {
		return nil, "", false
	}
	stored, err := storableTemplates(d.Templates)
	if err != nil {
		// the engine's own complaint, in its own words and in French: whoever
		// is looking at this screen wrote the text and can fix it
		errorJSON(w, http.StatusBadRequest, "%s", err.Error())
		return nil, "", false
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		s.failure(w, err)
		return nil, "", false
	}
	return stored, string(raw), true
}

func (s *Server) finishTemplates(w http.ResponseWriter, r *http.Request,
	stored map[string]string, tag pgconn.CommandTag, err error) {
	if err != nil {
		s.failure(w, err)
		return
	}
	// The row is gone — a team deleted while its lead had the screen open.
	// Answered rather than committed in silence: somebody told « enregistré »
	// by a save that wrote nothing goes on writing.
	if tag.RowsAffected() == 0 {
		errorJSON(w, http.StatusNotFound, "Cette équipe n'existe plus.")
		return
	}
	if err := s.commit(r); err != nil {
		s.failure(w, err)
		return
	}
	// What was STORED, not what was sent: a text emptied is an override
	// removed, and the screen has to show the inherited one in its place.
	replyJSON(w, http.StatusOK, map[string]any{"templates": stored})
}
