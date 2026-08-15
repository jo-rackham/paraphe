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
	"log"
	"net/http"
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
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	dsn := strings.TrimSpace(os.Getenv("PARAPHE_DATABASE_URL"))
	if dsn == "" {
		return errors.New(
			"PARAPHE_DATABASE_URL is required (PostgreSQL). Example:\n" +
				"  PARAPHE_DATABASE_URL=postgresql://paraphe:password@127.0.0.1:5432/paraphe\n" +
				"Locally: `task db` starts a disposable database.")
	}
	if *waitFlag > 0 {
		return waitForDatabase(ctx, dsn, *waitFlag)
	}
	// non-strict on the app side: it stays explorable with the template
	// configuration, but every page says so. The mass mailing, on the other
	// hand, refuses to run.
	cfg, err := LoadConfig(env("PARAPHE_CONFIG_DIR", "config"))
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
		env("PARAPHE_ORG_SLUG", "campaign")))
	if !ValidSlug(bootstrapSlug) {
		return fmt.Errorf("PARAPHE_ORG_SLUG = %q: 2 to 63 characters, lowercase, "+
			"digits and dashes, and not a reserved name", bootstrapSlug)
	}
	if err := InitDatabase(ctx, pool, env("PARAPHE_CSV", "out/04_base_complete.csv"),
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
		sessions:      NewSessions(secret, os.Getenv("PARAPHE_HTTPS") != ""),
		bootstrapSlug: bootstrapSlug,
		decoyHash:     decoy,
		now:           time.Now,
		webDir:        env("PARAPHE_WEB_DIR", "web/dist"),
	}
	if landingPage, err := markInterface(s.webDir); err != nil {
		log.Printf("interface missing or unreadable in %s (%v): the API "+
			"answers, but no page is served. Build with `task web-build`, or "+
			"point PARAPHE_WEB_DIR elsewhere.", s.webDir, err)
	} else {
		s.landingPage = landingPage
	}

	addr := env("PARAPHE_HOST", "127.0.0.1") + ":" + env("PARAPHE_PORT", "8047")
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

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Liveness probe: does NOT touch the database. A database outage makes
	// the app useless but it recovers on its own as soon as the database is
	// back; restarting it would turn a 30 s failure into CrashLoopBackOff.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		replyJSON(w, http.StatusOK, map[string]string{"state": "ok"})
	})

	// READINESS probe: it touches the database, because a pod that cannot
	// see it must not receive traffic. It resolves NO campaign: a
	// Kubernetes probe addresses the pod's IP, whose hostname matches no
	// subdomain — /api/config would answer 404 and the pod would never
	// become ready.
	mux.HandleFunc("GET /health/db", func(w http.ResponseWriter, r *http.Request) {
		if err := s.pool.Ping(r.Context()); err != nil {
			log.Printf("readiness probe: database unreachable: %v", err)
			errorJSON(w, http.StatusServiceUnavailable, "Base injoignable.")
			return
		}
		replyJSON(w, http.StatusOK, map[string]string{"state": "ok"})
	})

	mux.HandleFunc("GET /api/config", s.inScope(s.routeConfig))
	mux.HandleFunc("POST /api/session", jsonOnly(admitSignIn(s.inScope(s.routeSignIn))))
	mux.HandleFunc("DELETE /api/session", s.routeSignOut)
	mux.HandleFunc("GET /api/me", s.signedIn(s.routeMe))
	mux.HandleFunc("POST /api/me/personal_note", jsonOnly(s.signedIn(s.routePersonalNote)))

	mux.HandleFunc("GET /api/dashboard", s.inCampaign(s.routeDashboard))
	mux.HandleFunc("GET /api/facets", s.inCampaign(s.routeFacets))
	mux.HandleFunc("GET /api/mayors", s.inCampaign(s.routeMayors))
	mux.HandleFunc("GET /api/mayors/{insee}", s.inCampaign(s.routeCard))
	mux.HandleFunc("POST /api/mayors/{insee}/status",
		jsonOnly(s.inCampaign(s.routeStatus)))
	mux.HandleFunc("POST /api/batch", jsonOnly(s.inCampaign(s.routeBatch)))
	mux.HandleFunc("GET /api/export.csv", s.inCampaign(s.routeExport))

	mux.HandleFunc("GET /api/team", s.managers(s.routeTeam))
	mux.HandleFunc("POST /api/team/group",
		jsonOnly(s.coordinationOnly(s.routeCreateTeam)))
	mux.HandleFunc("POST /api/team/account",
		jsonOnly(s.managers(s.routeCreateAccount)))
	mux.HandleFunc("POST /api/team/account/{email}/active",
		jsonOnly(s.managers(s.routeToggleAccount)))
	// Read by the browser version from ANOTHER origin: no session, no
	// cookie, only the campaign a mayor already reads in every message.
	mux.HandleFunc("GET /api/campaign/public",
		publicToEveryOrigin(s.inScope(s.routePublicCampaign)))
	mux.HandleFunc("POST /api/campaign",
		jsonOnly(s.coordinationOnly(s.routeUpdateCampaign)))

	// The instance landing page: requesting a campaign, moderating requests.
	mux.HandleFunc("POST /api/request", jsonOnly(s.instanceOnly(s.routeHostingRequest)))
	mux.HandleFunc("GET /api/admin/requests", s.administrationOnly(s.routeHostingQueue))
	mux.HandleFunc("POST /api/admin/requests/{id}",
		jsonOnly(s.administrationOnly(s.routeDecideHosting)))

	// everything else: the interface. An unknown /api/ route returns JSON,
	// not the landing page — otherwise a typo in the front end shows up as
	// "unexpected token < in JSON", which tells nobody anything.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			errorJSON(w, http.StatusNotFound, "Route inconnue : %s %s.",
				r.Method, r.URL.Path)
			return
		}
		s.serveInterface(w, r)
	})
	return answerOnPanic(refuseUnstorableText(mux))
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
