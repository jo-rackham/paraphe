package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func session(t *testing.T, s *Sessions, email string, when time.Time) *http.Request {
	t.Helper()
	w := httptest.NewRecorder()
	if err := s.Set(w, email, 1, when); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
	return r
}

func TestSessionRoundTrip(t *testing.T) {
	s := NewSessions([]byte("test key"))
	t0 := time.Unix(1786000000, 0)
	read, org, ok := s.Read(session(t, s, "marie@exemple.fr", t0), t0)
	if !ok || read != "marie@exemple.fr" || org != 1 {
		t.Fatalf("session not read back: %q org=%d %v", read, org, ok)
	}
}

func TestSessionExpires(t *testing.T) {
	s := NewSessions([]byte("test key"))
	t0 := time.Unix(1786000000, 0)
	r := session(t, s, "marie@exemple.fr", t0)
	if _, _, ok := s.Read(r, t0.Add(SessionDuration-time.Minute)); !ok {
		t.Error("session refused before its term")
	}
	if _, _, ok := s.Read(r, t0.Add(SessionDuration)); ok {
		t.Error("session accepted after its term")
	}
}

// The cookie is signed, not encrypted: anyone can read the address it
// carries. What they must not be able to do is change it.
func TestSessionRefusesWrongSignature(t *testing.T) {
	s := NewSessions([]byte("test key"))
	t0 := time.Unix(1786000000, 0)
	r := session(t, s, "marie@exemple.fr", t0)
	raw := r.Cookies()[0].Value
	body, signature, _ := strings.Cut(raw, ".")

	other := NewSessions([]byte("other key"))
	w := httptest.NewRecorder()
	if err := other.Set(w, "coordination@exemple.fr", 1, t0); err != nil {
		t.Fatal(err)
	}
	forgedBody, _, _ := strings.Cut(w.Result().Cookies()[0].Value, ".")

	for name, value := range map[string]string{
		"swapped body":        forgedBody + "." + signature,
		"truncated signature": body + "." + signature[:len(signature)-1],
		"no signature":        body,
		"empty":               "",
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: value})
		if _, _, ok := s.Read(r, t0); ok {
			t.Errorf("%s: cookie accepted", name)
		}
	}
}

// The three attributes are not configurable, and that is the guarantee: an
// operator cannot forget one. A missing Secure serves the sign-in cookie in
// the clear, and everything keeps working — which is what makes it a
// mistake nobody catches.
//
// Both the cookie that OPENS a session and the one that clears it: a Clear
// without Secure is a Set-Cookie a proxy can strip on the way, and the
// session that should have ended survives.
func TestSessionCookieIsProtected(t *testing.T) {
	s := NewSessions([]byte("test key"))
	for _, tc := range []struct {
		name string
		emit func(w http.ResponseWriter)
	}{
		{"opening", func(w http.ResponseWriter) {
			if err := s.Set(w, "marie@exemple.fr", 1, time.Unix(1786000000, 0)); err != nil {
				t.Fatal(err)
			}
		}},
		{"clearing", func(w http.ResponseWriter) { s.Clear(w) }},
	} {
		w := httptest.NewRecorder()
		tc.emit(w)
		c := w.Result().Cookies()[0]
		if !c.HttpOnly {
			t.Errorf("%s: cookie readable from JavaScript", tc.name)
		}
		if !c.Secure {
			t.Errorf("%s: cookie without Secure — it would go out over plain "+
				"HTTP, which is how a session token is read off the wire",
				tc.name)
		}
		if c.SameSite != http.SameSiteLaxMode {
			t.Errorf("%s: SameSite missing, the cookie would go out on a "+
				"third-party request", tc.name)
		}
	}
}
