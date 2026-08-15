package main

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// The token is signed, not encrypted: anyone can read the address it
// carries. What they must not be able to do is change it.
func TestSessionRefusesWrongSignature(t *testing.T) {
	s := NewSessions([]byte("test key"))
	t0 := time.Unix(1786000000, 0)
	token := session(t, s, "marie@exemple.fr", t0).Cookies()[0].Value
	parts := strings.Split(token, ".")

	other := NewSessions([]byte("other key"))
	forged, err := other.mint("coordination@exemple.fr", 1, t0)
	if err != nil {
		t.Fatal(err)
	}
	forgedParts := strings.Split(forged, ".")

	for name, value := range map[string]string{
		"claims from another key":  forgedParts[0] + "." + forgedParts[1] + "." + parts[2],
		"signature of another key": parts[0] + "." + parts[1] + "." + forgedParts[2],
		"truncated signature":      parts[0] + "." + parts[1] + "." + parts[2][:len(parts[2])-1],
		"no signature":             parts[0] + "." + parts[1],
		"a fourth segment":         token + ".extra",
		"empty":                    "",
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: value})
		if _, _, ok := s.Read(r, t0); ok {
			t.Errorf("%s: token accepted", name)
		}
	}
}

// jwt: build a token with an arbitrary header and claims, signed with `key`
// under HMAC-SHA512. This is the attacker's toolbox, and it has to be able
// to produce shapes the minting code never would.
func jwt(t *testing.T, header, payload string, key []byte) string {
	t.Helper()
	b := func(s string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(s))
	}
	signingInput := b(header) + "." + b(payload)
	m := hmac.New(sha512.New, key)
	m.Write([]byte(signingInput))
	return signingInput + "." +
		base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Every classic way a JWT verifier is talked out of verifying. These are
// what justify writing the verifier by hand instead of taking a library and
// remembering to pass WithValidMethods.
func TestSessionRefusesTheClassicJWTForgeries(t *testing.T) {
	const key = "test key"
	s := NewSessions([]byte(key))
	t0 := time.Unix(1786000000, 0)
	exp := t0.Add(SessionDuration).Unix()
	ok := func(extra string) string {
		return `{"iss":"paraphe","sub":"marie@exemple.fr","aud":"1","exp":` +
			strconv.FormatInt(exp, 10) + `,"iat":` +
			strconv.FormatInt(t0.Unix(), 10) + extra + `}`
	}

	// a control: the same builder, with the header and claims we do emit,
	// must be ACCEPTED — otherwise every case below passes for free
	if _, _, accepted := s.verify(
		jwt(t, `{"alg":"HS512","typ":"JWT"}`, ok(""), []byte(key)), t0,
	); !accepted {
		t.Fatal("the control token is refused: every case below would pass " +
			"whatever the verifier does")
	}

	for name, token := range map[string]string{
		// alg: none — the token carries no signature and asks to be believed
		"alg none": base64.RawURLEncoding.EncodeToString(
			[]byte(`{"alg":"none","typ":"JWT"}`)) + "." +
			base64.RawURLEncoding.EncodeToString([]byte(ok(""))) + ".",
		// alg confusion: an RS256 token whose "signature" is an HMAC taken
		// with the HMAC key, which a verifier reading alg would try to check
		// as a public-key signature
		"alg RS256, signed with the HMAC key": jwt(t,
			`{"alg":"RS256","typ":"JWT"}`, ok(""), []byte(key)),
		"alg HS256 instead of HS512": jwt(t,
			`{"alg":"HS256","typ":"JWT"}`, ok(""), []byte(key)),
		"no alg at all": jwt(t, `{"typ":"JWT"}`, ok(""), []byte(key)),
		"empty header":  jwt(t, `{}`, ok(""), []byte(key)),
		"lowercase hs512": jwt(t,
			`{"alg":"hs512","typ":"JWT"}`, ok(""), []byte(key)),
		// kid / jku: a verifier that resolves a key from the header can be
		// pointed at one the attacker controls
		"kid in the header": jwt(t,
			`{"alg":"HS512","typ":"JWT","kid":"../../dev/null"}`, ok(""), []byte(key)),
		"jku in the header": jwt(t,
			`{"alg":"HS512","jku":"https://ailleurs.example/keys","typ":"JWT"}`,
			ok(""), []byte(key)),
		// same claims, different key order: the header is compared byte for
		// byte, so a token minted by anything but this code is refused
		"header keys reordered": jwt(t,
			`{"typ":"JWT","alg":"HS512"}`, ok(""), []byte(key)),

		// claims
		"another campaign in aud": jwt(t, `{"alg":"HS512","typ":"JWT"}`,
			`{"iss":"paraphe","sub":"marie@exemple.fr","aud":"2","exp":`+
				strconv.FormatInt(exp, 10)+`,"iat":`+
				strconv.FormatInt(t0.Unix(), 10)+`}`, []byte("other key")),
		"the maintenance scope": jwt(t, `{"alg":"HS512","typ":"JWT"}`,
			`{"iss":"paraphe","sub":"marie@exemple.fr","aud":"-1","exp":`+
				strconv.FormatInt(exp, 10)+`,"iat":`+
				strconv.FormatInt(t0.Unix(), 10)+`}`, []byte(key)),
		"aud is not a number": jwt(t, `{"alg":"HS512","typ":"JWT"}`,
			`{"iss":"paraphe","sub":"marie@exemple.fr","aud":"campagne","exp":`+
				strconv.FormatInt(exp, 10)+`,"iat":`+
				strconv.FormatInt(t0.Unix(), 10)+`}`, []byte(key)),
		"another issuer": jwt(t, `{"alg":"HS512","typ":"JWT"}`,
			`{"iss":"ailleurs","sub":"marie@exemple.fr","aud":"1","exp":`+
				strconv.FormatInt(exp, 10)+`,"iat":`+
				strconv.FormatInt(t0.Unix(), 10)+`}`, []byte(key)),
		"no subject": jwt(t, `{"alg":"HS512","typ":"JWT"}`,
			`{"iss":"paraphe","sub":"","aud":"1","exp":`+
				strconv.FormatInt(exp, 10)+`,"iat":`+
				strconv.FormatInt(t0.Unix(), 10)+`}`, []byte(key)),
		"no expiry": jwt(t, `{"alg":"HS512","typ":"JWT"}`,
			`{"iss":"paraphe","sub":"marie@exemple.fr","aud":"1","iat":`+
				strconv.FormatInt(t0.Unix(), 10)+`}`, []byte(key)),
		"issued well in the future": jwt(t, `{"alg":"HS512","typ":"JWT"}`,
			`{"iss":"paraphe","sub":"marie@exemple.fr","aud":"1","exp":`+
				strconv.FormatInt(exp, 10)+`,"iat":`+
				strconv.FormatInt(t0.Add(time.Hour).Unix(), 10)+`}`, []byte(key)),
		// a claim this code does not know was minted by something else
		"an unknown claim": jwt(t, `{"alg":"HS512","typ":"JWT"}`,
			ok(`,"role":"coordination"`), []byte(key)),
		// DisallowUnknownFields guards the INSIDE of the object; Decode
		// stops at the end of the first value and says nothing about what
		// follows it
		"a second document after the claims": jwt(t,
			`{"alg":"HS512","typ":"JWT"}`,
			ok("")+`{"iss":"ailleurs"}`, []byte(key)),
		// time.Unix overflows on MaxInt64 into a date in the PAST, so the
		// "not issued in the future" check passed
		"issued at the end of time": jwt(t, `{"alg":"HS512","typ":"JWT"}`,
			`{"iss":"paraphe","sub":"marie@exemple.fr","aud":"1","exp":`+
				strconv.FormatInt(exp, 10)+`,"iat":9223372036854775807}`, []byte(key)),
		"issued after its own expiry": jwt(t, `{"alg":"HS512","typ":"JWT"}`,
			`{"iss":"paraphe","sub":"marie@exemple.fr","aud":"1","exp":`+
				strconv.FormatInt(exp, 10)+`,"iat":`+
				strconv.FormatInt(exp+1, 10)+`}`, []byte(key)),
		"issued at zero": jwt(t, `{"alg":"HS512","typ":"JWT"}`,
			`{"iss":"paraphe","sub":"marie@exemple.fr","aud":"1","exp":`+
				strconv.FormatInt(exp, 10)+`,"iat":0}`, []byte(key)),
		"claims are not an object": jwt(t,
			`{"alg":"HS512","typ":"JWT"}`, `"marie@exemple.fr"`, []byte(key)),
	} {
		if _, _, accepted := s.verify(token, t0); accepted {
			t.Errorf("%s: token accepted", name)
		}
	}
}

// The signature runs over the WHOLE cookie before anything is parsed, so an
// unbounded one makes the verification the amplifier. A browser caps a
// cookie at about 4 kB; a scripted client caps nothing.
func TestSessionRefusesAnOversizedToken(t *testing.T) {
	s := NewSessions([]byte("test key"))
	t0 := time.Unix(1786000000, 0)
	token, err := s.mint("marie@exemple.fr", 1, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) > maxToken/4 {
		t.Errorf("a real token is %d bytes, close to the %d-byte bound: the "+
			"bound is no longer generous", len(token), maxToken)
	}
	if _, _, ok := s.verify(token+strings.Repeat("A", maxToken), t0); ok {
		t.Error("a megabyte of padding was hashed and read")
	}
}

// The skew exists so that a token minted a moment ahead by a neighbouring
// pod does not log someone out. It must not become a way to post-date one.
func TestSessionToleratesASecondOfSkewAndNotAnHour(t *testing.T) {
	s := NewSessions([]byte("test key"))
	t0 := time.Unix(1786000000, 0)
	token, err := s.mint("marie@exemple.fr", 1, t0.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.verify(token, t0); !ok {
		t.Error("a token minted 30 s ahead by another pod was refused")
	}
	token, err = s.mint("marie@exemple.fr", 1, t0.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.verify(token, t0); ok {
		t.Error("a token minted two minutes ahead was accepted")
	}
}

// The token is a JWT, which is the point of using one: something other than
// this package can read it. Checked on the shape rather than by pulling in a
// library to prove it.
func TestSessionMintsAReadableJWT(t *testing.T) {
	s := NewSessions([]byte("test key"))
	t0 := time.Unix(1786000000, 0)
	token, err := s.mint("marie@exemple.fr", 7, t0)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a compact JWS: %d segments", len(parts))
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("header not base64url: %v", err)
	}
	var h struct{ Alg, Typ string }
	if err := json.Unmarshal(header, &h); err != nil {
		t.Fatalf("header not JSON: %v", err)
	}
	if h.Alg != "HS512" || h.Typ != "JWT" {
		t.Errorf("header says alg=%q typ=%q", h.Alg, h.Typ)
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("claims not base64url: %v", err)
	}
	var cl claims
	if err := json.Unmarshal(body, &cl); err != nil {
		t.Fatalf("claims not JSON: %v", err)
	}
	if cl.Sub != "marie@exemple.fr" || cl.Aud != "7" || cl.Iss != jwtIssuer {
		t.Errorf("claims read back as %+v", cl)
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

// A deactivated account answers sign-in EXACTLY as a wrong password does.
// The branch is reached only once the password verified, so a distinct
// answer confirmed the credential is live — to whoever holds it, which in
// the situation deactivation exists for is the person the account was taken
// away from. The decoy hash already bought this silence for "does this
// address have an account"; the branch one step later gave it back.
func TestDeactivatedSignInIsIndistinguishableFromAWrongPassword(t *testing.T) {
	s, srv := testServer(t)
	email := "ecartee@exemple.fr"
	password := createAccount(t, s, email, RoleVolunteer, nil)
	execAsMaintenance(t, s,
		"UPDATE accounts SET active=FALSE WHERE email=$1", email)

	answer := func(address, secret string) (int, string) {
		c := newClient(t, srv)
		code, body := c.call(http.MethodPost, "/api/session",
			map[string]string{"email": address, "password": secret})
		return code, body["error"].(string)
	}

	codeOff, wordsOff := answer(email, password)     // right password, off
	codeWrong, wordsWrong := answer(email, "pas-ça") // wrong password
	codeGhost, wordsGhost := answer("nul@part.fr", password)

	if codeOff != http.StatusUnauthorized {
		t.Errorf("a deactivated account answers %d where a wrong password "+
			"answers 401: the difference says the password is the right one",
			codeOff)
	}
	if wordsOff != wordsWrong || wordsOff != wordsGhost {
		t.Errorf("three different sentences for one refusal:\n  deactivated: %q"+
			"\n  wrong password: %q\n  no such account: %q",
			wordsOff, wordsWrong, wordsGhost)
	}
	if codeWrong != http.StatusUnauthorized || codeGhost != http.StatusUnauthorized {
		t.Errorf("wrong=%d ghost=%d, want 401 for both", codeWrong, codeGhost)
	}
}
