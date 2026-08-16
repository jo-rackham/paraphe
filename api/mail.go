package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/netip"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// Sending email.
//
// This service sends exactly two messages — a sign-in link and an invitation
// — through a relay the operator provides. It speaks SMTP because that is
// what every host already gives a campaign, and because `net/smtp` is in the
// standard library: no provider to sign up with, no dependency to vendor.
//
// UNCONFIGURED, `Server.mailer` is nil, and that IS the state "this instance
// sends nothing". The routes that would send answer 503 saying so, and
// /api/config tells the interface not to offer the button. A mailer that
// silently swallowed messages would leave a volunteer waiting for a link that
// was never going to come.

// Mailer sends one message. Only smtpMailer implements it in the binary; the
// interface is what lets the tests read what was sent instead of running a
// relay.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// Timeouts. A relay that stops answering must not hold a request — the
// invitation path sends synchronously, inside an authenticated write.
const (
	smtpDialTimeout = 10 * time.Second
	smtpIOTimeout   = 20 * time.Second
)

// setupMail builds the relay, or refuses to start.
//
// The three settings hold together or none of them works: a relay with no
// sender has nothing to put in From, and a relay with no public URL could
// only build its links out of the request's Host header — the one thing this
// feature must never do. Refused at startup, where an operator reads it,
// rather than at the first volunteer who asks for a link.
func setupMail(now func() time.Time) (Mailer, *url.URL, error) {
	relay := strings.TrimSpace(Get("smtp_url"))
	if relay == "" {
		// Said, not ignored: an operator who filled two settings out of three
		// believes sign-in by email is on, and nothing else would tell them
		// otherwise until a volunteer reported a button that refuses.
		if Get("mail_from") != "" || Get("public_url") != "" {
			slog.Warn("PARAPHE_MAIL_FROM or PARAPHE_PUBLIC_URL is set while " +
				"PARAPHE_SMTP_URL is not: this instance sends no email at all, " +
				"and signing in by link is off")
		}
		return nil, nil, nil
	}
	from := strings.TrimSpace(Get("mail_from"))
	if from == "" {
		return nil, nil, fmt.Errorf("PARAPHE_SMTP_URL is set and " +
			"PARAPHE_MAIL_FROM is not: every message needs a sender. Give the " +
			"address volunteers will see, for instance " +
			"`Campagne <contact@exemple.fr>`")
	}
	public := strings.TrimSpace(Get("public_url"))
	if public == "" {
		return nil, nil, fmt.Errorf("PARAPHE_SMTP_URL is set and " +
			"PARAPHE_PUBLIC_URL is not: the links in those emails are built " +
			"from it, and never from a request's Host header — which anyone " +
			"can set to their own server. Give the origin volunteers type in " +
			"their browser, for instance https://paraphe.org")
	}
	publicURL, err := parsePublicURL(public)
	if err != nil {
		return nil, nil, err
	}
	mailer, err := newSMTPMailer(relay, Get("smtp_password"), from, now)
	if err != nil {
		return nil, nil, err
	}
	slog.Info("signing in by email is on", "relay", mailer.addr,
		"sender", mailer.from, "links", publicURL.String())
	return mailer, publicURL, nil
}

// detach runs work outside the request that asked for it, bounded by a
// deadline the CALLER gives — the wait belongs to whoever knows what is
// being waited on, not to this helper.
//
// The sign-in link needs it: an SMTP exchange takes a few hundred
// milliseconds for an account that exists and zero for one that does not, so
// answering only once it finished would put back, as a stopwatch, exactly the
// existence signal the constant answer removes.
//
// Its own context, because r.Context() is cancelled the instant the response
// is written. Counted in s.outbound, so shutdown drains it rather than
// cutting a message in half.
func (s *Server) detach(within time.Duration, work func(context.Context)) {
	s.outbound.Add(1)
	go func() {
		defer s.outbound.Done()
		ctx, cancel := context.WithTimeout(context.Background(), within)
		defer cancel()
		work(ctx)
	}()
}

type smtpMailer struct {
	// addr: host:port, what the dialer connects to.
	addr string
	// host: the name alone — TLS verifies against it, and it is what
	// smtp.PlainAuth is told to expect.
	host string
	auth smtp.Auth
	// implicit: TLS from the first byte (smtps://, port 465) rather than
	// STARTTLS negotiated on a plain connection (smtp://, port 587).
	implicit bool
	// from: the rendered From header, and sender.Address the envelope.
	from   string
	sender mail.Address
	// now: the Date header's clock. I/O deadlines use the real one — a
	// fake clock must not be able to make a socket wait for ever.
	now func() time.Time
}

// newSMTPMailer reads the relay's URL. The password is DELIBERATELY a
// separate setting, like Valkey's: a URL travels in logs, in a `describe pod`
// and in a support paste, and a password inside it travels with it.
func newSMTPMailer(rawURL, password, from string, now func() time.Time) (*smtpMailer, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		// The value is NOT quoted back. Every message below quotes it, and
		// this one cannot: the parse failed, so nothing is known about what
		// it holds — including whether the operator put a password in it
		// against the instruction beside the setting. An unreadable value
		// prints as the setting's name, and the operator looks at it.
		return nil, fmt.Errorf("PARAPHE_SMTP_URL cannot be read as a URL: %s. "+
			"Expected smtp://user@host:587 or smtps://user@host:465",
			parseProblem(err))
	}
	// From here on the userinfo is dropped from anything printed, for the
	// same reason.
	shown := *u
	shown.User = nil
	rawURL = shown.String()
	implicit := false
	defaultPort := "587"
	switch u.Scheme {
	case "smtp":
	case "smtps":
		implicit, defaultPort = true, "465"
	default:
		return nil, fmt.Errorf("PARAPHE_SMTP_URL: scheme %q is not one this "+
			"service speaks. Use smtp://user@host:587 (STARTTLS) or "+
			"smtps://user@host:465 (TLS from the first byte)", u.Scheme)
	}
	host, port := u.Hostname(), u.Port()
	if host == "" {
		return nil, fmt.Errorf("PARAPHE_SMTP_URL = %q: no host in it", rawURL)
	}
	if port == "" {
		port = defaultPort
	}
	// A path or a query in an SMTP URL means the operator wrote a URL for
	// something else. Said rather than ignored: the connection would succeed
	// and the intent behind those characters would be lost in silence.
	if strings.Trim(u.Path, "/") != "" || u.RawQuery != "" {
		return nil, fmt.Errorf("PARAPHE_SMTP_URL = %q: an SMTP URL carries a "+
			"host and a port, nothing after them", rawURL)
	}

	sender, err := mail.ParseAddress(strings.TrimSpace(from))
	if err != nil {
		return nil, fmt.Errorf("PARAPHE_MAIL_FROM = %q: not an email address "+
			"(%w). Expected `contact@exemple.fr` or "+
			"`Campagne <contact@exemple.fr>`", from, err)
	}
	if err := safeAddress(sender.Address); err != nil {
		return nil, fmt.Errorf("PARAPHE_MAIL_FROM: %w", err)
	}

	m := &smtpMailer{
		addr: net.JoinHostPort(host, port), host: host, implicit: implicit,
		// re-rendered rather than passed through: String() quotes and encodes
		// the display name, so an operator's accent or comma cannot break the
		// header apart
		from: sender.String(), sender: *sender, now: now,
	}
	if _, inURL := u.User.Password(); inURL {
		// Refused, not used and not ignored. Ignored, the operator watches
		// authentication fail against a relay whose password they can see
		// right there in the setting; used, that password is in every log
		// line and every `describe pod` that ever prints the URL.
		return nil, fmt.Errorf("PARAPHE_SMTP_URL carries a password. Put it " +
			"in PARAPHE_SMTP_PASSWORD instead: a URL travels into logs, into " +
			"a `describe pod` and into support threads, and a password inside " +
			"it travels with it")
	}
	if user := u.User.Username(); user != "" {
		// The two settings hold together in BOTH directions. A user with no
		// password authenticates with an empty one: the relay answers 535 to
		// every message, and that refusal reaches an operator as a line in a
		// detached goroutine's log while volunteers keep asking for links
		// that will never arrive. Refused at startup instead.
		if password == "" {
			return nil, fmt.Errorf("PARAPHE_SMTP_URL names the user %q and "+
				"PARAPHE_SMTP_PASSWORD is empty: the relay would refuse every "+
				"message, and nobody would learn it from the screen. Give the "+
				"password, or drop the user from the URL if the relay asks "+
				"for none", user)
		}
		// PLAIN and nothing else. Go's PlainAuth refuses to hand credentials
		// to a connection that is not encrypted (localhost excepted), which
		// is the behaviour to want: a relay that offers no TLS gets no
		// password, loudly, instead of leaking one quietly.
		m.auth = smtp.PlainAuth("", user, password, host)
	} else if password != "" {
		return nil, fmt.Errorf("PARAPHE_SMTP_PASSWORD is set but " +
			"PARAPHE_SMTP_URL names no user: write smtp://user@host:587")
	}
	return m, nil
}

// parseProblem: what url.Parse complained about, WITHOUT the value it
// complained about. `url.Error` prints the whole URL, and the whole URL is
// what must not reach a log line here.
func parseProblem(err error) string {
	var e *url.Error
	if errors.As(err, &e) && e.Err != nil {
		return e.Err.Error()
	}
	return "it is not a valid URL"
}

func (m *smtpMailer) Send(ctx context.Context, to, subject, body string) error {
	if err := safeAddress(to); err != nil {
		return fmt.Errorf("recipient: %w", err)
	}
	id, err := messageID(m.sender.Address)
	if err != nil {
		return err
	}
	message := buildMessage(m.from, to, subject, body, id, m.now())

	conn, err := (&net.Dialer{Timeout: smtpDialTimeout}).DialContext(ctx, "tcp", m.addr)
	if err != nil {
		return fmt.Errorf("connecting to the SMTP relay %s: %w", m.addr, err)
	}
	defer conn.Close() //nolint:errcheck // Quit already closed it on success
	// Wall-clock, not the injected clock: this bounds a socket, and a test
	// moving its clock must not move a deadline the kernel enforces.
	deadline := time.Now().Add(smtpIOTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("bounding the SMTP connection: %w", err)
	}
	if m.implicit {
		conn = tls.Client(conn, &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12})
	}

	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return fmt.Errorf("SMTP handshake with %s: %w", m.addr, err)
	}
	defer c.Close() //nolint:errcheck // best effort behind Quit
	if !m.implicit {
		offered, _ := c.Extension("STARTTLS")
		// REQUIRED, not attempted. Opportunistic TLS is TLS an attacker
		// removes: strip STARTTLS from the relay's greeting and the message
		// goes out in the clear — and this message carries a sign-in link,
		// which is a credential with a fifteen-minute life. A relay that
		// does not offer it is refused, loudly, rather than served in
		// plaintext under a setting whose documentation says "STARTTLS".
		//
		// The loopback is the exception, because it is not a network: it is
		// what a sidecar relay and the end-to-end suite use, and it is the
		// same exception net/smtp's own PlainAuth makes for credentials.
		if !offered && !loopback(m.host) {
			return fmt.Errorf("the relay %s offers no STARTTLS: this message "+
				"carries a sign-in link, and it will not travel in the clear. "+
				"Use smtps:// (TLS from the first byte), or a relay that "+
				"offers STARTTLS", m.addr)
		}
		if offered {
			if err := c.StartTLS(&tls.Config{
				ServerName: m.host, MinVersion: tls.VersionTLS12}); err != nil {
				return fmt.Errorf("STARTTLS with %s: %w", m.addr, err)
			}
		}
	}
	if m.auth != nil {
		if err := c.Auth(m.auth); err != nil {
			return fmt.Errorf("SMTP authentication on %s: %w", m.addr, err)
		}
	}
	if err := c.Mail(m.sender.Address); err != nil {
		return fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("SMTP RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := w.Write([]byte(message)); err != nil {
		return fmt.Errorf("writing the message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("closing the message: %w", err)
	}
	return c.Quit()
}

// buildMessage renders one message, headers included.
//
// NOTHING a human typed reaches a header. The subject is a constant of this
// package, the recipient is an address already refused if it carries a
// control character, and the body — the only place a campaign's name appears
// — is base64 of UTF-8, an alphabet that cannot spell a header break. That
// is the whole answer to header injection: there is no untrusted text on the
// header side to inject into.
func buildMessage(from, to, subject, body, messageID string, now time.Time) string {
	headers := []string{
		"From: " + from,
		"To: " + to,
		// BEncoding, not the raw string: these subjects are French and
		// accented, and a bare 8-bit byte in a header is not a header.
		"Subject: " + mime.BEncoding.Encode("utf-8", subject),
		"Date: " + now.Format(time.RFC1123Z),
		"Message-ID: <" + messageID + ">",
		// RFC 3834. Without it an out-of-office answers a sign-in link, and
		// the answer lands on the campaign's own contact address.
		"Auto-Submitted: auto-generated",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		// base64 rather than 8bit: 8BITMIME is an extension, and a relay
		// without it rewrites or refuses the message. This one crosses
		// anything.
		"Content-Transfer-Encoding: base64",
	}
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + wrapBase64(body)
}

// wrapBase64: 76 characters per line, which is what RFC 2045 allows. A single
// long line is refused outright by some relays (RFC 5321 caps a line at 998).
func wrapBase64(body string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	const width = 76
	var b strings.Builder
	for i := 0; i < len(encoded); i += width {
		end := min(i+width, len(encoded))
		b.WriteString(encoded[i:end])
		b.WriteString("\r\n")
	}
	return b.String()
}

func messageID(sender string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("drawing a message identifier: %w", err)
	}
	domain := "localhost"
	if at := strings.LastIndex(sender, "@"); at >= 0 && at+1 < len(sender) {
		domain = sender[at+1:]
	}
	return hex.EncodeToString(raw) + "@" + domain, nil
}

// loopback: this relay is on this machine, so there is no network between
// us and no plaintext to intercept. The same exception net/smtp makes before
// it will hand over a password.
func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err == nil && addr.IsLoopback()
}

// safeAddress: what may be written into a header.
//
// normalizeEmail only lowercases and trims, so an address carrying CR LF
// reaches here intact — and an address is the one piece of a message that
// comes from the database. Refused explicitly rather than escaped: there is
// no legitimate address with a control character in it, and a refusal that
// says so is worth more than an encoding nobody re-reads.
func safeAddress(a string) error {
	if a == "" {
		return fmt.Errorf("the address is empty")
	}
	if !strings.Contains(a, "@") {
		return fmt.Errorf("%q is not an email address", a)
	}
	for _, r := range a {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("the address carries a control character (U+%04X), "+
				"which cannot go into a header", r)
		}
	}
	return nil
}

// --- where a link points ---------------------------------------------------

// parsePublicURL reads PARAPHE_PUBLIC_URL: the origin the links in these
// emails point at.
//
// It is a SETTING and not the request's Host header, and that is the whole
// point. On a single-campaign instance every Host resolves the bootstrap
// campaign, so a link built from the header would let anyone make the
// application send, to a volunteer's real address, a message signed by the
// campaign and pointing at a server of their choosing.
func parsePublicURL(raw string) (*url.URL, error) {
	refuse := func(why string) error {
		return fmt.Errorf("PARAPHE_PUBLIC_URL = %q: %s. Give the origin "+
			"volunteers type in their browser, for instance "+
			"https://paraphe.org", raw, why)
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, refuse(err.Error())
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, refuse("its scheme is neither http nor https")
	}
	if u.Hostname() == "" {
		return nil, refuse("it names no host")
	}
	if strings.Trim(u.Path, "/") != "" {
		return nil, refuse("it carries a path")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, refuse("it carries a query or a fragment")
	}
	if u.User != nil {
		return nil, refuse("it carries a userinfo")
	}
	// Multi-campaign: campaigns live on subdomains OF THIS DOMAIN, and the
	// slug is what gets prefixed below. A public URL naming anything else
	// produces links to a host that serves no campaign — a message that goes
	// out, arrives, and leads nowhere.
	if base := BaseDomain(); base != "" && u.Hostname() != base {
		return nil, refuse(fmt.Sprintf("this instance serves campaigns under "+
			"%q (PARAPHE_BASE_DOMAIN), so the public URL is that domain's apex, "+
			"not %q", base, u.Hostname()))
	}
	u.Path = ""
	return u, nil
}
