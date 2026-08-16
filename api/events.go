package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Security events — structured, and carrying pseudonyms only.
//
// An operator needs to SEE a brute-force attempt, a refused wave, an account
// being switched off. They do not need, and must not get, the address or the
// submitted email behind it: this application handles nominative data about
// elected officials and volunteers, and its logs must not quietly become a
// second nominative store. So every subject is reduced to a keyed pseudonym:
//
//   - derived from the session secret, so all instances of a deployment
//     write the SAME pseudonym for the same subject — a wave is visible
//     across three pods;
//   - scoped to the DAY, so pseudonyms cannot be joined across days into a
//     long-term profile;
//   - truncated HMAC-SHA256 — nothing to reverse, no rainbow table to buy.
//
// The in-app rate limiter is the enforcement; these lines are the account of
// it. Nothing here is meant to feed an address back to a ban list.

// securityEvent writes one event, attributing the request to its client
// pseudonym. The message IS the event name, and an `event` field repeats it
// for collectors that filter on fields rather than messages.
func (s *Server) securityEvent(r *http.Request, level slog.Level,
	event string, attrs ...any) {
	all := append([]any{
		"event", event,
		"client", s.logPseudonym("client", clientAggregate(r, s.proxies)),
	}, attrs...)
	slog.Log(r.Context(), level, event, all...)
}

// accountPseudonym: the form a submitted address takes in a log line. Also
// applied to addresses that DO name an account: a team roster is data the
// database holds, not something to reconstitute from log retention.
func (s *Server) accountPseudonym(email string) string {
	return s.logPseudonym("account", email)
}

// withoutAddress: an error on its way to a log line, with the address taken
// out of it.
//
// A relay answers refusals in its own words, and plenty of them quote the
// recipient: `550 5.1.1 <marie@exemple.fr>: user unknown`. Wrapped and
// logged, that sentence puts in the log exactly what this file exists to
// keep out of it — and it arrives by a path no test that fakes the mailer
// can see, because a fake never speaks SMTP.
// Case-INSENSITIVELY, because a relay answers in its own spelling: plenty
// uppercase the domain, some the whole address, and a byte-for-byte replace
// walked straight past `<marie@EXEMPLE.FR>`.
//
// Matched on the ORIGINAL text, never on a lowercased copy of it. Lowering
// changes byte LENGTH for some characters — `Ⱦ` is two bytes and `ⱦ` is
// three — so an offset found in the copy does not address the same place in
// the original: it left half an address in the log, and past the end of the
// string it panicked. In a detached goroutine, which is where this runs,
// that panic takes the process with it.
func (s *Server) withoutAddress(err error, email string) string {
	if err == nil {
		return ""
	}
	if email == "" {
		return err.Error()
	}
	// QuoteMeta: an address is a literal here, whatever punctuation it
	// carries. The pattern is built from a stored address and cannot fail to
	// compile, but a refusal to redact must never be a refusal to log.
	pseudonym := s.accountPseudonym(email)
	whole, compileErr := regexp.Compile("(?i)" + regexp.QuoteMeta(email))
	if compileErr != nil {
		return withheld
	}
	// The whole address FIRST, so that where both could match no `@exemple.fr`
	// is left dangling behind a pseudonym.
	text := whole.ReplaceAllLiteralString(err.Error(), pseudonym)

	// Then the LOCAL PART on its own: a good many relays name the recipient
	// without its domain — `recipient "marie.dupont": user unknown` — and the
	// domain is the half that identifies nobody.
	//
	// It over-redacts when the local part is an ordinary word (`contact`,
	// `info`): the sentence loses a word an operator might have wanted. That
	// is the cheaper mistake — the other one puts a volunteer's name in a log
	// that is kept. Which is also why it must stay a WHOLE word: a local part
	// of `connect` would otherwise turn `connection reset` into nonsense.
	local, _, ok := strings.Cut(email, "@")
	if !ok || len(local) < 3 {
		return text
	}
	bare, compileErr := regexp.Compile("(?i)" + regexp.QuoteMeta(local))
	if compileErr != nil {
		return withheld
	}
	return replaceIsolated(text, bare, pseudonym)
}

const withheld = "the relay refused, and its answer is withheld: it could " +
	"not be cleared of the address it names"

// replaceIsolated replaces every match that stands as a WHOLE word.
//
// Written by hand because `\b` cannot express it: Go's is ASCII-only — `\w`
// is `[0-9A-Za-z_]` — so `\bhervé\b` has no boundary to find after the `é`
// and never matches. In a French campaign that is most of the volunteers,
// and the fragment the redaction exists for went into the log verbatim.
//
// Offsets come from the SAME string they index, which is the other half of
// this function's history: found in a lowercased copy, they addressed
// somewhere else entirely, because lowering changes byte length.
func replaceIsolated(text string, pattern *regexp.Regexp, with string) string {
	var out strings.Builder
	last := 0
	for _, loc := range pattern.FindAllStringIndex(text, -1) {
		if !isolated(text, loc[0], loc[1]) {
			continue
		}
		out.WriteString(text[last:loc[0]])
		out.WriteString(with)
		last = loc[1]
	}
	if last == 0 {
		return text
	}
	out.WriteString(text[last:])
	return out.String()
}

// isolated: neither side of [start,end) is a word rune. Letters and digits
// of EVERY alphabet count, which is the whole point.
func isolated(text string, start, end int) bool {
	if start > 0 {
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		if wordRune(before) {
			return false
		}
	}
	if end < len(text) {
		after, _ := utf8.DecodeRuneInString(text[end:])
		if wordRune(after) {
			return false
		}
	}
	return true
}

func wordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// logPseudonym derives the day-scoped pseudonym of one subject.
func (s *Server) logPseudonym(kind, subject string) string {
	day := s.now().UTC().Format("2006-01-02")
	m := hmac.New(sha256.New, s.logKey)
	m.Write([]byte(kind + "\x00" + day + "\x00" + subject))
	return hex.EncodeToString(m.Sum(nil))[:12]
}
