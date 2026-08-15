package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Rate limits.
//
// admitSignIn bounds how many password derivations run AT ONCE — memory.
// These bound how many events run PER WINDOW — abuse. The two are
// complementary and neither replaces the other.
//
// The per-account class deliberately keys on the SUBMITTED address, whether
// or not an account bears it: the counter moves identically for both, so a
// 429 reveals nothing about existence — the property the decoy hash buys on
// the password path (password.go) is preserved on this one. That is what
// makes an account-shaped limit acceptable where a lockout was refused.
//
// The ceilings are constants, not settings: a threshold is a thing an
// operator can get wrong once, in the direction where nothing appears to
// break. They are generous by design — a campaign office behind one NAT must
// never see them — because their job is to stop scripts, not to pace humans.
// The generated-password space is 39.8 bits (password.go): at 20 attempts
// per 10 minutes it stands for millennia.
type limitClass struct {
	name string
	// events allowed per window and per subject
	events int
	window time.Duration
}

var (
	limitSignInIP      = limitClass{"signin_ip", 20, 10 * time.Minute}
	limitSignInAccount = limitClass{"signin_account", 10, 15 * time.Minute}
	limitHostingIP     = limitClass{"hosting_ip", 3, time.Hour}
	limitAnonIP        = limitClass{"anon_ip", 120, time.Minute}
	limitWriteAccount  = limitClass{"write_account", 120, time.Minute}
	limitExportAccount = limitClass{"export_account", 6, 10 * time.Minute}
)

// counterStore counts events per opaque key. Two implementations: this
// process's memory (below), and Valkey (limiter_valkey.go) when several
// instances must share one view.
type counterStore interface {
	// count records one event and answers the window's total so far, with
	// the time left until it resets.
	count(ctx context.Context, key string, window time.Duration) (total int64, retryIn time.Duration, err error)
	// forget drops a key before its window ends — a successful sign-in
	// clears its own account counter.
	forget(ctx context.Context, key string) error
}

// decision: what allow concluded about one event.
type decision struct {
	allowed bool
	// first: this event is the one that crossed the ceiling — the moment
	// worth one log line. Refusals after it are the attack continuing.
	first   bool
	retryIn time.Duration
}

// rateLimiter fronts the two stores. With no shared store it IS the process
// limiter — the explicit single-replica mode. With one, every count goes to
// Valkey; an error there degrades to the process store, said once, and the
// first success returns, said once. Never silent, never blocking: a limiter
// outage must not be able to take sign-in down with it.
type rateLimiter struct {
	shared counterStore // nil = single-replica mode
	local  counterStore
	// key derived from the session secret: bucket names carry an HMAC of
	// the subject, so no address and no submitted email is ever stored —
	// not even in Valkey's short-lived memory.
	key      []byte
	degraded atomic.Bool
}

func newRateLimiter(secret []byte, shared counterStore, now func() time.Time) *rateLimiter {
	return &rateLimiter{
		shared: shared,
		local:  newProcessStore(now),
		key:    deriveKey(secret, "paraphe:rate-limit-buckets:v1"),
	}
}

// deriveKey: a purpose-bound subkey, so the session secret itself signs
// JWTs and nothing else.
func deriveKey(secret []byte, purpose string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(purpose))
	return m.Sum(nil)
}

// bucket: the stored name of (class, subject). 128 HMAC bits: nothing to
// reverse, nothing worth colliding.
func (l *rateLimiter) bucket(class limitClass, subject string) string {
	m := hmac.New(sha256.New, l.key)
	m.Write([]byte(class.name + "\x00" + subject))
	return "rl:" + class.name + ":" + hex.EncodeToString(m.Sum(nil)[:16])
}

func (l *rateLimiter) allow(ctx context.Context, class limitClass, subject string) decision {
	key := l.bucket(class, subject)
	total, retryIn, err := l.count(ctx, key, class.window)
	if err != nil {
		// count already fell back to the process store; an error HERE is
		// the local map failing, which does not happen — refuse nothing.
		slog.Error("rate limiter unavailable, event not counted",
			"class", class.name, "error", err)
		return decision{allowed: true}
	}
	return decision{
		allowed: total <= int64(class.events),
		first:   total == int64(class.events)+1,
		retryIn: retryIn,
	}
}

// count prefers the shared store and degrades OUT LOUD. The transition is
// logged once in each direction; per-request noise would say nothing more.
func (l *rateLimiter) count(ctx context.Context, key string, window time.Duration) (int64, time.Duration, error) {
	if l.shared == nil {
		return l.local.count(ctx, key, window)
	}
	total, retryIn, err := l.shared.count(ctx, key, window)
	if err == nil {
		if l.degraded.CompareAndSwap(true, false) {
			slog.Info("rate limiter restored: Valkey answers again, " +
				"counters are shared across instances")
		}
		return total, retryIn, nil
	}
	if l.degraded.CompareAndSwap(false, true) {
		slog.Error("rate limiter degraded: Valkey unreachable, counting "+
			"in process memory until it returns — ceilings hold per instance, "+
			"not across them", "error", err)
	}
	return l.local.count(ctx, key, window)
}

func (l *rateLimiter) forget(ctx context.Context, class limitClass, subject string) {
	key := l.bucket(class, subject)
	// both stores: after a degraded spell the same key may live in each,
	// and a leftover local counter would refuse a legitimate sign-in
	if l.shared != nil {
		if err := l.shared.forget(ctx, key); err != nil {
			// worst case the account waits out its window; said, not hidden
			slog.Warn("rate limit counter not cleared", "class", class.name,
				"error", err)
		}
	}
	if err := l.local.forget(ctx, key); err != nil {
		slog.Warn("rate limit counter not cleared", "class", class.name,
			"error", err)
	}
}

// processStore: fixed windows in this process's memory. The mode a single
// replica runs — where "per process" and "per deployment" are the same
// thing — and the floor every instance keeps under a Valkey outage.
type processStore struct {
	mu      sync.Mutex
	buckets map[string]*window
	ops     int
	now     func() time.Time
}

type window struct {
	n     int64
	reset time.Time
}

// processStoreCap bounds the map. Distinct keys cost real addresses to an
// attacker (spoofed sources never finish a handshake), so the cap is far
// above any legitimate population; reaching it despite the sweep means a
// flood, and dropping every counter — weakening limits, loudly — beats
// refusing new subjects, which would let that flood lock strangers out.
const processStoreCap = 1 << 18

// sweepEvery: expired windows are reaped in passing, not by a timer — the
// store must not need a goroutine and an owner for its lifecycle.
const sweepEvery = 4096

func newProcessStore(now func() time.Time) *processStore {
	return &processStore{buckets: map[string]*window{}, now: now}
}

func (p *processStore) count(_ context.Context, key string, d time.Duration) (int64, time.Duration, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	p.ops++
	if p.ops%sweepEvery == 0 || len(p.buckets) >= processStoreCap {
		p.sweep(now)
	}
	w := p.buckets[key]
	if w == nil || !now.Before(w.reset) {
		w = &window{reset: now.Add(d)}
		p.buckets[key] = w
	}
	w.n++
	return w.n, w.reset.Sub(now), nil
}

func (p *processStore) sweep(now time.Time) {
	for k, w := range p.buckets {
		if !now.Before(w.reset) {
			delete(p.buckets, k)
		}
	}
	if len(p.buckets) >= processStoreCap {
		slog.Warn("rate limiter reset: more live windows than the process "+
			"store holds — a flood is on, and every counter starts over",
			"windows", len(p.buckets))
		p.buckets = map[string]*window{}
	}
}

func (p *processStore) forget(_ context.Context, key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.buckets, key)
	return nil
}

// --- middleware ---

// limitIP bounds a class per client address (IPv4) or per /64 (IPv6) —
// resolved through the declared proxies, see clientip.go.
func (s *Server) limitIP(class limitClass) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			s.enforce(w, r, class, clientAggregate(r, s.proxies), next)
		}
	}
}

// limitAccount bounds a class per signed-in account. It runs after signedIn:
// wired anywhere else it has no account to read, and that is a bug to hear
// about, not to skip past.
func (s *Server) limitAccount(class limitClass) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			c := accountOf(r)
			if c == nil {
				panic("limitAccount before signedIn: no account to count")
			}
			subject := fmt.Sprintf("%d\x00%s", currentOrg(r), c.Email)
			s.enforce(w, r, class, subject, next)
		}
	}
}

// limitSignInBody bounds sign-in attempts per submitted address. It runs
// after jsonOnly, whose buffered body it re-reads and restores; the campaign
// comes from the Host header alone — no database touched, nothing held
// while the decision is made.
func (s *Server) limitSignInBody(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subject, ok := s.signInSubject(r)
		if !ok {
			// an unreadable body or an unknown host is refused by the
			// layers this middleware sits between; nothing to count yet
			next(w, r)
			return
		}
		s.enforce(w, r, limitSignInAccount, subject, next)
	}
}

// signInSubject: the (campaign, submitted address) pair a sign-in counts
// under. It reads the body jsonOnly buffered, and restores it for the
// handler behind it.
func (s *Server) signInSubject(r *http.Request) (string, bool) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return "", false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var d signInRequest
	if err := json.Unmarshal(raw, &d); err != nil {
		return "", false
	}
	return s.signInSubjectFor(r, strings.ToLower(strings.TrimSpace(d.Email)))
}

// signInSubjectFor builds the same pair from an address already in hand —
// the handler clears the counter with it after a success, when the body is
// long consumed.
func (s *Server) signInSubjectFor(r *http.Request, email string) (string, bool) {
	if email == "" {
		return "", false
	}
	slug := s.bootstrapSlug
	if base := BaseDomain(); base != "" {
		scope, ok := ScopeOfHost(r.Host, base)
		if !ok {
			return "", false
		}
		slug = scope.Slug
		if scope.Instance {
			slug = "instance"
		}
	}
	return slug + "\x00" + email, true
}

func (s *Server) enforce(w http.ResponseWriter, r *http.Request,
	class limitClass, subject string, next http.HandlerFunc) {
	d := s.limiter.allow(r.Context(), class, subject)
	if d.allowed {
		next(w, r)
		return
	}
	if d.first {
		s.securityEvent(r, slog.LevelWarn, "rate_limited", "class", class.name)
	}
	retry := int(math.Ceil(d.retryIn.Seconds()))
	if retry < 1 {
		retry = 1
	}
	w.Header().Set("Retry-After", fmt.Sprintf("%d", retry))
	errorJSON(w, http.StatusTooManyRequests,
		"Trop de tentatives. Réessayez dans %s.", humanDelay(retry))
}

// humanDelay: what the refusal tells a human — coarse on purpose.
func humanDelay(seconds int) string {
	if seconds >= 120 {
		return fmt.Sprintf("%d minutes", (seconds+59)/60)
	}
	if seconds == 1 {
		return "1 seconde"
	}
	return fmt.Sprintf("%d secondes", seconds)
}
