package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A relay answers refusals in its own spelling, and plenty of them quote the
// recipient back: `550 5.1.1 <marie@EXEMPLE.FR>: user unknown`. Logged as
// received, that sentence puts in the log exactly what this file's subject —
// day-scoped pseudonyms — exists to keep out of it, and by a path no test
// that fakes the mailer can reach.
func TestARelayCannotPutAnAddressInTheLog(t *testing.T) {
	s, _ := testServer(t)
	const email = "marie@exemple.fr"
	for _, answer := range []string{
		"SMTP RCPT TO: 550 5.1.1 <marie@exemple.fr>: user unknown",
		"SMTP RCPT TO: 550 5.1.1 <marie@EXEMPLE.FR>: user unknown",
		"SMTP RCPT TO: 550 5.1.1 <MARIE@Exemple.Fr>: mailbox unavailable",
		"550 marie@exemple.fr rejected; 550 marie@EXEMPLE.fr rejected twice",
	} {
		got := s.withoutAddress(errors.New(answer), email)
		if strings.Contains(strings.ToLower(got), email) {
			t.Errorf("the address survived into the log line:\n\tfrom: %s\n\tgot:  %s",
				answer, got)
		}
		if !strings.Contains(got, s.accountPseudonym(email)) {
			t.Errorf("nothing identifies the account any more: %s", got)
		}
	}
}

// A relay that names the recipient WITHOUT its domain says as much: the
// local part is the identifying half, the domain is the campaign's own and
// names nobody. `recipient "marie.dupont": user unknown` used to go into the
// log verbatim, because the pattern asked for the whole address.
func TestARelayNamingOnlyTheLocalPartSaysNoMore(t *testing.T) {
	s, _ := testServer(t)
	const email = "marie.dupont@exemple.fr"
	for _, answer := range []string{
		`550 recipient "marie.dupont": user unknown`,
		`550 5.1.1 MARIE.DUPONT: no such mailbox`,
		"550 <marie.dupont@exemple.fr> unknown; retry as marie.dupont",
	} {
		got := s.withoutAddress(errors.New(answer), email)
		if strings.Contains(strings.ToLower(got), "marie.dupont") {
			t.Errorf("the recipient's name survived into the log line:"+
				"\n\tfrom: %s\n\tgot:  %s", answer, got)
		}
		if !strings.Contains(got, s.accountPseudonym(email)) {
			t.Errorf("nothing identifies the account any more: %s", got)
		}
	}

	// …and the whole address still wins where both could match, so no
	// orphaned `@domain` is left standing beside a pseudonym.
	got := s.withoutAddress(
		errors.New("550 <marie.dupont@exemple.fr> unknown"), email)
	if strings.Contains(got, "@exemple.fr") {
		t.Errorf("the local part was redacted out of the middle of the "+
			"address, leaving its domain behind: %s", got)
	}
}

// Go's `\b` is ASCII-only — `\w` is `[0-9A-Za-z_]` — so `\bhervé\b` has no
// boundary to find after the `é` and never matches. In a French campaign
// that is not an edge case, it is most of the volunteers: the accented name
// the redaction exists for went into the log verbatim, while the ASCII one
// beside it was scrubbed.
func TestAnAccentedNameIsRedactedLikeAnyOther(t *testing.T) {
	s, _ := testServer(t)
	for _, c := range []struct{ email, answer, leaks string }{
		{"hervé@exemple.fr", `550 5.1.1 recipient "hervé": user unknown`, "hervé"},
		{"chloé@exemple.fr", "550 recipient chloé: no such mailbox", "chloé"},
		{"élise@exemple.fr", "550 <élise> unknown", "élise"},
		{"françois@exemple.fr", "550 françois, mailbox full", "françois"},
		// twice in one answer, and the second must go too: consuming the
		// character that bounds the first is how the second stays
		{"hervé@exemple.fr", "550 hervé rejected; hervé rejected twice", "hervé"},
	} {
		got := s.withoutAddress(errors.New(c.answer), c.email)
		if strings.Contains(strings.ToLower(got), c.leaks) {
			t.Errorf("the recipient's name survived into the log line:"+
				"\n\tfrom: %s\n\tgot:  %s", c.answer, got)
		}
	}

	// A whole word, still: a local part is not redacted out of the middle of
	// a longer one, or `connect@` would turn `connection reset` into nonsense
	// and take the operator's only clue with it.
	got := s.withoutAddress(
		errors.New("550 the connection was reset"), "connect@exemple.fr")
	if !strings.Contains(got, "connection was reset") {
		t.Errorf("an ordinary word was eaten from inside another: %s", got)
	}
}

// The same name written the other way round in Unicode.
//
// `é` is one rune (U+00E9) or two (`e` + U+0301), and the two spell the same
// address without sharing a byte. The stored one and the one a relay quotes
// back need not agree — this project already knows the shape, which is why
// the template check normalises before comparing: a `é` pasted from a PDF
// arrives decomposed. Byte against byte, neither pass matched and the WHOLE
// address went into the log, domain included.
func TestTheTwoWaysOfSpellingAnAccentAreTheSameAddress(t *testing.T) {
	s, _ := testServer(t)
	const composed = "hervé@exemple.fr"    // é as one rune
	const decomposed = "hervé@exemple.fr" // e + combining acute
	if composed == decomposed {
		t.Fatal("these are the same bytes, so this test proves nothing")
	}
	for _, c := range []struct{ stored, quoted string }{
		{composed, decomposed},
		{decomposed, composed},
		{composed, composed},
		{decomposed, decomposed},
	} {
		answer := "550 5.1.1 <" + c.quoted + ">: user unknown"
		got := s.withoutAddress(errors.New(answer), c.stored)
		if strings.Contains(got, "@exemple.fr") {
			t.Errorf("the address survived because it was spelled the other "+
				"way:\n\tstored: %q\n\tquoted: %q\n\tgot:    %s",
				c.stored, c.quoted, got)
		}
	}
}

// Lowercasing changes byte LENGTH for some characters — `Ⱦ` is two bytes and
// `ⱦ` is three — so an offset found in a lowercased copy does not address
// the same place in the original. Matching on the copy and slicing the
// original left half an address in the log, and past the end of the string
// it PANICKED — in a detached goroutine, where a panic takes the process
// with it.
func TestARelayAnswerInAnyAlphabetIsSafeToRedact(t *testing.T) {
	s, _ := testServer(t)
	for _, c := range []struct{ answer, email string }{
		// Ⱦ (U+023E, 2 bytes) lowercases to ⱦ (U+2C66, 3 bytes)
		{"Ⱦ user@a.b", "user@a.b"},
		{"at Ⱦ: <user@abc.com> unknown", "user@abc.com"},
		{"ȺȾȺȾ 550 <MARIE@EXEMPLE.FR> rejected", "marie@exemple.fr"},
		{"550 rejected", ""},
		{"İ 550 <a@b.c>", "a@b.c"},
		{strings.Repeat("Ⱦ<a@b.c> ", 50), "a@b.c"},
		// Bytes that are not UTF-8 at all, on both sides. A relay answers
		// whatever it answers, and this runs in a detached goroutine where a
		// panic takes the process with it — normalising and slicing must both
		// survive a truncated sequence and a bare continuation byte.
		{"550 \xff\xfe <a@b.c> unknown", "a@b.c"},
		{"550 \xc3 <a@b.c>", "a@b.c"},
		{"550 <a@b.c> \xe2\x82", "a@b.c"},
		{"550 rejected", "\xffbad@b.c"},
	} {
		got := s.withoutAddress(errors.New(c.answer), c.email)
		if c.email != "" && strings.Contains(strings.ToLower(got), c.email) {
			t.Errorf("the address survived:\n\tfrom: %q\n\tgot:  %q",
				c.answer, got)
		}
		// and a fragment of it must not survive either: the "before" part of
		// the text is where a misaligned offset used to leave one
		if local, _, _ := strings.Cut(c.email, "@"); local != "" &&
			strings.Contains(strings.ToLower(got), local+"@") {
			t.Errorf("part of the address survived:\n\tfrom: %q\n\tgot:  %q",
				c.answer, got)
		}
	}
}

// An operator asking for `log_level=warn` is following what the deployment
// guide tells them to do, and that is exactly the setting under which the
// events worth waking someone for must survive.
//
// They did not. Every log.Printf in the package went through logWriter, which
// calls slog.Info, so at Warn the handler dropped a handler panic, every 500,
// the readiness probe's diagnostic, and a message that literally began with
// "WARNING:". The redirection was written to avoid rewriting call sites; what
// it actually did was flatten their severity to the lowest one.
//
// The redirection stays — a log.Printf from pgx, or from anything else that
// knows nothing of this package, still has to land in the same stream — but
// what THIS package emits says what it is.
func atWarn(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf,
		&slog.HandlerOptions{Level: slog.LevelWarn})))
	oldFlags, oldOut := log.Flags(), log.Writer()
	log.SetFlags(0)
	log.SetOutput(logWriter{})
	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetFlags(oldFlags)
		log.SetOutput(oldOut)
	})
	return &buf
}

// logLevels: the level of every record in the buffer, in order. Named apart
// from the canary's `levels`, which splits SQL by nesting.
func logLevels(t *testing.T, buf *bytes.Buffer) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var record struct{ Level, Msg string }
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("a log line is not one JSON object: %q (%v)", line, err)
		}
		out = append(out, record.Level+" "+record.Msg)
	}
	return out
}

// A panic in a handler answers 500 to the browser. If it says nothing to the
// log as well, the incident exists nowhere at all.
func TestAPanicIsLoggedAtWarnLevel(t *testing.T) {
	buf := atWarn(t)
	handler := answerOnPanic(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { panic("boom") }))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/mayors", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("the panic answered %d, so this test proves nothing about "+
			"the log", w.Code)
	}
	got := logLevels(t, buf)
	if len(got) == 0 {
		t.Fatal("a handler panicked, the client got a 500, and the log is " +
			"empty at level=warn: the incident exists nowhere")
	}
	if !strings.HasPrefix(got[0], "ERROR") {
		t.Errorf("the panic was logged as %q, so a collector alerting on "+
			"WARN+ never sees it", got[0])
	}
}

// The four events an operator would page on, and the one that must NOT wake
// anybody. Read from real call sites rather than restated: the point is that
// the severity travels with the message.
func TestOpsEventsSurviveTheLevelFilter(t *testing.T) {
	for name, emit := range map[string]struct {
		fn   func()
		want string
	}{
		"an internal error behind a 500": {
			func() { slog.Error("internal error", "error", "boom") }, "ERROR"},
		"a template configuration": {
			func() {
				slog.Warn("configuration at template values — messages contain "+
					"example values", "keys", "candidat")
			}, "WARN"},
		"no instance administrator": {
			func() { slog.Warn("NO INSTANCE ADMINISTRATOR") }, "WARN"},
		"a lock that would not release": {
			func() { slog.Warn("lock not released", "lock", 8047) }, "WARN"},
	} {
		t.Run(name, func(t *testing.T) {
			buf := atWarn(t)
			emit.fn()
			got := logLevels(t, buf)
			if len(got) != 1 || !strings.HasPrefix(got[0], emit.want) {
				t.Errorf("emitted %v, want one %s record", got, emit.want)
			}
		})
	}

	// …and the ordinary chatter still does not, or the filter would be
	// pointless
	t.Run("routine progress stays quiet", func(t *testing.T) {
		buf := atWarn(t)
		slog.Info("list unchanged, import skipped", "mayors", 34826)
		if got := logLevels(t, buf); len(got) != 0 {
			t.Errorf("level=warn still emitted %v", got)
		}
	})
}

// The redirection is what catches a log.Printf from a package that knows
// nothing of this one — pgx, or the standard library. It lands as Info, which
// is right for something whose severity nobody declared, and it must still be
// ONE JSON object per line whatever the message contains.
func TestTheStandardLoggerStillLandsInTheStream(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf,
		&slog.HandlerOptions{Level: slog.LevelInfo})))
	oldFlags, oldOut := log.Flags(), log.Writer()
	log.SetFlags(0)
	log.SetOutput(logWriter{})
	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetFlags(oldFlags)
		log.SetOutput(oldOut)
	})

	// a quote, a newline and a comma: what a French sentence and a pgx
	// error look like
	log.Printf("connexion \"refusée\" par l'hôte,\nréessai dans 1s")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("one call produced %d lines: a collector reading line by "+
			"line sees a truncated object\n%s", len(lines), buf.String())
	}
	var record struct{ Level, Msg string }
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("not a JSON object: %q (%v)", lines[0], err)
	}
	if record.Level != "INFO" || !strings.Contains(record.Msg, "refusée") {
		t.Errorf("read back as %+v", record)
	}
}

// A guard that names the sites it protects is a guard that stops protecting
// them the day one is renamed. This one reads the package: what remains of
// the standard logger is the redirection itself, and nothing else.
func TestNoCallSiteUsesTheStandardLoggerAnyMore(t *testing.T) {
	files := apiPackage(t)
	var offenders []string
	for name, file := range files {
		if name == "main.go" {
			// setupLogging installs the redirection: log.SetFlags and
			// log.SetOutput are the two calls that must stay
			continue
		}
		for _, imported := range file.Imports {
			if imported.Path.Value == `"log"` {
				offenders = append(offenders, name)
			}
		}
	}
	if len(offenders) > 0 {
		t.Errorf("these still import the standard logger, so whatever they "+
			"emit is flattened to Info and dropped at level=warn:\n\t%s",
			strings.Join(offenders, "\n\t"))
	}
}
