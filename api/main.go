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
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
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
	// marked index.html: see markInterface
	landingPage []byte
	// the same page, gzipped once at startup: it is served on every load
	// and compressing it per request would be work repeated for nothing
	landingPageGz []byte
}

// Mode marker, injected into the page served by the API.
//
// Without it, the interface deduces "no API, so browser mode" from any
// failure on /api/config — including a Wi-Fi portal or a 502. The volunteer
// then works in their browser, on the team's origin, noticing nothing:
// their work never reaches the server and stays on the computer after they
// leave. The marker makes that switch impossible.
const modeMarker = `<meta name="paraphe-mode" content="team">`

func markInterface(dir string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		return nil, err
	}
	page := string(raw)
	if !strings.Contains(page, "</head>") {
		return nil, fmt.Errorf("%s/index.html has no </head>: the mode marker "+
			"cannot be set, and the interface would switch to browser mode at "+
			"the first failure", dir)
	}
	return []byte(strings.Replace(page, "</head>", modeMarker+"\n</head>", 1)), nil
}

// gzipBytes compresses once, at startup. BestCompression because this runs
// exactly one time for a document served on every load.
func gzipBytes(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	z, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := z.Write(raw); err != nil {
		return nil, err
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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

	s := &Server{
		pool:          pool,
		cfg:           cfg,
		sessions:      NewSessions(secret),
		bootstrapSlug: bootstrapSlug,
		decoyHash:     decoy,
		now:           time.Now,
		webDir:        Get("web_dir"),
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

// pathParam reads a route parameter, UNESCAPED.
//
// chi matches on the raw path whenever it differs from the decoded one, so
// `chi.URLParam` hands back what the client sent — an email arrives as
// `someone%40example.fr`. Every parameter here is compared against stored
// data, so every one of them has to be decoded.
func pathParam(r *http.Request, name string) string {
	raw := chi.URLParam(r, name)
	if decoded, err := url.PathUnescape(raw); err == nil {
		return decoded
	}
	return raw
}

// guard adapts the package's middleware — all `http.HandlerFunc ->
// http.HandlerFunc` — to what chi stacks with Use.
func guard(m func(http.HandlerFunc) http.HandlerFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(m(next.ServeHTTP))
	}
}

// routes: the guards are declared BY GROUP, not per route.
//
// Written route by route, a new one is a line that has to remember its own
// `jsonOnly`, its own `inCampaign` and its own role check — and forgetting
// one is invisible, because the route works. Under a group the guard is
// already there, and TestEveryAPIRouteIsGuarded walks the tree to say so.
func (s *Server) routes() http.Handler {
	return answerOnPanic(refuseUnstorableText(s.router()))
}

// router: the tree alone, without the wrappers routes() puts around it, so
// TestEveryAPIRouteRefusesAnAnonymousCaller walks exactly what is served.
func (s *Server) router() chi.Router {
	r := chi.NewRouter()

	// Liveness probe: does NOT touch the database. A database outage makes
	// the app useless but it recovers on its own as soon as the database is
	// back; restarting it would turn a 30 s failure into CrashLoopBackOff.
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		replyJSON(w, http.StatusOK, map[string]string{"state": "ok"})
	})

	// READINESS probe: it touches the database, because a pod that cannot
	// see it must not receive traffic. It resolves NO campaign: a Kubernetes
	// probe addresses the pod's IP, whose hostname matches no subdomain —
	// /api/config would answer 404 and the pod would never become ready.
	r.Get("/health/db", func(w http.ResponseWriter, r *http.Request) {
		if err := s.pool.Ping(r.Context()); err != nil {
			slog.Error("readiness probe: database unreachable", "error", err)
			errorJSON(w, http.StatusServiceUnavailable, "Base injoignable.")
			return
		}
		replyJSON(w, http.StatusOK, map[string]string{"state": "ok"})
	})

	r.Route("/api", func(r chi.Router) {
		// An unknown /api route answers JSON, not the landing page:
		// otherwise a typo in the front end surfaces as "unexpected token <
		// in JSON", which tells nobody anything.
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			errorJSON(w, http.StatusNotFound, "Route inconnue : %s %s.",
				r.Method, r.URL.Path)
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			errorJSON(w, http.StatusMethodNotAllowed,
				"Méthode refusée : %s %s.", r.Method, r.URL.Path)
		})

		// No session yet: what the page needs to know before anyone signs in.
		r.With(guard(s.inScope)).Get("/config", s.routeConfig)
		r.With(guard(jsonOnly), guard(admitSignIn), guard(s.inScope)).
			Post("/session", s.routeSignIn)
		r.Delete("/session", s.routeSignOut)
		// Read by the browser version from ANOTHER origin: no session, no
		// cookie, only the campaign a mayor already reads in every message.
		r.With(guard(publicToEveryOrigin), guard(s.inScope)).
			Get("/campaign/public", s.routePublicCampaign)

		// Signed in, whatever the scope.
		r.Group(func(r chi.Router) {
			r.Use(guard(s.signedIn))
			r.Get("/me", s.routeMe)
			r.With(guard(jsonOnly)).Post("/me/personal_note", s.routePersonalNote)
		})

		// Inside a campaign: everything a volunteer works with.
		r.Group(func(r chi.Router) {
			r.Use(guard(s.inCampaign))
			r.Get("/dashboard", s.routeDashboard)
			r.Get("/facets", s.routeFacets)
			r.Get("/mayors", s.routeMayors)
			r.Get("/mayors/{insee}", s.routeCard)
			r.Get("/export.csv", s.routeExport)
			r.With(guard(jsonOnly)).Post("/mayors/{insee}/status", s.routeStatus)
			r.With(guard(jsonOnly)).Post("/batch", s.routeBatch)
		})

		// Managing the campaign's own teams and accounts.
		r.Route("/team", func(r chi.Router) {
			r.With(guard(s.managers)).Get("/", s.routeTeam)
			r.With(guard(jsonOnly), guard(s.coordinationOnly)).
				Post("/group", s.routeCreateTeam)
			r.With(guard(jsonOnly), guard(s.managers)).
				Post("/account", s.routeCreateAccount)
			r.With(guard(jsonOnly), guard(s.managers)).
				Post("/account/{email}/active", s.routeToggleAccount)
		})
		r.With(guard(jsonOnly), guard(s.coordinationOnly)).
			Post("/campaign", s.routeUpdateCampaign)

		// The instance landing page: requesting a campaign, moderating.
		r.With(guard(jsonOnly), guard(s.instanceOnly)).
			Post("/request", s.routeHostingRequest)
		r.Route("/admin/requests", func(r chi.Router) {
			r.Use(guard(s.administrationOnly))
			r.Get("/", s.routeHostingQueue)
			r.With(guard(jsonOnly)).Post("/{id}", s.routeDecideHosting)
		})
	})

	// Everything else is the interface, when this binary serves one.
	r.NotFound(s.serveInterface)

	return r
}

// refuseUnstorableText: PostgreSQL refuses U+0000 in any text value AND
// any malformed UTF-8, with the same 22021 — %C0%80 is the NUL itself in
// overlong form, so refusing the rune alone still let the byte through to
// a 500. One refusal here instead of a guard per parameter; readBody does
// the same for request bodies (where encoding/json already guarantees
// valid UTF-8, leaving only the NUL to refuse).
func refuseUnstorableText(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		poisoned := !acceptableText(r.URL.Path)
		for _, values := range r.URL.Query() {
			for _, v := range values {
				poisoned = poisoned || !acceptableText(v)
			}
		}
		if poisoned {
			// the one route another origin reads keeps its CORS header on
			// this refusal too: without it the browser drops the body and
			// reports « Failed to fetch » instead of the actual message
			if r.URL.Path == "/api/campaign/public" {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			}
			errorJSON(w, http.StatusBadRequest,
				"La requête contient un caractère invalide.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// acceptableText: what PostgreSQL accepts in a text value.
func acceptableText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// signInAdmission bounds the sign-in attempts allowed PAST this point.
// inScope takes a pool connection and a transaction BEFORE the handler,
// so an attempt queued on hashGate held a PostgreSQL connection for
// nothing: 200 anonymous attempts measured 8.8 ms → 2.17 s on every
// authenticated request. Admitted at the gate's own width, the queue
// holds no connection, and ctx.Done() sheds a caller who hung up before
// their derivation started.
var signInAdmission = make(chan struct{}, cap(hashGate))

func admitSignIn(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case signInAdmission <- struct{}{}:
			defer func() { <-signInAdmission }()
		case <-r.Context().Done():
			// Only reached once the caller has already hung up, or at
			// shutdown — waiting for a slot is a wait, never a refusal. Said
			// rather than silent all the same: a bare return answers 200
			// with an empty body, which the interface reports as
			// "unexpected end of JSON input".
			errorJSON(w, http.StatusServiceUnavailable,
				"Connexion interrompue avant d'avoir pu vous identifier.")
			return
		}
		next(w, r)
	}
}

// serveInterface serves web/dist. No directory listing, and fallback to
// index.html for extension-less paths (the application is a single page: a
// reload on /team must render the application, not 404).
//
// This binary serves the pages AND the JSON, which is why securityHeaders
// wraps the whole router: whoever serves a response is who sets its headers,
// and splitting the two once left every page without a Content-Security-
// Policy while the API kept its own.
func (s *Server) serveInterface(w http.ResponseWriter, r *http.Request) {
	path := filepath.Clean(r.URL.Path)
	if path == "/" || filepath.Ext(path) == "" {
		path = "/index.html"
	}
	file := filepath.Join(s.webDir, filepath.FromSlash(path))
	// filepath.Clean on an absolute URL path already neutralises "..", but
	// the check is free and survives the day this prefix changes
	root, err := filepath.Abs(s.webDir)
	if err != nil {
		s.failure(w, err)
		return
	}
	abs, err := filepath.Abs(file)
	if err != nil || !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		errorJSON(w, http.StatusNotFound, "Chemin inconnu.")
		return
	}
	// the landing page is served from memory, marked: it is what tells the
	// interface it is talking to an API
	if path == "/index.html" {
		if s.landingPage == nil {
			errorJSON(w, http.StatusNotFound,
				"Interface introuvable. Construire avec `task web-build`.")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// never cached: a volunteer holding an index.html from before a
		// deployment loads asset names that no longer exist
		w.Header().Set("Cache-Control", "no-store")
		s.writePage(w, r)
		return
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		errorJSON(w, http.StatusNotFound,
			"Interface introuvable (%s). Construire avec `task web-build`.", path)
		return
	}
	// Everything under /assets/ is content-hashed by the build, so its name
	// changes whenever its bytes do and it can be kept for ever. Everything
	// else — the favicon, robots.txt — carries a stable name and must not be.
	if strings.HasPrefix(path, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	s.serveFile(w, r, abs)
}

// encodings: the precompressed variants the build produces, best first. They
// are built once at image build time rather than compressed per request:
// brotli at quality 11 is far too slow to run on the fly, and it is what
// takes the interface bundle from 357 kB to 90.
var encodings = []struct{ token, suffix string }{
	{"br", ".br"},
	{"gzip", ".gz"},
}

// serveFile answers with a precompressed variant when the client accepts one
// and it exists beside the original.
//
// Content-Type comes from the ORIGINAL name: index-a1b2.js.br is JavaScript,
// and http.ServeFile would call it application/x-brotli. Vary is required or
// a cache serves the compressed bytes to a client that asked for none.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, abs string) {
	w.Header().Set("Vary", "Accept-Encoding")
	accepted := r.Header.Get("Accept-Encoding")
	for _, e := range encodings {
		if !acceptsEncoding(accepted, e.token) {
			continue
		}
		variant := abs + e.suffix
		info, err := os.Stat(variant)
		if err != nil || info.IsDir() {
			continue
		}
		w.Header().Set("Content-Type", contentType(abs))
		w.Header().Set("Content-Encoding", e.token)
		http.ServeFile(w, r, variant)
		return
	}
	http.ServeFile(w, r, abs)
}

// acceptsEncoding: does this Accept-Encoding name the token, other than to
// refuse it? `gzip;q=0` is a client saying NOT gzip, and serving it gzip is
// how a response arrives unreadable.
func acceptsEncoding(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(fields[0]), token) {
			continue
		}
		for _, p := range fields[1:] {
			if q := strings.TrimSpace(p); strings.HasPrefix(q, "q=") &&
				(q == "q=0" || strings.HasPrefix(q, "q=0.0")) {
				return false
			}
		}
		return true
	}
	return false
}

func contentType(name string) string {
	if t := mime.TypeByExtension(filepath.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// writePage serves the marked index.html, gzipped when the client accepts
// it. Only gzip: the page is compressed once at startup, in memory, and
// compress/gzip is in the standard library — bringing a brotli encoder into
// the module to save two kilobytes on one document would be a dependency
// bought for nothing. The assets, where the bytes actually are, get brotli
// from the build.
func (s *Server) writePage(w http.ResponseWriter, r *http.Request) {
	if s.landingPageGz != nil && acceptsEncoding(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		if _, err := w.Write(s.landingPageGz); err != nil {
			slog.Error("landing page not served", "error", err)
		}
		return
	}
	if _, err := w.Write(s.landingPage); err != nil {
		slog.Error("landing page not served", "error", err)
	}
}

// answerOnPanic: a panic in a handler must still leave the client with an
// HTTP answer. Go's server recovers it and closes the connection, so the
// caller sees EOF — no status, no body, nothing an operator can read in a
// log or a browser. `scopeOrg` panics deliberately when a request carries no
// scope; that refusal is right, and it should arrive as a 500.
func answerOnPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				slog.Error("panic serving a request",
					"method", r.Method, "path", r.URL.Path, "panic", p)
				// the handler may already have written: setting a status
				// then is a no-op and logs, which is the lesser evil
				errorJSON(w, http.StatusInternalServerError,
					"Erreur interne. L'incident est enregistré.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeaders: the application needs nothing external — no font, no
// script, no remote image. The policy says so, so that injecting third-
// party content fails instead of executing.
func securityHeaders(next http.Handler) http.Handler {
	const policy = "default-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; " + // React sets style attributes
		"img-src 'self' data:; " +
		"connect-src 'self'; " +
		"form-action 'self'; " +
		"base-uri 'none'; " +
		"frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", policy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// publicToEveryOrigin marks a route readable from any origin, BEFORE the
// scope resolution runs. Set inside the handler only, the header was
// missing from every refusal — an unknown subdomain, the apex, a typo — and
// a browser discards the body of a cross-origin response without it: the
// sentence the API took care to write surfaced as "Failed to fetch".
func publicToEveryOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		next(w, r)
	}
}
