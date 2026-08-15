package main

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// One instance hosts several campaigns. Each campaign is an "organisation":
// it carries its configuration (the 9 CampaignKeys plus the batch size), its
// local teams, its accounts and its work. The list of mayors, however, stays
// SHARED and read-only — it is public data, identical for everyone, and
// duplicating it per organisation would copy 34,826 rows for nothing.
//
// The organisation is derived from the subdomain: <campaign>.example.org. No
// identifier therefore travels in the application URLs, and an instance with
// no base domain remains single-campaign, exactly as before.

// Sentinel scopes. These are organisation identifiers that cannot exist in
// the database (PostgreSQL identities start at 1), which tells them apart
// from any real campaign without an extra column.
const (
	// OrgInstance: the domain apex — public landing page, campaign requests,
	// moderation. Sees NO work row at all: a walled query bound to 0
	// matches nothing, deliberately — the notes carry people's names.
	OrgInstance = 0
	// OrgMaintenance: import and migrations, which must traverse every
	// organisation. Never reachable over HTTP: resolution only returns
	// identifiers read from the database (≥ 1) or OrgInstance.
	OrgMaintenance = -1
)

// defaultBatchSize: how many mayors "take a batch" assigns in a campaign
// created from a hosting request. Coordination adjusts it afterwards.
const defaultBatchSize = 10

// Organisation states: a suspended organisation still exists (its work is
// intact) but nobody can sign in to it.
const (
	OrgActive    = "active"
	OrgSuspended = "suspended"
)

// Org: a hosted campaign.
type Org struct {
	ID        int               `json:"id"`
	Slug      string            `json:"slug"`
	Name      string            `json:"name"`
	Campaign  map[string]string `json:"campaign"`
	BatchSize int               `json:"batch_size"`
	State     string            `json:"state"`
	CreatedAt string            `json:"created_at"`
	// Listed: whether the apex directory shows this campaign — chosen on
	// the hosting request, adjustable by coordination
	Listed bool `json:"listed"`
}

// BaseDomain: the domain under which campaigns receive their subdomain.
// Empty = SINGLE-CAMPAIGN instance, where every host designates the
// bootstrap campaign — the historical behaviour, and still the default: a
// small campaign must not have to set up a wildcard DNS to use the
// application.
func BaseDomain() string {
	return normaliseHost(Get("base_domain"))
}

// HostScope: what a Host header designates.
type HostScope struct {
	// Instance: the apex (example.org or www.example.org) — no campaign.
	Instance bool
	// Slug: the campaign subdomain, empty when Instance.
	Slug string
}

// ScopeOfHost reads the subdomain out of a Host header.
//
// The port is stripped (Host carries it whenever it is non-standard), the
// case normalised (a Host is case-insensitive) and the trailing dot of an
// absolute name removed — "campaign.example.org." is the SAME name as
// "campaign.example.org", and letting it through would produce a 404 nobody
// could explain.
//
// The second return value is false when the host does not belong to the base
// domain: that is a 404, not a silent fallback onto some arbitrary campaign.
func ScopeOfHost(host, base string) (HostScope, bool) {
	h := normaliseHost(host)
	b := normaliseHost(base)
	if b == "" || h == "" {
		return HostScope{}, false
	}
	if h == b || h == "www."+b {
		return HostScope{Instance: true}, true
	}
	suffix := "." + b
	if !strings.HasSuffix(h, suffix) {
		return HostScope{}, false
	}
	slug := strings.TrimSuffix(h, suffix)
	// One level only: "a.b.example.org" is not the campaign "a.b". Without
	// this refusal, a wildcard certificate (which only covers one level)
	// would not match anyway, and the error would surface as a browser
	// security warning instead of a readable 404.
	if slug == "" || strings.Contains(slug, ".") || !ValidSlug(slug) {
		return HostScope{}, false
	}
	return HostScope{Slug: slug}, true
}

func normaliseHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	// IPv6 literal: [::1]:8047. Campaigns are not served by IP, but the
	// splitting must not build a slug out of the colons.
	if i := strings.LastIndex(h, "]"); i >= 0 {
		h = h[:i+1]
	} else if i := strings.LastIndex(h, ":"); i >= 0 {
		h = h[:i]
	}
	return strings.TrimSuffix(h, ".")
}

// ValidSlug: what can serve as a DNS label, hence as a subdomain.
// Deliberately stricter than the RFC (no uppercase, no leading or trailing
// dash): the slug is typed into a public form, and an exotic label would
// produce a hostname nobody can type.
func ValidSlug(s string) bool {
	if len(s) < 2 || len(s) > 63 {
		return false
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return !reservedSlug(s)
}

// Slugs the instance keeps for itself: they designate infrastructure, and a
// campaign grabbing one would hijack a name the operators use. "www" is
// already treated as the apex above.
var reservedSlugs = map[string]bool{
	"www": true, "api": true, "admin": true, "mail": true, "smtp": true,
	"imap": true, "ns1": true, "ns2": true, "static": true, "cdn": true,
	"assets": true, "status": true, "monitoring": true, "grafana": true,
	"paraphe": true, "instance": true, "demande": true, "demandes": true,
}

func reservedSlug(s string) bool { return reservedSlugs[s] }

// UnfilledKeys: the campaign keys still holding a template value. The app
// stays explorable in that state, but every page says so and the mass
// mailing refuses to run — otherwise "Prénom NOM" goes out to thousands of
// mayors.
// It must decide EXACTLY like unfilledKeys() of noyau/messages.ts: this
// one arms the server's refusal, that one the banner the volunteer sees.
// The weaker of the two is the one that counts — a template value one side
// misses ("{candidat}", a decomposed accent, a zero-width space) is a
// template value that goes out. TestUnfilledKeysAgreesWithTheEngine holds
// them together.
func UnfilledKeys(campaign map[string]string) []string {
	unfilled := []string{}
	for _, k := range CampaignKeys {
		if templateValue(campaign[k]) {
			unfilled = append(unfilled, k)
		}
	}
	return unfilled
}

// templateValue: is this ONE value still the shipped template's — empty, a
// known example, or a {placeholder} left in place? The campaign keys are
// judged by it, and so is the public name the apex directory advertises.
func templateValue(s string) bool {
	v := normaliseForTemplateCheck(s)
	return v == "" || templateValues[strings.ToLower(v)] || rxPlaceholder.MatchString(v)
}

// NFC, without zero-width characters, spaces collapsed: a "é" pasted from
// a PDF in decomposed form, or an invisible U+200B, made the shipped
// template pass for a filled value.
func normaliseForTemplateCheck(s string) string {
	v := norm.NFC.String(s)
	v = rxInvisible.ReplaceAllString(v, "")
	return strings.TrimSpace(rxSpaces.ReplaceAllString(v, " "))
}
