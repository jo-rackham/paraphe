package main

import (
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
)

func TestVerifiesWerkzeugHash(t *testing.T) {
	for name, hash := range map[string]string{
		"scrypt": scryptHash,
		"pbkdf2": pbkdf2Hash,
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
		"verifying against scrypt": func() error {
			ok, err := VerifyPassword(stored, "colline-verger-42")
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
			for range cap(scryptGate) {
				scryptGate <- struct{}{}
			}
			done := make(chan error, 1)
			go func() { done <- derive() }()
			select {
			case <-done:
				t.Fatal("a derivation ran with the gate saturated: memory is " +
					"unbounded again")
			case <-time.After(100 * time.Millisecond):
			}
			for range cap(scryptGate) {
				<-scryptGate
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
