package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Session: a signed cookie, stateless server-side.
//
// No JWT: it would be the same thing — a signed token — plus the algorithm-
// confusion pitfalls, and without the only gain that matters. Immediate
// revocation does not come from the token but from re-reading the account
// from the database on every request (see auth.go): a deactivated account
// loses access on the very next call, whatever the cookie's lifetime.
//
// HMAC-SHA256 is symmetric, hence already quantum-resistant: Grover only
// halves the effective strength, and a 256-bit key keeps ~128 bits. Signing
// with RS256/ES256 would on the contrary make the post-quantum posture
// WORSE, since Shor breaks asymmetric schemes.

const (
	SessionCookieName = "paraphe_session"
	SessionDuration   = 12 * time.Hour
)

type Sessions struct {
	key    []byte
	secure bool
}

// The payload carries the campaign: a session is valid for ONE
// organisation. Without it, the guarantee would rest solely on cookies
// being partitioned by host — true today (no Domain attribute is set), but
// invisible in the code, hence lost the day someone adds one.
type payload struct {
	Email string `json:"email"`
	Org   int    `json:"org"`
	Exp   int64  `json:"exp"`
}

func NewSessions(key []byte, secure bool) *Sessions {
	return &Sessions{key: key, secure: secure}
}

func (s *Sessions) sign(data []byte) string {
	m := hmac.New(sha256.New, s.key)
	m.Write(data)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Set writes the session cookie. A volunteer's session has no business
// surviving a week on a shared computer: 12 h.
func (s *Sessions) Set(w http.ResponseWriter, email string, org int,
	now time.Time) error {
	data, err := json.Marshal(payload{
		Email: email, Org: org, Exp: now.Add(SessionDuration).Unix()})
	if err != nil {
		return fmt.Errorf("serialising the session: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(data)
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    body + "." + s.sign([]byte(body)),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  now.Add(SessionDuration),
		MaxAge:   int(SessionDuration / time.Second),
	})
	return nil
}

func (s *Sessions) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode,
		MaxAge: -1,
	})
}

// Read returns the address and campaign carried by a valid, unexpired
// cookie. Every anomaly (absence, wrong signature, expiry) yields the same
// result: nobody.
func (s *Sessions) Read(r *http.Request, now time.Time) (string, int, bool) {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return "", 0, false
	}
	body, signature, ok := strings.Cut(c.Value, ".")
	if !ok {
		return "", 0, false
	}
	if !hmac.Equal([]byte(signature), []byte(s.sign([]byte(body)))) {
		return "", 0, false
	}
	data, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", 0, false
	}
	var p payload
	if err := json.Unmarshal(data, &p); err != nil {
		return "", 0, false
	}
	if p.Email == "" || now.Unix() >= p.Exp {
		return "", 0, false
	}
	return p.Email, p.Org, true
}
