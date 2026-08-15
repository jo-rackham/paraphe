// Command paraphe-api: the JSON API of the team application.
//
// It renders no HTML and generates no message: the texts meant for mayors
// are produced by the front end (noyau/messages.ts), the same engine as the
// browser version. Two message paths already share that single engine (mass
// mailing and both interface modes); a second implementation would be one
// more occasion to say "thank you for your endorsement" to someone who
// never endorsed anyone.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/go-chi/chi/v5"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

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

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	if err := run(); err != nil {
		log.Fatalf("paraphe: %v", err)
	}
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
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The waiting flag answers before anything is resolved: an init
	// container has a DSN and nothing else, and must not be refused for a
	// setting it does not need.
	if *waitFlag > 0 {
		return waitForDatabase(ctx, strings.TrimSpace(
			os.Getenv("PARAPHE_DATABASE_URL")), *waitFlag)
	}
	// Resolved ONCE, and the same value locates the server block and the
	// campaign. Read twice, `-config-dir` moved one and not the other: the
	// settings came from ./config and the campaign from elsewhere, in the
	// same start.
	configDir := Get("config_dir")
	if err := CheckSettings(configDir); err != nil {
		return fmt.Errorf("%w\nLocally, `task db` starts a disposable database", err)
	}
	dsn := Get("database_url")
	// non-strict on the app side: it stays explorable with the template
	// configuration, but every page says so. The mass mailing, on the other
	// hand, refuses to run.
	cfg, err := LoadConfig(configDir)
	if err != nil {
		return err
	}
	if len(cfg.Unfilled) > 0 {
		log.Printf("WARNING: configuration at template values (%s) — messages "+
			"contain example values", strings.Join(cfg.Unfilled, ", "))
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
		log.Printf("multi-campaign instance on %s: each campaign at "+
			"<campaign>.%s, landing page and moderation on %s", base, base, base)
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
	// No interface is the DEPLOYED shape: the interface image serves the
	// pages and proxies /api back here, and serving them from both would be a
	// second copy nobody rebuilds. It is also what a developer sees before
	// the first `task web-build`. One message covers both, because nothing
	// here can tell them apart — an environment variable set to the empty
	// string reads exactly like one nobody set.
	if landingPage, err := markInterface(s.webDir); err != nil {
		log.Printf("no interface served here (%s: %v): this binary answers "+
			"JSON. In a deployment the interface image serves the pages; "+
			"locally, `task web-build` builds one.", s.webDir, err)
	} else {
		s.landingPage = landingPage
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
		log.Printf("paraphe: http://%s", addr)
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
		log.Print("shutdown requested, finishing in-flight requests…")
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
			log.Print("database reachable")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("database unreachable after %s: %w", timeout, err)
		}
		log.Printf("waiting for the database (attempt %d): %v", attempt, err)
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
			log.Printf("readiness probe: database unreachable: %v", err)
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
// so an attempt queued on scryptGate held a PostgreSQL connection for
// nothing: 200 anonymous attempts measured 8.8 ms → 2.17 s on every
// authenticated request. Admitted at the gate's own width, the queue
// holds no connection, and ctx.Done() sheds a caller who hung up before
// their derivation started.
var signInAdmission = make(chan struct{}, cap(scryptGate))

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
		w.Header().Set("Cache-Control", "no-store")
		if _, err := w.Write(s.landingPage); err != nil {
			log.Printf("landing page not served: %v", err)
		}
		return
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		errorJSON(w, http.StatusNotFound,
			"Interface introuvable (%s). Construire avec `task web-build`.", path)
		return
	}
	http.ServeFile(w, r, abs)
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
				log.Printf("panic serving %s %s: %v", r.Method, r.URL.Path, p)
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
