// Command paraphe-api: the team application — the JSON API, and the pages
// that consume it.
//
// It generates no MESSAGE: the texts meant for mayors are produced by the
// front end (noyau/messages.ts), the same engine as the browser version. Two
// message paths already share that single engine (mass mailing and both
// interface modes); a second implementation would be one more occasion to
// say "thank you for your endorsement" to someone who never endorsed anyone.
//
// It does serve the pages, and that is deliberate. Serving them from a
// second image meant the interface a volunteer loads came from one process
// and the API it talks to from another, with the version discipline holding
// them together; and it meant the page path exercised by every test was not
// the one production ran. One process, one artefact, one place where a
// response gets its headers.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	pool *pgxpool.Pool
	// cfg: the BOOTSTRAP configuration (file + PARAPHE_*), plus what belongs
	// to the instance rather than to a campaign (source code link). The
	// configuration served to the screens comes from the organisation, in
	// the database.
	cfg      *Config
	sessions *Sessions
	// bootstrapSlug: the campaign served when the instance is
	// single-campaign, that is, when no base domain is configured.
	bootstrapSlug string
	decoyHash     string
	now           func() time.Time
	webDir        string
	// browserDir: a second build of the same interface, served under
	// /navigateur/ WITHOUT the mode marker — the account-less browser
	// version, self-hosted (pages.go, serveBrowserVersion)
	browserDir string
	// proxies: the networks whose X-Forwarded-For is believed (clientip.go)
	proxies trustedProxies
	// limiter: the rate limiter — Valkey-backed when valkey_url is set,
	// this process's memory otherwise (limiter.go)
	limiter *rateLimiter
	// logKey derives the day-scoped pseudonyms the security events carry:
	// no client address and no submitted email reaches a log line raw
	logKey []byte
	// marked index.html: see markInterface
	landingPage []byte
	// the same page, gzipped once at startup: it is served on every load
	// and compressing it per request would be work repeated for nothing
	landingPageGz []byte
}

func main() {
	if err := run(); err != nil {
		slog.Error("paraphe stopped", "error", err)
		os.Exit(1)
	}
}

// setupLogging: JSON on stdout, one object per line.
//
// The twelfth-factor point is not the format for its own sake — the service
// already wrote to stdout and let the platform collect it. It is that a
// collector can then FILTER: a `level` to alert on, a `time` to correlate
// across three pods, and a message that stays one field however many commas
// it contains. A grep over free text does none of that, and the lines this
// service emits are French sentences with quotes in them.
//
// The standard logger is redirected into the same handler rather than each
// of its call sites being rewritten. `log.Printf` in this package, in pgx,
// or in anything else, lands in the same stream with the same shape; the
// sites that carry values worth querying use slog directly.
func setupLogging() {
	level := slog.LevelInfo
	if err := level.UnmarshalText([]byte(Get("log_level"))); err != nil {
		// Said, not swallowed: a misspelt level would otherwise silently
		// mean "info" and an operator would conclude the setting does
		// nothing.
		defer func() {
			slog.Warn("unreadable log level, using info",
				"value", Get("log_level"), "error", err)
		}()
		level = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
	log.SetFlags(0)
	log.SetOutput(logWriter{})
}

// logWriter sends the standard logger's lines through slog, so a package
// that knows nothing of this one still lands in the same stream.
type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	slog.Info(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// waitFlag: init-container mode. Without this wait, the instances restart
// in a loop while the operator elects a primary — noisy, unreadable, and
// indistinguishable from a real failure.
var waitFlag = flag.Duration("attendre-base", 0,
	"wait until PostgreSQL answers, then exit (init container)")

func run() error {
	DeclareFlags(flag.CommandLine)
	flag.Parse()
	AdoptFlags(flag.CommandLine)
	// After AdoptFlags, so that `-log-level` is one: set up before it, the
	// flag layer does not exist yet and the flag would silently do nothing
	// — the same shape as `config_dir` resolved twice.
	setupLogging()
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()
	// …and again after the file layer is loaded, below, because a
	// `log_level:` written under `server:` is dead until then. Set up once,
	// the flag worked and the file did not — the same shape a third time.

	// The waiting flag answers before anything is resolved: an init
	// container has a DSN and nothing else, and must not be refused for a
	// setting it does not need.
	if *waitFlag > 0 {
		// Get and not os.Getenv: the whole point of the settings table is
		// that a flag beats the environment, and reading the variable
		// directly here made `-database-url` silently do nothing in the one
		// mode where an operator is most likely to type it by hand. The
		// file layer is not loaded yet, so this resolves flag > env >
		// default — which is exactly what an init container has.
		return waitForDatabase(ctx, Get("database_url"), *waitFlag)
	}
	// Resolved ONCE, and the same value locates the server block and the
	// campaign. Read twice, `-config-dir` moved one and not the other: the
	// settings came from ./config and the campaign from elsewhere, in the
	// same start.
	configDir := Get("config_dir")
	if err := CheckSettings(configDir); err != nil {
		return fmt.Errorf("%w\nLocally, `task db` starts a disposable database", err)
	}
	// The file layer exists now: re-read the level, so `log_level:` under
	// `server:` is a setting and not decoration.
	setupLogging()
	dsn := Get("database_url")
	// non-strict on the app side: it stays explorable with the template
	// configuration, but every page says so. The mass mailing, on the other
	// hand, refuses to run.
	cfg, err := LoadConfig(configDir)
	if err != nil {
		return err
	}
	if len(cfg.Unfilled) > 0 {
		slog.Warn("configuration at template values — messages contain example values",
			"keys", strings.Join(cfg.Unfilled, ", "))
	}

	pool, err := OpenDatabase(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	bootstrapSlug := strings.ToLower(strings.TrimSpace(
		Get("org_slug")))
	if !ValidSlug(bootstrapSlug) {
		return fmt.Errorf("PARAPHE_ORG_SLUG = %q: 2 to 63 characters, lowercase, "+
			"digits and dashes, and not a reserved name", bootstrapSlug)
	}
	if err := InitDatabase(ctx, pool, Get("csv"),
		cfg, bootstrapSlug); err != nil {
		return err
	}
	if base := BaseDomain(); base != "" {
		slog.Info("multi-campaign instance: each campaign at <campaign>.<domain>, "+
			"landing page and moderation on the apex", "domain", base)
	}
	secret, err := SessionSecret(ctx, pool)
	if err != nil {
		return err
	}
	// compared when the address is unknown, so signing in costs the same
	// time as with an existing account
	decoy, err := HashPassword("nonexistent-account")
	if err != nil {
		return err
	}

	proxies, err := parseTrustedProxies(Get("trusted_proxies"))
	if err != nil {
		return err
	}
	// The shared store exists only when the deployment asked for one; the
	// process store always does. Reaching Valkey is checked here so a wrong
	// URL or password is one startup line, but a Valkey outage does not
	// hold the start hostage: the limiter degrades out loud instead.
	var shared counterStore
	var valkeyCounters *valkeyStore
	if url := Get("valkey_url"); url != "" {
		valkeyCounters, err = newValkeyStore(url, Get("valkey_password"))
		if err != nil {
			return err
		}
		defer valkeyCounters.close()
		if err := valkeyCounters.ping(ctx); err != nil {
			slog.Error("Valkey does not answer: rate limits start degraded, "+
				"held per instance until it does", "error", err)
		}
		shared = valkeyCounters
	}
	limiterStartupLog(valkeyCounters, Get("valkey_url"))

	s := &Server{
		pool:          pool,
		cfg:           cfg,
		sessions:      NewSessions(secret),
		bootstrapSlug: bootstrapSlug,
		decoyHash:     decoy,
		now:           time.Now,
		webDir:        Get("web_dir"),
		browserDir:    Get("browser_web_dir"),
		proxies:       proxies,
		limiter:       newRateLimiter(secret, shared, time.Now),
		logKey:        deriveKey(secret, "paraphe:log-pseudonyms:v1"),
	}
	// One image serves the pages AND the JSON, so an unreadable interface is
	// a broken image and not a shape anybody deploys: it FAILS the start.
	// Answering 404 on every page while /api works is the kind of half-alive
	// process a readiness probe calls healthy and a volunteer calls a blank
	// screen.
	//
	// An EMPTY web_dir is the exception, and it is explicit: it means "no
	// pages here", which is what a developer has before the first
	// `task web-build`.
	if s.webDir == "" {
		slog.Info("no interface served (web_dir is empty): this process " +
			"answers JSON only")
	} else {
		landingPage, err := markInterface(s.webDir)
		if err != nil {
			return fmt.Errorf("interface unreadable in %s: %w\n"+
				"This image serves the pages as well as the API, so there is "+
				"nothing to serve. Build one with `task web-build`, or set "+
				"web_dir empty to answer JSON only", s.webDir, err)
		}
		s.landingPage = landingPage
		if s.landingPageGz, err = gzipBytes(landingPage); err != nil {
			return fmt.Errorf("compressing the landing page: %w", err)
		}
	}
	// Same rule for the browser version: set and unreadable is a broken
	// image, not a deployment shape. Its index must NOT carry the mode
	// marker — the absence is what lets it fall into browser mode.
	if s.browserDir != "" {
		raw, err := os.ReadFile(filepath.Join(s.browserDir, "index.html"))
		if err != nil {
			return fmt.Errorf("browser version unreadable in %s: %w\n"+
				"Build one with `task web-build-navigateur`, or set "+
				"browser_web_dir empty to serve none", s.browserDir, err)
		}
		if strings.Contains(string(raw), `name="paraphe-mode"`) {
			return fmt.Errorf("%s/index.html carries the mode marker: this "+
				"build would never switch to browser mode. Point browser_web_dir "+
				"at a build made for /navigateur/, not at web_dir", s.browserDir)
		}
	}

	addr := Get("host") + ":" + Get("port")
	srv := &http.Server{
		Addr:    addr,
		Handler: securityHeaders(s.routes()),
		// bounded: without them, a connection that never sends its request
		// pins a file descriptor until restart
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// the CSV export of 34,826 rows goes through here
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}

	done := make(chan error, 1)
	go func() {
		slog.Info("listening", "address", addr, "interface", s.webDir != "")
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		slog.Info("shutdown requested, finishing in-flight requests")
		// long enough for a running export to finish
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-done
	}
}

func waitForDatabase(ctx context.Context, dsn string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for attempt := 1; ; attempt++ {
		pool, err := OpenDatabase(ctx, dsn)
		if err == nil {
			pool.Close()
			slog.Info("database reachable")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("database unreachable after %s: %w", timeout, err)
		}
		slog.Info("waiting for the database", "attempt", attempt, "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
