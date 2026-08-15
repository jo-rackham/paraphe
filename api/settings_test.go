package main

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The settings table promises an order — flag > environment > file > default —
// and the suite that existed did not prove it: every test ran with the
// environment set and no flag and no file, so `Get` returning the environment
// proved only that ONE layer worked. A resolution that had silently dropped
// the flag layer, or read the file before the environment, would have stayed
// green.
//
// So each layer is asserted where it must WIN over the one below it, and the
// state each test touches is restored: `flagTyped`, `flagValues` and
// `fromFile` are package-level, and a test that leaks into the next one would
// certify the wrong order.

func saveSettingsState(t *testing.T) {
	t.Helper()
	oldFile, oldValues, oldTyped := fromFile, flagValues, flagTyped
	t.Cleanup(func() { fromFile, flagValues, flagTyped = oldFile, oldValues, oldTyped })
	fromFile = map[string]string{}
	flagValues = map[string]*string{}
	flagTyped = map[string]bool{}
	// CheckSettings reads the file layer AND refuses a missing requirement,
	// so a test about precedence cannot reach its own assertion without a
	// DSN. Given here rather than in each test: what these tests are about is
	// the order, and a bare `t.Fatalf(err)` on an unrelated requirement is a
	// failure that proves nothing. The test that IS about the requirement
	// clears it again.
	t.Setenv("PARAPHE_DATABASE_URL", "postgresql://settings:test@127.0.0.1:5432/unused")
}

// writeServerBlock builds a configuration directory holding only what the
// test needs. `campagne.yaml` is the versioned template layer.
func writeServerBlock(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "campagne.yaml"),
		[]byte(body), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	return dir
}

func TestSettingPrecedence(t *testing.T) {
	// `host` carries a default, so all four layers are observable on one key.
	const key, envName, flagName = "host", "PARAPHE_HOST", "host"

	t.Run("the default answers when nothing else does", func(t *testing.T) {
		saveSettingsState(t)
		t.Setenv(envName, "")
		if got := Get(key); got != "127.0.0.1" {
			t.Fatalf("Get(%q) = %q, want the declared default 127.0.0.1", key, got)
		}
	})

	t.Run("the file beats the default", func(t *testing.T) {
		saveSettingsState(t)
		t.Setenv(envName, "")
		dir := writeServerBlock(t, "server:\n  host: from-file\n")
		if err := CheckSettings(dir); err != nil {
			t.Fatalf("CheckSettings: %v", err)
		}
		if got := Get(key); got != "from-file" {
			t.Fatalf("Get(%q) = %q, want from-file: the file layer never "+
				"reached the resolution", key, got)
		}
	})

	t.Run("the environment beats the file", func(t *testing.T) {
		saveSettingsState(t)
		// The DSN comes from the FILE here, not from the environment: with
		// the environment layer removed, a DSN that only the environment
		// carried would make CheckSettings refuse first, and this test would
		// go red on a missing requirement instead of on the order it exists
		// to prove.
		dir := writeServerBlock(t, "server:\n  host: from-file\n"+
			"  database_url: postgresql://settings:test@127.0.0.1:5432/unused\n")
		if err := CheckSettings(dir); err != nil {
			t.Fatalf("CheckSettings: %v", err)
		}
		t.Setenv(envName, "from-env")
		if got := Get(key); got != "from-env" {
			t.Fatalf("Get(%q) = %q, want from-env: a deployment's variable "+
				"must override what the clone carries", key, got)
		}
	})

	t.Run("a typed flag beats the environment", func(t *testing.T) {
		saveSettingsState(t)
		dir := writeServerBlock(t, "server:\n  host: from-file\n")
		t.Setenv(envName, "from-env")

		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		DeclareFlags(fs)
		if err := fs.Parse([]string{"-" + flagName, "from-flag"}); err != nil {
			t.Fatalf("parsing: %v", err)
		}
		AdoptFlags(fs)
		if err := CheckSettings(dir); err != nil {
			t.Fatalf("CheckSettings: %v", err)
		}
		if got := Get(key); got != "from-flag" {
			t.Fatalf("Get(%q) = %q, want from-flag: what an operator types "+
				"for one run must win", key, got)
		}
	})

	// `flag` cannot tell "left at its zero value" from "set to empty", and a
	// flag nobody typed beating the environment would make every unset flag
	// erase the deployment's configuration.
	t.Run("an untyped flag beats nothing", func(t *testing.T) {
		saveSettingsState(t)
		t.Setenv(envName, "from-env")

		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		DeclareFlags(fs)
		if err := fs.Parse(nil); err != nil {
			t.Fatalf("parsing: %v", err)
		}
		AdoptFlags(fs)
		if got := Get(key); got != "from-env" {
			t.Fatalf("Get(%q) = %q, want from-env: a flag nobody typed "+
				"erased the environment", key, got)
		}
	})
}

// An empty value is a value where the setting says so, and falls through
// everywhere else.
//
// `web_dir` empty means "serve no pages", and it is the escape hatch of a
// start that otherwise FAILS on an unreadable interface — a developer before
// their first `task web-build` has nothing else. It was unreachable: the
// resolution treated every empty layer as absent, so an explicitly empty
// PARAPHE_WEB_DIR resolved to the default `web/dist` and the hatch did not
// open. Meanwhile `PARAPHE_HOST=` in a generated .env must NOT resolve to an
// empty listening address, which is every interface rather than the loopback
// the default names.
func TestAnEmptyValueCountsOnlyWhereItMeansSomething(t *testing.T) {
	t.Run("web_dir empty is web_dir empty", func(t *testing.T) {
		saveSettingsState(t)
		t.Setenv("PARAPHE_WEB_DIR", "")
		if got := Get("web_dir"); got != "" {
			t.Errorf("an explicitly empty web_dir resolved to %q: the "+
				"start fails on a missing interface and nothing opts out", got)
		}
	})

	t.Run("web_dir untouched is the default", func(t *testing.T) {
		saveSettingsState(t)
		if got := Get("web_dir"); got != "web/dist" {
			t.Errorf("Get(web_dir) = %q, want the default", got)
		}
	})

	t.Run("an empty host is not every interface", func(t *testing.T) {
		saveSettingsState(t)
		t.Setenv("PARAPHE_HOST", "")
		if got := Get("host"); got != "127.0.0.1" {
			t.Errorf("Get(host) = %q — an empty listening address binds every "+
				"interface, where the default names the loopback", got)
		}
	})

	t.Run("a typed empty flag counts too", func(t *testing.T) {
		saveSettingsState(t)
		t.Setenv("PARAPHE_WEB_DIR", "/quelque/part")
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		DeclareFlags(fs)
		if err := fs.Parse([]string{"-web-dir="}); err != nil {
			t.Fatalf("parsing: %v", err)
		}
		AdoptFlags(fs)
		if got := Get("web_dir"); got != "" {
			t.Errorf("`-web-dir=` resolved to %q: an operator cannot turn the "+
				"pages off for one run", got)
		}
	})
}

// config_dir is what says where the file is, so reading it FROM that file
// would require already knowing it. It used to be resolved twice — once by
// `env()` for the server block, once by `Get` for the campaign — and
// `-config-dir` moved one and not the other.
func TestConfigDirIsNotReadFromTheFileItLocates(t *testing.T) {
	saveSettingsState(t)
	t.Setenv("PARAPHE_CONFIG_DIR", "")
	dir := writeServerBlock(t, "server:\n  config_dir: /somewhere/else\n")

	err := CheckSettings(dir)
	if err == nil {
		t.Fatal("CheckSettings accepted config_dir from the server block: " +
			"honouring it would name a directory nobody read this file from")
	}
	if !strings.Contains(err.Error(), "config_dir") {
		t.Fatalf("the refusal does not name the setting: %v", err)
	}
	// and it says which knobs DO work, read from the table rather than spelt
	// out here — a message deriving them would drift from what exists
	for _, want := range []string{"-config-dir", "PARAPHE_CONFIG_DIR"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not offer %q: %v", want, err)
		}
	}
}

func TestCheckSettingsRefusesAnUnknownKey(t *testing.T) {
	saveSettingsState(t)
	dir := writeServerBlock(t, "server:\n  hosst: typo\n")
	err := CheckSettings(dir)
	if err == nil {
		t.Fatal("a key nothing reads was accepted: it would sit there doing " +
			"nothing while the operator believed it applied")
	}
	if !strings.Contains(err.Error(), "hosst") {
		t.Fatalf("the refusal does not name the key: %v", err)
	}
}

func TestCheckSettingsRefusesAMissingRequirement(t *testing.T) {
	saveSettingsState(t)
	t.Setenv("PARAPHE_DATABASE_URL", "")
	err := CheckSettings(t.TempDir())
	if err == nil {
		t.Fatal("a required setting nobody provides was accepted")
	}
	if !strings.Contains(err.Error(), "PARAPHE_DATABASE_URL") {
		t.Fatalf("the refusal does not name the variable: %v", err)
	}
}

// A setting declared and read by nobody is a knob an operator turns for
// nothing: it appears in the help, in `-h`, and in the deployment surfaces
// that outils/deploiement.test.ts checks — all of them promising an effect
// that does not exist. The reverse (code reading an undeclared key) already
// panics in Get.
func TestEveryDeclaredSettingIsRead(t *testing.T) {
	read := map[string]bool{}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		// This file names every key in its own assertions, and settings.go
		// resolves them generically: neither is a reader.
		return strings.HasSuffix(fi.Name(), ".go") &&
			fi.Name() != "settings.go" && fi.Name() != "settings_test.go"
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name, ok := call.Fun.(*ast.Ident)
				if !ok || name.Name != "Get" || len(call.Args) != 1 {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if key, err := strconv.Unquote(lit.Value); err == nil {
					read[key] = true
				}
				return true
			})
		}
	}

	if len(read) == 0 {
		t.Fatal("no Get(\"…\") call found at all: the scan read nothing, so " +
			"it would pass whatever the table declared")
	}
	var unread []string
	for _, s := range Settings {
		if !read[s.Key] {
			unread = append(unread, s.Key)
		}
	}
	if len(unread) > 0 {
		t.Errorf("declared but read by nobody: %s\nEach one appears in -h and "+
			"in the deployment files, promising an effect it does not have.",
			strings.Join(unread, ", "))
	}
}
