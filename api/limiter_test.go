package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// a clock the tests move by hand
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestProcessStoreCountsWithinTheWindowAndResetsAfter(t *testing.T) {
	clock := newFakeClock()
	p := newProcessStore(clock.now)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		total, retryIn, err := p.count(ctx, "k", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if total != int64(i) {
			t.Fatalf("event %d counted as %d", i, total)
		}
		if retryIn <= 0 || retryIn > time.Minute {
			t.Fatalf("retryIn %s is outside the window", retryIn)
		}
	}
	clock.advance(time.Minute)
	total, _, err := p.count(ctx, "k", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("after the window closed the count is %d, want a fresh 1: "+
			"a window that never resets refuses its subject for ever", total)
	}
}

func TestProcessStoreForgetClearsOneKeyOnly(t *testing.T) {
	clock := newFakeClock()
	p := newProcessStore(clock.now)
	ctx := context.Background()
	for range 5 {
		if _, _, err := p.count(ctx, "gone", time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, _, err := p.count(ctx, "kept", time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.forget(ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	total, _, _ := p.count(ctx, "gone", time.Minute)
	if total != 1 {
		t.Fatalf("forgotten key counts %d, want 1", total)
	}
	total, _, _ = p.count(ctx, "kept", time.Minute)
	if total != 6 {
		t.Fatalf("the neighbouring key was disturbed: %d, want 6", total)
	}
}

func TestProcessStoreSweepsExpiredWindows(t *testing.T) {
	clock := newFakeClock()
	p := newProcessStore(clock.now)
	ctx := context.Background()
	for i := range 100 {
		if _, _, err := p.count(ctx, fmt.Sprintf("k%d", i), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	clock.advance(2 * time.Minute)
	p.mu.Lock()
	p.sweep(clock.now())
	left := len(p.buckets)
	p.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d expired windows survived the sweep: the map only grows", left)
	}
}

// flakyStore fails until told otherwise — the outage that ends.
type flakyStore struct {
	down  bool
	inner counterStore
}

func (f *flakyStore) count(ctx context.Context, key string, w time.Duration) (int64, time.Duration, error) {
	if f.down {
		return 0, 0, errors.New("connection refused")
	}
	return f.inner.count(ctx, key, w)
}
func (f *flakyStore) forget(ctx context.Context, key string) error {
	if f.down {
		return errors.New("connection refused")
	}
	return f.inner.forget(ctx, key)
}
func (f *flakyStore) refund(ctx context.Context, key string) error {
	if f.down {
		return errors.New("connection refused")
	}
	return f.inner.refund(ctx, key)
}

// A Valkey outage must not take sign-in down with it, and must not turn the
// ceilings off either: the process store keeps counting, and the ceilings
// hold per instance until the shared store answers again.
func TestLimiterDegradesToProcessCountingAndRecovers(t *testing.T) {
	clock := newFakeClock()
	shared := &flakyStore{down: true, inner: newProcessStore(clock.now)}
	l := newRateLimiter([]byte("secret"), shared, clock.now)
	ctx := context.Background()
	class := limitClass{"test", 2, time.Minute}

	for i := 1; i <= 2; i++ {
		if d := l.allow(ctx, class, "s"); !d.allowed {
			t.Fatalf("event %d refused during the outage: degraded must "+
				"still mean counted, not closed", i)
		}
	}
	if d := l.allow(ctx, class, "s"); d.allowed {
		t.Fatal("the ceiling did not hold during the outage: degraded " +
			"turned the limiter off")
	}
	if !l.degraded.Load() {
		t.Fatal("the limiter does not know it is degraded: the transition " +
			"was never observed, so it was never said")
	}

	shared.down = false
	if d := l.allow(ctx, class, "fresh"); !d.allowed {
		t.Fatal("a fresh subject refused right after recovery")
	}
	if l.degraded.Load() {
		t.Fatal("the shared store answers and the limiter still calls " +
			"itself degraded: recovery is silent, which was the one thing " +
			"it must not be")
	}
}

func TestLimiterMarksTheCrossingEventOnce(t *testing.T) {
	clock := newFakeClock()
	l := newRateLimiter([]byte("secret"), nil, clock.now)
	ctx := context.Background()
	class := limitClass{"test", 2, time.Minute}
	for range 2 {
		if d := l.allow(ctx, class, "s"); !d.allowed || d.first {
			t.Fatal("an allowed event flagged as the crossing one")
		}
	}
	d := l.allow(ctx, class, "s")
	if d.allowed || !d.first {
		t.Fatalf("the crossing event: allowed=%v first=%v, want refused and "+
			"flagged — it is the one log line the wave gets", d.allowed, d.first)
	}
	d = l.allow(ctx, class, "s")
	if d.allowed || d.first {
		t.Fatalf("the event after the crossing: allowed=%v first=%v — "+
			"logging every refusal would let the attack write the log", d.allowed, d.first)
	}
}

// The buckets are named by HMAC: neither the submitted email nor the client
// address may appear in the store — Valkey's memory is not a place nominative
// data goes, however short its TTL.
func TestBucketNamesCarryNoSubject(t *testing.T) {
	l := newRateLimiter([]byte("secret"), nil, time.Now)
	subject := "campaign\x00someone@example.fr"
	key := l.bucket(limitSignInAccount, subject)
	if strings.Contains(key, "someone") || strings.Contains(key, "example") {
		t.Fatalf("the bucket name %q carries the subject in the clear", key)
	}
	if !strings.HasPrefix(key, "rl:signin_account:") {
		t.Fatalf("the bucket name %q does not carry its class: two classes "+
			"could share a counter", key)
	}
	// distinct subjects, distinct buckets — and stable across calls
	if l.bucket(limitSignInAccount, subject) != key {
		t.Fatal("the same subject got two bucket names: nothing would ever " +
			"be counted twice")
	}
	if l.bucket(limitSignInAccount, "campaign\x00other@example.fr") == key {
		t.Fatal("two subjects share one bucket")
	}
	if l.bucket(limitSignInIP, subject) == key {
		t.Fatal("two classes share one bucket")
	}
}

// Different secrets must yield different bucket names: pseudonyms are only
// pseudonyms because the key is one of the secrets.
func TestBucketNamesDependOnTheSecret(t *testing.T) {
	a := newRateLimiter([]byte("secret A"), nil, time.Now)
	b := newRateLimiter([]byte("secret B"), nil, time.Now)
	if a.bucket(limitSignInIP, "203.0.113.7") == b.bucket(limitSignInIP, "203.0.113.7") {
		t.Fatal("the bucket name does not depend on the secret: it is a " +
			"plain hash, and a plain hash of an IPv4 space is reversible " +
			"by enumeration")
	}
}

// A refunded attempt leaves NOTHING behind — not a count, and not a window.
//
// Giving the event back was half of it. The bucket stayed, empty, carrying
// the reset it was armed with, and count only arms a window when it finds
// none: the next caller on that key inherits the owner's. So the delay a 429
// hands back is short by exactly the time since the owner signed in, and
// Retry-After is in SECONDS — the refusal's sentence is coarse, the header
// is not. Burn the ceiling for an address you know, read the header, and it
// says both that the address names an account and when its owner last signed
// in. That is the oracle the refund exists to close, reopened through the one
// observable nobody was watching.
//
// The shared store never had it: its count re-arms whenever INCR answers 1,
// which a bucket sitting at zero does. The two stores must be observably the
// same, and this is where they were not.
func TestARefundedAttemptLeavesNoWindowBehind(t *testing.T) {
	const gap = 4 * time.Minute
	class := limitClass{"test", 3, 15 * time.Minute}
	ctx := context.Background()

	// what an anonymous caller reads from the 429 after burning the ceiling
	// for a subject, `gap` after the moment under test
	burn := func(signedIn bool) time.Duration {
		clock := newFakeClock()
		l := newRateLimiter([]byte("secret"), nil, clock.now)
		if signedIn {
			l.allow(ctx, class, "s")
			l.refund(ctx, class, "s")
		}
		clock.advance(gap)
		for range class.events {
			l.allow(ctx, class, "s")
		}
		return l.allow(ctx, class, "s").retryIn
	}

	owner, nobody := burn(true), burn(false)
	if owner != nobody {
		t.Errorf("Retry-After tells the two apart by %s, which is the time "+
			"since the owner signed in: an address that names an account "+
			"answers %s where an address that names none answers %s",
			nobody-owner, owner, nobody)
	}
}

func TestHumanDelaySpeaksCoarsely(t *testing.T) {
	for _, tc := range []struct {
		seconds int
		want    string
	}{
		{1, "1 seconde"}, {45, "45 secondes"}, {119, "119 secondes"},
		{120, "2 minutes"}, {121, "3 minutes"}, {900, "15 minutes"},
	} {
		if got := humanDelay(tc.seconds); got != tc.want {
			t.Errorf("humanDelay(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}
