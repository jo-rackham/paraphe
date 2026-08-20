package main

import (
	"fmt"
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
		// A PING is not readiness. Restored from an empty database, this pod
		// answered 200 here with not a single table in place: the probe was
		// green, traffic arrived, and every screen was broken — the exact
		// shape of failure this application refuses everywhere else. The
		// schema is built at startup, so a table missing means the build did
		// not happen, and no request can be served.
		//
		// `LIMIT 0` reads no row: it costs a plan and answers 42P01 when the
		// table is absent, which is the whole question.
		if _, err := s.pool.Exec(r.Context(),
			"SELECT 1 FROM orgs LIMIT 0"); err != nil {
			slog.Error("readiness probe: the database cannot serve a request",
				"error", err)
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
			guard(s.limitEmailBody(limitSignInAccount)), guard(admitSignIn),
			guard(s.inScope)).
			Post("/session", s.routeSignIn)
		r.Delete("/session", s.routeSignOut)
		// Signing in by email. The link REQUEST counts per source and per
		// submitted address like a sign-in, and lower: admitted, it makes
		// this service send a real message to a real inbox, so its ceiling
		// protects the address it would be aimed at, not only this server.
		// No admitSignIn — nothing here derives a password hash.
		r.With(guard(s.limitIP(limitMagicLinkIP)), guard(jsonOnly),
			guard(s.limitEmailBody(limitMagicLinkAccount)), guard(s.inScope)).
			Post("/session/link", s.routeRequestLink)
		// Redeeming carries a 256-bit token and no address, so there is
		// nothing to count per account: the source ceiling is the whole
		// bound, and a token cannot be searched for behind it.
		r.With(guard(s.limitIP(limitSignInIP)), guard(jsonOnly),
			guard(s.inScope)).
			Post("/session/link/redeem", s.routeRedeemLink)
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
		// Changing one's own password, and OUTSIDE the group above because
		// of the order: this route derives two hashes — one to verify the
		// current password, one to write the new — so it queues on the same
		// gate a sign-in does, and admitSignIn has to run BEFORE signedIn
		// takes a pool connection. Inside the group, `r.Use` would put the
		// connection first and the queue after it, which is the measured
		// defect that gate exists for.
		r.With(guard(admitSignIn), guard(s.signedIn),
			guard(s.limitAccount(limitPasswordAccount)), guard(jsonOnly)).
			Post("/me/password", s.routeChangePassword)

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
			// Correcting one's own words, and removing a note. No jsonOnly on
			// the deletion: it carries no body, like DELETE /campaign/logo.
			r.With(guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/mayors/{insee}/notes/{id}", s.routeEditNote)
			r.With(guard(s.limitAccount(limitWriteAccount))).
				Delete("/mayors/{insee}/notes/{id}", s.routeDeleteNote)
			r.With(guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/batch", s.routeBatch)
		})

		// Managing the campaign's own teams and accounts.
		r.Route("/team", func(r chi.Router) {
			r.With(guard(s.managers)).Get("/", s.routeTeam)
			// Public, on the campaign: asking it to open a local team. The
			// ceiling is per source and narrow, like the hosting form's —
			// each request is moderation work for a coordination that has a
			// campaign to run.
			r.With(guard(s.limitIP(limitTeamRequestIP)), guard(jsonOnly),
				guard(s.campaignOnly)).
				Post("/request", s.routeTeamRequest)
			r.With(guard(s.coordinationOnly),
				guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/requests/{id}", s.routeDecideTeamRequest)
			r.With(guard(s.coordinationOnly),
				guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/group", s.routeCreateTeam)
			// …and correcting one. Name and perimeter were frozen at
			// creation, and neither is a decision that stays right.
			r.With(guard(s.coordinationOnly),
				guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/group/{id}", s.routeUpdateTeam)
			r.With(guard(s.managers),
				guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/account", s.routeCreateAccount)
			r.With(guard(s.managers),
				guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/account/{email}/active", s.routeToggleAccount)
			r.With(guard(s.coordinationOnly),
				guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/account/{email}/role", s.routeChangeRole)
			// Drawing a new password for somebody who lost theirs. A WRITE
			// ceiling and not the password one: nothing is verified here, a
			// password is drawn — the same act as opening an access, which
			// this route sits beside and shares its filter with.
			r.With(guard(s.managers),
				guard(s.limitAccount(limitWriteAccount))).
				Post("/account/{email}/password", s.routeResetPassword)
			// This team's own message templates, over its campaign's. A LEAD
			// and not a manager: `s.managers` also admits coordination, which
			// belongs to no team and would be writing into a row that is not
			// there — its own texts are one route below.
			r.With(guard(s.leadOnly),
				guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
				Post("/templates", s.routeTeamTemplates)
		})
		r.With(guard(s.coordinationOnly),
			guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
			Post("/campaign", s.routeUpdateCampaign)
		// The logo is its own route, not a tenth campaign field: a campaign
		// body at every one of its ceilings already weighs 94 616 bytes of
		// the 131 072 a body may carry, and an image does not fit in what
		// is left.
		// The campaign's own message templates. A route of its own for the
		// same reason as the logo below: the campaign body already weighs
		// 94 616 bytes at its ceilings.
		r.With(guard(s.coordinationOnly),
			guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
			Post("/campaign/templates", s.routeCampaignTemplates)
		r.With(guard(s.coordinationOnly),
			guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
			Post("/campaign/logo", s.routeUploadLogo)
		r.With(guard(s.coordinationOnly),
			guard(s.limitAccount(limitWriteAccount))).
			Delete("/campaign/logo", s.routeDeleteLogo)

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
		// The way back into a campaign nobody can enter. Same scope and same
		// ceiling: it writes one account, exactly as creation does.
		r.With(guard(s.administrationOnly),
			guard(s.limitAccount(limitWriteAccount)), guard(jsonOnly)).
			Post("/admin/campaigns/{slug}/coordination", s.routeGrantCoordination)
		// The public directory of the hosted campaigns, apex only. Names
		// and addresses are public by construction: every subdomain answers.
		// Public does not mean free: this one queries the database, is the
		// front door of the instance, and nothing behind it identifies the
		// caller — the same ceiling as /api/config, which it was already
		// documented as sharing.
		// instanceOnly FIRST. The other way round, a request to this apex-only
		// route on any campaign subdomain answered 404 and still spent a
		// token — and the bucket is keyed by source address, shared with
		// /api/config. One `<img src="https://une-campagne.paraphe.org/api/campaigns">`
		// on a third-party page therefore drained a visitor's own apex
		// ceiling, one hit for one, cross-origin and free. Refusing the host
		// costs nothing: openScope answers an unknown one without taking a
		// pool connection.
		r.With(guard(s.instanceOnly), guard(s.limitIP(limitAnonIP))).
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
// script, no analytics. The policy says so, so that injecting third-party
// content fails instead of executing.
//
// ONE exception, and it is named rather than opened: the campaign logos,
// which are served by the object store's own origin. That origin is a
// deployment setting, so the policy is assembled once at startup instead of
// being a constant. Left unset, nothing is added and the policy is what it
// always was.
func securityHeaders(next http.Handler) http.Handler {
	policy := contentSecurityPolicy(mediaOrigin())
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

// contentSecurityPolicy assembles the policy. Split out from the middleware
// so that it can be read at one glance and tested for what it lets through.
// The media origin is named TWICE, and the second one is not symmetry.
// `img-src` is how the header shows a logo. `connect-src` is how the
// account-less version served under /navigateur/ downloads one: it inlines
// the bytes as a data URI, because that mode promises nothing leaves the
// browser and a remote address in its header would make that false at every
// load. Left out, the campaign was adopted without its mark, in silence —
// the fetch fails in the console and the code treats a missing logo as
// costing the picture and nothing else. Published on a static host, that
// same build has no policy at all and has always worked.
//
// It buys one origin, the operator's own object store, for one call. What is
// still refused there is everything that matters: no script, no frame, no
// form, and `default-src 'self'` over the rest.
func contentSecurityPolicy(media string) string {
	images := "img-src 'self' data:"
	connect := "connect-src 'self'"
	if media != "" {
		images += " " + media
		connect += " " + media
	}
	const policy = "default-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; " + // React sets style attributes
		"%s; " +
		"%s; " +
		"form-action 'self'; " +
		"base-uri 'none'; " +
		"frame-ancestors 'none'"
	return fmt.Sprintf(policy, images, connect)
}

// mediaOrigin: the object store's public origin, or nothing.
//
// A REFUSED value yields nothing at all. Forwarding it — which an earlier
// version did, so that this and the startup check could not disagree — is
// how an operator's `* ; script-src *` became a policy allowing scripts
// from anywhere, on a process that started clean. The two agree by calling
// the SAME function (MediaOrigin, api/media.go), not by echoing the same
// unchecked string; and this side fails closed, since a policy without the
// media origin costs a picture while a widened one costs everything.
func mediaOrigin() string {
	origin, err := MediaOrigin(Get("media_public_url"))
	if err != nil {
		slog.Error("the media origin is refused and left out of the "+
			"Content-Security-Policy: campaign logos will not load",
			"error", err)
		return ""
	}
	return origin
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
