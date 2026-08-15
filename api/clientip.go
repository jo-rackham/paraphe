package main

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

// Who is asking — for the rate limiter, and for nothing else.
//
// The address is an INPUT here, never a record: it is reduced to an aggregate,
// keyed through HMAC (limiter.go) and forgotten when the window closes. No raw
// client address is stored or logged anywhere in this package — the pseudonym
// in the security events is as far as an address travels.

// trustedProxies: the networks allowed to speak for their clients.
//
// X-Forwarded-For is a plain request header: anyone can send one. It only
// means something when the DIRECT peer is a proxy the operator declared, so
// the default — an empty list — is "believe nobody" and every request is
// attributed to its TCP peer. A deployment behind a TLS proxy or an ingress
// declares that hop; DEPLOYMENT.md and the chart carry the usual values.
type trustedProxies []netip.Prefix

func parseTrustedProxies(csv string) (trustedProxies, error) {
	var nets trustedProxies
	for _, part := range strings.Split(csv, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			// refused at startup, not skipped: a typo'd CIDR silently
			// dropped would mean every client shares the proxy's bucket
			return nil, fmt.Errorf("trusted_proxies: %q is not a CIDR "+
				"(expected forms like 10.0.0.0/8): %w", part, err)
		}
		nets = append(nets, p.Masked())
	}
	return nets, nil
}

func (t trustedProxies) contains(a netip.Addr) bool {
	for _, p := range t {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// maxForwardedEntries bounds how much attacker-shaped header is parsed. A
// chain longer than this is not a topology anyone runs; it is a request
// padding its own attribution.
const maxForwardedEntries = 64

// clientAggregate returns the subject a per-IP limit counts: the IPv4
// address, or the IPv6 /64. An IPv6 subscriber holds an entire /64, so
// counting single addresses there would hand out 2⁶⁴ fresh buckets for free.
//
// The value is canonical text ("203.0.113.7", "2001:db8:1:2::/64"), fed to
// the limiter's HMAC and to the log pseudonym — never emitted as is.
func clientAggregate(r *http.Request, trusted trustedProxies) string {
	return aggregate(clientAddr(r, trusted))
}

func aggregate(a netip.Addr) string {
	a = a.Unmap()
	if a.Is4() {
		return a.String()
	}
	// Masked cannot fail here: 64 ≤ 128, and an invalid Addr is caught by
	// the caller falling back to the peer address.
	p, err := a.Prefix(64)
	if err != nil {
		return a.String()
	}
	return p.String()
}

// clientAddr resolves the client behind any declared proxies.
//
// Walk X-Forwarded-For from the RIGHT: each trusted hop appended its own
// peer, so the rightmost entry outside the trusted networks is the client
// the outermost trusted proxy actually talked to. Everything left of it is
// hearsay the client sent along, and is never read past that point.
func clientAddr(r *http.Request, trusted trustedProxies) netip.Addr {
	peer := peerAddr(r)
	if !peer.IsValid() || !trusted.contains(peer) {
		return peer
	}
	entries := forwardedEntries(r)
	for i := len(entries) - 1; i >= 0; i-- {
		a, err := parseForwarded(entries[i])
		if err != nil {
			// A trusted proxy appends the address it accepted, which always
			// parses; an entry that does not was written by the client. The
			// chain of custody stops here, and the request is attributed to
			// the nearest hop we can still name — the peer.
			return peer
		}
		if !trusted.contains(a) {
			return a
		}
	}
	// every entry is a declared proxy (or there were none): the peer it is
	return peer
}

func peerAddr(r *http.Request) netip.Addr {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		// net/http always sets host:port; anything else (a test's bare
		// value, a unix socket) has no client address to offer
		a, err := netip.ParseAddr(r.RemoteAddr)
		if err != nil {
			return netip.Addr{}
		}
		return a.Unmap()
	}
	return ap.Addr().Unmap()
}

func forwardedEntries(r *http.Request) []string {
	var entries []string
	for _, header := range r.Header.Values("X-Forwarded-For") {
		for _, e := range strings.Split(header, ",") {
			entries = append(entries, strings.TrimSpace(e))
			if len(entries) > maxForwardedEntries {
				// longer than any real topology: attribute to the peer
				// rather than walk a list built to be walked
				return nil
			}
		}
	}
	return entries
}

// parseForwarded reads one X-Forwarded-For entry. Proxies write a bare
// address; some (nginx with a port, IPv6 in brackets) write host:port.
func parseForwarded(entry string) (netip.Addr, error) {
	if a, err := netip.ParseAddr(entry); err == nil {
		return a.Unmap(), nil
	}
	ap, err := netip.ParseAddrPort(entry)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("unreadable forwarded entry")
	}
	return ap.Addr().Unmap(), nil
}
