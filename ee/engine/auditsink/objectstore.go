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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
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
	creds     cloudauth.AWSCredentials
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
		s.client = airgap.Client(airgap.SiteAuditSink, objectTimeout)
	}
	if s.suffix == nil {
		s.suffix = randomSuffix
	}

	switch {
	case u.Scheme == "s3":
		s.kind = "s3"
		s.bucket = u.Host
		s.prefix = strings.Trim(u.Path, "/")
		s.region = firstNonEmpty(getenv(AWSRegionEnv), getenv(AWSDefaultRegionEnv), "us-east-1")
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
		s.region = firstNonEmpty(getenv(AWSRegionEnv), getenv(AWSDefaultRegionEnv), "us-east-1")
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
	s.creds = cloudauth.AWSCredentials{
		AccessKeyID:     getenv(AWSAccessKeyIDEnv),
		SecretAccessKey: getenv(AWSSecretAccessKeyEnv),
		SessionToken:    getenv(AWSSessionTokenEnv),
		Source:          "the environment",
	}
	if s.creds.AccessKeyID == "" || s.creds.SecretAccessKey == "" {
		return nil, fmt.Errorf(
			"an s3 audit sink signs its requests with AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, " +
				"and one of them is not set on this machine. They are read from the environment " +
				"rather than from configuration, because configuration is committed")
	}
	// The endpoint is in the label for a store that is not AWS, because the
	// bucket name alone does not say which server, and two self hosted stores
	// in one fleet are frequently the same bucket name on different hosts. No
	// credential can reach this string: the endpoint here is a scheme and a
	// host, and a query was never part of it.
	name := s.bucket
	if s.prefix != "" {
		name += "/" + s.prefix
	}
	if s.pathStyle {
		s.label = s.endpoint.String() + "/" + name
	} else {
		s.label = "s3://" + name
	}
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
// Through ee/engine/cloudauth rather than here, and the first version of this
// file got that wrong. It carried its own hundred lines of Signature Version 4,
// which compiled, passed a test that recomputed the signature independently,
// and was still a defect: a second implementation of a cloud's authentication
// inside the enterprise module. TestThreeCloudsAreSignedByOneImplementation
// exists for exactly that and named this file.
//
// The reason the gate is right and the duplicate was wrong is not tidiness.
// Two signers that agree today look exactly like one signer, right up until a
// fix lands in one of them, and a signing bug is invisible until a server
// refuses a request that a customer's audit archive needed.
func (s *ObjectStore) sign(req *http.Request, payload []byte) error {
	headers := map[string]string{}
	for name := range req.Header {
		headers[strings.ToLower(name)] = req.Header.Get(name)
	}

	signed, err := cloudauth.SignV4(cloudauth.SigV4Request{
		Method:  req.Method,
		URL:     req.URL.String(),
		Body:    payload,
		Headers: headers,
		Region:  s.region,
		// S3 rather than the host, because the service name is part of the
		// credential scope and a signature scoped to the wrong service is
		// refused with a message about the date.
		Service:     "s3",
		Credentials: s.creds,
		Now:         time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	for name, value := range signed {
		req.Header.Set(name, value)
	}
	return nil
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
