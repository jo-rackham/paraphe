package main

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"hash"
	"strconv"
	"strings"

	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/scrypt"
)

// Password hashes in the werkzeug format: "method$salt$hex".
//
// The method travels with the hash, so the scheme can evolve without
// invalidating existing accounts — a volunteer's account is not something
// to invalidate silently. We write scrypt, we can still read pbkdf2.
//
// scrypt:32768:8:1 = 32 MiB per verification. That is the point: a password
// passed on by voice ("colline-verger-42") only holds if each attempt is
// expensive.

const (
	scryptN      = 1 << 15
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 64
	saltLength   = 16
)

const saltAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// scryptGate bounds concurrent derivations. Each one holds 32 MiB by
// design, and /api/session is public: without a bound, two dozen
// anonymous sign-in attempts held 771 MiB at once — an OOM kill on the
// small VPS the deployment doc recommends, during the endorsement window.
// Waiters queue instead of failing, memory stays bounded at 128 MiB, and
// the queue applies identically to a real account and to the decoy hash,
// so it opens no timing channel.
var scryptGate = make(chan struct{}, 4)

func deriveScrypt(password, salt string, n, r, p int) ([]byte, error) {
	scryptGate <- struct{}{}
	defer func() { <-scryptGate }()
	return scrypt.Key([]byte(password), []byte(salt), n, r, p, scryptKeyLen)
}

// Same gate: nothing writes pbkdf2 hashes today, but the reader keeps
// supporting werkzeug imports, and a million sha256 iterations with
// unbounded concurrency is the same public amplifier. `defer` like its
// sibling — a token released only on the happy path would be lost for
// good to a panic the HTTP server recovers, and four losses would wedge
// every sign-in with no trace.
func derivePbkdf2(password, salt string, iterations, size int,
	factory func() hash.Hash) []byte {
	scryptGate <- struct{}{}
	defer func() { <-scryptGate }()
	return pbkdf2.Key([]byte(password), []byte(salt), iterations, size, factory)
}

// HashPassword produces a werkzeug-format scrypt hash.
func HashPassword(password string) (string, error) {
	salt, err := randomSalt(saltLength)
	if err != nil {
		return "", err
	}
	sum, err := deriveScrypt(password, salt, scryptN, scryptR, scryptP)
	if err != nil {
		return "", fmt.Errorf("scrypt: %w", err)
	}
	return fmt.Sprintf("scrypt:%d:%d:%d$%s$%s",
		scryptN, scryptR, scryptP, salt, hex.EncodeToString(sum)), nil
}

// VerifyPassword compares in constant time. An unreadable hash returns an
// error — never "wrong password": the distinction would otherwise be lost,
// and a corrupted scheme would look like a wave of mistyped passwords.
func VerifyPassword(stored, password string) (bool, error) {
	parts := strings.SplitN(stored, "$", 3)
	if len(parts) != 3 {
		return false, fmt.Errorf("unreadable hash: %d segment(s) instead of 3",
			len(parts))
	}
	method, salt, expected := parts[0], parts[1], parts[2]
	args := strings.Split(method, ":")

	var sum []byte
	switch args[0] {
	case "scrypt":
		n, r, p := scryptN, scryptR, scryptP
		if len(args) == 4 {
			var err error
			if n, err = strconv.Atoi(args[1]); err != nil {
				return false, fmt.Errorf("scrypt: unreadable N parameter (%q)", args[1])
			}
			if r, err = strconv.Atoi(args[2]); err != nil {
				return false, fmt.Errorf("scrypt: unreadable r parameter (%q)", args[2])
			}
			if p, err = strconv.Atoi(args[3]); err != nil {
				return false, fmt.Errorf("scrypt: unreadable p parameter (%q)", args[3])
			}
		} else if len(args) != 1 {
			return false, fmt.Errorf("scrypt expects 3 parameters, got %d",
				len(args)-1)
		}
		var err error
		// the derived length is not in the hash: werkzeug uses the
		// hashlib.scrypt default (64 bytes)
		sum, err = deriveScrypt(password, salt, n, r, p)
		if err != nil {
			return false, fmt.Errorf("scrypt: %w", err)
		}
	case "pbkdf2":
		name, iterations := "sha256", 1000000
		if len(args) >= 2 {
			name = args[1]
		}
		if len(args) >= 3 {
			var err error
			if iterations, err = strconv.Atoi(args[2]); err != nil {
				return false, fmt.Errorf("pbkdf2: unreadable iteration count (%q)", args[2])
			}
		}
		factory, size, err := digest(name)
		if err != nil {
			return false, err
		}
		sum = derivePbkdf2(password, salt, iterations, size, factory)
	default:
		return false, fmt.Errorf("unknown hash method: %q", args[0])
	}

	return subtle.ConstantTimeCompare(
		[]byte(hex.EncodeToString(sum)), []byte(expected)) == 1, nil
}

func digest(name string) (func() hash.Hash, int, error) {
	switch name {
	case "sha256":
		return sha256.New, sha256.Size, nil
	case "sha512":
		return sha512.New, sha512.Size, nil
	case "sha1":
		return sha1.New, sha1.Size, nil
	}
	return nil, 0, fmt.Errorf("pbkdf2 hash: unsupported algorithm %q", name)
}

func randomSalt(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("drawing the salt: %w", err)
	}
	var b strings.Builder
	for _, o := range raw {
		// rand.Read is uniform over 256 values, the alphabet has 62: the
		// modulo leans very slightly towards the first 8 characters. Of no
		// consequence for a salt, whose only job is to be unique.
		b.WriteByte(saltAlphabet[int(o)%len(saltAlphabet)])
	}
	return b.String(), nil
}

// ReadablePassword: can be passed on by voice, no ambiguous characters.
// The words stay French: the password is read aloud to French volunteers.
func ReadablePassword() (string, error) {
	words := strings.Split(
		"colline|rivage|tilleul|sillon|clocher|verger|prairie|falaise", "|")
	a, err := randomInt(len(words))
	if err != nil {
		return "", err
	}
	b, err := randomInt(len(words))
	if err != nil {
		return "", err
	}
	n, err := randomInt(90)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%d", words[a], words[b], n+10), nil
}

func randomInt(n int) (int, error) {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return 0, fmt.Errorf("random draw: %w", err)
	}
	v := int(raw[0])<<24 | int(raw[1])<<16 | int(raw[2])<<8 | int(raw[3])
	if v < 0 {
		v = -v
	}
	return v % n, nil
}
