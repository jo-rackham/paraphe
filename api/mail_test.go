package main

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"strings"
	"testing"
	"time"
)

func fixedClock() func() time.Time {
	at := time.Date(2026, 3, 15, 9, 30, 0, 0, time.UTC)
	return func() time.Time { return at }
}

// An address is the one piece of a message that comes out of the database,
// and normalizeEmail only lowercases and trims. A CR LF in it would end the
// To header and start whatever follows — a Bcc, a second recipient, another
// body. Refused, and said.
func TestAnAddressCarryingAHeaderBreakIsRefused(t *testing.T) {
	for _, bad := range []string{
		"volontaire@exemple.fr\r\nBcc: ailleurs@exemple.fr",
		"volontaire@exemple.fr\nBcc: ailleurs@exemple.fr",
		"volontaire@exemple.fr\rX-Injected: 1",
		"volontaire\x00@exemple.fr",
	} {
		if err := safeAddress(bad); err == nil {
			t.Errorf("safeAddress accepted %q: this address would carry a "+
				"second header into the message", bad)
		}
	}
	if err := safeAddress("volontaire@exemple.fr"); err != nil {
		t.Errorf("a plain address was refused: %v", err)
	}
	if err := safeAddress("pas-une-adresse"); err == nil {
		t.Error("safeAddress accepted a string with no @ in it")
	}
}

// The relay refuses the recipient BEFORE opening a connection: the guard is
// the mailer's, not the caller's, so no call site can forget it.
func TestSendRefusesAPoisonedRecipientWithoutDialing(t *testing.T) {
	// port 1 on the loopback: a dial would fail instantly and loudly, which
	// is how this test tells "refused before dialing" from "tried and failed"
	m, err := newSMTPMailer("smtp://127.0.0.1:1", "", "Campagne <envoi@exemple.fr>",
		fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	err = m.Send(t.Context(), "cible@exemple.fr\r\nBcc: ailleurs@exemple.fr",
		"Sujet", "Corps")
	if err == nil {
		t.Fatal("a recipient carrying a header break was accepted")
	}
	if !strings.Contains(err.Error(), "recipient") {
		t.Errorf("the refusal does not name the recipient: %v", err)
	}
}

func TestTheMessageIsAWellFormedOne(t *testing.T) {
	body := "Bonjour,\n\nVoici votre lien : https://ma-campagne.paraphe.test/connexion#jeton=abc\n"
	raw := buildMessage("Campagne <envoi@exemple.fr>", "volontaire@exemple.fr",
		"Votre lien de connexion — à ouvrir", body,
		"deadbeef@exemple.fr", fixedClock()())

	head, encoded, found := strings.Cut(raw, "\r\n\r\n")
	if !found {
		t.Fatal("no blank line between the headers and the body")
	}
	headers := map[string]string{}
	for _, line := range strings.Split(head, "\r\n") {
		name, value, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("unreadable header line: %q", line)
		}
		headers[name] = value
	}
	for name, want := range map[string]string{
		"From":                      "Campagne <envoi@exemple.fr>",
		"To":                        "volontaire@exemple.fr",
		"MIME-Version":              "1.0",
		"Content-Type":              "text/plain; charset=utf-8",
		"Content-Transfer-Encoding": "base64",
		// RFC 3834: without it an out-of-office answers a sign-in link
		"Auto-Submitted": "auto-generated",
		"Message-ID":     "<deadbeef@exemple.fr>",
		"Date":           "Sun, 15 Mar 2026 09:30:00 +0000",
	} {
		if headers[name] != want {
			t.Errorf("%s = %q, expected %q", name, headers[name], want)
		}
	}

	// The subject is French and accented: a raw 8-bit byte in a header is not
	// a header, so it travels as an encoded word and comes back whole.
	subject, err := new(mime.WordDecoder).DecodeHeader(headers["Subject"])
	if err != nil {
		t.Fatalf("undecodable subject %q: %v", headers["Subject"], err)
	}
	if subject != "Votre lien de connexion — à ouvrir" {
		t.Errorf("subject decoded to %q", subject)
	}
	if strings.ContainsAny(headers["Subject"], "—à") {
		t.Errorf("the subject travels unencoded: %q", headers["Subject"])
	}

	for _, line := range strings.Split(strings.TrimRight(encoded, "\r\n"), "\r\n") {
		// RFC 5321 caps a line at 998 octets and some relays enforce it well
		// below; RFC 2045 asks for 76.
		if len(line) > 76 {
			t.Fatalf("a body line is %d characters long", len(line))
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(
		strings.ReplaceAll(encoded, "\r\n", ""))
	if err != nil {
		t.Fatalf("the body is not readable base64: %v", err)
	}
	if string(decoded) != body {
		t.Errorf("the body came back as %q", decoded)
	}
}

// Opportunistic TLS is TLS an attacker removes: strip STARTTLS from the
// greeting and the message goes out in the clear, carrying a credential with
// a fifteen-minute life. The setting's documentation says "STARTTLS", and
// this is what makes that true.
func TestARelayWithoutSTARTTLSIsRefused(t *testing.T) {
	relay := fakeRelay(t, false)
	m, err := newSMTPMailer("smtp://"+relay, "", "envoi@exemple.fr", fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	// the fake relay answers on 127.0.0.1; the host is what decides, so name
	// it something that is not the loopback
	m.host = "relais.exemple.fr"
	err = m.Send(t.Context(), "volontaire@exemple.fr", "Sujet", "Corps")
	if err == nil {
		t.Fatal("a relay offering no STARTTLS was used anyway, in the clear")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("the refusal does not name STARTTLS: %v", err)
	}
}

// The loopback is not a network: a sidecar relay and the end-to-end suite
// both live there, and it is the same exception net/smtp makes before it
// hands over a password.
func TestOnTheLoopbackAPlainRelayIsAccepted(t *testing.T) {
	relay := fakeRelay(t, false)
	m, err := newSMTPMailer("smtp://"+relay, "", "envoi@exemple.fr", fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Send(t.Context(), "volontaire@exemple.fr", "Sujet", "Corps"); err != nil {
		t.Fatalf("a loopback relay was refused: %v", err)
	}
}

// fakeRelay: enough SMTP to be talked to, and no more. It exists to answer
// the one question these two tests ask — what does the client do when the
// greeting does or does not offer STARTTLS.
func fakeRelay(t *testing.T, offerSTARTTLS bool) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				greeting := "250-fake\r\n250 SIZE 10485760\r\n"
				if offerSTARTTLS {
					greeting = "250-fake\r\n250-STARTTLS\r\n250 SIZE 10485760\r\n"
				}
				fmt.Fprint(conn, "220 fake ready\r\n")
				in := bufio.NewScanner(conn)
				for in.Scan() {
					switch strings.ToUpper(in.Text()[:min(4, len(in.Text()))]) {
					case "EHLO", "HELO":
						fmt.Fprint(conn, greeting)
					case "DATA":
						fmt.Fprint(conn, "354 go ahead\r\n")
						for in.Scan() && in.Text() != "." {
						}
						fmt.Fprint(conn, "250 taken\r\n")
					case "QUIT":
						fmt.Fprint(conn, "221 bye\r\n")
						return
					default:
						fmt.Fprint(conn, "250 ok\r\n")
					}
				}
			}()
		}
	}()
	return listener.Addr().String()
}

func TestTheRelayURLIsCheckedBeforeAnythingStarts(t *testing.T) {
	const from = "Campagne <envoi@exemple.fr>"
	for _, c := range []struct {
		name, url, password, from string
	}{
		{"a scheme this service does not speak", "https://relais.exemple.fr", "", from},
		{"no host at all", "smtp://", "", from},
		{"a path after the port", "smtp://relais.exemple.fr:587/envoi", "", from},
		{"a sender that is not an address", "smtp://relais.exemple.fr", "", "Campagne"},
		{"a password with no user to carry it", "smtp://relais.exemple.fr", "secret", from},
		// …and the other way round, which was accepted in silence: the mailer
		// authenticated with an empty password, the relay answered 535 to
		// every message, and that refusal only ever reached a detached
		// goroutine's log while volunteers waited on an inbox.
		{"a user with no password", "smtp://envoi@relais.exemple.fr:587", "", from},
	} {
		if _, err := newSMTPMailer(c.url, c.password, c.from, fixedClock()); err == nil {
			t.Errorf("%s was accepted (%q)", c.name, c.url)
		}
	}

	m, err := newSMTPMailer("smtp://envoi%40exemple.fr@relais.exemple.fr", "secret",
		from, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	// STARTTLS submission, the port an operator does not have to think about
	if m.addr != "relais.exemple.fr:587" || m.implicit {
		t.Errorf("smtp:// resolved to %q (implicit TLS: %v)", m.addr, m.implicit)
	}
	if m.auth == nil {
		t.Error("a user was given and no authentication was set up")
	}
	implicit, err := newSMTPMailer("smtps://relais.exemple.fr", "", from, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	if implicit.addr != "relais.exemple.fr:465" || !implicit.implicit {
		t.Errorf("smtps:// resolved to %q (implicit TLS: %v)",
			implicit.addr, implicit.implicit)
	}
	if implicit.auth != nil {
		t.Error("no user was given and authentication was set up anyway")
	}
}

// A password inside the URL is refused, not used and not ignored. Ignored,
// the operator watches authentication fail against a password they can see
// in the setting; used, it is in every log line that ever prints that URL.
func TestAPasswordInsideTheRelayURLIsRefused(t *testing.T) {
	_, err := newSMTPMailer("smtp://envoi:secret@relais.exemple.fr:587", "",
		"envoi@exemple.fr", fixedClock())
	if err == nil {
		t.Fatal("a URL carrying a password was accepted")
	}
	if !strings.Contains(err.Error(), "PARAPHE_SMTP_PASSWORD") {
		t.Errorf("the refusal does not say where the password belongs: %v", err)
	}
	// and it does not repeat the password back into the message an operator
	// pastes into a support thread
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("the refusal quotes the password: %v", err)
	}
}

// A value that cannot even be parsed is not quoted back: nothing is known
// about what it holds, including whether it holds a password.
func TestAnUnreadableRelayURLIsNotQuotedBack(t *testing.T) {
	_, err := newSMTPMailer("smtp://envoi:tr\x7fs-secret@relais\x00.exemple.fr", "",
		"envoi@exemple.fr", fixedClock())
	if err == nil {
		t.Fatal("an unparseable URL was accepted")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("the refusal repeats the value it could not read: %v", err)
	}
	if !strings.Contains(err.Error(), "PARAPHE_SMTP_URL") {
		t.Errorf("the refusal does not name the setting: %v", err)
	}
}

// The sender is parsed and RE-RENDERED rather than passed through, and that
// does real work: an accented display name comes out as an encoded word, and
// an unquoted comma — which a header reads as a second address — is refused
// at startup instead of splitting From in two.
func TestTheSenderIsRenderedNotPassedThrough(t *testing.T) {
	if _, err := newSMTPMailer("smtp://relais.exemple.fr", "",
		"Équipe, campagne <envoi@exemple.fr>", fixedClock()); err == nil {
		t.Error("a display name carrying an unquoted comma was accepted: a " +
			"header reads it as two addresses")
	}
	m, err := newSMTPMailer("smtp://relais.exemple.fr", "",
		"Équipe de campagne <envoi@exemple.fr>", fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.from, "Équipe") {
		t.Errorf("From travels with a raw 8-bit display name: %q", m.from)
	}
	if !strings.Contains(m.from, "=?utf-8?") ||
		!strings.HasSuffix(m.from, "<envoi@exemple.fr>") {
		t.Errorf("From came out as %q", m.from)
	}
	if m.sender.Address != "envoi@exemple.fr" {
		t.Errorf("the envelope sender is %q", m.sender.Address)
	}
}

func TestThePublicURLIsCheckedBeforeAnythingStarts(t *testing.T) {
	for _, c := range []struct{ name, url string }{
		{"no scheme", "paraphe.test"},
		{"a scheme that is not http", "ftp://paraphe.test"},
		{"a path", "https://paraphe.test/campagne"},
		{"a fragment", "https://paraphe.test/#jeton"},
		{"a userinfo", "https://qui@paraphe.test"},
	} {
		if _, err := parsePublicURL(c.url); err == nil {
			t.Errorf("%s was accepted (%q)", c.name, c.url)
		}
	}
	u, err := parsePublicURL("https://paraphe.test/")
	if err != nil {
		t.Fatal(err)
	}
	if u.String() != "https://paraphe.test" {
		t.Errorf("the origin came back as %q", u)
	}
}

// A public URL naming another domain than the one campaigns are served under
// produces links to a host that answers for no campaign: a message that goes
// out, arrives, and leads nowhere.
func TestThePublicURLMustBeTheDomainCampaignsLiveUnder(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	if _, err := parsePublicURL("https://ailleurs.test"); err == nil {
		t.Fatal("a public URL outside the base domain was accepted")
	}
	if _, err := parsePublicURL("https://paraphe.test"); err != nil {
		t.Fatalf("the apex of the base domain was refused: %v", err)
	}
}

func TestACampaignURLIsTheApexWithItsSlugInFront(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "paraphe.test")
	public, err := parsePublicURL("http://paraphe.test:8047")
	if err != nil {
		t.Fatal(err)
	}
	// the port and the scheme are kept: that is what makes a local instance
	// on http://paraphe.test:8047 send links a browser can open
	if got := campaignURL(public, "ma-campagne").String(); got !=
		"http://ma-campagne.paraphe.test:8047" {
		t.Errorf("a campaign's origin came back as %q", got)
	}
	// the apex has no slug, and is served as it stands
	if got := campaignURL(public, "").String(); got != "http://paraphe.test:8047" {
		t.Errorf("the apex's origin came back as %q", got)
	}
}

// Single-campaign, there is no subdomain to prefix: the configured origin is
// the answer, slug or no slug.
func TestSingleCampaignLinksPointAtTheConfiguredOrigin(t *testing.T) {
	t.Setenv("PARAPHE_BASE_DOMAIN", "")
	public, err := parsePublicURL("https://campagne.exemple.fr")
	if err != nil {
		t.Fatal(err)
	}
	if got := campaignURL(public, "campagne").String(); got !=
		"https://campagne.exemple.fr" {
		t.Errorf("the origin came back as %q", got)
	}
}

// The three settings hold together or none of them works. Refused at
// startup, where an operator reads it — not at the first volunteer who asks
// for a link.
func TestAHalfConfiguredRelayRefusesToStart(t *testing.T) {
	t.Setenv("PARAPHE_SMTP_URL", "smtp://relais.exemple.fr:587")
	t.Setenv("PARAPHE_MAIL_FROM", "")
	t.Setenv("PARAPHE_PUBLIC_URL", "https://paraphe.test")
	if _, _, err := setupMail(fixedClock()); err == nil {
		t.Error("a relay with no sender started")
	}
	t.Setenv("PARAPHE_MAIL_FROM", "envoi@exemple.fr")
	t.Setenv("PARAPHE_PUBLIC_URL", "")
	if _, _, err := setupMail(fixedClock()); err == nil {
		t.Error("a relay with no public URL started: its links could then " +
			"only come from the request's Host header")
	}
	t.Setenv("PARAPHE_PUBLIC_URL", "https://paraphe.test")
	mailer, public, err := setupMail(fixedClock())
	if err != nil {
		t.Fatalf("a complete configuration was refused: %v", err)
	}
	if mailer == nil || public == nil {
		t.Fatal("a complete configuration produced no mailer")
	}
}

// No relay is a STATE, not a failure: the instance starts, and says so
// everywhere it matters.
func TestNoRelayIsAStateAndNotAnError(t *testing.T) {
	t.Setenv("PARAPHE_SMTP_URL", "")
	t.Setenv("PARAPHE_MAIL_FROM", "")
	t.Setenv("PARAPHE_PUBLIC_URL", "")
	mailer, public, err := setupMail(fixedClock())
	if err != nil {
		t.Fatalf("an instance without a relay refused to start: %v", err)
	}
	if mailer != nil || public != nil {
		t.Fatal("a mailer was built with no relay configured")
	}
}
