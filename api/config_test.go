package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const shippedTemplate = `
campagne:
  candidat: "Prénom NOM"
  candidat_description: "candidat(e) [courant / démarche], [profession]"
  candidat_description_longue: "Je suis [qui]."
  signataire: "Prénom Nom"
  signataire_qualite: "équipe de campagne de [candidat]"
  contact_tel: "06 00 00 00 00"
  contact_email: "contact@exemple.fr"
  site: "https://exemple.fr"
  ville_envoi: "Ville"

app:
  taille_lot: 10
`

func configDir(t *testing.T, files map[string]string) string {
	t.Helper()
	d := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(d, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// The shipped template must be detected key by key: it is what triggers the
// warning banner, and it is what kept "Prénom NOM" from going out to 1,934
// mayors.
func TestTemplateDetected(t *testing.T) {
	cfg, err := LoadConfig(configDir(t, map[string]string{
		"campagne.yaml": shippedTemplate}))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Unfilled) != len(CampaignKeys) {
		t.Errorf("template detected on %d keys instead of %d: %v",
			len(cfg.Unfilled), len(CampaignKeys), cfg.Unfilled)
	}
}

func TestLocalOverrideThenEnvironment(t *testing.T) {
	d := configDir(t, map[string]string{
		"campagne.yaml": shippedTemplate,
		"campagne.local.yaml": `
campagne:
  candidat: "Camille Réel"
  contact_email: "camille@exemple.org"
  site: "https://camille.example"
`,
	})
	t.Setenv("PARAPHE_CONTACT_EMAIL", "team@exemple.org")
	t.Setenv("PARAPHE_BATCH_SIZE", "25")

	cfg, err := LoadConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Campaign["candidat"] != "Camille Réel" {
		t.Errorf("local override ignored: %q", cfg.Campaign["candidat"])
	}
	if cfg.Campaign["contact_email"] != "team@exemple.org" {
		t.Errorf("the environment does not take precedence: %q", cfg.Campaign["contact_email"])
	}
	if cfg.BatchSize != 25 {
		t.Errorf("batch_size = %d instead of 25", cfg.BatchSize)
	}
	// campagne.yaml is untouched: that is what keeps a real value out of the
	// repository
	raw, err := os.ReadFile(filepath.Join(d, "campagne.yaml"))
	if err != nil || string(raw) != shippedTemplate {
		t.Error("the versioned template was modified")
	}
}

func TestIncompleteConfigFails(t *testing.T) {
	_, err := LoadConfig(configDir(t, map[string]string{
		"campagne.yaml": "campagne:\n  candidat: \"Camille\"\napp:\n  taille_lot: 10\n"}))
	if err == nil {
		t.Fatal("a configuration missing eight keys was accepted")
	}
}

// A missing directory must SAY SO: without that, a badly built image starts
// with placeholders and nobody notices before the mailing.
func TestMissingConfigSaysSo(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "nowhere"))
	if err == nil {
		t.Fatal("a nonexistent configuration directory was accepted")
	}
	if !containsText(err.Error(), "missing") {
		t.Errorf("the message does not say the file is missing: %v", err)
	}
}

func TestBatchSizeIntegerAndPositive(t *testing.T) {
	d := configDir(t, map[string]string{"campagne.yaml": shippedTemplate})
	t.Setenv("PARAPHE_BATCH_SIZE", "zéro")
	if _, err := LoadConfig(d); err == nil {
		t.Error("PARAPHE_BATCH_SIZE=zéro accepted")
	}
	t.Setenv("PARAPHE_BATCH_SIZE", "0")
	if _, err := LoadConfig(d); err == nil {
		t.Error("a batch of 0 mayors accepted")
	}
}

// The example secrets are published in the repository: accepting them would
// ship an instance whose sessions anyone can forge.
func TestExampleSecretsRefused(t *testing.T) {
	for _, v := range []string{"à-changer-absolument", "CHANGEME", "à-générer"} {
		if _, err := UsableSecret(v, "PARAPHE_SECRET_KEY"); err == nil {
			t.Errorf("example secret accepted: %q", v)
		}
	}
	got, err := UsableSecret("  vrai-secret-tiré-au-hasard  ", "PARAPHE_SECRET_KEY")
	if err != nil || got != "vrai-secret-tiré-au-hasard" {
		t.Errorf("legitimate secret refused or badly trimmed: %q %v", got, err)
	}
}

// Refusing the five published values left every OTHER short string through,
// and a session key is only worth its length: one captured cookie, an offline
// search, and any account can be minted. The floor is checked before the pool
// is ever touched, hence the nil one here.
func TestShortSessionSecretsRefused(t *testing.T) {
	for _, v := range []string{"x", "paraphe", "0123456789abcdef0123456789abcde"} {
		t.Setenv("PARAPHE_SECRET_KEY", v)
		if _, err := SessionSecret(context.Background(), nil); err == nil {
			t.Errorf("a %d-byte session key was accepted: %q", len(v), v)
		}
	}
	// exactly at the floor, and not a template value: accepted, and the pool
	// is never reached
	t.Setenv("PARAPHE_SECRET_KEY", "0123456789abcdef0123456789abcdef")
	key, err := SessionSecret(context.Background(), nil)
	if err != nil || len(key) != 32 {
		t.Errorf("a key at the floor was refused: %d %v", len(key), err)
	}
}

func containsText(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// The guard exists twice: here, arming the server's refusal, and in
// noyau/messages.ts, arming the banner the volunteer sees. Two
// implementations means the WEAKER one is the one that counts — and Go was
// the weaker, blind to "{candidat}", to a decomposed accent and to a
// zero-width space. The cases live in one file both languages read, so a
// case added on either side must be answered on both.
func TestUnfilledKeysAgreesWithTheEngine(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "noyau", "gabarit-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Value    string `json:"value"`
			Unfilled bool   `json:"unfilled"`
			Why      string `json:"why"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("no shared case: the two implementations are held by nothing")
	}
	for _, c := range file.Cases {
		// one key at a time, the others filled: the case under test is the
		// only thing that may appear in the answer
		campaign := map[string]string{}
		for _, k := range CampaignKeys {
			campaign[k] = "valeur réelle et remplie"
		}
		campaign["signataire_qualite"] = c.Value
		got := slices.Contains(UnfilledKeys(campaign), "signataire_qualite")
		if got != c.Unfilled {
			t.Errorf("%q (%s): unfilled=%v, expected %v",
				c.Value, c.Why, got, c.Unfilled)
		}
	}
}

// Go cannot import the TypeScript table, so it keeps its own copy — and a
// copy drifts. noyau/campaign-env.json is the referee both sides answer to:
// outils/config.test.ts checks CAMPAIGN_ENV against the same file.
func TestCampaignEnvMatchesTheSharedTable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "noyau", "campaign-env.json"))
	if err != nil {
		t.Fatal(err)
	}
	var shared map[string]any
	if err := json.Unmarshal(raw, &shared); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{}
	for k, v := range shared {
		if strings.HasPrefix(k, "_") {
			continue
		}
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s: expected a variable name, got %T", k, v)
		}
		expected[k] = s
	}
	if len(expected) == 0 {
		t.Fatal("the shared table is empty: this test would prove nothing")
	}
	if !reflect.DeepEqual(CampaignEnv, expected) {
		t.Errorf("CampaignEnv and campaign-env.json disagree:\n Go:     %v\n shared: %v",
			CampaignEnv, expected)
	}
	for _, k := range CampaignKeys {
		if CampaignEnv[k] == "" {
			t.Errorf("campaign key %q overrides through no variable", k)
		}
	}
}

// The same referee, for the list that decides whether a campaign is told it
// is unconfigured. A key dropped from one side's copy would stop blocking —
// or start blocking — on that side alone, and the banner and the mass
// mailing's refusal read one side while the interface reads the other.
func TestOptionalCampaignKeysMatchTheSharedList(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "noyau", "campaign-optional.json"))
	if err != nil {
		t.Fatal(err)
	}
	var shared struct {
		Optional []string `json:"optional"`
	}
	if err := json.Unmarshal(raw, &shared); err != nil {
		t.Fatal(err)
	}
	if len(shared.Optional) == 0 {
		t.Fatal("the shared list is empty: this test would prove nothing")
	}
	expected := map[string]bool{}
	for _, k := range shared.Optional {
		expected[k] = true
		if !slices.Contains(CampaignKeys, k) {
			t.Errorf("campaign-optional.json names %q, which is no campaign key", k)
		}
	}
	if !reflect.DeepEqual(optionalCampaignKeys, expected) {
		t.Errorf("optionalCampaignKeys and campaign-optional.json disagree:"+
			"\n Go:     %v\n shared: %v", optionalCampaignKeys, expected)
	}
}

// The rule the list exists for, asserted on both halves: a campaign that
// gives no telephone, no contact address, no website and no sending town is
// configured; one that left the shipped template in any of them is NOT, and
// that is what would reach five hundred mayors verbatim.
func TestAnEmptyOptionalKeyIsFilledButATemplateOneIsNot(t *testing.T) {
	campaign := map[string]string{}
	for _, k := range CampaignKeys {
		campaign[k] = "une valeur réelle"
	}
	for _, k := range []string{"contact_email", "contact_tel", "site",
		"ville_envoi"} {
		campaign[k] = ""
	}
	if got := UnfilledKeys(campaign); len(got) != 0 {
		t.Errorf("UnfilledKeys = %v: leaving the campaign's own contact "+
			"details empty is a choice, not a misconfiguration", got)
	}
	campaign["contact_tel"] = "06 00 00 00 00"
	if got := UnfilledKeys(campaign); !slices.Contains(got, "contact_tel") {
		t.Errorf("UnfilledKeys = %v: the shipped number is not an empty "+
			"field, it is a number that goes out verbatim", got)
	}
	campaign["contact_tel"] = ""
	campaign["signataire"] = ""
	if got := UnfilledKeys(campaign); !slices.Contains(got, "signataire") {
		t.Errorf("UnfilledKeys = %v: a required key stayed optional", got)
	}
}
