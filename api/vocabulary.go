package main

// Business vocabulary shared by every route: the list columns, the contact
// statuses, the outreach ranks. These are ordered lists, not maps: the
// order is the screens' order, and a Go map would shuffle it at every
// start.

// Columns imported from out/04_base_complete.csv. A column added to the CSV
// (a new signal) must be added here: the schema migrates itself, but the
// import does not guess what to read.
var Cols = []string{
	"rank", "rank_label", "priority", "score",
	"democratic_theme_endorsement", "title", "first_name", "last_name",
	"commune", "department", "insee_code", "endorsement_history",
	"predecessor", "predecessor_mayor", "recent_candidate",
	"recent_year", "email",
	"phone", "town_hall_hours", "postal_address", "postal_code",
	"city", "website",
}

type Status struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Colour string `json:"colour"`
}

var Statuses = []Status{
	{"to_contact", "À contacter", "#e2e8f0"},
	{"email_sent", "Email envoyé", "#bfdbfe"},
	{"letter_sent", "Courrier envoyé", "#c7d2fe"},
	{"to_call_back", "À rappeler", "#fde68a"},
	{"promised", "Promesse de présentation", "#bbf7d0"},
	{"signed", "A signé (publié par le CC)", "#86efac"},
	{"promised_elsewhere", "Déjà promis à un autre candidat", "#fed7aa"},
	{"refused", "Refus", "#fecaca"},
	{"do_not_contact", "Ne plus contacter", "#e5e5e5"},
}

func validStatus(s string) bool {
	for _, x := range Statuses {
		if x.Key == s {
			return true
		}
	}
	return false
}

type Rank struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// The rank drives the message template AND the work order: known endorsers
// are exhausted before writing to mayors with no history.
var Ranks = []Rank{
	{"has_endorsed", "A déjà parrainé un candidat peu médiatisé"},
	{"commune_has_endorsed", "Sa commune l'a déjà fait (maire différent depuis)"},
	{"no_signal", "Aucun signal connu"},
}

func validRank(r string) bool {
	for _, x := range Ranks {
		if x.Key == r {
			return true
		}
	}
	return false
}

// Roles, from widest to narrowest. Coordination sees the whole campaign; a
// team lead only opens access within their own team; a volunteer works.
const (
	RoleCoordination = "coordination"
	RoleLead         = "lead"
	RoleVolunteer    = "volunteer"
	// RoleAdministration lives in the INSTANCE scope, outside any campaign:
	// it approves hosting requests. It reads no campaign data — its scope
	// policies let nothing through for organisation 0.
	RoleAdministration = "administration"
)

// validRole knows ONLY the campaign roles: that is what prevents a
// coordination from crafting an instance administrator by posting a chosen
// role. Administration can only be obtained through bootstrap.
func validRole(r string) bool {
	return r == RoleCoordination || r == RoleLead || r == RoleVolunteer
}

// Accounts without a team form the "national" scope: without this sentinel,
// COALESCE(team_id, NULL) would leave their cards — and their notes — in
// the shared pool, readable by every team.
const NationalTeam = 0
