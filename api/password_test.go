package main

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Hashes produced by werkzeug — the library whose format this file
// implements — for the password below. They are pinned here: the day the
// Go verification drifts, an existing database becomes inaccessible all
// at once, and that is exactly the kind of failure nobody diagnoses
// mid-campaign.
const (
	referencePassword = "colline-verger-42"

	scryptHash = "scrypt:32768:8:1$Fxk3GDIkd8pVKXuo$" +
		"75011ee14d8fc36230352a953e689b3ce49ceab9a5323665210d0a23177b1e9f" +
		"d319d5570c86cbc5705bb2089d1e3a52db00f91ffbaed016eeda3c73780919b4"

	pbkdf2Hash = "pbkdf2:sha256:1000$rmz3vX7EmHEAtUjf$" +
		"d1aee1dd2a27dac8f15b0ae6728787d530290130a33a0bbcbc8b6903d8760134"

	// What this build writes. Pinned for the same reason as the two above,
	// pointing the other way: those guard against losing the ability to read
	// what werkzeug wrote, this one against drifting away from what we write
	// ourselves — a changed parameter or a changed key length locks every
	// account out at once, and the failure looks like everyone mistyping.
	argon2Hash = "argon2id:3:32768:1$Fxk3GDIkd8pVKXuo$" +
		"cf066d84b98c1d86f781bb90ac9953cf91a2459bdae2343c1132dffe7a6d2fbb" +
		"e192b0eb8fd874eb74676fbc8e9b81d47a13ce9ec11c6e89445b1c6e3a333208"
)

func TestVerifiesWerkzeugHash(t *testing.T) {
	for name, hash := range map[string]string{
		"scrypt":   scryptHash,
		"pbkdf2":   pbkdf2Hash,
		"argon2id": argon2Hash,
	} {
		ok, err := VerifyPassword(hash, referencePassword)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !ok {
			t.Errorf("%s: the correct password is refused", name)
		}
		ok, err = VerifyPassword(hash, referencePassword+"x")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ok {
			t.Errorf("%s: a wrong password is accepted", name)
		}
	}
}

func TestHashVerifyRoundTrip(t *testing.T) {
	hash, err := HashPassword(referencePassword)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword(hash, referencePassword)
	if err != nil || !ok {
		t.Fatalf("hash not read back: ok=%v err=%v (%s)", ok, err, hash)
	}
	other, err := HashPassword(referencePassword)
	if err != nil {
		t.Fatal(err)
	}
	if other == hash {
		t.Error("two hashes of the same password are identical: salt not drawn")
	}
}

// What HashPassword writes is argon2id, and it says so in the hash. Read
// from the produced string rather than restated here: the point is that the
// scheme travelling with the hash is the scheme that was used.
func TestHashPasswordWritesArgon2id(t *testing.T) {
	hash, err := HashPassword(referencePassword)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "argon2id:") {
		t.Fatalf("a new hash is not argon2id: %s", hash)
	}
	if NeedsRehash(hash) {
		t.Errorf("a hash just written is already stale: %s", hash)
	}
}

// A hash in an older scheme is replaced the next time its owner signs in.
// Without that, accounts created before argon2id would keep their scrypt
// hash until someone changed the password — which, for a volunteer's account
// handed out once by a lead, is never.
func TestNeedsRehashSeesAnOlderScheme(t *testing.T) {
	for name, tc := range map[string]struct {
		hash string
		want bool
	}{
		"scrypt":                  {scryptHash, true},
		"pbkdf2":                  {pbkdf2Hash, true},
		"argon2id, current cost":  {argon2Hash, false},
		"argon2id, cheaper cost":  {"argon2id:1:8192:1$sel$abcd", true},
		"argon2id, no parameters": {"argon2id$sel$abcd", true},
		"not a hash at all":       {"", true},
	} {
		if got := NeedsRehash(tc.hash); got != tc.want {
			t.Errorf("%s: NeedsRehash = %v, want %v", name, got, tc.want)
		}
	}
}

// The parameters are read FROM the hash, so a row claiming a gigabyte would
// let one anonymous sign-in attempt take the process down — /api/session is
// public and needs no account to reach. Nothing but this code writes these
// hashes, so anything above what it writes is a corrupted row or a planted
// one, and it is refused rather than honoured.
func TestArgon2ParametersAboveWhatWeWriteAreRefused(t *testing.T) {
	for name, hash := range map[string]string{
		"a gigabyte of memory": "argon2id:3:1048576:1$sel$abcd",
		"a thousand passes":    "argon2id:1000:32768:1$sel$abcd",
		"sixty-four threads":   "argon2id:3:32768:64$sel$abcd",
		"zero memory":          "argon2id:3:0:1$sel$abcd",
		"zero passes":          "argon2id:0:32768:1$sel$abcd",
		"missing parameters":   "argon2id:3$sel$abcd",
		"unreadable memory":    "argon2id:3:beaucoup:1$sel$abcd",
	} {
		ok, err := VerifyPassword(hash, referencePassword)
		if err == nil {
			t.Errorf("%s: accepted without error (ok=%v)", name, ok)
		}
		if ok {
			t.Errorf("%s: the password verified against it", name)
		}
	}
	// …and a cheaper hash that this code DID write once stays readable, or
	// raising the cost would lock out every account it was raised for
	cheap := "argon2id:1:8192:1$" + strings.Repeat("a", 16) + "$"
	sum := deriveArgon2id(referencePassword, strings.Repeat("a", 16), 1, 8192, 1)
	ok, err := VerifyPassword(cheap+hex.EncodeToString(sum), referencePassword)
	if err != nil || !ok {
		t.Errorf("a hash written under a lower cost no longer verifies: "+
			"ok=%v err=%v", ok, err)
	}
}

// A broken hash must NEVER pass for "wrong password": conflating them would
// turn a corrupted database into a wave of mistyped passwords, without a
// single log line.
func TestUnreadableHashReturnsError(t *testing.T) {
	for _, tc := range []string{
		"", "n'importe quoi", "scrypt$sel", "md5$sel$abcd",
		"scrypt:x:8:1$sel$abcd", "pbkdf2:md4:1000$sel$abcd",
	} {
		if _, err := VerifyPassword(tc, referencePassword); err == nil {
			t.Errorf("hash %q accepted without error", tc)
		}
	}
}

func TestReadablePassword(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		m, err := ReadablePassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(m) < 12 {
			t.Errorf("password too short: %q", m)
		}
		seen[m] = true
	}
	if len(seen) < 40 {
		t.Errorf("only %d distinct passwords out of 50: suspicious draw",
			len(seen))
	}
}

// Each derivation holds 32 MiB (scrypt) or a million iterations (pbkdf2)
// and /api/session is public: two dozen anonymous sign-in attempts held
// 771 MiB at once — an OOM kill on the small VPS the deployment doc
// recommends. The gate queues them four at a time; saturating it by hand
// proves each path WAITS for a slot instead of allocating beside the
// others. VerifyPassword is the path an anonymous request reaches — a
// first version of this test drove only HashPassword, and removing the
// gate from the one branch that justifies it left the suite green.
func TestDerivationsAreGated(t *testing.T) {
	stored, err := HashPassword("colline-verger-42")
	if err != nil {
		t.Fatal(err)
	}
	for name, derive := range map[string]func() error{
		"hashing a new password": func() error {
			_, err := HashPassword("colline-verger-42")
			return err
		},
		"verifying against argon2id": func() error {
			ok, err := VerifyPassword(stored, "colline-verger-42")
			if err == nil && !ok {
				return fmt.Errorf("the right password did not verify")
			}
			return err
		},
		// still reached: an account created before argon2id keeps its scrypt
		// hash until its owner next signs in, and that sign-in is the very
		// request the gate protects
		"verifying against scrypt": func() error {
			ok, err := VerifyPassword(scryptHash, referencePassword)
			if err == nil && !ok {
				return fmt.Errorf("the right password did not verify")
			}
			return err
		},
		"verifying against pbkdf2": func() error {
			// the werkzeug hash the reader keeps supporting
			_, err := VerifyPassword(
				"pbkdf2:sha256:2000$abcdefgh$"+strings.Repeat("ab", 32), "x")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			for range cap(hashGate) {
				hashGate <- struct{}{}
			}
			done := make(chan error, 1)
			go func() { done <- derive() }()
			select {
			case <-done:
				t.Fatal("a derivation ran with the gate saturated: memory is " +
					"unbounded again")
			case <-time.After(100 * time.Millisecond):
			}
			for range cap(hashGate) {
				<-hashGate
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("derivation failed once the gate opened: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("derivation never ran after the gate opened")
			}
		})
	}
}
