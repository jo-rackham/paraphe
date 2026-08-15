package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Every setting the service reads, declared ONCE.
//
// Three sources, in decreasing order: a command-line flag, an environment
// variable, then the `server:` block of the configuration file. What none of
// them provides is the default below. That order is the usual one and the
// reason is practical: a flag is what an operator types to override a
// deployment for one run, the environment is what a deployment sets, and the
// file is what a clone carries.
//
// Declared once so that nothing can drift: the flags, the help, the
// documentation and the deployment files all come from this table, and
// TestEveryDeclaredSettingIsRead / the deployment tests refuse a variable
// that no code reads or a setting nobody declared.
type Setting struct {
	// Key: the name in the file's `server:` block. English, like the
	// environment variables: this block is read by whoever runs the service,
	// not by a campaign team.
	Key string
	// Env: PARAPHE_<KEY>, uppercased. Kept explicit rather than derived, so
	// grepping for a variable finds it.
	Env string
	// Flag: the command-line name, always the key with dashes.
	Flag     string
	Default  string
	Help     string
	Required bool
	// NoFile: this setting cannot come from the file layer. Only
	// `config_dir` is in that case, and for a reason that is not a
	// preference: it is what LOCATES the file, so reading it from there
	// would require already knowing it.
	NoFile bool
	// Blankable: an EMPTY value, given explicitly, is a value.
	//
	// Off by default, and that default is the safe one: `PARAPHE_HOST=` in
	// a generated .env would otherwise resolve to an empty listening
	// address, which is every interface rather than the loopback the
	// default names. It is on for `web_dir` alone, where empty means
	// "serve no pages" — the escape hatch of a start that otherwise fails
	// on an unreadable interface, and the shape a developer has before
	// their first `task web-build`.
	Blankable bool
}

var Settings = []Setting{
	{Key: "database_url", Env: "PARAPHE_DATABASE_URL", Flag: "database-url",
		Required: true,
		Help: "PostgreSQL DSN. Example: " +
			"postgresql://paraphe:password@127.0.0.1:5432/paraphe"},
	{Key: "host", Env: "PARAPHE_HOST", Flag: "host", Default: "127.0.0.1",
		Help: "listening address"},
	{Key: "port", Env: "PARAPHE_PORT", Flag: "port", Default: "8047",
		Help: "listening port"},
	{Key: "config_dir", Env: "PARAPHE_CONFIG_DIR", Flag: "config-dir",
		Default: "config", NoFile: true,
		Help: "directory holding campagne.yaml"},
	{Key: "csv", Env: "PARAPHE_CSV", Flag: "csv",
		Default: "out/04_base_complete.csv", Help: "the mayor list to import"},
	{Key: "web_dir", Env: "PARAPHE_WEB_DIR", Flag: "web-dir", Default: "web/dist",
		Blankable: true,
		Help:      "built interface to serve; set empty to answer JSON only"},
	{Key: "base_domain", Env: "PARAPHE_BASE_DOMAIN", Flag: "base-domain",
		Help: "domain of the campaign subdomains; empty means a single campaign"},
	{Key: "org_slug", Env: "PARAPHE_ORG_SLUG", Flag: "org-slug",
		Default: "campaign", Help: "subdomain of the bootstrap campaign"},
	{Key: "source_url", Env: "PARAPHE_SOURCE_URL", Flag: "source-url",
		Help: "public repository URL, shown in the footer"},
	{Key: "secret_key", Env: "PARAPHE_SECRET_KEY", Flag: "secret-key",
		Help: "session signing secret; drawn at random and stored if unset"},
	{Key: "admin_email", Env: "PARAPHE_ADMIN_EMAIL", Flag: "admin-email",
		Help: "coordination account, created or refreshed at every start"},
	{Key: "admin_password", Env: "PARAPHE_ADMIN_PASSWORD", Flag: "admin-password",
		Help: "coordination password"},
	{Key: "admin_name", Env: "PARAPHE_ADMIN_NAME", Flag: "admin-name",
		Default: "Coordination", Help: "coordination display name"},
	{Key: "instance_admin_email", Env: "PARAPHE_INSTANCE_ADMIN_EMAIL",
		Flag: "instance-admin-email",
		Help: "instance administration, required in multi-campaign mode"},
	{Key: "instance_admin_password", Env: "PARAPHE_INSTANCE_ADMIN_PASSWORD",
		Flag: "instance-admin-password", Help: "instance administration password"},
	{Key: "instance_admin_name", Env: "PARAPHE_INSTANCE_ADMIN_NAME",
		Flag: "instance-admin-name", Default: "Administration",
		Help: "instance administration display name"},
	{Key: "batch_size", Env: "PARAPHE_BATCH_SIZE", Flag: "batch-size",
		Help: "mayors handed out per batch; overrides the campaign file"},
	{Key: "log_level", Env: "PARAPHE_LOG_LEVEL", Flag: "log-level",
		Default: "info", Help: "debug, info, warn or error"},
}

// fromFile: the `server:` block, read once at startup. Empty until then, so
// a caller before CheckSettings sees the environment and the defaults —
// which is exactly what a test wants, and what a tool that never reads the
// file gets.
var fromFile = map[string]string{}

// flagValues: what the command line carried, and which of those a caller
// actually typed. `flag` cannot tell "left at zero" from "set to empty" on
// its own, and a flag nobody typed must not beat the environment.
var (
	flagValues = map[string]*string{}
	flagTyped  = map[string]bool{}
)

// DeclareFlags registers one flag per setting. Called before flag.Parse.
func DeclareFlags(fs *flag.FlagSet) {
	for _, s := range Settings {
		flagValues[s.Key] = fs.String(s.Flag, "", s.Help)
	}
}

// AdoptFlags records which flags the caller actually typed. Called right
// after flag.Parse, and separate from CheckSettings on purpose: `config_dir`
// has to resolve — flag included — BEFORE the file layer can be read, since
// it is what says where that file is.
func AdoptFlags(fs *flag.FlagSet) {
	fs.Visit(func(f *flag.Flag) { flagTyped[f.Name] = true })
}

// Get resolves one setting: the flag if it was typed, then the environment,
// then the file, then the default. Resolved on each call rather than frozen,
// because none of those sources changes while the service runs and freezing
// them would only add an order to get wrong.
func Get(key string) string {
	for _, s := range Settings {
		if s.Key != key {
			continue
		}
		// A layer that was TOUCHED answers, empty included, when the setting
		// says an empty value means something. Otherwise an empty layer
		// falls through — which is what makes `PARAPHE_BATCH_SIZE=` in a
		// generated .env mean "leave it alone" rather than "zero".
		if flagTyped[s.Flag] {
			v := strings.TrimSpace(*flagValues[s.Key])
			if v != "" || s.Blankable {
				return v
			}
		}
		if raw, set := os.LookupEnv(s.Env); set {
			v := strings.TrimSpace(raw)
			if v != "" || s.Blankable {
				return v
			}
		}
		// The file layer counts as touched the same way, and it has to: an
		// operator writing `web_dir: ""` under `server:` is doing exactly
		// what the flag and the variable let them do, and falling through to
		// the default there would make Blankable true of two layers out of
		// three — a distinction nothing in the documentation draws.
		if raw, set := fromFile[s.Key]; set {
			v := strings.TrimSpace(raw)
			if v != "" || s.Blankable {
				return v
			}
		}
		return s.Default
	}
	// An undeclared key would read as "unset" for ever, which is the one
	// answer a caller cannot act on.
	panic("undeclared setting " + key + ": add it to Settings")
}

// CheckSettings reads the file layer and refuses a configuration that cannot
// work: a required setting nobody provides, or a key in the file that no
// code reads. Called once, at startup, after AdoptFlags.
func CheckSettings(dir string) error {
	block, err := serverBlock(dir)
	if err != nil {
		return err
	}
	fromFile = block
	var missing []string
	for _, s := range Settings {
		if s.Required && Get(s.Key) == "" {
			missing = append(missing, fmt.Sprintf("%s (flag -%s, or %s: under "+
				"`server:` in the configuration) — %s", s.Env, s.Flag, s.Key, s.Help))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing configuration:\n  %s",
			strings.Join(missing, "\n  "))
	}
	return nil
}

// serverBlock reads the `server:` block of campagne.yaml, then of
// campagne.local.yaml, the second overriding the first — the same two layers
// the campaign values use, so an operator has one file to fill and it is the
// git-ignored one.
func serverBlock(dir string) (map[string]string, error) {
	out := map[string]string{}
	for _, name := range []string{"campagne.yaml", "campagne.local.yaml"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue // absent is the normal case: the environment provides
		}
		var file struct {
			Server map[string]string `yaml:"server"`
		}
		if err := yaml.Unmarshal(raw, &file); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(dir, name), err)
		}
		for k, v := range file.Server {
			out[k] = v
		}
	}
	// A key nobody declared is a typo that would sit there doing nothing.
	declared := map[string]Setting{}
	for _, s := range Settings {
		declared[s.Key] = s
	}
	for k := range out {
		s, ok := declared[k]
		if !ok {
			return nil, fmt.Errorf("unknown setting %q in the server block: "+
				"nothing reads it. Expected one of %s", k, declaredKeys())
		}
		// Said rather than ignored: honouring it would mean this file had
		// already been found somewhere else, so the value would name a
		// directory nobody read it from.
		if s.NoFile {
			return nil, fmt.Errorf("%q cannot be set in the server block: it "+
				"is what says where this file is. Use -%s, or %s",
				k, s.Flag, s.Env)
		}
	}
	return out, nil
}

func declaredKeys() string {
	keys := make([]string, 0, len(Settings))
	for _, s := range Settings {
		keys = append(keys, s.Key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
