package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestValkeyURLParsing(t *testing.T) {
	for _, tc := range []struct {
		url       string
		sentinels []string
		master    string
		wantErr   string
	}{
		{url: "valkey://127.0.0.1:6379", sentinels: []string{"127.0.0.1:6379"}},
		{url: "valkey://valkey", sentinels: []string{"valkey:6379"}},
		{url: "valkey+sentinel://s1:26379,s2:26379,s3:26379/paraphe",
			sentinels: []string{"s1:26379", "s2:26379", "s3:26379"}, master: "paraphe"},
		{url: "valkey+sentinel://s1,s2/paraphe",
			sentinels: []string{"s1:26379", "s2:26379"}, master: "paraphe"},
		// no master name: the client would have nothing to ask the sentinels
		{url: "valkey+sentinel://s1:26379", wantErr: "master-name"},
		{url: "valkey+sentinel:///paraphe", wantErr: "master-name"},
		// credentials belong in valkey_password, where deployment files can
		// source them from a secret — never in a URL that gets echoed around
		{url: "valkey://:secret@host:6379", wantErr: "valkey_password"},
		{url: "redis://host:6379", wantErr: "expected"},
		{url: "host:6379", wantErr: "expected"},
	} {
		option, err := valkeyOption(tc.url, "pw")
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: error %v, want it to mention %q", tc.url, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.url, err)
			continue
		}
		if strings.Join(option.InitAddress, " ") != strings.Join(tc.sentinels, " ") {
			t.Errorf("%s: addresses %v, want %v", tc.url, option.InitAddress, tc.sentinels)
		}
		if option.Sentinel.MasterSet != tc.master {
			t.Errorf("%s: master %q, want %q", tc.url, option.Sentinel.MasterSet, tc.master)
		}
		if option.Password != "pw" {
			t.Errorf("%s: the password was not carried to the data nodes", tc.url)
		}
		if tc.master != "" && option.Sentinel.Password != "pw" {
			t.Errorf("%s: the password was not carried to the sentinels", tc.url)
		}
	}
}

// Integration: a disposable Valkey named by PARAPHE_TEST_VALKEY_URL —
// `task db` starts one and prints the value. Keys are HMAC bucket names
// under a test secret; nothing here needs flushing.
func testValkey(t *testing.T) *valkeyStore {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("PARAPHE_TEST_VALKEY_URL"))
	if url == "" {
		t.Skip("PARAPHE_TEST_VALKEY_URL not set: a disposable Valkey is required")
	}
	v, err := newValkeyStore(url, os.Getenv("PARAPHE_TEST_VALKEY_PASSWORD"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.close)
	if err := v.ping(context.Background()); err != nil {
		t.Fatalf("valkey does not answer at %s: %v", url, err)
	}
	return v
}

func TestValkeyCountsAtomicallyAndExpires(t *testing.T) {
	v := testValkey(t)
	ctx := context.Background()
	key := "rl:test:" + t.Name()
	for i := 1; i <= 3; i++ {
		total, retryIn, err := v.count(ctx, key, 400*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if total != int64(i) {
			t.Fatalf("event %d counted as %d", i, total)
		}
		if retryIn <= 0 || retryIn > 400*time.Millisecond {
			t.Fatalf("retryIn %s is outside the window", retryIn)
		}
	}
	time.Sleep(500 * time.Millisecond)
	total, _, err := v.count(ctx, key, 400*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("after the TTL the count is %d, want a fresh 1: a counter "+
			"that survives its window refuses its subject for ever", total)
	}
}

func TestValkeyForgetResetsTheWindow(t *testing.T) {
	v := testValkey(t)
	ctx := context.Background()
	key := "rl:test:" + t.Name()
	for range 5 {
		if _, _, err := v.count(ctx, key, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.forget(ctx, key); err != nil {
		t.Fatal(err)
	}
	total, _, err := v.count(ctx, key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("after forget the count is %d, want 1", total)
	}
}

// Two limiters, one Valkey: the reason it exists. Three replicas that each
// allowed the full ceiling would triple it; through the shared store the
// second instance sees the events of the first.
func TestTwoInstancesShareOneCeiling(t *testing.T) {
	v := testValkey(t)
	clock := newFakeClock()
	secret := []byte("shared secret, as SessionSecret guarantees in production")
	one := newRateLimiter(secret, v, clock.now)
	two := newRateLimiter(secret, v, clock.now)
	ctx := context.Background()
	class := limitClass{"shared_" + t.Name(), 4, time.Minute}

	for i := 1; i <= 4; i++ {
		instance := one
		if i%2 == 0 {
			instance = two
		}
		if d := instance.allow(ctx, class, "s"); !d.allowed {
			t.Fatalf("event %d refused below the shared ceiling", i)
		}
	}
	if d := two.allow(ctx, class, "s"); d.allowed {
		t.Fatal("the fifth event passed: the instances do not share their " +
			"counters, and every ceiling is multiplied by the replica count")
	}
	if one.degraded.Load() || two.degraded.Load() {
		t.Fatal("degraded during a healthy run: the shared store errored " +
			"and the test almost proved the wrong thing")
	}
}

// The degradation path against a real refusal to answer: an address nothing
// listens on. The dial fails within valkeyTimeout, the event is still
// counted — in this process — and the limiter says so through its state.
func TestValkeyOutageDegradesButStillCounts(t *testing.T) {
	v, err := newValkeyStore("valkey://127.0.0.1:1", "")
	if err != nil {
		t.Fatal(err)
	}
	defer v.close()
	clock := newFakeClock()
	l := newRateLimiter([]byte("secret"), v, clock.now)
	ctx := context.Background()
	class := limitClass{"outage", 2, time.Minute}

	start := time.Now()
	for i := 1; i <= 2; i++ {
		if d := l.allow(ctx, class, "s"); !d.allowed {
			t.Fatalf("event %d refused during the outage", i)
		}
	}
	if d := l.allow(ctx, class, "s"); d.allowed {
		t.Fatal("the ceiling did not hold during the outage")
	}
	if !l.degraded.Load() {
		t.Fatal("the limiter never noticed the outage")
	}
	// three attempts, each bounded by valkeyTimeout: sign-in latency during
	// an outage is part of the contract, not a detail
	if elapsed := time.Since(start); elapsed > 3*valkeyTimeout+time.Second {
		t.Fatalf("three degraded decisions took %s: the timeout is not "+
			"bounding the round-trip and every sign-in hangs with Valkey", elapsed)
	}
}
