package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// The object store holding the campaign logos.
//
// S3 by protocol, so the deployment picks the implementation: the chart
// ships a three-node Garage cluster, and an operator on a cloud points the
// same settings at OVH, Scaleway or R2 and runs no storage at all.
//
// Signed here rather than with an SDK. The whole need is two calls — PUT and
// DELETE on an object of at most 64 KiB — and the usual client brings
// fourteen transitive modules into a project that has six direct ones, most
// of them for the multipart and high-throughput paths this will never take.
// SigV4 over a known payload is a specification, not a guess, and
// TestMediaStoreRoundTrip exercises it against a real Garage: a signature
// this gets wrong is a 403, loudly, not a subtle drift.

// sha256Empty: the payload hash of a request with no body. Written out
// because DELETE and HEAD need it on every call.
const sha256Empty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// mediaTimeout bounds a call to the store. The application answers a
// volunteer while it waits, so this is what stands between a wedged object
// store and a request hanging until the client gives up.
const mediaTimeout = 5 * time.Second

// MediaStore: nil when nothing is configured, which is the normal state of a
// developer's instance and of the test suite. Every caller therefore asks
// `if s.media == nil` and says the feature is unavailable, rather than
// failing on a nil endpoint deep inside a handler.
type MediaStore struct {
	endpoint  *url.URL
	bucket    string
	region    string
	accessKey string
	secretKey string
	// publicURL: the origin the BROWSER fetches from, which is not the
	// endpoint this writes to — the application talks to a cluster-internal
	// service, the browser to an ingress.
	publicURL string
	client    *http.Client
}

// NewMediaStore reads the settings. It returns (nil, nil) when the store is
// not configured, and an error when it is configured HALF WAY: an endpoint
// with no credentials is a deployment someone believes is working.
func NewMediaStore() (*MediaStore, error) {
	endpoint := strings.TrimSpace(Get("media_endpoint"))
	bucket := strings.TrimSpace(Get("media_bucket"))
	access := strings.TrimSpace(Get("media_access_key"))
	secret := strings.TrimSpace(Get("media_secret_key"))
	// NOT trimmed here: MediaOrigin is what judges this value, and it drops
	// the path itself. Trimming first made the two disagree — a value with
	// a space before its trailing slash passed the startup check and was
	// refused at request time, so the instance started, the probe stayed
	// green, and every page shipped a policy without the media origin.
	public := strings.TrimSpace(Get("media_public_url"))

	given := map[string]string{
		"PARAPHE_MEDIA_ENDPOINT":   endpoint,
		"PARAPHE_MEDIA_BUCKET":     bucket,
		"PARAPHE_MEDIA_ACCESS_KEY": access,
		"PARAPHE_MEDIA_SECRET_KEY": secret,
		"PARAPHE_MEDIA_PUBLIC_URL": public,
	}
	var missing []string
	for name, value := range given {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == len(given) {
		return nil, nil // not configured: no logo, and that is a state
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("the object store is half configured: %s "+
			"missing. Set all five, or none — a campaign that can upload a "+
			"logo nobody can fetch is worse than no logo at all",
			strings.Join(missing, ", "))
	}

	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("PARAPHE_MEDIA_ENDPOINT = %q: expected the S3 "+
			"API URL of the object store, scheme included — "+
			"http://garage:3900 for instance", endpoint)
	}
	if _, err := MediaOrigin(public); err != nil {
		return nil, err
	}
	return &MediaStore{
		endpoint:  parsed,
		bucket:    bucket,
		region:    strings.TrimSpace(Get("media_region")),
		accessKey: access,
		secretKey: secret,
		publicURL: public,
		client:    &http.Client{Timeout: mediaTimeout},
	}, nil
}

// MediaOrigin reads PARAPHE_MEDIA_PUBLIC_URL as what it actually becomes: a
// SOURCE inside a Content-Security-Policy. It returns the scheme and host —
// which is what a source names — or refuses.
//
// Checked as a CSP source and not merely as a URL, because those are not the
// same grammar. A policy separates its directives with `;` and its sources
// with whitespace, so a value carrying either does not name one origin: it
// APPENDS DIRECTIVES. `* ; script-src *` parses as a perfectly good URL —
// no error, empty host — and an earlier version forwarded it verbatim into
// the header, which started the process, passed every probe, and served
// every page with scripts allowed from anywhere.
//
// One function, used by the startup check AND by the middleware, so the two
// cannot disagree about what is allowed.
func MediaOrigin(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", nil
	}
	refuse := func(why string) error {
		return fmt.Errorf("PARAPHE_MEDIA_PUBLIC_URL = %q: %s. Give the origin "+
			"the browser fetches logos from, scheme included and nothing "+
			"else — https://media.paraphe.org for instance", raw, why)
	}
	// The characters that would end this source and start something else.
	if strings.ContainsAny(v, " \t\r\n;,'\"") {
		return "", refuse("it carries a character that would close the " +
			"Content-Security-Policy source and open another directive")
	}
	parsed, err := url.Parse(v)
	if err != nil {
		return "", refuse(err.Error())
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", refuse("it carries no http(s) scheme")
	}
	if parsed.Host == "" {
		return "", refuse("it names no host")
	}
	// The PATH is allowed — a bucket may live under one — but it is dropped
	// from the source: as a CSP source a path is a prefix match, so the same
	// setting written with and without a trailing slash would allow
	// different things.
	return parsed.Scheme + "://" + parsed.Host, nil
}

// URL: where the browser fetches this key from.
func (m *MediaStore) URL(key string) string {
	if m == nil || key == "" {
		return ""
	}
	return m.publicURL + "/" + key
}

// CheckBucket runs once at startup. A configured store that cannot be
// reached, or a bucket that does not exist, FAILS THE START: the alternative
// is an instance whose coordination discovers at upload time that the
// deployment was never finished, and a readiness probe calling that healthy.
func (m *MediaStore) CheckBucket(ctx context.Context) error {
	code, body, err := m.do(ctx, http.MethodHead, "", nil, nil)
	if err != nil {
		return fmt.Errorf("object store %s unreachable: %w", m.endpoint, err)
	}
	if code == http.StatusNotFound {
		return fmt.Errorf("bucket %q does not exist on %s: create it, or "+
			"correct PARAPHE_MEDIA_BUCKET", m.bucket, m.endpoint)
	}
	if code < 200 || code > 299 {
		return fmt.Errorf("bucket %q on %s answered %d: %s",
			m.bucket, m.endpoint, code, body)
	}
	return nil
}

// Put writes an object. `immutable` caching is set at write time because the
// key carries a digest of the content: a different image is a different key,
// so this one can be kept for ever and the header travels with the object
// rather than depending on the web server in front.
func (m *MediaStore) Put(ctx context.Context, key, contentType string,
	raw []byte) error {
	headers := map[string]string{
		"content-type":  contentType,
		"cache-control": "public, max-age=31536000, immutable",
	}
	// An SVG is a DOCUMENT, and a document opened at its own address runs
	// what it contains. `<img src>` ignores Content-Disposition and renders
	// the drawing exactly as before; a top-level navigation downloads the
	// file instead of executing it.
	//
	// This is the layer that closes the class. The validator refuses the
	// shapes it knows — and a single review pass found two it did not, an
	// `<?xml-stylesheet?>` pointing at an XSLT that emits HTML, and a SMIL
	// `<animate>` rewriting a link to `java<TAB>script:`. Enumerating what
	// an SVG may do is a losing game; refusing to render it as a page is
	// not.
	if contentType == "image/svg+xml" {
		headers["content-disposition"] = "attachment"
	}
	code, body, err := m.do(ctx, http.MethodPut, key, raw, headers)
	if err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	if code < 200 || code > 299 {
		return fmt.Errorf("writing %s: the object store answered %d: %s",
			key, code, body)
	}
	return nil
}

// Delete removes an object. S3 answers 204 for a key that was never there,
// which is what makes this safe to call on a pointer the database may have
// lost track of.
func (m *MediaStore) Delete(ctx context.Context, key string) error {
	code, body, err := m.do(ctx, http.MethodDelete, key, nil, nil)
	if err != nil {
		return fmt.Errorf("deleting %s: %w", key, err)
	}
	if code < 200 || (code > 299 && code != http.StatusNotFound) {
		return fmt.Errorf("deleting %s: the object store answered %d: %s",
			key, code, body)
	}
	return nil
}

// do signs and runs one request. `extra` holds headers that are sent AND
// signed: an unsigned header travels unprotected, and there is no reason
// here to send one.
func (m *MediaStore) do(ctx context.Context, method, key string, body []byte,
	extra map[string]string) (int, string, error) {
	path := "/" + m.bucket
	if key != "" {
		path += "/" + escapePath(key)
	}
	target := *m.endpoint
	target.Path = strings.TrimSuffix(target.Path, "/") + path

	req, err := http.NewRequestWithContext(ctx, method, target.String(),
		bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	// Explicit, because a nil body would otherwise be sent chunked and S3
	// signs what it counts.
	req.ContentLength = int64(len(body))

	payload := sha256Empty
	if len(body) > 0 {
		sum := sha256.Sum256(body)
		payload = hex.EncodeToString(sum[:])
	}
	now := time.Now().UTC()
	headers := map[string]string{
		"host":                 target.Host,
		"x-amz-content-sha256": payload,
		"x-amz-date":           now.Format("20060102T150405Z"),
	}
	for k, v := range extra {
		headers[strings.ToLower(k)] = v
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// Host travels in the request line, not in Header, and net/http drops it
	// from the map — it still has to be signed.
	req.Host = target.Host
	req.Header.Set("Authorization",
		m.authorization(method, target.EscapedPath(), headers, payload, now))

	resp, err := m.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only
	// bounded: an error body is a few hundred bytes of XML, and a store
	// answering megabytes of it must not become this process's problem
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode, strings.TrimSpace(string(detail)), nil
}

// authorization builds the AWS Signature Version 4 header.
func (m *MediaStore) authorization(method, path string,
	headers map[string]string, payload string, now time.Time) string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)

	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(strings.TrimSpace(headers[name]))
		canonicalHeaders.WriteString("\n")
	}
	signed := strings.Join(names, ";")

	// No query string is ever sent, hence the empty fourth line.
	canonical := strings.Join([]string{
		method, path, "", canonicalHeaders.String(), signed, payload,
	}, "\n")
	canonicalSum := sha256.Sum256([]byte(canonical))

	day := now.Format("20060102")
	scope := strings.Join([]string{day, m.region, "s3", "aws4_request"}, "/")
	toSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		now.Format("20060102T150405Z"),
		scope,
		hex.EncodeToString(canonicalSum[:]),
	}, "\n")

	key := hmacSHA256([]byte("AWS4"+m.secretKey), day)
	key = hmacSHA256(key, m.region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, toSign))

	return fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		m.accessKey, scope, signed, signature)
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// escapePath percent-encodes a key the way S3 canonicalises it: every
// character outside the unreserved set, with "/" left alone as the separator.
//
// url.PathEscape is not that — it leaves "+", "$", ":" and others alone,
// which S3 encodes, so the canonical request would differ from the one the
// server rebuilds and every signature would be rejected. The keys this
// writes are `logos/<slug>/<hex>.<ext>` and need no escaping at all; the
// function exists so that the day one does, it is not a signature bug.
func escapePath(key string) string {
	var out strings.Builder
	for _, b := range []byte(key) {
		switch {
		case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9',
			b == '-', b == '_', b == '.', b == '~', b == '/':
			out.WriteByte(b)
		default:
			fmt.Fprintf(&out, "%%%02X", b)
		}
	}
	return out.String()
}
