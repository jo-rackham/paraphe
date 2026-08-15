package main

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Session: a JWT in a cookie, stateless server-side.
//
// The token is a compact JWS any library can read, and the choice of
// algorithm is the point rather than a detail.
//
// HS512 is symmetric, so it is post-quantum by construction: Grover halves
// the effective strength of a brute-force search and nothing else applies.
// RS256 or ES256 would make the posture WORSE, not better — Shor breaks
// asymmetric signatures outright — and the post-quantum signature schemes
// that replace them (ML-DSA and friends) produce 2 to 4 kB, which is not a
// thing to put in a cookie. Asymmetric signing buys one property: a third
// party verifying without being able to mint. Here the same service does
// both, so it buys nothing and costs the security margin.
//
// The margin is bounded by the KEY, not by the digest: 64 random bytes give
// 512 bits, hence ~256 after Grover. A 32-byte key — what
// `openssl rand -hex 32` produces and what deployments before this carry —
// gives ~128, which is ample; SessionSecret draws 64 for new instances and
// DEPLOYMENT.md asks operators for the same.
//
// Immediate revocation does not come from the token and never did: it comes
// from re-reading the account from the database on every request (auth.go),
// so a deactivated account loses access on the very next call, whatever the
// token's lifetime.
const (
	SessionCookieName = "paraphe_session"
	SessionDuration   = 12 * time.Hour

	jwtIssuer = "paraphe"
	// The header, emitted as a fixed string and compared as one.
	//
	// Every classic JWT break lives in a verifier that READS `alg` out of
	// the token and then decides what to do: `alg: none`, an RS256 token
	// verified with the HMAC key taken for a public key, a `kid` or `jku`
	// pointing the verifier at a key of the attacker's choosing. A verifier
	// that never reads the header cannot be talked into any of them.
	//
	// This is also why the header is not marshalled from a struct: what is
	// emitted and what is compared are then the same bytes, with no
	// key-ordering or escaping question in between.
	jwtHeader = `{"alg":"HS512","typ":"JWT"}`
	// Clock skew tolerated on `iat`. Several pods mint tokens, and a token
	// minted a moment "in the future" by a neighbour must not log someone
	// out. `exp` gets no such indulgence.
	jwtSkew = 60 * time.Second
)

// The cookie is Secure, HttpOnly and SameSite=Lax, and none of the three is
// configurable — a setting for any of them is a setting an operator can get
// wrong once, in the direction where nothing appears to break.
//
// Secure costs nothing locally: browsers treat http://localhost and
// http://127.0.0.1 as secure contexts and accept the cookie there. What it
// forbids is a deployment on plain HTTP under a real host name, where the
// session token travels readable to anyone on the path.
//
// HttpOnly keeps the token out of reach of JavaScript — the interface never
// reads it, it only rides along — so an XSS cannot carry the session away
// with it.
type Sessions struct {
	key []byte
}

// claims: the registered ones and nothing else.
//
// `aud` carries the campaign, and that is not decoration: a session is valid
// for ONE organisation. Without it the guarantee would rest solely on
// cookies being partitioned by host — true today, since no Domain attribute
// is set, but invisible in the code and therefore lost the day someone adds
// one.
type claims struct {
	Iss string `json:"iss"`
	Sub string `json:"sub"`
	Aud string `json:"aud"`
	Exp int64  `json:"exp"`
	Iat int64  `json:"iat"`
}

func NewSessions(key []byte) *Sessions {
	return &Sessions{key: key}
}

func (s *Sessions) sign(signingInput string) string {
	m := hmac.New(sha512.New, s.key)
	m.Write([]byte(signingInput))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// mint builds the compact JWS. Exported behaviour is Set; this is separate
// so the tests can forge neighbours of a real token.
func (s *Sessions) mint(email string, org int, now time.Time) (string, error) {
	body, err := json.Marshal(claims{
		Iss: jwtIssuer,
		Sub: email,
		Aud: strconv.Itoa(org),
		Exp: now.Add(SessionDuration).Unix(),
		Iat: now.Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("serialising the session: %w", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString([]byte(jwtHeader)) +
		"." + base64.RawURLEncoding.EncodeToString(body)
	return signingInput + "." + s.sign(signingInput), nil
}

// Set writes the session cookie. A volunteer's session has no business
// surviving a week on a shared computer: 12 h.
func (s *Sessions) Set(w http.ResponseWriter, email string, org int,
	now time.Time) error {
	token, err := s.mint(email, org, now)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  now.Add(SessionDuration),
		MaxAge:   int(SessionDuration / time.Second),
	})
	return nil
}

func (s *Sessions) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		MaxAge: -1,
	})
}

// Read returns the address and campaign carried by a valid, unexpired
// cookie. Every anomaly — absence, a header that is not ours, a wrong
// signature, an expiry, a campaign that is not a campaign — yields the same
// result: nobody. There is nothing an attacker can learn from which of them
// it was.
func (s *Sessions) Read(r *http.Request, now time.Time) (string, int, bool) {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return "", 0, false
	}
	return s.verify(c.Value, now)
}

// maxToken bounds what is hashed. HMAC-SHA512 runs over the whole cookie
// BEFORE anything is parsed, and a cookie is up to 4 kB per browser but
// nothing stops a scripted client sending a megabyte of them: the signature
// check would then be the amplifier. A real token is under 400 bytes.
const maxToken = 4096

func (s *Sessions) verify(token string, now time.Time) (string, int, bool) {
	if len(token) > maxToken {
		return "", 0, false
	}
	// Exactly three segments. Counting "at least three" is how a fourth one
	// gets ignored, and a JWS with a fourth segment is not a JWS.
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", 0, false
	}
	// The header is compared, never parsed. See jwtHeader.
	if parts[0] != base64.RawURLEncoding.EncodeToString([]byte(jwtHeader)) {
		return "", 0, false
	}
	signingInput := parts[0] + "." + parts[1]
	// Constant time: a byte-by-byte comparison leaks how much of a forged
	// signature was right, which is enough to build the rest one byte at a
	// time.
	if !hmac.Equal([]byte(parts[2]), []byte(s.sign(signingInput))) {
		return "", 0, false
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", 0, false
	}
	// DisallowUnknownFields: a claim this code does not know is a claim it
	// cannot honour, and a token carrying one was minted by something else.
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var cl claims
	if err := dec.Decode(&cl); err != nil {
		return "", 0, false
	}
	// A second document after the claims is not part of them. Decode stops
	// at the end of the first value and says nothing about what follows, so
	// `{...}{"iss":"elsewhere"}` passed with the first object read and the
	// second ignored — DisallowUnknownFields guards the inside of the
	// object, not what comes after it.
	if dec.More() {
		return "", 0, false
	}

	if cl.Iss != jwtIssuer || cl.Sub == "" {
		return "", 0, false
	}
	if cl.Exp == 0 || now.Unix() >= cl.Exp {
		return "", 0, false
	}
	// Bounded on BOTH sides. `iat` at MaxInt64 overflows time.Unix into a
	// date in the past, so the "not in the future" check passed and the
	// token read as issued long ago. A token issued before its own expiry
	// is the only shape that means anything.
	if cl.Iat <= 0 || cl.Iat >= cl.Exp ||
		time.Unix(cl.Iat, 0).After(now.Add(jwtSkew)) {
		return "", 0, false
	}
	org, err := strconv.Atoi(cl.Aud)
	// OrgMaintenance crosses campaigns and no HTTP request may ever reach
	// it. It cannot be reached through a token either — signing one would
	// take the key, but the refusal is written here rather than assumed.
	if err != nil || org < 0 {
		return "", 0, false
	}
	return cl.Sub, org, true
}
