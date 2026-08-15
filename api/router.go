package main

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

// The route tree and the middleware that wraps it whole.

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
		// The anonymous ceilings sit FIRST: a refused caller must not reach
		// inScope, which is where a request starts holding a pool connection.
		r.With(guard(s.limitIP(limitAnonIP)), guard(s.inScope)).
			Get("/config", s.routeConfig)
		// Sign-in counts twice: per source before anything is read, and per
		// submitted address once jsonOnly has buffered the body. Both before
		// admitSignIn — a refused attempt has no business queueing for the
		// hash gate.
		r.With(guard(s.limitIP(limitSignInIP)), guard(jsonOnly),
			guard(s.limitSignInBody), guard(admitSignIn), guard(s.inScope)).
			Post("/session", s.routeSignIn)
		r.Delete("/session", s.routeSignOut)
		// Read by the browser version from ANOTHER origin: no session, no
		// cookie, only the campaign a mayor already reads in every message.
		// The CORS marker stays first so a 429 remains readable over there.
		r.With(guard(publicToEveryOrigin), guard(s.limitIP(limitAnonIP)),
			guard(s.inScope)).
			Get("/campaign/public", s.routePublicCampaign)

		// Signed in, whatever the scope.
		r.Group(func(r chi.Router) {
			r.Use(guard(s.signedIn))
			r.Get("/me", s.routeMe)
			r.With(guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/me/personal_note", s.routePersonalNote)
		})

		// Inside a campaign: everything a volunteer works with.
		r.Group(func(r chi.Router) {
			r.Use(guard(s.inCampaign))
			r.Get("/dashboard", s.routeDashboard)
			r.Get("/facets", s.routeFacets)
			r.Get("/mayors", s.routeMayors)
			r.Get("/mayors/{insee}", s.routeCard)
			r.With(guard(s.limitAccount(limitExportAccount))).
				Get("/export.csv", s.routeExport)
			r.With(guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/mayors/{insee}/status", s.routeStatus)
			r.With(guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/batch", s.routeBatch)
		})

		// Managing the campaign's own teams and accounts.
		r.Route("/team", func(r chi.Router) {
			r.With(guard(s.managers)).Get("/", s.routeTeam)
			r.With(guard(s.coordinationOnly),
				guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/group", s.routeCreateTeam)
			r.With(guard(s.managers),
				guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/account", s.routeCreateAccount)
			r.With(guard(s.managers),
				guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/account/{email}/active", s.routeToggleAccount)
		})
		r.With(guard(s.coordinationOnly),
			guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
			Post("/campaign", s.routeUpdateCampaign)

		// The instance landing page: requesting a campaign, moderating.
		// The public form is the narrowest ceiling of all: each request is
		// moderation work for a human, and a thousand of them close the
		// queue (maxPendingRequests) for everyone.
		r.With(guard(s.limitIP(limitHostingIP)), guard(jsonOnly),
			guard(s.instanceOnly)).
			Post("/request", s.routeHostingRequest)
		r.Route("/admin/requests", func(r chi.Router) {
			r.Use(guard(s.administrationOnly))
			r.Get("/", s.routeHostingQueue)
			r.With(guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/{id}", s.routeDecideHosting)
		})
		// Direct creation, same scope as moderation: the queue's ceiling
		// message points a flooded requester at exactly this door.
		r.With(guard(s.administrationOnly),
			guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
			Post("/admin/campaigns", s.routeCreateCampaign)
		// The public directory of the hosted campaigns, apex only. Names
		// and addresses are public by construction: every subdomain answers.
		r.With(guard(s.instanceOnly)).
			Get("/campaigns", s.routeCampaignDirectory)
	})

	// The account-less browser version. Its /navigateur/api/* paths are
	// answered with HTML by the handler itself, which is what keeps that
	// build in browser mode. With no build configured the handler steps
	// aside, and the paths belong to the ordinary interface fallback.
	r.Handle("/navigateur",
		http.RedirectHandler("/navigateur/", http.StatusMovedPermanently))
	r.Handle("/navigateur/*", http.HandlerFunc(s.serveBrowserVersion))

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
		// Unconditional, like the cookie flags: browsers only honour it
		// over TLS, so it costs nothing locally and pins HTTPS wherever it
		// counts. No `preload` — that is a semi-permanent commitment on the
		// operator's domain, theirs to make, not this binary's.
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		// The application uses none of these; saying so makes an injected
		// script ask for nothing quietly.
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
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
