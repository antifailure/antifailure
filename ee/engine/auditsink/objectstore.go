package auditsink

// A drop into an object store, in the same two shapes the goldens already use.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// This is the sink that answers a retention requirement rather than an alerting
// one. A SIEM keeps ninety days because a SIEM is priced per gigabyte ingested;
// a seven year obligation is satisfied by objects in a bucket with a lifecycle
// policy and object lock on it, which is a thing a security team already has and
// already audits. Both of the other sinks here are for looking at entries now.
// This one is for still having them in 2033.
//
// # Why it re-implements the protocol rather than importing the engine's
//
// engine/internal/golden already speaks both of these, correctly and with tests
// against real servers, and it cannot be imported: engine/internal is
// unimportable from outside engine/... by construction, and that wall is what
// makes the enterprise edition a separate module rather than a build tag
// somebody can flip. The signing below is the same algorithm and is deliberately
// written in the same shape as store_s3.go so the two read as the same thing,
// because the failure to avoid here is two implementations that drift.
//
// # One object per entry
//
// Not a batch and not an append, and the reason is the same one that makes
// object stores usable for this at all: an object is written once and can then
// be locked. An appended file has to be read, extended and rewritten, which is
// a race between two `af down` commands and is not lockable, so the retention
// control an auditor asks about would not hold. The key carries the date as a
// path so a lifecycle rule and a partitioned query both work on it without
// anybody parsing a filename.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// objectTimeout bounds one upload.
//
// Thirty seconds rather than the five the other sinks use, because this is one
// small PUT to a store that is frequently in another region and because nothing
// downstream is waiting on it: the SIEM sinks are what somebody is watching,
// and this one is the archive.
const objectTimeout = 30 * time.Second

// azureAPIVersion is pinned, for the reason the golden store pins it: the
// service keys behaviour off it, so a version that floats is a client whose
// behaviour changes without a commit.
const azureAPIVersion = "2021-08-06"

// ObjectStoreConfig is what an object store drop needs.
type ObjectStoreConfig struct {
	// URL is s3://bucket/prefix, the https URL of a server that speaks the same
	// API as https://host/bucket/prefix, or an Azure Blob CONTAINER URL
	// carrying a shared access signature.
	URL string
	// Getenv supplies the AWS credentials, by the names the AWS tools already
	// use, so that a machine set up for the AWS CLI needs nothing else. Nil
	// means the process environment.
	Getenv func(string) string
	// Client is injected for tests. Nil means one with the timeout above.
	Client *http.Client
	// Now is injected for tests. Nil means the wall clock.
	Now func() time.Time
	// suffix makes the key unique within a nanosecond. Injected only by tests,
	// which need a key they can predict; in production it is random.
	suffix func() string
}

// ObjectStore writes one object per audit entry.
type ObjectStore struct {
	kind   string
	client *http.Client
	now    func() time.Time
	suffix func() string
	label  string

	// S3.
	endpoint  *url.URL
	bucket    string
	prefix    string
	region    string
	accessKey string
	secretKey string
	session   string
	pathStyle bool

	// Azure Blob.
	base  *url.URL
	query string
}

// NewObjectStore builds an object store sink, or reports what it is missing.
func NewObjectStore(cfg ObjectStoreConfig) (*ObjectStore, error) {
	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	raw := strings.TrimSpace(cfg.URL)
	if raw == "" {
		return nil, fmt.Errorf("an object store sink needs a URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%q is not a usable object store URL: %w", redact(raw), err)
	}

	s := &ObjectStore{
		client: cfg.Client,
		now:    clockOf(cfg.Now),
		suffix: cfg.suffix,
	}
	if s.client == nil {
		s.client = &http.Client{Timeout: objectTimeout}
	}
	if s.suffix == nil {
		s.suffix = randomSuffix
	}

	switch {
	case u.Scheme == "s3":
		s.kind = "s3"
		s.bucket = u.Host
		s.prefix = strings.Trim(u.Path, "/")
		s.region = firstNonEmpty(getenv("AWS_REGION"), getenv("AWS_DEFAULT_REGION"), "us-east-1")
		s.endpoint = &url.URL{Scheme: "https", Host: "s3." + s.region + ".amazonaws.com"}
	case isAzureBlob(u):
		s.kind = "azure_blob"
		if u.RawQuery == "" {
			return nil, fmt.Errorf(
				"%s carries no shared access signature, and this sink authenticates with one. "+
					"Generate a container scoped signature with create and write, and put the "+
					"whole URL in the variable", redact(raw))
		}
		s.query = u.RawQuery
		u.RawQuery = ""
		u.Path = "/" + strings.Trim(u.Path, "/")
		s.base = u
		s.label = redact(raw)
		return s, nil
	case u.Scheme == "http" || u.Scheme == "https":
		// A full URL that is not Azure points at a server speaking the S3 API,
		// and the first path segment is the bucket. Path style, because that is
		// what those servers serve and because an endpoint given as an address
		// cannot take a bucket prefix.
		s.kind = "s3"
		parts := strings.SplitN(strings.Trim(u.Path, "/"), "/", 2)
		if parts[0] == "" {
			return nil, fmt.Errorf(
				"%s names no bucket. For a server that is not AWS the URL is "+
					"https://<host>/<bucket>", redact(raw))
		}
		s.bucket = parts[0]
		if len(parts) > 1 {
			s.prefix = strings.Trim(parts[1], "/")
		}
		s.region = firstNonEmpty(getenv("AWS_REGION"), getenv("AWS_DEFAULT_REGION"), "us-east-1")
		s.endpoint = &url.URL{Scheme: u.Scheme, Host: u.Host}
		s.pathStyle = true
	default:
		return nil, fmt.Errorf(
			"an object store sink URL is s3://<bucket>/<prefix>, the https URL of a server "+
				"speaking the same API, or an Azure Blob container URL, and this one is %q", u.Scheme)
	}

	if s.bucket == "" {
		return nil, fmt.Errorf("%s names no bucket", redact(raw))
	}
	s.accessKey = getenv("AWS_ACCESS_KEY_ID")
	s.secretKey = getenv("AWS_SECRET_ACCESS_KEY")
	s.session = getenv("AWS_SESSION_TOKEN")
	if s.accessKey == "" || s.secretKey == "" {
		return nil, fmt.Errorf(
			"an s3 audit sink signs its requests with AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, " +
				"and one of them is not set on this machine. They are read from the environment " +
				"rather than from configuration, because configuration is committed")
	}
	s.label = "s3://" + s.bucket + "/" + s.prefix
	return s, nil
}

// Name identifies the sink.
func (s *ObjectStore) Name() string {
	if s.kind == "azure_blob" {
		return "the Azure Blob container " + s.label
	}
	return "the bucket " + s.label
}

// Write drops one entry as one object.
func (s *ObjectStore) Write(ctx context.Context, entry extension.AuditEntry) error {
	if !permitted(ctx) {
		return nil
	}
	now := s.now()
	body, err := encode(entry, now)
	if err != nil {
		return err
	}

	putCtx, cancel := context.WithTimeout(ctx, objectTimeout)
	defer cancel()

	key := s.key(entry, now)
	if s.kind == "azure_blob" {
		err = s.putAzure(putCtx, key, body)
	} else {
		err = s.putS3(putCtx, key, body)
	}
	if err != nil {
		return fmt.Errorf("dropping %s into %s: %w", key, s.Name(), err)
	}
	return nil
}

// key is where one entry lands.
//
// The date is a path rather than part of the filename so a bucket lifecycle
// rule, an object lock policy and a partitioned query all work on it without
// anybody writing a parser. The action is in the name because the most common
// question asked of an archive like this is "show me every golden.published",
// and a prefix listing answers it without reading a single object.
//
// The random suffix is what makes two entries in the same nanosecond two
// objects. Without it the second silently replaces the first, which is an audit
// log that loses exactly the entries that arrived together, which is exactly the
// entries somebody is investigating.
func (s *ObjectStore) key(entry extension.AuditEntry, now time.Time) string {
	stamp := entry.OccurredAt
	if stamp.IsZero() {
		stamp = now
	}
	stamp = stamp.UTC()
	name := fmt.Sprintf("%s/%s-%s-%s.json",
		stamp.Format("2006/01/02"),
		stamp.Format("150405.000000000"),
		safeSegment(entry.Action),
		s.suffix(),
	)
	if s.prefix == "" {
		return name
	}
	return s.prefix + "/" + name
}

// putAzure writes one blob through the container's shared access signature.
func (s *ObjectStore) putAzure(ctx context.Context, key string, body []byte) error {
	u := *s.base
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + key
	u.RawQuery = s.query

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	req.Header.Set("x-ms-version", azureAPIVersion)
	req.Header.Set("Content-Type", "application/json")
	// Refuse to replace an object that is already there. Two entries that
	// collide is a defect in the key rather than a thing to resolve by losing
	// one, and an audit archive whose writer overwrites is an audit archive
	// somebody can edit by replaying.
	req.Header.Set("If-None-Match", "*")

	return s.do(req)
}

// putS3 writes one object with a Signature Version 4 signed PUT.
func (s *ObjectStore) putS3(ctx context.Context, key string, body []byte) error {
	u := *s.endpoint
	if s.pathStyle {
		u.Path = "/" + s.bucket + "/" + key
	} else {
		u.Host = s.bucket + "." + s.endpoint.Host
		u.Path = "/" + key
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	// The same refusal to replace as the Azure half, spelled the way S3 spells
	// it. A server that does not implement it answers 501 rather than silently
	// overwriting, which is a refusal an operator can read.
	req.Header.Set("If-None-Match", "*")
	if err := s.sign(req, body); err != nil {
		return err
	}
	return s.do(req)
}

// do performs a request and turns a non 2xx into an error without the body.
//
// Without the body, deliberately. A store's error document can echo the request
// and the request is the audit entry, so quoting it here would print the entry
// into a terminal and into a CI log.
func (s *ObjectStore) do(req *http.Request) error {
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("the store answered %s", resp.Status)
	}
	return nil
}

// sign applies Signature Version 4 to a request.
//
// The same algorithm and the same shape as engine/internal/golden's, which is
// exercised against a real server rather than a fixture. Four parts of it are
// where implementations go wrong and all four are here on purpose: the payload
// hash is a real SHA-256 of the body rather than UNSIGNED-PAYLOAD, the signed
// header set includes host and every x-amz-, the path is escaped per segment
// with the separators left alone, and the credential scope pins the date, the
// region and the service so a signature cannot be replayed elsewhere tomorrow.
func (s *ObjectStore) sign(req *http.Request, payload []byte) error {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateOnly := now.Format("20060102")

	sum := sha256.Sum256(payload)
	payloadHash := hex.EncodeToString(sum[:])

	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	if s.session != "" {
		req.Header.Set("x-amz-security-token", s.session)
	}

	signed, canonical := signedHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalPath(req.URL),
		canonicalQuery(req.URL),
		canonical,
		signed,
		payloadHash,
	}, "\n")

	crSum := sha256.Sum256([]byte(canonicalRequest))
	scope := strings.Join([]string{dateOnly, s.region, "s3", "aws4_request"}, "/")
	toSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", amzDate, scope, hex.EncodeToString(crSum[:]),
	}, "\n")

	key := hmacSHA256([]byte("AWS4"+s.secretKey), dateOnly)
	key = hmacSHA256(key, s.region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, toSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.accessKey, scope, signed, signature))
	return nil
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// signedHeaders returns the signed header list and the canonical block.
func signedHeaders(req *http.Request) (string, string) {
	names := []string{"host"}
	values := map[string]string{"host": req.Host}
	for name, vs := range req.Header {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, "x-amz-") && lower != "content-type" {
			continue
		}
		names = append(names, lower)
		collapsed := make([]string, len(vs))
		for i, v := range vs {
			collapsed[i] = strings.Join(strings.Fields(v), " ")
		}
		values[lower] = strings.Join(collapsed, ",")
	}
	sort.Strings(names)

	var block strings.Builder
	for _, n := range names {
		block.WriteString(n)
		block.WriteString(":")
		block.WriteString(values[n])
		block.WriteString("\n")
	}
	return strings.Join(names, ";"), block.String()
}

// canonicalPath escapes each path segment, leaving the separators alone.
func canonicalPath(u *url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		return "/"
	}
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		raw, err := url.PathUnescape(seg)
		if err != nil {
			raw = seg
		}
		segments[i] = awsEscape(raw)
	}
	return strings.Join(segments, "/")
}

// canonicalQuery sorts and escapes the query with AWS's rules.
func canonicalQuery(u *url.URL) string {
	values := u.Query()
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), values[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, awsEscape(k)+"="+awsEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// awsEscape percent encodes everything outside the unreserved set.
//
// Not url.QueryEscape, which writes a space as + and leaves alone some
// characters this has to encode. The difference is invisible until a key with a
// space in it fails to sign.
func awsEscape(s string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if strings.IndexByte(unreserved, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// isAzureBlob reports whether a URL addresses Azure Blob storage.
//
// The host suffix rather than a scheme, because an Azure container URL and a
// self hosted S3 URL are both https and there is otherwise no way to tell them
// apart. Azurite, which is what a test uses, serves on 127.0.0.1, so the query
// signature is the second signal: an S3 URL here never carries one.
func isAzureBlob(u *url.URL) bool {
	if u.Scheme != "https" && u.Scheme != "http" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return strings.HasSuffix(host, ".blob.core.windows.net") ||
		strings.HasSuffix(host, ".blob.core.chinacloudapi.cn") ||
		strings.HasSuffix(host, ".blob.core.usgovcloudapi.net")
}

// safeSegment reduces a value to something that is a legal object key part.
func safeSegment(v string) string {
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "entry"
	}
	return b.String()
}

// randomSuffix is eight hex characters from crypto/rand.
//
// crypto/rand rather than math/rand, and it is not paranoia: two `af down`
// commands in the same second on two runners is ordinary, and a seeded
// pseudo-random generator on two containers started from the same image at the
// same instant collides more often than anybody expects.
func randomSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Unreachable on every platform this builds for, and if it ever is
		// reachable a nanosecond is still a better key than a fixed string.
		return strconv.FormatInt(time.Now().UnixNano()%0xffffffff, 16)
	}
	return hex.EncodeToString(b[:])
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// redact removes a query string from a URL before it is printed.
//
// Both of the shapes this sink takes can carry a credential in the query: an
// Azure shared access signature is one, and a presigned S3 URL is the other.
// Every message in this file that names the destination goes through here.
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "the configured object store"
	}
	u.RawQuery = ""
	u.User = nil
	return u.String()
}
