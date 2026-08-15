package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func requestFrom(remote string, xff ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func mustProxies(t *testing.T, csv string) trustedProxies {
	t.Helper()
	p, err := parseTrustedProxies(csv)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The default posture: nobody is trusted, so a header anyone can type must
// change nothing. An attacker who could pick their own bucket would never
// meet a ceiling — one spoofed header per attempt is free.
func TestForgedForwardedForIsIgnoredWithoutATrustedProxy(t *testing.T) {
	r := requestFrom("203.0.113.7:4321", "198.51.100.9")
	if got := clientAggregate(r, nil); got != "203.0.113.7" {
		t.Fatalf("attributed to %q: the header outranked the TCP peer, and "+
			"every ceiling is now the caller's choice", got)
	}
}

func TestForwardedForIsReadOnlyBehindATrustedProxy(t *testing.T) {
	trusted := mustProxies(t, "10.0.0.0/8")
	// the proxy (10.0.0.2) appended the client it accepted; what the client
	// itself sent (its lie, 198.51.100.9) sits left of it and is never read
	r := requestFrom("10.0.0.2:33000", "198.51.100.9, 203.0.113.7")
	if got := clientAggregate(r, trusted); got != "203.0.113.7" {
		t.Fatalf("attributed to %q, want the rightmost untrusted entry", got)
	}
}

func TestChainedTrustedProxiesAreWalkedThrough(t *testing.T) {
	trusted := mustProxies(t, "10.0.0.0/8, 192.168.0.0/16")
	// ingress (10.x) behind a front proxy (192.168.x): both declared, both
	// skipped, the client is what the OUTERMOST trusted hop accepted
	r := requestFrom("10.0.0.2:33000", "203.0.113.7, 192.168.1.1")
	if got := clientAggregate(r, trusted); got != "203.0.113.7" {
		t.Fatalf("attributed to %q, want the client behind both proxies", got)
	}
}

func TestAllTrustedFallsBackToThePeer(t *testing.T) {
	trusted := mustProxies(t, "10.0.0.0/8")
	r := requestFrom("10.0.0.2:33000", "10.0.0.9")
	if got := clientAggregate(r, trusted); got != "10.0.0.2" {
		t.Fatalf("attributed to %q, want the peer when every entry is a proxy", got)
	}
}

func TestUnparseableForwardedEntryFallsBackToThePeer(t *testing.T) {
	trusted := mustProxies(t, "10.0.0.0/8")
	r := requestFrom("10.0.0.2:33000", "not-an-address")
	if got := clientAggregate(r, trusted); got != "10.0.0.2" {
		t.Fatalf("attributed to %q: an entry no proxy writes must end the "+
			"chain of custody, not be guessed at", got)
	}
}

// An IPv6 subscriber holds an entire /64: counted per address, every attempt
// could come from a "different" client at zero cost, and the ceilings would
// bound nothing.
func TestIPv6IsAggregatedToItsSixtyFour(t *testing.T) {
	a := clientAggregate(requestFrom("[2001:db8:1:2:aaaa::1]:443"), nil)
	b := clientAggregate(requestFrom("[2001:db8:1:2:bbbb::2]:443"), nil)
	if a != b {
		t.Fatalf("two addresses of one /64 got two buckets (%q, %q): the "+
			"ceiling is free to walk around", a, b)
	}
	if a != "2001:db8:1:2::/64" {
		t.Fatalf("aggregate is %q, want the /64 itself", a)
	}
	other := clientAggregate(requestFrom("[2001:db8:1:3::1]:443"), nil)
	if other == a {
		t.Fatalf("two different /64s share the bucket %q", a)
	}
}

func TestMappedIPv4CountsAsItsIPv4(t *testing.T) {
	mapped := clientAggregate(requestFrom("[::ffff:203.0.113.7]:443"), nil)
	plain := clientAggregate(requestFrom("203.0.113.7:80"), nil)
	if mapped != plain {
		t.Fatalf("the same client counts as %q over IPv6 and %q over IPv4: "+
			"two buckets for one machine", mapped, plain)
	}
}

func TestOverlongForwardedChainFallsBackToThePeer(t *testing.T) {
	trusted := mustProxies(t, "10.0.0.0/8")
	entries := make([]string, maxForwardedEntries+2)
	for i := range entries {
		entries[i] = "10.0.0.9"
	}
	r := requestFrom("10.0.0.2:33000", strings.Join(entries, ", "))
	if got := clientAggregate(r, trusted); got != "10.0.0.2" {
		t.Fatalf("attributed to %q on a chain built to be walked", got)
	}
}

func TestTrustedProxiesRefusesATypo(t *testing.T) {
	if _, err := parseTrustedProxies("10.0.0.0/8, oops"); err == nil {
		t.Fatal("a CIDR nothing can parse was accepted: the operator's typo " +
			"would silently attribute every client to the proxy")
	}
	// a bare address is a common way to write "this one proxy" — and not a
	// prefix, so it must be refused with a readable reason, not guessed
	if _, err := parseTrustedProxies("10.0.0.1"); err == nil {
		t.Fatal("a bare address was accepted as a CIDR")
	}
}

func TestPortedForwardedEntriesParse(t *testing.T) {
	trusted := mustProxies(t, "10.0.0.0/8")
	r := requestFrom("10.0.0.2:33000", "203.0.113.7:61234")
	if got := clientAggregate(r, trusted); got != "203.0.113.7" {
		t.Fatalf("attributed to %q: proxies that append host:port exist", got)
	}
}
