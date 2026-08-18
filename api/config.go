package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Campaign configuration. Three layers, least to most specific:
// config/campagne.yaml (versioned template), config/campagne.local.yaml
// (real values, git-ignored), PARAPHE_* environment variables. The same file
// feeds the mass mailing and the application, and filling the configuration
// never modifies a git-tracked file.

// Keys that fill the {placeholders} of the message templates. The keys are
// French on purpose: the campaign team edits the templates themselves.
var CampaignKeys = []string{
	"candidat", "candidat_description", "candidat_description_longue",
	"signataire", "signataire_qualite", "contact_tel", "contact_email",
	"site", "ville_envoi",
}

// The environment variable that overrides each key. Not derivable by
// uppercasing: the keys are French, the variables are English because an
// operator reads them. noyau/campaign-env.json holds the same table and the
// tests of both languages check theirs against it.
var CampaignEnv = map[string]string{
	"candidat":                    "PARAPHE_CANDIDATE",
	"candidat_description":        "PARAPHE_CANDIDATE_DESCRIPTION",
	"candidat_description_longue": "PARAPHE_CANDIDATE_DESCRIPTION_LONG",
	"signataire":                  "PARAPHE_SIGNATORY",
	"signataire_qualite":          "PARAPHE_SIGNATORY_ROLE",
	"contact_tel":                 "PARAPHE_CONTACT_PHONE",
	"contact_email":               "PARAPHE_CONTACT_EMAIL",
	"site":                        "PARAPHE_SITE",
	"ville_envoi":                 "PARAPHE_SENDING_CITY",
}

// Keys a campaign may leave EMPTY without being told it is unconfigured.
// noyau/campaign-optional.json is the referee both languages answer to, and
// it carries the reasoning; noyau/messages.ts holds the other copy.
var optionalCampaignKeys = map[string]bool{
	"contact_tel": true, "site": true, "ville_envoi": true,
}

// Values of the shipped template: letting them through would send
// "Prénom NOM" to thousands of mayors.
var templateValues = map[string]bool{
	"prénom nom": true, "ville": true, "06 00 00 00 00": true,
	"contact@exemple.fr": true, "https://exemple.fr": true,
}

// Example secrets: published in the repository, hence known to everyone.
// Accepting them would ship an instance where anyone can forge a session or
// take over the coordination account.
var templateSecrets = map[string]bool{
	"à-changer-absolument": true, "à-generer": true, "à-générer": true,
	"changeme": true, "à-remplir": true,
}

// The template's three placeholder syntaxes. "{candidat}" matters as much
// as "[qui]": writing `signataire_qualite = "équipe de campagne de
// {candidat}"` is the natural move, and the string went out verbatim.
var rxPlaceholder = regexp.MustCompile(`\[[^\]]+\]|\{[^}]+\}|<[^>]+>`)

// Zero-width characters and the byte-order mark: invisible, and enough to
// make the shipped template pass for a filled value.
var rxInvisible = regexp.MustCompile(`[\x{200b}-\x{200d}\x{feff}]`)

// NOT `\s+`: RE2 spells it [\t\n\f\r ], while the JavaScript engine
// this must agree with also folds every Unicode space. A single
// non-breaking space inside "Prénom NOM" made Go declare the campaign
// filled while the engine still printed the template to mayors.
var rxSpaces = regexp.MustCompile(
	"[\\t\\n\\v\\f\\r \\x{0085}\\x{00a0}\\x{1680}\\x{2000}-\\x{200a}" +
		"\\x{2028}\\x{2029}\\x{202f}\\x{205f}\\x{3000}]+")

type Config struct {
	Campaign  map[string]string `json:"campaign"`
	BatchSize int               `json:"batch_size"`
	// keys still at their template value: the app stays explorable, but
	// every page says so
	Unfilled  []string `json:"unfilled"`
	SourceURL string   `json:"source_url"`
	// Overrides: the campaign keys given by a PARAPHE_* variable, as
	// opposed to those read from the file. Only these may reapply
	// themselves over what coordination edited in the application.
	Overrides map[string]string `json:"-"`
	// BatchSizeOverride: nil unless PARAPHE_BATCH_SIZE is set. The file
	// value seeds a new campaign; it must not overwrite what coordination
	// set in the application.
	BatchSizeOverride *int `json:"-"`
	// Complete: the configuration describes a whole campaign, hence can
	// bootstrap an organisation. False only on a pristine multi-campaign
	// instance, where campaigns are born from approved hosting requests.
	Complete bool `json:"-"`
}

type configFile struct {
	Campaign map[string]string `yaml:"campagne"`
	App      struct {
		BatchSize *int `yaml:"taille_lot"`
	} `yaml:"app"`
}

// LoadConfig reads the three layers and refuses to return an incomplete
// configuration: a template referencing a missing key would fail at message
// generation, on the volunteer's screen, with no way to tell why.
func LoadConfig(dir string) (*Config, error) {
	cfg := &Config{
		Campaign: map[string]string{}, Overrides: map[string]string{}, BatchSize: 0,
	}

	base := filepath.Join(dir, "campagne.yaml")
	baseMissing := false
	if err := merge(cfg, base); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		baseMissing = true
	}
	local := filepath.Join(dir, "campagne.local.yaml")
	if err := merge(cfg, local); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	for _, k := range CampaignKeys {
		if v := strings.TrimSpace(os.Getenv(CampaignEnv[k])); v != "" {
			cfg.Campaign[k] = v
			// Remembered apart: on restart these are the ONLY keys allowed
			// to overwrite what coordination typed into the application.
			cfg.Overrides[k] = v
		}
	}
	if v := strings.TrimSpace(Get("batch_size")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("PARAPHE_BATCH_SIZE = %q: expected an integer", v)
		}
		cfg.BatchSize = n
		cfg.BatchSizeOverride = &n
	}
	cfg.SourceURL = strings.TrimSpace(Get("source_url"))

	// The base domain is a DNS name, and nothing checked it. `http://x.org`
	// lost everything after its first colon and became the domain `http`;
	// `x.org/path` and `.x.org` kept their punctuation. In all three the
	// process starts, /health/db answers 200, Kubernetes marks the pod
	// ready, traffic arrives — and every legitimate Host matches no campaign
	// and answers 404. A blank screen behind a green probe is the failure
	// this application refuses everywhere else it can.
	if raw := strings.TrimSpace(Get("base_domain")); raw != "" {
		if err := validBaseDomain(raw); err != nil {
			return nil, err
		}
	}

	// Same posture, one setting over: this one becomes an href on the home
	// page and on every campaign's sign-in screen, and the interface drops
	// what is not http(s) rather than render it. A wrong value would then be
	// indistinguishable from "no browser version here".
	if raw := strings.TrimSpace(Get("browser_version_url")); raw != "" {
		if err := validBrowserVersionURL(raw); err != nil {
			return nil, err
		}
	}

	var missing []string
	for _, k := range CampaignKeys {
		if strings.TrimSpace(cfg.Campaign[k]) == "" {
			missing = append(missing, k)
		}
	}
	if cfg.BatchSize < 1 {
		missing = append(missing, "taille_lot (integer ≥ 1)")
	}
	cfg.Complete = len(missing) == 0
	if !cfg.Complete {
		// On a SINGLE-CAMPAIGN instance, an incomplete configuration is a
		// failure: there is nothing else to serve, and a template referencing
		// a missing key would fail on the volunteer's screen with no way to
		// tell why. On a multi-campaign instance, this is the normal state at
		// first start — campaigns arrive through the hosting request form.
		if BaseDomain() == "" {
			where := fmt.Sprintf("%s or PARAPHE_* variables", base)
			if baseMissing {
				where = fmt.Sprintf("%s is missing, and the PARAPHE_* variables "+
					"do not provide them", base)
			}
			return nil, fmt.Errorf("incomplete configuration (%s): %s",
				where, strings.Join(missing, ", "))
		}
		slog.Warn("no bootstrap campaign: the instance will only host "+
			"campaigns approved through the hosting request form",
			"missing", strings.Join(missing, ", "))
	}

	cfg.Unfilled = UnfilledKeys(cfg.Campaign)
	return cfg, nil
}

func merge(cfg *Config, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var f configFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for k, v := range f.Campaign {
		cfg.Campaign[k] = v
	}
	if f.App.BatchSize != nil {
		cfg.BatchSize = *f.App.BatchSize
	}
	return nil
}

// UsableSecret refuses a secret left at the repository's example value.
func UsableSecret(value, what string) (string, error) {
	v := strings.TrimSpace(value)
	if templateSecrets[strings.ToLower(v)] {
		return "", fmt.Errorf("%s still holds the repository's example value "+
			"(%q), which is public. Generate one: openssl rand -hex 32", what, v)
	}
	return v, nil
}
