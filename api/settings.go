package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	{Key: "browser_web_dir", Env: "PARAPHE_BROWSER_WEB_DIR",
		Flag: "browser-web-dir",
		Help: "build of the account-less browser version, served under " +
			"/navigateur/ without the mode marker; empty serves none"},
	{Key: "base_domain", Env: "PARAPHE_BASE_DOMAIN", Flag: "base-domain",
		Help: "domain of the campaign subdomains; empty means a single campaign"},
	{Key: "org_slug", Env: "PARAPHE_ORG_SLUG", Flag: "org-slug",
		Default: "campagne", Help: "subdomain of the bootstrap campaign"},
	{Key: "source_url", Env: "PARAPHE_SOURCE_URL", Flag: "source-url",
		Help: "public repository URL, shown in the footer"},
	{Key: "browser_version_url", Env: "PARAPHE_BROWSER_VERSION_URL",
		Flag: "browser-version-url",
		Help: "public URL of the account-less browser version, offered on " +
			"the instance home page; empty shows no link"},
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
	{Key: "valkey_url", Env: "PARAPHE_VALKEY_URL", Flag: "valkey-url",
		Help: "shared rate-limit counters: valkey://host:6379, or " +
			"valkey+sentinel://h1:26379,h2:26379,h3:26379/master-name; " +
			"empty holds them in process memory (fine for one instance)"},
	{Key: "valkey_password", Env: "PARAPHE_VALKEY_PASSWORD", Flag: "valkey-password",
		Help: "password for Valkey and its sentinels; never put it in valkey_url"},
	{Key: "trusted_proxies", Env: "PARAPHE_TRUSTED_PROXIES", Flag: "trusted-proxies",
		Help: "CIDRs whose X-Forwarded-For is believed (the TLS proxy, the " +
			"ingress); empty attributes every request to its TCP peer"},
	// The object store holding the campaign logos. All six empty is the
	// normal state of a developer's instance and of the test suite: the
	// logo feature then says it is unavailable instead of failing.
	{Key: "media_endpoint", Env: "PARAPHE_MEDIA_ENDPOINT", Flag: "media-endpoint",
		Help: "S3 API of the object store holding the campaign logos: " +
			"http://garage:3900, or a provider's endpoint; empty offers no logo"},
	{Key: "media_bucket", Env: "PARAPHE_MEDIA_BUCKET", Flag: "media-bucket",
		Help: "bucket the logos are written to"},
	{Key: "media_region", Env: "PARAPHE_MEDIA_REGION", Flag: "media-region",
		Default: "garage", Help: "S3 region of that endpoint"},
	{Key: "media_access_key", Env: "PARAPHE_MEDIA_ACCESS_KEY",
		Flag: "media-access-key", Help: "S3 access key"},
	{Key: "media_secret_key", Env: "PARAPHE_MEDIA_SECRET_KEY",
		Flag: "media-secret-key", Help: "S3 secret key"},
	// The origin the BROWSER fetches a logo from — not the endpoint the
	// application writes to. It is what the Content-Security-Policy has to
	// name, so a wrong value here shows as an image the browser refuses to
	// load, in the console, and nowhere else.
	{Key: "media_public_url", Env: "PARAPHE_MEDIA_PUBLIC_URL",
		Flag: "media-public-url",
		Help: "public origin serving the bucket, e.g. https://media.paraphe.org"},
	{Key: "smtp_url", Env: "PARAPHE_SMTP_URL", Flag: "smtp-url",
		Help: "relay that carries the sign-in links: smtp://user@host:587 " +
			"(STARTTLS) or smtps://user@host:465; empty sends no email at all"},
	{Key: "smtp_password", Env: "PARAPHE_SMTP_PASSWORD", Flag: "smtp-password",
		Help: "password of the SMTP relay; never put it in smtp_url"},
	{Key: "mail_from", Env: "PARAPHE_MAIL_FROM", Flag: "mail-from",
		Help: "sender of those emails, `Campagne <contact@exemple.fr>`; " +
			"required as soon as smtp_url is set"},
	{Key: "public_url", Env: "PARAPHE_PUBLIC_URL", Flag: "public-url",
		Help: "origin the sign-in links point at (https://paraphe.org); " +
			"required with smtp_url — a link is never built from a Host header"},
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
		slices.Sort(missing)
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
	slices.Sort(keys)
	return strings.Join(keys, ", ")
}
