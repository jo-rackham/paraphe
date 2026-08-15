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

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/scrypt"
)

// Password hashes in the werkzeug format: "method$salt$hex".
//
// The method travels with the hash, so the scheme can evolve without
// invalidating existing accounts — a volunteer's account is not something to
// invalidate silently. We write argon2id, we can still read scrypt and
// pbkdf2, and a hash in an older scheme is replaced the next time its owner
// signs in (NeedsRehash).
//
// argon2id, and the "quantum-resistant" of the request needs saying plainly:
// no password hash is broken by a quantum computer in the way RSA is.
// Grover halves the exponent of a brute-force search and that is all, so what
// protects a password read aloud over the phone ("colline-verger-42") is the
// COST of one attempt — which is exactly what a memory-hard function buys and
// what a quantum machine does not help with. argon2id over scrypt is the
// current recommendation rather than a change of kind: same memory-hardness,
// plus a side-channel-resistant first pass.
//
// 32 MiB and three passes per verification. The memory is deliberately the
// same as the scrypt it replaces, because hashGate bounds four derivations at
// once and 4 × 32 MiB = 128 MiB is what the deployment's memory limit was
// sized for.

const (
	argonTime    = 3
	argonMemory  = 32 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 64

	scryptN      = 1 << 15
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 64
	saltLength   = 16
)

const saltAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// hashGate bounds concurrent derivations, whichever scheme they use. Each
// one holds 32 MiB by design, and /api/session is public: without a bound,
// two dozen anonymous sign-in attempts held 771 MiB at once — an OOM kill on
// the small VPS the deployment doc recommends, during the endorsement
// window. Waiters queue instead of failing, memory stays bounded at 128 MiB,
// and the queue applies identically to a real account and to the decoy hash,
// so it opens no timing channel.
var hashGate = make(chan struct{}, 4)

func deriveArgon2id(password, salt string, time, memory uint32,
	threads uint8) []byte {
	hashGate <- struct{}{}
	defer func() { <-hashGate }()
	return argon2.IDKey([]byte(password), []byte(salt), time, memory,
		threads, argonKeyLen)
}

func deriveScrypt(password, salt string, n, r, p int) ([]byte, error) {
	hashGate <- struct{}{}
	defer func() { <-hashGate }()
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
	hashGate <- struct{}{}
	defer func() { <-hashGate }()
	return pbkdf2.Key([]byte(password), []byte(salt), iterations, size, factory)
}

// currentMethod: what HashPassword writes today. NeedsRehash compares
// against it, so the two cannot drift.
var currentMethod = fmt.Sprintf("argon2id:%d:%d:%d",
	argonTime, argonMemory, argonThreads)

// HashPassword produces a werkzeug-format argon2id hash.
func HashPassword(password string) (string, error) {
	salt, err := randomSalt(saltLength)
	if err != nil {
		return "", err
	}
	sum := deriveArgon2id(password, salt, argonTime, argonMemory, argonThreads)
	return fmt.Sprintf("%s$%s$%s",
		currentMethod, salt, hex.EncodeToString(sum)), nil
}

// NeedsRehash: this hash is readable, but not in the scheme and parameters
// written today. The password is only known during a successful sign-in, so
// that is the one moment an old hash can be replaced — see routeSignIn.
//
// Compared against the whole method segment, parameters included: raising
// argonMemory later must upgrade existing hashes too, and a comparison on
// the algorithm alone would leave every account on the old cost for ever.
func NeedsRehash(stored string) bool {
	method, _, ok := strings.Cut(stored, "$")
	return !ok || method != currentMethod
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
	case "argon2id":
		// The parameters are read from the hash and not from the constants:
		// raising them must not lock out the accounts hashed under the old
		// ones. That is the whole reason the method travels with the hash.
		if len(args) != 4 {
			return false, fmt.Errorf("argon2id expects 3 parameters, got %d",
				len(args)-1)
		}
		t, err := strconv.ParseUint(args[1], 10, 32)
		if err != nil {
			return false, fmt.Errorf("argon2id: unreadable time parameter (%q)", args[1])
		}
		m, err := strconv.ParseUint(args[2], 10, 32)
		if err != nil {
			return false, fmt.Errorf("argon2id: unreadable memory parameter (%q)", args[2])
		}
		p, err := strconv.ParseUint(args[3], 10, 8)
		if err != nil {
			return false, fmt.Errorf("argon2id: unreadable parallelism parameter (%q)", args[3])
		}
		// A hash claiming a gigabyte would let one sign-in attempt take the
		// process down, and /api/session is public. Nothing but this code
		// writes these, so a value above what it writes is a corrupted row
		// or a planted one — either way it is refused, not honoured.
		if t == 0 || t > argonTime || m == 0 || m > argonMemory ||
			p == 0 || p > argonThreads {
			return false, fmt.Errorf(
				"argon2id: parameters %d:%d:%d exceed what this build writes (%d:%d:%d)",
				t, m, p, argonTime, argonMemory, argonThreads)
		}
		sum = deriveArgon2id(password, salt, uint32(t), uint32(m), uint8(p))
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
