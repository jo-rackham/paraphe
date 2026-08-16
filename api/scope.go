package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// One HTTP request = one transaction, opened on the scope its Host header
// designates.
//
// The wall between campaigns is the `org_id` predicate that every query on
// a walled table binds — as $1, by the single constructor scoped(r).
// PostgreSQL enforces nothing itself (row-level security was a second wall
// and was removed): two tests carry the whole guarantee.
// TestEveryQueryOnAWalledTableNamesTheCampaign reads the package as an AST
// and demands the predicate per table alias, and TestNoCampaignSeesAnother
// runs two campaigns against every route.

const scopeKey contextKey = 1

type Scope struct {
	Tx pgx.Tx
	// Org: the campaign being served. Nil on the instance scope (the apex).
	Org *Org
	// OrgID: the same scope as a number — including the sentinels, where
	// Org is nil: 0 on the apex. Every query on a walled table binds it:
	// that predicate IS the wall.
	OrgID int

	conn *pgxpool.Conn
	// committed: the route already committed the transaction. Writes do it
	// themselves, so they can return an error BEFORE having answered 200 —
	// a commit performed afterwards by the wrapper could no longer tell the
	// client anything.
	committed bool
	// released: the connection has already gone back to the pool, and
	// inScope's deferred close must not release it a second time.
	released bool
}

func scopeOf(r *http.Request) *Scope {
	p, _ := r.Context().Value(scopeKey).(*Scope)
	return p
}

// tx: the request's transaction. Every read and every write goes through
// it — never through s.pool, which would carry no declared scope.
func (s *Server) tx(r *http.Request) pgx.Tx { return scopeOf(r).Tx }

// orgOf: the campaign being served, nil on the apex.
func orgOf(r *http.Request) *Org {
	if p := scopeOf(r); p != nil {
		return p.Org
	}
	return nil
}

// scopeOrg: the request's scope as a number, to bind in every query touching
// a walled table. The apex is 0 — a scope that owns no campaign row.
//
// A request that never went through inScope has NO scope, and answering 0
// there would quietly serve the instance scope instead of failing. Every
// route that queries goes through inScope, so this cannot happen; if it ever
// does, it must be loud. `s.tx(r)` panics on the same condition, one line
// later, for the same reason.
func scopeOrg(r *http.Request) int {
	p := scopeOf(r)
	if p == nil {
		panic("scopeOrg outside a scope: this request never went through " +
			"inScope, and binding a default campaign would hide it")
	}
	return p.OrgID
}

// commit closes a write's transaction. Call it before replying.
func (s *Server) commit(r *http.Request) error {
	p := scopeOf(r)
	if err := p.Tx.Commit(r.Context()); err != nil {
		return err
	}
	p.committed = true
	return nil
}

func (p *Scope) close(ctx context.Context) {
	if p.released {
		return
	}
	if !p.committed {
		// read-only, or interrupted write: rolling back is the right default
		if err := p.Tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.Warn("transaction not rolled back", "error", err)
		}
	}
	p.released = true
	p.conn.Release()
}

// renew commits what the request has written so far and opens a fresh
// transaction ON THE SAME CONNECTION.
//
// One route needs it, and the reason is worth stating because both obvious
// alternatives are wrong. Spending a sign-in token must be FINAL the instant
// the token is read — everything after it can fail, and a failure that rolls
// the DELETE back hands a live link to whoever presented it. But the answer
// still needs the account and its departments, so the request still needs a
// transaction afterwards.
//
// Committing late was the first shape, and a failed commit undid the spend.
// Taking a SECOND connection from the pool was the second, and it deadlocks:
// the request already holds one, so enough simultaneous redemptions all wait
// for a connection that none of them will release — measured, eight
// concurrent redemptions against a pool of four hung until they timed out.
// This costs nothing: the connection is already ours.
//
// The commit does NOT run under the request's context. Cancelling it is the
// one failure the caller controls: closing the connection between the DELETE
// and the commit cancels the commit, PostgreSQL rolls the DELETE back, and
// the link just presented is live again — a replay obtained on demand, and
// then kept for the day the account it was refused against is reactivated.
// Detached from the cancellation, bounded on its own so a connection that
// died for real cannot pin the transaction.
func (s *Server) renew(r *http.Request) error {
	p := scopeOf(r)
	ctx, cancel := context.WithTimeout(
		context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	if err := p.Tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing before continuing: %w", err)
	}
	tx, err := p.conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("reopening a transaction: %w", err)
	}
	p.Tx = tx
	return nil
}

// release hands the connection back BEFORE the handler has finished.
//
// It exists for one shape: a route that has committed and then does slow
// work with the outside world — sending an invitation through an SMTP relay,
// bounded at 30 s. Held until the handler returns, that connection is idle
// in the pool for the whole exchange, and pgx opens max(4, NumCPU) of them:
// a lead opening accounts one after another while a relay drags would pin
// them one by one, and every authenticated request would queue behind. The
// same failure jsonOnly already describes for a dribbled request body.
//
// After it, s.tx(r) hands back a closed transaction — which is the point:
// there is nothing left to query, and a query written after this should
// fail loudly rather than half-work.
func (s *Server) release(r *http.Request) {
	if p := scopeOf(r); p != nil {
		p.close(r.Context())
	}
}

// reacquire takes a connection again, after release handed one back.
//
// One route needs it: uploading a logo writes the object BEFORE it moves the
// pointer — the other order publishes a URL that answers 404, and for ever
// if the write fails — and that object goes to another machine. Holding a
// pool connection across that round trip is what let a store which stopped
// answering take every connection an instance had, readiness probe
// included: measured, six probes out of six lost. So the route hands its
// connection back, writes the object, and asks for one again.
//
// Where `renew` above warns that taking a SECOND connection deadlocks, this
// is the opposite shape and safe for that very reason: the first one is
// already back in the pool, so the caller waits holding nothing. Reversing
// them — asking before releasing — is the deadlock, which is why this
// refuses outright unless release has run.
func (s *Server) reacquire(r *http.Request) error {
	p := scopeOf(r)
	if p == nil || !p.released {
		return errors.New("reacquire without release: the request would " +
			"wait for a connection while holding one")
	}
	conn, err := s.pool.Acquire(r.Context())
	if err != nil {
		return fmt.Errorf("taking a connection back: %w", err)
	}
	tx, err := conn.Begin(r.Context())
	if err != nil {
		conn.Release()
		return fmt.Errorf("reopening a transaction: %w", err)
	}
	p.conn, p.Tx = conn, tx
	p.released, p.committed = false, false
	return nil
}

// inScope resolves the campaign and opens the transaction. Routes depending
// on it do not run when the scope is unknown: better a readable 404 than an
// arbitrary campaign served by accident.
func (s *Server) inScope(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.openScope(w, r)
		if !ok {
			return
		}
		defer p.close(r.Context())
		next(w, r.WithContext(context.WithValue(r.Context(), scopeKey, p)))
	}
}

// instanceOnly: the apex routes (hosting request, moderation). Served from a
// campaign subdomain they make no sense, and would suggest a campaign can
// create other campaigns.
func (s *Server) instanceOnly(next http.HandlerFunc) http.HandlerFunc {
	return s.inScope(func(w http.ResponseWriter, r *http.Request) {
		if orgOf(r) != nil {
			errorJSON(w, http.StatusNotFound,
				"Cette page vit sur %s, pas sur le site d'une campagne.",
				BaseDomain())
			return
		}
		next(w, r)
	})
}

// campaignOnly: a campaign's own public routes — no session, but a campaign
// to write into. The mirror of instanceOnly, and the reason it exists apart
// from inCampaign: that one requires an account, and the team request form
// is answered for a visitor who has none yet.
func (s *Server) campaignOnly(next http.HandlerFunc) http.HandlerFunc {
	return s.inScope(func(w http.ResponseWriter, r *http.Request) {
		if orgOf(r) == nil {
			errorJSON(w, http.StatusNotFound,
				"Cette page appartient à une campagne, pas à %s.", BaseDomain())
			return
		}
		next(w, r)
	})
}

func (s *Server) openScope(w http.ResponseWriter, r *http.Request) (*Scope, bool) {
	ctx := r.Context()
	base := BaseDomain()

	var slug string
	instance := false
	if base == "" {
		// single-campaign: every host designates the bootstrap campaign
		slug = s.bootstrapSlug
	} else {
		scope, ok := ScopeOfHost(r.Host, base)
		if !ok {
			errorJSON(w, http.StatusNotFound,
				"%q ne correspond à aucune campagne hébergée ici.", r.Host)
			return nil, false
		}
		slug, instance = scope.Slug, scope.Instance
	}

	// The body is drained into memory BEFORE a pooled connection is taken. On
	// the sign-in route jsonOnly does this ahead of inScope, but every other
	// write route reaches inScope first: without this, a client dribbling its
	// body held a connection idle-in-transaction for ReadTimeout, and enough
	// such sockets exhausted the pool for every campaign on the instance.
	if !bufferBody(w, r) {
		return nil, false
	}

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		s.failure(w, err)
		return nil, false
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		conn.Release()
		s.failure(w, err)
		return nil, false
	}
	p := &Scope{Tx: tx, conn: conn}

	org := OrgInstance
	if !instance {
		o, err := s.ReadOrg(ctx, tx, slug)
		if err != nil {
			p.close(ctx)
			s.failure(w, err)
			return nil, false
		}
		if o == nil {
			p.close(ctx)
			s.unknownCampaign(w, slug, base)
			return nil, false
		}
		if o.State != OrgActive {
			p.close(ctx)
			// no name here: this refusal happens before the handler, so it is
			// also the body /api/campaign/public serves to EVERY origin — the
			// same reason that route's 409 stays generic
			errorJSON(w, http.StatusServiceUnavailable,
				"Cette campagne est suspendue. Son travail est conservé ; "+
					"contactez l'administration de l'instance.")
			return nil, false
		}
		p.Org, org = o, o.ID
	}
	p.OrgID = org
	return p, true
}

// unknownCampaign: the message differs with the mode, because the cause
// does. Single-campaign, it is an instance not yet configured; multi-
// campaign, it is a subdomain that matches nothing.
func (s *Server) unknownCampaign(w http.ResponseWriter, slug, base string) {
	if base == "" {
		errorJSON(w, http.StatusServiceUnavailable,
			"Aucune campagne n'est configurée sur cette instance. Renseignez "+
				"config/campagne.yaml ou les variables PARAPHE_*, puis "+
				"redémarrez.")
		return
	}
	errorJSON(w, http.StatusNotFound,
		"Aucune campagne « %s » sur %s.", slug, base)
}
